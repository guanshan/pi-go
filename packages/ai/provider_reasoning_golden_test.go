package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenAIResponsesReasoningPayloadGolden locks the OpenAI Responses request
// wire-shape for a reasoning-enabled, high-effort request as a byte-for-byte
// golden. It captures the request body emitted by registry.StreamlessChat and
// compares it against testdata/provider/openai_responses_reasoning_payload.golden.json.
// Run with UPDATE_GOLDEN=1 to regenerate the golden file.
//
// This is the reasoning/thinking counterpart to TestOpenAIChatPayloadGolden and
// reuses the same httptest capture + UPDATE_GOLDEN regenerate pattern.
func TestOpenAIResponsesReasoningPayloadGolden(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_golden","status":"completed","output":[{"type":"message","id":"msg_g","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	model := Model{
		Provider:  "openai",
		ID:        "gpt-golden",
		API:       "openai-responses",
		BaseURL:   server.URL + "/v1",
		Input:     []string{"text"},
		Reasoning: true,
	}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:         model,
		SystemPrompt:  "system prompt",
		Messages:      []Message{NewUserMessage("please reason", nil)},
		ThinkingLevel: ThinkingHigh,
		SessionID:     "golden-session",
		MaxTokens:     64,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGolden(t, captured, "openai_responses_reasoning_payload.golden.json")
}

// TestAnthropicAdaptiveThinkingPayloadGolden locks the Anthropic Messages
// request wire-shape for a model that forces adaptive thinking (Opus 4.8 family
// behavior). Adaptive thinking emits `thinking:{type:"adaptive"}` plus an
// `output_config.effort`, distinct from the budget-token enabled-thinking shape.
// Run with UPDATE_GOLDEN=1 to regenerate the golden file.
func TestAnthropicAdaptiveThinkingPayloadGolden(t *testing.T) {
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
		Provider:  "anthropic",
		ID:        "claude-adaptive-golden",
		API:       "anthropic-messages",
		BaseURL:   server.URL,
		Reasoning: true,
		Compat:    OpenAICompat{ForceAdaptiveThinking: boolPtr(true)},
	}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:         model,
		SystemPrompt:  "system prompt",
		Messages:      []Message{NewUserMessage("please reason", nil)},
		ThinkingLevel: ThinkingHigh,
		MaxTokens:     64,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGolden(t, captured, "anthropic_adaptive_thinking_payload.golden.json")
}

// assertGolden marshals the captured request body with indentation and compares
// it against testdata/provider/<name>, regenerating when UPDATE_GOLDEN is set.
func assertGolden(t *testing.T, captured map[string]any, name string) {
	t.Helper()
	got, err := json.MarshalIndent(captured, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	goldenPath := filepath.Join("testdata", "provider", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s golden mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
	assertTSProviderFixture(t, got, name)
}
