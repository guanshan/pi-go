package tui

import "strings"

// ansiSequenceLength returns the byte length of the ANSI escape sequence
// starting at index i, or -1 if no recognised sequence begins there.
//
// Recognised:
//   - CSI:  ESC [ ... <final byte 0x40-0x7E>
//   - OSC:  ESC ] ... (BEL | ESC \\)
//   - DCS:  ESC P ... ESC \\
//   - APC:  ESC _ ... ESC \\  (used by Kitty graphics)
//   - SOS:  ESC X ... ESC \\
//   - PM:   ESC ^ ... ESC \\
//   - SS3:  ESC O <one char>
//   - 7-bit single-character escape: ESC <single byte>
func ansiSequenceLength(s string, i int) int {
	if i >= len(s) || s[i] != '\x1b' {
		return -1
	}
	if i+1 >= len(s) {
		return 1
	}
	switch s[i+1] {
	case '[':
		// CSI: parameter bytes 0x30-0x3F, intermediate 0x20-0x2F, final 0x40-0x7E.
		j := i + 2
		for j < len(s) {
			c := s[j]
			if c >= 0x40 && c <= 0x7e {
				return j + 1 - i
			}
			j++
		}
		return len(s) - i
	case ']':
		return findStringTerminator(s, i+2) - i
	case 'P':
		return findStringTerminator(s, i+2) - i
	case '_':
		return findStringTerminator(s, i+2) - i
	case 'X', '^':
		return findStringTerminator(s, i+2) - i
	case 'O':
		// SS3: ESC O followed by a single byte.
		if i+2 < len(s) {
			return 3
		}
		return 2
	default:
		// 7-bit single-character escape (e.g. ESC + letter for meta keys).
		return 2
	}
}

// findStringTerminator returns the absolute byte index just past the next
// string terminator (BEL or ESC \\) starting from j, or len(s) if none.
func findStringTerminator(s string, j int) int {
	for j < len(s) {
		if s[j] == '\a' { // BEL
			return j + 1
		}
		if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
			return j + 2
		}
		j++
	}
	return len(s)
}

// ExtractAnsiCode returns the ANSI escape sequence at the given position, or
// an empty string + 0 length if none. Compatible with the upstream TS helper.
func ExtractAnsiCode(s string, pos int) (code string, length int) {
	length = ansiSequenceLength(s, pos)
	if length <= 0 {
		return "", 0
	}
	return s[pos : pos+length], length
}

// StripAnsi removes all ANSI escape sequences from s.
func StripAnsi(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			length := ansiSequenceLength(s, i)
			if length <= 0 {
				length = 1
			}
			i += length
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
