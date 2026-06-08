package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

const (
	UpstreamVersion = "0.78.0"
	Version         = UpstreamVersion + "-go"
)

// errOAuthRefreshUnavailable signals that no OAuth provider/refresh path is
// available, so the caller should skip the provider rather than surface an error
// (matching the TypeScript behaviour of returning no API key).
var errOAuthRefreshUnavailable = errors.New("oauth refresh unavailable")

type ModelRegistry struct {
	mu     sync.RWMutex
	Models []Model
	Auth   *AuthStorage
}

type ChatRequest struct {
	Model           Model
	SystemPrompt    string
	Messages        []Message
	Tools           ToolSet
	ThinkingLevel   ThinkingLevel
	CacheRetention  string
	SessionID       string
	MaxTokens       int
	Temperature     *float64
	Headers         map[string]string
	Transport       string
	OnPayload       func(payload any, model Model) (any, error)
	OnResponse      func(resp ProviderResponse, model Model) error
	TimeoutMs       int
	IdleTimeoutMs   int
	MaxRetries      int
	MaxRetryDelayMs int
	ToolChoice      any
	RequestMetadata map[string]string
	Metadata        map[string]any
	ThinkingBudgets ThinkingBudgets
}

type ChatResponse struct {
	Message   AssistantMessage
	ToolCalls []ToolCall
}

type InitialModelOptions struct {
	Provider        string
	Model           string
	Models          []string
	DefaultProvider string
	DefaultModel    string
	EnabledModels   []string
}

func NewModelRegistry(agentDir string, auth *AuthStorage) *ModelRegistry {
	if auth == nil {
		auth = NewAuthStorage(agentDir)
	}
	models := LoadModels(agentDir)
	models = ApplyOAuthModelModifiers(models, auth)
	return &ModelRegistry{Auth: auth, Models: models}
}

func (r *ModelRegistry) ModelsSnapshot() []Model {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Model(nil), r.Models...)
}

func (r *ModelRegistry) ReplaceModels(models []Model) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Models = append([]Model(nil), models...)
}

func (r *ModelRegistry) MutateModels(update func([]Model) []Model) {
	if r == nil || update == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Models = update(r.Models)
}

func (r *ModelRegistry) Find(provider, id string) (Model, bool) {
	return Find(r.ModelsSnapshot(), provider, id)
}

func (r *ModelRegistry) Match(provider, pattern string) (Model, bool, string) {
	return Match(r.ModelsSnapshot(), provider, pattern)
}

func (r *ModelRegistry) InitialModel(options InitialModelOptions) (Model, bool, string) {
	if options.Model != "" {
		model, ok, warning := r.Match(options.Provider, options.Model)
		return model, ok, warning
	}
	if options.DefaultProvider != "" && options.DefaultModel != "" {
		if model, ok := r.Find(options.DefaultProvider, options.DefaultModel); ok {
			return model, true, ""
		}
	}
	patterns := options.Models
	if len(patterns) == 0 {
		patterns = options.EnabledModels
	}
	for _, pattern := range patterns {
		if model, ok, _ := r.Match("", pattern); ok {
			return model, true, ""
		}
	}
	for _, m := range r.ModelsSnapshot() {
		if m.Provider != "faux" && r.HasAuth(m) {
			return m, true, ""
		}
	}
	return Model{}, false, "No models available"
}

func (r *ModelRegistry) AvailableConfigured() []Model {
	var out []Model
	for _, m := range r.ModelsSnapshot() {
		if r.HasAuth(m) || m.Provider == "faux" {
			out = append(out, m)
		}
	}
	return out
}

func (r *ModelRegistry) HasAuth(model Model) bool {
	if model.Provider == "faux" {
		return true
	}
	if model.Provider == "amazon-bedrock" {
		return bedrockHasAuth(r.Auth, model)
	}
	if model.Provider == "google-vertex" {
		return r.modelAPIKey(model) != "" || aiproviders.HasGoogleVertexADC()
	}
	if IsCloudflareProvider(model.Provider) {
		return r.modelAPIKey(model) != "" && HasCloudflareRequiredEnv(model.Provider)
	}
	return r.modelAPIKey(model) != ""
}

