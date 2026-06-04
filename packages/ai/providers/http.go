package providers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	openaioption "github.com/openai/openai-go/v3/option"
)

type ProviderResponse struct {
	Status  int
	Headers map[string]string
}

type RequestOptions struct {
	TimeoutMs       int
	IdleTimeoutMs   int
	MaxRetries      int
	UseMaxRetries   bool
	MaxRetryDelayMs int
	OnResponse      func(ProviderResponse) error
}

func NewHTTPClient() *http.Client {
	return NewHTTPClientWithOptions(RequestOptions{})
}

func NewHTTPClientWithOptions(options RequestOptions) *http.Client {
	client := &http.Client{Timeout: RequestTimeout(options)}
	// IdleTimeoutMs is a per-read (stream-idle) deadline distinct from the total
	// request Timeout; see docs/AI_PROVIDER_PARITY.md (P1-08).
	if options.IdleTimeoutMs > 0 {
		client.Transport = &idleTimeoutTransport{
			base: http.DefaultTransport,
			idle: time.Duration(options.IdleTimeoutMs) * time.Millisecond,
		}
	}
	return client
}

// idleTimeoutTransport wraps a RoundTripper so each response body is governed by
// a per-read idle deadline; the total-request Timeout still applies via the
// owning http.Client.
type idleTimeoutTransport struct {
	base http.RoundTripper
	idle time.Duration
}

func (t *idleTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil || t.idle <= 0 {
		return resp, err
	}
	resp.Body = newIdleTimeoutReader(resp.Body, t.idle)
	return resp, nil
}

// idleTimeoutReader enforces a per-read idle deadline without ever closing a
// body that is still delivering data. A dedicated goroutine (readLoop) performs
// the underlying reads and hands each chunk to Read over a channel; Read waits
// up to the idle window for the next chunk and closes the body only when that
// window genuinely elapses with no data arriving. A slow-but-progressing stream
// is therefore never truncated. (The previous design armed a timer that closed
// the body around every Read, so a chunk landing near the boundary — or the
// terminal EOF — could be lost the instant the timer fired, even on a healthy
// connection.)
type idleTimeoutReader struct {
	body io.ReadCloser
	idle time.Duration

	start   sync.Once
	results chan idleReadResult
	closeCh chan struct{}

	// Consumer-goroutine-only state. Read is not safe for concurrent use (per the
	// io.Reader contract), so these need no lock: leftover holds the tail of an
	// oversized chunk, and done/finalErr latch the terminal result.
	leftover []byte
	done     bool
	finalErr error

	mu     sync.Mutex
	closed bool
}

// idleReadResult carries one outcome from readLoop: either a freshly-copied data
// chunk or a terminal error (the two are sent as separate results).
type idleReadResult struct {
	data []byte
	err  error
}

func newIdleTimeoutReader(body io.ReadCloser, idle time.Duration) *idleTimeoutReader {
	return &idleTimeoutReader{
		body:    body,
		idle:    idle,
		results: make(chan idleReadResult, 1),
		closeCh: make(chan struct{}),
	}
}

// readLoop is the sole reader of the underlying body. It copies each chunk into
// a fresh buffer (so the consumer owns the bytes even if a later Read times out
// and aborts the stream) and forwards it, then forwards the terminal error. It
// exits once the body errors or the reader is closed.
func (r *idleTimeoutReader) readLoop() {
	defer close(r.results)
	buf := make([]byte, 32*1024)
	for {
		n, err := r.body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case r.results <- idleReadResult{data: chunk}:
			case <-r.closeCh:
				return
			}
		}
		if err != nil {
			select {
			case r.results <- idleReadResult{err: err}:
			case <-r.closeCh:
			}
			return
		}
	}
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	r.start.Do(func() { go r.readLoop() })
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.leftover) > 0 {
		n := copy(p, r.leftover)
		r.leftover = r.leftover[n:]
		return n, nil
	}
	if r.done {
		return 0, r.finalErr
	}

	timer := time.NewTimer(r.idle)
	defer timer.Stop()
	select {
	case res, ok := <-r.results:
		return r.deliver(p, res, ok)
	case <-timer.C:
		// The idle window elapsed with no delivery. Prefer a chunk that landed at
		// the very boundary before declaring a timeout, so a healthy stream is
		// never truncated by a hair's-breadth scheduling race.
		select {
		case res, ok := <-r.results:
			return r.deliver(p, res, ok)
		default:
		}
		_ = r.closeBody()
		r.done = true
		r.finalErr = fmt.Errorf("stream idle timeout after %s", r.idle)
		return 0, r.finalErr
	}
}

