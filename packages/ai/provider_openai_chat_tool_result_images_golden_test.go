package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenAIChatToolResultImagesPayloadGolden locks the OpenAI Chat Completions
// request wire-shape when an assistant turn emits two tool calls and the two
// consecutive tool results carry text + image content. providers/openai_chat.go
// (OpenAIChatMessages, ~297-323) emits one role:"tool" message per result then a
// single trailing synthetic role:"user" message that batches every image_url
// part behind the "Attached image(s) from tool result:" text part. This ports
// ../pi/packages/ai/test/openai-completions-tool-result-images.test.ts and the
// TS converter in ../pi/packages/ai/src/providers/openai-completions.ts:918-983.
// Run with UPDATE_GOLDEN=1 to regenerate the Go snapshot.
func TestOpenAIChatToolResultImagesPayloadGolden(t *testing.T) {
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
	model := Model{
		Provider: "openai",
		ID:       "gpt-image-golden",
		API:      "openai-completions",
		BaseURL:  server.URL + "/api.openai.com/v1/chat/completions",
		Input:    []string{"text", "image"},
	}
	assistant := NewAssistantMessageForModel(model, []ContentBlock{
		{Type: "toolCall", ID: "tool-1", Name: "read", Arguments: json.RawMessage(`{"path":"img-1.png"}`)},
		{Type: "toolCall", ID: "tool-2", Name: "read", Arguments: json.RawMessage(`{"path":"img-2.png"}`)},
	}, Usage{}, "toolUse")
	toolResult := func(id string) ToolResultMessage {
		return NewToolResultMessage(id, "read", []ContentBlock{
			{Type: "text", Text: "Read image file [image/png]"},
			{Type: "image", MimeType: "image/png", Data: "ZmFrZQ=="},
		}, nil, false)
	}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:        model,
		SystemPrompt: "system prompt",
		Messages: []Message{
			NewUserMessage("Read the images", nil),
			assistant,
			toolResult("tool-1"),
			toolResult("tool-2"),
		},
		CacheRetention: "none",
		MaxTokens:      64,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGolden(t, captured, "openai_chat_tool_result_images_payload.golden.json")
}
