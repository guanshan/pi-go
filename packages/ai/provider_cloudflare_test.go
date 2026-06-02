package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

func TestCloudflareOpenAICompatibleBaseURLResolution(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")

	var capturedPath string
	var auth string
	var cfAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		auth = r.Header.Get("Authorization")
		cfAuth = r.Header.Get("cf-aig-authorization")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "workers-ai/@cf/test/model" {
			t.Fatalf("payload=%#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("cloudflare-ai-gateway", "cf-key")
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
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
	if capturedPath != "/v1/account-id/gateway-id/compat/chat/completions" {
		t.Fatalf("path=%q", capturedPath)
	}
	// TS (openai-completions.ts:480-486) sends the gateway key only via
	// cf-aig-authorization and leaves the upstream Authorization unset (null)
	// unless a BYOK Authorization header was supplied.
	if auth != "" {
		t.Fatalf("upstream Authorization should be unset, got auth=%q", auth)
	}
	if cfAuth != "Bearer cf-key" {
		t.Fatalf("cf-aig-authorization=%q", cfAuth)
	}
	if MessageText(response.Message) != "ok" {
		t.Fatalf("message=%#v", response.Message)
	}
}

func TestCloudflarePlaceholderErrorsAndAuth(t *testing.T) {
	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("cloudflare-ai-gateway", "cf-key")
	gateway := Model{
		Provider: "cloudflare-ai-gateway",
		ID:       "workers-ai/@cf/test/model",
		API:      "openai-completions",
		BaseURL:  "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat",
	}
	if registry.HasAuth(gateway) {
		t.Fatal("gateway should require account and gateway env vars")
	}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{Model: gateway, Messages: []Message{NewUserMessage("hello", nil)}})
	if err == nil || !strings.Contains(err.Error(), "CLOUDFLARE_ACCOUNT_ID") {
		t.Fatalf("missing account error=%v", err)
	}

	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account-id")
	worker := Model{Provider: "cloudflare-workers-ai", API: "openai-completions", BaseURL: "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"}
	registry.Auth.SetRuntime("cloudflare-workers-ai", "cf-key")
	if !registry.HasAuth(worker) {
		t.Fatal("workers-ai should require only api key and account id")
	}
	if registry.HasAuth(gateway) {
		t.Fatal("gateway should still require gateway id")
	}

	t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway-id")
	if !registry.HasAuth(gateway) {
		t.Fatal("gateway auth should pass with api key, account id, and gateway id")
	}
}

func TestOpenAIChatURLCloudflareCompat(t *testing.T) {
	if got := aiproviders.OpenAIChatURL("https://gateway.ai.cloudflare.com/v1/a/g/compat"); got != "https://gateway.ai.cloudflare.com/v1/a/g/compat/chat/completions" {
		t.Fatalf("compat url=%q", got)
	}
	if got := aiproviders.OpenAIChatURL("https://gateway.ai.cloudflare.com/v1/a/g/openai"); got != "https://gateway.ai.cloudflare.com/v1/a/g/openai/chat/completions" {
		t.Fatalf("openai url=%q", got)
	}
}

func TestOpenAIResponsesURLCloudflareOpenAIPassthrough(t *testing.T) {
	if got := aiproviders.OpenAIResponsesURL("https://gateway.ai.cloudflare.com/v1/a/g/openai"); got != "https://gateway.ai.cloudflare.com/v1/a/g/openai/v1/responses" {
		t.Fatalf("openai responses url=%q", got)
	}
}
