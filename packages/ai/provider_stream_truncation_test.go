package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenAIChatStreamErrorsWhenNoFinishReason locks the parity fix for P1-2 on
// the OpenAI completions path: a stream that delivers content but never sends a
// finish_reason is truncated, so the provider message must end with
// stopReason=error and an errorMessage that the retry whitelist recognizes
// ("ended without"). Mirrors openai-completions.ts:
//
//	if (!hasFinishReason) throw new Error("Stream ended without finish_reason");
func TestOpenAIChatStreamErrorsWhenNoFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Content delta but no finish_reason and no terminal usage chunk: the
		// stream simply ends (connection closes) mid-response.
		_, _ = w.Write([]byte(
			`data: {"id":"chatcmpl_x","model":"gpt-x","choices":[{"delta":{"content":"hel"}}]}` + "\n\n" +
				`data: {"id":"chatcmpl_x","model":"gpt-x","choices":[{"delta":{"content":"lo"}}]}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-x",
			API:      "openai-completions",
			// Non-openai.com host + custom route forces the manual HTTP stream path.
			BaseURL: server.URL + "/compat-route",
			Input:   []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})

	sawError := false
	for event := range stream.Events() {
		if event.Type == "error" {
			sawError = true
		}
	}
	message := stream.Result()
	if message.StopReason != "error" {
		t.Fatalf("stopReason=%q want error; msg=%#v", message.StopReason, message)
	}
	if !strings.Contains(message.ErrorMessage, "finish_reason") {
		t.Fatalf("errorMessage=%q want to mention finish_reason", message.ErrorMessage)
	}
	if !sawError {
		t.Fatal("expected an error event to be emitted")
	}
	if !IsRetryableProviderError(message.ErrorMessage) {
		t.Fatalf("error %q should be retryable", message.ErrorMessage)
	}
}

// TestOpenAIChatStreamSucceedsWithFinishReason is the positive control: an
// otherwise-identical stream that DOES send a finish_reason finalizes normally.
func TestOpenAIChatStreamSucceedsWithFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"id":"chatcmpl_x","model":"gpt-x","choices":[{"delta":{"content":"hel"}}]}` + "\n\n" +
				`data: {"id":"chatcmpl_x","model":"gpt-x","choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model:    Model{Provider: "openai", ID: "gpt-x", API: "openai-completions", BaseURL: server.URL + "/compat-route", Input: []string{"text"}},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	for range stream.Events() {
	}
	message := stream.Result()
	if message.StopReason == "error" {
		t.Fatalf("unexpected error: %q", message.ErrorMessage)
	}
	if got := MessageText(message); got != "hello" {
		t.Fatalf("text=%q", got)
	}
}

// TestOpenAIChatStreamAppendsOpenRouterErrorMetadata locks the parity fix for
// P2-4: when an OpenRouter SSE error carries error.metadata.raw, the extra
// diagnostic text is appended to the errorMessage. Mirrors openai-completions.ts.
func TestOpenAIChatStreamAppendsOpenRouterErrorMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"error":{"message":"upstream failed","metadata":{"raw":"provider said: quota burst"}}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openrouter", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model:    Model{Provider: "openrouter", ID: "x", API: "openai-completions", BaseURL: server.URL + "/compat", Input: []string{"text"}},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	for range stream.Events() {
	}
	message := stream.Result()
	if message.StopReason != "error" {
		t.Fatalf("stopReason=%q want error", message.StopReason)
	}
	if !strings.Contains(message.ErrorMessage, "upstream failed") {
		t.Fatalf("errorMessage=%q missing base message", message.ErrorMessage)
	}
	if !strings.Contains(message.ErrorMessage, "provider said: quota burst") {
		t.Fatalf("errorMessage=%q missing appended metadata.raw", message.ErrorMessage)
	}
}

// TestAnthropicChatStreamErrorsBeforeMessageStop locks the parity fix for P1-2 on
// the Anthropic path: a stream that emits message_start (and content) but ends
// before message_stop is truncated. Mirrors anthropic.ts iterateAnthropicEvents:
//
//	if (sawMessageStart && !sawMessageEnd)
//	  throw new Error("Anthropic stream ended before message_stop");
func TestAnthropicChatStreamErrorsBeforeMessageStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// message_start + content, then the connection closes (no message_stop).
		_, _ = w.Write([]byte(
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
				"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model:    Model{Provider: "anthropic", ID: "claude-test", API: "anthropic-messages", BaseURL: server.URL},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	sawError := false
	for event := range stream.Events() {
		if event.Type == "error" {
			sawError = true
		}
	}
	message := stream.Result()
	if message.StopReason != "error" {
		t.Fatalf("stopReason=%q want error; msg=%#v", message.StopReason, message)
	}
	if !strings.Contains(message.ErrorMessage, "message_stop") {
		t.Fatalf("errorMessage=%q want to mention message_stop", message.ErrorMessage)
	}
	if !sawError {
		t.Fatal("expected an error event to be emitted")
	}
	if !IsRetryableProviderError(message.ErrorMessage) {
		t.Fatalf("error %q should be retryable", message.ErrorMessage)
	}
}
