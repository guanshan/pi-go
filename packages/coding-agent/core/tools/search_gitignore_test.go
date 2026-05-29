package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildSearchTree creates a tree exercising .gitignore, nested ignores, hidden
// files, and .git pruning, and returns the root.
func buildSearchTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "*.log\nignored_dir/\n")
	write("keep.go", "FINDME here\n")
	write("app.log", "FINDME in ignored log\n")
	write(".hidden.txt", "FINDME hidden but not ignored\n")
	write("ignored_dir/x.go", "FINDME in ignored dir\n")
	write("sub/.gitignore", "nested.txt\n")
	write("sub/other.go", "FINDME in sub\n")
	write("sub/nested.txt", "FINDME nested ignored\n")
	write(".git/config", "FINDME in git internals\n")
	return root
}

func TestFindRespectsGitignore(t *testing.T) {
	root := buildSearchTree(t)
	res := FindTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "*.go"}), nil)
	text := toolText(res.Content)
	if !strings.Contains(text, "keep.go") || !strings.Contains(text, "other.go") {
		t.Fatalf("expected keep.go and sub/other.go, got: %s", text)
	}
	if strings.Contains(text, "ignored_dir") {
		t.Fatalf("ignored_dir should be excluded: %s", text)
	}
}

func TestFindIncludesHiddenButExcludesGit(t *testing.T) {
	root := buildSearchTree(t)
	res := FindTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "*.txt"}), nil)
	text := toolText(res.Content)
	if !strings.Contains(text, ".hidden.txt") {
		t.Fatalf("hidden file should be found (rg/fd --hidden semantics): %s", text)
	}
	if strings.Contains(text, "nested.txt") {
		t.Fatalf("nested.txt should be excluded by sub/.gitignore: %s", text)
	}
	if strings.Contains(text, "config") {
		t.Fatalf(".git contents must never be returned: %s", text)
	}
}

func TestGrepRespectsGitignore(t *testing.T) {
	root := buildSearchTree(t)
	res := GrepTool{CWD: root}.Execute(context.Background(), raw(map[string]any{"pattern": "FINDME"}), nil)
	text := toolText(res.Content)
	if !strings.Contains(text, "keep.go") || !strings.Contains(text, "other.go") || !strings.Contains(text, ".hidden.txt") {
		t.Fatalf("expected matches in keep.go, sub/other.go, .hidden.txt: %s", text)
	}
	for _, excluded := range []string{"app.log", "ignored_dir", "nested.txt", "config"} {
		if strings.Contains(text, excluded) {
			t.Fatalf("%s should be excluded by ignore/.git rules: %s", excluded, text)
		}
	}
}
