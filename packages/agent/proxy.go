package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

const DefaultProxyStreamBuffer = 16

type ProxyStreamOptions struct {
	AuthToken       string              `json:"-"`
	ProxyURL        string              `json:"-"`
	Headers         map[string]string   `json:"headers,omitempty"`
	Temperature     *float64            `json:"temperature,omitempty"`
	MaxTokens       int                 `json:"maxTokens,omitempty"`
	Reasoning       ai.ThinkingLevel    `json:"reasoning,omitempty"`
	CacheRetention  string              `json:"cacheRetention,omitempty"`
	SessionID       string              `json:"sessionId,omitempty"`
	Transport       string              `json:"transport,omitempty"`
	Metadata        map[string]any      `json:"metadata,omitempty"`
	ThinkingBudgets *ai.ThinkingBudgets `json:"thinkingBudgets,omitempty"`
	TimeoutMs       int                 `json:"timeoutMs,omitempty"`
	IdleTimeoutMs   int                 `json:"idleTimeoutMs,omitempty"`
	MaxRetries      int                 `json:"maxRetries,omitempty"`
	MaxRetryDelayMs int                 `json:"maxRetryDelayMs,omitempty"`
	Extra           map[string]any      `json:"-"`
	HTTPClient      *http.Client        `json:"-"`
	Buffer          int                 `json:"-"`
}

