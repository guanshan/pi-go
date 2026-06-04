package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

var openAICodexSSEHeaderTimeout = 10 * time.Second

func (r *ModelRegistry) doOpenAICodexResponsesJSON(ctx context.Context, req ChatRequest, url string, headers map[string]string, body map[string]any) ([]byte, error) {
	rawBody, err := aiproviders.MarshalJSON(body)
	if err != nil {
		return nil, err
	}
	options := providerRequestOptions(req)
	for attempt := 0; ; attempt++ {
		resp, cleanup, err := doOpenAICodexResponsesHTTPRequest(ctx, req, url, headers, rawBody, openAICodexSSEHeaderTimeout)
		if err != nil {
			if shouldRetryOpenAICodexError(ctx, err, attempt, options, nil) {
				if retryErr := waitOpenAICodexRetry(ctx, attempt, options, nil); retryErr != nil {
					return nil, retryErr
				}
				continue
			}
			return nil, err
		}
		raw, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cleanup()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if req.OnResponse != nil {
				if err := req.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: aiproviders.HeadersRecord(resp.Header)}, req.Model); err != nil {
					return nil, err
				}
			}
			return raw, nil
		}
		err = openAICodexResponseError(resp.StatusCode, raw)
		if req.OnResponse != nil && !shouldRetryOpenAICodexError(ctx, err, attempt, options, resp) {
			if responseErr := req.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: aiproviders.HeadersRecord(resp.Header)}, req.Model); responseErr != nil {
				return nil, responseErr
			}
		}
		if shouldRetryOpenAICodexError(ctx, err, attempt, options, resp) {
			if retryErr := waitOpenAICodexRetry(ctx, attempt, options, resp); retryErr != nil {
				return nil, retryErr
			}
			continue
		}
		return nil, err
	}
}

func (r *ModelRegistry) openAICodexResponsesHTTPStreamResponse(ctx context.Context, req ChatRequest, url string, headers map[string]string, rawBody []byte) (*http.Response, context.CancelFunc, error) {
	options := providerRequestOptions(req)
	for attempt := 0; ; attempt++ {
		resp, cleanup, err := doOpenAICodexResponsesHTTPRequest(ctx, req, url, headers, rawBody, openAICodexSSEHeaderTimeout)
		if err != nil {
			if shouldRetryOpenAICodexError(ctx, err, attempt, options, nil) {
				if retryErr := waitOpenAICodexRetry(ctx, attempt, options, nil); retryErr != nil {
					return nil, nil, retryErr
				}
				continue
			}
			return nil, nil, err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, cleanup, nil
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cleanup()
		err = openAICodexResponseError(resp.StatusCode, raw)
		if req.OnResponse != nil && !shouldRetryOpenAICodexError(ctx, err, attempt, options, resp) {
			if responseErr := req.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: aiproviders.HeadersRecord(resp.Header)}, req.Model); responseErr != nil {
				return nil, nil, responseErr
			}
		}
		if shouldRetryOpenAICodexError(ctx, err, attempt, options, resp) {
			if retryErr := waitOpenAICodexRetry(ctx, attempt, options, resp); retryErr != nil {
				return nil, nil, retryErr
			}
			continue
		}
		return nil, nil, err
	}
}

func doOpenAICodexResponsesHTTPRequest(ctx context.Context, req ChatRequest, url string, headers map[string]string, rawBody []byte, headerTimeout time.Duration) (*http.Response, context.CancelFunc, error) {
	doCtx, cancel := context.WithCancel(ctx)
	httpReq, err := http.NewRequestWithContext(doCtx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		cancel()
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "pi-go/"+Version)
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	var timedOut atomic.Bool
	var timer *time.Timer
	if headerTimeout > 0 {
		timer = time.AfterFunc(headerTimeout, func() {
			timedOut.Store(true)
			cancel()
		})
	}
	resp, err := providerHTTPClient(req).Do(httpReq)
	if timer != nil {
		_ = timer.Stop()
	}
	if err != nil {
		cancel()
		if timedOut.Load() && ctx.Err() == nil {
			return nil, nil, fmt.Errorf("codex SSE response headers timed out after %dms", headerTimeout.Milliseconds())
		}
		return nil, nil, err
	}
	return resp, cancel, nil
}

func shouldRetryOpenAICodexError(ctx context.Context, err error, attempt int, options aiproviders.RequestOptions, resp *http.Response) bool {
	if err == nil || ctx.Err() != nil || attempt >= aiproviders.MaxRetries(options) {
		return false
	}
	if isOpenAICodexTerminalRateLimitError(err.Error()) {
		return false
	}
	if resp != nil {
		return aiproviders.IsRetryableStatus(resp.StatusCode)
	}
	return IsRetryableProviderError(err.Error())
}

