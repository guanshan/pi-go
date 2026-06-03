package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestIdleTimeoutFiresOnStalledStream covers P1-08 (ai side): a server that
// sends a partial body and then stalls mid-stream must surface a stream idle
// timeout error rather than hanging until the total request Timeout. The idle
// window is per-read, distinct from the total Timeout.
func TestIdleTimeoutFiresOnStalledStream(t *testing.T) {
	// done is closed once the client has observed the idle timeout, releasing the
	// stalled handler so the test server can shut down cleanly.
	done := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		// Send a first chunk so the stream is established, then stall until the
		// client has timed out (bounded by <-done, with a hard ceiling so the
		// handler can never leak).
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}))
	defer server.Close()

	// Total Timeout is generous (5s) so the test only passes if the per-read idle
	// deadline (50ms) is what fires first.
	client := NewHTTPClientWithOptions(RequestOptions{TimeoutMs: 5000, IdleTimeoutMs: 50})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected to receive response headers, got error: %v", err)
	}
	defer resp.Body.Close()

	start := time.Now()
	_, err = io.ReadAll(resp.Body)
	elapsed := time.Since(start)
	close(done)

	if err == nil {
		t.Fatal("expected idle timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("expected idle timeout error, got: %v", err)
	}
	// The idle deadline is 50ms; it must fire well before the 5s total timeout.
	if elapsed >= 2*time.Second {
		t.Fatalf("idle timeout did not fire promptly, elapsed=%s", elapsed)
	}
}

// TestIdleTimeoutAllowsCompleteStream verifies that a stream which keeps
// producing bytes within the idle window (steady sub-idle progress) completes
// normally; only an actual stall exceeds the idle window.
func TestIdleTimeoutAllowsCompleteStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			_, _ = io.WriteString(w, "data: chunk\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond) // well under the 200ms idle window
		}
	}))
	defer server.Close()

	client := NewHTTPClientWithOptions(RequestOptions{TimeoutMs: 5000, IdleTimeoutMs: 200})

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("steady stream should not idle-timeout, got: %v", err)
	}
	if !strings.Contains(string(body), "chunk") {
		t.Fatalf("unexpected body: %q", body)
	}
}

// TestNoIdleTimeoutWhenUnset confirms that omitting IdleTimeoutMs leaves the
// default transport in place (no idle wrapper installed).
func TestNoIdleTimeoutWhenUnset(t *testing.T) {
	if c := NewHTTPClientWithOptions(RequestOptions{}); c.Transport != nil {
		t.Fatalf("expected nil (default) transport when IdleTimeoutMs unset, got %T", c.Transport)
	}
	if c := NewHTTPClientWithOptions(RequestOptions{IdleTimeoutMs: 100}); c.Transport == nil {
		t.Fatal("expected idle-timeout transport installed when IdleTimeoutMs set")
	}
}
