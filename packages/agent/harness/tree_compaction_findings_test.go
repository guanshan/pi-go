package harness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

// R3-P2-2: navigating to the entry that is already the current leaf is a no-op:
// the entry count must be unchanged and no session_tree / branch_summary work
// happens (the hook is never even consulted).
func TestAgentHarnessNavigateTreeToCurrentLeafIsNoOp(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil)); err != nil {
		t.Fatal(err)
	}
	leafID, err := sess.AppendMessage(ctx, ai.NewUserMessage("leaf", nil))
	if err != nil {
		t.Fatal(err)
	}
	before, err := sess.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{Session: sess})
	if err != nil {
		t.Fatal(err)
	}
	var sawBefore, sawTree bool
	h.OnSessionBeforeTree(func(ctx context.Context, ev SessionBeforeTreeEvent) (*SessionBeforeTreeResult, error) {
		sawBefore = true
		return nil, nil
	})
	h.OnSessionTree(func(ctx context.Context, ev SessionTreeEvent) error {
		sawTree = true
		return nil
	})
	result, err := h.NavigateTree(ctx, leafID, NavigateTreeOptions{UserWantsSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Canceled {
		t.Fatalf("expected not canceled, got %#v", result)
	}
	if sawBefore || sawTree {
		t.Fatalf("expected no hooks/events, before=%v tree=%v", sawBefore, sawTree)
	}
	after, err := sess.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("entry count changed: before=%d after=%d", len(before), len(after))
	}
	for _, entry := range after {
		if entry.EntryType() == "branch_summary" {
			t.Fatalf("unexpected branch_summary entry appended: %#v", entry)
		}
	}
}

// R3-P2-3: when an auto-generated branch summary is used, the branch_summary
// entry's details must carry the {readFiles, modifiedFiles} arrays derived from
// the generated summary, and they must survive a JSONL round-trip.
func TestAgentHarnessNavigateTreeGeneratedSummaryPersistsFileDetails(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	storage, err := session.CreateJSONLStorage(ctx, path, session.JSONLMetadata{
		Metadata: session.Metadata{ID: "s1", CreatedAt: "2026-05-28T00:00:00Z"},
		Cwd:      "/work",
		Path:     path,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := session.New(storage)

	dir := t.TempDir()
	registry := ai.NewModelRegistry(dir, ai.NewAuthStorage(dir))
	model, ok := registry.Find("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}

	rootID, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil))
	if err != nil {
		t.Fatal(err)
	}
	// The "old" branch performs file operations the generated summary should record.
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("explore", nil)); err != nil {
		t.Fatal(err)
	}
	assistant := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
		{Type: "toolCall", ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"main.go"}`)},
		{Type: "toolCall", ID: "c2", Name: "edit", Arguments: json.RawMessage(`{"path":"agent.go"}`)},
	}, ai.Usage{}, "toolUse")
	oldLeafID, err := sess.AppendMessage(ctx, assistant)
	if err != nil {
		t.Fatal(err)
	}
	// Move back to root and create a different branch as the navigation target.
	if _, err := sess.MoveTo(ctx, &rootID); err != nil {
		t.Fatal(err)
	}
	targetID, err := sess.AppendMessage(ctx, ai.NewUserMessage("target", nil))
	if err != nil {
		t.Fatal(err)
	}
	// Sit on the "old" branch so navigating to the target branch is not a no-op
	// (navigateTree returns immediately when oldLeaf == target) and the old
	// branch's file operations are what gets summarized.
	if _, err := sess.MoveTo(ctx, &oldLeafID); err != nil {
		t.Fatal(err)
	}

	h, err := New(Options{
		Session:  sess,
		Registry: registry,
		Model:    model,
		GetAPIKeyAndHeaders: func(ctx context.Context, model ai.Model) (APIKeyAndHeaders, error) {
			return APIKeyAndHeaders{APIKey: "k"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.NavigateTree(ctx, targetID, NavigateTreeOptions{UserWantsSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.SummaryEntry == nil {
		t.Fatalf("expected a branch summary entry, got %#v", result)
	}
	assertFileDetails(t, "in-memory", result.SummaryEntry.Details)

	// Reopen the JSONL and confirm the details survive the round-trip.
	reopened, err := session.OpenJSONLStorage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	reopenedSession := session.New(reopened)
	entry, err := reopenedSession.Entry(ctx, result.SummaryEntry.EntryID())
	if err != nil {
		t.Fatal(err)
	}
	branch, ok := entry.(session.BranchSummaryEntry)
	if !ok {
		t.Fatalf("reopened entry is not a branch summary: %#v", entry)
	}
	assertFileDetails(t, "reopened", branch.Details)
}

func assertFileDetails(t *testing.T, label string, details any) {
	t.Helper()
	if details == nil {
		t.Fatalf("%s: details missing", label)
	}
	// The in-memory store keeps the original map[string]any; reopening from JSONL
	// decodes arrays as []any, so normalize both shapes via JSON.
	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("%s: marshal details: %v", label, err)
	}
	var decoded struct {
		ReadFiles     []string `json:"readFiles"`
		ModifiedFiles []string `json:"modifiedFiles"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("%s: decode details %s: %v", label, raw, err)
	}
	if len(decoded.ReadFiles) != 1 || decoded.ReadFiles[0] != "main.go" {
		t.Fatalf("%s: readFiles=%v", label, decoded.ReadFiles)
	}
	if len(decoded.ModifiedFiles) != 1 || decoded.ModifiedFiles[0] != "agent.go" {
		t.Fatalf("%s: modifiedFiles=%v", label, decoded.ModifiedFiles)
	}
}

