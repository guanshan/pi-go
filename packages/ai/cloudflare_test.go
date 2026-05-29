package ai

import (
	"strings"
	"testing"
)

func TestCloudflareCredentialHelpersAndBaseURL(t *testing.T) {
	model := Model{
		Provider: "cloudflare-ai-gateway",
		BaseURL:  CloudflareAIGatewayCompatBaseURL,
	}
	if HasCloudflareWorkersAICredentials() || HasCloudflareAIGatewayCredentials() {
		t.Fatal("cloudflare credentials should be absent by default in this test")
	}
	t.Setenv("CLOUDFLARE_API_KEY", "key")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account")
	if !HasCloudflareWorkersAICredentials() {
		t.Fatal("workers-ai credentials should require api key and account id")
	}
	if HasCloudflareAIGatewayCredentials() {
		t.Fatal("gateway credentials should require gateway id too")
	}
	_, err := ResolveCloudflareBaseURL(model)
	if err == nil || !strings.Contains(err.Error(), "CLOUDFLARE_GATEWAY_ID") {
		t.Fatalf("missing gateway error=%v", err)
	}
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway")
	if !HasCloudflareAIGatewayCredentials() {
		t.Fatal("gateway credentials should pass")
	}
	resolved, err := ResolveCloudflareBaseURL(model)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "https://gateway.ai.cloudflare.com/v1/account/gateway/compat" {
		t.Fatalf("resolved=%q", resolved)
	}
	if !IsCloudflareProvider("cloudflare-workers-ai") || IsCloudflareProvider("openai") {
		t.Fatal("cloudflare provider detection mismatch")
	}
}
