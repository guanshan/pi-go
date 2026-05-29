package utils

import (
	"strings"
	"unicode/utf8"
)

// SanitizeUnicode removes invalid UTF-8 byte sequences, including WTF-8 encoded
// unpaired UTF-16 surrogates, while preserving valid Unicode such as emoji/CJK.
func SanitizeUnicode(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}
