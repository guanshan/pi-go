package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	// deadline (200ms) is what fires first. Keep the idle window above very short
	// scheduler hiccups while still well below the total timeout.
	client := NewHTTPClientWithOptions(RequestOptions{TimeoutMs: 5000, IdleTimeoutMs: 200})

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
	// The idle deadline is 200ms; it must fire well before the 5s total timeout.
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

// TestIdleTimeoutReaderDoesNotTruncateLiveStream proves the idle timer never
// closes a body that is still delivering data within the idle window. Unlike a
// no-op Close, liveStreamReadCloser.Close actually severs the stream, so a
// premature timer-close would lose data and the assertions below would fail.
// The full payload must arrive and the body must remain open until the consumer
// closes it explicitly.
func TestIdleTimeoutReaderDoesNotTruncateLiveStream(t *testing.T) {
	body := &liveStreamReadCloser{chunks: []string{"a", "b", "c", "d"}, delay: 5 * time.Millisecond}
	reader := newIdleTimeoutReader(body, 100*time.Millisecond)

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("live stream read returned error: %v", err)
	}
	if string(got) != "abcd" {
		t.Fatalf("live stream = %q, want abcd", got)
	}
	if c := atomic.LoadInt32(&body.closeCount); c != 0 {
		t.Fatalf("idle timer closed a live stream mid-flight: closeCount=%d, want 0", c)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("explicit Close returned error: %v", err)
	}
	if c := atomic.LoadInt32(&body.closeCount); c != 1 {
		t.Fatalf("body close count after explicit Close=%d, want 1", c)
	}
}

func TestIdleTimeoutReaderTimerCloseIsIdempotent(t *testing.T) {
	body := newCloseCountingReadCloser()
	reader := newIdleTimeoutReader(body, 10*time.Millisecond)

	n, err := reader.Read(make([]byte, 1))
	if n != 0 {
		t.Fatalf("timeout read returned n=%d, want 0", n)
	}
	if err == nil || !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("expected idle timeout error, got %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("explicit Close after timer close returned error: %v", err)
	}
	if got := atomic.LoadInt32(&body.closeCount); got != 1 {
		t.Fatalf("underlying body close count=%d, want 1", got)
	}
}

// liveStreamReadCloser emits its chunks one per Read, each after a sub-idle
// delay, then EOF. Its Close genuinely severs the stream (subsequent reads fail
// with io.ErrClosedPipe) and counts invocations, so a test can detect a timer
// that closes the body mid-stream.
type liveStreamReadCloser struct {
	chunks     []string
	idx        int
	delay      time.Duration
	closeCount int32
	closed     int32
}

func (b *liveStreamReadCloser) Read(p []byte) (int, error) {
	if atomic.LoadInt32(&b.closed) != 0 {
		return 0, io.ErrClosedPipe
	}
	if b.idx >= len(b.chunks) {
		return 0, io.EOF
	}
	time.Sleep(b.delay) // each chunk arrives well within the idle window
	n := copy(p, b.chunks[b.idx])
	b.idx++
	return n, nil
}

func (b *liveStreamReadCloser) Close() error {
	atomic.AddInt32(&b.closeCount, 1)
	atomic.StoreInt32(&b.closed, 1)
	return nil
}

type closeCountingReadCloser struct {
	closed     chan struct{}
	closeCount int32
}

func newCloseCountingReadCloser() *closeCountingReadCloser {
	return &closeCountingReadCloser{closed: make(chan struct{})}
}

func (b *closeCountingReadCloser) Read([]byte) (int, error) {
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *closeCountingReadCloser) Close() error {
	if atomic.AddInt32(&b.closeCount, 1) == 1 {
		close(b.closed)
		return nil
	}
	return io.ErrClosedPipe
}
