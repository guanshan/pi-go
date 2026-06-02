package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenAIChatToolResultIncludesNameWhenRequired locks the parity fix for P2-2:
// a model with compat.requiresToolResultName must emit the tool `name` on tool
// (tool_result) messages, like the Mistral/Google paths. Mirrors
// openai-completions.ts: if (requiresToolResultName && toolName) msg.name = toolName.
func TestOpenAIChatToolResultIncludesNameWhenRequired(t *testing.T) {
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
	registry.Auth.SetRuntime("custom", "test-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "custom",
			ID:       "x",
			API:      "openai-completions",
			BaseURL:  server.URL,
			Input:    []string{"text"},
			Compat:   OpenAICompat{RequiresToolResultName: boolPtr(true)},
		},
		Messages: []Message{
			NewUserMessage("look it up", nil),
			NewAssistantMessage("openai-completions", "custom", "x", []ContentBlock{
				{Type: "toolCall", ID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
			}, Usage{}, "toolUse"),
			NewToolResultMessage("call_1", "lookup", TextBlocks("the result"), nil, false),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := captured["messages"].([]any)
	var toolMsg map[string]any
	for _, m := range messages {
		mm := m.(map[string]any)
		if mm["role"] == "tool" {
			toolMsg = mm
			break
		}
	}
	if toolMsg == nil {
		t.Fatalf("no tool message found in %#v", messages)
	}
	if toolMsg["name"] != "lookup" {
		t.Fatalf("tool message name=%v want lookup (requiresToolResultName)", toolMsg["name"])
	}
	if toolMsg["tool_call_id"] != "call_1" {
		t.Fatalf("tool_call_id=%v want call_1", toolMsg["tool_call_id"])
	}
}
