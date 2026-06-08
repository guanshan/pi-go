package ai

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

type Provider interface {
	API() string
	Stream(context.Context, *ModelRegistry, ChatRequest) *AssistantMessageEventStream
	StreamSimple(context.Context, *ModelRegistry, ChatRequest) *AssistantMessageEventStream
}

type CompletingProvider interface {
	Complete(context.Context, *ModelRegistry, ChatRequest) (ChatResponse, error)
}

type registeredProvider struct {
	api      string
	provider Provider
	sourceID string
}

const builtinProviderSourceID = "builtins"

var (
	providerMu sync.RWMutex
	providers  = map[string]registeredProvider{}
)

func init() {
	RegisterBuiltinProviders()
}

func RegisterProvider(provider Provider, sourceID ...string) {
	if provider == nil || provider.API() == "" {
		return
	}
	id := ""
	if len(sourceID) > 0 {
		id = sourceID[0]
	}
	registerProviderEntry(provider, id, false)
}

func RegisterAPIProvider(provider Provider, sourceID ...string) {
	RegisterProvider(provider, sourceID...)
}

func GetAPIProvider(api string) Provider {
	providerMu.RLock()
	defer providerMu.RUnlock()
	entry, ok := providers[api]
	if !ok {
		return nil
	}
	return entry.provider
}

func GetAPIProviders() []Provider {
	providerMu.RLock()
	defer providerMu.RUnlock()
	out := make([]Provider, 0, len(providers))
	apis := make([]string, 0, len(providers))
	for api := range providers {
		apis = append(apis, api)
	}
	sort.Strings(apis)
	for _, api := range apis {
		out = append(out, providers[api].provider)
	}
	return out
}

func UnregisterProviders(sourceID string) {
	providerMu.Lock()
	defer providerMu.Unlock()
	for api, entry := range providers {
		if entry.sourceID == sourceID {
			delete(providers, api)
		}
	}
}

func UnregisterProvider(api string, sourceID ...string) {
	if api == "" {
		return
	}
	source := ""
	if len(sourceID) > 0 {
		source = sourceID[0]
	}
	providerMu.Lock()
	defer providerMu.Unlock()
	entry, ok := providers[api]
	if !ok {
		return
	}
	if source != "" && entry.sourceID != source {
		return
	}
	delete(providers, api)
}

func ClearAPIProviders() {
	providerMu.Lock()
	defer providerMu.Unlock()
	providers = map[string]registeredProvider{}
}

func ResetAPIProviders() {
	ClearAPIProviders()
	RegisterBuiltinProviders()
}

func RegisterChatAPIProvider(provider Provider, sourceID ...string) {
	RegisterProvider(provider, sourceID...)
}

func registerBuiltinProvider(provider Provider) {
	if provider == nil || provider.API() == "" {
		return
	}
	registerProviderEntry(provider, builtinProviderSourceID, true)
}

func registerProviderEntry(provider Provider, sourceID string, preserveCustom bool) {
	api := provider.API()
	if api == "" {
		return
	}
	providerMu.Lock()
	defer providerMu.Unlock()
	if preserveCustom {
		if existing, ok := providers[api]; ok && existing.sourceID != builtinProviderSourceID {
			return
		}
	}
	providers[api] = registeredProvider{api: api, provider: provider, sourceID: sourceID}
}

func resolveProviderEntry(api string) (registeredProvider, bool) {
	providerMu.RLock()
	defer providerMu.RUnlock()
	entry, ok := providers[api]
	return entry, ok
}

func ResolveAPIProvider(api string) (Provider, bool) {
	entry, ok := resolveProviderEntry(api)
	if !ok {
		return nil, false
	}
	return entry.provider, true
}

func ResolveChatAPIProvider(api string) (Provider, bool) {
	return ResolveAPIProvider(api)
}

