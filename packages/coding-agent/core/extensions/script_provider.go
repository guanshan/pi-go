package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
)

type scriptProviderMetadata struct {
	API          string          `json:"api"`
	ProviderName string          `json:"providerName"`
	HasHandler   bool            `json:"hasHandler"`
	ModelConfig  json.RawMessage `json:"modelConfig"`
}

type scriptAIProvider struct {
	api     string
	runtime *scriptRuntime
}

func (p *scriptAIProvider) API() string { return p.api }

func (p *scriptAIProvider) Complete(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) (ai.ChatResponse, error) {
	if p == nil || p.runtime == nil {
		return ai.ChatResponse{}, errors.New("script provider runtime is nil")
	}
	return p.runtime.ProviderCall(ctx, p.api, "complete", false, false, req)
}

func (p *scriptAIProvider) Stream(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.stream(ctx, "stream", false, req)
}

func (p *scriptAIProvider) StreamSimple(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.stream(ctx, "streamSimple", true, req)
}

func (p *scriptAIProvider) stream(ctx context.Context, method string, simple bool, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	if ctx == nil {
		ctx = context.Background()
	}
	stream := ai.NewAssistantMessageEventStreamWithContext(ctx, 8)
	go func() {
		message := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "error")
		defer func() {
			if recovered := recover(); recovered != nil {
				message.StopReason = "error"
				message.ErrorMessage = fmt.Sprint(recovered)
				pushScriptProviderError(stream, message)
			}
			stream.End(message)
		}()
		if p == nil || p.runtime == nil {
			message.ErrorMessage = "script provider runtime is nil"
			pushScriptProviderError(stream, message)
			return
		}

		// emit maps each token-level provider_chunk onto an ai stream event. The
		// leading "start" is pushed exactly once (on the first chunk, or below if
		// the provider streamed nothing) so the terminal "done" never re-emits it.
		acc := &scriptStreamAccumulator{model: req.Model}
		started := false
		emit := func(ev scriptProviderChunkEvent) {
			if !started {
				started = true
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: acc.partial()})
			}
			if out, ok := acc.apply(ev); ok {
				stream.Push(out)
			}
		}
		payload := map[string]any{
			"type":    "provider_call",
			"api":     p.api,
			"method":  method,
			"simple":  simple,
			"stream":  true,
			"request": scriptProviderRequestFromChatRequest(req),
		}
		response, err := p.runtime.ProviderStream(ctx, payload, emit)
		if err != nil {
			message = ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "error")
			if len(response.Result) > 0 && string(response.Result) != "null" {
				var result scriptProviderResult
				if json.Unmarshal(response.Result, &result) == nil {
					if built, _ := scriptChatResponseFromResult(result, req.Model); built.Message.Role != "" {
						message = built.Message
					}
				}
			}
			if message.StopReason != "aborted" {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					message.StopReason = "aborted"
				} else {
					message.StopReason = "error"
				}
			}
			message.ErrorMessage = firstNonEmpty(message.ErrorMessage, err.Error())
			if !started {
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: emptyPartialMessage(message)})
			}
			pushScriptProviderError(stream, message)
			return
		}

		// Authoritative final message from the integer-id reply.
		if len(response.Result) == 0 || string(response.Result) == "null" {
			message = ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "stop")
		} else {
			var result scriptProviderResult
			if uerr := json.Unmarshal(response.Result, &result); uerr != nil {
				message = ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "error")
				message.ErrorMessage = uerr.Error()
				if !started {
					stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: emptyPartialMessage(message)})
				}
				pushScriptProviderError(stream, message)
				return
			}
			built, _ := scriptChatResponseFromResult(result, req.Model)
			message = built.Message
		}
		if message.StopReason == "" {
			message.StopReason = "stop"
		}
		if !started {
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: emptyPartialMessage(message)})
		}
		if message.StopReason == "error" || message.StopReason == "aborted" {
			// start already emitted; push only the terminal error.
			pushScriptProviderError(stream, message)
			return
		}
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: message.StopReason, Partial: message, Message: message})
	}()
	return stream
}

