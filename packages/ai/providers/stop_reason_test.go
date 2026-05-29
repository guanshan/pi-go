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
	for _, reason := range []string{"refusal", "sensitive", "provider_made_this_up"} {
		stopReason, errorMessage := AnthropicStopReason(reason, false)
		if stopReason != "error" || !strings.Contains(errorMessage, reason) {
			t.Fatalf("reason %q mapped to stopReason=%q errorMessage=%q", reason, stopReason, errorMessage)
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
