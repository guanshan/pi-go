package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

// recordingTool captures the raw arguments it executed with so tests can assert
// that a tool_call handler's in-place input mutation reached execution.
type recordingTool struct {
	name     string
	executed int
	rawArgs  json.RawMessage
	result   ai.ToolResult
}

func (t *recordingTool) Name() string        { return t.name }
func (t *recordingTool) Description() string { return "records executed arguments" }
func (t *recordingTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"path":    map[string]any{"type": "string"},
		},
	}
}

func (t *recordingTool) Execute(_ context.Context, raw json.RawMessage, _ catools.ToolUpdate) ai.ToolResult {
	t.executed++
	t.rawArgs = append(json.RawMessage(nil), raw...)
	if len(t.result.Content) == 0 {
		return ai.ToolResult{Content: ai.TextBlocks("ok")}
	}
	return t.result
}

// toolCallProvider is an OpenAI-completions-shaped server that returns a single
// tool call to toolName with the given arguments JSON, then "done" on the next
// turn. It mirrors the wiring in TestAgentSessionDelegatesToolLoopToAgentPackage.
func toolCallProvider(t *testing.T, toolName, argsJSON string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			payload := map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]any{
						"content": "",
						"tool_calls": []any{map[string]any{
							"id":   "call_1",
							"type": "function",
							"function": map[string]any{
								"name":      toolName,
								"arguments": argsJSON,
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
				"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			}
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func newToolHookAgent(t *testing.T, server *httptest.Server, tool catools.RuntimeTool, factory coreext.Factory) (*AgentSession, *SessionManager) {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	registry.Auth.SetRuntime("openai", "test-key")
	model := ai.Model{Provider: "openai", ID: "gpt-test", API: "openai-completions", BaseURL: server.URL}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{tool.Name(): tool}, "system")
	runtime, err := coreext.NewRunner(factory)
	if err != nil {
		t.Fatal(err)
	}
	agent.extensionRuntime = runtime
	return agent, session
}

func TestBeforeToolCallHookBlocksExecution(t *testing.T) {
	server, _ := toolCallProvider(t, "bash", `{"command":"rm -rf /"}`)
	tool := &recordingTool{name: "bash"}
	var sawInput any
	agent, session := newToolHookAgent(t, server, tool, func(api *coreext.API) error {
		api.On("tool_call", func(payload any) {
			event := payload.(*coreext.ToolCallEvent)
			sawInput = event.Input
			if event.ToolName == "bash" {
				event.Block = true
				event.Reason = "blocked: rm -rf"
			}
		})
		return nil
	})

	if err := agent.Prompt(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	if tool.executed != 0 {
		t.Fatalf("blocked tool executed %d times", tool.executed)
	}
	// The handler must observe the TS-shaped input payload.
	input, ok := sawInput.(map[string]any)
	if !ok || input["command"] != "rm -rf /" {
		t.Fatalf("handler input=%#v", sawInput)
	}
	// The tool result message must carry the block reason.
	messages := session.BuildContext().Messages
	var toolResultText string
	for _, m := range messages {
		if ai.MessageRole(m) == "toolResult" {
			toolResultText = ai.MessageText(m)
		}
	}
	if toolResultText != "blocked: rm -rf" {
		t.Fatalf("tool result text=%q messages=%#v", toolResultText, messages)
	}
}

func TestBeforeToolCallHookMutatesInput(t *testing.T) {
	server, _ := toolCallProvider(t, "write", `{"path":"original.txt","command":"keep"}`)
	tool := &recordingTool{name: "write"}
	agent, _ := newToolHookAgent(t, server, tool, func(api *coreext.API) error {
		api.On("tool_call", func(payload any) {
			event := payload.(*coreext.ToolCallEvent)
			input, ok := event.Input.(map[string]any)
			if !ok {
				t.Errorf("input not a map: %#v", event.Input)
				return
			}
			input["path"] = "mutated.txt"
		})
		return nil
	})

	if err := agent.Prompt(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	if tool.executed != 1 {
		t.Fatalf("tool executed %d times", tool.executed)
	}
	var got map[string]any
	if err := json.Unmarshal(tool.rawArgs, &got); err != nil {
		t.Fatalf("executed args invalid: %v (%s)", err, tool.rawArgs)
	}
	if got["path"] != "mutated.txt" {
		t.Fatalf("executed path=%v want mutated.txt (raw=%s)", got["path"], tool.rawArgs)
	}
}

func TestBeforeToolCallHookNoMutationLeavesArgsUnchanged(t *testing.T) {
	server, _ := toolCallProvider(t, "read", `{"path":"keep.txt"}`)
	tool := &recordingTool{name: "read"}
	agent, _ := newToolHookAgent(t, server, tool, func(api *coreext.API) error {
		api.On("tool_call", func(payload any) {
			_ = payload.(*coreext.ToolCallEvent) // observe only
		})
		return nil
	})

	if err := agent.Prompt(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(tool.rawArgs, &got); err != nil {
		t.Fatalf("executed args invalid: %v (%s)", err, tool.rawArgs)
	}
	if got["path"] != "keep.txt" {
		t.Fatalf("executed path=%v want keep.txt", got["path"])
	}
	if len(agent.mutatedToolArgs) != 0 {
		t.Fatalf("stash not cleared: %#v", agent.mutatedToolArgs)
	}
	if len(agent.mutatedToolInputs) != 0 {
		t.Fatalf("input stash not cleared: %#v", agent.mutatedToolInputs)
	}
}

func TestAfterToolCallHookOverridesResult(t *testing.T) {
	server, _ := toolCallProvider(t, "lookup", `{"command":"x"}`)
	tool := &recordingTool{name: "lookup", result: ai.ToolResult{Content: ai.TextBlocks("original output")}}
	agent, session := newToolHookAgent(t, server, tool, func(api *coreext.API) error {
		api.On("tool_result", func(payload any) {
			event := payload.(*coreext.ToolResultEvent)
			event.Content = ai.TextBlocks("overridden output")
			event.IsError = true
		})
		return nil
	})

	if err := agent.Prompt(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	if tool.executed != 1 {
		t.Fatalf("tool executed %d times", tool.executed)
	}
	messages := session.BuildContext().Messages
	var toolResult ai.ToolResultMessage
	found := false
	for _, m := range messages {
		if tr, ok := m.(ai.ToolResultMessage); ok {
			toolResult = tr
			found = true
		}
	}
	if !found {
		t.Fatalf("no tool result message in %#v", messages)
	}
	if ai.MessageText(toolResult) != "overridden output" {
		t.Fatalf("tool result text=%q", ai.MessageText(toolResult))
	}
	if !toolResult.IsError {
		t.Fatalf("expected overridden isError=true, got %#v", toolResult)
	}
}

func TestAfterToolCallHookNoHandlerKeepsResult(t *testing.T) {
	server, _ := toolCallProvider(t, "lookup", `{"command":"x"}`)
	tool := &recordingTool{name: "lookup", result: ai.ToolResult{Content: ai.TextBlocks("original output")}}
	// Only a tool_call handler is registered, so tool_result must not be touched.
	agent, session := newToolHookAgent(t, server, tool, func(api *coreext.API) error {
		api.On("tool_call", func(payload any) { _ = payload })
		return nil
	})

	if err := agent.Prompt(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	messages := session.BuildContext().Messages
	for _, m := range messages {
		if tr, ok := m.(ai.ToolResultMessage); ok {
			if ai.MessageText(tr) != "original output" || tr.IsError {
				t.Fatalf("result unexpectedly altered: %q err=%v", ai.MessageText(tr), tr.IsError)
			}
		}
	}
}