func (r *ModelRegistry) modelAPIKey(model Model) string {
	if r != nil && r.Auth != nil {
		if key := r.Auth.APIKey(model); key != "" {
			return key
		}
	}
	return model.APIKey
}

func (r *ModelRegistry) APIKey(ctx context.Context, model Model) (string, error) {
	if r == nil || r.Auth == nil {
		return "", nil
	}
	if key := r.Auth.RuntimeKey[model.Provider]; key != "" {
		return key, nil
	}
	if raw, ok := r.Auth.Records[model.Provider]; ok {
		var meta struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &meta); err == nil && meta.Type == "oauth" {
			var credentials OAuthCredentials
			if err := json.Unmarshal(raw, &credentials); err != nil {
				return "", err
			}
			providerID := OAuthProviderID(model.Provider)
			provider, hasProvider := GetOAuthProvider(providerID)
			if credentials.Expired(time.Now()) {
				refreshed, ok, err := r.Auth.RefreshOAuthCredentials(model.Provider, func(current OAuthCredentials) (OAuthCredentials, error) {
					result, err := GetOAuthAPIKey(ctx, providerID, map[OAuthProviderID]OAuthCredentials{providerID: current})
					if err != nil {
						return OAuthCredentials{}, err
					}
					if result == nil {
						return OAuthCredentials{}, errOAuthRefreshUnavailable
					}
					return result.NewCredentials, nil
				})
				if err != nil {
					if errors.Is(err, errOAuthRefreshUnavailable) {
						return "", nil
					}
					return "", err
				}
				if !ok {
					return "", nil
				}
				if hasProvider {
					return provider.GetAPIKey(refreshed), nil
				}
				return refreshed.Access, nil
			}
			if hasProvider {
				return provider.GetAPIKey(credentials), nil
			}
			return credentials.Access, nil
		}
	}
	if key := r.Auth.APIKey(model); key != "" {
		return key, nil
	}
	return model.APIKey, nil
}

func (r *ModelRegistry) List(search string) []Model {
	return List(r.ModelsSnapshot(), search)
}

func (r *ModelRegistry) StreamlessChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	prepared, err := prepareChatRequest(req)
	if err != nil {
		return ChatResponse{}, err
	}
	entry, ok := resolveProviderEntry(prepared.Model.API)
	if !ok {
		return ChatResponse{}, registryMissingProviderError(prepared.Model.API)
	}
	if provider, ok := entry.provider.(CompletingProvider); ok {
		return provider.Complete(ctx, r, prepared)
	}
	return responseFromStream(entry.provider.Stream(ctx, r, prepared))
}

func (r *ModelRegistry) StreamChat(ctx context.Context, req ChatRequest) *AssistantMessageEventStream {
	prepared, err := prepareChatRequest(req)
	if err != nil {
		return streamChatError(req.Model, err)
	}
	entry, ok := resolveProviderEntry(prepared.Model.API)
	if !ok {
		return streamChatError(prepared.Model, registryMissingProviderError(prepared.Model.API))
	}
	return entry.provider.Stream(ctx, r, prepared)
}

func prepareChatRequest(req ChatRequest) (ChatRequest, error) {
	if req.Model.Provider == "" {
		return ChatRequest{}, errors.New("no model selected")
	}
	resolved, err := resolveProviderBaseURL(req.Model)
	if err != nil {
		return ChatRequest{}, err
	}
	req.Model.BaseURL = resolved
	return sanitizeChatRequest(req), nil
}

func streamChatResponse(ctx context.Context, complete func(context.Context) (ChatResponse, error)) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStreamWithContext(ctx, 8)
	go func() {
		message := NewAssistantMessage("", "", "", nil, Usage{}, "error")
		defer func() {
			if recovered := recover(); recovered != nil {
				message.StopReason = "error"
				message.ErrorMessage = fmt.Sprint(recovered)
				pushAssistantError(stream, message)
			}
			stream.End(message)
		}()
		response, err := complete(ctx)
		message = response.Message
		if err != nil {
			if message.Role == "" {
				message = NewAssistantMessage("", "", "", nil, Usage{}, "error")
				message.ErrorMessage = err.Error()
			}
			message.StopReason = stopReasonForError(err)
			pushAssistantError(stream, message)
			return
		}
		pushAssistantMessage(stream, message)
	}()
	return stream
}

