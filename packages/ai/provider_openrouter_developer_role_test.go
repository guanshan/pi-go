package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenRouterReasoningUsesSystemRole locks the parity fix for P1-6: OpenRouter
// reasoning models must send the system prompt with the standard `system` role,
// not `developer`. Mirrors openai-completions.ts:
//
//	supportsDeveloperRole: !isNonStandard && !isOpenRouter
func TestOpenRouterReasoningUsesSystemRole(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openrouter", "test-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "openrouter",
			ID:        "openai/o3-mini",
			API:       "openai-completions",
			BaseURL:   server.URL,
			Reasoning: true,
			Input:     []string{"text"},
		},
		SystemPrompt: "be helpful",
		Messages:     []Message{NewUserMessage("hi", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, ok := captured["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("messages=%#v", captured["messages"])
	}
	first := messages[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("OpenRouter reasoning system prompt role=%q want system (not developer)", first["role"])
	}
}

// TestOpenAIReasoningStillUsesDeveloperRole is the positive control: a first-party
// OpenAI reasoning model still uses the `developer` role, proving the OpenRouter
// guard did not regress standard OpenAI behavior.
func TestOpenAIReasoningStillUsesDeveloperRole(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "openai",
			ID:        "o3-mini",
			API:       "openai-completions",
			BaseURL:   server.URL,
			Reasoning: true,
			Input:     []string{"text"},
		},
		SystemPrompt: "be helpful",
		Messages:     []Message{NewUserMessage("hi", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := captured["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["role"] != "developer" {
		t.Fatalf("OpenAI reasoning system prompt role=%q want developer", first["role"])
	}
}
