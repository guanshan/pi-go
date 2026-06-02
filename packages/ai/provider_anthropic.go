package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

type anthropicAPIProvider struct{}

func registerAnthropicProviders() {
	registerBuiltinProvider(anthropicAPIProvider{})
}

func (anthropicAPIProvider) API() string { return "anthropic-messages" }

func (anthropicAPIProvider) complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return r.anthropicChat(ctx, req)
}

func (p anthropicAPIProvider) Complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.complete(ctx, r, req)
}

func (anthropicAPIProvider) Stream(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return r.anthropicChatStream(ctx, req)
}

func (p anthropicAPIProvider) StreamSimple(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

func (r *ModelRegistry) anthropicChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return ChatResponse{}, err
	}
	if key == "" {
		return ChatResponse{}, fmt.Errorf("no API key for provider: %s", req.Model.Provider)
	}
	params, err := anthropicMessageParams(req, key)
	if err != nil {
		return ChatResponse{}, err
	}
	client := anthropicClient(req, key)
	var raw []byte
	err = client.Post(ctx, aiproviders.AnthropicMessagesURL(req.Model.BaseURL), params, nil, anthropicoption.WithResponseBodyInto(&raw))
	if err != nil {
		return ChatResponse{}, err
	}
	var resp anthropic.Message
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ChatResponse{}, err
	}
	return anthropicChatResponse(aiproviders.ParseAnthropicMessage(&resp, anthropicIsOAuthToken(key), ToolDefinitions(req.Tools)), req.Model), nil
}

func (r *ModelRegistry) anthropicChatStream(ctx context.Context, req ChatRequest) *AssistantMessageEventStream {
	return providerStream(ctx, req.Model, 16, func(stream *AssistantMessageEventStream) (AssistantMessage, error) {
		return r.runAnthropicChatStream(ctx, req, stream)
	})
}

func (r *ModelRegistry) runAnthropicChatStream(ctx context.Context, req ChatRequest, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial := NewAssistantMessageForModel(req.Model, nil, Usage{}, "stop")
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return anthropicStreamError(partial, err, stream)
	}
	if key == "" {
		return anthropicStreamError(partial, fmt.Errorf("no API key for provider: %s", req.Model.Provider), stream)
	}
	client := anthropicClient(req, key)
	params, err := anthropicMessageParams(req, key)
	if err != nil {
		return anthropicStreamError(partial, err, stream)
	}
	sdkStream := client.Messages.NewStreaming(ctx, params)
	defer sdkStream.Close()

	isOAuth := anthropicIsOAuthToken(key)
	tools := ToolDefinitions(req.Tools)
	var accumulated anthropic.Message
	blocks := []ContentBlock{}
	sawEvent := false
	sawMessageStart := false
	sawMessageStop := false
	for sdkStream.Next() {
		if err := ctx.Err(); err != nil {
			return anthropicStreamError(partial, err, stream)
		}
		sawEvent = true
		event := sdkStream.Current()
		switch event.Type {
		case "message_start":
			sawMessageStart = true
		case "message_stop":
			sawMessageStop = true
		}
		if err := accumulated.Accumulate(event); err != nil {
			return anthropicStreamError(partial, err, stream)
		}
		anthropicApplyStreamEvent(event, &partial, &blocks, stream, isOAuth, tools)
	}
	if err := ctx.Err(); err != nil {
		return anthropicStreamError(partial, err, stream)
	}
	if err := sdkStream.Err(); err != nil {
		return anthropicStreamError(partial, err, stream)
	}
	if !sawEvent {
		response, err := r.anthropicChat(ctx, req)
		if err != nil {
			return anthropicStreamError(partial, err, stream)
		}
		pushAssistantMessage(stream, response.Message)
		return response.Message, nil
	}
	if sawMessageStart && !sawMessageStop {
		// Mirror anthropic.ts iterateAnthropicEvents: a stream that began
		// (message_start) but ended before message_stop was truncated. Surface an
		// error so the retry whitelist ("stream ended before message_stop") can
		// re-issue instead of silently returning a partial "stop".
		return anthropicStreamError(partial, fmt.Errorf("Anthropic stream ended before message_stop"), stream)
	}
	response := anthropicChatResponse(aiproviders.ParseAnthropicMessage(&accumulated, isOAuth, tools), req.Model)
	if response.Message.StopReason == "error" {
		if response.Message.ErrorMessage == "" {
			response.Message.ErrorMessage = "Provider returned an error stop reason"
		}
		return anthropicStreamError(response.Message, fmt.Errorf("%s", response.Message.ErrorMessage), stream)
	}
	stream.Push(AssistantMessageEvent{Type: "done", Reason: doneReason(response.Message.StopReason), Partial: response.Message, Message: response.Message})
	return response.Message, nil
}

