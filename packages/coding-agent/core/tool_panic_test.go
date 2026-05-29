package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

type panicTool struct{}

func (panicTool) Name() string           { return "boom" }
func (panicTool) Description() string    { return "always panics" }
func (panicTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (panicTool) Execute(ctx context.Context, raw json.RawMessage, _ catools.ToolUpdate) ai.ToolResult {
	panic("kaboom")
}

// TestPanickingToolDoesNotCrashRun verifies the agentToolAdapter recovers a
// panicking tool into an error result instead of crashing the agent run.
func TestPanickingToolDoesNotCrashRun(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"boom","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer server.Close()

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	registry.Auth.SetRuntime("openai", "test-key")
	model := ai.Model{Provider: "openai", ID: "gpt-test", API: "openai-completions", BaseURL: server.URL}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{"boom": panicTool{}}, "system")

	if err := agent.Prompt(context.Background(), "use tool", nil, nil); err != nil {
		t.Fatalf("prompt errored: %v", err)
	}

	messages := session.BuildContext().Messages
	var toolResultText string
	for _, m := range messages {
		if ai.MessageRole(m) == "toolResult" {
			toolResultText = ai.MessageText(m)
		}
	}
	if !strings.Contains(toolResultText, "panicked") {
		t.Fatalf("expected recovered panic in tool result, got %q (messages=%d)", toolResultText, len(messages))
	}
}