func providerStream(ctx context.Context, model Model, buffer int, run func(*AssistantMessageEventStream) (AssistantMessage, error)) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStreamWithContext(ctx, buffer)
	go func() {
		message := NewAssistantMessageForModel(model, nil, Usage{}, "error")
		defer func() {
			if recovered := recover(); recovered != nil {
				message.StopReason = "error"
				message.ErrorMessage = fmt.Sprint(recovered)
				pushAssistantError(stream, message)
			}
			stream.End(message)
		}()
		message, _ = run(stream)
	}()
	return stream
}

func streamChatError(model Model, err error) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream(1)
	msg := NewAssistantMessageForModel(model, nil, Usage{}, "error")
	if err != nil {
		msg.ErrorMessage = err.Error()
	}
	pushAssistantError(stream, msg)
	return stream
}

func pushAssistantMessage(stream *AssistantMessageEventStream, message AssistantMessage) {
	if message.StopReason == "error" || message.StopReason == "aborted" {
		pushAssistantError(stream, message)
		return
	}
	partial := messageWithBlocks(message, nil)
	stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
	blocks := MessageBlocks(message)
	running := make([]ContentBlock, 0, len(blocks))
	for index, block := range blocks {
		running = append(running, emptyContentBlock(block))
		partial = messageWithBlocks(message, running)
		stream.Push(contentStartEvent(block.Type, index, partial))

		running[index] = block
		partial = messageWithBlocks(message, running)
		if delta := contentBlockDelta(block); delta != "" {
			stream.Push(contentDeltaEvent(block.Type, index, delta, partial))
		}
		stream.Push(contentEndEvent(block, index, partial))
	}
	stream.Push(AssistantMessageEvent{Type: "done", Reason: doneReason(message.StopReason), Partial: message, Message: message})
}

func pushAssistantError(stream *AssistantMessageEventStream, message AssistantMessage) {
	if message.StopReason != "aborted" {
		message.StopReason = "error"
	}
	stream.Push(AssistantMessageEvent{Type: "error", Reason: errorReason(message.StopReason), Partial: message, Error: message})
}

func messageWithBlocks(message AssistantMessage, blocks []ContentBlock) AssistantMessage {
	if blocks == nil {
		message.Content = []ContentBlock{}
		return message
	}
	message.Content = blocks
	return message
}

func emptyContentBlock(block ContentBlock) ContentBlock {
	switch block.Type {
	case "thinking":
		return ContentBlock{Type: "thinking", Signature: thinkingBlockSignature(block), RawItem: cloneRawMessage(block.RawItem), Redacted: block.Redacted}
	case "toolCall":
		return ContentBlock{Type: "toolCall", ID: block.ID, Name: block.Name, Arguments: jsonRawObject(), ThoughtSignature: block.ThoughtSignature}
	default:
		return ContentBlock{Type: "text", TextSignature: block.TextSignature}
	}
}

func contentBlockDelta(block ContentBlock) string {
	switch block.Type {
	case "thinking":
		return block.Thinking
	case "toolCall":
		return string(block.Arguments)
	default:
		return block.Text
	}
}

func contentStartEvent(blockType string, index int, partial AssistantMessage) AssistantMessageEvent {
	return AssistantMessageEvent{Type: contentEventType(blockType, "start"), ContentIndex: index, Partial: partial}
}

func contentDeltaEvent(blockType string, index int, delta string, partial AssistantMessage) AssistantMessageEvent {
	return AssistantMessageEvent{Type: contentEventType(blockType, "delta"), ContentIndex: index, Delta: delta, Partial: partial}
}

func contentEndEvent(block ContentBlock, index int, partial AssistantMessage) AssistantMessageEvent {
	event := AssistantMessageEvent{Type: contentEventType(block.Type, "end"), ContentIndex: index, Partial: partial}
	switch block.Type {
	case "thinking":
		event.Content = block.Thinking
	case "toolCall":
		call := toolCallFromBlock(block)
		event.ToolCall = &call
	default:
		event.Content = block.Text
	}
	return event
}

