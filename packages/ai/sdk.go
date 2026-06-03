package ai

import (
	"context"
	"encoding/json"
	"sync"
)

type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

func (c *Context) UnmarshalJSON(data []byte) error {
	var raw struct {
		SystemPrompt string            `json:"systemPrompt,omitempty"`
		Messages     []json.RawMessage `json:"messages"`
		Tools        []Tool            `json:"tools,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	messages := make([]Message, 0, len(raw.Messages))
	for _, rawMessage := range raw.Messages {
		message, err := UnmarshalMessageJSON(rawMessage)
		if err != nil {
			return err
		}
		messages = append(messages, message)
	}
	c.SystemPrompt = raw.SystemPrompt
	c.Messages = messages
	c.Tools = raw.Tools
	return nil
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type ProviderResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

type ThinkingBudgets struct {
	Minimal int `json:"minimal,omitempty"`
	Low     int `json:"low,omitempty"`
	Medium  int `json:"medium,omitempty"`
	High    int `json:"high,omitempty"`
}

type StreamOptions struct {
	APIKey          string                                         `json:"apiKey,omitempty"`
	Headers         map[string]string                              `json:"headers,omitempty"`
	Reasoning       ThinkingLevel                                  `json:"reasoning,omitempty"`
	MaxTokens       int                                            `json:"maxTokens,omitempty"`
	Temperature     *float64                                       `json:"temperature,omitempty"`
	Transport       string                                         `json:"transport,omitempty"`
	CacheRetention  string                                         `json:"cacheRetention,omitempty"`
	SessionID       string                                         `json:"sessionId,omitempty"`
	OnPayload       func(payload any, model Model) (any, error)    `json:"-"`
	OnResponse      func(resp ProviderResponse, model Model) error `json:"-"`
	TimeoutMs       int                                            `json:"timeoutMs,omitempty"`
	IdleTimeoutMs   int                                            `json:"idleTimeoutMs,omitempty"`
	MaxRetries      int                                            `json:"maxRetries,omitempty"`
	MaxRetryDelayMs int                                            `json:"maxRetryDelayMs,omitempty"`
	ToolChoice      any                                            `json:"toolChoice,omitempty"`
	RequestMetadata map[string]string                              `json:"requestMetadata,omitempty"`
	Metadata        map[string]any                                 `json:"metadata,omitempty"`
	ThinkingBudgets ThinkingBudgets                                `json:"thinkingBudgets,omitempty"`
}

type SimpleStreamOptions StreamOptions

type AssistantMessageEvent struct {
	Type         string           `json:"type"`
	ContentIndex int              `json:"contentIndex"`
	Delta        string           `json:"delta,omitempty"`
	Content      string           `json:"content,omitempty"`
	ToolCall     *ToolCall        `json:"toolCall,omitempty"`
	Reason       string           `json:"reason,omitempty"`
	Partial      AssistantMessage `json:"partial"`
	Message      AssistantMessage `json:"message,omitempty"`
	Error        AssistantMessage `json:"error,omitempty"`
}

func (e AssistantMessageEvent) MarshalJSON() ([]byte, error) {
	// The emitted shape mirrors the upstream AssistantMessageEvent union exactly.
	// In particular `*_end` events always carry `content` and `*_delta` events
	// always carry `delta` (both typed `string` upstream), so they must not be
	// dropped when empty; `done`/`error` carry `message`/`error` and no `partial`.
	out := map[string]any{"type": e.Type}
	switch e.Type {
	case "done":
		out["reason"] = e.Reason
		out["message"] = e.finalMessage()
	case "error":
		out["reason"] = e.Reason
		out["error"] = e.finalMessage()
	case "start":
		out["partial"] = e.Partial
	case "text_start", "thinking_start", "toolcall_start":
		out["contentIndex"] = e.ContentIndex
		out["partial"] = e.Partial
	case "text_delta", "thinking_delta", "toolcall_delta":
		out["contentIndex"] = e.ContentIndex
		out["delta"] = e.Delta
		out["partial"] = e.Partial
	case "text_end", "thinking_end":
		out["contentIndex"] = e.ContentIndex
		out["content"] = e.Content
		out["partial"] = e.Partial
	case "toolcall_end":
		out["contentIndex"] = e.ContentIndex
		if e.ToolCall != nil {
			out["toolCall"] = e.ToolCall
		}
		out["partial"] = e.Partial
	default:
		if eventHasContentIndex(e.Type) {
			out["contentIndex"] = e.ContentIndex
		}
		if e.Delta != "" {
			out["delta"] = e.Delta
		}
		if e.Content != "" {
			out["content"] = e.Content
		}
		if e.ToolCall != nil {
			out["toolCall"] = e.ToolCall
		}
		out["partial"] = e.Partial
	}
	return json.Marshal(out)
}

func (e AssistantMessageEvent) finalMessage() AssistantMessage {
	if !assistantMessageIsZero(e.Message) {
		return e.Message
	}
	if !assistantMessageIsZero(e.Error) {
		return e.Error
	}
	return e.Partial
}

func eventHasContentIndex(eventType string) bool {
	switch eventType {
	case "text_start", "text_delta", "text_end",
		"thinking_start", "thinking_delta", "thinking_end",
		"toolcall_start", "toolcall_delta", "toolcall_end":
		return true
	default:
		return false
	}
}

func assistantMessageIsZero(message AssistantMessage) bool {
	return message.Role == "" &&
		len(message.Content) == 0 &&
		message.API == "" &&
		message.Provider == "" &&
		message.Model == "" &&
		message.ResponseModel == "" &&
		message.ResponseID == "" &&
		len(message.Diagnostics) == 0 &&
		message.Usage.IsZero() &&
		message.StopReason == "" &&
		message.ErrorMessage == "" &&
		message.TimestampMs == 0
}

type AssistantMessageEventStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	events chan AssistantMessageEvent
	done   chan struct{}
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []AssistantMessageEvent
	once   sync.Once
	result AssistantMessage
	latest AssistantMessage
	ended  bool
}

func NewAssistantMessageEventStream(buffer int) *AssistantMessageEventStream {
	return NewAssistantMessageEventStreamWithContext(context.Background(), buffer)
}

func NewAssistantMessageEventStreamWithContext(ctx context.Context, buffer int) *AssistantMessageEventStream {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	if buffer < 1 {
		buffer = 1
	}
	stream := &AssistantMessageEventStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan AssistantMessageEvent, buffer),
		done:   make(chan struct{}),
	}
	stream.cond = sync.NewCond(&stream.mu)
	go stream.watchContext()
	return stream
}

func (s *AssistantMessageEventStream) Events() <-chan AssistantMessageEvent {
	s.once.Do(func() {
		go s.dispatchEvents()
	})
	return s.events
}

func (s *AssistantMessageEventStream) Push(event AssistantMessageEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.latest = event.finalMessage()
	if assistantMessageIsZero(s.latest) {
		s.latest = event.Partial
	}
	s.enqueueLocked(event)
	if event.Type == "done" || event.Type == "error" {
		s.finishLocked(event.finalMessage())
	}
	s.cond.Signal()
}

func (s *AssistantMessageEventStream) End(result AssistantMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.finishLocked(result)
	s.cond.Broadcast()
}

func (s *AssistantMessageEventStream) Result() AssistantMessage {
	<-s.done
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

func (s *AssistantMessageEventStream) dispatchEvents() {
	defer close(s.events)
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.ended {
			s.cond.Wait()
		}
		if len(s.queue) == 0 && s.ended {
			s.mu.Unlock()
			return
		}
		event := s.queue[0]
		copy(s.queue, s.queue[1:])
		s.queue[len(s.queue)-1] = AssistantMessageEvent{}
		s.queue = s.queue[:len(s.queue)-1]
		s.cond.Broadcast()
		s.mu.Unlock()

		// Prefer delivering an already-dequeued event over honouring
		// cancellation: Result() cancels the context once the stream ends, and a
		// racing select could otherwise drop terminal/queued events that a
		// draining consumer is still entitled to. The ctx.Done() branch only
		// guards against a leaked goroutine when the consumer abandons the
		// stream (the send would block indefinitely).
		select {
		case s.events <- event:
			continue
		default:
		}
		select {
		case s.events <- event:
		case <-s.ctx.Done():
			s.abort(s.ctx.Err())
			return
		}
	}
}

func (s *AssistantMessageEventStream) watchContext() {
	select {
	case <-s.ctx.Done():
		s.abort(s.ctx.Err())
	case <-s.done:
	}
}

func (s *AssistantMessageEventStream) abort(err error) {
	if err == nil {
		err = context.Canceled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	message := s.latest
	if assistantMessageIsZero(message) {
		message = NewAssistantMessage("", "", "", nil, Usage{}, "aborted")
	}
	message.StopReason = "aborted"
	message.ErrorMessage = err.Error()
	event := AssistantMessageEvent{Type: "error", Reason: "aborted", Partial: message, Error: message}
	s.enqueueLocked(event)
	s.finishLocked(message)
	s.cond.Broadcast()
}

func (s *AssistantMessageEventStream) finishLocked(result AssistantMessage) {
	if s.ended {
		return
	}
	s.result = result
	s.ended = true
	close(s.done)
}

func (s *AssistantMessageEventStream) enqueueLocked(event AssistantMessageEvent) {
	// The queue is unbounded, matching the upstream TS EventStream. An earlier
	// implementation capped it at a fixed size and dropped the oldest event when
	// full, which silently lost stream events (e.g. text/toolcall deltas) under
	// load with no diagnostic. All consumers drain Events(), so the queue size is
	// bounded in practice by the size of a single assistant message.
	s.queue = append(s.queue, event)
}

func ClampThinkingLevel(model Model, level ThinkingLevel) ThinkingLevel {
	return ClampThinking(model, level)
}

func ModelsAreEqual(a, b Model) bool {
	return a.Provider == b.Provider && a.ID == b.ID
}

func CompleteSimple(ctx context.Context, model Model, llmContext Context, options SimpleStreamOptions) (AssistantMessage, error) {
	return completeWithRegistry(ctx, nil, model, llmContext, StreamOptions(options), true, publicMissingProviderError)
}

func (r *ModelRegistry) CompleteSimple(ctx context.Context, model Model, llmContext Context, options SimpleStreamOptions) (AssistantMessage, error) {
	return completeWithRegistry(ctx, r, model, llmContext, StreamOptions(options), true, publicMissingProviderError)
}

func coreToolSet(tools []Tool) ToolSet {
	return ToolsByName(tools)
}

func StreamSimple(ctx context.Context, model Model, llmContext Context, options SimpleStreamOptions) *AssistantMessageEventStream {
	return streamWithRegistry(ctx, nil, model, llmContext, StreamOptions(options), true, publicMissingProviderError)
}

func (r *ModelRegistry) StreamSimple(ctx context.Context, model Model, llmContext Context, options SimpleStreamOptions) *AssistantMessageEventStream {
	return streamWithRegistry(ctx, r, model, llmContext, StreamOptions(options), true, publicMissingProviderError)
}

func chatRequestFromOptions(model Model, llmContext Context, options StreamOptions) ChatRequest {
	return ChatRequest{
		Model:           model,
		SystemPrompt:    llmContext.SystemPrompt,
		Messages:        llmContext.Messages,
		Tools:           coreToolSet(llmContext.Tools),
		ThinkingLevel:   options.Reasoning,
		CacheRetention:  options.CacheRetention,
		SessionID:       options.SessionID,
		MaxTokens:       options.MaxTokens,
		Temperature:     options.Temperature,
		Headers:         options.Headers,
		Transport:       options.Transport,
		OnPayload:       options.OnPayload,
		OnResponse:      options.OnResponse,
		TimeoutMs:       options.TimeoutMs,
		IdleTimeoutMs:   options.IdleTimeoutMs,
		MaxRetries:      options.MaxRetries,
		MaxRetryDelayMs: options.MaxRetryDelayMs,
		ToolChoice:      options.ToolChoice,
		RequestMetadata: options.RequestMetadata,
		Metadata:        options.Metadata,
		ThinkingBudgets: options.ThinkingBudgets,
	}
}

func registryFromOptions(registry *ModelRegistry, model Model, options StreamOptions) *ModelRegistry {
	if registry != nil {
		if registry.Auth == nil {
			registry.Auth = newRuntimeAuthStorage()
		}
		return registry
	}
	auth := newRuntimeAuthStorage()
	if options.APIKey != "" {
		auth.SetRuntime(model.Provider, options.APIKey)
	}
	return &ModelRegistry{Auth: auth}
}

func newRuntimeAuthStorage() *AuthStorage {
	return &AuthStorage{
		RuntimeKey: map[string]string{},
		Data:       map[string]string{},
		Records:    map[string]json.RawMessage{},
		Types:      map[string]string{},
	}
}

func UnmarshalToolArguments(raw json.RawMessage, target any) error {
	return json.Unmarshal(raw, target)
}
