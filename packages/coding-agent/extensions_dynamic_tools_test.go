package codingagent

import (
	"context"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

// TestDynamicToolRegistrationRefreshesRegistry verifies the after-init refresh
// path: a tool registered dynamically during a session_start handler (not at
// factory time) is reflected in the runner's registered-tools registry, carries
// the correct metadata, and is actually invocable.
//
// Note on parity scope: TS distinguishes "promptSnippet omitted -> hidden from
// the prompt/available list but still callable". The Go ToolDefinition has no
// promptSnippet field, so that visibility distinction does not exist here; the
// test asserts the behavior Go does model (discoverable + callable) and this
// comment records the semantic gap rather than forcing an implementation change.
func TestDynamicToolRegistrationRefreshesRegistry(t *testing.T) {
	executed := false
	runner, err := NewExtensionRunner(func(api *ExtensionAPI) error {
		// Register the dynamic tool only when session_start fires, simulating an
		// extension that adds tools after initialization rather than at factory time.
		api.On("session_start", func(any) {
			api.RegisterTool(DefineTool(
				"dynamic_tool",
				"A tool registered after session start",
				map[string]any{"type": "object", "properties": map[string]any{}},
				func(context.Context, []byte) (ai.ToolResult, error) {
					executed = true
					return ai.ToolResult{Content: ai.TextBlocks("dynamic ran")}, nil
				},
			))
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Shutdown(context.Background()) }()

	// Before session_start the dynamic tool must not exist (it is added later).
	if _, ok := runner.GetToolDefinition("dynamic_tool"); ok {
		t.Fatal("dynamic_tool should not be registered before session_start")
	}

	runner.Emit("session_start", &coreext.SessionStartEvent{Type: "session_start", Reason: coreext.SessionStartStartup})

	// After session_start the registry must reflect the dynamically added tool.
	def, ok := runner.GetToolDefinition("dynamic_tool")
	if !ok {
		t.Fatal("dynamic_tool not present in registry after session_start refresh")
	}
	if def.Description != "A tool registered after session start" {
		t.Fatalf("dynamic_tool metadata wrong: %#v", def)
	}

	// GetAllRegisteredTools (the full available set) must also include it.
	found := false
	for _, tool := range runner.GetAllRegisteredTools() {
		if tool.Name == "dynamic_tool" {
			found = true
		}
	}
	if !found {
		t.Fatal("dynamic_tool missing from GetAllRegisteredTools after refresh")
	}

	// The dynamically registered tool must be callable.
	if def.Execute == nil {
		t.Fatal("dynamic_tool has no executor")
	}
	result, err := def.Execute(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("dynamic_tool execute error: %v", err)
	}
	if !executed {
		t.Fatal("dynamic_tool executor did not run")
	}
	if got := ai.MessageText(ai.ToolResultMessage{Content: result.Content}); got != "dynamic ran" {
		t.Fatalf("dynamic_tool output=%q want %q", got, "dynamic ran")
	}
}
