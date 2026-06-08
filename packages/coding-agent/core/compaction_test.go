package core

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

type recordingCompactionProvider struct {
	mu            sync.Mutex
	api           string
	responses     []string
	prompts       []string
	systemPrompts []string
	calls         int
}

func (p *recordingCompactionProvider) API() string { return p.api }

func (p *recordingCompactionProvider) Stream(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.stream(req)
}

func (p *recordingCompactionProvider) StreamSimple(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.stream(req)
}

func (p *recordingCompactionProvider) stream(req ai.ChatRequest) *ai.AssistantMessageEventStream {
	p.mu.Lock()
	p.systemPrompts = append(p.systemPrompts, req.SystemPrompt)
	p.prompts = append(p.prompts, ai.MessageText(req.Messages[0]))
	responseText := p.responses[min(p.calls, len(p.responses)-1)]
	p.calls++
	p.mu.Unlock()

	stream := ai.NewAssistantMessageEventStream(2)
	go func() {
		msg := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks(responseText), ai.Usage{}, "stop")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "")})
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: msg.StopReason, Message: msg})
	}()
	return stream
}

func appendSessionMessage(t *testing.T, session *SessionManager, message ai.Message) string {
	t.Helper()
	if err := session.Append(SessionEntry{Type: "message", Message: message}); err != nil {
		t.Fatal(err)
	}
	return session.Entries[len(session.Entries)-1].ID
}

func assistantToolCallMessage(model ai.Model, text string, blocks ...ai.ContentBlock) ai.AssistantMessage {
	content := make([]ai.ContentBlock, 0, len(blocks)+1)
	if text != "" {
		content = append(content, ai.ContentBlock{Type: "text", Text: text})
	}
	content = append(content, blocks...)
	return ai.NewAssistantMessageForModel(model, content, ai.Usage{}, "stop")
}

func TestShouldCompactUsesReserveTokens(t *testing.T) {
	settings := CompactionSettings{Enabled: true, ReserveTokens: 20}
	if shouldCompact(80, 100, settings) {
		t.Fatal("expected threshold boundary to stay below compaction")
	}
	if !shouldCompact(81, 100, settings) {
		t.Fatal("expected tokens above reserved window to compact")
	}
	if shouldCompact(500, 1000, CompactionSettings{Enabled: false, ReserveTokens: 20}) {
		t.Fatal("disabled compaction should not trigger")
	}
}

func TestPrepareCompactionCarriesPreviousSummaryAndFileDetails(t *testing.T) {
	model := ai.Model{Provider: "unit", ID: "compaction-model", API: "unit-api"}
	entries := []SessionEntry{
		{Type: "message", ID: "user-1", Message: ai.NewUserMessage("initial", nil)},
		{Type: "message", ID: "assistant-1", Message: assistantToolCallMessage(model, "read file", ai.ContentBlock{Type: "toolCall", ID: "read-1", Name: "read", Arguments: json.RawMessage(`{"path":"legacy-read.txt"}`)})},
		{Type: "compaction", ID: "comp-1", Summary: "old summary", FirstKeptID: "user-2", Details: CompactionDetails{ReadFiles: []string{"legacy-read.txt"}, ModifiedFiles: []string{"legacy-edit.txt"}}},
		{Type: "message", ID: "user-2", Message: ai.NewUserMessage("continue", nil)},
		{Type: "message", ID: "assistant-2", Message: assistantToolCallMessage(model, "update file", ai.ContentBlock{Type: "toolCall", ID: "write-1", Name: "write", Arguments: json.RawMessage(`{"path":"fresh-edit.txt"}`)})},
		{Type: "message", ID: "user-3", Message: ai.NewUserMessage("recent task", nil)},
		{Type: "message", ID: "assistant-3", Message: ai.NewAssistantMessageForModel(model, ai.TextBlocks("latest status"), ai.Usage{}, "stop")},
	}

	preparation := prepareCompaction(entries, CompactionSettings{Enabled: true, ReserveTokens: 32, KeepRecentTokens: 1})
	if preparation == nil {
		t.Fatal("expected compaction preparation")
	}
	if preparation.PreviousSummary != "old summary" {
		t.Fatalf("previous summary=%q", preparation.PreviousSummary)
	}
	if !preparation.IsSplitTurn || preparation.FirstKeptEntryID != "assistant-3" {
		t.Fatalf("preparation=%#v", preparation)
	}
	if got := len(preparation.MessagesToSummarize); got != 2 {
		t.Fatalf("messagesToSummarize=%d", got)
	}
	if got := len(preparation.TurnPrefixMessages); got != 1 || ai.MessageRole(preparation.TurnPrefixMessages[0]) != "user" {
		t.Fatalf("turnPrefixMessages=%#v", preparation.TurnPrefixMessages)
	}
	details := computeCompactionDetails(preparation.FileOps)
	if strings.Join(details.ReadFiles, ",") != "legacy-read.txt" {
		t.Fatalf("readFiles=%#v", details.ReadFiles)
	}
	if strings.Join(details.ModifiedFiles, ",") != "fresh-edit.txt,legacy-edit.txt" {
		t.Fatalf("modifiedFiles=%#v", details.ModifiedFiles)
	}
}

