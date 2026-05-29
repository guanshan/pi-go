package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// =============================================================================
// Token parsing
// =============================================================================

func TestExtractPathTokenPlainPath(t *testing.T) {
	tok, start := extractPathToken("see foo/bar")
	if tok != "foo/bar" || start != 4 {
		t.Errorf("plain: tok=%q start=%d", tok, start)
	}
}

func TestExtractPathTokenAtPrefix(t *testing.T) {
	tok, start := extractPathToken("hello @docs/")
	if tok != "@docs/" || start != 6 {
		t.Errorf("@: tok=%q start=%d", tok, start)
	}
}

func TestExtractPathTokenRelative(t *testing.T) {
	tok, _ := extractPathToken("./pkg")
	if tok != "./pkg" {
		t.Errorf("relative: %q", tok)
	}
	tok, _ = extractPathToken("ls ../config")
	if tok != "../config" {
		t.Errorf("parent: %q", tok)
	}
}

func TestExtractPathTokenAbsolute(t *testing.T) {
	tok, _ := extractPathToken("cd /tmp/abc")
	if tok != "/tmp/abc" {
		t.Errorf("absolute: %q", tok)
	}
}

func TestExtractPathTokenQuoted(t *testing.T) {
	tok, start := extractPathToken("open \"My Documents/")
	if tok != "\"My Documents/" || start != 5 {
		t.Errorf("quoted: tok=%q start=%d", tok, start)
	}
}

func TestExtractPathTokenAtQuoted(t *testing.T) {
	tok, start := extractPathToken("hello @\"Docs/")
	if tok != "@\"Docs/" || start != 6 {
		t.Errorf("@quoted: tok=%q start=%d", tok, start)
	}
}

func TestExtractPathTokenNotPath(t *testing.T) {
	tok, _ := extractPathToken("hello world")
	if tok != "" {
		t.Errorf("plain words → no path: %q", tok)
	}
}

func TestParsePathPrefix(t *testing.T) {
	cases := []struct {
		in     string
		raw    string
		isAt   bool
		quoted bool
	}{
		{"foo", "foo", false, false},
		{"@foo", "foo", true, false},
		{"\"foo bar", "foo bar", false, true},
		{"@\"foo bar", "foo bar", true, true},
	}
	for _, c := range cases {
		raw, at, q := parsePathPrefix(c.in)
		if raw != c.raw || at != c.isAt || q != c.quoted {
			t.Errorf("%q → raw=%q at=%v q=%v (want %q %v %v)", c.in, raw, at, q, c.raw, c.isAt, c.quoted)
		}
	}
}

func TestFindUnclosedQuoteStart(t *testing.T) {
	if _, ok := findUnclosedQuoteStart("a \"b\" c"); ok {
		t.Error("balanced quotes")
	}
	pos, ok := findUnclosedQuoteStart("a \"b c")
	if !ok || pos != 2 {
		t.Errorf("unclosed: pos=%d ok=%v", pos, ok)
	}
}

// =============================================================================
// SlashCommand provider
// =============================================================================

func TestSlashCommandSuggest(t *testing.T) {
	p := SlashCommandAutocompleteProvider{
		Commands: []SlashCommand{
			{Name: "help", Description: "Show help"},
			{Name: "history", Description: "Show history"},
			{Name: "quit", Description: "Quit"},
		},
	}
	got := p.Suggest("/h", 2)
	if len(got.Items) != 2 {
		t.Errorf("/h: %#v", got.Items)
	}
	if got.Prefix != "/h" {
		t.Errorf("prefix: %q", got.Prefix)
	}
	got = p.Suggest("hello /q", 8)
	if len(got.Items) != 0 {
		t.Errorf("expected empty: %#v", got.Items)
	}
}

func TestSlashCommandEmptyQuery(t *testing.T) {
	p := SlashCommandAutocompleteProvider{
		Commands: []SlashCommand{{Name: "a"}, {Name: "b"}},
	}
	got := p.Suggest("/", 1)
	if len(got.Items) != 2 {
		t.Errorf("empty query: %#v", got.Items)
	}
}

