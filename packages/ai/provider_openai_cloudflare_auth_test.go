package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// End-to-end wire check for the cloudflare-ai-gateway chat path: the gateway key
// must travel in cf-aig-authorization and the upstream Authorization header must
// be absent without a BYOK header. Mirrors openai-completions-empty-tools.test.ts.
func TestCloudflareGatewayChatAuthHeadersWire(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")

	var auth, cfAig string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		cfAig = r.Header.Get("cf-aig-authorization")
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("cloudflare-ai-gateway", "cf-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "cloudflare-ai-gateway",
			ID:       "workers-ai/@cf/test/model",
			API:      "openai-completions",
			BaseURL:  server.URL + "/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat",
		},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Fatalf("Authorization must be empty without BYOK header, got %q", auth)
	}
	if cfAig != "Bearer cf-key" {
		t.Fatalf("cf-aig-authorization=%q", cfAig)
	}
}

// With a BYOK upstream Authorization header, it is forwarded while the gateway
// key still travels in cf-aig-authorization.
func TestCloudflareGatewayChatByokAuthHeadersWire(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")

	var auth, cfAig string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		cfAig = r.Header.Get("cf-aig-authorization")
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("cloudflare-ai-gateway", "cf-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "cloudflare-ai-gateway",
			ID:       "workers-ai/@cf/test/model",
			API:      "openai-completions",
			BaseURL:  server.URL + "/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat",
		},
		Headers:  map[string]string{"Authorization": "Bearer upstream-token"},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer upstream-token" {
		t.Fatalf("Authorization=%q", auth)
	}
	if cfAig != "Bearer cf-key" {
		t.Fatalf("cf-aig-authorization=%q", cfAig)
	}
}
