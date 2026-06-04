package openairesponses

import (
	"encoding/json"
	"testing"
)

// decodeEvent parses a JSON SSE event payload into the map[string]any shape the
// streaming decoder consumes, mirroring how the transport hands decoded events
// to StreamState.Apply.
func decodeEvent(t *testing.T, raw string) map[string]any {
	t.Helper()
	var event map[string]any
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("decode event %s: %v", raw, err)
	}
	return event
}

// TestStreamStateDecodesTextReasoningToolAndUsage feeds a representative
// Responses-API event sequence (created, reasoning summary, text deltas, a
// function call, and a completed response carrying usage) and asserts the
// accumulated Parsed() output, covering the previously-untested decode path.
func TestStreamStateDecodesTextReasoningToolAndUsage(t *testing.T) {
	state := NewStreamState()

	events := []string{
		`{"type":"response.created","response":{"id":"resp-1","model":"gpt-resp","service_tier":"default","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"r1"}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"r1","delta":"plan"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"m1","role":"assistant"}}`,
		`{"type":"response.output_text.delta","output_index":1,"item_id":"m1","delta":"Hi "}`,
		`{"type":"response.output_text.delta","output_index":1,"item_id":"m1","delta":"there"}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"f1","call_id":"call-7","name":"search"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":2,"item_id":"f1","delta":"{\"q\":\"x\"}"}`,
		`{"type":"response.function_call_arguments.done","output_index":2,"item_id":"f1","arguments":"{\"q\":\"x\"}"}`,
		`{"type":"response.completed","response":{"id":"resp-1","model":"gpt-resp","status":"completed","usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28,"input_tokens_details":{"cached_tokens":4}},"output":[{"type":"function_call","id":"f1","call_id":"call-7","name":"search","arguments":"{\"q\":\"x\"}"}]}}`,
	}

	var textDeltas, thinkingDeltas, toolDeltas int
	for _, raw := range events {
		for _, u := range state.Apply(decodeEvent(t, raw)) {
			switch u.Type {
			case "text_delta":
				textDeltas++
			case "thinking_delta":
				thinkingDeltas++
			case "toolcall_delta":
				toolDeltas++
			}
		}
	}

	if textDeltas != 2 {
		t.Fatalf("text deltas=%d, want 2", textDeltas)
	}
	if thinkingDeltas != 1 {
		t.Fatalf("thinking deltas=%d, want 1", thinkingDeltas)
	}
	if toolDeltas < 1 {
		t.Fatalf("toolcall deltas=%d, want >=1", toolDeltas)
	}

	parsed := state.Parsed()
	if parsed.ResponseID != "resp-1" || parsed.ResponseModel != "gpt-resp" {
		t.Fatalf("response id/model=%q/%q", parsed.ResponseID, parsed.ResponseModel)
	}
	if parsed.ServiceTier != "default" {
		t.Fatalf("service tier=%q", parsed.ServiceTier)
	}
	if parsed.StopReason != "toolUse" {
		t.Fatalf("stop reason=%q, want toolUse", parsed.StopReason)
	}
	// input_tokens 20 minus 4 cached => 16 input, 4 cacheRead.
	if parsed.Usage.Input != 16 || parsed.Usage.Output != 8 || parsed.Usage.CacheRead != 4 {
		t.Fatalf("usage=%#v", parsed.Usage)
	}
	if len(parsed.ToolCalls) != 1 {
		t.Fatalf("tool calls=%d, want 1", len(parsed.ToolCalls))
	}
	// The decoder composes the tool-call id from call_id and the item id
	// (call_id|item_id) so the per-item argument stream can be reassembled.
	if call := parsed.ToolCalls[0]; call.ID != "call-7|f1" || call.Name != "search" || string(call.Arguments) != `{"q":"x"}` {
		t.Fatalf("tool call=%#v args=%s", call, call.Arguments)
	}

	var haveText, haveThinking bool
	for _, b := range parsed.Blocks {
		switch b.Type {
		case "text":
			haveText = b.Text == "Hi there"
		case "thinking":
			haveThinking = b.Thinking == "plan"
		}
	}
	if !haveText || !haveThinking {
		t.Fatalf("blocks missing text/thinking: %#v", parsed.Blocks)
	}
}

// TestStreamStateSurfacesErrorEvent locks the error-event decode path: an
// `error` event sets stopReason=error and a formatted message.
func TestStreamStateSurfacesErrorEvent(t *testing.T) {
	state := NewStreamState()
	state.Apply(decodeEvent(t, `{"type":"error","code":"rate_limit","message":"slow down"}`))
	parsed := state.Parsed()
	if parsed.StopReason != "error" {
		t.Fatalf("stop reason=%q, want error", parsed.StopReason)
	}
	if parsed.ErrorMessage != "Error Code rate_limit: slow down" {
		t.Fatalf("error message=%q", parsed.ErrorMessage)
	}
}

// TestStreamStateFailedResponseReportsError covers the response.failed branch.
func TestStreamStateFailedResponseReportsError(t *testing.T) {
	state := NewStreamState()
	state.Apply(decodeEvent(t, `{"type":"response.failed","response":{"id":"r","status":"failed","error":{"code":"server_error","message":"boom"}}}`))
	parsed := state.Parsed()
	if parsed.StopReason == "stop" {
		t.Fatalf("failed response should not report stop, got %q", parsed.StopReason)
	}
	if parsed.ErrorMessage == "" {
		t.Fatal("expected non-empty error message for failed response")
	}
}
