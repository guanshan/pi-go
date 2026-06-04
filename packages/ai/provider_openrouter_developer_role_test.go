package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// openRouterSystemRole captures the system-prompt role an OpenRouter reasoning
// model emits for the given model id.
func openRouterSystemRole(t *testing.T, modelID string) string {
	t.Helper()
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
			ID:        modelID,
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
	return messages[0].(map[string]any)["role"].(string)
}

// TestOpenRouterReasoningDeveloperRole locks the parity fix: OpenRouter
// reasoning models whose id is prefixed openai/ or anthropic/ DO use the
// `developer` role (those backends accept it), while every other OpenRouter
// reasoning backend rejects developer and must use the standard `system` role.
// Mirrors openai-completions.ts:
//
//	const isOpenRouterDeveloperRoleModel =
//	    isOpenRouter && (model.id.startsWith("anthropic/") || model.id.startsWith("openai/"));
//	supportsDeveloperRole: isOpenRouterDeveloperRoleModel || (!isNonStandard && !isOpenRouter)
func TestOpenRouterReasoningDeveloperRole(t *testing.T) {
	if role := openRouterSystemRole(t, "openai/o3-mini"); role != "developer" {
		t.Fatalf("OpenRouter openai/* reasoning role=%q want developer", role)
	}
	if role := openRouterSystemRole(t, "anthropic/claude-opus-4-1"); role != "developer" {
		t.Fatalf("OpenRouter anthropic/* reasoning role=%q want developer", role)
	}
	// A non-prefixed OpenRouter reasoning backend must fall back to system.
	if role := openRouterSystemRole(t, "qwen/qwen3-235b-a22b-thinking"); role != "system" {
		t.Fatalf("OpenRouter non-prefixed reasoning role=%q want system", role)
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
