package ai

import (
	"context"
	"encoding/json"
	"fmt"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	"google.golang.org/genai"
)

type googleAPIProvider struct {
	vertex bool
}

type GoogleGeneratePayload struct {
	ModelID  string
	Contents []*genai.Content
	Config   *genai.GenerateContentConfig
}

func registerGoogleProviders() {
	registerBuiltinProvider(googleAPIProvider{})
	registerBuiltinProvider(googleAPIProvider{vertex: true})
}

func (p googleAPIProvider) API() string {
	if p.vertex {
		return "google-vertex"
	}
	return "google-generative-ai"
}

func (p googleAPIProvider) complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	if p.vertex {
		return r.googleVertexChat(ctx, req)
	}
	return r.googleChat(ctx, req)
}

func (p googleAPIProvider) Complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.complete(ctx, r, req)
}

func (p googleAPIProvider) Stream(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return r.googleChatStream(ctx, req, p.vertex)
}

func (p googleAPIProvider) StreamSimple(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

func (r *ModelRegistry) googleChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return ChatResponse{}, err
	}
	if key == "" {
		return ChatResponse{}, fmt.Errorf("no API key for provider: %s", req.Model.Provider)
	}
	client, err := googleGenAIClient(ctx, req, key, false, aiproviders.RequestHeaders(req.Model.Headers, req.Headers))
	if err != nil {
		return ChatResponse{}, err
	}
	payload, err := googleGeneratePayload(req, false)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := client.Models.GenerateContent(ctx, payload.ModelID, payload.Contents, payload.Config)
	if err != nil {
		return ChatResponse{}, err
	}
	parsed, err := aiproviders.ParseGoogleGenerateContentResponse(resp)
	if err != nil {
		return ChatResponse{}, err
	}
	return googleChatResponse(parsed, req.Model), nil
}

func (r *ModelRegistry) googleVertexChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return ChatResponse{}, err
	}
	client, err := googleGenAIClient(ctx, req, key, true, aiproviders.RequestHeaders(req.Model.Headers, req.Headers))
	if err != nil {
		return ChatResponse{}, err
	}
	payload, err := googleGeneratePayload(req, true)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := client.Models.GenerateContent(ctx, payload.ModelID, payload.Contents, payload.Config)
	if err != nil {
		return ChatResponse{}, err
	}
	parsed, err := aiproviders.ParseGoogleGenerateContentResponse(resp)
	if err != nil {
		return ChatResponse{}, err
	}
	return googleChatResponse(parsed, req.Model), nil
}

func (r *ModelRegistry) googleChatStream(ctx context.Context, req ChatRequest, vertex bool) *AssistantMessageEventStream {
	return providerStream(ctx, req.Model, 16, func(stream *AssistantMessageEventStream) (AssistantMessage, error) {
		return r.runGoogleChatStream(ctx, req, vertex, stream)
	})
}

