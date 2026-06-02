package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAppendStatusPreservesTrailingNewline verifies bash status concatenation
// matches TS bash.ts:370 `${text ? `${text}\n\n` : ""}${status}` — the command
// output is kept verbatim (trailing newlines NOT trimmed), and an empty output
// yields just the status line.
func TestAppendStatusPreservesTrailingNewline(t *testing.T) {
	if got := appendStatus("", "Command aborted"); got != "Command aborted" {
		t.Fatalf("empty text: got %q", got)
	}
	if got := appendStatus("done", "Command exited with code 1"); got != "done\n\nCommand exited with code 1" {
		t.Fatalf("plain text: got %q", got)
	}
	// Trailing newlines in the output must be preserved (old code trimmed them).
	if got := appendStatus("out\n", "Command exited with code 1"); got != "out\n\n\nCommand exited with code 1" {
		t.Fatalf("trailing newline not preserved: got %q", got)
	}
}

// TestLsFollowsSymlinkToDirectory verifies ls marks a symlink that points to a
// directory with a trailing slash, matching TS which stats the full path
// (ls.ts:159-166) rather than using the (non-following) dirent type.
func TestLsFollowsSymlinkToDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation typically requires privilege on Windows")
	}
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(cwd, "target"), filepath.Join(cwd, "link")); err != nil {
		t.Fatal(err)
	}
	res := LsTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "."}), nil)
	out := toolText(res.Content)
	if res.IsError {
		t.Fatalf("ls failed: %s", out)
	}
	lines := strings.Split(out, "\n")
	var sawLink bool
	for _, l := range lines {
		if l == "link/" {
			sawLink = true
		}
	}
	if !sawLink {
		t.Fatalf("expected symlink-to-dir to render as %q, got:\n%s", "link/", out)
	}
}