func contentEventType(blockType, suffix string) string {
	switch blockType {
	case "thinking":
		return "thinking_" + suffix
	case "toolCall":
		return "toolcall_" + suffix
	default:
		return "text_" + suffix
	}
}

func toolCallFromBlock(block ContentBlock) ToolCall {
	args := block.Arguments
	if len(args) == 0 {
		args = jsonRawObject()
	}
	return ToolCall{ID: block.ID, Name: block.Name, Arguments: args, ThoughtSignature: block.ThoughtSignature}
}

func doneReason(reason string) string {
	switch reason {
	case "length", "toolUse":
		return reason
	default:
		return "stop"
	}
}

func errorReason(reason string) string {
	if reason == "aborted" {
		return "aborted"
	}
	return "error"
}

func stopReasonForError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "aborted"
	}
	return "error"
}

type contentStreamTracker struct {
	started map[int]string
	ended   map[int]bool
}

func newContentStreamTracker() *contentStreamTracker {
	return &contentStreamTracker{started: map[int]string{}, ended: map[int]bool{}}
}

func (t *contentStreamTracker) PushDelta(stream *AssistantMessageEventStream, eventType string, index int, delta string, partial AssistantMessage) {
	if index < 0 {
		index = contentIndexForEvent(eventType, partial)
	}
	blockType := blockTypeForEvent(eventType, partial, index)
	if _, ok := t.started[index]; !ok {
		t.started[index] = blockType
		stream.Push(AssistantMessageEvent{Type: contentEventType(blockType, "start"), ContentIndex: index, Partial: partial})
	}
	stream.Push(AssistantMessageEvent{Type: eventType, ContentIndex: index, Delta: delta, Partial: partial})
}

func (t *contentStreamTracker) Finish(stream *AssistantMessageEventStream, partial AssistantMessage) {
	blocks := MessageBlocks(partial)
	indexes := make([]int, 0, len(t.started))
	for index := range t.started {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		if t.ended[index] || index < 0 || index >= len(blocks) {
			continue
		}
		stream.Push(contentEndEvent(blocks[index], index, partial))
		t.ended[index] = true
	}
}

func contentIndexForEvent(eventType string, partial AssistantMessage) int {
	want := blockTypeForEvent(eventType, partial, -1)
	blocks := MessageBlocks(partial)
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Type == want {
			return i
		}
	}
	if len(blocks) > 0 {
		return len(blocks) - 1
	}
	return 0
}

func blockTypeForEvent(eventType string, partial AssistantMessage, index int) string {
	if index >= 0 {
		blocks := MessageBlocks(partial)
		if index < len(blocks) && blocks[index].Type != "" {
			return blocks[index].Type
		}
	}
	switch eventType {
	case "thinking_delta":
		return "thinking"
	case "toolcall_delta":
		return "toolCall"
	default:
		return "text"
	}
}

func applyOnPayload(req ChatRequest, payload any) (any, error) {
	if req.OnPayload == nil {
		return payload, nil
	}
	next, err := req.OnPayload(payload, req.Model)
	if err != nil {
		return nil, err
	}
	if next == nil {
		return payload, nil
	}
	return next, nil
}

func applyOnPayloadMap(req ChatRequest, payload map[string]any) (map[string]any, error) {
	next, err := applyOnPayload(req, payload)
	if err != nil {
		return nil, err
	}
	mapped, ok := next.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OnPayload returned %T, want map[string]any", next)
	}
	return mapped, nil
}

func applyOnPayloadAs[T any](req ChatRequest, payload T) (T, error) {
	var zero T
	next, err := applyOnPayload(req, payload)
	if err != nil {
		return zero, err
	}
	typed, ok := next.(T)
	if !ok {
		return zero, fmt.Errorf("OnPayload returned %T, want %T", next, payload)
	}
	return typed, nil
}

func resolveProviderBaseURL(model Model) (string, error) {
	return ResolveCloudflareBaseURL(model)
}
