package ai

import "regexp"

// nonRetryableProviderLimitPattern matches provider-side limit/billing errors
// that must NOT be retried. This is checked first (a blacklist).
//
// Mirrors `_isNonRetryableProviderLimitError` in the TypeScript original:
// packages/coding-agent/src/core/agent-session.ts (~line 2450).
var nonRetryableProviderLimitPattern = regexp.MustCompile(
	`(?i)GoUsageLimitError|FreeUsageLimitError|Monthly usage limit reached|available balance|insufficient_quota|out of budget|quota exceeded|billing`,
)

// retryableProviderPattern matches transient/recoverable provider and network
// errors that SHOULD be retried (overloaded, rate limit, 5xx, connection
// errors including "connection lost", timeouts, etc.). This is the whitelist.
//
// Mirrors `_isRetryableError` in the TypeScript original:
// packages/coding-agent/src/core/agent-session.ts (~line 2469).
var retryableProviderPattern = regexp.MustCompile(
	`(?i)overloaded|provider.?returned.?error|rate.?limit|too many requests|429|500|502|503|504|service.?unavailable|server.?error|internal.?error|network.?error|connection.?error|connection.?refused|connection.?lost|websocket.?closed|websocket.?error|other side closed|fetch failed|upstream.?connect|reset before headers|socket hang up|ended without|stream ended before message_stop|http2 request did not get a response|timed? out|timeout|terminated|retry delay`,
)

// IsRetryableProviderError reports whether a provider error message describes a
// transient failure that is safe to retry.
//
// Classification mirrors the TypeScript original
// (packages/coding-agent/src/core/agent-session.ts):
//   - The non-retryable provider-limit blacklist (quota/billing/usage limits) is
//     checked first; matches return false.
//   - Otherwise, only messages matching the transient-retryable whitelist
//     (overloaded, 429/5xx, connection lost, timeouts, ...) return true.
//   - Everything else returns false.
//
// Matching is case-insensitive, consistent with TS's /i regexes. Context
// overflow is intentionally not handled here (the caller routes it to
// compaction via IsContextOverflow before reaching retry classification).
func IsRetryableProviderError(msg string) bool {
	if msg == "" {
		return false
	}
	if nonRetryableProviderLimitPattern.MatchString(msg) {
		return false
	}
	return retryableProviderPattern.MatchString(msg)
}
