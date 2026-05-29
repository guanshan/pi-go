package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// AutocompleteItem is a single suggestion. Detail is optional secondary text
// (file size, command description, …).
type AutocompleteItem struct {
	Label  string
	Value  string
	Detail string
}

// AutocompleteSuggestions is the result of an autocomplete query.
type AutocompleteSuggestions struct {
	Items []AutocompleteItem
	// Prefix is the substring of the input that the suggestion would
	// replace, useful for embedders that want to keep the rest of the
	// input intact when accepting a suggestion.
	Prefix string
}

// AutocompleteProvider supplies suggestions for an input + cursor position.
type AutocompleteProvider interface {
	Suggest(input string, cursor int) AutocompleteSuggestions
}

// CombinedAutocompleteProvider asks each provider in order and merges all
// returned items into a single suggestion list. Providers earlier in the list
// take prefix precedence (the first non-empty Prefix wins).
type CombinedAutocompleteProvider struct {
	Providers []AutocompleteProvider
}

// Suggest implements AutocompleteProvider.
func (c CombinedAutocompleteProvider) Suggest(input string, cursor int) AutocompleteSuggestions {
	var out AutocompleteSuggestions
	for _, p := range c.Providers {
		if p == nil {
			continue
		}
		got := p.Suggest(input, cursor)
		if out.Prefix == "" {
			out.Prefix = got.Prefix
		}
		out.Items = append(out.Items, got.Items...)
	}
	return out
}

// =============================================================================
// SlashCommand provider
// =============================================================================

// SlashCommand describes a "/foo" command suggestion.
type SlashCommand struct {
	Name        string
	Description string
}

// SlashCommandAutocompleteProvider suggests slash commands when the input
// begins with "/" and the cursor is within the command token.
type SlashCommandAutocompleteProvider struct {
	Commands []SlashCommand
}

// Suggest implements AutocompleteProvider.
func (s SlashCommandAutocompleteProvider) Suggest(input string, cursor int) AutocompleteSuggestions {
	if cursor < 0 || cursor > len(input) {
		return AutocompleteSuggestions{}
	}
	if !strings.HasPrefix(input, "/") {
		return AutocompleteSuggestions{}
	}
	prefix := input[:cursor]
	if strings.ContainsAny(prefix, " \t") {
		return AutocompleteSuggestions{}
	}
	query := strings.TrimPrefix(prefix, "/")
	items := make([]AutocompleteItem, 0, len(s.Commands))
	for _, c := range s.Commands {
		if c.Name == "" {
			continue
		}
		if query == "" || strings.HasPrefix(c.Name, query) {
			items = append(items, AutocompleteItem{
				Label:  "/" + c.Name,
				Value:  "/" + c.Name,
				Detail: c.Description,
			})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return AutocompleteSuggestions{Items: items, Prefix: prefix}
}

// =============================================================================
// Path provider
// =============================================================================

// PathAutocompleteProvider suggests filesystem paths.
//
// It triggers when the cursor sits on a token that "looks like" a path:
// contains '/' or starts with '@' or starts with '"' (an unclosed double
// quote). When enabled and `fd` is on PATH, files are listed via `fd`;
// otherwise it falls back to os.ReadDir on the directory portion of the
// prefix.
type PathAutocompleteProvider struct {
	// MaxResults caps the number of returned items. <= 0 → 25.
	MaxResults int
	// IncludeHidden controls whether dotfiles are listed.
	IncludeHidden bool
	// BaseDir overrides the working directory for relative paths.
	BaseDir string
	// FdTimeout caps fd execution. <= 0 → 250ms.
	FdTimeout time.Duration
	// DisableFd forces the os.ReadDir code path. Useful for tests.
	DisableFd bool
}

// Suggest implements AutocompleteProvider.
func (p PathAutocompleteProvider) Suggest(input string, cursor int) AutocompleteSuggestions {
	if cursor < 0 || cursor > len(input) {
		return AutocompleteSuggestions{}
	}
	prefixToken, prefixStart := extractPathToken(input[:cursor])
	if prefixToken == "" {
		return AutocompleteSuggestions{}
	}
	rawPrefix, _, _ := parsePathPrefix(prefixToken)

	max := p.MaxResults
	if max <= 0 {
		max = 25
	}

	// Preferred path: try fd when available.
	var items []AutocompleteItem
	if !p.DisableFd && fdAvailable() {
		items = p.fdSuggest(rawPrefix, max)
	}
	if items == nil {
		items = p.fsSuggest(rawPrefix, max)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })

	return AutocompleteSuggestions{
		Items:  items,
		Prefix: input[prefixStart:cursor],
	}
}

// fsSuggest is the os.ReadDir fallback.
func (p PathAutocompleteProvider) fsSuggest(rawPrefix string, max int) []AutocompleteItem {
	dir, base := filepath.Split(rawPrefix)
	searchDir := dir
	if searchDir == "" {
		searchDir = "."
	}
	if !filepath.IsAbs(searchDir) && p.BaseDir != "" {
		searchDir = filepath.Join(p.BaseDir, searchDir)
	}
	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}
	items := make([]AutocompleteItem, 0, max)
	baseLower := strings.ToLower(base)
	for _, entry := range entries {
		name := entry.Name()
		if !p.IncludeHidden && strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if baseLower != "" && !strings.HasPrefix(strings.ToLower(name), baseLower) {
			continue
		}
		display := dir + name
		if entry.IsDir() {
			display += "/"
		}
		items = append(items, AutocompleteItem{Label: display, Value: display})
		if len(items) >= max {
			break
		}
	}
	return items
}