func anthropicApplyStreamEvent(event anthropic.MessageStreamEventUnion, partial *AssistantMessage, blocks *[]ContentBlock, stream *AssistantMessageEventStream, isOAuth bool, tools []map[string]any) {
	switch event.Type {
	case "message_start":
		partial.ResponseID = event.Message.ID
		partial.Usage = anthropicUsage(aiproviders.AnthropicUsageFromMessageUsage(event.Message.Usage))
		stream.Push(AssistantMessageEvent{Type: "start", Partial: *partial})
	case "content_block_start":
		block := anthropicStartBlock(event.ContentBlock, isOAuth, tools)
		ensureAnthropicStreamBlock(blocks, int(event.Index), block)
		partial.Content = *blocks
		stream.Push(AssistantMessageEvent{Type: anthropicStartEventType(block.Type), ContentIndex: int(event.Index), Partial: *partial})
	case "content_block_delta":
		delta, eventType := applyAnthropicDelta(blocks, int(event.Index), event.Delta)
		partial.Content = *blocks
		stream.Push(AssistantMessageEvent{Type: eventType, ContentIndex: int(event.Index), Delta: delta, Partial: *partial})
	case "content_block_stop":
		if index := int(event.Index); index >= 0 && index < len(*blocks) && (*blocks)[index].Type == "toolCall" {
			(*blocks)[index].Arguments = aiproviders.NormalizeToolArguments(json.RawMessage((*blocks)[index].Data))
			(*blocks)[index].Data = ""
		}
		partial.Content = *blocks
		index := int(event.Index)
		if index >= 0 && index < len(*blocks) {
			stream.Push(contentEndEvent((*blocks)[index], index, *partial))
		}
	case "message_delta":
		partial.Usage = anthropicUsage(aiproviders.AnthropicUsageFromDeltaUsage(event.Usage))
		stopReason, errorMessage := aiproviders.AnthropicStopReason(string(event.Delta.StopReason), hasToolCallBlock(*blocks))
		partial.StopReason = stopReason
		partial.ErrorMessage = errorMessage
	}
}

func anthropicStreamError(partial AssistantMessage, err error, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial.StopReason = stopReasonForError(err)
	if err != nil {
		partial.ErrorMessage = err.Error()
	}
	stream.Push(AssistantMessageEvent{Type: "error", Reason: errorReason(partial.StopReason), Partial: partial, Error: partial})
	return partial, err
}

func anthropicStartBlock(block anthropic.ContentBlockStartEventContentBlockUnion, isOAuth bool, tools []map[string]any) ContentBlock {
	switch block.Type {
	case "thinking":
		return ContentBlock{Type: "thinking", Thinking: block.Thinking, Signature: block.Signature}
	case "redacted_thinking":
		return ContentBlock{Type: "thinking", Thinking: "[Reasoning redacted]", Signature: block.Data, Redacted: true}
	case "tool_use":
		name := block.Name
		if isOAuth {
			name = aiproviders.FromClaudeCodeName(name, tools)
		}
		return ContentBlock{Type: "toolCall", ID: block.ID, Name: name, Arguments: aiproviders.NormalizeToolArguments(mustJSONRaw(block.Input))}
	default:
		return ContentBlock{Type: "text", Text: block.Text}
	}
}

