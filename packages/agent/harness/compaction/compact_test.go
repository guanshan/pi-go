package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

func TestPrepareAndCompactLocalSummary(t *testing.T) {
	const sourceID = "compaction-local-provider"
	ai.RegisterProvider(fakeSummaryProvider{text: "generated summary"}, sourceID)
	defer ai.UnregisterProviders(sourceID)
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("old work", nil)); err != nil {
		t.Fatal(err)
	}
	keptID, err := sess.AppendMessage(ctx, ai.NewUserMessage("recent work", nil))
	if err != nil {
		t.Fatal(err)
	}
	branch, err := sess.Branch(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	prep, err := PrepareCompaction(branch, Settings{Enabled: true, KeepRecentTokens: 3, SummaryMaxChars: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if prep == nil || prep.FirstKeptEntryID != keptID || len(prep.MessagesToSummarize) != 1 || len(prep.KeptMessages) != 1 {
		t.Fatalf("prep=%#v keptID=%s", prep, keptID)
	}
	model := ai.Model{Provider: "test", ID: "summary-model", API: "summary-test"}
	registry := ai.NewModelRegistry(t.TempDir(), ai.NewAuthStorage(t.TempDir()))
	result, err := Compact(ctx, prep, model, "test-key", nil, "custom note", registry, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Summary, "generated summary") || result.FirstKeptEntryID != keptID {
		t.Fatalf("result=%#v", result)
	}
}

func TestPrepareCompactionHandlesSplitTurnWithoutOrphaningToolResults(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("old work", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewAssistantMessageForModel(ai.Model{}, ai.TextBlocks("old answer"), ai.Usage{}, "stop")); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("current turn", nil)); err != nil {
		t.Fatal(err)
	}
	assistantID, err := sess.AppendMessage(ctx, ai.NewAssistantMessageForModel(ai.Model{}, ai.TextBlocks(strings.Repeat("large ", 30)), ai.Usage{}, "toolUse"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewToolResultMessage("call-1", "lookup", ai.TextBlocks("ok"), nil, false)); err != nil {
		t.Fatal(err)
	}
	branch, err := sess.Branch(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	prep, err := PrepareCompaction(branch, Settings{Enabled: true, KeepRecentTokens: 5, SummaryMaxChars: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if prep == nil || !prep.IsSplitTurn || prep.FirstKeptEntryID != assistantID {
		t.Fatalf("prep=%#v assistantID=%s", prep, assistantID)
	}
	if len(prep.TurnPrefixMessages) != 1 || ai.MessageText(prep.TurnPrefixMessages[0]) != "current turn" {
		t.Fatalf("turn prefix=%#v", prep.TurnPrefixMessages)
	}
	if len(prep.KeptMessages) != 1 {
		t.Fatalf("kept messages=%#v", prep.KeptMessages)
	}
	if _, ok := ai.AsToolResultMessage(prep.KeptMessages[0]); ok {
		t.Fatalf("orphaned tool result kept first: %#v", prep.KeptMessages)
	}
}

func TestBranchSummaryFileOps(t *testing.T) {
	const sourceID = "branch-summary-file-provider"
	ai.RegisterProvider(fakeSummaryProvider{text: "branch llm summary"}, sourceID)
	defer ai.UnregisterProviders(sourceID)
	ctx := context.Background()
	callArgs := json.RawMessage(`{"path":"README.md"}`)
	assistant := ai.NewAssistantMessageForModel(ai.Model{Provider: "test", ID: "m", API: "test"}, []ai.ContentBlock{{
		Type:      "toolCall",
		ID:        "call-1",
		Name:      "read",
		Arguments: callArgs,
	}}, ai.Usage{}, "toolUse")
	entries := []session.Entry{
		session.MessageEntry{Message: ai.NewUserMessage("inspect", nil)},
		session.MessageEntry{Message: assistant},
	}
	model := ai.Model{Provider: "test", ID: "summary-model", API: "summary-test", ContextWindow: 128000}
	registry := ai.NewModelRegistry(t.TempDir(), ai.NewAuthStorage(t.TempDir()))
	summary, err := GenerateBranchSummary(ctx, entries, BranchSummaryOptions{Model: model, Registry: registry, APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.ReadFiles) != 1 || summary.ReadFiles[0] != "README.md" || !strings.Contains(summary.Summary, "README.md") {
		t.Fatalf("summary=%#v", summary)
	}
}

func TestSerializeConversationIncludesToolCalls(t *testing.T) {
	raw := json.RawMessage(`{"path":"main.go"}`)
	msg := ai.NewAssistantMessageForModel(ai.Model{Provider: "test", ID: "m", API: "test"}, []ai.ContentBlock{
		{Type: "thinking", Thinking: "think"},
		{Type: "text", Text: "done"},
		{Type: "toolCall", ID: "call-1", Name: "read", Arguments: raw},
	}, ai.Usage{}, "toolUse")
	text := SerializeConversation([]ai.Message{msg})
	if !strings.Contains(text, "[Assistant thinking]: think") || !strings.Contains(text, "read(path=\"main.go\")") {
		t.Fatalf("serialized=%q", text)
	}
}

func TestCompactNilPreparationError(t *testing.T) {
	_, err := Compact(context.Background(), nil, ai.Model{}, "", nil, "", nil, "")
	var compactionErr *CompactionError
	if !errors.As(err, &compactionErr) || compactionErr.Code != "invalid_preparation" {
		t.Fatalf("err=%#v compactionErr=%#v", err, compactionErr)
	}
}

func TestCompactionAndBranchSummaryUseLLMProvider(t *testing.T) {
	const sourceID = "compaction-test-provider"
	ai.RegisterProvider(fakeSummaryProvider{text: "llm summary"}, sourceID)
	defer ai.UnregisterProviders(sourceID)

	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "summary-model", API: "summary-test"}
	registry := ai.NewModelRegistry(t.TempDir(), ai.NewAuthStorage(t.TempDir()))
	prep := &Preparation{
		FirstKeptEntryID:    "keep",
		MessagesToSummarize: []agent.AgentMessage{ai.NewUserMessage("old", nil)},
		TokensBefore:        12,
		Settings:            withDefaults(Settings{Enabled: true, SummaryMaxChars: 2000}),
	}
	compact, err := Compact(ctx, prep, model, "", nil, "", registry, ai.ThinkingOff)
	if err != nil {
		t.Fatal(err)
	}
	if compact.Summary != "llm summary" {
		t.Fatalf("compact summary=%q", compact.Summary)
	}
	branch, err := GenerateBranchSummary(ctx, []session.Entry{
		session.MessageEntry{Message: ai.NewUserMessage("branch", nil)},
	}, BranchSummaryOptions{Model: model, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(branch.Summary, "llm summary") {
		t.Fatalf("branch summary=%q", branch.Summary)
	}
}

type fakeSummaryProvider struct {
	text string
}

func (p fakeSummaryProvider) API() string { return "summary-test" }
func (p fakeSummaryProvider) Stream(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.stream(req.Model)
}
func (p fakeSummaryProvider) StreamSimple(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.stream(req.Model)
}
func (p fakeSummaryProvider) stream(model ai.Model) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(2)
	go func() {
		msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks(p.text), ai.Usage{}, "stop")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
	}()
	return stream
}
