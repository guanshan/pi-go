package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoverContextFilesMatchesTSOrderingAndVariants mirrors
// resource-loader.ts:57-112: each dir yields only its first matching candidate
// (all four casings), the global agent dir comes first, ancestors are ordered
// root->cwd, and the list is not re-sorted.
func TestDiscoverContextFilesMatchesTSOrderingAndVariants(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	proj := filepath.Join(tmp, "proj")
	work := filepath.Join(proj, "work")
	for _, d := range []string{agentDir, proj, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("ctx"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	agentClaude := write(agentDir, "CLAUDE.md")
	projAgentsMD := write(proj, "AGENTS.MD") // uppercase variant
	workAgents := write(work, "AGENTS.md")
	write(work, "CLAUDE.md") // both present in cwd: only AGENTS.md (first match) must win

	got := discoverContextFiles(work, agentDir)

	want := []string{agentClaude, projAgentsMD, workAgents}
	if len(got) != len(want) {
		t.Fatalf("got %d files %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch at %d:\n got: %v\nwant: %v", i, got, want)
		}
	}
	for _, p := range got {
		if filepath.Base(p) == "CLAUDE.md" && filepath.Dir(p) == work {
			t.Fatalf("cwd CLAUDE.md must not load when AGENTS.md exists in the same dir; got %v", got)
		}
	}
}
