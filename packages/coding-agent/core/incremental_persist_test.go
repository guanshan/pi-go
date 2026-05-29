package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

// midRunTool records how many messages are already persisted to the session at
// the moment the tool executes (mid-turn).
type midRunTool struct {
	session    *SessionManager
	midPersist int
}

func (t *midRunTool) Name() string           { return "probe" }
func (t *midRunTool) Description() string    { return "probe persisted state" }
func (t *midRunTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *midRunTool) Execute(ctx context.Context, raw json.RawMessage, _ catools.ToolUpdate) ai.ToolResult {
	t.midPersist = len(t.session.BuildContext().Messages)
	return ai.ToolResult{Content: ai.TextBlocks("probed")}
}

// TestPromptPersistsMessagesIncrementally verifies messages are persisted as
// they complete (message_end) rather than batched at the end of the run. When
// the tool executes, the user message and the assistant tool-call message must
// already be on the session branch.
func TestPromptPersistsMessagesIncrementally(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"probe","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
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
	session := InMemorySession(cwd)
	tool := &midRunTool{session: session}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{"probe": tool}, "system")

	if err := agent.Prompt(context.Background(), "use tool", nil, nil); err != nil {
		t.Fatal(err)
	}

	// At tool-execution time, the user message and the assistant tool-call
	// message should already be persisted (2). With end-of-run batch append this
	// would have been 0.
	if tool.midPersist < 2 {
		t.Fatalf("expected >=2 messages persisted mid-run, got %d", tool.midPersist)
	}
	final := len(session.BuildContext().Messages)
	if final != 4 {
		t.Fatalf("final messages=%d, want 4", final)
	}
}
