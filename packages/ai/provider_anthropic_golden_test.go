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

// TestAnthropicMessagesPayloadGolden locks the full Anthropic Messages request
// wire-shape (system blocks + cache_control, multi-turn messages with tool_use /
// tool_result content blocks, tools, tool_choice) as a byte-for-byte golden, the
// Anthropic counterpart to TestOpenAIChatPayloadGolden. Run with
// UPDATE_GOLDEN=1 to regenerate the golden file.
func TestAnthropicMessagesPayloadGolden(t *testing.T) {
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
	temp := 0.2
	model := Model{
		Provider: "anthropic",
		ID:       "claude-golden",
		API:      "anthropic-messages",
		BaseURL:  server.URL,
	}
	assistant := NewAssistantMessageForModel(model, []ContentBlock{
		{Type: "text", Text: "I will check."},
		{Type: "toolCall", ID: "toolu_1", Name: "lookup", Arguments: json.RawMessage(`{"query":"pi"}`)},
	}, Usage{}, "toolUse")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:        model,
		SystemPrompt: "system prompt",
		Messages: []Message{
			NewUserMessage("please look up", nil),
			assistant,
			NewToolResultMessage("toolu_1", "lookup", TextBlocks("result text"), nil, false),
			NewUserMessage("summarize", nil),
		},
		Tools: ToolSet{
			"lookup": {
				Name:        "lookup",
				Description: "Look up docs",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
					"required": []string{"query"},
				},
			},
		},
		ToolChoice:     map[string]any{"name": "lookup"},
		CacheRetention: "long",
		SessionID:      "golden-session",
		MaxTokens:      64,
		Temperature:    &temp,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := json.MarshalIndent(captured, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	goldenPath := filepath.Join("testdata", "provider", "anthropic_messages_payload.golden.json")
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
		t.Fatalf("Anthropic messages payload golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