func applyAnthropicDelta(blocks *[]ContentBlock, index int, delta anthropic.MessageStreamEventUnionDelta) (string, string) {
	if index < 0 {
		return "", "content_delta"
	}
	for len(*blocks) <= index {
		*blocks = append(*blocks, ContentBlock{Type: "text"})
	}
	block := (*blocks)[index]
	switch delta.Type {
	case "thinking_delta":
		block.Type = "thinking"
		block.Thinking += delta.Thinking
		(*blocks)[index] = block
		return delta.Thinking, "thinking_delta"
	case "signature_delta":
		block.Type = "thinking"
		block.Signature = delta.Signature
		(*blocks)[index] = block
		return "", "thinking_delta"
	case "input_json_delta":
		block.Type = "toolCall"
		block.Data += delta.PartialJSON
		block.Arguments = aiutils.StreamingToolArguments(block.Data)
		(*blocks)[index] = block
		return delta.PartialJSON, "toolcall_delta"
	default:
		block.Type = "text"
		block.Text += delta.Text
		(*blocks)[index] = block
		return delta.Text, "text_delta"
	}
}

func ensureAnthropicStreamBlock(blocks *[]ContentBlock, index int, block ContentBlock) {
	if index < 0 {
		return
	}
	for len(*blocks) <= index {
		*blocks = append(*blocks, ContentBlock{})
	}
	(*blocks)[index] = block
}

func anthropicStartEventType(blockType string) string {
	switch blockType {
	case "thinking":
		return "thinking_start"
	case "toolCall":
		return "toolcall_start"
	default:
		return "text_start"
	}
}

func anthropicUsage(usage aiproviders.AnthropicUsageCounts) Usage {
	return Usage{
		Input:       usage.Input,
		Output:      usage.Output,
		CacheRead:   usage.CacheRead,
		CacheWrite:  usage.CacheWrite,
		TotalTokens: usage.TotalTokens,
	}
}

func anthropicChatResponse(parsed aiproviders.AnthropicParsed, model Model) ChatResponse {
	blocks := anthropicContentBlocks(parsed.Blocks)
	msg := NewAssistantMessageForModel(model, blocks, usageWithCost(model, anthropicUsage(parsed.Usage)), parsed.StopReason)
	msg.ErrorMessage = parsed.ErrorMessage
	msg.ResponseID = parsed.ResponseID
	return ChatResponse{Message: msg, ToolCalls: anthropicToolCalls(parsed.ToolCalls)}
}

func anthropicContentBlocks(blocks []aiproviders.AnthropicBlock) []ContentBlock {
	out := make([]ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, ContentBlock{
			Type:      b.Type,
			Text:      b.Text,
			Data:      b.Data,
			MimeType:  b.MimeType,
			Thinking:  b.Thinking,
			ID:        b.ID,
			Name:      b.Name,
			Arguments: b.Arguments,
			Signature: b.ThinkingSignature,
			Redacted:  b.Redacted,
		})
	}
	return out
}

func anthropicToolCalls(calls []aiproviders.AnthropicToolCall) []ToolCall {
	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	}
	return out
}

func hasToolCallBlock(blocks []ContentBlock) bool {
	for _, block := range blocks {
		if block.Type == "toolCall" {
			return true
		}
	}
	return false
}

func mustJSONRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func anthropicRequestOptions(req ChatRequest, key string) aiproviders.AnthropicRequestOptions {
	compat := GetAnthropicMessagesCompat(req.Model)
	return aiproviders.AnthropicRequestOptions{
		ModelID:                     req.Model.ID,
		SystemPrompt:                req.SystemPrompt,
		Messages:                    anthropicMessages(req.Messages, req.Model),
		Tools:                       ToolDefinitions(req.Tools),
		IsOAuth:                     anthropicIsOAuthToken(key),
		CacheRetention:              req.CacheRetention,
		MaxTokens:                   req.MaxTokens,
		MaxOutput:                   req.Model.MaxOutput,
		Temperature:                 req.Temperature,
		ToolChoice:                  requestToolChoice(req),
		Reasoning:                   req.Model.Reasoning,
		ThinkingLevel:               string(req.ThinkingLevel),
		ThinkingLevelMap:            effectiveThinkingLevelMap(req.Model),
		ThinkingBudgets:             providerThinkingBudgets(req),
		ThinkingDisplay:             metadataString(req.Metadata, "thinkingDisplay"),
		Metadata:                    req.Metadata,
		SupportsEagerToolStreaming:  compat.SupportsEagerToolInputStreaming,
		SupportsLongCacheRetention:  compat.SupportsLongCacheRetention,
		SupportsCacheControlOnTools: compat.SupportsCacheControlOnTools,
		ForceAdaptiveThinking:       compat.ForceAdaptiveThinking,
		AllowEmptySignature:         compat.AllowEmptySignature,
	}
}

func anthropicClient(req ChatRequest, key string) anthropic.Client {
	return aiproviders.NewAnthropicClientWithMode(
		key,
		aiproviders.AnthropicBaseURL(req.Model.BaseURL),
		anthropicHeaders(req, key),
		providerHTTPClient(req),
		anthropicUsesBearerAuth(req.Model, key),
		anthropicUsesGatewayAuth(req.Model),
		providerRequestOptions(req),
	)
}

func anthropicUsesGatewayAuth(model Model) bool {
	return model.Provider == "cloudflare-ai-gateway"
}

func anthropicHeaders(req ChatRequest, key string) map[string]string {
	headers := aiproviders.AnthropicHeaders(req.Model.Headers, req.Headers)
	compat := GetAnthropicMessagesCompat(req.Model)
	headers = appendAnthropicBetaHeaders(headers, anthropicBetaFeatures(req, compat)...)
	if aiproviders.ResolveCacheRetention(req.CacheRetention) != "none" && req.SessionID != "" && compat.SendSessionAffinityHeaders {
		headers = aiproviders.MergeHeaders(headers, map[string]string{"x-session-affinity": req.SessionID})
	}
	if req.Model.Provider == "github-copilot" {
		headers = aiproviders.MergeHeaders(headers, anthropicCopilotHeaders(req.Messages))
	}
	if anthropicIsOAuthToken(key) {
		headers = appendAnthropicBetaHeaders(headers, "claude-code-20250219", "oauth-2025-04-20")
		headers = aiproviders.MergeHeaders(headers, map[string]string{"user-agent": "claude-cli/2.1.75", "x-app": "cli"})
	}
	return headers
}

func anthropicBetaFeatures(req ChatRequest, compat AnthropicMessagesCompat) []string {
	features := []string{}
	if len(req.Tools) > 0 && !compat.SupportsEagerToolInputStreaming {
		features = append(features, "fine-grained-tool-streaming-2025-05-14")
	}
	if req.ThinkingLevel != "" && req.ThinkingLevel != ThinkingOff && !compat.ForceAdaptiveThinking && metadataBoolDefault(req.Metadata, "interleavedThinking", true) {
		features = append(features, "interleaved-thinking-2025-05-14")
	}
	return features
}

func appendAnthropicBetaHeaders(headers map[string]string, features ...string) map[string]string {
	if len(features) == 0 {
		return headers
	}
	if headers == nil {
		headers = map[string]string{}
	}
	key := "anthropic-beta"
	for existingKey := range headers {
		if strings.EqualFold(existingKey, key) {
			key = existingKey
			break
		}
	}
	seen := map[string]bool{}
	var values []string
	for _, part := range strings.Split(headers[key], ",") {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			values = append(values, part)
			seen[part] = true
		}
	}
	for _, feature := range features {
		feature = strings.TrimSpace(feature)
		if feature != "" && !seen[feature] {
			values = append(values, feature)
			seen[feature] = true
		}
	}
	headers[key] = strings.Join(values, ",")
	return headers
}