// emptyPartialMessage clones message with empty content for the leading "start"
// event's Partial (mirrors pushScriptProviderMessage's empty-content start).
func emptyPartialMessage(message ai.AssistantMessage) ai.AssistantMessage {
	partial := message
	partial.Content = []ai.ContentBlock{}
	return partial
}

// scriptStreamAccumulator builds the running Partial message for incremental
// provider stream events. The final message comes from the authoritative reply,
// so the Partial only needs to be a faithful running approximation.
type scriptStreamAccumulator struct {
	model  ai.Model
	blocks map[int]*ai.ContentBlock
	order  []int
}

func (a *scriptStreamAccumulator) block(idx int, typ string) *ai.ContentBlock {
	if a.blocks == nil {
		a.blocks = map[int]*ai.ContentBlock{}
	}
	b := a.blocks[idx]
	if b == nil {
		b = &ai.ContentBlock{Type: typ}
		a.blocks[idx] = b
		a.order = append(a.order, idx)
		sort.Ints(a.order)
	}
	return b
}

func (a *scriptStreamAccumulator) partial() ai.AssistantMessage {
	content := make([]ai.ContentBlock, 0, len(a.order))
	for _, idx := range a.order {
		if b := a.blocks[idx]; b != nil {
			content = append(content, *b)
		}
	}
	return ai.NewAssistantMessageForModel(a.model, content, ai.Usage{}, "")
}

// apply folds a chunk event into the accumulator and returns the ai event to push
// (false to drop unknown event types).
func (a *scriptStreamAccumulator) apply(ev scriptProviderChunkEvent) (ai.AssistantMessageEvent, bool) {
	idx := 0
	if ev.ContentIndex != nil {
		idx = *ev.ContentIndex
	}
	switch ev.Type {
	case "text_start":
		a.block(idx, "text")
	case "text_delta":
		a.block(idx, "text").Text += ev.Delta
	case "text_end":
		if ev.Content != "" {
			a.block(idx, "text").Text = ev.Content
		}
	case "thinking_start":
		a.block(idx, "thinking")
	case "thinking_delta":
		a.block(idx, "thinking").Thinking += ev.Delta
	case "thinking_end":
		if ev.Content != "" {
			a.block(idx, "thinking").Thinking = ev.Content
		}
	case "toolcall_start":
		a.block(idx, "toolCall")
	case "toolcall_delta":
		a.block(idx, "toolCall")
	case "toolcall_end":
		b := a.block(idx, "toolCall")
		if ev.ToolCall != nil {
			b.ID = ev.ToolCall.ID
			b.Name = ev.ToolCall.Name
			b.Arguments = ev.ToolCall.Arguments
		}
	default:
		return ai.AssistantMessageEvent{}, false
	}
	out := ai.AssistantMessageEvent{
		Type:         ev.Type,
		ContentIndex: idx,
		Delta:        ev.Delta,
		Content:      ev.Content,
		ToolCall:     ev.ToolCall,
		Partial:      a.partial(),
	}
	return out, true
}

type scriptProviderRequest struct {
	Model           ai.Model                `json:"model"`
	SystemPrompt    string                  `json:"systemPrompt,omitempty"`
	Messages        []scriptProviderMessage `json:"messages,omitempty"`
	Tools           []map[string]any        `json:"tools,omitempty"`
	ThinkingLevel   ai.ThinkingLevel        `json:"thinkingLevel,omitempty"`
	CacheRetention  string                  `json:"cacheRetention,omitempty"`
	MaxTokens       int                     `json:"maxTokens,omitempty"`
	Temperature     *float64                `json:"temperature,omitempty"`
	Headers         map[string]string       `json:"headers,omitempty"`
	Transport       string                  `json:"transport,omitempty"`
	TimeoutMs       int                     `json:"timeoutMs,omitempty"`
	IdleTimeoutMs   int                     `json:"idleTimeoutMs,omitempty"`
	MaxRetries      int                     `json:"maxRetries,omitempty"`
	MaxRetryDelayMs int                     `json:"maxRetryDelayMs,omitempty"`
	ToolChoice      any                     `json:"toolChoice,omitempty"`
	RequestMetadata map[string]string       `json:"requestMetadata,omitempty"`
	Metadata        map[string]any          `json:"metadata,omitempty"`
	ThinkingBudgets ai.ThinkingBudgets      `json:"thinkingBudgets,omitempty"`
	SessionID       string                  `json:"sessionId,omitempty"`
}

