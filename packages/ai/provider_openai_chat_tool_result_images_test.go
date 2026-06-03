package ai

import (
	"encoding/json"
	"testing"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

// TestOpenAIChatBatchesToolResultImagesIntoTrailingUserMessage ports
// ../pi/packages/ai/test/openai-completions-tool-result-images.test.ts
// ("batches tool-result images after consecutive tool results").
//
// Source under test: packages/ai/providers/openai_chat.go:296-309. When an
// assistant turn emits multiple tool calls and the consecutive tool results
// carry images, the converter emits one "tool" message per result followed by
// a SINGLE trailing synthetic user message that batches every image_url part.
func TestOpenAIChatBatchesToolResultImagesIntoTrailingUserMessage(t *testing.T) {
	imageBlock := aiproviders.OpenAIChatMessageBlock{Type: "image", MimeType: "image/png", Data: "ZmFrZQ=="}
	toolResult := func(id string) aiproviders.OpenAIChatMessage {
		return aiproviders.OpenAIChatMessage{
			Role:       "toolResult",
			ToolCallID: id,
			ToolName:   "read",
			Blocks: []aiproviders.OpenAIChatMessageBlock{
				{Type: "text", Text: "Read image file [image/png]"},
				imageBlock,
			},
		}
	}

	out := aiproviders.OpenAIChatMessages(aiproviders.OpenAIChatRequestOptions{
		SupportsImages: true,
		Messages: []aiproviders.OpenAIChatMessage{
			{Role: "user", Text: "Read the images"},
			{Role: "assistant", Blocks: []aiproviders.OpenAIChatMessageBlock{
				{Type: "toolCall", ID: "tool-1", Name: "read", Arguments: json.RawMessage(`{"path":"img-1.png"}`)},
				{Type: "toolCall", ID: "tool-2", Name: "read", Arguments: json.RawMessage(`{"path":"img-2.png"}`)},
			}},
			toolResult("tool-1"),
			toolResult("tool-2"),
		},
	})

	roles := make([]string, len(out))
	for i, m := range out {
		role, _ := m["role"].(string)
		roles[i] = role
	}
	want := []string{"user", "assistant", "tool", "tool", "user"}
	if len(roles) != len(want) {
		t.Fatalf("role count=%d (%v), want %v", len(roles), roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles=%v, want %v", roles, want)
		}
	}

	trailing := out[len(out)-1]
	if trailing["role"] != "user" {
		t.Fatalf("trailing role=%#v, want user", trailing["role"])
	}
	content, ok := trailing["content"].([]map[string]any)
	if !ok {
		t.Fatalf("trailing content not an array: %#v", trailing["content"])
	}
	imageParts := 0
	for _, part := range content {
		if part["type"] == "image_url" {
			imageParts++
		}
	}
	if imageParts != 2 {
		t.Fatalf("synthetic user message batched %d image_url parts, want 2: %#v", imageParts, content)
	}
}
