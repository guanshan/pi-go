package providers

import "testing"

// Ports openai-completions-empty-tools.test.ts:130-181. For the
// cloudflare-ai-gateway chat path the gateway key travels in
// cf-aig-authorization and the upstream Authorization header is dropped when no
// BYOK header is supplied.
func TestBuildOpenAIChatRequestCloudflareGatewayDefaultAuth(t *testing.T) {
	prepared := BuildOpenAIChatRequest("cf-token", OpenAIChatRequestOptions{
		ModelID:  "workers-ai/@cf/moonshotai/kimi-k2.6",
		Provider: "cloudflare-ai-gateway",
		Messages: []OpenAIChatMessage{{Role: "user", Text: "hi"}},
	})
	if prepared.BearerAuth {
		t.Fatalf("cloudflare gateway must not use bearer auth")
	}
	if got := prepared.Headers["cf-aig-authorization"]; got != "Bearer cf-token" {
		t.Fatalf("cf-aig-authorization=%q", got)
	}
	if _, ok := prepared.Headers["Authorization"]; ok {
		t.Fatalf("Authorization must be unset without BYOK header, headers=%#v", prepared.Headers)
	}
}

// With an inline upstream Authorization header (BYOK), it is preserved while the
// gateway key still travels in cf-aig-authorization.
func TestBuildOpenAIChatRequestCloudflareGatewayByokAuth(t *testing.T) {
	prepared := BuildOpenAIChatRequest("cf-token", OpenAIChatRequestOptions{
		ModelID:        "gpt-5.1",
		Provider:       "cloudflare-ai-gateway",
		Messages:       []OpenAIChatMessage{{Role: "user", Text: "hi"}},
		RequestHeaders: map[string]string{"Authorization": "Bearer upstream-token"},
	})
	if prepared.BearerAuth {
		t.Fatalf("cloudflare gateway must not use bearer auth")
	}
	if got := prepared.Headers["Authorization"]; got != "Bearer upstream-token" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := prepared.Headers["cf-aig-authorization"]; got != "Bearer cf-token" {
		t.Fatalf("cf-aig-authorization=%q", got)
	}
}

// Non-cloudflare providers keep the default bearer auth behaviour.
func TestBuildOpenAIChatRequestDefaultBearerAuth(t *testing.T) {
	prepared := BuildOpenAIChatRequest("key", OpenAIChatRequestOptions{
		ModelID:  "gpt-4o",
		Provider: "openai",
		Messages: []OpenAIChatMessage{{Role: "user", Text: "hi"}},
	})
	if !prepared.BearerAuth {
		t.Fatalf("openai must use bearer auth")
	}
	if _, ok := prepared.Headers["cf-aig-authorization"]; ok {
		t.Fatalf("non-cloudflare must not set cf-aig-authorization")
	}
}

// The Responses path mirrors the chat path: cf-aig-authorization carries the
// gateway key and Authorization is unset without a BYOK header.
func TestOpenAIResponsesRequestCloudflareGatewayDefaultAuth(t *testing.T) {
	_, headers, err := OpenAIResponsesRequest(OpenAIResponsesRequestOptions{
		API:      "openai-responses",
		Provider: "cloudflare-ai-gateway",
		ModelID:  "gpt-5.1",
		BaseURL:  "https://gateway.ai.cloudflare.com/v1/a/g/openai",
	}, "cf-token")
	if err != nil {
		t.Fatal(err)
	}
	if got := headers["cf-aig-authorization"]; got != "Bearer cf-token" {
		t.Fatalf("cf-aig-authorization=%q", got)
	}
	if _, ok := headers["Authorization"]; ok {
		t.Fatalf("Authorization must be unset without BYOK header, headers=%#v", headers)
	}
}

func TestOpenAIResponsesRequestCloudflareGatewayByokAuth(t *testing.T) {
	_, headers, err := OpenAIResponsesRequest(OpenAIResponsesRequestOptions{
		API:            "openai-responses",
		Provider:       "cloudflare-ai-gateway",
		ModelID:        "gpt-5.1",
		BaseURL:        "https://gateway.ai.cloudflare.com/v1/a/g/openai",
		RequestHeaders: map[string]string{"Authorization": "Bearer upstream-token"},
	}, "cf-token")
	if err != nil {
		t.Fatal(err)
	}
	if got := headers["Authorization"]; got != "Bearer upstream-token" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := headers["cf-aig-authorization"]; got != "Bearer cf-token" {
		t.Fatalf("cf-aig-authorization=%q", got)
	}
}

// A standard OpenAI Responses request still uses Authorization: Bearer <key>.
func TestOpenAIResponsesRequestDefaultBearerAuth(t *testing.T) {
	_, headers, err := OpenAIResponsesRequest(OpenAIResponsesRequestOptions{
		API:      "openai-responses",
		Provider: "openai",
		ModelID:  "gpt-5.1",
		BaseURL:  "https://api.openai.com/v1",
	}, "key")
	if err != nil {
		t.Fatal(err)
	}
	if got := headers["Authorization"]; got != "Bearer key" {
		t.Fatalf("Authorization=%q", got)
	}
	if _, ok := headers["cf-aig-authorization"]; ok {
		t.Fatalf("non-cloudflare must not set cf-aig-authorization")
	}
}
