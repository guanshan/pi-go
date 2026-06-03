package ai

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// fauxAPIProvider is a deterministic test provider. Each instance carries its
// own scripted-response queue and call counter, so multiple registrations (and
// t.Parallel subtests using RegisterFauxProvider) never share state. The
// process-wide default instance (defaultFauxProvider, API "faux") backs the
// package-level Set/Append/Reset shims for serial tests. Because the struct now
// embeds a mutex, every method uses a pointer receiver.
type fauxAPIProvider struct {
	api       string
	mu        sync.Mutex
	responses []FauxResponse
	callCount int
}

// NewFauxProvider returns a fresh faux provider instance with its own scripted
// state. An empty api defaults to "faux".
func NewFauxProvider(api string) *fauxAPIProvider {
	if api == "" {
		api = "faux"
	}
	return &fauxAPIProvider{api: api}
}

// defaultFauxProvider is the process-wide instance registered as the builtin
// "faux" provider; the package-level shims drive its queue.
var defaultFauxProvider = NewFauxProvider("faux")

func registerFauxProvider() {
	registerBuiltinProvider(defaultFauxProvider)
}

func (p *fauxAPIProvider) API() string { return p.api }

func (p *fauxAPIProvider) complete(_ context.Context, _ *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.fauxChat(req), nil
}

func (p *fauxAPIProvider) Complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.complete(ctx, r, req)
}

func (p *fauxAPIProvider) Stream(ctx context.Context, _ *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return streamChatResponse(ctx, func(context.Context) (ChatResponse, error) {
		return p.complete(ctx, nil, req)
	})
}

func (p *fauxAPIProvider) StreamSimple(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

// SetResponses replaces this instance's scripted queue. Responses are consumed
// in order across successive Complete/Stream calls.
func (p *fauxAPIProvider) SetResponses(responses []FauxResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append(p.responses[:0:0], responses...)
}

// AppendResponses appends to this instance's scripted queue.
func (p *fauxAPIProvider) AppendResponses(responses []FauxResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append(p.responses, responses...)
}

// ResetResponses clears this instance's scripted queue and call counter,
// restoring the legacy echo behaviour.
func (p *fauxAPIProvider) ResetResponses() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = nil
	p.callCount = 0
}

// PendingResponseCount reports how many scripted responses remain queued on this
// instance.
func (p *fauxAPIProvider) PendingResponseCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.responses)
}

// CallCount reports how many times this instance has been invoked since the last
// ResetResponses.
func (p *fauxAPIProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

// next pops the next scripted response, reporting whether the queue was active.
// It always increments the call counter.
func (p *fauxAPIProvider) next() (FauxResponse, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	if len(p.responses) == 0 {
		return FauxResponse{}, false
	}
	next := p.responses[0]
	p.responses = p.responses[1:]
	return next, true
}

func (p *fauxAPIProvider) fauxChat(req ChatRequest) ChatResponse {
	scripted, ok := p.next()
	if ok {
		return fauxScriptedResponse(req, scripted)
	}
	return fauxEchoResponse(req)
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

// The package-level Set/Append/Reset/Pending/FauxCallCount functions are thin
// shims over the shared default faux instance (defaultFauxProvider). They exist
// for serial tests that script the builtin "faux" provider without a registry
// handle, isolated with `defer ResetFauxResponses()`. Tests needing concurrency
// safety (t.Parallel, or multiple independent scripts in one process) should use
// RegisterFauxProvider, which mints a private instance under a unique API.

// SetFauxResponses replaces the default faux instance's scripted response queue.
// Responses are consumed in order across successive Complete/Stream calls.
// Passing nil (or calling ResetFauxResponses) restores the legacy echo behaviour.
func SetFauxResponses(responses []FauxResponse) { defaultFauxProvider.SetResponses(responses) }

// AppendFauxResponses appends to the default faux instance's scripted queue.
func AppendFauxResponses(responses []FauxResponse) { defaultFauxProvider.AppendResponses(responses) }

// ResetFauxResponses clears the default faux instance's queue and call counter,
// restoring the legacy echo behaviour. Tests should defer this for isolation.
func ResetFauxResponses() { defaultFauxProvider.ResetResponses() }

// PendingFauxResponseCount reports how many scripted responses remain queued on
// the default faux instance.
func PendingFauxResponseCount() int { return defaultFauxProvider.PendingResponseCount() }

// FauxCallCount reports how many times the default faux instance has been
// invoked since the last ResetFauxResponses.
func FauxCallCount() int { return defaultFauxProvider.CallCount() }

// fauxInstanceCounter mints unique API ids for per-instance faux registrations.
var fauxInstanceCounter atomic.Uint64

// FauxRegistration is a handle to a per-instance faux provider registered under
// a unique API id. Call Unregister (e.g. via t.Cleanup) to remove it.
type FauxRegistration struct {
	// Provider is the per-instance faux provider; script it via its
	// SetResponses/AppendResponses/ResetResponses methods.
	Provider *fauxAPIProvider
	// Model resolves to this instance (API is the unique id) while keeping
	// Provider "faux" so auth/availability gates treat it as always-available.
	Model    Model
	sourceID string
}

// Unregister removes the per-instance faux provider from the global registry.
func (r *FauxRegistration) Unregister() {
	if r == nil {
		return
	}
	UnregisterProviders(r.sourceID)
}

// RegisterFauxProvider registers a fresh faux provider instance under a unique
// API id so multiple instances — including t.Parallel subtests — can script
// independent responses without crosstalk. The returned Model keeps Provider
// "faux" (so HasAuth/AvailableConfigured/InitialModel still treat it as the
// always-available test provider) but carries the unique API for dispatch,
// mirroring the TS registerFauxProvider per-registration state. Optional initial
// responses may be supplied; further scripting goes through reg.Provider.
func RegisterFauxProvider(responses ...FauxResponse) *FauxRegistration {
	n := fauxInstanceCounter.Add(1)
	suffix := strconv.FormatUint(n, 10)
	api := "faux-" + suffix
	sourceID := "faux-instance-" + suffix
	p := NewFauxProvider(api)
	if len(responses) > 0 {
		p.SetResponses(responses)
	}
	RegisterProvider(p, sourceID)
	model := Model{
		Provider:       "faux",
		ID:             api,
		Name:           "Faux deterministic test model (" + api + ")",
		API:            api,
		Input:          []string{"text"},
		ThinkingLevels: []ThinkingLevel{ThinkingOff},
	}
	return &FauxRegistration{Provider: p, Model: model, sourceID: sourceID}
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
