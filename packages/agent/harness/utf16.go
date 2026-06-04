package harness

import (
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// utf16Len returns the number of UTF-16 code units in s, matching JavaScript's
// String.length: each rune <= U+FFFF counts as one code unit and each rune above
// U+FFFF (encoded as a surrogate pair in UTF-16) counts as two. Go's builtin
// len() counts UTF-8 bytes, which over-counts non-ASCII text, so any limit or
// slice that must stay byte-for-byte identical with the TS port uses this helper.
//
// (Mirrors compaction.utf16Len, which is unexported and cannot be imported here.)
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// sliceUTF16 returns the longest rune-aligned prefix of s whose UTF-16 length
// does not exceed maxUnits. TS's String.slice(0, maxUnits) cuts on UTF-16 code
// unit boundaries and can split an astral rune mid-surrogate; to avoid emitting
// an invalid (lone-surrogate) UTF-8 sequence, Go cuts at the last whole rune that
// stays within the maxUnits budget. For BMP-only (and ASCII) text this is exactly
// the first maxUnits characters, matching TS; it only differs by at most one
// astral rune at the boundary, where TS would otherwise produce broken output.
func sliceUTF16(s string, maxUnits int) string {
	if maxUnits <= 0 {
		return ""
	}
	units := 0
	for i, r := range s {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if units+w > maxUnits {
			return s[:i]
		}
		units += w
	}
	return s
}

// localeRootCollator is the ICU root collator (language.Und) used to approximate
// JavaScript's String.prototype.localeCompare for directory entry ordering.
// Collator.CompareString mutates internal iterator state and is therefore not
// safe for concurrent use, so localeCollatorMu serializes access; directory
// loads are not hot paths, so the lock is inexpensive.
var (
	localeRootCollator = collate.New(language.Und)
	localeCollatorMu   sync.Mutex
)

// localeCompare approximates JS a.localeCompare(b) using the ICU root collation,
// returning -1, 0, or 1. This matches TS's entries.sort((a, b) =>
// a.name.localeCompare(b.name)) used to order skill and prompt-template directory
// entries. The root collator reproduces localeCompare for the common
// case-insensitive-then-tie-break ordering of typical [A-Za-z0-9-] names;
// residual divergence is only possible for exotic Unicode, which does not occur
// in skill/template directory names in practice.
func localeCompare(a, b string) int {
	localeCollatorMu.Lock()
	defer localeCollatorMu.Unlock()
	return localeRootCollator.CompareString(a, b)
}
