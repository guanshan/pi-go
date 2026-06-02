package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The non-streaming Anthropic response carries message.id, which must surface
// as AssistantMessage.ResponseID. Mirrors the Anthropic case of responseid.test.ts.
func TestAnthropicResponseIDNonStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_nonstream_1","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:    Model{Provider: "anthropic", ID: "claude-test", API: "anthropic-messages", BaseURL: server.URL},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.ResponseID != "msg_nonstream_1" {
		t.Fatalf("responseId=%q", response.Message.ResponseID)
	}
}

// The streaming message_start event carries message.id, which must surface on
// both the partial and the final AssistantMessage. Mirrors anthropic.ts:528-530.
func TestAnthropicResponseIDStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"msg_stream_42","usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
				"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n" +
				"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}` + "\n\n" +
				"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}` + "\n\n" +
				"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model:    Model{Provider: "anthropic", ID: "claude-test", API: "anthropic-messages", BaseURL: server.URL},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	sawPartialID := false
	for event := range stream.Events() {
		if event.Type == "start" && event.Partial.ResponseID == "msg_stream_42" {
			sawPartialID = true
		}
	}
	message := stream.Result()
	if !sawPartialID {
		t.Fatal("expected responseId on start partial")
	}
	if message.ResponseID != "msg_stream_42" {
		t.Fatalf("final responseId=%q", message.ResponseID)
	}
}
