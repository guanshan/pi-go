package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGoogleChatStreamCapturesResponseID verifies the stream keeps the first
// non-empty responseId from the chunks and reports it on the final message.
func TestGoogleChatStreamCapturesResponseID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data:{"responseId":"resp-google-1","candidates":[{"content":{"role":"model","parts":[{"text":"hel"}]}}]}` + "\n\n" +
				`data:{"responseId":"resp-google-2","candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}]}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("google", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "google",
			ID:       "gemini-test",
			API:      "google-generative-ai",
			BaseURL:  server.URL,
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	for range stream.Events() {
	}
	message := stream.Result()
	if message.ResponseID != "resp-google-1" {
		t.Fatalf("responseId=%q", message.ResponseID)
	}
}

// TestGoogleVertexChatStreamCapturesResponseID verifies the Vertex stream path
// also captures the first chunk's responseId.
func TestGoogleVertexChatStreamCapturesResponseID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data:{"responseId":"resp-vertex-1","candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("google-vertex", "vertex-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "google-vertex",
			ID:       "gemini-3-pro",
			API:      "google-vertex",
			BaseURL:  server.URL,
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	for range stream.Events() {
	}
	message := stream.Result()
	if message.ResponseID != "resp-vertex-1" {
		t.Fatalf("responseId=%q", message.ResponseID)
	}
}

// TestGoogleChatStreamDeduplicatesFunctionCallIDs verifies that duplicate
// provider-supplied function call ids and empty ids both get unique ids.
func TestGoogleChatStreamDeduplicatesFunctionCallIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"dup","name":"lookup","args":{"q":"a"}}}]}}]}` + "\n\n" +
				`data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"dup","name":"lookup","args":{"q":"b"}}}]}}]}` + "\n\n" +
				`data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"q":"c"}}}]},"finishReason":"STOP"}]}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("google", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "google",
			ID:       "gemini-test",
			API:      "google-generative-ai",
			BaseURL:  server.URL,
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
		Tools:    ToolSet{"lookup": cacheTestToolDef("lookup")},
	})
	for range stream.Events() {
	}
	message := stream.Result()

	blocks := MessageBlocks(message)
	var ids []string
	for _, b := range blocks {
		if b.Type == "toolCall" {
			ids = append(ids, b.ID)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 tool calls, got %d: %#v", len(ids), blocks)
	}
	// First duplicate keeps the provider id; the rest must be distinct.
	if ids[0] != "dup" {
		t.Fatalf("first tool call id=%q", ids[0])
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Fatalf("empty tool call id in %#v", ids)
		}
		if seen[id] {
			t.Fatalf("duplicate tool call id %q in %#v", id, ids)
		}
		seen[id] = true
	}
	// The second chunk reuses "dup", so its id must be regenerated.
	if ids[1] == "dup" {
		t.Fatalf("second tool call id should be regenerated, got %q", ids[1])
	}
}

// TestVertexEnvKeysIgnoreGeminiKeys verifies the Gemini env keys configure only
// the "google" provider, never "google-vertex".
func TestVertexEnvKeysIgnoreGeminiKeys(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "gemini-only")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	if !registry.HasAuth(Model{Provider: "google", API: "google-generative-ai"}) {
		t.Fatal("GEMINI_API_KEY should authenticate the google provider")
	}
	if registry.HasAuth(Model{Provider: "google-vertex", API: "google-vertex"}) {
		t.Fatal("GEMINI_API_KEY must not authenticate google-vertex")
	}
}

// TestVertexEnvKeysAcceptCloudApiKey verifies GOOGLE_CLOUD_API_KEY authenticates
// google-vertex.
func TestVertexEnvKeysAcceptCloudApiKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_CLOUD_API_KEY", "cloud-key")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	if !registry.HasAuth(Model{Provider: "google-vertex", API: "google-vertex"}) {
		t.Fatal("GOOGLE_CLOUD_API_KEY should authenticate google-vertex")
	}
}