// deliver copies a received result into p, stashing any overflow in leftover and
// latching a terminal error/EOF for subsequent calls.
func (r *idleTimeoutReader) deliver(p []byte, res idleReadResult, ok bool) (int, error) {
	if !ok {
		r.done = true
		r.finalErr = io.EOF
		return 0, io.EOF
	}
	if len(res.data) > 0 {
		n := copy(p, res.data)
		if n < len(res.data) {
			r.leftover = res.data[n:]
		}
		return n, nil
	}
	r.done = true
	r.finalErr = res.err
	if r.finalErr == nil {
		r.finalErr = io.EOF
	}
	return 0, r.finalErr
}

func (r *idleTimeoutReader) Close() error {
	return r.closeBody()
}

// closeBody closes the underlying body exactly once and signals readLoop to
// stop. Safe to call from both the idle-timeout path and an explicit Close.
func (r *idleTimeoutReader) closeBody() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	close(r.closeCh)
	body := r.body
	r.mu.Unlock()
	return body.Close()
}

func RequestTimeout(options RequestOptions) time.Duration {
	if options.TimeoutMs > 0 {
		return time.Duration(options.TimeoutMs) * time.Millisecond
	}
	return 10 * time.Minute
}

func MaxRetries(options RequestOptions) int {
	if options.MaxRetries < 0 {
		return 0
	}
	return options.MaxRetries
}

func ShouldSetMaxRetries(options RequestOptions) bool {
	return options.UseMaxRetries || options.MaxRetries != 0
}

func HeadersRecord(headers http.Header) map[string]string {
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) > 0 {
			out[key] = values[0]
		}
	}
	if len(out) == 0 {
		return map[string]string{}
	}
	return out
}

func ProviderResponseFromHTTP(resp *http.Response) ProviderResponse {
	if resp == nil {
		return ProviderResponse{Headers: map[string]string{}}
	}
	return ProviderResponse{Status: resp.StatusCode, Headers: HeadersRecord(resp.Header)}
}

func HTTPStatusError(resp *http.Response) error {
	if resp == nil {
		return fmt.Errorf("HTTP request failed")
	}
	var body string
	if resp.Body != nil {
		raw, _ := io.ReadAll(resp.Body)
		body = strings.TrimSpace(string(raw))
	}
	if body == "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
}

func DoJSONURL(ctx context.Context, url, userAgent string, headers map[string]string, body any) ([]byte, error) {
	return DoJSONURLWithClient(ctx, url, userAgent, headers, body, nil, RequestOptions{})
}

func DoJSONURLWithClient(ctx context.Context, url, userAgent string, headers map[string]string, body any, httpClient *http.Client, options RequestOptions) ([]byte, error) {
	// MarshalJSON (not json.Marshal) so < > & are sent literally, matching the
	// TS upstream wire bytes. Used by the Mistral and Google providers.
	data, err := MarshalJSON(body)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = NewHTTPClientWithOptions(options)
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if userAgent != "" {
			req.Header.Set("User-Agent", userAgent)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			if ctx.Err() == nil && attempt < MaxRetries(options) {
				if retryErr := waitForRetry(ctx, retryDelay(nil, attempt, options)); retryErr != nil {
					return nil, retryErr
				}
				continue
			}
			return nil, err
		}
		if isRetryableStatus(resp.StatusCode) && attempt < MaxRetries(options) {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			delay, err := responseRetryDelay(resp, attempt, options)
			if err != nil {
				return nil, err
			}
			if retryErr := waitForRetry(ctx, delay); retryErr != nil {
				return nil, retryErr
			}
			continue
		}
		if options.OnResponse != nil {
			if err := options.OnResponse(ProviderResponseFromHTTP(resp)); err != nil {
				_ = resp.Body.Close()
				return nil, err
			}
		}
		raw, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		return raw, nil
	}
}