type ProxyAssistantMessageEvent struct {
	Type             string          `json:"type"`
	ContentIndex     int             `json:"contentIndex,omitempty"`
	Delta            string          `json:"delta,omitempty"`
	ContentSignature string          `json:"contentSignature,omitempty"`
	ID               string          `json:"id,omitempty"`
	ToolName         string          `json:"toolName,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	ErrorMessage     string          `json:"errorMessage,omitempty"`
	Usage            ai.Usage        `json:"usage,omitempty"`
	Raw              json.RawMessage `json:"-"`
}

func StreamProxy(options ProxyStreamOptions) StreamFn {
	return func(ctx context.Context, model ai.Model, llmContext ai.Context, streamOptions ai.StreamOptions) AssistantStream {
		merged := mergeProxyStreamOptions(options, streamOptions)
		buffer := merged.Buffer
		if buffer <= 0 {
			buffer = DefaultProxyStreamBuffer
		}
		stream := ai.NewAssistantMessageEventStream(buffer)
		go func() {
			message, _ := runProxyStream(ctx, model, llmContext, merged, stream)
			stream.End(message)
		}()
		return stream
	}
}

func runProxyStream(ctx context.Context, model ai.Model, llmContext ai.Context, options ProxyStreamOptions, stream *ai.AssistantMessageEventStream) (ai.AssistantMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	partial := ai.NewAssistantMessageForModel(model, nil, ai.Usage{}, "stop")
	if partial.TimestampMs == 0 {
		partial.TimestampMs = time.Now().UnixMilli()
	}
	if options.ProxyURL == "" {
		err := fmt.Errorf("proxy URL is required")
		partial.StopReason = "error"
		partial.ErrorMessage = err.Error()
		stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "error", Partial: partial, Error: partial})
		return partial, err
	}
	body, err := json.Marshal(map[string]any{
		"model":   model,
		"context": llmContext,
		"options": buildProxyRequestOptions(options),
	})
	if err != nil {
		return partial, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(options.ProxyURL, "/")+"/api/stream", bytes.NewReader(body))
	if err != nil {
		return partial, err
	}
	req.Header.Set("Content-Type", "application/json")
	if options.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+options.AuthToken)
	}
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		partial.StopReason = stopReasonForProxyError(ctx)
		partial.ErrorMessage = err.Error()
		stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: partial.StopReason, Partial: partial, Error: partial})
		return partial, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := proxyHTTPError(resp)
		partial.StopReason = "error"
		partial.ErrorMessage = err.Error()
		stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "error", Partial: partial, Error: partial})
		return partial, err
	}
	if err := readProxySSE(ctx, resp.Body, &partial, stream); err != nil {
		partial.StopReason = stopReasonForProxyError(ctx)
		partial.ErrorMessage = err.Error()
		stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: partial.StopReason, Partial: partial, Error: partial})
		return partial, err
	}
	return partial, nil
}

func buildProxyRequestOptions(options ProxyStreamOptions) map[string]any {
	out := map[string]any{}
	if options.Temperature != nil {
		out["temperature"] = *options.Temperature
	}
	if options.MaxTokens > 0 {
		out["maxTokens"] = options.MaxTokens
	}
	if options.Reasoning != "" {
		out["reasoning"] = options.Reasoning
	}
	if options.CacheRetention != "" {
		out["cacheRetention"] = options.CacheRetention
	}
	if options.SessionID != "" {
		out["sessionId"] = options.SessionID
	}
	if len(options.Headers) > 0 {
		out["headers"] = options.Headers
	}
	if options.Transport != "" {
		out["transport"] = options.Transport
	}
	if len(options.Metadata) > 0 {
		out["metadata"] = options.Metadata
	}
	if options.ThinkingBudgets != nil {
		out["thinkingBudgets"] = options.ThinkingBudgets
	}
	if options.TimeoutMs > 0 {
		out["timeoutMs"] = options.TimeoutMs
	}
	if options.IdleTimeoutMs > 0 {
		out["idleTimeoutMs"] = options.IdleTimeoutMs
	}
	if options.MaxRetries > 0 {
		out["maxRetries"] = options.MaxRetries
	}
	if options.MaxRetryDelayMs > 0 {
		out["maxRetryDelayMs"] = options.MaxRetryDelayMs
	}
	for key, value := range options.Extra {
		out[key] = value
	}
	return out
}

func mergeProxyStreamOptions(base ProxyStreamOptions, stream ai.StreamOptions) ProxyStreamOptions {
	if stream.Headers != nil {
		base.Headers = stream.Headers
	}
	if stream.Temperature != nil {
		base.Temperature = stream.Temperature
	}
	if stream.MaxTokens > 0 {
		base.MaxTokens = stream.MaxTokens
	}
	if stream.Reasoning != "" {
		base.Reasoning = stream.Reasoning
	}
	if stream.CacheRetention != "" {
		base.CacheRetention = stream.CacheRetention
	}
	if stream.SessionID != "" {
		base.SessionID = stream.SessionID
	}
	if stream.Transport != "" {
		base.Transport = stream.Transport
	}
	if stream.Metadata != nil {
		base.Metadata = stream.Metadata
	}
	if stream.ThinkingBudgets != (ai.ThinkingBudgets{}) {
		budgets := stream.ThinkingBudgets
		base.ThinkingBudgets = &budgets
	}
	if stream.TimeoutMs > 0 {
		base.TimeoutMs = stream.TimeoutMs
	}
	if stream.IdleTimeoutMs > 0 {
		base.IdleTimeoutMs = stream.IdleTimeoutMs
	}
	if stream.MaxRetries > 0 {
		base.MaxRetries = stream.MaxRetries
	}
	if stream.MaxRetryDelayMs > 0 {
		base.MaxRetryDelayMs = stream.MaxRetryDelayMs
	}
	return base
}

func readProxySSE(ctx context.Context, reader io.Reader, partial *ai.AssistantMessage, stream *ai.AssistantMessageEventStream) error {
	decoder := newProxyDecoder()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload == "" {
			continue
		}
		var event ProxyAssistantMessageEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
		event.Raw = append(json.RawMessage(nil), payload...)
		assistantEvent, err := decoder.process(event, partial)
		if err != nil {
			return err
		}
		if assistantEvent != nil {
			stream.Push(*assistantEvent)
		}
	}
	return scanner.Err()
}

// proxyDecoder reconstructs an AssistantMessage from the partial-stripped proxy
// event stream. Tool-call argument fragments are accumulated in partialJSON,
// keyed by content index, rather than in the ContentBlock itself. The TS source
// stores the running JSON on a transient `partialJson` property that is deleted
// at toolcall_end; ContentBlock has no such throwaway field and its Data field
// is a persisted model field (base64 payloads etc.), so writing the fragments
// there would pollute the streamed/persisted message. A side buffer keeps Data
// untouched and is dropped at toolcall_end.
type proxyDecoder struct {
	partialJSON map[int]string
}

func newProxyDecoder() *proxyDecoder {
	return &proxyDecoder{partialJSON: map[int]string{}}
}

// ProcessProxyEvent decodes a single proxy event against the partial message.
//
// Deprecated: this stateless helper cannot accumulate tool-call argument
// fragments across calls. Use a proxyDecoder (as readProxySSE does) for the
// streaming path. It is retained for single-event decoding and tests.
func ProcessProxyEvent(event ProxyAssistantMessageEvent, partial *ai.AssistantMessage) (*ai.AssistantMessageEvent, error) {
	return newProxyDecoder().process(event, partial)
}

func (d *proxyDecoder) process(event ProxyAssistantMessageEvent, partial *ai.AssistantMessage) (*ai.AssistantMessageEvent, error) {
	blocks := ai.MessageBlocks(*partial)
	ensureBlock := func(index int, block ai.ContentBlock) {
		for len(blocks) <= index {
			blocks = append(blocks, ai.ContentBlock{})
		}
		blocks[index] = block
		partial.Content = blocks
	}
	getBlock := func(index int) (ai.ContentBlock, bool) {
		if index < 0 || index >= len(blocks) {
			return ai.ContentBlock{}, false
		}
		return blocks[index], true
	}
	switch event.Type {
	case "start":
		return &ai.AssistantMessageEvent{Type: "start", Partial: *partial}, nil
	case "text_start":
		ensureBlock(event.ContentIndex, ai.ContentBlock{Type: "text"})
		return &ai.AssistantMessageEvent{Type: "text_start", ContentIndex: event.ContentIndex, Partial: *partial}, nil
	case "text_delta":
		block, ok := getBlock(event.ContentIndex)
		if !ok || block.Type != "text" {
			return nil, fmt.Errorf("received text_delta for non-text content")
		}
		block.Text += event.Delta
		ensureBlock(event.ContentIndex, block)
		return &ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: event.ContentIndex, Delta: event.Delta, Partial: *partial}, nil
	case "text_end":
		block, ok := getBlock(event.ContentIndex)
		if !ok || block.Type != "text" {
			return nil, fmt.Errorf("received text_end for non-text content")
		}
		block.TextSignature = event.ContentSignature
		ensureBlock(event.ContentIndex, block)
		return &ai.AssistantMessageEvent{Type: "text_end", ContentIndex: event.ContentIndex, Content: block.Text, Partial: *partial}, nil
	case "thinking_start":
		ensureBlock(event.ContentIndex, ai.ContentBlock{Type: "thinking"})
		return &ai.AssistantMessageEvent{Type: "thinking_start", ContentIndex: event.ContentIndex, Partial: *partial}, nil
	case "thinking_delta":
		block, ok := getBlock(event.ContentIndex)
		if !ok || block.Type != "thinking" {
			return nil, fmt.Errorf("received thinking_delta for non-thinking content")
		}
		block.Thinking += event.Delta
		ensureBlock(event.ContentIndex, block)
		return &ai.AssistantMessageEvent{Type: "thinking_delta", ContentIndex: event.ContentIndex, Delta: event.Delta, Partial: *partial}, nil
	case "thinking_end":
		block, ok := getBlock(event.ContentIndex)
		if !ok || block.Type != "thinking" {
			return nil, fmt.Errorf("received thinking_end for non-thinking content")
		}
		block.ThinkingSignature = event.ContentSignature
		ensureBlock(event.ContentIndex, block)
		return &ai.AssistantMessageEvent{Type: "thinking_end", ContentIndex: event.ContentIndex, Content: block.Thinking, Partial: *partial}, nil
	case "toolcall_start":
		d.partialJSON[event.ContentIndex] = ""
		ensureBlock(event.ContentIndex, ai.ContentBlock{Type: "toolCall", ID: event.ID, Name: event.ToolName, Arguments: json.RawMessage(`{}`)})
		return &ai.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: event.ContentIndex, Partial: *partial}, nil
	case "toolcall_delta":
		block, ok := getBlock(event.ContentIndex)
		if !ok || block.Type != "toolCall" {
			return nil, fmt.Errorf("received toolcall_delta for non-toolCall content")
		}
		buffer := d.partialJSON[event.ContentIndex] + event.Delta
		d.partialJSON[event.ContentIndex] = buffer
		block.Arguments = parseStreamingToolArgs(buffer)
		ensureBlock(event.ContentIndex, block)
		return &ai.AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: event.ContentIndex, Delta: event.Delta, Partial: *partial}, nil
	case "toolcall_end":
		block, ok := getBlock(event.ContentIndex)
		if !ok || block.Type != "toolCall" {
			return nil, nil
		}
		delete(d.partialJSON, event.ContentIndex)
		if len(block.Arguments) == 0 {
			block.Arguments = json.RawMessage(`{}`)
		}
		ensureBlock(event.ContentIndex, block)
		call := ai.ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Arguments, ThoughtSignature: block.ThoughtSignature}
		return &ai.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: event.ContentIndex, ToolCall: &call, Partial: *partial}, nil
	case "done":
		partial.StopReason = event.Reason
		partial.Usage = event.Usage
		return &ai.AssistantMessageEvent{Type: "done", Reason: event.Reason, Partial: *partial, Message: *partial}, nil
	case "error":
		partial.StopReason = event.Reason
		partial.ErrorMessage = event.ErrorMessage
		partial.Usage = event.Usage
		return &ai.AssistantMessageEvent{Type: "error", Reason: event.Reason, Partial: *partial, Error: *partial}, nil
	default:
		return nil, nil
	}
}

func parseStreamingToolArgs(raw string) json.RawMessage {
	parsed := ai.ParseStreamingJSON(raw)
	if len(parsed) == 0 {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func proxyHTTPError(resp *http.Response) error {
	raw, _ := io.ReadAll(resp.Body)
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &payload) == nil && payload.Error != "" {
		return fmt.Errorf("proxy error: %s", payload.Error)
	}
	return fmt.Errorf("proxy error: %d %s", resp.StatusCode, resp.Status)
}

func stopReasonForProxyError(ctx context.Context) string {
	if ctx != nil && ctx.Err() != nil {
		return "aborted"
	}
	return "error"
}
