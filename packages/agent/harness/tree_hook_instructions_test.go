package harness

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

// P1-07b: when session_before_tree returns only customInstructions (no summary),
// the harness must still run generateBranchSummary -- feeding those
// instructions in as input -- rather than treating customInstructions as a
// literal summary that skips generation. We detect generation by the presence
// of the BRANCH_SUMMARY preamble, which is only added by generateBranchSummary.
func TestNavigateTreeHookCustomInstructionsTriggerGeneration(t *testing.T) {
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

	const customInstructions = "FOCUS ONLY ON THE DATABASE MIGRATION"

	rootID, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("explore", nil)); err != nil {
		t.Fatal(err)
	}
	assistant := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
		{Type: "toolCall", ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"db.go"}`)},
	}, ai.Usage{}, "toolUse")
	oldLeafID, err := sess.AppendMessage(ctx, assistant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.MoveTo(ctx, &rootID); err != nil {
		t.Fatal(err)
	}
	targetID, err := sess.AppendMessage(ctx, ai.NewUserMessage("target", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.MoveTo(ctx, &oldLeafID); err != nil {
		t.Fatal(err)
	}

	ai.SetFauxResponses([]ai.FauxResponse{ai.NewFauxText("GENERATED-SUMMARY-BODY")})
	defer ai.ResetFauxResponses()

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
	h.OnSessionBeforeTree(func(ctx context.Context, ev SessionBeforeTreeEvent) (*SessionBeforeTreeResult, error) {
		// Return ONLY customInstructions, no Summary.
		return &SessionBeforeTreeResult{CustomInstructions: customInstructions}, nil
	})

	result, err := h.NavigateTree(ctx, targetID, NavigateTreeOptions{UserWantsSummary: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.SummaryEntry == nil {
		t.Fatalf("expected a generated branch summary entry, got %#v", result)
	}
	summary := result.SummaryEntry.Summary
	// Generation ran: the preamble that generateBranchSummary prepends is present.
	if !strings.Contains(summary, "explored a different conversation branch") {
		t.Fatalf("summary missing generated preamble (generation skipped?): %q", summary)
	}
	if !strings.Contains(summary, "GENERATED-SUMMARY-BODY") {
		t.Fatalf("summary missing generated body: %q", summary)
	}
	// The buggy behavior used customInstructions verbatim as the summary and
	// skipped generation entirely.
	if strings.Contains(summary, customInstructions) {
		t.Fatalf("customInstructions leaked into the summary text instead of being used only as input: %q", summary)
	}
}
