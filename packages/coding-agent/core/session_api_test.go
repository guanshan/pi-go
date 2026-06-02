package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

func TestAgentSessionDocumentedAPIs(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	model.ContextWindow = 128
	model.ThinkingLevels = []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingHigh}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")

	agent.SetAutoCompactionEnabled(false)
	agent.SetAutoRetryEnabled(false)
	agent.SetSteeringMode(agentcore.QueueAll)
	agent.SetFollowUpMode(agentcore.QueueOneAtATime)
	agent.QueueSteer("first steer", nil)
	agent.QueueFollowUp("queued follow-up", nil)
	cleared := agent.ClearQueue()
	if len(cleared.Steering) != 1 || cleared.Steering[0] != "first steer" {
		t.Fatalf("cleared steering=%#v", cleared.Steering)
	}
	if len(cleared.FollowUp) != 1 || cleared.FollowUp[0] != "queued follow-up" {
		t.Fatalf("cleared follow-up=%#v", cleared.FollowUp)
	}
	if got := agent.GetAvailableThinkingLevels(); len(got) == 0 {
		t.Fatal("expected thinking levels")
	}
	if !agent.SupportsThinking() {
		t.Fatal("expected session to report thinking support")
	}
	if err := agent.SendUserMessage(context.Background(), SendUserMessageOptions{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if got := ai.MessageText(session.BuildContext().Messages[1]); got != "faux: hello" {
		t.Fatalf("assistant text=%q", got)
	}
	bashResult, err := agent.ExecuteBash(context.Background(), "printf hello", BashRunOptions{ExcludeFromContext: true})
	if err != nil {
		t.Fatal(err)
	}
	if bashResult.Output != "hello" {
		t.Fatalf("bash output=%q", bashResult.Output)
	}
	parsed := ParseSkillBlock("<skill name=\"demo\" location=\"/tmp/demo\">\nbody\n</skill>\n\nextra")
	if parsed == nil || parsed.Name != "demo" || parsed.Location != "/tmp/demo" || parsed.Content != "body" || parsed.UserMessage != "extra" {
		t.Fatalf("parsed=%#v", parsed)
	}
	state := agent.State()
	if state.SessionID == "" || state.Model.Provider != "faux" || state.MessageCount != 2 || state.IsStreaming {
		t.Fatalf("state=%#v", state)
	}
	stats := agent.GetSessionStats()
	if stats.SessionID == "" || stats.UserMessages != 1 || stats.AssistantMessages != 1 || stats.TotalMessages != 2 {
		t.Fatalf("stats=%#v", stats)
	}
	if stats.ContextUsage == nil || stats.ContextUsage.ContextWindow != 128 {
		t.Fatalf("context usage=%#v", stats.ContextUsage)
	}
	jsonlPath, err := agent.ExportToJsonl(filepath.Join(cwd, "export.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jsonlPath); err != nil {
		t.Fatal(err)
	}
	htmlPath, err := agent.ExportToHTML(context.Background(), filepath.Join(cwd, "export.html"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(htmlPath); err != nil {
		t.Fatal(err)
	}
	if err := agent.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	replaced := agent.CreateReplacedSessionContext()
	if replaced.Session != agent || replaced.Services == nil || replaced.Services.Cwd != cwd {
		t.Fatalf("replaced=%#v", replaced)
	}
	agent.Dispose()
	if err := agent.SendUserMessage(context.Background(), SendUserMessageOptions{Text: "after dispose"}); err == nil {
		t.Fatal("expected disposed session to reject messages")
	}
}

func TestSendUserMessageFallsBackToFollowUpAfterStreamingRace(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	agent := NewAgentSession(InMemorySession(cwd), settings, registry, ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}, model, ai.ThinkingOff, ToolSet{}, "system")
	agent.mu.Lock()
	agent.streaming = true
	agent.mu.Unlock()
	if err := agent.SendUserMessage(context.Background(), SendUserMessageOptions{Text: "queued", StreamingBehavior: StreamingFollowUp}); err != nil {
		t.Fatal(err)
	}
	if got := len(agent.followUpQueue); got != 1 || agent.followUpQueue[0].Message != "queued" {
		t.Fatalf("followUpQueue=%#v", agent.followUpQueue)
	}
}

func TestParseSkillBlockMatchesTypeScriptShape(t *testing.T) {
	if parsed := ParseSkillBlock("<skill name='demo' location='/tmp/demo'>\nbody\n</skill>"); parsed != nil {
		t.Fatalf("unexpected single-quoted skill block=%#v", parsed)
	}
	if parsed := ParseSkillBlock(" <skill name=\"demo\" location=\"/tmp/demo\">\nbody\n</skill>"); parsed != nil {
		t.Fatalf("unexpected whitespace-prefixed skill block=%#v", parsed)
	}
}

func TestModelChangesEmitSessionEvents(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	agent := NewAgentSession(InMemorySession(cwd), settings, registry, ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}, model, ai.ThinkingOff, ToolSet{}, "system")
	var changes []ModelChangedEvent
	unsubscribe := agent.Subscribe(func(event SessionEvent) {
		if changed, ok := event.(ModelChangedEvent); ok {
			changes = append(changes, changed)
		}
	})
	defer unsubscribe()
	if _, err := agent.SetModel("faux", "faux"); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Model.Provider != "faux" {
		t.Fatalf("changes=%#v", changes)
	}
}

func TestAgentSessionNavigateRetryAndAutoCompact(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary failure"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer server.Close()

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	enabled := true
	settings.Global.Retry.Enabled = &enabled
	settings.Global.Retry.MaxRetries = 1
	settings.Global.Retry.BaseDelayMS = 1
	settings.Global.Compaction.Enabled = &enabled
	settings.Global.Compaction.ReserveTokens = 1
	settings.Global.Compaction.KeepRecentTokens = 1
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	registry.Auth.SetRuntime("openai", "test-key")
	model := ai.Model{Provider: "openai", ID: "gpt-test", API: "openai-completions", BaseURL: server.URL, ContextWindow: 80}
	session := InMemorySession(cwd)
	for i := 0; i < 4; i++ {
		if err := session.AppendMessage(ai.NewUserMessage(strings.Repeat("u", 40), nil)); err != nil {
			t.Fatal(err)
		}
		if err := session.AppendMessage(ai.NewAssistantMessage("openai", "gpt-test", "openai-completions", ai.TextBlocks(strings.Repeat("a", 40)), ai.Usage{}, "stop")); err != nil {
			t.Fatal(err)
		}
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	var sawRetryStart, sawRetryEnd, sawCompaction bool
	unsubscribe := agent.Subscribe(func(event SessionEvent) {
		switch event.(type) {
		case AutoRetryStartEvent:
			sawRetryStart = true
		case AutoRetryEndEvent:
			sawRetryEnd = true
		case CompactionEndEvent:
			sawCompaction = true
		}
	})
	defer unsubscribe()

	if err := agent.Prompt(context.Background(), "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatalf("requests=%d", requests)
	}
	if !sawRetryStart || !sawRetryEnd || !sawCompaction {
		t.Fatalf("events retryStart=%v retryEnd=%v compaction=%v", sawRetryStart, sawRetryEnd, sawCompaction)
	}
	hasCompaction := false
	firstUserID := ""
	for _, entry := range session.Entries {
		if entry.Type == "message" && entry.Message != nil && ai.MessageRole(entry.Message) == "user" && firstUserID == "" {
			firstUserID = entry.ID
		}
		if entry.Type == "compaction" {
			hasCompaction = true
		}
	}
	if !hasCompaction {
		t.Fatalf("entries=%#v", session.Entries)
	}
	result, err := agent.NavigateTree(context.Background(), firstUserID, NavigateTreeOptions{Summarize: true, CustomInstructions: "keep short", Label: "fork point"})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 5 {
		t.Fatalf("requests after branch summary=%d", requests)
	}
	if result.NewLeafID == "" || result.SummaryEntry == nil {
		t.Fatalf("navigate result=%#v", result)
	}
	if !strings.Contains(result.SummaryEntry.Summary, "The user explored a different conversation branch before returning here.") {
		t.Fatalf("summary=%q", result.SummaryEntry.Summary)
	}
	last := session.Entries[len(session.Entries)-1]
	if last.Type != "branch_summary" {
		data, _ := json.MarshalIndent(session.Entries, "", "  ")
		t.Fatalf("expected branch_summary tail, got %s", data)
	}
	if forkable := agent.GetUserMessagesForForking(); len(forkable) == 0 {
		t.Fatal("expected forkable user messages")
	}
}

func TestAgentSessionContextOverflowCompactsAndRetries(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"context_length_exceeded: reduce the length of the messages"}}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"overflow summary"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done after compact"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
		}
	}))
	defer server.Close()

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	enabled := true
	settings.Global.Compaction.Enabled = &enabled
	settings.Global.Compaction.ReserveTokens = 64
	settings.Global.Compaction.KeepRecentTokens = 1
	settings.Global.Retry.Enabled = &enabled
	settings.Global.Retry.MaxRetries = 1
	settings.Global.Retry.BaseDelayMS = 1
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	registry.Auth.SetRuntime("openai", "test-key")
	model := ai.Model{Provider: "openai", ID: "gpt-test", API: "openai-completions", BaseURL: server.URL, ContextWindow: 80}
	session := InMemorySession(cwd)
	for i := 0; i < 3; i++ {
		appendSessionMessage(t, session, ai.NewUserMessage(strings.Repeat("u", 40), nil))
		appendSessionMessage(t, session, ai.NewAssistantMessage("openai", "gpt-test", "openai-completions", ai.TextBlocks(strings.Repeat("a", 40)), ai.Usage{}, "stop"))
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	agent.SetAutoCompactionEnabled(false)
	var overflowEnd *CompactionEndEvent
	unsubscribe := agent.Subscribe(func(event SessionEvent) {
		if compacted, ok := event.(CompactionEndEvent); ok && compacted.Reason == CompactionOverflow {
			copy := compacted
			overflowEnd = &copy
		}
	})
	defer unsubscribe()

	if err := agent.Prompt(context.Background(), "retry me", nil, nil); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatalf("requests=%d", requests)
	}
	if overflowEnd == nil || !overflowEnd.WillRetry {
		t.Fatalf("overflow compaction event=%#v", overflowEnd)
	}
	messages := session.BuildContext().Messages
	if got := ai.MessageText(messages[len(messages)-1]); got != "done after compact" {
		t.Fatalf("last message=%q messages=%#v", got, messages)
	}
}

