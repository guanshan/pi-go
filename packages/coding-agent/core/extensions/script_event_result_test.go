package extensions

import (
	"encoding/json"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestApplyScriptEventResultToolCallBlock(t *testing.T) {
	event := &ToolCallEvent{Type: "tool_call", ToolCallID: "c1", ToolName: "bash", Input: map[string]any{"command": "rm -rf /"}}
	result := json.RawMessage(`{"type":"tool_call","toolCallId":"c1","toolName":"bash","input":{"command":"rm -rf /"},"block":true,"reason":"nope"}`)
	applyScriptEventResult("tool_call", event, result)
	if !event.Block || event.Reason != "nope" {
		t.Fatalf("block=%v reason=%q", event.Block, event.Reason)
	}
}

func TestApplyScriptEventResultToolCallInputMutation(t *testing.T) {
	event := &ToolCallEvent{Type: "tool_call", ToolCallID: "c1", ToolName: "write", Input: map[string]any{"path": "a"}}
	// The bridge writes the whole payload back with the mutated input.
	result := json.RawMessage(`{"type":"tool_call","toolCallId":"c1","toolName":"write","input":{"path":"b"}}`)
	applyScriptEventResult("tool_call", event, result)
	input, ok := event.Input.(map[string]any)
	if !ok || input["path"] != "b" {
		t.Fatalf("input=%#v", event.Input)
	}
	if event.Block {
		t.Fatalf("unexpected block")
	}
}

func TestApplyScriptEventResultToolResultOverride(t *testing.T) {
	event := &ToolResultEvent{
		Type:       "tool_result",
		ToolCallID: "c1",
		ToolName:   "lookup",
		Content:    ai.TextBlocks("original"),
		IsError:    false,
	}
	result := json.RawMessage(`{"type":"tool_result","toolCallId":"c1","toolName":"lookup","content":[{"type":"text","text":"new"}],"isError":true,"details":{"k":"v"}}`)
	applyScriptEventResult("tool_result", event, result)
	if len(event.Content) != 1 || event.Content[0].Text != "new" {
		t.Fatalf("content=%#v", event.Content)
	}
	if !event.IsError {
		t.Fatalf("isError not applied")
	}
	details, ok := event.Details.(map[string]any)
	if !ok || details["k"] != "v" {
		t.Fatalf("details=%#v", event.Details)
	}
}

func TestApplyScriptEventResultIgnoresUnrelatedEvents(t *testing.T) {
	event := &ToolCallEvent{Type: "agent_start"}
	applyScriptEventResult("agent_start", event, json.RawMessage(`{"block":true}`))
	if event.Block {
		t.Fatalf("agent_start result must not mutate the payload")
	}
}

// TestToolEventJSONShape locks the TS-shaped wire encoding the script bridge sees.
func TestToolEventJSONShape(t *testing.T) {
	call := &ToolCallEvent{Type: "tool_call", ToolCallID: "c1", ToolName: "bash", Input: map[string]any{"command": "ls"}}
	encoded, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"toolCallId", "toolName", "input"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("tool_call payload missing %q: %s", key, encoded)
		}
	}
	if _, ok := decoded["args"]; ok {
		t.Fatalf("tool_call payload leaks Go field name 'args': %s", encoded)
	}

	res := &ToolResultEvent{Type: "tool_result", ToolCallID: "c1", ToolName: "bash", Input: map[string]any{"command": "ls"}, Content: ai.TextBlocks("out"), IsError: false}
	encoded, err = json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	decoded = nil
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"toolCallId", "toolName", "input", "content", "isError"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("tool_result payload missing %q: %s", key, encoded)
		}
	}
}
