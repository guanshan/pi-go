package tui

import (
	"strings"
	"sync"
	"time"
)

// StdinBuffer accumulates raw stdin bytes and emits complete escape sequences
// (or single printable runes) via OnData. Bracketed paste payloads are
// extracted and forwarded to OnPaste instead.
//
// Mirrors @earendil-works/pi-tui's stdin-buffer.ts.
type StdinBuffer struct {
	OnData  func(string)
	OnPaste func(string)

	// Timeout flushes incomplete sequences after this delay (default 10ms).
	Timeout time.Duration

	mu                             sync.Mutex
	buffer                         string
	timer                          *time.Timer
	pasteMode                      bool
	pasteBuffer                    string
	pendingKittyPrintableCodepoint int
}

const (
	stdinDefaultTimeout = 10 * time.Millisecond
	bracketedPasteStart = "\x1b[200~"
	bracketedPasteEnd   = "\x1b[201~"
)

// Process feeds a chunk of bytes (typically what was just read from stdin)
// into the buffer.
func (s *StdinBuffer) Process(data string) {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.buffer += data

	// Flush whatever can be emitted right now.
	emitData, emitPaste, scheduleFlush := s.drainLocked()
	s.mu.Unlock()

	for _, p := range emitPaste {
		if s.OnPaste != nil {
			s.OnPaste(p)
		}
	}
	for _, d := range emitData {
		s.emitData(d)
	}

	if scheduleFlush {
		s.scheduleFlush()
	}
}

// Clear drops any buffered state (does NOT call OnData/OnPaste).
func (s *StdinBuffer) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.buffer = ""
	s.pasteMode = false
	s.pasteBuffer = ""
	s.pendingKittyPrintableCodepoint = 0
}

// Flush forces emission of whatever is currently buffered as a single
// sequence, then resets buffers. Useful for tests / shutdown.
func (s *StdinBuffer) Flush() {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	leftover := s.buffer
	s.buffer = ""
	s.pendingKittyPrintableCodepoint = 0
	s.mu.Unlock()
	if leftover != "" {
		s.emitData(leftover)
	}
}

// Destroy releases resources (timer).
func (s *StdinBuffer) Destroy() { s.Clear() }

func (s *StdinBuffer) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return stdinDefaultTimeout
}

func (s *StdinBuffer) scheduleFlush() {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.timeout(), func() {
		s.Flush()
	})
	s.mu.Unlock()
}

// drainLocked extracts whatever it can from s.buffer / pasteBuffer.
// Caller must hold s.mu. Returns the slices to emit AFTER releasing the lock,
// plus a flag indicating that a flush timer should be scheduled.
func (s *StdinBuffer) drainLocked() (data []string, paste []string, schedule bool) {
	for {
		if s.pasteMode {
			s.pasteBuffer += s.buffer
			s.buffer = ""
			end := strings.Index(s.pasteBuffer, bracketedPasteEnd)
			if end < 0 {
				return data, paste, false
			}
			content := s.pasteBuffer[:end]
			remaining := s.pasteBuffer[end+len(bracketedPasteEnd):]
			s.pasteMode = false
			s.pasteBuffer = ""
			s.pendingKittyPrintableCodepoint = 0
			paste = append(paste, content)
			s.buffer = remaining
			continue
		}
		if start := strings.Index(s.buffer, bracketedPasteStart); start >= 0 {
			before := s.buffer[:start]
			rest := s.buffer[start+len(bracketedPasteStart):]
			if before != "" {
				ds, leftover := extractCompleteSequences(before)
				data = append(data, ds...)
				if leftover != "" {
					// Partial sequence at the start; preserve it.
					s.buffer = leftover + s.buffer[len(before):]
					return data, paste, leftover != ""
				}
			}
			s.pendingKittyPrintableCodepoint = 0
			s.pasteMode = true
			s.pasteBuffer = ""
			s.buffer = rest
			continue
		}
		ds, leftover := extractCompleteSequences(s.buffer)
		data = append(data, ds...)
		s.buffer = leftover
		schedule = leftover != ""
		return data, paste, schedule
	}
}

func (s *StdinBuffer) emitData(seq string) {
	s.mu.Lock()
	pending := s.pendingKittyPrintableCodepoint
	if len(seq) > 0 && len([]rune(seq)) == 1 {
		r := []rune(seq)[0]
		if pending != 0 && int(r) == pending {
			s.pendingKittyPrintableCodepoint = 0
			s.mu.Unlock()
			return
		}
	}
	s.pendingKittyPrintableCodepoint = parseUnmodifiedKittyPrintableCodepoint(seq)
	s.mu.Unlock()

	if s.OnData != nil {
		s.OnData(seq)
	}
}

