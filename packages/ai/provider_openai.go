package ai

import (
	"context"
	"fmt"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

type openAIChatAPIProvider struct{}

type openAIResponsesAPIProvider struct {
	api string
}

func registerOpenAIProviders() {
	registerBuiltinProvider(openAIChatAPIProvider{})
	registerBuiltinProvider(openAIResponsesAPIProvider{api: "openai-responses"})
	registerBuiltinProvider(openAIResponsesAPIProvider{api: "azure-openai-responses"})
	registerBuiltinProvider(openAIResponsesAPIProvider{api: "openai-codex-responses"})
}

func (openAIChatAPIProvider) API() string { return "openai-completions" }

func (openAIChatAPIProvider) complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return r.openAIChat(ctx, req)
}

func (p openAIChatAPIProvider) Complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.complete(ctx, r, req)
}

func (p openAIChatAPIProvider) Stream(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	if shouldStreamOpenAIChat(req.Model) {
		return r.openAIChatStream(ctx, req)
	}
	return streamChatResponse(ctx, func(ctx context.Context) (ChatResponse, error) {
		return p.complete(ctx, r, req)
	})
}

func (p openAIChatAPIProvider) StreamSimple(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

func (p openAIResponsesAPIProvider) API() string { return p.api }

func (openAIResponsesAPIProvider) complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return r.openAIResponsesChat(ctx, req)
}

func (p openAIResponsesAPIProvider) Complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.complete(ctx, r, req)
}

func (p openAIResponsesAPIProvider) Stream(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	if shouldStreamOpenAIResponses(req.Model) {
		return r.openAIResponsesChatStream(ctx, req)
	}
	return streamChatResponse(ctx, func(ctx context.Context) (ChatResponse, error) {
		return p.complete(ctx, r, req)
	})
}

func (p openAIResponsesAPIProvider) StreamSimple(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

func shouldStreamOpenAIResponses(model Model) bool {
	return model.API == "openai-responses" || model.API == "openai-codex-responses" || model.API == "azure-openai-responses"
}

func (r *ModelRegistry) prepareOpenAIChatRequest(ctx context.Context, req ChatRequest) (aiproviders.PreparedOpenAIChatRequest, error) {
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return aiproviders.PreparedOpenAIChatRequest{}, err
	}
	if key == "" {
		return aiproviders.PreparedOpenAIChatRequest{}, fmt.Errorf("no API key for provider: %s", req.Model.Provider)
	}
	compat := GetOpenAICompletionsCompat(req.Model)
	prepared := aiproviders.BuildOpenAIChatRequest(key, aiproviders.OpenAIChatRequestOptions{
		ModelID:                          req.Model.ID,
		Provider:                         req.Model.Provider,
		BaseURL:                          req.Model.BaseURL,
		SystemPrompt:                     req.SystemPrompt,
		Messages:                         openAIChatMessages(req.Messages, req.Model),
		Tools:                            ToolDefinitions(req.Tools),
		SupportsImages:                   SupportsInput(req.Model, "image"),
		ModelHeaders:                     req.Model.Headers,
		RequestHeaders:                   req.Headers,
		MaxTokens:                        req.MaxTokens,
		MaxOutput:                        req.Model.MaxOutput,
		Temperature:                      req.Temperature,
		ToolChoice:                       requestToolChoice(req),
		ThinkingLevel:                    string(req.ThinkingLevel),
		ThinkingLevelMap:                 effectiveThinkingLevelMap(req.Model),
		Reasoning:                        req.Model.Reasoning,
		CacheRetention:                   req.CacheRetention,
		SessionID:                        req.SessionID,
		MaxTokensField:                   compat.MaxTokensField,
		SupportsStore:                    compat.SupportsStore,
		SupportsDeveloperRole:            compat.SupportsDeveloperRole,
		SupportsReasoningEffort:          compat.SupportsReasoningEffort,
		CacheControlFormat:               compat.CacheControlFormat,
		SendSessionAffinityHeaders:       compat.SendSessionAffinityHeaders,
		SupportsLongCacheRetention:       boolPtr(compat.SupportsLongCacheRetention),
		RequiresToolResultName:           compat.RequiresToolResultName,
		RequiresAssistantAfterToolResult: compat.RequiresAssistantAfterToolResult,
		RequiresThinkingAsText:           compat.RequiresThinkingAsText,
		RequiresReasoningContentOnAssistantMessages: compat.RequiresReasoningContentOnAssistantMessages,
		ThinkingFormat:       compat.ThinkingFormat,
		ZaiToolStream:        compat.ZaiToolStream,
		SupportsStrictMode:   compat.SupportsStrictMode,
		OpenRouterRouting:    compat.OpenRouterRouting,
		VercelGatewayRouting: compat.VercelGatewayRouting,
	})
	body, err := applyOnPayloadMap(req, prepared.Body)
	if err != nil {
		return aiproviders.PreparedOpenAIChatRequest{}, err
	}
	prepared.Body = body
	return prepared, nil
}