type scriptProviderMessage struct {
	Role          string            `json:"role"`
	Content       []ai.ContentBlock `json:"content,omitempty"`
	TimestampMs   int64             `json:"timestamp,omitempty"`
	ToolCallID    string            `json:"toolCallId,omitempty"`
	ToolName      string            `json:"toolName,omitempty"`
	IsError       bool              `json:"isError,omitempty"`
	API           string            `json:"api,omitempty"`
	Provider      string            `json:"provider,omitempty"`
	Model         string            `json:"model,omitempty"`
	ResponseID    string            `json:"responseId,omitempty"`
	ResponseModel string            `json:"responseModel,omitempty"`
	Usage         ai.Usage          `json:"usage,omitempty"`
	StopReason    string            `json:"stopReason,omitempty"`
	ErrorMessage  string            `json:"errorMessage,omitempty"`
}

// scriptProviderChunk is an out-of-band token-level streaming event from the Node
// provider bridge, distinct from the integer-id final reply. event maps onto an
// ai.AssistantMessageEvent on the Go side.
type scriptProviderChunk struct {
	CallID int64                    `json:"callId"`
	Event  scriptProviderChunkEvent `json:"event"`
}

type scriptProviderChunkEvent struct {
	Type         string       `json:"type"`
	ContentIndex *int         `json:"contentIndex,omitempty"`
	Delta        string       `json:"delta,omitempty"`
	Content      string       `json:"content,omitempty"`
	ToolCall     *ai.ToolCall `json:"toolCall,omitempty"`
}

type scriptProviderResult struct {
	Content       []ai.ContentBlock               `json:"content"`
	Usage         ai.Usage                        `json:"usage"`
	StopReason    string                          `json:"stopReason"`
	ErrorMessage  string                          `json:"errorMessage"`
	ResponseID    string                          `json:"responseId"`
	ResponseModel string                          `json:"responseModel"`
	Diagnostics   []ai.AssistantMessageDiagnostic `json:"diagnostics"`
	ToolCalls     []ai.ToolCall                   `json:"toolCalls"`
}

func (r *scriptRuntime) ProviderCall(ctx context.Context, apiID, method string, simple, stream bool, req ai.ChatRequest) (ai.ChatResponse, error) {
	apiID = strings.TrimSpace(apiID)
	if apiID == "" {
		return ai.ChatResponse{}, errors.New("script provider api is empty")
	}
	response, err := r.request(ctx, map[string]any{
		"type":    "provider_call",
		"api":     apiID,
		"method":  method,
		"simple":  simple,
		"stream":  stream,
		"request": scriptProviderRequestFromChatRequest(req),
	})
	if err != nil {
		return ai.ChatResponse{}, err
	}
	if len(response.Result) == 0 || string(response.Result) == "null" {
		message := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "stop")
		return ai.ChatResponse{Message: message}, nil
	}
	var result scriptProviderResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return ai.ChatResponse{}, fmt.Errorf("%s: invalid provider result for %s: %w", r.path, apiID, err)
	}
	return scriptChatResponseFromResult(result, req.Model)
}