func TestAgentSessionBindExtensionsLifecycle(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")

	var aborts []string
	var errs []string
	shutdownErr := errors.New("shutdown failed")
	err := agent.BindExtensions(context.Background(), ExtensionBindings{
		UIContext:             struct{}{},
		CommandContextActions: struct{}{},
		AbortHandler: func() {
			aborts = append(aborts, "abort")
		},
		ShutdownHandler: func(context.Context) error {
			aborts = append(aborts, "shutdown")
			return shutdownErr
		},
		OnError: func(err error) {
			errs = append(errs, err.Error())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{"ui", "command_context", "abort", "session_shutdown", "error", "any"} {
		if !agent.HasExtensionHandlers(eventType) {
			t.Fatalf("expected handlers for %q", eventType)
		}
	}
	if agent.HasExtensionHandlers("unknown") {
		t.Fatal("unexpected handler for unknown event type")
	}
	if err := agent.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(aborts, []string{"abort"}) {
		t.Fatalf("abort callbacks=%#v", aborts)
	}
	agent.Dispose()
	if !reflect.DeepEqual(aborts, []string{"abort", "shutdown"}) {
		t.Fatalf("lifecycle callbacks=%#v", aborts)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], shutdownErr.Error()) {
		t.Fatalf("extension errors=%#v", errs)
	}
	if agent.HasExtensionHandlers("any") {
		t.Fatal("expected dispose to clear extension handlers")
	}
	if err := agent.BindExtensions(context.Background(), ExtensionBindings{}); err == nil {
		t.Fatal("expected disposed session to reject extension rebinding")
	}
}

func TestAgentSessionAbortRetryStopsBackoff(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"temporary failure"}}`))
	}))
	defer server.Close()

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	enabled := true
	settings.Global.Retry.Enabled = &enabled
	settings.Global.Retry.MaxRetries = 3
	settings.Global.Retry.BaseDelayMS = 5000
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	registry.Auth.SetRuntime("openai", "test-key")
	model := ai.Model{Provider: "openai", ID: "gpt-test", API: "openai-completions", BaseURL: server.URL, ContextWindow: 80}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")

	retryStarted := make(chan struct{}, 1)
	unsubscribe := agent.Subscribe(func(event SessionEvent) {
		if _, ok := event.(AutoRetryStartEvent); ok {
			select {
			case retryStarted <- struct{}{}:
			default:
			}
			agent.AbortRetry()
		}
	})
	defer unsubscribe()

	done := make(chan error, 1)
	go func() {
		done <- agent.Prompt(context.Background(), "hello", nil, nil)
	}()

	select {
	case <-retryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for retry to start")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("prompt error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not stop after retry abort")
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

// TestRetryablePromptErrorClassifiesProviderErrors proves auto-retry only fires
// for transient provider/network errors and never for provider-limit
// (quota/billing/usage) errors, matching the TypeScript _isRetryableError /
// _isNonRetryableProviderLimitError classification in
// packages/coding-agent/src/core/agent-session.ts.
func TestRetryablePromptErrorClassifiesProviderErrors(t *testing.T) {
	errorAssistant := func(msg string) []agentcore.AgentMessage {
		return []agentcore.AgentMessage{ai.AssistantMessage{
			Role:         "assistant",
			StopReason:   "error",
			ErrorMessage: msg,
		}}
	}

	cases := []struct {
		name        string
		msg         string
		wantRetried bool
	}{
		{"insufficient_quota", "insufficient_quota", false},
		{"monthly usage limit", "Monthly usage limit reached", false},
		{"network connection lost", "Network connection lost.", true},
		{"overloaded", "overloaded", true},
		{"http 429", "429 Too Many Requests", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := &AgentSession{
				Model:            ai.Model{ContextWindow: 200000},
				autoRetryEnabled: true,
			}
			got := agent.retryablePromptError(errorAssistant(tc.msg), false)
			if tc.wantRetried {
				if got == "" {
					t.Fatalf("expected retryable error for %q, got empty (not retried)", tc.msg)
				}
				if got != tc.msg {
					t.Fatalf("retry message=%q, want %q", got, tc.msg)
				}
			} else if got != "" {
				t.Fatalf("expected %q to NOT be retried, got %q", tc.msg, got)
			}
		})
	}
}
