package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests lock the model-visible output formats of grep/find to match the
// TypeScript originals (grep.ts ~255-356, find.ts ~300-333, truncate.ts ~268-275).

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGrepMatchLineFormat: a match line renders as "path:N: text" (colon + space).
func TestGrepMatchLineFormat(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "first\nNEEDLE here\nthird\n")
	res := GrepTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "NEEDLE"}), nil)
	got := toolText(res.Content)
	want := "a.txt:2: NEEDLE here"
	if got != want {
		t.Fatalf("match line format mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestGrepContextLineFormat: context lines render as "path-N- text" while the
// match line keeps "path:N: text".
func TestGrepContextLineFormat(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "line1\nline2\nNEEDLE\nline4\nline5\n")
	res := GrepTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "NEEDLE", "context": 1}), nil)
	got := toolText(res.Content)
	want := strings.Join([]string{
		"a.txt-2- line2",
		"a.txt:3: NEEDLE",
		"a.txt-4- line4",
	}, "\n")
	if got != want {
		t.Fatalf("context block format mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestGrepLongLineTruncationSuffix: a line longer than GrepMaxLineLength is
// truncated with the "... [truncated]" suffix and a notice is appended.
func TestGrepLongLineTruncationSuffix(t *testing.T) {
	root := t.TempDir()
	long := "NEEDLE" + strings.Repeat("x", GrepMaxLineLength+50)
	writeFile(t, root, "a.txt", long+"\n")
	res := GrepTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "NEEDLE"}), nil)
	got := toolText(res.Content)

	wantBody := "a.txt:1: " + utf16Slice(utf16Units(long), 0, GrepMaxLineLength) + "... [truncated]"
	wantNotice := "[Some lines truncated to 500 chars. Use read tool to see full lines]"
	want := wantBody + "\n\n" + wantNotice
	if got != want {
		t.Fatalf("long-line truncation mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestGrepTruncatesByUTF16CodeUnits locks the truncate.ts semantics: the cut
// point is measured in UTF-16 code units, so a non-BMP character (2 units) near
// the boundary is dropped whole rather than counted as a single rune.
func TestGrepTruncatesByUTF16CodeUnits(t *testing.T) {
	root := t.TempDir()
	// "NEEDLE" is 6 units; then enough emoji (each 2 UTF-16 units, 1 rune) so the
	// line exceeds GrepMaxLineLength in UTF-16 units. A line built from emoji
	// truncates at a different rune index than UTF-16 index.
	body := "NEEDLE" + strings.Repeat("\U0001F600", GrepMaxLineLength)
	writeFile(t, root, "a.txt", body+"\n")
	res := GrepTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "NEEDLE"}), nil)
	got := toolText(res.Content)

	units := utf16Units(body)
	if len(units) <= GrepMaxLineLength {
		t.Fatalf("test setup: body must exceed %d UTF-16 units, got %d", GrepMaxLineLength, len(units))
	}
	wantBody := "a.txt:1: " + utf16Slice(units, 0, GrepMaxLineLength) + "... [truncated]"
	wantNotice := "[Some lines truncated to 500 chars. Use read tool to see full lines]"
	want := wantBody + "\n\n" + wantNotice
	if got != want {
		t.Fatalf("UTF-16 truncation mismatch\n got: %q\nwant: %q", got, want)
	}
	// Sanity: a rune-count truncation (the old, wrong behavior) would slice the
	// rune slice at GrepMaxLineLength, producing a different body.
	runeBody := "a.txt:1: " + string([]rune(body)[:GrepMaxLineLength]) + "... [truncated]"
	if want == runeBody+"\n\n"+wantNotice {
		t.Fatalf("test does not distinguish rune vs UTF-16 semantics")
	}
}

