//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindToolUsesFdWhenAvailable(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	logPath := filepath.Join(root, "fd.args")
	fdPath := filepath.Join(bin, "fd")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FD_ARGS_LOG\"\nprintf 'from-fd.txt\\n'\n"
	if err := os.WriteFile(fdPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FD_ARGS_LOG", logPath)

	res := FindTool{CWD: root, BinDir: bin}.Execute(context.Background(), raw(map[string]any{"pattern": "*.txt"}), nil)
	if res.IsError {
		t.Fatalf("find returned error: %s", toolText(res.Content))
	}
	if got := strings.TrimSpace(toolText(res.Content)); got != "from-fd.txt" {
		t.Fatalf("find output=%q, want fake fd output", got)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("fd was not invoked: %v", err)
	}
	for _, want := range []string{"--hidden", "--glob", "--no-require-git", "--exclude", ".git", "*.txt", "."} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("fd args missing %q: %s", want, args)
		}
	}
	if strings.Contains(string(args), "--full-path") {
		t.Fatalf("basename glob should not request --full-path: %s", args)
	}
}

func TestFindToolFdUsesFullPathForPathGlob(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	logPath := filepath.Join(root, "fd.args")
	fdPath := filepath.Join(bin, "fd")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FD_ARGS_LOG\"\nprintf 'src/app.ts\\n'\n"
	if err := os.WriteFile(fdPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FD_ARGS_LOG", logPath)

	res := FindTool{CWD: root, BinDir: bin}.Execute(context.Background(), raw(map[string]any{"pattern": "src/**/*.ts"}), nil)
	if res.IsError {
		t.Fatalf("find returned error: %s", toolText(res.Content))
	}
	if got := strings.TrimSpace(toolText(res.Content)); got != "src/app.ts" {
		t.Fatalf("find output=%q, want fake fd output", got)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("fd was not invoked: %v", err)
	}
	for _, want := range []string{"--glob", "--no-require-git", "--full-path", "."} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("fd args missing %q: %s", want, args)
		}
	}
}

// fdArgLines runs find against a fake fd that records each argument on its own
// line and returns those argument lines.
func fdArgLines(t *testing.T, pattern string) []string {
	t.Helper()
	root := t.TempDir()
	bin := t.TempDir()
	logPath := filepath.Join(root, "fd.args")
	fdPath := filepath.Join(bin, "fd")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FD_ARGS_LOG\"\n"
	if err := os.WriteFile(fdPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FD_ARGS_LOG", logPath)

	res := FindTool{CWD: root, BinDir: bin}.Execute(context.Background(), raw(map[string]any{"pattern": pattern}), nil)
	if res.IsError {
		t.Fatalf("find returned error: %s", toolText(res.Content))
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("fd was not invoked: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	return lines
}

// effectivePattern returns the glob fd was actually given (the token immediately
// after the "--" separator) and whether --full-path was requested.
func effectivePattern(t *testing.T, lines []string) (string, bool) {
	t.Helper()
	fullPath := false
	pattern := ""
	for i, a := range lines {
		if a == "--full-path" {
			fullPath = true
		}
		if a == "--" {
			if i+1 >= len(lines) {
				t.Fatalf("no pattern after -- separator in %v", lines)
			}
			pattern = lines[i+1]
		}
	}
	if pattern == "" {
		t.Fatalf("no -- separator in fd args %v", lines)
	}
	return pattern, fullPath
}

// TestFindToolFdEffectivePattern asserts the exact glob handed to fd: path globs
// get --full-path plus a leading **/ so they match under fd's full-path mode,
// while basename globs and already-anchored **/ globs are passed through unchanged.
func TestFindToolFdEffectivePattern(t *testing.T) {
	cases := []struct {
		name         string
		pattern      string
		wantPattern  string
		wantFullPath bool
	}{
		{"path glob is anchored", "src/**/*.ts", "**/src/**/*.ts", true},
		{"basename glob untouched", "*.ts", "*.ts", false},
		{"already anchored not double-prefixed", "**/foo.ts", "**/foo.ts", true},
		{"absolute pattern untouched", "/abs/foo.ts", "/abs/foo.ts", true},
		{"double-star wildcard untouched", "**", "**", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := fdArgLines(t, tc.pattern)
			gotPattern, gotFullPath := effectivePattern(t, lines)
			if gotPattern != tc.wantPattern {
				t.Fatalf("effective pattern = %q, want %q (args %v)", gotPattern, tc.wantPattern, lines)
			}
			if gotFullPath != tc.wantFullPath {
				t.Fatalf("--full-path = %v, want %v (args %v)", gotFullPath, tc.wantFullPath, lines)
			}
		})
	}
}

// TestFindToolFdPassesMaxResults asserts fd is given --max-results so it can
// self-terminate, mirroring the TS implementation.
func TestFindToolFdPassesMaxResults(t *testing.T) {
	lines := fdArgLines(t, "*.ts")
	found := false
	for i, a := range lines {
		if a == "--max-results" {
			if i+1 >= len(lines) {
				t.Fatalf("--max-results missing value in %v", lines)
			}
			if lines[i+1] != "1000" {
				t.Fatalf("--max-results = %q, want default 1000 (args %v)", lines[i+1], lines)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("fd args missing --max-results: %v", lines)
	}
}
