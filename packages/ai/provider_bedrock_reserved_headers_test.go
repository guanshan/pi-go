package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBedrockReservedHeadersSkipped locks the parity fix for P1-4: caller-supplied
// reserved headers (authorization, host, x-amz-*) must NOT be written onto the
// outgoing Bedrock request, because they participate in SigV4 / bearer auth and
// would break signing. Non-reserved custom headers must still be applied.
// Mirrors isReservedHeader + addCustomHeadersMiddleware in amazon-bedrock.ts.
func TestBedrockReservedHeadersSkipped(t *testing.T) {
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")

	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		_, _ = w.Write([]byte(`{"output":{"message":{"content":[{"text":"ok"}]}},"stopReason":"end_turn"}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "amazon-bedrock",
			ID:       "amazon.nova-2-lite-v1:0",
			API:      "bedrock-converse-stream",
			BaseURL:  server.URL,
			// Reserved headers on the model: must be skipped.
			Headers: map[string]string{
				"Authorization": "Bearer caller-should-be-ignored",
				"X-Amz-Custom":  "amz-should-be-ignored",
				"Host":          "evil.example.com",
				"X-Safe-Header": "keep-me",
			},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
		// Reserved headers on the request as well (different casing): must be skipped.
		Headers: map[string]string{
			"authorization": "Bearer req-should-be-ignored",
			"x-amz-target":  "req-amz-ignored",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Under skip-auth there is no legitimate Authorization, and the caller's
	// reserved overrides must not have leaked through.
	if got := captured.Get("Authorization"); got != "" {
		t.Fatalf("Authorization should be empty (reserved header skipped), got %q", got)
	}
	if got := captured.Get("X-Amz-Custom"); got != "" {
		t.Fatalf("X-Amz-Custom should be skipped, got %q", got)
	}
	if got := captured.Get("X-Amz-Target"); got != "" {
		t.Fatalf("x-amz-target should be skipped, got %q", got)
	}
	if got := captured.Get("Host"); got == "evil.example.com" {
		t.Fatalf("Host should not be overridden by caller header, got %q", got)
	}
	// Non-reserved custom header must survive.
	if got := captured.Get("X-Safe-Header"); got != "keep-me" {
		t.Fatalf("X-Safe-Header=%q want keep-me", got)
	}
}
