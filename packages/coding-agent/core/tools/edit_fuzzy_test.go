package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runEdit is a small helper that writes initialContent to a temp file, runs the
// edit tool with the given edits, and returns (resultText, isError, finalFile).
func runEdit(t *testing.T, initialContent string, edits []map[string]any) (string, bool, string) {
	t.Helper()
	cwd := t.TempDir()
	path := "file.txt"
	abs := filepath.Join(cwd, path)
	if err := os.WriteFile(abs, []byte(initialContent), 0o644); err != nil {
		t.Fatal(err)
	}
	res := EditTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
		"path":  path,
		"edits": edits,
	}), nil)
	data, _ := os.ReadFile(abs)
	return toolText(res.Content), res.IsError, string(data)
}

// TestEditFuzzyNFKC verifies the new Unicode NFKC normalization (TS
// normalizeForFuzzyMatch .normalize("NFKC")). Full-width characters and ligatures
// only match after NFKC folding, which the pre-fix Go normalizer did not perform.
func TestEditFuzzyNFKC(t *testing.T) {
	// File has the NFKC-folded ("file") form; oldText uses the ligature "ﬁle".
	out, isErr, final := runEdit(t, "const ﬁle = 1\n", []map[string]any{
		{"oldText": "const file = 1", "newText": "const file = 2"},
	})
	if isErr {
		t.Fatalf("expected fuzzy NFKC match to succeed, got error: %s", out)
	}
	if !strings.Contains(final, "const file = 2") {
		t.Fatalf("NFKC fuzzy edit not applied, final=%q", final)
	}

	// Full-width latin letters fold to ASCII under NFKC.
	out, isErr, final = runEdit(t, "value ＡＢＣ here\n", []map[string]any{
		{"oldText": "value ABC here", "newText": "value XYZ here"},
	})
	if isErr {
		t.Fatalf("expected full-width NFKC match to succeed, got error: %s", out)
	}
	if !strings.Contains(final, "value XYZ here") {
		t.Fatalf("full-width NFKC edit not applied, final=%q", final)
	}
}

// TestEditFuzzyWhitespaceEquivalent verifies the completed whitespace-equivalence
// set, in particular the U+2002-U+200A range (e.g. thin space U+2009) that the
// pre-fix Go normalizer omitted, plus dashes and smart quotes.
func TestEditFuzzyWhitespaceEquivalent(t *testing.T) {
	// File uses a thin space (U+2009); oldText uses a regular space.
	out, isErr, final := runEdit(t, "a b end\n", []map[string]any{
		{"oldText": "a b end", "newText": "a b done"},
	})
	if isErr {
		t.Fatalf("expected thin-space fuzzy match to succeed, got error: %s", out)
	}
	if !strings.Contains(final, "done") {
		t.Fatalf("thin-space fuzzy edit not applied, final=%q", final)
	}

	// En-dash (U+2013) is equivalent to ASCII hyphen.
	out, isErr, final = runEdit(t, "range 1–5\n", []map[string]any{
		{"oldText": "range 1-5", "newText": "range 1-9"},
	})
	if isErr {
		t.Fatalf("expected en-dash fuzzy match to succeed, got error: %s", out)
	}
	if !strings.Contains(final, "range 1-9") {
		t.Fatalf("en-dash fuzzy edit not applied, final=%q", final)
	}

	// Smart double quotes are equivalent to ASCII double quotes.
	out, isErr, final = runEdit(t, "say “hi” now\n", []map[string]any{
		{"oldText": "say \"hi\" now", "newText": "say \"bye\" now"},
	})
	if isErr {
		t.Fatalf("expected smart-quote fuzzy match to succeed, got error: %s", out)
	}
	if !strings.Contains(final, "bye") {
		t.Fatalf("smart-quote fuzzy edit not applied, final=%q", final)
	}
}

