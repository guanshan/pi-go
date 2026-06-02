package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestTypeScriptLegacyV1SessionFixtureMigratesToCurrentJSONL(t *testing.T) {
	path := copySessionFixture(t, "ts_legacy_v1.jsonl")
	session, err := OpenSession(path, "/fallback")
	if err != nil {
		t.Fatal(err)
	}
	if session.Header.Version != CurrentSessionVersion {
		t.Fatalf("version=%d, want %d", session.Header.Version, CurrentSessionVersion)
	}
	if len(session.Entries) != 3 {
		t.Fatalf("entries=%d", len(session.Entries))
	}
	for i, entry := range session.Entries {
		if entry.ID == "" {
			t.Fatalf("entry %d has no id: %#v", i, entry)
		}
		if i == 0 && entry.ParentID != nil {
			t.Fatalf("first entry parent=%v", *entry.ParentID)
		}
		if i > 0 && (entry.ParentID == nil || *entry.ParentID != session.Entries[i-1].ID) {
			t.Fatalf("entry %d parent=%v previous=%s", i, entry.ParentID, session.Entries[i-1].ID)
		}
	}
	if got := ai.MessageRole(session.Entries[1].Message); got != "custom" {
		t.Fatalf("legacy hookMessage role=%q, want custom", got)
	}
	compaction := session.Entries[2]
	if compaction.FirstKeptID != session.Entries[0].ID || compaction.FirstKeptEntryIndex != nil {
		t.Fatalf("compaction firstKeptID=%q firstKeptEntryIndex=%v firstEntryID=%q", compaction.FirstKeptID, compaction.FirstKeptEntryIndex, session.Entries[0].ID)
	}
}

func TestTypeScriptBranchAndCompactionFixtureBuildsExpectedContexts(t *testing.T) {
	path := copySessionFixture(t, "ts_branch_compaction_v3.jsonl")
	session, err := OpenSession(path, "/fallback")
	if err != nil {
		t.Fatal(err)
	}

	ctx := session.BuildContext()
	if got := messageRoles(ctx.Messages); !reflect.DeepEqual(got, []string{"user", "assistant", "user", "branchSummary", "user"}) {
		t.Fatalf("default branch roles=%v", got)
	}
	if got := messageTexts(ctx.Messages); !reflect.DeepEqual(got, []string{"start", "r1", "q2", "Tried wrong approach", "better approach"}) {
		t.Fatalf("default branch texts=%v", got)
	}
	if ctx.ModelProvider != "anthropic" || ctx.ModelID != "claude-test" {
		t.Fatalf("model context=%s/%s", ctx.ModelProvider, ctx.ModelID)
	}
	if len(session.Branch()) != 6 {
		t.Fatalf("default branch entries=%#v", session.Branch())
	}

	compactedLeaf := "7"
	session.CurrentID = &compactedLeaf
	ctx = session.BuildContext()
	if got := messageRoles(ctx.Messages); !reflect.DeepEqual(got, []string{"compactionSummary", "user", "assistant", "user", "assistant"}) {
		t.Fatalf("compacted branch roles=%v", got)
	}
	if got := messageTexts(ctx.Messages); !reflect.DeepEqual(got, []string{"Compacted history", "q2", "r2", "q3", "r3"}) {
		t.Fatalf("compacted branch texts=%v", got)
	}
}

func TestSessionInfoUsesLastUserAssistantTimestampLikeTypeScript(t *testing.T) {
	cwd := t.TempDir()
	sessionDir := t.TempDir()
	session, err := NewSessionManagerWithID(cwd, sessionDir, "modified-fixture")
	if err != nil {
		t.Fatal(err)
	}
	msgTime := int64(1_735_862_400_123)
	if err := session.AppendMessage(ai.AssistantMessage{
		Role:        "assistant",
		Content:     ai.TextBlocks("later"),
		API:         "openai-completions",
		Provider:    "openai",
		Model:       "test",
		Usage:       ai.Usage{Input: 1, Output: 1, TotalTokens: 2},
		StopReason:  "stop",
		TimestampMs: msgTime,
	}); err != nil {
		t.Fatal(err)
	}
	future := time.UnixMilli(msgTime).Add(24 * time.Hour)
	if err := os.Chtimes(session.File(), future, future); err != nil {
		t.Fatal(err)
	}

	infos, err := ListSessions(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("infos=%#v", infos)
	}
	if got := infos[0].UpdatedAt.UnixMilli(); got != msgTime {
		t.Fatalf("updatedAt=%d, want message timestamp %d", got, msgTime)
	}
}

// TestLoadSessionFileOpensEntriesLargerThanScannerCap proves a single entry
// larger than the old bufio.Scanner 10MB per-line cap (a big base64 image or a
// large paste) no longer makes the whole session unopenable. Mirrors the
// TypeScript "opens session files larger than Node's max string length" test in
// packages/coding-agent/test/session-manager/file-operations.test.ts.
func TestLoadSessionFileOpensEntriesLargerThanScannerCap(t *testing.T) {
	// ~11MB of text in a single entry's content field, well past the old 10MB
	// per-line scanner limit. Built in memory; never committed as a fixture.
	big := strings.Repeat("x", 11*1024*1024)

	header := `{"type":"session","version":3,"id":"abc","timestamp":"2025-01-01T00:00:00Z","cwd":"/tmp"}`
	msg, err := json.Marshal(ai.UserMessage{Role: "user", Content: ai.TextBlocks(big), TimestampMs: 1})
	if err != nil {
		t.Fatal(err)
	}
	entry := `{"type":"message","id":"1","parentId":null,"timestamp":"2025-01-01T00:00:01Z","message":` + string(msg) + `}`

	path := filepath.Join(t.TempDir(), "large.jsonl")
	// No trailing newline on the final line: a file may end without one.
	if err := os.WriteFile(path, []byte(header+"\n"+entry), 0o600); err != nil {
		t.Fatal(err)
	}

	loadedHeader, entries, err := loadSessionFile(path, "/fallback")
	if err != nil {
		t.Fatalf("loadSessionFile returned error: %v", err)
	}
	if loadedHeader.ID != "abc" {
		t.Fatalf("header id=%q, want abc", loadedHeader.ID)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d, want 1", len(entries))
	}
	if got := ai.MessageText(entries[0].Message); len(got) != len(big) {
		t.Fatalf("loaded message text length=%d, want %d", len(got), len(big))
	}
}

func copySessionFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "session", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func messageRoles(messages []ai.Message) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, ai.MessageRole(message))
	}
	return out
}

func messageTexts(messages []ai.Message) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, ai.MessageText(message))
	}
	return out
}
