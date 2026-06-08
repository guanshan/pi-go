package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnthropicImageContentPayloadGolden locks the Anthropic Messages request
// wire-shape for a user message that carries an image content block. The image
// branch of providers/anthropic.go emits
// {type:"image", source:{type:"base64", media_type, data}}, mirroring the TS
// upstream convertContentBlocks (../pi/packages/ai/src/providers/anthropic.ts
// :110-157). Run with UPDATE_GOLDEN=1 to regenerate the Go snapshot.
func TestAnthropicImageContentPayloadGolden(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	model := Model{
		Provider: "anthropic",
		ID:       "claude-image-golden",
		API:      "anthropic-messages",
		BaseURL:  server.URL,
		Input:    []string{"text", "image"},
	}
	user := UserMessage{Role: "user", Content: []ContentBlock{
		{Type: "text", Text: "describe this"},
		{Type: "image", MimeType: "image/png", Data: "aGVsbG8="},
	}}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:          model,
		SystemPrompt:   "system prompt",
		Messages:       []Message{user},
		CacheRetention: "none",
		MaxTokens:      64,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGolden(t, captured, "anthropic_image_content_payload.golden.json")
}

// TestAnthropicThinkingBudgetPayloadGolden locks the Anthropic Messages request
// wire-shape for a reasoning model that uses budget-based (non-adaptive)
// thinking. With ForceAdaptiveThinking unset and ThinkingBudgets provided,
// providers/anthropic.go emits thinking:{type:"enabled", budget_tokens, display}
// and drops temperature (extended thinking is incompatible with temperature),
// mirroring the TS upstream (../pi/packages/ai/src/providers/anthropic.ts
// :967-974). Run with UPDATE_GOLDEN=1 to regenerate the Go snapshot.
func TestAnthropicThinkingBudgetPayloadGolden(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	temp := 0.7
	model := Model{
		Provider:  "anthropic",
		ID:        "claude-budget-golden",
		API:       "anthropic-messages",
		BaseURL:   server.URL,
		Reasoning: true,
		// ForceAdaptiveThinking deliberately unset so the budget-token path runs.
		Compat: OpenAICompat{ForceAdaptiveThinking: boolPtr(false)},
	}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:           model,
		SystemPrompt:    "system prompt",
		Messages:        []Message{NewUserMessage("please reason", nil)},
		ThinkingLevel:   ThinkingHigh,
		ThinkingBudgets: ThinkingBudgets{High: 12000},
		CacheRetention:  "none",
		MaxTokens:       64,
		Temperature:     &temp,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGolden(t, captured, "anthropic_thinking_budget_payload.golden.json")
}
