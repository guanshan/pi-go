package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

func TestAgentFauxPrompt(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	auth := ai.NewAuthStorage(settings.AgentDir)
	registry := ai.NewModelRegistry(settings.AgentDir, auth)
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	if err := agent.Prompt(context.Background(), "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	ctx := session.BuildContext()
	if got := len(ctx.Messages); got != 2 {
		t.Fatalf("messages=%d", got)
	}
	if text := ai.MessageText(ctx.Messages[1]); text != "faux: hello" {
		t.Fatalf("assistant text=%q", text)
	}
}

type lookupTool struct {
	query string
}

func (t *lookupTool) Name() string { return "lookup" }

func (t *lookupTool) Description() string { return "lookup test data" }

func (t *lookupTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"q": map[string]any{"type": "string"}},
	}
}

func (t *lookupTool) Execute(ctx context.Context, raw json.RawMessage, _ catools.ToolUpdate) ai.ToolResult {
	var args struct {
		Q string `json:"q"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return ai.ToolResult{Content: ai.TextBlocks(err.Error()), IsError: true}
	}
	t.query = args.Q
	return ai.ToolResult{Content: ai.TextBlocks("result for " + args.Q)}
}

func TestAgentSessionDelegatesToolLoopToAgentPackage(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, body)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"go\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer server.Close()

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	registry.Auth.SetRuntime("openai", "test-key")
	model := ai.Model{Provider: "openai", ID: "gpt-test", API: "openai-completions", BaseURL: server.URL}
	tool := &lookupTool{}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{"lookup": tool}, "system")
	var events []string
	runtime, err := coreext.NewRunner(func(api *coreext.API) error {
		api.On("agent_start", func(any) { events = append(events, "ext:agent_start") })
		api.On("turn_start", func(any) { events = append(events, "ext:turn_start") })
		api.On("tool_call", func(any) { events = append(events, "ext:tool_call") })
		api.On("tool_result", func(any) { events = append(events, "ext:tool_result") })
		api.On("agent_end", func(any) { events = append(events, "ext:agent_end") })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.extensionRuntime = runtime

	err = agent.Prompt(context.Background(), "use tool", nil, func(event ai.Event) {
		if typ, ok := event["type"].(string); ok {
			events = append(events, typ)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.query != "go" {
		t.Fatalf("tool query=%q", tool.query)
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	messages := session.BuildContext().Messages
	if got := len(messages); got != 4 {
		t.Fatalf("messages=%d: %#v", got, messages)
	}
	if ai.MessageRole(messages[2]) != "toolResult" || ai.MessageText(messages[3]) != "done" {
		t.Fatalf("messages=%#v", messages)
	}
	if !containsString(events, "tool_execution_start") || !containsString(events, "tool_execution_end") || !containsString(events, "agent_end") {
		t.Fatalf("events=%v", events)
	}
	if got := countString(events, "turn_start"); got != 2 {
		t.Fatalf("turn_start events=%d in %v", got, events)
	}
	for _, eventType := range []string{"ext:agent_start", "ext:tool_call", "ext:tool_result", "ext:agent_end"} {
		if !containsString(events, eventType) {
			t.Fatalf("missing extension event %q in %v", eventType, events)
		}
	}
}

type stagedStreamingProvider struct {
	firstDeltaSent chan struct{}
	release        chan struct{}
}

func newStagedStreamingProvider() *stagedStreamingProvider {
	return &stagedStreamingProvider{
		firstDeltaSent: make(chan struct{}),
		release:        make(chan struct{}),
	}
}

func (p *stagedStreamingProvider) API() string { return "coding-agent-streaming-e2e" }

func (p *stagedStreamingProvider) Stream(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(8)
	go func() {
		start := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "")
		partial := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("he"), ai.Usage{}, "")
		final := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("hello"), ai.Usage{}, "stop")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: start})
		stream.Push(ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "he", Partial: partial})
		close(p.firstDeltaSent)
		select {
		case <-p.release:
		case <-ctx.Done():
			aborted := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "aborted")
			aborted.ErrorMessage = ctx.Err().Error()
			stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: aborted})
			return
		}
		stream.Push(ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "llo", Partial: final})
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: final.StopReason, Message: final})
	}()
	return stream
}

func (p *stagedStreamingProvider) StreamSimple(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.Stream(ctx, registry, req)
}

func TestAgentSessionPromptStreamsAssistantDeltasBeforeCompletion(t *testing.T) {
	provider := newStagedStreamingProvider()
	ai.RegisterProvider(provider, "coding-agent-streaming-e2e")
	defer ai.UnregisterProviders("coding-agent-streaming-e2e")

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model := ai.Model{Provider: "unit", ID: "stream-model", API: provider.API()}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	extensionDeltas := make(chan string, 2)
	runtime, err := coreext.NewRunner(func(api *coreext.API) error {
		api.On("message_update", func(payload any) {
			event, ok := payload.(*coreext.MessageUpdateEvent)
			if !ok || event.AssistantMessageEvent.Type != "text_delta" {
				return
			}
			extensionDeltas <- event.AssistantMessageEvent.Delta
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	agent.extensionRuntime = runtime

	deltas := make(chan string, 2)
	promptDone := make(chan error, 1)
	go func() {
		promptDone <- agent.Prompt(context.Background(), "hello", nil, func(event ai.Event) {
			if eventType, _ := event["type"].(string); eventType != "message_update" {
				return
			}
			assistantEvent, ok := event["assistantMessageEvent"].(ai.AssistantMessageEvent)
			if !ok || assistantEvent.Type != "text_delta" {
				return
			}
			deltas <- assistantEvent.Delta
		})
	}()

	select {
	case <-provider.firstDeltaSent:
	case <-time.After(time.Second):
		t.Fatal("provider never emitted first delta")
	}

	select {
	case delta := <-deltas:
		if delta != "he" {
			t.Fatalf("first delta=%q", delta)
		}
	case <-time.After(time.Second):
		t.Fatal("expected sink to receive streamed delta before completion")
	}

	select {
	case delta := <-extensionDeltas:
		if delta != "he" {
			t.Fatalf("first extension delta=%q", delta)
		}
	case <-time.After(time.Second):
		t.Fatal("expected extension runtime to receive streamed delta before completion")
	}

	select {
	case err := <-promptDone:
		t.Fatalf("prompt returned before stream completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(provider.release)

	select {
	case delta := <-deltas:
		if delta != "llo" {
			t.Fatalf("second delta=%q", delta)
		}
	case <-time.After(time.Second):
		t.Fatal("expected sink to receive second streamed delta")
	}

	select {
	case delta := <-extensionDeltas:
		if delta != "llo" {
			t.Fatalf("second extension delta=%q", delta)
		}
	case <-time.After(time.Second):
		t.Fatal("expected extension runtime to receive second streamed delta")
	}

	select {
	case err := <-promptDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt did not finish")
	}

	ctx := session.BuildContext()
	if got := len(ctx.Messages); got != 2 {
		t.Fatalf("messages=%d", got)
	}
	if text := ai.MessageText(ctx.Messages[1]); text != "hello" {
		t.Fatalf("assistant text=%q", text)
	}
}

func TestFilterImageBlocksForLLMRequests(t *testing.T) {
	messages := []ai.Message{
		ai.NewUserMessage("look", []ai.ContentBlock{
			{Type: "image", MimeType: "image/png", Data: "abc"},
			{Type: "text", Text: "caption"},
		}),
		ai.NewToolResultMessage("call-1", "read", []ai.ContentBlock{
			{Type: "text", Text: "result"},
			{Type: "image", MimeType: "image/png", Data: "def"},
		}, nil, false),
	}
	filtered := filterImageBlocks(messages)
	if got := ai.MessageBlocks(filtered[0]); len(got) != 2 || got[0].Text != "look" || got[1].Text != "caption" {
		t.Fatalf("user blocks=%#v", got)
	}
	if got := ai.MessageBlocks(filtered[1]); len(got) != 1 || got[0].Text != "result" {
		t.Fatalf("tool result blocks=%#v", got)
	}
	if got := ai.MessageBlocks(messages[0]); len(got) != 3 || got[1].Type != "image" {
		t.Fatalf("original user blocks mutated=%#v", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func countString(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

func TestSettingsManagerImagesCompatibility(t *testing.T) {
	block := true
	autoResize := false
	settings := &SettingsManager{
		Global: Settings{Images: ImageSettings{BlockImages: &block, AutoResize: &autoResize}},
	}
	if !settings.BlockImages() {
		t.Fatal("expected nested images.blockImages to be honored")
	}
	if settings.ImageAutoResize() {
		t.Fatal("expected nested images.autoResize to be honored")
	}

	settings = &SettingsManager{Global: Settings{BlockImages: &block, ImageAutoResize: &autoResize}}
	if !settings.BlockImages() {
		t.Fatal("expected legacy blockImages to be honored")
	}
	if settings.ImageAutoResize() {
		t.Fatal("expected legacy imageAutoResize to be honored")
	}
}

func TestSettingsManagerShellCommandPrefixCompatibility(t *testing.T) {
	settings := &SettingsManager{
		Global: Settings{
			ShellCommandPrefix: "global shell",
			BashCommandPrefix:  "global bash",
		},
	}
	if got := settings.ShellCommandPrefix(); got != "global shell" {
		t.Fatalf("global shellCommandPrefix=%q", got)
	}

	settings.Project.BashCommandPrefix = "project legacy"
	if got := settings.ShellCommandPrefix(); got != "project legacy" {
		t.Fatalf("project legacy bashCommandPrefix=%q", got)
	}

	settings.Project.ShellCommandPrefix = "project shell"
	if got := settings.ShellCommandPrefix(); got != "project shell" {
		t.Fatalf("project shellCommandPrefix=%q", got)
	}
}

func TestSettingsManagerNestedTypeScriptSettings(t *testing.T) {
	disabled := false
	enabled := true
	padding := 7
	visible := 2
	idleDisabled := HTTPIdleTimeoutSetting(0)
	idleDefaultOverride := HTTPIdleTimeoutSetting(120000)
	settings := &SettingsManager{
		Global: Settings{
			Compaction: CompactionConfig{Enabled: &disabled, ReserveTokens: 123, KeepRecentTokens: 456},
			Retry: RetryConfig{
				Enabled:     &disabled,
				MaxRetries:  4,
				BaseDelayMS: 300,
				Provider:    ProviderRetryConfig{TimeoutMS: 1000, MaxRetries: 2, MaxRetryDelayMS: 7000},
			},
			Terminal:          TerminalConfig{ShowImages: &disabled, ImageWidthCells: 42, ClearOnShrink: &enabled, ShowTerminalProgress: &enabled},
			Markdown:          MarkdownConfig{CodeBlockIndent: "\t"},
			Warnings:          WarningConfig{AnthropicExtraUsage: &disabled},
			HTTPIdleTimeoutMS: &idleDefaultOverride,
		},
		Project: Settings{
			DoubleEscapeAction:     "fork",
			TreeFilterMode:         "user-only",
			EditorPaddingX:         &padding,
			AutocompleteMaxVisible: &visible,
			ShowHardwareCursor:     &enabled,
			HTTPIdleTimeoutMS:      &idleDisabled,
		},
	}
	if settings.AutoCompactionEnabled() || settings.CompactionReserveTokens() != 123 || settings.CompactionKeepRecentTokens() != 456 {
		t.Fatalf("compaction settings not honored")
	}
	if settings.AutoRetryEnabled() || settings.RetryMaxRetries() != 4 || settings.RetryBaseDelayMS() != 300 {
		t.Fatalf("retry settings not honored")
	}
	if settings.ProviderRetryTimeoutMS() != 1000 || settings.ProviderRetryMaxRetries() != 2 || settings.ProviderRetryMaxDelayMS() != 7000 {
		t.Fatalf("provider retry settings not honored")
	}
	if settings.ShowImages() || settings.ImageWidthCells() != 42 || !settings.ClearOnShrink() || !settings.ShowTerminalProgress() {
		t.Fatalf("terminal settings not honored")
	}
	if settings.DoubleEscapeAction() != "fork" || settings.TreeFilterMode() != "user-only" {
		t.Fatalf("interactive settings not honored")
	}
	if settings.EditorPaddingX() != 3 || settings.AutocompleteMaxVisible() != 3 || !settings.ShowHardwareCursor() {
		t.Fatalf("editor settings not clamped/honored")
	}
	if settings.CodeBlockIndent() != "\t" || settings.AnthropicExtraUsageWarning() {
		t.Fatalf("markdown/warning settings not honored")
	}
	if settings.HTTPIdleTimeoutMS() != 0 {
		t.Fatalf("http idle timeout=%d", settings.HTTPIdleTimeoutMS())
	}
	var parsed Settings
	if err := json.Unmarshal([]byte(`{"httpIdleTimeoutMs":"disabled"}`), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.HTTPIdleTimeoutMS == nil || int(*parsed.HTTPIdleTimeoutMS) != 0 {
		t.Fatalf("parsed http idle timeout=%#v", parsed.HTTPIdleTimeoutMS)
	}
}