// fdSuggest runs `fd --hidden? <pattern>` and returns one item per line.
func (p PathAutocompleteProvider) fdSuggest(rawPrefix string, max int) []AutocompleteItem {
	timeout := p.FdTimeout
	if timeout <= 0 {
		timeout = 250 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := []string{"--color=never"}
	if p.IncludeHidden {
		args = append(args, "--hidden")
	}
	if max > 0 {
		args = append(args, "--max-results", itoa(max))
	}
	dir, base := filepath.Split(rawPrefix)
	searchDir := dir
	if searchDir == "" {
		searchDir = "."
	}
	if !filepath.IsAbs(searchDir) && p.BaseDir != "" {
		searchDir = filepath.Join(p.BaseDir, searchDir)
	}
	pattern := "^" + regexEscape(base)
	args = append(args, pattern, searchDir)

	cmd := exec.CommandContext(ctx, "fd", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	items := make([]AutocompleteItem, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		// fd returns paths relative to searchDir when searchDir is "." and
		// the original prefix had no directory part; preserve user-supplied
		// dir prefix so the value the user types is replaced cleanly.
		display := line
		if dir != "" && !strings.HasPrefix(line, dir) {
			display = dir + filepath.Base(line)
		}
		items = append(items, AutocompleteItem{Label: display, Value: display})
		if len(items) >= max {
			break
		}
	}
	return items
}

// fdAvailableCache memoizes the LookPath result so repeated suggest calls
// don't shell out.
var fdAvailableCache atomic.Int32 // 0=unknown, 1=yes, 2=no

func fdAvailable() bool {
	switch fdAvailableCache.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	if _, err := exec.LookPath("fd"); err == nil {
		fdAvailableCache.Store(1)
		return true
	}
	fdAvailableCache.Store(2)
	return false
}

// resetFdAvailableCache is used by tests to force re-detection.
func resetFdAvailableCache() { fdAvailableCache.Store(0) }

// =============================================================================
// Token / prefix parsing
// =============================================================================

const pathDelimiters = " \t\"'="

// extractPathToken pulls the trailing path-like token from prefix. Quoted
// regions take precedence: if there is an unclosed double-quote, the text
// from that quote (or the optional preceding '@') to the cursor is returned
// as the token. Otherwise the substring after the last delimiter is
// considered.
func extractPathToken(prefix string) (string, int) {
	if prefix == "" {
		return "", 0
	}
	if start, ok := findUnclosedQuoteStart(prefix); ok {
		// Include preceding '@' when adjacent and at a token boundary.
		tokStart := start
		if tokStart > 0 && prefix[tokStart-1] == '@' && isTokenStart(prefix, tokStart-1) {
			tokStart = start - 1
		} else if !isTokenStart(prefix, start) {
			return "", 0
		}
		return prefix[tokStart:], tokStart
	}
	last := findLastDelimiter(prefix)
	tokStart := 0
	if last >= 0 {
		tokStart = last + 1
	}
	tok := prefix[tokStart:]
	if looksLikePath(tok) {
		return tok, tokStart
	}
	return "", 0
}

func findUnclosedQuoteStart(text string) (int, bool) {
	inQuotes := false
	start := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				start = i
			}
		}
	}
	if inQuotes {
		return start, true
	}
	return -1, false
}

func findLastDelimiter(text string) int {
	for i := len(text) - 1; i >= 0; i-- {
		if strings.ContainsRune(pathDelimiters, rune(text[i])) {
			return i
		}
	}
	return -1
}

func isTokenStart(text string, index int) bool {
	if index == 0 {
		return true
	}
	return strings.ContainsRune(pathDelimiters, rune(text[index-1]))
}

func looksLikePath(token string) bool {
	if token == "" {
		return false
	}
	if strings.HasPrefix(token, "@") || strings.HasPrefix(token, "\"") {
		return true
	}
	if strings.HasPrefix(token, "./") || strings.HasPrefix(token, "../") || strings.HasPrefix(token, "~/") {
		return true
	}
	return strings.Contains(token, "/")
}

// parsePathPrefix returns the raw filesystem prefix after stripping leading
// '@' and / or matched-double-quote markers, plus flags indicating which
// markers were present.
func parsePathPrefix(prefix string) (raw string, atPrefix bool, quoted bool) {
	if strings.HasPrefix(prefix, "@\"") {
		return prefix[2:], true, true
	}
	if strings.HasPrefix(prefix, "@") {
		return prefix[1:], true, false
	}
	if strings.HasPrefix(prefix, "\"") {
		return prefix[1:], false, true
	}
	return prefix, false, false
}

// regexEscape is a tiny replacement for the subset of regexp characters that
// could appear in user-supplied path bases. Mirrors upstream's behaviour
// (escape ., *, +, ?, ^, $, {, }, (, ), |, [, ], \).
func regexEscape(s string) string {
	special := `.+*?^$(){}|[]\`
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