func Complete(ctx context.Context, model Model, llmContext Context, options StreamOptions) (AssistantMessage, error) {
	return completeWithRegistry(ctx, nil, model, llmContext, options, false, publicMissingProviderError)
}

func (r *ModelRegistry) Complete(ctx context.Context, model Model, llmContext Context, options StreamOptions) (AssistantMessage, error) {
	return completeWithRegistry(ctx, r, model, llmContext, options, false, publicMissingProviderError)
}

func Stream(ctx context.Context, model Model, llmContext Context, options StreamOptions) *AssistantMessageEventStream {
	return streamWithRegistry(ctx, nil, model, llmContext, options, false, publicMissingProviderError)
}

func (r *ModelRegistry) Stream(ctx context.Context, model Model, llmContext Context, options StreamOptions) *AssistantMessageEventStream {
	return streamWithRegistry(ctx, r, model, llmContext, options, false, publicMissingProviderError)
}

func assistantMessageError(message AssistantMessage) error {
	if message.StopReason != "error" && message.StopReason != "aborted" {
		return nil
	}
	if message.ErrorMessage == "" {
		return errors.New(message.StopReason)
	}
	return errors.New(message.ErrorMessage)
}

func completeWithRegistry(ctx context.Context, registry *ModelRegistry, model Model, llmContext Context, options StreamOptions, simple bool, missingProvider func(string) error) (AssistantMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	registry = registryFromOptions(registry, model, options)
	if options.APIKey != "" && registry.Auth != nil {
		registry.Auth.SetRuntime(model.Provider, options.APIKey)
	}
	req, err := prepareChatRequest(chatRequestFromOptions(model, llmContext, options))
	if err != nil {
		return AssistantMessage{}, err
	}
	entry, ok := resolveProviderEntry(req.Model.API)
	if !ok {
		return AssistantMessage{}, missingProvider(req.Model.API)
	}
	if simple {
		response, err := responseFromStream(entry.provider.StreamSimple(ctx, registry, req))
		if err != nil {
			return response.Message, err
		}
		return response.Message, assistantMessageError(response.Message)
	}
	response, err := responseFromStream(entry.provider.Stream(ctx, registry, req))
	if err != nil {
		return response.Message, err
	}
	return response.Message, assistantMessageError(response.Message)
}

func streamWithRegistry(ctx context.Context, registry *ModelRegistry, model Model, llmContext Context, options StreamOptions, simple bool, missingProvider func(string) error) *AssistantMessageEventStream {
	if ctx == nil {
		ctx = context.Background()
	}
	registry = registryFromOptions(registry, model, options)
	if options.APIKey != "" && registry.Auth != nil {
		registry.Auth.SetRuntime(model.Provider, options.APIKey)
	}
	req, err := prepareChatRequest(chatRequestFromOptions(model, llmContext, options))
	if err != nil {
		return streamChatError(model, err)
	}
	entry, ok := resolveProviderEntry(req.Model.API)
	if !ok {
		return streamChatError(req.Model, missingProvider(req.Model.API))
	}
	if simple {
		return entry.provider.StreamSimple(ctx, registry, req)
	}
	return entry.provider.Stream(ctx, registry, req)
}

func publicMissingProviderError(api string) error {
	return fmt.Errorf("no API provider registered for api: %s", api)
}

func registryMissingProviderError(api string) error {
	return fmt.Errorf("provider api %q is not registered", api)
}

func responseFromStream(stream *AssistantMessageEventStream) (ChatResponse, error) {
	if stream == nil {
		return ChatResponse{}, errors.New("provider returned nil stream")
	}
	for range stream.Events() {
	}
	message := stream.Result()
	return ChatResponse{Message: message, ToolCalls: toolCallsFromMessage(message)}, assistantMessageError(message)
}

func toolCallsFromMessage(message AssistantMessage) []ToolCall {
	blocks := MessageBlocks(message)
	out := make([]ToolCall, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "toolCall" {
			out = append(out, toolCallFromBlock(block))
		}
	}
	return out
}
