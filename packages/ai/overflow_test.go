package ai

import "testing"

func overflowTestMessage(errorMessage string) Message {
	msg := NewAssistantMessage("", "", "", nil, Usage{}, "error")
	msg.ErrorMessage = errorMessage
	return msg
}

func TestIsContextOverflowDetectsProviderErrors(t *testing.T) {
	tests := []string{
		"400 `prompt too long; exceeded max context length by 100918 tokens`",
		"400 The input (516368 tokens) is longer than the model's context length (262144 tokens).",
		"Requested token count exceeds the model's maximum context length of 131072 tokens.",
		"Provider returned error: Input length 131393 exceeds the maximum allowed input length of 131040 tokens.",
	}
	for _, message := range tests {
		if !IsContextOverflow(overflowTestMessage(message), 131072) {
			t.Fatalf("expected overflow for %q", message)
		}
	}
}

func TestIsContextOverflowIgnoresNonOverflowErrors(t *testing.T) {
	tests := []string{
		"500 `model runner crashed unexpectedly`",
		"Throttling error: Too many tokens, please wait before trying again.",
		"Service unavailable: The service is temporarily unavailable.",
		"Rate limit exceeded, please retry after 30 seconds.",
		"Too many requests. Please slow down.",
	}
	for _, message := range tests {
		if IsContextOverflow(overflowTestMessage(message), 200000) {
			t.Fatalf("did not expect overflow for %q", message)
		}
	}
}

func TestIsContextOverflowDetectsSilentLengthOverflow(t *testing.T) {
	message := NewAssistantMessage("", "", "", nil, Usage{Input: 58, CacheRead: 1048512, Output: 0}, "length")
	if !IsContextOverflow(message, 1048576) {
		t.Fatal("expected silent context overflow")
	}

	message.Usage.Output = 4096
	if IsContextOverflow(message, 1048576) {
		t.Fatal("length stop with output should not be overflow")
	}
}
