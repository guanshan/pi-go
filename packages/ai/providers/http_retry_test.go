package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// http_retry_test.go provides the previously-missing direct coverage for the
// retry logic in http.go (lines ~104-259): retryable-status classification,
// Retry-After / Retry-After-Ms header parsing, the exponential backoff formula
// 100*2^min(attempt,5) ms, the maxRetryDelayMs cap, and context cancellation
// taking priority over a pending retry.
//
// Mirrors the intent of the TS retry suite
// (pi/packages/ai/test/openai-completions-retry.test.ts and the retry helpers
// in pi/packages/ai/src/providers/openai-codex-responses.ts:122-152,338-348),
// while asserting against this Go port's exact behavior. Timing is made
// deterministic by asserting the computed delay values rather than sleeping,
// and by using a fake RoundTripper for the full-chain tests.

// roundTripFunc is a minimal fake http.RoundTripper so tests never hit the
// network.
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestIsRetryableStatus(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{http.StatusOK, false},                 // 200
		{http.StatusBadRequest, false},         // 400
		{http.StatusRequestTimeout, false},     // 408 - not retried at HTTP layer
		{http.StatusTooManyRequests, true},     // 429
		{http.StatusInternalServerError, true}, // 500
		{http.StatusBadGateway, true},          // 502
		{http.StatusServiceUnavailable, true},  // 503
		{http.StatusGatewayTimeout, true},      // 504
		{499, false},                           // just below the 5xx band
		{599, true},                            // top of the 5xx band
	}
	for _, tc := range cases {
		if got := isRetryableStatus(tc.status); got != tc.want {
			t.Errorf("isRetryableStatus(%d) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestRetryDelayExponentialBackoff(t *testing.T) {
	// 100*2^min(attempt,5) ms; clamps the exponent at 5 (3200ms) for attempt>=5.
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{4, 1600 * time.Millisecond},
		{5, 3200 * time.Millisecond},
		{6, 3200 * time.Millisecond},  // clamped
		{20, 3200 * time.Millisecond}, // clamped
	}
	for _, tc := range cases {
		if got := retryDelay(nil, tc.attempt, RequestOptions{}); got != tc.want {
			t.Errorf("retryDelay(attempt=%d) = %s, want %s", tc.attempt, got, tc.want)
		}
	}
}

func TestRetryDelayUnderCapPasses(t *testing.T) {
	// attempt 0 -> 100ms, which is under a 1000ms cap, so it is returned as-is.
	got := retryDelay(nil, 0, RequestOptions{MaxRetryDelayMs: 1000})
	if got != 100*time.Millisecond {
		t.Fatalf("retryDelay with cap = %s, want 100ms", got)
	}
}

func TestRetryDelayOverCapFallsBackToUncapped(t *testing.T) {
	// retryDelay swallows the cap error and returns the uncapped value.
	// attempt 5 -> 3200ms which exceeds a 1000ms cap.
	got := retryDelay(nil, 5, RequestOptions{MaxRetryDelayMs: 1000})
	if got != 3200*time.Millisecond {
		t.Fatalf("retryDelay over cap = %s, want 3200ms (uncapped fallback)", got)
	}
}

func TestCapRetryDelay(t *testing.T) {
	t.Run("no cap configured returns delay unchanged", func(t *testing.T) {
		got, err := capRetryDelay(5*time.Second, RequestOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 5*time.Second {
			t.Fatalf("got %s, want 5s", got)
		}
	})

	t.Run("negative delay is clamped to zero", func(t *testing.T) {
		got, err := capRetryDelay(-3*time.Second, RequestOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 0 {
			t.Fatalf("got %s, want 0", got)
		}
	})

	t.Run("delay at the cap is allowed", func(t *testing.T) {
		got, err := capRetryDelay(1000*time.Millisecond, RequestOptions{MaxRetryDelayMs: 1000})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != time.Second {
			t.Fatalf("got %s, want 1s", got)
		}
	})

	t.Run("delay over the cap returns an error", func(t *testing.T) {
		_, err := capRetryDelay(2*time.Second, RequestOptions{MaxRetryDelayMs: 1000})
		if err == nil {
			t.Fatalf("expected error for delay exceeding cap")
		}
		if !strings.Contains(err.Error(), "maxRetryDelayMs") {
			t.Fatalf("error %q should mention maxRetryDelayMs", err.Error())
		}
	})
}

func TestResponseRetryDelayHeaderParsing(t *testing.T) {
	t.Run("Retry-After-Ms takes priority", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After-Ms", "250")
		h.Set("Retry-After", "5") // would be 5s; must be ignored in favor of -Ms
		resp := newResponse(http.StatusTooManyRequests, "", h)
		got, err := responseRetryDelay(resp, 0, RequestOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 250*time.Millisecond {
			t.Fatalf("got %s, want 250ms", got)
		}
	})

	t.Run("Retry-After seconds", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After", "2")
		resp := newResponse(http.StatusServiceUnavailable, "", h)
		got, err := responseRetryDelay(resp, 0, RequestOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 2*time.Second {
			t.Fatalf("got %s, want 2s", got)
		}
	})

	t.Run("Retry-After fractional seconds", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After", "1.5")
		resp := newResponse(http.StatusServiceUnavailable, "", h)
		got, err := responseRetryDelay(resp, 0, RequestOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 1500*time.Millisecond {
			t.Fatalf("got %s, want 1500ms", got)
		}
	})

	t.Run("Retry-After HTTP-date", func(t *testing.T) {
		h := http.Header{}
		future := time.Now().Add(3 * time.Second).UTC()
		h.Set("Retry-After", future.Format(http.TimeFormat))
		resp := newResponse(http.StatusServiceUnavailable, "", h)
		got, err := responseRetryDelay(resp, 0, RequestOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// HTTP-date has second granularity and time.Until shrinks as the test
		// runs, so allow a generous lower bound and a ceiling at the header value.
		if got <= 0 || got > 3*time.Second {
			t.Fatalf("got %s, want (0, 3s]", got)
		}
	})

	t.Run("no headers falls back to exponential backoff", func(t *testing.T) {
		resp := newResponse(http.StatusServiceUnavailable, "", nil)
		got, err := responseRetryDelay(resp, 2, RequestOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 400*time.Millisecond { // 100*2^2
			t.Fatalf("got %s, want 400ms (exponential fallback)", got)
		}
	})

	t.Run("malformed Retry-After-Ms falls through to Retry-After", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After-Ms", "not-a-number")
		h.Set("Retry-After", "2")
		resp := newResponse(http.StatusTooManyRequests, "", h)
		got, err := responseRetryDelay(resp, 0, RequestOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 2*time.Second {
			t.Fatalf("got %s, want 2s", got)
		}
	})

	t.Run("nil response uses exponential backoff", func(t *testing.T) {
		got, err := responseRetryDelay(nil, 1, RequestOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 200*time.Millisecond { // 100*2^1
			t.Fatalf("got %s, want 200ms", got)
		}
	})

	t.Run("header delay over cap returns an error", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After-Ms", "5000")
		resp := newResponse(http.StatusTooManyRequests, "", h)
		_, err := responseRetryDelay(resp, 0, RequestOptions{MaxRetryDelayMs: 1000})
		if err == nil {
			t.Fatalf("expected cap error for 5000ms header against 1000ms cap")
		}
	})
}

func TestWaitForRetryReturnsImmediatelyForNonPositiveDelay(t *testing.T) {
	// delay <= 0 must not block; it returns ctx.Err() (nil for a live ctx).
	start := time.Now()
	if err := waitForRetry(context.Background(), 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("waitForRetry(0) blocked for %s", elapsed)
	}
}

func TestWaitForRetryCancelledContextTakesPriority(t *testing.T) {
	// A context already cancelled must short-circuit a multi-second pending retry
	// rather than sleeping it out.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := waitForRetry(ctx, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("waitForRetry did not honor cancellation promptly: %s", elapsed)
	}
}

func TestWaitForRetryCancellationMidWait(t *testing.T) {
	// Cancelling while a retry delay is pending must return promptly.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := waitForRetry(ctx, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waitForRetry blocked through cancellation: %s", elapsed)
	}
}

func TestDoJSONURLWithClientRetriesThenSucceeds(t *testing.T) {
	// Full chain: 503 -> retry -> 200. Retry-After-Ms is tiny to keep the test
	// fast while still exercising the real backoff/wait code path.
	var attempts int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			h := http.Header{}
			h.Set("Retry-After-Ms", "1")
			return newResponse(http.StatusServiceUnavailable, "overloaded", h), nil
		}
		return newResponse(http.StatusOK, `{"ok":true}`, nil), nil
	})
	client := &http.Client{Transport: transport}

	raw, err := DoJSONURLWithClient(context.Background(), "https://example.test/v1", "ua", nil, map[string]any{"x": 1}, client, RequestOptions{MaxRetries: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("body = %q, want {\"ok\":true}", raw)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestDoJSONURLWithClientExhaustsRetriesReturnsError(t *testing.T) {
	// Every attempt is 503; after MaxRetries the final 503 is surfaced as an
	// HTTP error rather than retried again.
	var attempts int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		h := http.Header{}
		h.Set("Retry-After-Ms", "1")
		return newResponse(http.StatusServiceUnavailable, "still down", h), nil
	})
	client := &http.Client{Transport: transport}

	_, err := DoJSONURLWithClient(context.Background(), "https://example.test/v1", "ua", nil, map[string]any{}, client, RequestOptions{MaxRetries: 1})
	if err == nil {
		t.Fatalf("expected error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("error %q should mention status 503", err.Error())
	}
	// MaxRetries=1 means: initial attempt (0) retried once, then attempt 1 is final.
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestDoJSONURLWithClientNoRetryOnNonRetryableStatus(t *testing.T) {
	// A 400 is not retryable: a single attempt, error returned immediately.
	var attempts int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		return newResponse(http.StatusBadRequest, "bad", nil), nil
	})
	client := &http.Client{Transport: transport}

	_, err := DoJSONURLWithClient(context.Background(), "https://example.test/v1", "ua", nil, map[string]any{}, client, RequestOptions{MaxRetries: 3})
	if err == nil {
		t.Fatalf("expected error for 400")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on 400)", got)
	}
}

