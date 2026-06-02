package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

type mistralAPIProvider struct{}

func registerMistralProviders() {
	registerBuiltinProvider(mistralAPIProvider{})
}

func (mistralAPIProvider) API() string { return "mistral-conversations" }

func (mistralAPIProvider) complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return r.mistralChat(ctx, req)
}

func (p mistralAPIProvider) Complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.complete(ctx, r, req)
}

func (p mistralAPIProvider) Stream(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return r.mistralChatStream(ctx, req)
}

func (p mistralAPIProvider) StreamSimple(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

func (r *ModelRegistry) mistralChatStream(ctx context.Context, req ChatRequest) *AssistantMessageEventStream {
	return providerStream(ctx, req.Model, 16, func(stream *AssistantMessageEventStream) (AssistantMessage, error) {
		return r.runMistralChatStream(ctx, req, stream)
	})
}

func (r *ModelRegistry) mistralChat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return ChatResponse{}, err
	}
	if key == "" {
		return ChatResponse{}, fmt.Errorf("no API key for provider: %s", req.Model.Provider)
	}
	body := aiproviders.BuildMistralBody(aiproviders.MistralRequestOptions{
		ModelID:          req.Model.ID,
		SystemPrompt:     req.SystemPrompt,
		Messages:         mistralMessages(req.Messages, req.Model),
		Tools:            ToolDefinitions(req.Tools),
		MaxTokens:        req.MaxTokens,
		MaxOutput:        req.Model.MaxOutput,
		Temperature:      req.Temperature,
		ToolChoice:       requestToolChoice(req),
		ThinkingLevel:    string(req.ThinkingLevel),
		ThinkingLevelMap: effectiveThinkingLevelMap(req.Model),
		SupportsImages:   SupportsInput(req.Model, "image"),
	})
	body, err = applyOnPayloadMap(req, body)
	if err != nil {
		return ChatResponse{}, err
	}
	httpModel := req.Model
	httpModel.BaseURL = aiproviders.MistralChatURL(req.Model.BaseURL)
	raw, err := r.doJSON(ctx, req, httpModel, key, body, aiproviders.MistralExtraHeaders(req.Model.Headers, req.Headers, req.SessionID))
	if err != nil {
		return ChatResponse{}, err
	}
	parsed, err := aiproviders.ParseMistralResponse(raw)
	if err != nil {
		return ChatResponse{}, err
	}
	return mistralChatResponse(parsed, req.Model), nil
}

func (r *ModelRegistry) runMistralChatStream(ctx context.Context, req ChatRequest, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial := NewAssistantMessageForModel(req.Model, nil, Usage{}, "stop")
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return mistralStreamError(partial, err, stream)
	}
	if key == "" {
		return mistralStreamError(partial, fmt.Errorf("no API key for provider: %s", req.Model.Provider), stream)
	}
	body := aiproviders.BuildMistralBody(aiproviders.MistralRequestOptions{
		ModelID:          req.Model.ID,
		SystemPrompt:     req.SystemPrompt,
		Messages:         mistralMessages(req.Messages, req.Model),
		Tools:            ToolDefinitions(req.Tools),
		MaxTokens:        req.MaxTokens,
		MaxOutput:        req.Model.MaxOutput,
		Temperature:      req.Temperature,
		ToolChoice:       requestToolChoice(req),
		ThinkingLevel:    string(req.ThinkingLevel),
		ThinkingLevelMap: effectiveThinkingLevelMap(req.Model),
		SupportsImages:   SupportsInput(req.Model, "image"),
	})
	body["stream"] = true
	body, err = applyOnPayloadMap(req, body)
	if err != nil {
		return mistralStreamError(partial, err, stream)
	}
	// MarshalJSON keeps < > & literal to match the TS upstream wire bytes.
	rawBody, err := aiproviders.MarshalJSON(body)
	if err != nil {
		return mistralStreamError(partial, err, stream)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, aiproviders.MistralChatURL(req.Model.BaseURL), strings.NewReader(string(rawBody)))
	if err != nil {
		return mistralStreamError(partial, err, stream)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "pi-go/"+Version)
	httpReq.Header.Set("Authorization", "Bearer "+key)
	for k, v := range req.Model.Headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range aiproviders.MistralExtraHeaders(req.Model.Headers, req.Headers, req.SessionID) {
		httpReq.Header.Set(k, v)
	}
	resp, err := providerHTTPClient(req).Do(httpReq)
	if err != nil {
		return mistralStreamError(partial, err, stream)
	}
	defer resp.Body.Close()
	if req.OnResponse != nil {
		if err := req.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: aiproviders.HeadersRecord(resp.Header)}, req.Model); err != nil {
			return mistralStreamError(partial, err, stream)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return mistralStreamError(partial, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))), stream)
	}

	stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
	accumulator := newMistralStreamAccumulator()
	tracker := newContentStreamTracker()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), streamScannerMaxLineBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return mistralStreamError(partial, err, stream)
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		updates, err := accumulator.Apply([]byte(data))
		if err != nil {
			return mistralStreamError(partial, err, stream)
		}
		for _, update := range updates {
			partial = mistralChatResponse(accumulator.Parsed(false), req.Model).Message
			tracker.PushDelta(stream, update.Type, update.ContentIndex, update.Delta, partial)
		}
	}
	if err := ctx.Err(); err != nil {
		return mistralStreamError(partial, err, stream)
	}
	if err := scanner.Err(); err != nil {
		return mistralStreamError(partial, err, stream)
	}
	if !accumulator.SawChunk() {
		response, err := r.mistralChat(ctx, req)
		if err != nil {
			return mistralStreamError(partial, err, stream)
		}
		pushAssistantMessage(stream, response.Message)
		return response.Message, nil
	}
	message := mistralChatResponse(accumulator.Parsed(true), req.Model).Message
	tracker.Finish(stream, message)
	stream.Push(AssistantMessageEvent{Type: "done", Reason: doneReason(message.StopReason), Partial: message, Message: message})
	return message, nil
}