func anthropicMessageParams(req ChatRequest, key string) (anthropic.MessageNewParams, error) {
	opts := anthropicRequestOptions(req, key)
	params := aiproviders.AnthropicMessageParams(opts)
	if anthropicIsOAuthToken(key) {
		// Mirror anthropic.ts:909-923: under OAuth the Claude Code identity block
		// carries the same cache_control breakpoint as the system prompt block.
		identity := anthropic.TextBlockParam{Text: "You are Claude Code, Anthropic's official CLI for Claude."}
		if cacheControl, useCacheControl := aiproviders.AnthropicCacheControlParam(opts.CacheRetention, opts.SupportsLongCacheRetention); useCacheControl {
			identity.CacheControl = cacheControl
		}
		params.System = append([]anthropic.TextBlockParam{identity}, params.System...)
	}
	return applyOnPayloadAs[anthropic.MessageNewParams](req, params)
}

func anthropicUsesBearerAuth(model Model, key string) bool {
	return model.Provider == "github-copilot" || anthropicIsOAuthToken(key)
}

func anthropicIsOAuthToken(key string) bool {
	return strings.Contains(key, "sk-ant-oat")
}

func anthropicCopilotHeaders(messages []Message) map[string]string {
	initiator := "user"
	if len(messages) > 0 && MessageRole(messages[len(messages)-1]) != "user" {
		initiator = "agent"
	}
	headers := map[string]string{
		"X-Initiator":   initiator,
		"Openai-Intent": "conversation-edits",
	}
	if anthropicHasImageInput(messages) {
		headers["Copilot-Vision-Request"] = "true"
	}
	return headers
}

func anthropicHasImageInput(messages []Message) bool {
	for _, msg := range messages {
		role := MessageRole(msg)
		if role != "user" && role != "toolResult" {
			continue
		}
		for _, block := range MessageBlocks(msg) {
			if block.Type == "image" {
				return true
			}
		}
	}
	return false
}

func anthropicMessages(messages []Message, model Model) []aiproviders.AnthropicMessage {
	messages = transformMessages(messages, model, func(id string, _ Model, _ AssistantMessage) string {
		return aiproviders.AnthropicNormalizeToolCallID(id)
	})
	out := make([]aiproviders.AnthropicMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, aiproviders.AnthropicMessage{
			Role:       MessageRole(msg),
			Text:       MessageText(msg),
			ToolCallID: MessageToolCallID(msg),
			IsError:    MessageIsError(msg),
			Blocks:     anthropicBlocks(MessageBlocks(msg)),
		})
	}
	return out
}

func anthropicBlocks(blocks []ContentBlock) []aiproviders.AnthropicBlock {
	out := make([]aiproviders.AnthropicBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, aiproviders.AnthropicBlock{
			Type:              b.Type,
			Text:              b.Text,
			Data:              b.Data,
			MimeType:          b.MimeType,
			Thinking:          b.Thinking,
			ID:                b.ID,
			Name:              b.Name,
			Arguments:         b.Arguments,
			ThinkingSignature: thinkingBlockSignature(b),
			Redacted:          b.Redacted,
		})
	}
	return out
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return value
}

func metadataBoolDefault(metadata map[string]any, key string, fallback bool) bool {
	if metadata == nil {
		return fallback
	}
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return fallback
}

func metadataBoolPointer(metadata map[string]any, key string) *bool {
	if metadata == nil {
		return nil
	}
	if _, ok := metadata[key]; !ok {
		return nil
	}
	value := metadataBoolDefault(metadata, key, false)
	return &value
}
