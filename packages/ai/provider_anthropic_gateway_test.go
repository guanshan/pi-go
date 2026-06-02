package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// When an Anthropic model routes through Cloudflare AI Gateway the token must
// be sent as cf-aig-authorization and the SDK must not set x-api-key /
// Authorization. Mirrors anthropic.ts:802-819.
func TestAnthropicCloudflareGatewayAuthHeaders(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")

	var capturedPath string
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		headers = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("cloudflare-ai-gateway", "cf-key")
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "cloudflare-ai-gateway",
			ID:       "anthropic/claude-test",
			API:      "anthropic-messages",
			BaseURL:  server.URL + "/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic",
		},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturedPath != "/v1/account-id/gateway-id/anthropic/v1/messages" {
		t.Fatalf("path=%q", capturedPath)
	}
	if got := headers.Get("cf-aig-authorization"); got != "Bearer cf-key" {
		t.Fatalf("cf-aig-authorization=%q", got)
	}
	if got := headers.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key should be unset, got %q", got)
	}
	if got := headers.Get("Authorization"); got != "" {
		t.Fatalf("Authorization should be unset, got %q", got)
	}
	if MessageText(response.Message) != "ok" {
		t.Fatalf("message=%#v", response.Message)
	}
}

// A caller-supplied upstream Authorization header (BYOK) must be preserved even
// when routing through the gateway.
func TestAnthropicCloudflareGatewayPreservesByokAuthorization(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")

	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("cloudflare-ai-gateway", "cf-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "cloudflare-ai-gateway",
			ID:       "anthropic/claude-test",
			API:      "anthropic-messages",
			BaseURL:  server.URL + "/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic",
		},
		Headers:  map[string]string{"Authorization": "Bearer byok-upstream"},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headers.Get("cf-aig-authorization"); got != "Bearer cf-key" {
		t.Fatalf("cf-aig-authorization=%q", got)
	}
	if got := headers.Get("Authorization"); got != "Bearer byok-upstream" {
		t.Fatalf("BYOK Authorization should be preserved, got %q", got)
	}
	if got := headers.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key should be unset, got %q", got)
	}
}
