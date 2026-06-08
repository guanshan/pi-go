package session

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestMemorySessionBuildContextAndMove(t *testing.T) {
	ctx := context.Background()
	sess, err := NewMemory(Metadata{ID: "s1", CreatedAt: "now"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := sess.AppendMessage(ctx, ai.NewUserMessage("hello", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendModelChange(ctx, "openai", "gpt-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendThinkingLevelChange(ctx, "high"); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendActiveToolsChange(ctx, []string{"search", "edit"}); err != nil {
		t.Fatal(err)
	}
	secondID, err := sess.AppendMessage(ctx, ai.NewUserMessage("later", nil))
	if err != nil {
		t.Fatal(err)
	}
	built, err := sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 2 || built.Model == nil || built.Model.ModelID != "gpt-test" || built.ThinkingLevel != "high" || built.ActiveToolNames == nil {
		t.Fatalf("context=%#v", built)
	}
	if got := *built.ActiveToolNames; len(got) != 2 || got[0] != "search" || got[1] != "edit" {
		t.Fatalf("active tools=%#v", got)
	}
	if _, err := sess.AppendActiveToolsChange(ctx, []string{}); err != nil {
		t.Fatal(err)
	}
	built, err = sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if built.ActiveToolNames == nil || len(*built.ActiveToolNames) != 0 {
		t.Fatalf("active tools after clear=%#v", built.ActiveToolNames)
	}
	if _, err := sess.AppendLabel(ctx, secondID, "latest"); err != nil {
		t.Fatal(err)
	}
	if label, ok := sess.Label(ctx, secondID); !ok || label != "latest" {
		t.Fatalf("label=%q ok=%v", label, ok)
	}
	if _, err := sess.MoveTo(ctx, &firstID); err != nil {
		t.Fatal(err)
	}
	built, err = sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 1 || ai.MessageText(built.Messages[0]) != "hello" {
		t.Fatalf("moved context=%#v", built)
	}
}

func TestSessionBuildContextAppliesCompaction(t *testing.T) {
	ctx := context.Background()
	sess, err := NewMemory(Metadata{ID: "s1", CreatedAt: "now"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("old", nil)); err != nil {
		t.Fatal(err)
	}
	firstKeptID, err := sess.AppendMessage(ctx, ai.NewUserMessage("kept one", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("kept two", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendCompaction(ctx, "old summary", firstKeptID, 12, nil, false); err != nil {
		t.Fatal(err)
	}
	built, err := sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 3 || ai.MessageRole(built.Messages[0]) != "compactionSummary" || ai.MessageText(built.Messages[1]) != "kept one" || ai.MessageText(built.Messages[2]) != "kept two" {
		t.Fatalf("context=%#v", built.Messages)
	}
}

func TestSessionCustomNameAndBranchMove(t *testing.T) {
	ctx := context.Background()
	sess, err := NewMemory(Metadata{ID: "s1", CreatedAt: "now"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rootID, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendSessionName(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendCustomMessageEntry(ctx, "note", "custom text", true, map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendCustomEntry(ctx, "raw", map[string]any{"value": 1}); err != nil {
		t.Fatal(err)
	}
	name, err := sess.Name(ctx)
	if err != nil || name != "demo" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	if _, err := sess.MoveTo(ctx, &rootID, &BranchMove{Summary: "came back", Details: "detail", FromHook: true}); err != nil {
		t.Fatal(err)
	}
	built, err := sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 2 || ai.MessageText(built.Messages[0]) != "root" || ai.MessageRole(built.Messages[1]) != "branchSummary" {
		t.Fatalf("context=%#v", built.Messages)
	}
}

// TestSessionMoveToAppendsBranchSummaryForEmptySummary locks the TS behavior
// (session.ts:254-264): MoveTo appends a branch_summary ENTRY whenever a summary
// OBJECT is supplied, even when its text is "". A present *BranchMove with an
// empty Summary must still produce a branch_summary entry in the session tree;
// only a nil move (no summary object) skips the append. (Note: buildContext
// still omits empty-summary branch_summary entries from the LLM context, both in
// TS session.ts:56 `entry.summary` and Go context.go — so the entry is asserted
// at the storage level, not via BuildContext.)
func TestSessionMoveToAppendsBranchSummaryForEmptySummary(t *testing.T) {
	ctx := context.Background()
	sess, err := NewMemory(Metadata{ID: "s1", CreatedAt: "now"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rootID, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil))
	if err != nil {
		t.Fatal(err)
	}

	countBranchSummaries := func() int {
		entries, err := sess.Entries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range entries {
			if _, ok := e.(BranchSummaryEntry); ok {
				n++
			}
		}
		return n
	}

	// Empty-summary BranchMove: a present summary object still appends an entry.
	if _, err := sess.MoveTo(ctx, &rootID, &BranchMove{Summary: ""}); err != nil {
		t.Fatal(err)
	}
	if got := countBranchSummaries(); got != 1 {
		t.Fatalf("empty-summary move should append exactly one branch_summary entry, got %d", got)
	}

	// A nil move (no summary object) must NOT append a branch_summary entry.
	if _, err := sess.MoveTo(ctx, &rootID, nil); err != nil {
		t.Fatal(err)
	}
	if got := countBranchSummaries(); got != 1 {
		t.Fatalf("nil move must not append a branch_summary entry, branch_summary count=%d", got)
	}
}

func TestMemoryRepoCreateForkDelete(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepo()
	sess, err := repo.Create(ctx, CreateOptions{ID: "one"})
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := sess.AppendMessage(ctx, ai.NewUserMessage("one", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("two", nil)); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx, MemoryListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "one" {
		t.Fatalf("list=%#v", list)
	}
	fork, err := repo.Fork(ctx, Metadata{ID: "one"}, ForkOptions{CreateOptions: CreateOptions{ID: "fork"}, EntryID: firstID, Position: "at"})
	if err != nil {
		t.Fatal(err)
	}
	built, err := fork.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 1 || ai.MessageText(built.Messages[0]) != "one" {
		t.Fatalf("fork context=%#v", built)
	}
	if err := repo.Delete(ctx, Metadata{ID: "fork"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Open(ctx, Metadata{ID: "fork"}); err == nil {
		t.Fatal("expected deleted fork to be missing")
	}
}

func TestSessionErrorSupportsErrorsAs(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &SessionError{Code: "not_found", Msg: "missing"})
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Code != "not_found" {
		t.Fatalf("sessionErr=%#v", sessionErr)
	}
}
