package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestFileReferenceSuggestionsRecursiveFuzzy verifies the @-attachment provider
// performs a RECURSIVE fuzzy search (P1-18): a query surfaces deep-tree matches,
// not just a single-directory prefix listing. Mirrors TS getFuzzyFileSuggestions.
func TestFileReferenceSuggestionsRecursiveFuzzy(t *testing.T) {
	cwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(cwd, "src", "ui", "components"))
	mustWrite(t, filepath.Join(cwd, "src", "ui", "components", "button.go"))
	mustWrite(t, filepath.Join(cwd, "src", "main.go"))
	mustWrite(t, filepath.Join(cwd, "README.md"))

	// "@compon" must surface the deep "src/ui/components/" directory even though it
	// is several levels below cwd — a flat prefix listing could never find it.
	got := fileReferenceSuggestions("@compon", cwd)
	if !slices.Contains(got, "@src/ui/components/") {
		t.Fatalf("@compon should surface deep dir, got %#v", got)
	}

	// Substring-in-name fuzzy match: "button" lives deep, query "butt" finds it.
	got = fileReferenceSuggestions("@butt", cwd)
	if !slices.Contains(got, "@src/ui/components/button.go") {
		t.Fatalf("@butt should surface deep file, got %#v", got)
	}
}

// TestFileReferenceCompletionsCarryDisplayPathDescription verifies each
// @-suggestion includes description=displayPath (P1-18), mirroring TS
// getFuzzyFileSuggestions which sets `description: displayPath`.
func TestFileReferenceCompletionsCarryDisplayPathDescription(t *testing.T) {
	cwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(cwd, "src"))
	mustWrite(t, filepath.Join(cwd, "src", "foo.go"))

	completions := fileReferenceCompletions("@foo", cwd)
	var found bool
	for _, completion := range completions {
		if completion.Value == "@src/foo.go" {
			found = true
			if completion.Description != "src/foo.go" {
				t.Fatalf("description=%q want display path %q", completion.Description, "src/foo.go")
			}
			if completion.Label != "foo.go" {
				t.Fatalf("label=%q want basename %q", completion.Label, "foo.go")
			}
		}
	}
	if !found {
		t.Fatalf("expected @src/foo.go in completions, got %#v", completions)
	}
}

// TestFileReferenceScoringOrdersDirectoriesAndExactMatches verifies the scoring
// order: exact filename match first, then prefix, with directories bonused to
// the top of their score band (P1-18, mirrors TS scoreEntry).
func TestFileReferenceScoringOrdersDirectoriesAndExactMatches(t *testing.T) {
	cwd := t.TempDir()
	// "app" (exact dir, score 100+10), "app.go" (prefix file, score 80),
	// "application.go" (prefix file, score 80), "myapp.go" (substring, score 50).
	mustMkdirAll(t, filepath.Join(cwd, "app"))
	mustWrite(t, filepath.Join(cwd, "app.go"))
	mustWrite(t, filepath.Join(cwd, "application.go"))
	mustWrite(t, filepath.Join(cwd, "myapp.go"))

	got := fileReferenceSuggestions("@app", cwd)
	// The exact-name directory must rank first.
	if len(got) == 0 || got[0] != "@app/" {
		t.Fatalf("exact-name dir should rank first, got %#v", got)
	}
	// Substring-only match must rank below the prefix matches.
	idxMyapp := slices.Index(got, "@myapp.go")
	idxAppGo := slices.Index(got, "@app.go")
	if idxMyapp < 0 || idxAppGo < 0 || idxMyapp < idxAppGo {
		t.Fatalf("prefix match should outrank substring match, got %#v", got)
	}
}

// TestFileReferenceWalkRespectsLimit verifies the recursive walk caps the number
// of entries it scores (P1-18 mirrors fd --max-results 100) and never returns
// more than the top-20 cap.
func TestFileReferenceWalkRespectsLimit(t *testing.T) {
	cwd := t.TempDir()
	for i := 0; i < 50; i++ {
		mustWrite(t, filepath.Join(cwd, "match"+itoa(i)+".go"))
	}
	got := fileReferenceSuggestions("@match", cwd)
	if len(got) > fileReferenceMaxResults {
		t.Fatalf("expected at most %d results, got %d", fileReferenceMaxResults, len(got))
	}
}

// TestSlashCommandFuzzyMatching verifies slash-command completion uses fuzzy
// subsequence matching (P1-19): "/mdl"->"/model" and "/tngs"->"/settings", which
// literal-prefix matching could never produce. Mirrors TS fuzzyFilter wiring.
func TestSlashCommandFuzzyMatching(t *testing.T) {
	if got := slashCommandSuggestions("/mdl"); !slices.Contains(got, "/model") {
		t.Fatalf("/mdl should fuzzy-match /model, got %#v", got)
	}
	if got := slashCommandSuggestions("/tngs"); !slices.Contains(got, "/settings") {
		t.Fatalf("/tngs should fuzzy-match /settings, got %#v", got)
	}
	// A plain prefix still works.
	if got := slashCommandSuggestions("/mo"); !slices.Contains(got, "/model") {
		t.Fatalf("/mo should still match /model, got %#v", got)
	}
	// A query matching nothing returns no suggestions.
	if got := slashCommandSuggestions("/zzzqqq"); len(got) != 0 {
		t.Fatalf("/zzzqqq should match nothing, got %#v", got)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