func TestCombinedAutocomplete(t *testing.T) {
	a := SlashCommandAutocompleteProvider{Commands: []SlashCommand{{Name: "help"}}}
	b := SlashCommandAutocompleteProvider{Commands: []SlashCommand{{Name: "history"}}}
	c := CombinedAutocompleteProvider{Providers: []AutocompleteProvider{a, b}}
	got := c.Suggest("/h", 2)
	if len(got.Items) != 2 {
		t.Errorf("combined: %#v", got.Items)
	}
}

// =============================================================================
// Path provider (filesystem fallback)
// =============================================================================

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"alpha.txt", "alphabet.txt", "beta.txt", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPathProviderBasic(t *testing.T) {
	dir := setupTestDir(t)
	p := PathAutocompleteProvider{BaseDir: dir, DisableFd: true}
	got := p.Suggest("ls al", 5)
	// "al" doesn't contain '/'; not detected as path → no items.
	if len(got.Items) != 0 {
		t.Errorf("non-path: %#v", got.Items)
	}
	got = p.Suggest("ls ./al", 7)
	names := map[string]bool{}
	for _, it := range got.Items {
		names[it.Label] = true
	}
	if !names["./alpha.txt"] || !names["./alphabet.txt"] {
		t.Errorf("alpha matches: %#v", got.Items)
	}
}

func TestPathProviderHiddenFiles(t *testing.T) {
	dir := setupTestDir(t)
	p := PathAutocompleteProvider{BaseDir: dir, DisableFd: true}
	// Hidden by default.
	got := p.Suggest("./", 2)
	for _, it := range got.Items {
		if it.Label == "./.hidden" {
			t.Error("hidden should be excluded by default")
		}
	}
	// Including hidden.
	p.IncludeHidden = true
	got = p.Suggest("./", 2)
	found := false
	for _, it := range got.Items {
		if it.Label == "./.hidden" {
			found = true
		}
	}
	if !found {
		t.Errorf("hidden should be included: %#v", got.Items)
	}
}

func TestPathProviderDirectorySlash(t *testing.T) {
	dir := setupTestDir(t)
	p := PathAutocompleteProvider{BaseDir: dir, DisableFd: true}
	got := p.Suggest("./sub", 5)
	found := false
	for _, it := range got.Items {
		if it.Label == "./subdir/" {
			found = true
		}
	}
	if !found {
		t.Errorf("dir trailing slash missing: %#v", got.Items)
	}
}

func TestPathProviderQuotedPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spaced name.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	p := PathAutocompleteProvider{BaseDir: dir, DisableFd: true}
	got := p.Suggest("open \"./sp", 10)
	if got.Prefix != "\"./sp" {
		t.Errorf("quoted prefix: %q", got.Prefix)
	}
}

func TestPathProviderMaxResults(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(dir, "f"+itoa(i)), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := PathAutocompleteProvider{BaseDir: dir, MaxResults: 3, DisableFd: true}
	got := p.Suggest("./f", 3)
	if len(got.Items) != 3 {
		t.Errorf("max-results: %d", len(got.Items))
	}
}

func TestPathProviderNoTriggerForBareWord(t *testing.T) {
	p := PathAutocompleteProvider{DisableFd: true}
	got := p.Suggest("hello", 5)
	if len(got.Items) != 0 || got.Prefix != "" {
		t.Errorf("bare word should not trigger: %#v", got)
	}
}

func TestPathProviderAtPrefix(t *testing.T) {
	dir := setupTestDir(t)
	p := PathAutocompleteProvider{BaseDir: dir, DisableFd: true}
	got := p.Suggest("@al", 3)
	// @al — looksLikePath = true (starts with @), but no '/' so treated as
	// the file basename in cwd.
	if got.Prefix != "@al" {
		t.Errorf("@-prefix: %q", got.Prefix)
	}
}
