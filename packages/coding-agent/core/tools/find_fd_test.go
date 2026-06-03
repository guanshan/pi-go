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
	for _, want := range []string{"--hidden", "--glob", "--exclude", ".git", "*.txt", "."} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("fd args missing %q: %s", want, args)
		}
	}
}
