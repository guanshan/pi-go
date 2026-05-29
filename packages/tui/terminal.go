// terminal.go — small, output-only terminal abstraction.
//
// pi-go's primary interactive renderer is charm.land/bubbletea/v2; this file
// no longer contains a stdin event loop, raw-mode toggling, or Kitty
// keyboard-protocol negotiation. ProcessTerminal exposes a stable surface for
// callers that just need to write OSC sequences (titles, progress) or query
// the terminal size.

package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	xterm "github.com/charmbracelet/x/term"
)

const (
	terminalProgressKeepalive = time.Second
	terminalProgressActive    = "\x1b]9;4;3\a"
	terminalProgressClear     = "\x1b]9;4;0;\a"
	appleShiftEnterSequence   = "\x1b[13;2u"
)

// Terminal is the minimal surface exposed by pi-go's tui package. It covers
// stateless output (writing bytes, OSC sequences, cursor moves) and size
// queries, but NOT a stdin event loop — the latter is the embedder's
// responsibility (Bubble Tea handles it for the interactive coding-agent UI).
type Terminal interface {
	Write(data string)
	Columns() int
	Rows() int
	KittyProtocolActive() bool
	MoveBy(lines int)
	HideCursor()
	ShowCursor()
	ClearLine()
	ClearFromCursor()
	ClearScreen()
	SetTitle(title string)
	SetProgress(active bool)
}

// ProcessTerminal writes to a real (or test) writer with optional progress
// keepalive ticking and Kitty / modifyOtherKeys lifecycle helpers.
type ProcessTerminal struct {
	output                io.Writer
	writeLogPath          string
	mu                    sync.Mutex
	kittyProtocolActive   bool
	modifyOtherKeysActive bool
	progressStop          chan struct{}
	progressDone          chan struct{}
}

// NewProcessTerminal constructs a ProcessTerminal that writes to os.Stdout.
// PI_TUI_WRITE_LOG, when set, mirrors all writes to a file (or, if it points
// at an existing directory, to a timestamped file inside it).
func NewProcessTerminal() *ProcessTerminal {
	return newProcessTerminal(os.Stdout, terminalWriteLogPath(os.Getenv("PI_TUI_WRITE_LOG"), time.Now()))
}

// NewProcessTerminalWithWriter constructs a ProcessTerminal writing to the
// supplied writer. Useful in tests.
func NewProcessTerminalWithWriter(output io.Writer) *ProcessTerminal {
	return newProcessTerminal(output, "")
}

func newProcessTerminal(output io.Writer, writeLogPath string) *ProcessTerminal {
	if output == nil {
		output = os.Stdout
	}
	return &ProcessTerminal{output: output, writeLogPath: writeLogPath}
}

// Start emits the initial mode-setting sequences (bracketed paste, Kitty
// keyboard query, modifyOtherKeys). It does NOT install stdin or resize
// handlers; pi-go's interactive layer uses Bubble Tea for that.
//
// Deprecated: only the byte-emitting effect is preserved. The onInput /
// onResize callbacks are accepted but ignored.
func (p *ProcessTerminal) Start(onInput func(string), onResize func()) {
	_ = onInput
	_ = onResize
	p.Write("\x1b[?2004h")
	p.Write("\x1b[?u")
	p.mu.Lock()
	p.modifyOtherKeysActive = true
	p.mu.Unlock()
	p.Write("\x1b[>4;2m")
}

// Stop emits cleanup sequences (disable bracketed paste, modifyOtherKeys,
// Kitty protocol, progress).
func (p *ProcessTerminal) Stop() {
	if p.clearProgressInterval() {
		p.Write(terminalProgressClear)
	}
	p.Write("\x1b[?2004l")
	p.mu.Lock()
	kitty := p.kittyProtocolActive
	modifyOtherKeys := p.modifyOtherKeysActive
	p.kittyProtocolActive = false
	p.modifyOtherKeysActive = false
	p.mu.Unlock()
	if kitty {
		p.Write("\x1b[<u")
	}
	if modifyOtherKeys {
		p.Write("\x1b[>4;0m")
	}
}

// DrainInput exists for API parity with the upstream TS implementation. It
// disables Kitty / modifyOtherKeys to flush late events, then sleeps for
// idleDuration so any remaining bytes arrive at the embedder's stdin reader.
//
// Deprecated: pi-go does not own stdin in the new architecture. Bubble Tea's
// own teardown handles input drain.
func (p *ProcessTerminal) DrainInput(maxDuration, idleDuration time.Duration) {
	if maxDuration <= 0 {
		maxDuration = time.Second
	}
	if idleDuration <= 0 {
		idleDuration = 50 * time.Millisecond
	}
	p.mu.Lock()
	kitty := p.kittyProtocolActive
	modifyOtherKeys := p.modifyOtherKeysActive
	p.kittyProtocolActive = false
	p.modifyOtherKeysActive = false
	p.mu.Unlock()
	if kitty {
		p.Write("\x1b[<u")
	}
	if modifyOtherKeys {
		p.Write("\x1b[>4;0m")
	}
	if idleDuration > maxDuration {
		idleDuration = maxDuration
	}
	time.Sleep(idleDuration)
}

