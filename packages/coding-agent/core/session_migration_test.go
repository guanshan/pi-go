package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// TestOpenSessionMigratesLegacyV1 verifies that opening a legacy v1 session
// (no entry ids, hookMessage role, compaction firstKeptEntryIndex) migrates it to
// the current version in memory and rewrites the file on disk.
func TestOpenSessionMigratesLegacyV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.jsonl")
	content := `{"type":"session","version":1,"id":"s1","timestamp":"2026-05-27T00:00:00Z","cwd":"/tmp"}` + "\n" +
		`{"type":"message","message":{"role":"hookMessage","content":"legacy"}}` + "\n" +
		`{"type":"compaction","summary":"old","firstKeptEntryIndex":1,"tokensBefore":10}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	sm, err := OpenSession(path, "/fallback")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if sm.Header.Version != CurrentSessionVersion {
		t.Fatalf("header version=%d", sm.Header.Version)
	}
	if len(sm.Entries) != 2 {
		t.Fatalf("entries=%d", len(sm.Entries))
	}
	msg, comp := sm.Entries[0], sm.Entries[1]
	if msg.ID == "" || comp.ParentID == nil || *comp.ParentID != msg.ID {
		t.Fatalf("ids: msg=%#v comp=%#v", msg, comp)
	}
	if ai.MessageRole(msg.Message) != "custom" {
		t.Fatalf("hookMessage not migrated: role=%q", ai.MessageRole(msg.Message))
	}
	if comp.FirstKeptID != msg.ID || comp.FirstKeptEntryIndex != nil {
		t.Fatalf("compaction not migrated: %#v", comp)
	}

	// The file should have been rewritten at the current version.
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), `"fromHook"`) {
		t.Fatalf("legacy migration should preserve missing fromHook field:\n%s", rewritten)
	}
	reopened, err := OpenSession(path, "/fallback")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Header.Version != CurrentSessionVersion {
		t.Fatalf("rewritten header version=%d", reopened.Header.Version)
	}
}

// TestOpenSessionSkipsMalformedLines verifies the loader skips a malformed
// (partially written) entry line instead of failing the whole load.
func TestOpenSessionSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.jsonl")
	content := `{"type":"session","version":3,"id":"s2","timestamp":"2026-05-27T00:00:00Z","cwd":"/tmp"}` + "\n" +
		`{"type":"message","id":"a","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n" +
		`{"type":"message","id":"b","message":{"role":"user"` + "\n" // truncated final line
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	sm, err := OpenSession(path, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(sm.Entries) != 1 {
		t.Fatalf("expected malformed line skipped, got %d entries", len(sm.Entries))
	}
	if sm.Entries[0].ID != "a" {
		t.Fatalf("entry id=%q", sm.Entries[0].ID)
	}
}

// TestOpenSessionMissingHeader verifies a file without a session header is an error.
func TestOpenSessionMissingHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noheader.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"message","id":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession(path, ""); err == nil {
		t.Fatal("expected missing-header error")
	}
}