func TestDoJSONURLWithClientCapErrorAbortsRetry(t *testing.T) {
	// A retryable 503 whose Retry-After exceeds maxRetryDelayMs must abort the
	// retry loop with the cap error instead of waiting.
	var attempts int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		h := http.Header{}
		h.Set("Retry-After-Ms", "5000")
		return newResponse(http.StatusServiceUnavailable, "", h), nil
	})
	client := &http.Client{Transport: transport}

	_, err := DoJSONURLWithClient(context.Background(), "https://example.test/v1", "ua", nil, map[string]any{}, client, RequestOptions{MaxRetries: 3, MaxRetryDelayMs: 1000})
	if err == nil {
		t.Fatalf("expected cap error")
	}
	if !strings.Contains(err.Error(), "maxRetryDelayMs") {
		t.Fatalf("error %q should mention maxRetryDelayMs", err.Error())
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (aborted before second request)", got)
	}
}

func TestDoJSONURLWithClientContextCancelledDuringRetry(t *testing.T) {
	// Context cancellation must take priority over a pending retry: after the
	// first retryable 503, a cancelled context stops the loop without issuing a
	// second request.
	var attempts int32
	ctx, cancel := context.WithCancel(context.Background())
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		cancel() // cancel before the retry delay elapses
		h := http.Header{}
		h.Set("Retry-After-Ms", "10000")
		return newResponse(http.StatusServiceUnavailable, "", h), nil
	})
	client := &http.Client{Transport: transport}

	start := time.Now()
	_, err := DoJSONURLWithClient(ctx, "https://example.test/v1", "ua", nil, map[string]any{}, client, RequestOptions{MaxRetries: 5})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("retry did not honor cancellation promptly: %s", elapsed)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no second request after cancel)", got)
	}
}

func TestDoJSONURLWithClientRetriesTransportError(t *testing.T) {
	// A transport (network) error is retryable while the context is live and
	// attempts remain; a subsequent success returns normally.
	var attempts int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			return nil, errors.New("connection refused")
		}
		return newResponse(http.StatusOK, `{"ok":true}`, nil), nil
	})
	client := &http.Client{Transport: transport}

	raw, err := DoJSONURLWithClient(context.Background(), "https://example.test/v1", "ua", nil, map[string]any{}, client, RequestOptions{MaxRetries: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("body = %q", raw)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}
