package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// streamUsageResult runs a single-chunk OpenAI completions stream over the HTTP
// path and returns the finalized message usage.
func streamUsageResult(t *testing.T, usageJSON string) Usage {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"id":"c","model":"m","choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}],"usage":` + usageJSON + `}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openrouter", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model:    Model{Provider: "openrouter", ID: "x", API: "openai-completions", BaseURL: server.URL + "/compat", Input: []string{"text"}},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	for range stream.Events() {
	}
	return stream.Result().Usage
}

// TestOpenAIChatStreamUsageCacheWrite locks the parity fix for P1-3: the
// streaming path must read the non-standard cache_write_tokens (OpenRouter/DS4)
// just like the non-streaming path, so cacheWrite is recorded and input is
// computed as prompt - cacheRead - cacheWrite. Mirrors parseChunkUsage in
// openai-completions.ts.
func TestOpenAIChatStreamUsageCacheWrite(t *testing.T) {
	usage := streamUsageResult(t, `{"prompt_tokens":20,"completion_tokens":5,"total_tokens":32,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":3}}`)
	if usage.Input != 13 {
		t.Errorf("input=%d want 13 (20-4-3)", usage.Input)
	}
	if usage.CacheRead != 4 {
		t.Errorf("cacheRead=%d want 4", usage.CacheRead)
	}
	if usage.CacheWrite != 3 {
		t.Errorf("cacheWrite=%d want 3 (streaming previously dropped this)", usage.CacheWrite)
	}
	if usage.Output != 5 {
		t.Errorf("output=%d want 5", usage.Output)
	}
}

// TestOpenAIChatStreamUsageDeepSeekCacheHit locks the DeepSeek-style
// prompt_cache_hit_tokens fallback on the streaming path (no cached_tokens
// present): cacheRead must reflect the hit count and input must be reduced.
func TestOpenAIChatStreamUsageDeepSeekCacheHit(t *testing.T) {
	usage := streamUsageResult(t, `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":0,"prompt_cache_hit_tokens":4}`)
	if usage.CacheRead != 4 {
		t.Errorf("cacheRead=%d want 4 (from prompt_cache_hit_tokens)", usage.CacheRead)
	}
	if usage.Input != 6 {
		t.Errorf("input=%d want 6 (10-4)", usage.Input)
	}
	if usage.CacheWrite != 0 {
		t.Errorf("cacheWrite=%d want 0", usage.CacheWrite)
	}
	// total_tokens was 0 so it must be derived: input + output + cacheRead + cacheWrite.
	if usage.TotalTokens != 12 {
		t.Errorf("totalTokens=%d want 12 (6+2+4+0)", usage.TotalTokens)
	}
}
