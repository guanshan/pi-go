package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSlashChangelog(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("## [2.0.0]\n- hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := &AgentSession{Session: InMemorySession(cwd)}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if _, err := handleSlash(context.Background(), agent, "/changelog CHANGELOG.md", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	for _, want := range []string{"What's New", "## [2.0.0]", "- hello"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("/changelog output missing %q: %q", want, stdout.String())
		}
	}
}