// TestEditUniquenessCountingFuzzy verifies that occurrence counting always runs
// in fuzzy-normalized space (TS countOccurrences). A straight quote and a smart
// quote variant of the same text must be counted as two occurrences, so a unique
// oldText is rejected as duplicate.
func TestEditUniquenessCountingFuzzy(t *testing.T) {
	// Two occurrences once normalized: one uses a straight quote, one a smart quote.
	content := "x = 'a'\ny = ‘a’\n"
	out, isErr, _ := runEdit(t, content, []map[string]any{
		{"oldText": "'a'", "newText": "'b'"},
	})
	if !isErr {
		t.Fatalf("expected duplicate error from fuzzy occurrence counting, got success: %s", out)
	}
	if !strings.Contains(out, "Found 2 occurrences of the text in") {
		t.Fatalf("expected fuzzy-counted duplicate message, got: %s", out)
	}
}

func TestEditErrorMessages(t *testing.T) {
	t.Run("not-found-single", func(t *testing.T) {
		out, isErr, _ := runEdit(t, "hello world\n", []map[string]any{
			{"oldText": "nonexistent", "newText": "x"},
		})
		want := "Could not find the exact text in file.txt. The old text must match exactly including all whitespace and newlines."
		if !isErr || out != want {
			t.Fatalf("not-found single mismatch:\n got: %q\nwant: %q", out, want)
		}
	})

	t.Run("not-found-multi", func(t *testing.T) {
		out, isErr, _ := runEdit(t, "hello world\n", []map[string]any{
			{"oldText": "hello", "newText": "hi"},
			{"oldText": "nonexistent", "newText": "x"},
		})
		want := "Could not find edits[1] in file.txt. The oldText must match exactly including all whitespace and newlines."
		if !isErr || out != want {
			t.Fatalf("not-found multi mismatch:\n got: %q\nwant: %q", out, want)
		}
	})

	t.Run("duplicate-single", func(t *testing.T) {
		out, isErr, _ := runEdit(t, "foo\nfoo\n", []map[string]any{
			{"oldText": "foo", "newText": "bar"},
		})
		want := "Found 2 occurrences of the text in file.txt. The text must be unique. Please provide more context to make it unique."
		if !isErr || out != want {
			t.Fatalf("duplicate single mismatch:\n got: %q\nwant: %q", out, want)
		}
	})

	t.Run("duplicate-multi", func(t *testing.T) {
		out, isErr, _ := runEdit(t, "foo\nfoo\nuniq\n", []map[string]any{
			{"oldText": "uniq", "newText": "U"},
			{"oldText": "foo", "newText": "bar"},
		})
		want := "Found 2 occurrences of edits[1] in file.txt. Each oldText must be unique. Please provide more context to make it unique."
		if !isErr || out != want {
			t.Fatalf("duplicate multi mismatch:\n got: %q\nwant: %q", out, want)
		}
	})

	t.Run("no-change-single", func(t *testing.T) {
		out, isErr, _ := runEdit(t, "same\n", []map[string]any{
			{"oldText": "same", "newText": "same"},
		})
		want := "No changes made to file.txt. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected."
		if !isErr || out != want {
			t.Fatalf("no-change single mismatch:\n got: %q\nwant: %q", out, want)
		}
	})

	t.Run("no-change-multi", func(t *testing.T) {
		out, isErr, _ := runEdit(t, "same\nalso\n", []map[string]any{
			{"oldText": "same", "newText": "same"},
			{"oldText": "also", "newText": "also"},
		})
		want := "No changes made to file.txt. The replacements produced identical content."
		if !isErr || out != want {
			t.Fatalf("no-change multi mismatch:\n got: %q\nwant: %q", out, want)
		}
	})

	t.Run("access-error-code", func(t *testing.T) {
		cwd := t.TempDir()
		res := EditTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"path":  "missing.txt",
			"edits": []map[string]any{{"oldText": "a", "newText": "b"}},
		}), nil)
		out := toolText(res.Content)
		want := "Could not edit file: missing.txt. Error code: ENOENT."
		if !res.IsError || out != want {
			t.Fatalf("access error mismatch:\n got: %q\nwant: %q", out, want)
		}
	})
}
