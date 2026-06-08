package utils

import (
	"strings"
	"unicode/utf8"
)

// SanitizeUnicode removes unpaired Unicode surrogate characters from a string,
// mirroring TS sanitizeSurrogates. Go strings are UTF-8, so lone surrogates can
// only appear WTF-8 encoded (the 3-byte 0xED 0xA0-0xBF 0x80-0xBF form). This
// function deletes exactly those unpaired high/low surrogate sequences while
// preserving everything else — including properly paired surrogates and any
// other (non-surrogate) bytes — to match the TS regex which targets only
// unpaired UTF-16 surrogates.
func SanitizeUnicode(s string) string {
	// Fast path: valid UTF-8 cannot contain any (WTF-8) surrogate bytes, so
	// there is nothing to strip.
	if utf8.ValidString(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	i := 0
	n := len(s)
	for i < n {
		if hi, ok := wtf8SurrogateAt(s, i); ok && isHighSurrogate(hi) {
			// A high surrogate immediately followed by a low surrogate is a
			// valid pair; TS leaves paired surrogates intact.
			if lo, ok2 := wtf8SurrogateAt(s, i+3); ok2 && isLowSurrogate(lo) {
				b.WriteString(s[i : i+6])
				i += 6
				continue
			}
			// Unpaired high surrogate: drop it.
			i += 3
			continue
		}
		if lo, ok := wtf8SurrogateAt(s, i); ok && isLowSurrogate(lo) {
			// A leading/standalone low surrogate is unpaired (a preceding high
			// surrogate would already have consumed it above): drop it.
			i += 3
			continue
		}
		// Copy one valid UTF-8 rune verbatim. For an invalid, non-surrogate
		// byte, DecodeRuneInString returns (RuneError, 1); preserve that single
		// byte unchanged to match TS, which only targets surrogates.
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// wtf8SurrogateAt decodes a WTF-8 encoded UTF-16 surrogate code unit starting at
// byte offset i (a 3-byte sequence 0xED, 0xA0-0xBF, 0x80-0xBF). It returns the
// surrogate code point (0xD800-0xDFFF) and true on success.
func wtf8SurrogateAt(s string, i int) (rune, bool) {
	if i+3 > len(s) {
		return 0, false
	}
	b0 := s[i]
	b1 := s[i+1]
	b2 := s[i+2]
	if b0 != 0xED || b1 < 0xA0 || b1 > 0xBF || b2 < 0x80 || b2 > 0xBF {
		return 0, false
	}
	cp := rune(b0&0x0F)<<12 | rune(b1&0x3F)<<6 | rune(b2&0x3F)
	return cp, true
}

func isHighSurrogate(r rune) bool { return r >= 0xD800 && r <= 0xDBFF }

func isLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= 0xDFFF }
