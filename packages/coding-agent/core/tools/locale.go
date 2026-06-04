package tools

import (
	"strings"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// localeCollator approximates JavaScript's String.prototype.localeCompare with
// the default locale: a Unicode-aware ordering. ls.ts sorts directory entries
// with `a.toLowerCase().localeCompare(b.toLowerCase())`, so non-ASCII names must
// order by collation weight rather than raw byte value to match TS output.
var (
	localeCollatorOnce sync.Once
	localeCollator     *collate.Collator
	localeCollatorMu   sync.Mutex
)

func localeCompareLower(a, b string) int {
	localeCollatorOnce.Do(func() {
		localeCollator = collate.New(language.Und)
	})
	// Lowercase first to mirror TS `a.toLowerCase().localeCompare(b.toLowerCase())`.
	localeCollatorMu.Lock()
	defer localeCollatorMu.Unlock()
	return localeCollator.CompareString(strings.ToLower(a), strings.ToLower(b))
}