func TestSerializeConversationRendersToolCallArgsAsKeyValue(t *testing.T) {
	model := ai.Model{Provider: "unit", ID: "compaction-model", API: "unit-api"}
	messages := []ai.Message{
		ai.NewUserMessage("please edit", nil),
		assistantToolCallMessage(model, "editing now",
			ai.ContentBlock{Type: "toolCall", ID: "edit-1", Name: "edit", Arguments: json.RawMessage(`{"path":"a.go","oldString":"x","replaceAll":true}`)},
		),
	}
	got := serializeConversation(messages)
	want := "[User]: please edit\n\n" +
		"[Assistant]: editing now\n\n" +
		`[Assistant tool calls]: edit(path="a.go", oldString="x", replaceAll=true)`
	if got != want {
		t.Fatalf("serializeConversation mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestSerializeConversationToolCallArgsPreserveValueTypes(t *testing.T) {
	model := ai.Model{Provider: "unit", ID: "compaction-model", API: "unit-api"}
	messages := []ai.Message{
		assistantToolCallMessage(model, "",
			ai.ContentBlock{Type: "toolCall", ID: "grep-1", Name: "grep", Arguments: json.RawMessage(`{"pattern":"foo<bar>","limit":5,"paths":["x","y"],"nested":{"k":"v"}}`)},
		),
	}
	got := serializeConversation(messages)
	want := `[Assistant tool calls]: grep(pattern="foo<bar>", limit=5, paths=["x","y"], nested={"k":"v"})`
	if got != want {
		t.Fatalf("serializeConversation mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestSerializeConversationAfterConvertEmitsSummariesAsUser(t *testing.T) {
	exit := 0
	messages := []ai.Message{
		BranchSummaryMessage{Role: "branchSummary", Summary: "branch work"},
		CompactionSummaryMessage{Role: "compactionSummary", Summary: "old context"},
		CustomSessionMessage{Role: "custom", CustomType: "note", Content: "a custom note"},
		BashExecutionMessage{Role: "bashExecution", Command: "ls", Output: "file.txt", ExitCode: &exit},
		ai.NewUserMessage("now continue", nil),
	}
	converted, err := convertSessionMessagesToLLM(messages)
	if err != nil {
		t.Fatal(err)
	}
	got := serializeConversation(converted)

	wantParts := []string{
		"[User]: " + ai.BranchSummaryText("branch work"),
		"[User]: " + ai.CompactionSummaryText("old context"),
		"[User]: a custom note",
		"[User]: " + ai.FormatBashExecutionText("ls", "file.txt", &exit, false, false, ""),
		"[User]: now continue",
	}
	want := strings.Join(wantParts, "\n\n")
	if got != want {
		t.Fatalf("serializeConversation after convert mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestAgentSessionCompactUsesRegistryCompleteSimpleAndPersistsDetails(t *testing.T) {
	provider := &recordingCompactionProvider{
		api:       "compaction-test-api",
		responses: []string{"history summary", "turn prefix summary"},
	}
	ai.RegisterProvider(provider, "compaction-test-api")
	defer ai.UnregisterProviders("compaction-test-api")

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	settings.Global.Compaction.KeepRecentTokens = 1
	settings.Global.Compaction.ReserveTokens = 64
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model := ai.Model{Provider: "unit", ID: "compaction-model", API: provider.api, MaxOutput: 2048}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}

	appendSessionMessage(t, session, ai.NewUserMessage("inspect old code", nil))
	appendSessionMessage(t, session, assistantToolCallMessage(model, "opened file", ai.ContentBlock{Type: "toolCall", ID: "read-1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)}))
	appendSessionMessage(t, session, ai.NewToolResultMessage("read-1", "read", ai.TextBlocks("package main"), nil, false))
	appendSessionMessage(t, session, ai.NewUserMessage("patch it", nil))
	appendSessionMessage(t, session, assistantToolCallMessage(model, "wrote file", ai.ContentBlock{Type: "toolCall", ID: "write-1", Name: "write", Arguments: json.RawMessage(`{"path":"b.go"}`)}))
	appendSessionMessage(t, session, ai.NewToolResultMessage("write-1", "write", ai.TextBlocks("done"), nil, false))
	appendSessionMessage(t, session, ai.NewUserMessage("verify latest state", nil))
	keptID := appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("latest response"), ai.Usage{}, "stop"))

	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	result, err := agent.Compact("preserve tests", nil)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 {
		t.Fatalf("compaction calls=%d", provider.calls)
	}
	for _, systemPrompt := range provider.systemPrompts {
		if systemPrompt != summarizationSystemPrompt {
			t.Fatalf("system prompt=%q", systemPrompt)
		}
	}
	if len(provider.prompts) != 2 || !strings.Contains(provider.prompts[0], "<conversation>") || !strings.Contains(provider.prompts[0], "Additional focus: preserve tests") {
		t.Fatalf("prompts=%#v", provider.prompts)
	}
	summary, _ := result["summary"].(string)
	if !strings.Contains(summary, "history summary") || !strings.Contains(summary, "turn prefix summary") || !strings.Contains(summary, "<read-files>\na.go\n</read-files>") || !strings.Contains(summary, "<modified-files>\nb.go\n</modified-files>") {
		t.Fatalf("summary=%q", summary)
	}
	if got, _ := result["firstKeptEntryId"].(string); got != keptID {
		t.Fatalf("firstKeptEntryId=%q want=%q", got, keptID)
	}
	last := session.Entries[len(session.Entries)-1]
	if last.Type != "compaction" {
		t.Fatalf("last entry=%#v", last)
	}
	details := compactionDetailsFromAny(last.Details)
	if strings.Join(details.ReadFiles, ",") != "a.go" || strings.Join(details.ModifiedFiles, ",") != "b.go" {
		t.Fatalf("details=%#v", details)
	}
}
