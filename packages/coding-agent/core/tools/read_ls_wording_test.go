package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadTruncationWording verifies the line-truncation continuation notice
// includes "of TOTAL" (TS read.ts:312).
func TestReadTruncationWording(t *testing.T) {
	cwd := t.TempDir()
	total := DefaultMaxLines + 50
	var b strings.Builder
	for i := 0; i < total; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	if err := os.WriteFile(filepath.Join(cwd, "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	res := ReadTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "big.txt"}), nil)
	out := toolText(res.Content)
	if res.IsError {
		t.Fatalf("read failed: %s", out)
	}
	// total lines = DefaultMaxLines + 50 content lines, plus 1 from the trailing newline split.
	wantSubstr := fmt.Sprintf("[Showing lines 1-%d of %d. Use offset=%d to continue.]", DefaultMaxLines, total+1, DefaultMaxLines+1)
	if !strings.Contains(out, wantSubstr) {
		t.Fatalf("missing/incorrect continuation notice:\n got tail: %q\nwant substr: %q", lastLine(out), wantSubstr)
	}
}

// TestReadFirstLineExceedsWording verifies the first-line-too-large notice
// reports the offending line's size (TS read.ts:303-304).
func TestReadFirstLineExceedsWording(t *testing.T) {
	cwd := t.TempDir()
	huge := strings.Repeat("a", DefaultMaxBytes+100)
	if err := os.WriteFile(filepath.Join(cwd, "huge.txt"), []byte(huge+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := ReadTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "huge.txt"}), nil)
	out := toolText(res.Content)
	if res.IsError {
		t.Fatalf("read failed: %s", out)
	}
	wantPrefix := fmt.Sprintf("[Line 1 is %s, exceeds %s limit. Use bash:", FormatSize(len(huge)), FormatSize(DefaultMaxBytes))
	if !strings.HasPrefix(out, wantPrefix) {
		t.Fatalf("first-line-exceeds notice mismatch:\n got: %q\nwant prefix: %q", out, wantPrefix)
	}
}

func TestLsErrorWording(t *testing.T) {
	cwd := t.TempDir()

	t.Run("path-not-found", func(t *testing.T) {
		res := LsTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "nope"}), nil)
		out := toolText(res.Content)
		want := "Path not found: " + filepath.Join(cwd, "nope")
		if !res.IsError || out != want {
			t.Fatalf("path-not-found mismatch:\n got: %q\nwant: %q", out, want)
		}
	})

	t.Run("not-a-directory", func(t *testing.T) {
		fpath := filepath.Join(cwd, "afile.txt")
		if err := os.WriteFile(fpath, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		res := LsTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "afile.txt"}), nil)
		out := toolText(res.Content)
		want := "Not a directory: " + fpath
		if !res.IsError || out != want {
			t.Fatalf("not-a-directory mismatch:\n got: %q\nwant: %q", out, want)
		}
	})
}

// TestLsSkipsUnstatableEntries verifies that ls drops entries it cannot stat
// (e.g. a dangling symlink), matching TS ls.ts:166-168 `catch { continue }`.
func TestLsSkipsUnstatableEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink whose target does not exist -> os.Stat (which follows the link)
	// fails, so the entry must be skipped.
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "dangling")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	res := LsTool{CWD: dir}.Execute(context.Background(), raw(map[string]any{"path": "."}), nil)
	if res.IsError {
		t.Fatalf("ls failed: %s", toolText(res.Content))
	}
	out := toolText(res.Content)
	if !strings.Contains(out, "real.txt") {
		t.Fatalf("expected real.txt in output, got: %q", out)
	}
	if strings.Contains(out, "dangling") {
		t.Fatalf("expected dangling symlink to be skipped, got: %q", out)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}
