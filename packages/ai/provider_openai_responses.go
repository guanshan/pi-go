package ai

import (
	"context"
	"fmt"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

func (r *ModelRegistry) openAIResponsesChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if err := validateOpenAICodexResponsesTransport(req); err != nil {
		return ChatResponse{}, err
	}
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return ChatResponse{}, err
	}
	if key == "" {
		return ChatResponse{}, fmt.Errorf("no API key for provider: %s", req.Model.Provider)
	}
	body, err := openAIResponsesBody(req)
	if err != nil {
		return ChatResponse{}, err
	}
	url, headers, err := openAIResponsesRequest(req, key)
	if err != nil {
		return ChatResponse{}, err
	}
	if raw, usedSDK, err := doOpenAIResponsesSDK(ctx, req, key, headers, body); usedSDK {
		if err != nil {
			return ChatResponse{}, err
		}
		parsed, err := aiproviders.ParseOpenAIResponses(raw)
		if err != nil {
			return ChatResponse{}, err
		}
		parsed = openAIResponsesParsedWithRequestDefaults(parsed, req)
		response := openAIResponsesChatResponse(parsed, req.Model)
		applyOpenAICodexSSEFallbackDiagnostic(&response.Message, req)
		return response, nil
	}
	bearerAuth := req.Model.API == "openai-responses" && req.Model.Provider != "cloudflare-ai-gateway"
	raw, err := aiproviders.DoOpenAISDKJSONWithClient(ctx, url, key, headers, body, bearerAuth, providerHTTPClient(req), providerRequestOptions(req))
	if err != nil {
		return ChatResponse{}, err
	}
	parsed, err := aiproviders.ParseOpenAIResponses(raw)
	if err != nil {
		return ChatResponse{}, err
	}
	parsed = openAIResponsesParsedWithRequestDefaults(parsed, req)
	response := openAIResponsesChatResponse(parsed, req.Model)
	applyOpenAICodexSSEFallbackDiagnostic(&response.Message, req)
	return response, nil
}

func openAIResponsesParsedWithRequestDefaults(parsed aiproviders.OpenAIResponsesParsed, req ChatRequest) aiproviders.OpenAIResponsesParsed {
	if parsed.ServiceTier == "" {
		parsed.ServiceTier = metadataString(req.Metadata, "serviceTier")
	}
	return parsed
}

func openAIResponsesBody(req ChatRequest) (map[string]any, error) {
	body := aiproviders.OpenAIResponsesBody(openAIResponsesRequestOptions(req))
	return applyOnPayloadMap(req, body)
}

func openAIResponsesRequest(req ChatRequest, key string) (string, map[string]string, error) {
	return aiproviders.OpenAIResponsesRequest(openAIResponsesRequestOptions(req), key)
}

func validateOpenAICodexResponsesTransport(req ChatRequest) error {
	if req.Model.API != "openai-codex-responses" {
		return nil
	}
	switch req.Transport {
	case "", "auto", "sse", "websocket", "websocket-cached":
		return nil
	default:
		return fmt.Errorf("unsupported openai-codex-responses transport %q; expected \"sse\", \"websocket\", \"websocket-cached\", or \"auto\"", req.Transport)
	}
}

func applyOpenAICodexSSEFallbackDiagnostic(message *AssistantMessage, req ChatRequest) {
	if message == nil || req.Model.API != "openai-codex-responses" {
		return
	}
	if req.Transport != "" && req.Transport != "auto" && req.Transport != "websocket" && req.Transport != "websocket-cached" {
		return
	}
	transport := req.Transport
	if transport == "" {
		transport = "auto"
	}
	reason := "websocket transport is not implemented in the Go provider"
	if transport == "websocket" || transport == "websocket-cached" {
		reason = "configured websocket transport fell back to SSE in the Go provider"
	}
	message.Diagnostics = append(message.Diagnostics, AssistantMessageDiagnostic{
		Type: "provider_transport_fallback",
		Details: map[string]any{
			"configuredTransport": transport,
			"fallbackTransport":   "sse",
			"reason":              reason,
		},
	})
}

func openAIResponsesRequestOptions(req ChatRequest) aiproviders.OpenAIResponsesRequestOptions {
	compat := GetOpenAIResponsesCompat(req.Model)
	return aiproviders.OpenAIResponsesRequestOptions{
		API:                          req.Model.API,
		Provider:                     req.Model.Provider,
		ModelID:                      req.Model.ID,
		BaseURL:                      req.Model.BaseURL,
		SystemPrompt:                 req.SystemPrompt,
		Messages:                     openAIResponsesMessages(req.Messages, req.Model),
		Tools:                        ToolDefinitions(req.Tools),
		ModelHeaders:                 req.Model.Headers,
		RequestHeaders:               req.Headers,
		CacheRetention:               req.CacheRetention,
		SessionID:                    req.SessionID,
		MaxTokens:                    req.MaxTokens,
		MaxOutput:                    req.Model.MaxOutput,
		Temperature:                  req.Temperature,
		Reasoning:                    req.Model.Reasoning,
		ThinkingLevel:                string(req.ThinkingLevel),
		ThinkingLevelMap:             effectiveThinkingLevelMap(req.Model),
		ReasoningSummary:             metadataString(req.Metadata, "reasoningSummary"),
		ServiceTier:                  metadataString(req.Metadata, "serviceTier"),
		TextVerbosity:                metadataString(req.Metadata, "textVerbosity"),
		ToolChoice:                   requestToolChoice(req),
		SupportsLongCacheRetention:   boolPtr(compat.SupportsLongCacheRetention),
		SendSessionIDHeader:          boolPtr(compat.SendSessionIDHeader),
		SupportsImageToolResultInput: SupportsInput(req.Model, "image"),
	}
}