// scriptChatResponseFromResult builds the authoritative ai.ChatResponse from a
// provider's final result. Shared by the blocking ProviderCall path and the
// streaming path so token-level streaming yields the identical final message,
// usage, stop reason, and tool calls as the collect-to-final behavior.
func scriptChatResponseFromResult(result scriptProviderResult, model ai.Model) (ai.ChatResponse, error) {
	stopReason := result.StopReason
	if stopReason == "" {
		stopReason = "stop"
	}
	usage := result.Usage
	usage.Cost = ai.CalculateCost(model, usage)
	message := ai.NewAssistantMessageForModel(model, result.Content, usage, stopReason)
	message.ErrorMessage = result.ErrorMessage
	message.ResponseID = result.ResponseID
	message.ResponseModel = result.ResponseModel
	message.Diagnostics = append(message.Diagnostics, result.Diagnostics...)
	responseOut := ai.ChatResponse{Message: message, ToolCalls: result.ToolCalls}
	if len(responseOut.ToolCalls) == 0 {
		responseOut.ToolCalls = scriptToolCallsFromBlocks(result.Content)
	}
	if stopReason == "error" || stopReason == "aborted" {
		if result.ErrorMessage != "" {
			return responseOut, errors.New(result.ErrorMessage)
		}
		return responseOut, errors.New(stopReason)
	}
	return responseOut, nil
}

func scriptProviderRequestFromChatRequest(req ai.ChatRequest) scriptProviderRequest {
	return scriptProviderRequest{
		Model:           req.Model,
		SystemPrompt:    req.SystemPrompt,
		Messages:        scriptProviderMessages(ai.TransformMessagesForProvider(req.Messages, req.Model)),
		Tools:           ai.ToolDefinitions(req.Tools),
		ThinkingLevel:   req.ThinkingLevel,
		CacheRetention:  req.CacheRetention,
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		Headers:         req.Headers,
		Transport:       req.Transport,
		TimeoutMs:       req.TimeoutMs,
		IdleTimeoutMs:   req.IdleTimeoutMs,
		MaxRetries:      req.MaxRetries,
		MaxRetryDelayMs: req.MaxRetryDelayMs,
		ToolChoice:      req.ToolChoice,
		RequestMetadata: req.RequestMetadata,
		Metadata:        req.Metadata,
		ThinkingBudgets: req.ThinkingBudgets,
		SessionID:       req.SessionID,
	}
}

func scriptProviderMessages(messages []ai.Message) []scriptProviderMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]scriptProviderMessage, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		role := ai.MessageRole(message)
		switch role {
		case "user", "assistant", "toolResult":
		default:
			role = "user"
		}
		item := scriptProviderMessage{
			Role:        role,
			Content:     ai.MessageBlocks(message),
			TimestampMs: ai.MessageTimestamp(message),
		}
		if assistant, ok := ai.AsAssistantMessage(message); ok {
			item.API = assistant.API
			item.Provider = assistant.Provider
			item.Model = assistant.Model
			item.ResponseID = assistant.ResponseID
			item.ResponseModel = assistant.ResponseModel
			item.Usage = assistant.Usage
			item.StopReason = assistant.StopReason
			item.ErrorMessage = assistant.ErrorMessage
		}
		if role == "toolResult" {
			item.ToolCallID = ai.MessageToolCallID(message)
			item.ToolName = ai.MessageToolName(message)
			item.IsError = ai.MessageIsError(message)
		}
		out = append(out, item)
	}
	return out
}

func scriptToolCallsFromBlocks(blocks []ai.ContentBlock) []ai.ToolCall {
	out := make([]ai.ToolCall, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "toolCall" {
			continue
		}
		out = append(out, ai.ToolCall{
			Type:             firstNonEmpty(block.Type, "toolCall"),
			ID:               block.ID,
			Name:             block.Name,
			Arguments:        cloneRawJSON(block.Arguments),
			ThoughtSignature: firstNonEmpty(block.ThoughtSignature, block.ThinkingSignature),
		})
	}
	return out
}

func cloneRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func pushScriptProviderError(stream *ai.AssistantMessageEventStream, message ai.AssistantMessage) {
	if message.StopReason != "aborted" {
		message.StopReason = "error"
	}
	stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: message.StopReason, Partial: message, Error: message})
}
