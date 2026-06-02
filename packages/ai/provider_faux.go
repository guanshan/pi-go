package ai

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

type fauxAPIProvider struct{}

func registerFauxProvider() {
	registerBuiltinProvider(fauxAPIProvider{})
}

func (fauxAPIProvider) API() string { return "faux" }

func (fauxAPIProvider) complete(_ context.Context, _ *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return fauxChat(req), nil
}

func (p fauxAPIProvider) Complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.complete(ctx, r, req)
}

func (p fauxAPIProvider) Stream(ctx context.Context, _ *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return streamChatResponse(ctx, func(context.Context) (ChatResponse, error) {
		return p.complete(ctx, nil, req)
	})
}

func (p fauxAPIProvider) StreamSimple(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

// FauxResponse scripts a single faux-provider turn. It can express assistant
// text/thinking/toolCall content, usage, a stop reason, and an error. When no
// responses are scripted the provider falls back to the legacy echo behaviour.
type FauxResponse struct {
	// Content is the assistant content blocks to emit (text/thinking/toolCall).
	Content []ContentBlock
	// Usage, when non-zero, overrides the estimated usage.
	Usage Usage
	// StopReason overrides the default stop reason ("stop", or "toolUse" when a
	// tool call is present).
	StopReason string
	// ErrorMessage, when set (or when StopReason is "error"/"aborted"), surfaces
	// as a provider error / terminal error event.
	ErrorMessage string
}

// FauxText builds a text content block for a FauxResponse.
func FauxText(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// FauxThinking builds a thinking content block for a FauxResponse.
func FauxThinking(thinking string) ContentBlock {
	return ContentBlock{Type: "thinking", Thinking: thinking}
}

// FauxToolCall builds a toolCall content block for a FauxResponse. The id may be
// empty; arguments are encoded to JSON (a nil/empty value yields "{}").
func FauxToolCall(id, name string, arguments any) ContentBlock {
	return ContentBlock{Type: "toolCall", ID: id, Name: name, Arguments: encodeFauxArguments(arguments)}
}

func encodeFauxArguments(arguments any) json.RawMessage {
	switch v := arguments.(type) {
	case nil:
		return jsonRawObject()
	case json.RawMessage:
		if len(v) == 0 {
			return jsonRawObject()
		}
		return v
	case []byte:
		if len(v) == 0 {
			return jsonRawObject()
		}
		return json.RawMessage(v)
	case string:
		if v == "" {
			return jsonRawObject()
		}
		return json.RawMessage(v)
	default:
		data, err := json.Marshal(v)
		if err != nil || len(data) == 0 {
			return jsonRawObject()
		}
		return json.RawMessage(data)
	}
}

// NewFauxText is a convenience constructor for a single-text FauxResponse.
func NewFauxText(text string) FauxResponse {
	return FauxResponse{Content: []ContentBlock{FauxText(text)}}
}

var (
	fauxMu        sync.Mutex
	fauxResponses []FauxResponse
	fauxCallCount int
)

// SetFauxResponses replaces the scripted faux-provider response queue. Responses
// are consumed in order across successive Complete/Stream calls. Passing nil (or
// calling ResetFauxResponses) restores the legacy echo behaviour.
func SetFauxResponses(responses []FauxResponse) {
	fauxMu.Lock()
	defer fauxMu.Unlock()
	fauxResponses = append(fauxResponses[:0:0], responses...)
}

// AppendFauxResponses appends to the scripted faux-provider response queue.
func AppendFauxResponses(responses []FauxResponse) {
	fauxMu.Lock()
	defer fauxMu.Unlock()
	fauxResponses = append(fauxResponses, responses...)
}

// ResetFauxResponses clears the scripted queue and resets the call counter,
// restoring the legacy echo behaviour. Tests should defer this for isolation.
func ResetFauxResponses() {
	fauxMu.Lock()
	defer fauxMu.Unlock()
	fauxResponses = nil
	fauxCallCount = 0
}

// PendingFauxResponseCount reports how many scripted responses remain queued.
func PendingFauxResponseCount() int {
	fauxMu.Lock()
	defer fauxMu.Unlock()
	return len(fauxResponses)
}

// FauxCallCount reports how many times the faux provider has been invoked since
// the last ResetFauxResponses.
func FauxCallCount() int {
	fauxMu.Lock()
	defer fauxMu.Unlock()
	return fauxCallCount
}

// nextFauxResponse pops the next scripted response, reporting whether the queue
// was active (a response was scripted). It always increments the call counter.
func nextFauxResponse() (FauxResponse, bool) {
	fauxMu.Lock()
	defer fauxMu.Unlock()
	fauxCallCount++
	if len(fauxResponses) == 0 {
		return FauxResponse{}, false
	}
	next := fauxResponses[0]
	fauxResponses = fauxResponses[1:]
	return next, true
}

func fauxChat(req ChatRequest) ChatResponse {
	scripted, ok := nextFauxResponse()
	if ok {
		return fauxScriptedResponse(req, scripted)
	}
	return fauxEchoResponse(req)
}

func fauxScriptedResponse(req ChatRequest, scripted FauxResponse) ChatResponse {
	blocks := append([]ContentBlock(nil), scripted.Content...)
	usage := scripted.Usage
	if usage.IsZero() {
		usage = fauxEstimateUsage(req, blocks)
	}
	stopReason := scripted.StopReason
	if stopReason == "" {
		stopReason = "stop"
		if hasToolCallBlock(blocks) {
			stopReason = "toolUse"
		}
	}
	msg := NewAssistantMessageForModel(req.Model, blocks, usage, stopReason)
	if scripted.ErrorMessage != "" {
		msg.ErrorMessage = scripted.ErrorMessage
		if stopReason != "error" && stopReason != "aborted" {
			msg.StopReason = "error"
		}
	}
	return ChatResponse{Message: msg, ToolCalls: toolCallsFromMessage(msg)}
}

func fauxEchoResponse(req ChatRequest) ChatResponse {
	last := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if MessageRole(req.Messages[i]) == "user" {
			last = MessageText(req.Messages[i])
			break
		}
	}
	text := "faux: " + last
	if strings.TrimSpace(last) == "" {
		text = "faux: ready"
	}
	msg := NewAssistantMessageForModel(req.Model, TextBlocks(text), Usage{Input: len(req.Messages), Output: len(strings.Fields(text)), TotalTokens: len(req.Messages) + len(strings.Fields(text))}, "stop")
	return ChatResponse{Message: msg}
}

// fauxEstimateUsage produces a deterministic, non-zero usage estimate so
// scripted responses flow non-trivial token counts through, mirroring the TS
// faux provider's character/4 estimate.
func fauxEstimateUsage(req ChatRequest, blocks []ContentBlock) Usage {
	input := estimateFauxTokens(fauxSerializePrompt(req))
	output := estimateFauxTokens(fauxAssistantText(blocks))
	return Usage{Input: input, Output: output, TotalTokens: input + output}
}

func estimateFauxTokens(text string) int {
	return (len(text) + 3) / 4
}

func fauxSerializePrompt(req ChatRequest) string {
	parts := make([]string, 0, len(req.Messages)+2)
	if req.SystemPrompt != "" {
		parts = append(parts, "system:"+req.SystemPrompt)
	}
	for _, msg := range req.Messages {
		parts = append(parts, MessageRole(msg)+":"+MessageText(msg))
	}
	return strings.Join(parts, "\n\n")
}

func fauxAssistantText(blocks []ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "thinking":
			parts = append(parts, block.Thinking)
		case "toolCall":
			parts = append(parts, block.Name+":"+string(block.Arguments))
		}
	}
	return strings.Join(parts, "\n")
}