// R3-P2-4: nothing-to-compact and hook cancellation must surface a typed
// AgentErrCompaction error and must not append a compaction entry or emit a
// session_compact event.
func TestAgentHarnessCompactNothingToCompactIsTypedError(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// An empty session has nothing to compact.
	h, err := New(Options{Session: sess})
	if err != nil {
		t.Fatal(err)
	}
	var sawCompact bool
	h.OnSessionCompact(func(ctx context.Context, ev SessionCompactEvent) error {
		sawCompact = true
		return nil
	})
	_, err = h.Compact(ctx, "")
	var agentErr *agent.AgentError
	if !errors.As(err, &agentErr) || agentErr.Code != agent.AgentErrCompaction {
		t.Fatalf("expected compaction error, got %v", err)
	}
	if sawCompact {
		t.Fatal("session_compact emitted for nothing-to-compact")
	}
	assertNoCompactionEntry(t, ctx, sess)
}

func TestAgentHarnessCompactHookCancelIsTypedError(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 14; i++ {
		if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("msg "+string(rune('a'+i)), nil)); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(Options{Session: sess})
	if err != nil {
		t.Fatal(err)
	}
	var sawCompact bool
	h.OnSessionBeforeCompact(func(ctx context.Context, ev SessionBeforeCompactEvent) (*SessionBeforeCompactResult, error) {
		return &SessionBeforeCompactResult{Cancel: true}, nil
	})
	h.OnSessionCompact(func(ctx context.Context, ev SessionCompactEvent) error {
		sawCompact = true
		return nil
	})
	_, err = h.Compact(ctx, "")
	var agentErr *agent.AgentError
	if !errors.As(err, &agentErr) || agentErr.Code != agent.AgentErrCompaction {
		t.Fatalf("expected compaction cancel error, got %v", err)
	}
	if sawCompact {
		t.Fatal("session_compact emitted for cancelled compaction")
	}
	assertNoCompactionEntry(t, ctx, sess)
}

func assertNoCompactionEntry(t *testing.T, ctx context.Context, sess *session.Session) {
	t.Helper()
	entries, err := sess.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.EntryType() == "compaction" {
			t.Fatalf("unexpected compaction entry appended: %#v", entry)
		}
	}
}