func (r *ModelRegistry) runGoogleChatStream(ctx context.Context, req ChatRequest, vertex bool, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial := NewAssistantMessageForModel(req.Model, nil, Usage{}, "stop")
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return googleStreamError(partial, err, stream)
	}
	if !vertex && key == "" {
		return googleStreamError(partial, fmt.Errorf("no API key for provider: %s", req.Model.Provider), stream)
	}
	client, err := googleGenAIClient(ctx, req, key, vertex, aiproviders.RequestHeaders(req.Model.Headers, req.Headers))
	if err != nil {
		return googleStreamError(partial, err, stream)
	}
	payload, err := googleGeneratePayload(req, vertex)
	if err != nil {
		return googleStreamError(partial, err, stream)
	}

	stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
	blocks := []ContentBlock{}
	sawChunk := false
	for resp, streamErr := range client.Models.GenerateContentStream(ctx, payload.ModelID, payload.Contents, payload.Config) {
		if err := ctx.Err(); err != nil {
			return googleStreamError(partial, err, stream)
		}
		if streamErr != nil {
			return googleStreamError(partial, streamErr, stream)
		}
		sawChunk = true
		if err := googleApplyStreamResponse(resp, &partial, &blocks, stream); err != nil {
			return googleStreamError(partial, err, stream)
		}
	}
	if err := ctx.Err(); err != nil {
		return googleStreamError(partial, err, stream)
	}
	if !sawChunk {
		var response ChatResponse
		var err error
		if vertex {
			response, err = r.googleVertexChat(ctx, req)
		} else {
			response, err = r.googleChat(ctx, req)
		}
		if err != nil {
			return googleStreamError(partial, err, stream)
		}
		pushAssistantMessage(stream, response.Message)
		return response.Message, nil
	}
	googleEndCurrentStreamBlock(&partial, blocks, stream)
	if hasToolCallBlock(blocks) {
		partial.StopReason = "toolUse"
	}
	partial.Content = append([]ContentBlock(nil), blocks...)
	partial.Usage = usageWithCost(req.Model, partial.Usage)
	stream.Push(AssistantMessageEvent{Type: "done", Reason: doneReason(partial.StopReason), Partial: partial, Message: partial})
	return partial, nil
}

func googleApplyStreamResponse(resp *genai.GenerateContentResponse, partial *AssistantMessage, blocks *[]ContentBlock, stream *AssistantMessageEventStream) error {
	if resp == nil {
		return nil
	}
	// genai documents GenerateContentResponse.responseId as an output-only field
	// used to identify each response. Keep the first non-empty one from the stream.
	if partial.ResponseID == "" && resp.ResponseID != "" {
		partial.ResponseID = resp.ResponseID
	}
	if resp.UsageMetadata != nil {
		partial.Usage = googleUsage(aiproviders.GoogleUsageFromMetadata(resp.UsageMetadata))
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0] == nil {
		return nil
	}
	candidate := resp.Candidates[0]
	if candidate.FinishReason != "" {
		partial.StopReason = aiproviders.GoogleStopReason(string(candidate.FinishReason))
	}
	if candidate.Content == nil {
		return nil
	}
	for _, part := range candidate.Content.Parts {
		if part == nil {
			continue
		}
		if part.Text != "" {
			googleAppendStreamText(part, partial, blocks, stream)
		}
		if part.FunctionCall != nil {
			if err := googleAppendStreamToolCall(part, partial, blocks, stream); err != nil {
				return err
			}
		}
	}
	if hasToolCallBlock(*blocks) {
		partial.StopReason = "toolUse"
	}
	return nil
}

func googleAppendStreamText(part *genai.Part, partial *AssistantMessage, blocks *[]ContentBlock, stream *AssistantMessageEventStream) {
	blockType := "text"
	eventType := "text_delta"
	startType := "text_start"
	if part.Thought {
		blockType = "thinking"
		eventType = "thinking_delta"
		startType = "thinking_start"
	}
	index := googleEnsureStreamTextBlock(partial, blocks, blockType, startType, stream)
	signature := aiproviders.GoogleThoughtSignature(part.ThoughtSignature)
	if part.Thought {
		(*blocks)[index].Thinking += part.Text
		if signature != "" {
			(*blocks)[index].Signature = signature
		}
	} else {
		(*blocks)[index].Text += part.Text
		if signature != "" {
			(*blocks)[index].TextSignature = signature
		}
	}
	partial.Content = append([]ContentBlock(nil), (*blocks)...)
	stream.Push(AssistantMessageEvent{Type: eventType, ContentIndex: index, Delta: part.Text, Partial: *partial})
}

func googleEnsureStreamTextBlock(partial *AssistantMessage, blocks *[]ContentBlock, blockType, startType string, stream *AssistantMessageEventStream) int {
	if len(*blocks) > 0 {
		last := (*blocks)[len(*blocks)-1]
		if last.Type == blockType {
			return len(*blocks) - 1
		}
		googleEndCurrentStreamBlock(partial, *blocks, stream)
	}
	*blocks = append(*blocks, ContentBlock{Type: blockType})
	partial.Content = append([]ContentBlock(nil), (*blocks)...)
	stream.Push(AssistantMessageEvent{Type: startType, ContentIndex: len(*blocks) - 1, Partial: *partial})
	return len(*blocks) - 1
}

