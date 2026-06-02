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

func TestOpenAIChatPayloadGolden(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	temp := 0.2
	model := Model{
		Provider:  "openai",
		ID:        "gpt-golden",
		API:       "openai-completions",
		BaseURL:   server.URL + "/api.openai.com/v1/chat/completions",
		Reasoning: true,
		Compat: OpenAICompat{
			MaxTokensField:          "max_completion_tokens",
			SupportsDeveloperRole:   boolPtr(true),
			SupportsReasoningEffort: boolPtr(true),
			SupportsStore:           boolPtr(true),
			SupportsStrictMode:      boolPtr(true),
		},
	}
	assistant := NewAssistantMessageForModel(model, []ContentBlock{
		{Type: "text", Text: "I will check."},
		{Type: "toolCall", ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"query":"pi"}`)},
	}, Usage{}, "toolUse")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:        model,
		SystemPrompt: "system prompt",
		Messages: []Message{
			NewUserMessage("please look up", nil),
			assistant,
			NewToolResultMessage("call_1", "lookup", TextBlocks("result text"), nil, false),
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
		ThinkingLevel:  ThinkingHigh,
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
	want, err := os.ReadFile(filepath.Join("testdata", "provider", "openai_chat_payload.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("OpenAI chat payload golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
