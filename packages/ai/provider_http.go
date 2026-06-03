package ai

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

// htmlUnescapingTransport wraps an http.RoundTripper and rewrites outgoing JSON
// request bodies so the HTML-significant characters < > & appear literally rather
// than HTML-escaped (\uXXXX). It is used to align third-party SDKs whose internal
// JSON encoder HTML-escapes by default (e.g. google.golang.org/genai) with the
// TypeScript upstream's JSON.stringify wire bytes. Bodies are only rewritten when
// the request advertises a JSON content type and exposes a re-readable body.
type htmlUnescapingTransport struct {
	base http.RoundTripper
}

func (t htmlUnescapingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req != nil && req.Body != nil && isJSONContentType(req.Header.Get("Content-Type")) {
		if raw, err := io.ReadAll(req.Body); err == nil {
			_ = req.Body.Close()
			rewritten := aiproviders.UnescapeJSONHTML(raw)
			// Clone the request so we never mutate the caller's *http.Request.
			clone := req.Clone(req.Context())
			clone.Body = io.NopCloser(bytes.NewReader(rewritten))
			clone.ContentLength = int64(len(rewritten))
			clone.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(rewritten)), nil
			}
			return base.RoundTrip(clone)
		}
	}
	return base.RoundTrip(req)
}

func isJSONContentType(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "application/json") || strings.Contains(value, "+json")
}

// withHTMLUnescapingTransport returns a copy of client whose transport rewrites
// HTML-escaped JSON request bodies to their literal form. The input client is not
// mutated (callers may share it), and a nil client falls back to a new one.
func withHTMLUnescapingTransport(client *http.Client) *http.Client {
	if client == nil {
		client = aiproviders.NewHTTPClient()
	}
	wrapped := *client
	wrapped.Transport = htmlUnescapingTransport{base: client.Transport}
	return &wrapped
}

func (r *ModelRegistry) doJSON(ctx context.Context, req ChatRequest, model Model, key string, body any, extraHeaders map[string]string) ([]byte, error) {
	headers := map[string]string{}
	for k, v := range model.Headers {
		headers[k] = v
	}
	switch model.Provider {
	case "anthropic":
		headers["x-api-key"] = key
		headers["anthropic-version"] = "2023-06-01"
		headers["anthropic-beta"] = "prompt-caching-2024-07-31"
	case "azure-openai":
		headers["api-key"] = key
	default:
		headers["Authorization"] = "Bearer " + key
	}
	for k, v := range extraHeaders {
		headers[k] = v
	}
	return aiproviders.DoJSONURLWithClient(ctx, model.BaseURL, "pi-go/"+Version, headers, body, providerHTTPClient(req), providerRequestOptions(req))
}

var providerSDKHTTPClient = aiproviders.NewHTTPClient()

// streamScannerMaxLineBytes is the maximum size of a single SSE line accepted by
// the streaming providers' bufio.Scanner. A single chunk can be large (long
// thinking summaries, big tool-call argument deltas, inline base64 data), and an
// undersized limit makes scanner.Scan() fail with bufio.ErrTooLong, aborting the
// whole stream. Keep this generous and shared so all providers behave the same.
const streamScannerMaxLineBytes = 8 * 1024 * 1024

func providerHTTPClient(req ChatRequest) *http.Client {
	if req.TimeoutMs > 0 || req.IdleTimeoutMs > 0 {
		return aiproviders.NewHTTPClientWithOptions(providerRequestOptions(req))
	}
	return providerSDKHTTPClient
}

func providerRequestOptions(req ChatRequest) aiproviders.RequestOptions {
	options := aiproviders.RequestOptions{
		TimeoutMs:       req.TimeoutMs,
		IdleTimeoutMs:   req.IdleTimeoutMs,
		MaxRetries:      req.MaxRetries,
		UseMaxRetries:   true,
		MaxRetryDelayMs: req.MaxRetryDelayMs,
	}
	if req.OnResponse != nil {
		options.OnResponse = func(resp aiproviders.ProviderResponse) error {
			return req.OnResponse(ProviderResponse{Status: resp.Status, Headers: resp.Headers}, req.Model)
		}
	}
	return options
}

func providerThinkingBudgets(req ChatRequest) aiproviders.ThinkingBudgets {
	return aiproviders.ThinkingBudgets{
		Minimal: req.ThinkingBudgets.Minimal,
		Low:     req.ThinkingBudgets.Low,
		Medium:  req.ThinkingBudgets.Medium,
		High:    req.ThinkingBudgets.High,
	}
}

const (
	CloudflareWorkersAIBaseURL          = aiproviders.CloudflareWorkersAIBaseURL
	CloudflareAIGatewayCompatBaseURL    = aiproviders.CloudflareAIGatewayCompatBaseURL
	CloudflareAIGatewayOpenAIBaseURL    = aiproviders.CloudflareAIGatewayOpenAIBaseURL
	CloudflareAIGatewayAnthropicBaseURL = aiproviders.CloudflareAIGatewayAnthropicBaseURL
)

func IsCloudflareProvider(provider string) bool {
	return aiproviders.IsCloudflareProvider(provider)
}

func HasCloudflareWorkersAICredentials() bool {
	return aiproviders.HasCloudflareWorkersAICredentials()
}

func HasCloudflareAIGatewayCredentials() bool {
	return aiproviders.HasCloudflareAIGatewayCredentials()
}

func HasCloudflareRequiredEnv(provider string) bool {
	return aiproviders.HasCloudflareRequiredEnv(provider)
}

func ResolveCloudflareBaseURL(model Model) (string, error) {
	return aiproviders.ResolveCloudflareBaseURL(model.BaseURL, model.Provider)
}
