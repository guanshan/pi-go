package providers

import (
	"strings"
	"testing"
)

func TestOpenAIChatCompletionErrorFinishReasons(t *testing.T) {
	for _, reason := range []string{"content_filter", "network_error", "provider_made_this_up"} {
		raw := []byte(`{"choices":[{"message":{"content":"blocked"},"finish_reason":"` + reason + `"}]}`)
		parsed, err := ParseOpenAIChatCompletionRaw(raw)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.StopReason != "error" || !strings.Contains(parsed.ErrorMessage, reason) {
			t.Fatalf("reason %q parsed as stopReason=%q errorMessage=%q", reason, parsed.StopReason, parsed.ErrorMessage)
		}
	}
}

func TestAnthropicStopReasonErrors(t *testing.T) {
	// refusal/sensitive map to error with no message (TS mapStopReason returns
	// "error" and leaves errorMessage for the caller's fallback). Unknown reasons
	// carry an "Unhandled stop reason" message that includes the reason text.
	for _, reason := range []string{"refusal", "sensitive"} {
		stopReason, errorMessage := AnthropicStopReason(reason)
		if stopReason != "error" || errorMessage != "" {
			t.Fatalf("reason %q mapped to stopReason=%q errorMessage=%q", reason, stopReason, errorMessage)
		}
	}
	stopReason, errorMessage := AnthropicStopReason("provider_made_this_up")
	if stopReason != "error" || !strings.Contains(errorMessage, "provider_made_this_up") {
		t.Fatalf("unknown reason mapped to stopReason=%q errorMessage=%q", stopReason, errorMessage)
	}
}

// TestAnthropicStopReasonParity locks the mapping to the TypeScript
// mapStopReason in packages/ai/src/providers/anthropic.ts. Crucially, the Go
// mapping no longer rewrites end_turn/pause_turn/stop_sequence/"" to "toolUse"
// based on tool-call presence: Anthropic natively returns "tool_use" when the
// model calls tools, matching TS which has no such rewrite for this provider.
func TestAnthropicStopReasonParity(t *testing.T) {
	cases := map[string]string{
		"end_turn":      "stop",
		"pause_turn":    "stop",
		"stop_sequence": "stop",
		"":              "stop", // documented divergence: TS throws, Go maps to stop
		"max_tokens":    "length",
		"tool_use":      "toolUse",
	}
	for reason, want := range cases {
		got, errorMessage := AnthropicStopReason(reason)
		if got != want || errorMessage != "" {
			t.Fatalf("reason %q mapped to stopReason=%q errorMessage=%q, want %q", reason, got, errorMessage, want)
		}
	}
}

func TestOpenAIResponsesUnknownStatusIsError(t *testing.T) {
	parsed, err := ParseOpenAIResponses([]byte(`{
		"id":"resp_unknown",
		"status":"provider_made_this_up",
		"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"partial"}]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.StopReason != "error" || !strings.Contains(parsed.ErrorMessage, "provider_made_this_up") {
		t.Fatalf("parsed stopReason=%q errorMessage=%q", parsed.StopReason, parsed.ErrorMessage)
	}
}