func googleAppendStreamToolCall(part *genai.Part, partial *AssistantMessage, blocks *[]ContentBlock, stream *AssistantMessageEventStream) error {
	googleEndCurrentStreamBlock(partial, *blocks, stream)
	// Generate a unique id when none is provided or when the provider reuses one
	// that already exists among the streamed tool-call blocks.
	id := part.FunctionCall.ID
	if id == "" || googleHasToolCallID(*blocks, id) {
		id = aiproviders.ShortID()
	}
	argsMap := part.FunctionCall.Args
	if argsMap == nil {
		argsMap = map[string]any{}
	}
	args, err := json.Marshal(argsMap)
	if err != nil {
		return err
	}
	block := ContentBlock{
		Type:             "toolCall",
		ID:               id,
		Name:             part.FunctionCall.Name,
		Arguments:        aiproviders.NormalizeToolArguments(args),
		ThoughtSignature: aiproviders.GoogleThoughtSignature(part.ThoughtSignature),
	}
	*blocks = append(*blocks, block)
	partial.Content = append([]ContentBlock(nil), (*blocks)...)
	index := len(*blocks) - 1
	stream.Push(AssistantMessageEvent{Type: "toolcall_start", ContentIndex: index, Partial: *partial})
	stream.Push(AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: index, Delta: string(block.Arguments), Partial: *partial})
	call := toolCallFromBlock(block)
	stream.Push(AssistantMessageEvent{Type: "toolcall_end", ContentIndex: index, ToolCall: &call, Partial: *partial})
	return nil
}

func googleHasToolCallID(blocks []ContentBlock, id string) bool {
	for _, b := range blocks {
		if b.Type == "toolCall" && b.ID == id {
			return true
		}
	}
	return false
}

func googleEndCurrentStreamBlock(partial *AssistantMessage, blocks []ContentBlock, stream *AssistantMessageEventStream) {
	if len(blocks) == 0 {
		return
	}
	last := blocks[len(blocks)-1]
	partial.Content = append([]ContentBlock(nil), blocks...)
	switch last.Type {
	case "thinking":
		stream.Push(contentEndEvent(last, len(blocks)-1, *partial))
	case "text":
		stream.Push(contentEndEvent(last, len(blocks)-1, *partial))
	}
}

func googleStreamError(partial AssistantMessage, err error, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial.StopReason = stopReasonForError(err)
	if err != nil {
		partial.ErrorMessage = err.Error()
	}
	stream.Push(AssistantMessageEvent{Type: "error", Reason: errorReason(partial.StopReason), Partial: partial, Error: partial})
	return partial, err
}

func googleChatResponse(parsed aiproviders.GoogleParsed, model Model) ChatResponse {
	blocks := googleContentBlocks(parsed.Blocks)
	usage := usageWithCost(model, googleUsage(parsed.Usage))
	msg := NewAssistantMessageForModel(model, blocks, usage, parsed.StopReason)
	return ChatResponse{Message: msg, ToolCalls: googleToolCalls(parsed.ToolCalls)}
}

func googleContentBlocks(blocks []aiproviders.GoogleBlock) []ContentBlock {
	out := make([]ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		block := ContentBlock{
			Type:          b.Type,
			Text:          b.Text,
			Data:          b.Data,
			MimeType:      b.MimeType,
			Thinking:      b.Thinking,
			ID:            b.ID,
			Name:          b.Name,
			Arguments:     b.Arguments,
			TextSignature: b.TextSignature,
		}
		if b.Type == "toolCall" {
			block.ThoughtSignature = b.ThinkingSignature
		} else {
			block.Signature = b.ThinkingSignature
		}
		out = append(out, block)
	}
	return out
}