func mistralMessages(messages []Message, model Model) []aiproviders.MistralMessage {
	messages = transformMessages(messages, model, nil)
	out := make([]aiproviders.MistralMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, aiproviders.MistralMessage{
			Role:       MessageRole(msg),
			Text:       MessageText(msg),
			ToolCallID: MessageToolCallID(msg),
			ToolName:   MessageToolName(msg),
			IsError:    MessageIsError(msg),
			Blocks:     mistralMessageBlocks(MessageBlocks(msg)),
		})
	}
	return out
}

func mistralMessageBlocks(blocks []ContentBlock) []aiproviders.MistralBlock {
	out := make([]aiproviders.MistralBlock, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, aiproviders.MistralBlock{
			Type:      block.Type,
			Text:      block.Text,
			Thinking:  block.Thinking,
			MimeType:  block.MimeType,
			Data:      block.Data,
			ID:        block.ID,
			Name:      block.Name,
			Arguments: block.Arguments,
		})
	}
	return out
}

func mistralChatResponse(parsed aiproviders.MistralParsed, model Model) ChatResponse {
	blocks := make([]ContentBlock, 0, len(parsed.Blocks))
	for _, block := range parsed.Blocks {
		blocks = append(blocks, ContentBlock{
			Type:      block.Type,
			Text:      block.Text,
			Thinking:  block.Thinking,
			ID:        block.ID,
			Name:      block.Name,
			Arguments: block.Arguments,
		})
	}
	calls := make([]ToolCall, 0, len(parsed.ToolCalls))
	for _, call := range parsed.ToolCalls {
		calls = append(calls, ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	}
	usage := Usage{Input: parsed.Usage.Input, Output: parsed.Usage.Output, TotalTokens: parsed.Usage.TotalTokens}
	msg := NewAssistantMessageForModel(model, blocks, usageWithCost(model, usage), parsed.StopReason)
	msg.ResponseID = parsed.ResponseID
	if parsed.ResponseModel != "" && parsed.ResponseModel != model.ID {
		msg.ResponseModel = parsed.ResponseModel
	}
	return ChatResponse{Message: msg, ToolCalls: calls}
}

func mistralStreamError(partial AssistantMessage, err error, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial.StopReason = stopReasonForError(err)
	if err != nil {
		partial.ErrorMessage = err.Error()
	}
	stream.Push(AssistantMessageEvent{Type: "error", Reason: errorReason(partial.StopReason), Partial: partial, Error: partial})
	return partial, err
}

type mistralStreamUpdate struct {
	Type         string
	ContentIndex int
	Delta        string
}

type mistralStreamAccumulator struct {
	blocks            []mistralStreamingBlock
	toolBlocksByIndex map[int]int
	finishReason      string
	usage             aiproviders.MistralUsage
	sawChunk          bool
	responseID        string
	responseModel     string
}

type mistralStreamingBlock struct {
	typ         string
	streamIndex int
	id          string
	name        string
	text        strings.Builder
	thinking    strings.Builder
	arguments   strings.Builder
}

func newMistralStreamAccumulator() *mistralStreamAccumulator {
	return &mistralStreamAccumulator{toolBlocksByIndex: map[int]int{}}
}

func (s *mistralStreamAccumulator) SawChunk() bool {
	return s.sawChunk
}

func (s *mistralStreamAccumulator) Apply(raw []byte) ([]mistralStreamUpdate, error) {
	s.sawChunk = true
	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content   json.RawMessage `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return nil, err
	}
	if chunk.Error != nil {
		return nil, fmt.Errorf("%s", chunk.Error.Message)
	}
	if s.responseID == "" {
		s.responseID = chunk.ID
	}
	if s.responseModel == "" {
		s.responseModel = chunk.Model
	}
	if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 || chunk.Usage.TotalTokens != 0 {
		s.usage = aiproviders.MistralUsage{Input: chunk.Usage.PromptTokens, Output: chunk.Usage.CompletionTokens, TotalTokens: chunk.Usage.TotalTokens}
	}
	var updates []mistralStreamUpdate
	for _, choice := range chunk.Choices {
		if choice.FinishReason != "" {
			s.finishReason = choice.FinishReason
		}
		for _, block := range mistralStreamContentBlocks(choice.Delta.Content) {
			switch block.Type {
			case "text":
				if block.Text == "" {
					continue
				}
				index := s.contentBlock("text")
				s.blocks[index].text.WriteString(block.Text)
				updates = append(updates, mistralStreamUpdate{Type: "text_delta", ContentIndex: index, Delta: block.Text})
			case "thinking":
				if block.Thinking == "" {
					continue
				}
				index := s.contentBlock("thinking")
				s.blocks[index].thinking.WriteString(block.Thinking)
				updates = append(updates, mistralStreamUpdate{Type: "thinking_delta", ContentIndex: index, Delta: block.Thinking})
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			index := s.toolBlock(tc.Index)
			call := &s.blocks[index]
			if tc.ID != "" {
				call.id = tc.ID
			}
			if tc.Function.Name != "" {
				call.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				call.arguments.WriteString(tc.Function.Arguments)
			}
			updates = append(updates, mistralStreamUpdate{Type: "toolcall_delta", ContentIndex: index, Delta: tc.Function.Arguments})
		}
	}
	return updates, nil
}

func (s *mistralStreamAccumulator) contentBlock(typ string) int {
	if len(s.blocks) > 0 && s.blocks[len(s.blocks)-1].typ == typ {
		return len(s.blocks) - 1
	}
	s.blocks = append(s.blocks, mistralStreamingBlock{typ: typ})
	return len(s.blocks) - 1
}

func (s *mistralStreamAccumulator) toolBlock(index int) int {
	if blockIndex, ok := s.toolBlocksByIndex[index]; ok {
		return blockIndex
	}
	s.blocks = append(s.blocks, mistralStreamingBlock{typ: "toolCall", streamIndex: index})
	blockIndex := len(s.blocks) - 1
	s.toolBlocksByIndex[index] = blockIndex
	return blockIndex
}

func (s *mistralStreamAccumulator) Parsed(final bool) aiproviders.MistralParsed {
	blocks := []aiproviders.MistralBlock{}
	var calls []aiproviders.MistralToolCall
	for _, block := range s.blocks {
		switch block.typ {
		case "text":
			if text := block.text.String(); text != "" {
				blocks = append(blocks, aiproviders.MistralBlock{Type: "text", Text: text})
			}
		case "thinking":
			if thinking := block.thinking.String(); thinking != "" {
				blocks = append(blocks, aiproviders.MistralBlock{Type: "thinking", Thinking: thinking})
			}
		case "toolCall":
			id := block.id
			if (id == "" || id == "null") && final {
				id = aiproviders.DeriveMistralToolCallID(fmt.Sprintf("toolcall:%d", block.streamIndex), 0)
			}
			args := json.RawMessage(`{}`)
			rawArgs := block.arguments.String()
			if final {
				args = aiproviders.MistralNormalizeToolArguments(json.RawMessage(rawArgs))
			} else if parsed := aiutils.ParseStreamingJSON(rawArgs); len(parsed) > 0 {
				if encoded, err := json.Marshal(parsed); err == nil {
					args = encoded
				}
			}
			mapped := aiproviders.MistralBlock{Type: "toolCall", ID: id, Name: block.name, Arguments: args}
			blocks = append(blocks, mapped)
			calls = append(calls, aiproviders.MistralToolCall{ID: id, Name: block.name, Arguments: args})
		}
	}
	stop := aiproviders.MistralStopReason(s.finishReason)
	if len(calls) > 0 {
		stop = "toolUse"
	}
	return aiproviders.MistralParsed{Blocks: blocks, ToolCalls: calls, Usage: s.usage, StopReason: stop, ResponseID: s.responseID, ResponseModel: s.responseModel}
}

func mistralStreamContentBlocks(raw json.RawMessage) []aiproviders.MistralBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var content any
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil
	}
	return aiproviders.MistralResponseContentBlocks(content)
}
