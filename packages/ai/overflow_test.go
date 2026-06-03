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

// TestIsContextOverflowAllPatterns ports ../pi/packages/ai/test/overflow.test.ts
// coverage of overflow.ts:34-73 — every one of the 24 OVERFLOW_PATTERNS must be
// detected, and every one of the 3 NON_OVERFLOW_PATTERNS must be suppressed even
// though they may also match an overflow pattern (e.g. "too many tokens").
func TestIsContextOverflowAllPatterns(t *testing.T) {
	// One representative error string per OVERFLOW_PATTERN (overflow.ts:34-58),
	// mirroring the documented example messages. The trailing comment names the
	// pattern each string exercises, in source order.
	overflow := []string{
		"prompt is too long: 213462 tokens > 200000 maximum",                                                  // /prompt is too long/i
		`413 {"error":{"type":"request_too_large","message":"Request exceeds the maximum size"}}`,             // /request_too_large/i
		"Input is too long for requested model.",                                                              // /input is too long for requested model/i (Bedrock)
		"Your input exceeds the context window of this model",                                                 // /exceeds the context window/i
		"Requested token count exceeds the model's maximum context length of 131072 tokens",                   // LiteLLM
		"The input token count (1196265) exceeds the maximum number of tokens allowed (1048575)",              // Google
		"This model's maximum prompt length is 131072 but the request contains 537812 tokens",                 // xAI
		"Please reduce the length of the messages or completion",                                              // Groq
		"This endpoint's maximum context length is 131072 tokens. However, you requested about 200000 tokens", // OpenRouter
		"Input length 131393 exceeds the maximum allowed input length of 131040 tokens.",                      // OpenRouter/Poolside
		"The input (516368 tokens) is longer than the model's context length (262144 tokens).",                // Together AI
		"prompt token count of 200000 exceeds the limit of 128000",                                            // GitHub Copilot
		"the request exceeds the available context size, try increasing it",                                   // llama.cpp
		"tokens to keep from the initial prompt is greater than the context length",                           // LM Studio
		"invalid params, context window exceeds limit",                                                        // MiniMax
		"Your request exceeded model token limit: 131072 (requested: 200000)",                                 // Kimi For Coding
		"Prompt contains 537812 tokens, too large for model with 131072 maximum context length",               // Mistral
		"model_context_window_exceeded",                                                                       // z.ai surfaced as text
		"prompt too long; exceeded max context length by 100918 tokens",                                       // Ollama
		"context_length_exceeded",   // generic fallback
		"too many tokens",           // generic fallback
		"token limit exceeded",      // generic fallback
		"413 status code (no body)", // Cerebras
	}
	if len(overflow) != 23 {
		t.Fatalf("expected 23 distinct overflow example strings, got %d", len(overflow))
	}
	// The 24th pattern (/exceeds (?:the )?(?:model'?s )?maximum context length of
	// [\d,]+ tokens?/i) admits a comma-grouped variant; exercise it separately so
	// the [\d,]+ alternation is covered too.
	overflow = append(overflow, "exceeds the maximum context length of 1,048,576 tokens")
	if len(overflow) != 24 {
		t.Fatalf("expected 24 overflow examples after comma variant, got %d", len(overflow))
	}
	for _, message := range overflow {
		if !IsContextOverflow(overflowTestMessage(message), 0) {
			t.Fatalf("expected overflow for %q", message)
		}
	}

	// One representative error string per NON_OVERFLOW_PATTERN (overflow.ts:69-73).
	// "Throttling error: Too many tokens" also matches the /too many tokens/i
	// overflow pattern but must be suppressed by the non-overflow prefix.
	nonOverflow := []string{
		"Throttling error: Too many tokens, please wait before trying again.", // /^(Throttling error|Service unavailable):/i
		"Service unavailable: temporarily down",                               // /^(Throttling error|Service unavailable):/i
		"Provider hit a rate limit, please retry.",                            // /rate limit/i
		"429 Too Many Requests",                                               // /too many requests/i
	}
	for _, message := range nonOverflow {
		if IsContextOverflow(overflowTestMessage(message), 0) {
			t.Fatalf("did not expect overflow for non-overflow message %q", message)
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