func googleToolCalls(calls []aiproviders.GoogleToolCall) []ToolCall {
	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	}
	return out
}

func googleUsage(usage aiproviders.GoogleUsage) Usage {
	return Usage{
		Input:       usage.Input,
		Output:      usage.Output,
		CacheRead:   usage.CacheRead,
		TotalTokens: usage.TotalTokens,
	}
}

func googleGenAIClient(ctx context.Context, req ChatRequest, key string, vertex bool, headers map[string]string) (*genai.Client, error) {
	config := &genai.ClientConfig{
		APIKey:      key,
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  providerHTTPClient(req),
		HTTPOptions: aiproviders.GoogleHTTPOptions(req.Model.BaseURL, req.Model.Headers, headers, vertex),
	}
	if vertex {
		config.Backend = genai.BackendVertexAI
		if key == "" {
			project, location, err := aiproviders.GoogleVertexProjectLocation()
			if err != nil {
				return nil, err
			}
			config.Project = project
			config.Location = location
		}
	}
	return genai.NewClient(ctx, config)
}

func googleRequestOptions(req ChatRequest, vertex bool) aiproviders.GoogleRequestOptions {
	return aiproviders.GoogleRequestOptions{
		ModelID:         req.Model.ID,
		SystemPrompt:    req.SystemPrompt,
		Messages:        googleMessages(req.Messages, req.Model),
		Tools:           ToolDefinitions(req.Tools),
		MaxTokens:       req.MaxTokens,
		MaxOutput:       req.Model.MaxOutput,
		Temperature:     req.Temperature,
		ToolChoice:      requestToolChoice(req),
		Reasoning:       req.Model.Reasoning,
		ThinkingLevel:   string(req.ThinkingLevel),
		ThinkingBudgets: providerThinkingBudgets(req),
		Vertex:          vertex,
	}
}

func googleGeneratePayload(req ChatRequest, vertex bool) (GoogleGeneratePayload, error) {
	options := googleRequestOptions(req, vertex)
	payload := GoogleGeneratePayload{
		ModelID:  req.Model.ID,
		Contents: aiproviders.GoogleContents(options.Messages, req.Model.ID),
		Config:   aiproviders.GoogleGenerateContentConfig(options),
	}
	return applyOnPayloadAs[GoogleGeneratePayload](req, payload)
}

func googleMessages(messages []Message, model Model) []aiproviders.GoogleMessage {
	messages = transformMessages(messages, model, func(id string, _ Model, _ AssistantMessage) string {
		if aiproviders.GoogleRequiresToolCallID(model.ID) {
			return aiproviders.GoogleNormalizeToolCallID(id)
		}
		return id
	})
	out := make([]aiproviders.GoogleMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, aiproviders.GoogleMessage{
			Role:       MessageRole(msg),
			Text:       MessageText(msg),
			ToolCallID: MessageToolCallID(msg),
			ToolName:   MessageToolName(msg),
			IsError:    MessageIsError(msg),
			Blocks:     googleMessageBlocks(MessageBlocks(msg)),
		})
	}
	return out
}

func googleMessageBlocks(blocks []ContentBlock) []aiproviders.GoogleBlock {
	out := make([]aiproviders.GoogleBlock, 0, len(blocks))
	for _, b := range blocks {
		signature := thinkingBlockSignature(b)
		if b.Type == "toolCall" && b.ThoughtSignature != "" {
			signature = b.ThoughtSignature
		}
		out = append(out, aiproviders.GoogleBlock{
			Type:              b.Type,
			Text:              b.Text,
			Data:              b.Data,
			MimeType:          b.MimeType,
			Thinking:          b.Thinking,
			ID:                b.ID,
			Name:              b.Name,
			Arguments:         b.Arguments,
			TextSignature:     b.TextSignature,
			ThinkingSignature: signature,
		})
	}
	return out
}
