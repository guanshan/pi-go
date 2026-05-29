package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFooterDataProviderBranchStatusesAndProviderCount(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(repo, "sub")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	provider := NewFooterDataProvider(child)
	branch, ok := provider.GetGitBranch()
	if !ok || branch != "main" {
		t.Fatalf("branch=%q ok=%v", branch, ok)
	}

	called := 0
	unsubscribe := provider.OnBranchChange(func() { called++ })
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider.RefreshGitBranch()
	branch, ok = provider.GetGitBranch()
	if !ok || branch != "feature" || called != 1 {
		t.Fatalf("branch=%q ok=%v called=%d", branch, ok, called)
	}
	unsubscribe()
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider.RefreshGitBranch()
	branch, ok = provider.GetGitBranch()
	if !ok || branch != "detached" || called != 1 {
		t.Fatalf("detached branch=%q ok=%v called=%d", branch, ok, called)
	}

	provider.SetExtensionStatus("ext", "ready")
	statuses := provider.GetExtensionStatuses()
	statuses["ext"] = "mutated"
	if provider.GetExtensionStatuses()["ext"] != "ready" {
		t.Fatal("extension status map should be copied")
	}
	provider.SetExtensionStatus("ext", "")
	if _, found := provider.GetExtensionStatuses()["ext"]; found {
		t.Fatal("extension status should be cleared by empty text")
	}
	provider.SetAvailableProviderCount(3)
	if provider.GetAvailableProviderCount() != 3 {
		t.Fatalf("provider count=%d", provider.GetAvailableProviderCount())
	}
}

func TestFooterDataProviderWorktreeGitFileAndSetCwd(t *testing.T) {
	mainRepo := t.TempDir()
	mainGit := filepath.Join(mainRepo, ".git")
	if err := os.MkdirAll(filepath.Join(mainGit, "worktrees", "wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainGit, "worktrees", "wt", "HEAD"), []byte("ref: refs/heads/work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainGit, "worktrees", "wt", "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+filepath.Join(mainGit, "worktrees", "wt")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()

	provider := NewFooterDataProvider(worktree)
	branch, ok := provider.GetGitBranch()
	if !ok || branch != "work" {
		t.Fatalf("branch=%q ok=%v", branch, ok)
	}
	called := 0
	provider.OnBranchChange(func() { called++ })
	provider.SetCwd(other)
	if called != 1 {
		t.Fatalf("set cwd callback count=%d", called)
	}
	if branch, ok := provider.GetGitBranch(); ok || branch != "" {
		t.Fatalf("branch outside repo=%q ok=%v", branch, ok)
	}
	provider.Dispose()
	provider.SetCwd(worktree)
	if called != 1 {
		t.Fatalf("disposed provider should not call callbacks: %d", called)
	}
}

func TestProviderDisplayName(t *testing.T) {
	if ProviderDisplayName("google-vertex") != "Google Vertex AI" {
		t.Fatal("missing built-in provider display name")
	}
	if ProviderDisplayName("custom-provider") != "custom-provider" {
		t.Fatal("custom provider should fall back to slug")
	}
}