func DoOpenAISDKJSONWithClient(ctx context.Context, endpoint string, key string, headers map[string]string, body any, bearerAuth bool, httpClient *http.Client, requestOptions ...RequestOptions) ([]byte, error) {
	options := firstRequestOptions(requestOptions)
	if httpClient == nil {
		httpClient = NewHTTPClientWithOptions(options)
	}
	client := NewOpenAIClient(key, "", headers, bearerAuth, httpClient, options)
	var raw []byte
	if err := client.Post(ctx, endpoint, body, nil, openaioption.WithResponseBodyInto(&raw)); err != nil {
		return nil, err
	}
	return raw, nil
}

func DoAnthropicSDKJSONWithClient(ctx context.Context, endpoint string, key string, headers map[string]string, body any, httpClient *http.Client, requestOptions ...RequestOptions) ([]byte, error) {
	options := firstRequestOptions(requestOptions)
	if httpClient == nil {
		httpClient = NewHTTPClientWithOptions(options)
	}
	client := NewAnthropicClient(key, "", headers, httpClient, options)
	var raw []byte
	if err := client.Post(ctx, endpoint, body, nil, anthropicoption.WithResponseBodyInto(&raw)); err != nil {
		return nil, err
	}
	return raw, nil
}

func MergeHeaders(sources ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, headers := range sources {
		for key, value := range headers {
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func RequestHeaders(sources ...map[string]string) map[string]string {
	headers := map[string]string{}
	for _, source := range sources {
		for key, value := range source {
			headers[key] = value
		}
	}
	return headers
}

func firstRequestOptions(values []RequestOptions) RequestOptions {
	if len(values) == 0 {
		return RequestOptions{}
	}
	return values[0]
}

func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// IsRetryableStatus reports whether an HTTP status is transient enough to retry.
func IsRetryableStatus(status int) bool {
	return isRetryableStatus(status)
}

func responseRetryDelay(resp *http.Response, attempt int, options RequestOptions) (time.Duration, error) {
	if resp == nil {
		return retryDelay(nil, attempt, options), nil
	}
	if value := strings.TrimSpace(resp.Header.Get("Retry-After-Ms")); value != "" {
		if ms, err := strconv.Atoi(value); err == nil {
			return capRetryDelay(time.Duration(ms)*time.Millisecond, options)
		}
	}
	if value := strings.TrimSpace(resp.Header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.ParseFloat(value, 64); err == nil {
			return capRetryDelay(time.Duration(seconds*float64(time.Second)), options)
		}
		if at, err := http.ParseTime(value); err == nil {
			return capRetryDelay(time.Until(at), options)
		}
	}
	return retryDelay(nil, attempt, options), nil
}

// ResponseRetryDelay returns the delay for a retry attempt, honoring
// Retry-After-Ms / Retry-After headers and maxRetryDelayMs.
func ResponseRetryDelay(resp *http.Response, attempt int, options RequestOptions) (time.Duration, error) {
	return responseRetryDelay(resp, attempt, options)
}

func retryDelay(_ *http.Response, attempt int, options RequestOptions) time.Duration {
	delay := time.Duration(100*(1<<min(attempt, 5))) * time.Millisecond
	capped, err := capRetryDelay(delay, options)
	if err != nil {
		return delay
	}
	return capped
}

// RetryDelay returns the exponential fallback delay for an attempt.
func RetryDelay(attempt int, options RequestOptions) time.Duration {
	return retryDelay(nil, attempt, options)
}

func capRetryDelay(delay time.Duration, options RequestOptions) (time.Duration, error) {
	if delay < 0 {
		delay = 0
	}
	if options.MaxRetryDelayMs <= 0 {
		return delay, nil
	}
	capDelay := time.Duration(options.MaxRetryDelayMs) * time.Millisecond
	if delay > capDelay {
		return 0, fmt.Errorf("retry delay %s exceeds maxRetryDelayMs %d", delay, options.MaxRetryDelayMs)
	}
	return delay, nil
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// WaitForRetry waits for delay or context cancellation, whichever comes first.
func WaitForRetry(ctx context.Context, delay time.Duration) error {
	return waitForRetry(ctx, delay)
}