// TestGrepMatchLimitNotice locks the match-limit notice wording (and that the
// suffix is plain "... [truncated]" is covered separately).
func TestGrepMatchLimitNotice(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("NEEDLE\n")
	}
	writeFile(t, root, "a.txt", b.String())
	res := GrepTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "NEEDLE", "limit": 2}), nil)
	got := toolText(res.Content)
	want := strings.Join([]string{
		"a.txt:1: NEEDLE",
		"a.txt:2: NEEDLE",
		"",
		"[2 matches limit reached. Use limit=4 for more, or refine pattern]",
	}, "\n")
	if got != want {
		t.Fatalf("match limit notice mismatch\n got: %q\nwant: %q", got, want)
	}
}

// truncateLine should report truncation and append "... [truncated]".
func TestTruncateLineSuffix(t *testing.T) {
	short := "hello"
	if got, was := truncateLine(short, 10); got != short || was {
		t.Fatalf("short line should be untouched: got=%q was=%v", got, was)
	}
	got, was := truncateLine("abcdefghij", 4)
	if !was {
		t.Fatalf("expected wasTruncated=true")
	}
	if got != "abcd... [truncated]" {
		t.Fatalf("suffix mismatch: %q", got)
	}
}

// TestFindMatchesDirectories: fd lists directories by default; find must be able
// to match a directory and emit it with a trailing slash.
func TestFindMatchesDirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "components/button.go", "x\n")
	writeFile(t, root, "components/input.go", "y\n")
	res := FindTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "components"}), nil)
	got := toolText(res.Content)
	if got != "components/" {
		t.Fatalf("directory match mismatch\n got: %q\nwant: %q", got, "components/")
	}
}

// TestFindMatchesDirectoryAndFile: a glob that matches both directory and file
// basenames returns both, the directory carrying a trailing slash.
func TestFindMatchesDirectoryAndFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/app.go", "x\n")
	// A directory named "build" and a file "build" would collide, so use a glob.
	writeFile(t, root, "build/out.txt", "y\n")
	res := FindTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "*"}), nil)
	got := toolText(res.Content)
	lines := strings.Split(got, "\n")
	hasDirWithSlash := false
	for _, l := range lines {
		if l == "src/" || l == "build/" {
			hasDirWithSlash = true
		}
	}
	if !hasDirWithSlash {
		t.Fatalf("expected at least one directory with trailing slash, got: %q", got)
	}
}

// TestFindTraversalOrderNotSorted: find must not force lexical sort; output
// should follow traversal order (which differs from sort.Strings here).
func TestFindTraversalOrderNotSorted(t *testing.T) {
	root := t.TempDir()
	// Create files whose traversal order differs from a global lexical sort of
	// the relative paths. WalkDir descends "a" fully before visiting "z.txt"
	// (lexical within a dir), so the order is: a/, a/m.txt, z.txt.
	writeFile(t, root, "z.txt", "1\n")
	writeFile(t, root, "a/m.txt", "2\n")
	res := FindTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "*"}), nil)
	got := toolText(res.Content)
	want := strings.Join([]string{"a/", "a/m.txt", "z.txt"}, "\n")
	if got != want {
		t.Fatalf("traversal order mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestFindLimitReachedMessage locks the limit-reached notice wording, matching
// find.ts (~321-324).
func TestFindLimitReachedMessage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "1\n")
	writeFile(t, root, "b.txt", "2\n")
	writeFile(t, root, "c.txt", "3\n")
	res := FindTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "*.txt", "limit": 2}), nil)
	got := toolText(res.Content)
	if !strings.HasSuffix(got, "\n\n[2 results limit reached. Use limit=4 for more, or refine pattern]") {
		t.Fatalf("limit message mismatch, got: %q", got)
	}
}

// TestGrepDirectoryRelativePrefix: when searching a directory, the path prefix
// is the slash-joined relative path (mirrors formatPath in grep.ts).
func TestGrepDirectoryRelativePrefix(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sub/dir/file.go", "alpha\nNEEDLE\nbeta\n")
	res := GrepTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "NEEDLE"}), nil)
	got := toolText(res.Content)
	want := "sub/dir/file.go:2: NEEDLE"
	if got != want {
		t.Fatalf("relative prefix mismatch\n got: %q\nwant: %q", got, want)
	}
}
