package ai

import "testing"

func TestIsRetryableProviderError(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		// --- Retryable (transient whitelist) ---
		{"overloaded", "Anthropic API error: overloaded_error", true},
		{"provider returned error", "provider returned error", true},
		{"provider-returned-error dot variant", "provider.returned.error", true},
		{"rate limit", "rate limit exceeded", true},
		{"rate-limit dot variant", "you have hit a rate.limit", true},
		{"too many requests", "Too Many Requests", true},
		{"http 429", "HTTP 429 from upstream", true},
		{"http 500", "received status 500 from server", true},
		{"http 502", "502 Bad Gateway", true},
		{"http 503", "503 Service Unavailable", true},
		{"http 504", "504 Gateway Timeout", true},
		{"service unavailable", "service unavailable", true},
		{"server error", "internal server error", true},
		{"internal error", "internal error occurred", true},
		{"network error", "network error while connecting", true},
		{"connection error", "connection error", true},
		{"connection refused", "connection refused", true},
		{"connection lost", "Network connection lost.", true},
		{"websocket closed", "websocket closed unexpectedly", true},
		{"websocket error", "websocket error during stream", true},
		{"other side closed", "other side closed", true},
		{"fetch failed", "fetch failed", true},
		{"upstream connect", "upstream connect error or disconnect", true},
		{"reset before headers", "stream was reset before headers", true},
		{"socket hang up", "socket hang up", true},
		{"ended without", "stream ended without a stop reason", true},
		{"stream ended before message_stop", "stream ended before message_stop", true},
		{"http2 no response", "http2 request did not get a response", true},
		{"timed out", "request timed out", true},
		{"timeout", "operation timeout", true},
		{"terminated", "the connection was terminated", true},
		{"retry delay", "retry delay exceeded", true},
		{"case insensitive", "OVERLOADED", true},

		// --- Non-retryable provider-limit blacklist (checked first) ---
		{"insufficient_quota", "Error code: 429 - insufficient_quota", false},
		{"quota exceeded", "quota exceeded for this month", false},
		{"billing", "billing issue: please update your payment method", false},
		{"monthly usage limit reached", "Monthly usage limit reached", false},
		{"available balance", "your available balance is too low", false},
		{"out of budget", "you are out of budget", false},
		{"go usage limit error", "GoUsageLimitError: limit reached", false},
		{"free usage limit error", "FreeUsageLimitError", false},
		// Blacklist wins even when a retryable token also appears (429 + quota).
		{"quota with 429 not retryable", "429 insufficient_quota", false},

		// --- Neither list: not retryable ---
		{"empty", "", false},
		{"unknown error", "something unexpected happened", false},
		{"validation error", "invalid request: missing field", false},
		{"context overflow not handled here", "prompt is too long for the context window", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryableProviderError(tc.msg); got != tc.want {
				t.Fatalf("IsRetryableProviderError(%q)=%v want %v", tc.msg, got, tc.want)
			}
		})
	}
}
