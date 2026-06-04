package openaichat

import (
	"testing"
)

// TestStreamAccumulatorDecodesTextToolsReasoningUsage decodes a representative
// sequence of OpenAI-completions SSE chunks (text delta, reasoning delta, a
// streamed tool call, usage, and finish_reason) via ApplyRaw and asserts the
// accumulated Parsed() output, exercising the streaming decode path that
// previously had no coverage.
func TestStreamAccumulatorDecodesTextToolsReasoningUsage(t *testing.T) {
	acc := NewStreamAccumulator("openai")

	chunks := []string{
		`{"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		`{"id":"chatcmpl-1","model":"gpt-test","choices":[{"index":0,"delta":{"content":" world"}}]}`,
		`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"reasoning":"thinking..."}}]}`,
		`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"go\"}"}}]}}]}`,
		`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`,
	}

	var textDeltas, thinkingDeltas, toolDeltas int
	for _, raw := range chunks {
		updates, err := acc.ApplyRaw([]byte(raw))
		if err != nil {
			t.Fatalf("ApplyRaw(%s): %v", raw, err)
		}
		for _, u := range updates {
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

	if !acc.SawChunk() {
		t.Fatal("expected SawChunk to be true")
	}
	if !acc.SawFinishReason() {
		t.Fatal("expected SawFinishReason to be true")
	}
	if textDeltas != 2 {
		t.Fatalf("text deltas=%d, want 2", textDeltas)
	}
	if thinkingDeltas != 1 {
		t.Fatalf("thinking deltas=%d, want 1", thinkingDeltas)
	}
	if toolDeltas != 2 {
		t.Fatalf("toolcall deltas=%d, want 2", toolDeltas)
	}

	parsed := acc.Parsed(true)
	if parsed.ResponseID != "chatcmpl-1" || parsed.ResponseModel != "gpt-test" {
		t.Fatalf("response id/model=%q/%q", parsed.ResponseID, parsed.ResponseModel)
	}
	if parsed.StopReason != "toolUse" {
		t.Fatalf("stop reason=%q, want toolUse", parsed.StopReason)
	}
	if parsed.Usage.Input != 12 || parsed.Usage.Output != 5 {
		t.Fatalf("usage=%#v", parsed.Usage)
	}
	if len(parsed.ToolCalls) != 1 {
		t.Fatalf("tool calls=%d, want 1", len(parsed.ToolCalls))
	}
	call := parsed.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "lookup" {
		t.Fatalf("tool call=%#v", call)
	}
	if string(call.Arguments) != `{"q":"go"}` {
		t.Fatalf("tool args=%s, want {\"q\":\"go\"}", call.Arguments)
	}

	// Blocks must contain text, thinking, and the tool call in stream order.
	var haveText, haveThinking, haveTool bool
	for _, b := range parsed.Blocks {
		switch b.Type {
		case "text":
			haveText = b.Text == "Hello world"
		case "thinking":
			haveThinking = b.Thinking == "thinking..."
		case "toolCall":
			haveTool = true
		}
	}
	if !haveText || !haveThinking || !haveTool {
		t.Fatalf("blocks missing content: %#v", parsed.Blocks)
	}
}

// TestStreamAccumulatorSurfacesErrorPayload locks the OpenRouter-style error
// envelope decode path: an error chunk turns into an error, including the
// metadata.raw diagnostic suffix.
func TestStreamAccumulatorSurfacesErrorPayload(t *testing.T) {
	acc := NewStreamAccumulator()
	_, err := acc.ApplyRaw([]byte(`{"error":{"message":"boom","metadata":{"raw":"upstream detail"}}}`))
	if err == nil {
		t.Fatal("expected error from error payload")
	}
	if err.Error() != "boom\nupstream detail" {
		t.Fatalf("err=%q, want boom\\nupstream detail", err.Error())
	}
}

// TestStreamAccumulatorMissingFinishReason guards the truncation signal: a
// stream that never emits finish_reason must report SawFinishReason()==false so
// callers can treat it as truncated rather than a silent stop.
func TestStreamAccumulatorMissingFinishReason(t *testing.T) {
	acc := NewStreamAccumulator()
	if _, err := acc.ApplyRaw([]byte(`{"id":"x","choices":[{"index":0,"delta":{"content":"partial"}}]}`)); err != nil {
		t.Fatal(err)
	}
	if acc.SawFinishReason() {
		t.Fatal("SawFinishReason should be false when no finish_reason was streamed")
	}
}
