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

// idleTimeoutReader fails a Read that blocks longer than the idle window: a timer
// armed around each Read closes the body on fire, unblocking the stalled Read.
type idleTimeoutReader struct {
	body io.ReadCloser
	idle time.Duration

	mu       sync.Mutex
	closed   bool
	timedOut bool
}

func newIdleTimeoutReader(body io.ReadCloser, idle time.Duration) *idleTimeoutReader {
	return &idleTimeoutReader{body: body, idle: idle}
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	timer := time.AfterFunc(r.idle, func() {
		r.mu.Lock()
		r.timedOut = true
		body := r.body
		r.mu.Unlock()
		// Closing the underlying body unblocks a stalled Read on the connection.
		_ = body.Close()
	})
	n, err := r.body.Read(p)
	timer.Stop()
	r.mu.Lock()
	timedOut := r.timedOut
	r.mu.Unlock()
	if timedOut && (err != nil || n == 0) {
		return n, fmt.Errorf("stream idle timeout after %s", r.idle)
	}
	return n, err
}

func (r *idleTimeoutReader) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
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

func retryDelay(_ *http.Response, attempt int, options RequestOptions) time.Duration {
	delay := time.Duration(100*(1<<min(attempt, 5))) * time.Millisecond
	capped, err := capRetryDelay(delay, options)
	if err != nil {
		return delay
	}
	return capped
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