func waitOpenAICodexRetry(ctx context.Context, attempt int, options aiproviders.RequestOptions, resp *http.Response) error {
	delay, err := aiproviders.ResponseRetryDelay(resp, attempt, options)
	if err != nil {
		return err
	}
	return aiproviders.WaitForRetry(ctx, delay)
}

func openAICodexResponseError(status int, raw []byte) error {
	fields := openAICodexErrorFieldsFromRaw(raw)
	if friendly := openAICodexFriendlyUsageLimitMessage(status, fields); friendly != "" {
		return fmt.Errorf("%s", friendly)
	}
	body := strings.TrimSpace(string(raw))
	if message := fields.message(); message != "" {
		body = message
	}
	if body == "" {
		return fmt.Errorf("HTTP %d", status)
	}
	return fmt.Errorf("HTTP %d: %s", status, body)
}

func openAICodexFriendlyUsageLimitMessage(status int, fields openAICodexErrorFields) string {
	code := strings.ToLower(fields.code())
	message := strings.ToLower(fields.message())
	usageLimit := code == "usage_limit_reached" ||
		code == "usage_not_included" ||
		strings.Contains(message, "monthly usage limit") ||
		strings.Contains(message, "free usage limit") ||
		(status == http.StatusTooManyRequests && fields.planType() != "")
	if !usageLimit {
		return ""
	}
	plan := strings.TrimSpace(fields.planType())
	if plan == "" {
		plan = "unknown"
	}
	out := fmt.Sprintf("You have hit your ChatGPT usage limit (%s plan).", plan)
	if delay := fields.resetDelay(time.Now()); delay > 0 {
		minutes := int(math.Ceil(delay.Minutes()))
		if minutes < 1 {
			minutes = 1
		}
		out += fmt.Sprintf(" Try again in ~%d min.", minutes)
	} else {
		out += " Try again later."
	}
	return out
}

func isOpenAICodexTerminalRateLimitError(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "chatgpt usage limit") ||
		strings.Contains(lower, "usage_limit_reached") ||
		strings.Contains(lower, "usage_not_included") ||
		strings.Contains(lower, "monthly usage limit") ||
		strings.Contains(lower, "free usage limit")
}

type openAICodexErrorFields map[string]string

func (f openAICodexErrorFields) code() string {
	return firstNonEmpty(f["code"], f["type"], f["error.code"], f["error.type"])
}

func (f openAICodexErrorFields) message() string {
	return firstNonEmpty(f["message"], f["error.message"], f["detail"], f["error.detail"])
}

func (f openAICodexErrorFields) planType() string {
	return firstNonEmpty(f["plan_type"], f["planType"], f["plan"], f["error.plan_type"], f["error.planType"], f["error.plan"])
}

func (f openAICodexErrorFields) resetDelay(now time.Time) time.Duration {
	value := firstNonEmpty(f["resets_at"], f["resetsAt"], f["reset_at"], f["resetAt"], f["error.resets_at"], f["error.resetsAt"])
	if value == "" {
		return 0
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at.Sub(now)
	}
	if at, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return at.Sub(now)
	}
	if unix, err := parseFloat(value); err == nil {
		if unix > 1_000_000_000_000 {
			return time.UnixMilli(int64(unix)).Sub(now)
		}
		return time.Unix(int64(unix), 0).Sub(now)
	}
	return 0
}

func openAICodexErrorFieldsFromRaw(raw []byte) openAICodexErrorFields {
	fields := openAICodexErrorFields{}
	trimmed := bytes.TrimSpace(raw)
	if bytes.HasPrefix(trimmed, []byte("data:")) || bytes.Contains(trimmed, []byte("\ndata:")) {
		for _, line := range strings.Split(string(trimmed), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var decoded any
			if err := json.Unmarshal([]byte(data), &decoded); err == nil {
				collectOpenAICodexErrorFields(fields, "", decoded)
			}
		}
		return fields
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err == nil {
		collectOpenAICodexErrorFields(fields, "", decoded)
	}
	return fields
}

func collectOpenAICodexErrorFields(fields openAICodexErrorFields, prefix string, value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			collectOpenAICodexErrorFields(fields, next, child)
		}
	case string:
		fields[prefix] = v
	case float64:
		fields[prefix] = fmt.Sprintf("%.0f", v)
	case bool:
		fields[prefix] = fmt.Sprintf("%t", v)
	}
}

func parseFloat(value string) (float64, error) {
	return strconv.ParseFloat(value, 64)
}