func openAIResponsesMessages(messages []Message, model Model) []aiproviders.OpenAIResponsesMessage {
	messages = transformMessages(messages, model, func(id string, target Model, source AssistantMessage) string {
		return aiproviders.OpenAIResponsesNormalizeToolCallID(id, target.Provider, target.API, source.Provider, source.API)
	})
	out := make([]aiproviders.OpenAIResponsesMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, aiproviders.OpenAIResponsesMessage{
			Role:       MessageRole(msg),
			Text:       MessageText(msg),
			ToolCallID: MessageToolCallID(msg),
			Blocks:     openAIResponsesMessageBlocks(MessageBlocks(msg)),
			API:        messageAPI(msg),
			Provider:   messageProvider(msg),
			Model:      messageModel(msg),
		})
	}
	return out
}

func openAIResponsesMessageBlocks(blocks []ContentBlock) []aiproviders.OpenAIResponsesMessageBlock {
	out := make([]aiproviders.OpenAIResponsesMessageBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, aiproviders.OpenAIResponsesMessageBlock{
			Type:              b.Type,
			Text:              b.Text,
			Data:              b.Data,
			MimeType:          b.MimeType,
			ThinkingSignature: thinkingBlockSignature(b),
			RawItem:           cloneRawMessage(b.RawItem),
			TextSignature:     b.TextSignature,
			ID:                b.ID,
			Name:              b.Name,
			Arguments:         b.Arguments,
		})
	}
	return out
}

func openAIResponsesChatResponse(parsed aiproviders.OpenAIResponsesParsed, model Model) ChatResponse {
	message, calls := openAIResponsesMessage(parsed, model)
	return ChatResponse{Message: message, ToolCalls: calls}
}

func openAIResponsesMessage(parsed aiproviders.OpenAIResponsesParsed, model Model) (AssistantMessage, []ToolCall) {
	blocks := openAIResponseBlocks(parsed.Blocks)
	calls := make([]ToolCall, 0, len(parsed.ToolCalls))
	for _, call := range parsed.ToolCalls {
		calls = append(calls, ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	}
	msg := NewAssistantMessageForModel(model, blocks, openAIResponseUsage(parsed.Usage, model), parsed.StopReason)
	msg.ErrorMessage = parsed.ErrorMessage
	msg.ResponseID = parsed.ResponseID
	if parsed.ResponseModel != "" && parsed.ResponseModel != model.ID {
		msg.ResponseModel = parsed.ResponseModel
	}
	applyOpenAIResponsesServiceTierPricing(&msg.Usage, model, parsed.ServiceTier)
	return msg, calls
}

func openAIResponsesApplyPartial(message *AssistantMessage, parsed aiproviders.OpenAIResponsesParsed, model Model) {
	message.Content = openAIResponseBlocks(parsed.Blocks)
	message.Usage = openAIResponseUsage(parsed.Usage, model)
	message.ResponseID = parsed.ResponseID
	message.ErrorMessage = parsed.ErrorMessage
	if parsed.ResponseModel != "" && parsed.ResponseModel != model.ID {
		message.ResponseModel = parsed.ResponseModel
	}
	applyOpenAIResponsesServiceTierPricing(&message.Usage, model, parsed.ServiceTier)
	if parsed.StopReason != "" {
		message.StopReason = parsed.StopReason
	}
}

func openAIResponseBlocks(blocks []aiproviders.OpenAIResponsesBlock) []ContentBlock {
	out := make([]ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, openAIResponseBlock(block))
	}
	return out
}

func openAIResponseBlock(block aiproviders.OpenAIResponsesBlock) ContentBlock {
	out := ContentBlock{
		Type:          block.Type,
		Text:          block.Text,
		Thinking:      block.Thinking,
		Signature:     block.ThinkingSignature,
		RawItem:       cloneRawMessage(block.RawItem),
		TextSignature: block.TextSignature,
		ID:            block.ID,
		Name:          block.Name,
		Arguments:     block.Arguments,
		Data:          block.Data,
	}
	if out.Type == "toolCall" {
		out.Data = ""
	}
	return out
}

func openAIResponseUsage(usage aiproviders.OpenAIResponsesUsage, model Model) Usage {
	return usageWithCost(model, Usage{
		Input:       usage.Input,
		Output:      usage.Output,
		CacheRead:   usage.CacheRead,
		TotalTokens: usage.TotalTokens,
	})
}

func applyOpenAIResponsesServiceTierPricing(usage *Usage, model Model, serviceTier string) {
	multiplier := openAIResponsesServiceTierCostMultiplier(model, serviceTier)
	if multiplier == 1 {
		return
	}
	usage.Cost.Input *= multiplier
	usage.Cost.Output *= multiplier
	usage.Cost.CacheRead *= multiplier
	usage.Cost.CacheWrite *= multiplier
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
}

func openAIResponsesServiceTierCostMultiplier(model Model, serviceTier string) float64 {
	switch serviceTier {
	case "flex":
		return 0.5
	case "priority":
		if model.ID == "gpt-5.5" {
			return 2.5
		}
		return 2
	default:
		return 1
	}
}

func messageAPI(msg Message) string {
	if assistant, ok := AsAssistantMessage(msg); ok {
		return assistant.API
	}
	return ""
}

func messageProvider(msg Message) string {
	if assistant, ok := AsAssistantMessage(msg); ok {
		return assistant.Provider
	}
	return ""
}

func messageModel(msg Message) string {
	if assistant, ok := AsAssistantMessage(msg); ok {
		return assistant.Model
	}
	return ""
}