func parseUnmodifiedKittyPrintableCodepoint(seq string) int {
	m := kittyCsiURegex.FindStringSubmatch(seq)
	if m == nil {
		return 0
	}
	cp := atoiOr(m[1], 0)
	mod := atoiOr(m[4], 1) - 1
	if mod != 0 {
		return 0
	}
	if cp < 32 {
		return 0
	}
	return cp
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// =============================================================================
// Sequence completeness
// =============================================================================

type seqStatus int

const (
	seqIncomplete seqStatus = iota
	seqComplete
	seqNotEscape
)

// isCompleteSequence reports whether candidate (which should start at "\x1b")
// is a complete escape sequence.
func isCompleteSequence(candidate string) seqStatus {
	if len(candidate) == 0 || candidate[0] != '\x1b' {
		return seqNotEscape
	}
	if len(candidate) == 1 {
		return seqIncomplete
	}
	switch candidate[1] {
	case '[':
		// Old-style mouse: ESC[M + 3 bytes = 6 total.
		if len(candidate) >= 3 && candidate[2] == 'M' {
			if len(candidate) >= 6 {
				return seqComplete
			}
			return seqIncomplete
		}
		return isCompleteCSI(candidate)
	case ']':
		return isCompleteOSC(candidate)
	case 'P':
		return isCompleteDCS(candidate)
	case '_':
		return isCompleteAPC(candidate)
	case 'O':
		// SS3: ESC O <one char>
		if len(candidate) >= 3 {
			return seqComplete
		}
		return seqIncomplete
	default:
		// Meta key: ESC + single byte → complete.
		if len(candidate) >= 2 {
			return seqComplete
		}
		return seqIncomplete
	}
}

func isCompleteCSI(data string) seqStatus {
	if len(data) < 3 {
		return seqIncomplete
	}
	payload := data[2:]
	last := payload[len(payload)-1]
	if last < 0x40 || last > 0x7e {
		return seqIncomplete
	}
	// Special handling for SGR mouse sequences: \x1b[<B;X;Y[Mm]
	if payload[0] == '<' {
		if last == 'M' || last == 'm' {
			parts := strings.Split(payload[1:len(payload)-1], ";")
			if len(parts) == 3 {
				for _, p := range parts {
					if !allDigits(p) {
						return seqIncomplete
					}
				}
				return seqComplete
			}
			return seqIncomplete
		}
		return seqIncomplete
	}
	return seqComplete
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isCompleteOSC(data string) seqStatus {
	if strings.HasSuffix(data, "\x1b\\") || strings.HasSuffix(data, "\x07") {
		return seqComplete
	}
	return seqIncomplete
}

func isCompleteDCS(data string) seqStatus {
	if strings.HasSuffix(data, "\x1b\\") {
		return seqComplete
	}
	return seqIncomplete
}

func isCompleteAPC(data string) seqStatus {
	if strings.HasSuffix(data, "\x1b\\") {
		return seqComplete
	}
	return seqIncomplete
}

// extractCompleteSequences walks the buffer from the start, peeling off as
// many complete sequences (or single non-escape characters) as it can.
// Anything left at the tail is returned as remainder.
func extractCompleteSequences(buffer string) (sequences []string, remainder string) {
	pos := 0
	for pos < len(buffer) {
		remaining := buffer[pos:]
		if remaining[0] == '\x1b' {
			// Try to extract a complete escape sequence.
			seqEnd := 1
			advanced := false
			for seqEnd <= len(remaining) {
				cand := remaining[:seqEnd]
				status := isCompleteSequence(cand)
				if status == seqComplete {
					// Special case: \x1b\x1b followed by [ ] O P _ — emit
					// the first ESC and rescan.
					if cand == "\x1b\x1b" && seqEnd < len(remaining) {
						switch remaining[seqEnd] {
						case '[', ']', 'O', 'P', '_':
							sequences = append(sequences, "\x1b")
							pos++
							advanced = true
							goto outerContinue
						}
					}
					sequences = append(sequences, cand)
					pos += seqEnd
					advanced = true
					goto outerContinue
				}
				if status == seqIncomplete {
					seqEnd++
					continue
				}
				// seqNotEscape can't happen when we start with ESC.
				sequences = append(sequences, cand)
				pos += seqEnd
				advanced = true
				goto outerContinue
			}
			// Ran out of input — keep the partial as remainder.
			if !advanced {
				return sequences, remaining
			}
		} else {
			// Take a single rune (not byte) — multi-byte UTF-8 sequences should
			// stay together.
			r, size := decodeRune(buffer, pos)
			if size == 0 {
				size = 1
			}
			sequences = append(sequences, string(r))
			pos += size
		}
	outerContinue:
	}
	return sequences, ""
}