// Write writes data to the underlying output and (if configured) to the
// write log.
func (p *ProcessTerminal) Write(data string) {
	p.mu.Lock()
	output := p.output
	logPath := p.writeLogPath
	p.mu.Unlock()
	_, _ = io.WriteString(output, data)
	if logPath != "" {
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = file.WriteString(data)
			_ = file.Close()
		}
	}
}

// Columns reports the terminal column count, preferring x/term.GetSize on
// stdout, falling back to the COLUMNS env var, and finally 80.
func (p *ProcessTerminal) Columns() int {
	if w, _, err := xterm.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	if v := envInt("COLUMNS"); v > 0 {
		return v
	}
	return 80
}

// Rows reports the terminal row count, preferring x/term.GetSize, then
// LINES, then 24.
func (p *ProcessTerminal) Rows() int {
	if _, h, err := xterm.GetSize(os.Stdout.Fd()); err == nil && h > 0 {
		return h
	}
	if v := envInt("LINES"); v > 0 {
		return v
	}
	return 24
}

// KittyProtocolActive reports whether the terminal currently advertises
// Kitty keyboard protocol support.
func (p *ProcessTerminal) KittyProtocolActive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.kittyProtocolActive
}

// SetKittyProtocolActive records whether the Kitty keyboard protocol is in
// use. Also updates the package-wide flag (used by ParseKey/MatchesKey).
func (p *ProcessTerminal) SetKittyProtocolActive(active bool) {
	p.mu.Lock()
	p.kittyProtocolActive = active
	p.mu.Unlock()
	_kittyProtocolActive.Store(active)
}

// MoveBy moves the cursor up (negative) or down (positive) by N lines.
func (p *ProcessTerminal) MoveBy(lines int) {
	if lines < 0 {
		p.Write(fmt.Sprintf("\x1b[%dA", -lines))
	} else if lines > 0 {
		p.Write(fmt.Sprintf("\x1b[%dB", lines))
	}
}

// HideCursor / ShowCursor / ClearLine / ClearFromCursor / ClearScreen emit the
// usual VT100/CSI sequences.
func (p *ProcessTerminal) HideCursor()      { p.Write("\x1b[?25l") }
func (p *ProcessTerminal) ShowCursor()      { p.Write("\x1b[?25h") }
func (p *ProcessTerminal) ClearLine()       { p.Write("\x1b[K") }
func (p *ProcessTerminal) ClearFromCursor() { p.Write("\x1b[J") }
func (p *ProcessTerminal) ClearScreen()     { p.Write("\x1b[2J\x1b[H") }

// SetTitle emits OSC 0 to set the terminal window title.
func (p *ProcessTerminal) SetTitle(title string) { p.Write("\x1b]0;" + title + "\a") }

// SetProgress toggles the OSC 9;4 indeterminate-progress indicator.
func (p *ProcessTerminal) SetProgress(active bool) {
	if active {
		p.Write(terminalProgressActive)
		p.startProgressInterval()
		return
	}
	p.clearProgressInterval()
	p.Write(terminalProgressClear)
}

func (p *ProcessTerminal) startProgressInterval() {
	p.mu.Lock()
	if p.progressStop != nil {
		p.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	p.progressStop = stop
	p.progressDone = done
	p.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(terminalProgressKeepalive)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.Write(terminalProgressActive)
			case <-stop:
				return
			}
		}
	}()
}

func (p *ProcessTerminal) clearProgressInterval() bool {
	p.mu.Lock()
	stop := p.progressStop
	done := p.progressDone
	if stop == nil {
		p.mu.Unlock()
		return false
	}
	p.progressStop = nil
	p.progressDone = nil
	close(stop)
	p.mu.Unlock()
	<-done
	return true
}

// IsAppleTerminalSession reports whether the current process appears to be
// running under macOS Terminal.app.
func IsAppleTerminalSession() bool {
	return IsAppleTerminalSessionFor(runtime.GOOS, map[string]string{"TERM_PROGRAM": os.Getenv("TERM_PROGRAM")})
}

// IsAppleTerminalSessionFor is the testable form of IsAppleTerminalSession.
func IsAppleTerminalSessionFor(goos string, env map[string]string) bool {
	return goos == "darwin" && env["TERM_PROGRAM"] == "Apple_Terminal"
}

// NormalizeAppleTerminalInput rewrites a plain "\r" to a Kitty Shift+Enter
// sequence on Apple Terminal when shift is pressed (Apple Terminal does not
// distinguish them otherwise).
func NormalizeAppleTerminalInput(data string, isAppleTerminal, isShiftPressed bool) string {
	if isAppleTerminal && data == "\r" && isShiftPressed {
		return appleShiftEnterSequence
	}
	return data
}

// terminalWriteLogPath builds the path that PI_TUI_WRITE_LOG resolves to. If
// the value names an existing directory, a timestamped file is created
// inside; otherwise the value is used as-is.
func terminalWriteLogPath(value string, now time.Time) string {
	if value == "" {
		return ""
	}
	stat, err := os.Stat(value)
	if err == nil && stat.IsDir() {
		name := fmt.Sprintf("tui-%04d-%02d-%02d_%02d-%02d-%02d-%d.log",
			now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), os.Getpid())
		return filepath.Join(value, name)
	}
	return value
}

func envInt(key string) int {
	value := os.Getenv(key)
	if value == "" {
		return 0
	}
	var parsed int
	_, _ = fmt.Sscanf(value, "%d", &parsed)
	return parsed
}