func (r *ModelRegistry) openAIChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	prepared, err := r.prepareOpenAIChatRequest(ctx, req)
	if err != nil {
		return ChatResponse{}, err
	}
	if sdkResp, usedSDK, err := doOpenAIChatCompletionSDK(ctx, req, prepared.Key, prepared.Headers, prepared.Body, prepared.BearerAuth); usedSDK {
		if err != nil {
			return ChatResponse{}, err
		}
		return sdkResp, nil
	}
	raw, err := aiproviders.DoOpenAISDKJSONWithClient(ctx, aiproviders.OpenAIChatURL(req.Model.BaseURL), prepared.Key, prepared.Headers, prepared.Body, prepared.BearerAuth, providerHTTPClient(req), providerRequestOptions(req))
	if err != nil {
		return ChatResponse{}, err
	}
	parsed, err := aiproviders.ParseOpenAIChatCompletionRawForProvider(raw, req.Model.Provider)
	if err != nil {
		return ChatResponse{}, err
	}
	return openAIChatResponse(parsed, req.Model), nil
}

func openAIChatResponse(parsed aiproviders.OpenAIChatParsed, model Model) ChatResponse {
	blocks := make([]ContentBlock, 0, len(parsed.Blocks))
	for _, block := range parsed.Blocks {
		blocks = append(blocks, ContentBlock{
			Type:             block.Type,
			Text:             block.Text,
			Thinking:         block.Thinking,
			Signature:        block.ThinkingSignature,
			ID:               block.ID,
			Name:             block.Name,
			Arguments:        block.Arguments,
			ThoughtSignature: block.ThoughtSignature,
		})
	}
	calls := make([]ToolCall, 0, len(parsed.ToolCalls))
	for _, call := range parsed.ToolCalls {
		calls = append(calls, ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments, ThoughtSignature: call.ThoughtSignature})
	}
	usage := Usage{Input: parsed.Usage.Input, Output: parsed.Usage.Output, CacheRead: parsed.Usage.CacheRead, CacheWrite: parsed.Usage.CacheWrite, TotalTokens: parsed.Usage.TotalTokens}
	msg := NewAssistantMessageForModel(model, blocks, usageWithCost(model, usage), parsed.StopReason)
	msg.ErrorMessage = parsed.ErrorMessage
	msg.ResponseID = parsed.ResponseID
	if parsed.ResponseModel != "" && parsed.ResponseModel != model.ID {
		msg.ResponseModel = parsed.ResponseModel
	}
	return ChatResponse{Message: msg, ToolCalls: calls}
}

func openAIChatMessages(messages []Message, model Model) []aiproviders.OpenAIChatMessage {
	messages = transformMessages(messages, model, nil)
	out := make([]aiproviders.OpenAIChatMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, aiproviders.OpenAIChatMessage{
			Role:       MessageRole(msg),
			Text:       MessageText(msg),
			ToolCallID: MessageToolCallID(msg),
			// Populate ToolName so models with requiresToolResultName get the tool
			// `name` on tool messages, matching the Mistral/Google paths and the TS
			// upstream (openai-completions.ts: requiresToolResultName && toolName).
			ToolName: MessageToolName(msg),
			Blocks:   openAIChatMessageBlocks(MessageBlocks(msg)),
		})
	}
	return out
}

func openAIChatMessageBlocks(blocks []ContentBlock) []aiproviders.OpenAIChatMessageBlock {
	out := make([]aiproviders.OpenAIChatMessageBlock, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, aiproviders.OpenAIChatMessageBlock{
			Type:              block.Type,
			Text:              block.Text,
			MimeType:          block.MimeType,
			Data:              block.Data,
			Thinking:          block.Thinking,
			ID:                block.ID,
			Name:              block.Name,
			Arguments:         block.Arguments,
			ThinkingSignature: thinkingBlockSignature(block),
			ThoughtSignature:  block.ThoughtSignature,
		})
	}
	return out
}
