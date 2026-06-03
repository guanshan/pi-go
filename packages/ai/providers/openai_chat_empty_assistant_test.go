package providers

import (
	"encoding/json"
	"testing"
)

// Ports openai-completions.ts:816 — the default assistant content is
// `compat.requiresAssistantAfterToolResult ? "" : null`. When a tool-call-only
// assistant message (no text) is kept, its content must serialize to JSON null
// when RequiresAssistantAfterToolResult is false, and to "" when true.
func TestOpenAIChatAssistantDefaultContentNullVsEmpty(t *testing.T) {
	blocks := []OpenAIChatMessageBlock{
		{Type: "toolCall", ID: "call_1", Name: "lookup", Arguments: []byte(`{"q":"pi"}`)},
	}

	// !RequiresAssistantAfterToolResult => content should be JSON null.
	out := OpenAIChatMessages(OpenAIChatRequestOptions{
		Messages: []OpenAIChatMessage{{Role: "assistant", Blocks: blocks}},
	})
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d: %#v", len(out), out)
	}
	if got, ok := out[0]["content"]; !ok || got != nil {
		t.Fatalf("expected nil content, got %#v (present=%v)", got, ok)
	}
	encoded, err := json.Marshal(out[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded["content"]) != "null" {
		t.Fatalf("expected content serialized as null, got %s", decoded["content"])
	}

	// RequiresAssistantAfterToolResult => content should be the empty string.
	out = OpenAIChatMessages(OpenAIChatRequestOptions{
		RequiresAssistantAfterToolResult: true,
		Messages:                         []OpenAIChatMessage{{Role: "assistant", Blocks: blocks}},
	})
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d: %#v", len(out), out)
	}
	if got := out[0]["content"]; got != "" {
		t.Fatalf("expected empty-string content, got %#v", got)
	}
}

// Ports empty.test.ts (user -> empty assistant -> user): an assistant message
// with no content and no tool_calls must be skipped from the converted payload.
func TestOpenAIChatMessagesSkipsEmptyAssistant(t *testing.T) {
	options := OpenAIChatRequestOptions{
		Messages: []OpenAIChatMessage{
			{Role: "user", Text: "Hello, how are you?"},
			{Role: "assistant", Blocks: nil},
			{Role: "user", Text: "Please respond this time."},
		},
	}
	out := OpenAIChatMessages(options)
	if len(out) != 2 {
		t.Fatalf("expected empty assistant skipped, got %d messages: %#v", len(out), out)
	}
	if out[0]["role"] != "user" || out[1]["role"] != "user" {
		t.Fatalf("roles=%#v", out)
	}
	if out[0]["content"] != "Hello, how are you?" || out[1]["content"] != "Please respond this time." {
		t.Fatalf("content=%#v", out)
	}
}

// An assistant message that carries tool_calls but no text content must NOT be
// skipped.
func TestOpenAIChatMessagesKeepsToolCallAssistant(t *testing.T) {
	options := OpenAIChatRequestOptions{
		Messages: []OpenAIChatMessage{
			{Role: "user", Text: "look it up"},
			{Role: "assistant", Blocks: []OpenAIChatMessageBlock{
				{Type: "toolCall", ID: "call_1", Name: "lookup", Arguments: []byte(`{"q":"pi"}`)},
			}},
		},
	}
	out := OpenAIChatMessages(options)
	if len(out) != 2 {
		t.Fatalf("expected tool-call assistant kept, got %d messages: %#v", len(out), out)
	}
	assistant := out[1]
	if assistant["role"] != "assistant" {
		t.Fatalf("assistant role=%#v", assistant)
	}
	if _, ok := assistant["tool_calls"]; !ok {
		t.Fatalf("tool_calls missing: %#v", assistant)
	}
}

// An assistant message with actual text content must NOT be skipped.
func TestOpenAIChatMessagesKeepsTextAssistant(t *testing.T) {
	options := OpenAIChatRequestOptions{
		Messages: []OpenAIChatMessage{
			{Role: "assistant", Blocks: []OpenAIChatMessageBlock{{Type: "text", Text: "here you go"}}},
		},
	}
	out := OpenAIChatMessages(options)
	if len(out) != 1 || out[0]["content"] != "here you go" {
		t.Fatalf("expected text assistant kept, got %#v", out)
	}
}
