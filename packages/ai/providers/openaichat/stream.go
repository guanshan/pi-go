package openaichat

import (
	"encoding/json"
	"errors"
	"strings"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
	openai "github.com/openai/openai-go/v3"
)

type StreamUpdate struct {
	Type         string
	ContentIndex int
	Delta        string
}

type StreamAccumulator struct {
	text                  strings.Builder
	thinking              strings.Builder
	thinkingSignature     string
	toolCalls             map[int64]*streamingToolCall
	toolThoughtSignatures map[string]string
	blockOrder            []streamingBlockRef
	textSeen              bool
	thinkingSeen          bool
	finishReason          string
	usage                 aiproviders.OpenAIChatUsage
	sawChunk              bool
	responseID            string
	responseModel         string
	provider              string
}

type streamingToolCall struct {
	id        string
	name      string
	arguments strings.Builder
}

type streamingBlockRef struct {
	kind      string
	toolIndex int64
}

func NewStreamAccumulator(provider ...string) *StreamAccumulator {
	providerName := ""
	if len(provider) > 0 {
		providerName = provider[0]
	}
	return &StreamAccumulator{
		toolCalls:             map[int64]*streamingToolCall{},
		toolThoughtSignatures: map[string]string{},
		provider:              providerName,
	}
}

func (s *StreamAccumulator) SawChunk() bool {
	return s.sawChunk
}

// SawFinishReason reports whether any streamed choice carried a finish_reason.
// Mirrors the TypeScript `hasFinishReason` guard in openai-completions.ts: a
// stream that ends without a finish_reason is treated as truncated/failed rather
// than a silent successful "stop".
func (s *StreamAccumulator) SawFinishReason() bool {
	return s.finishReason != ""
}

func (s *StreamAccumulator) Apply(chunk openai.ChatCompletionChunk) []StreamUpdate {
	s.sawChunk = true
	if s.responseID == "" {
		s.responseID = chunk.ID
	}
	if s.responseModel == "" {
		s.responseModel = chunk.Model
	}
	if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 || chunk.Usage.TotalTokens != 0 {
		// Parse usage from the chunk's raw JSON so the non-standard
		// cache_write_tokens (OpenRouter/DS4) and DeepSeek prompt_cache_hit_tokens
		// fallback are honored exactly like the non-streaming path; the typed SDK
		// chunk struct omits both. Fall back to the typed fields only if the raw
		// JSON is unavailable.
		if usage, ok := aiproviders.OpenAIChatStreamUsageFromRaw([]byte(chunk.RawJSON())); ok {
			s.usage = usage
		} else {
			cacheRead := int(chunk.Usage.PromptTokensDetails.CachedTokens)
			s.usage = aiproviders.OpenAIChatUsage{
				Input:       aiproviders.MaxInt(0, int(chunk.Usage.PromptTokens)-cacheRead),
				Output:      int(chunk.Usage.CompletionTokens),
				CacheRead:   cacheRead,
				TotalTokens: int(chunk.Usage.TotalTokens),
			}
		}
	}
	var updates []StreamUpdate
	for _, choice := range chunk.Choices {
		if choice.FinishReason != "" {
			s.finishReason = choice.FinishReason
		}
		delta := choice.Delta.Content
		if delta == "" {
			delta = choice.Delta.Refusal
		}
		if delta != "" {
			s.text.WriteString(delta)
			index := s.noteText()
			updates = append(updates, StreamUpdate{Type: "text_delta", ContentIndex: index, Delta: delta})
		}
		//nolint:staticcheck // legacy OpenAI function_call streaming field, kept for older deployments that still emit it
		if fc := choice.Delta.FunctionCall; fc.Name != "" || fc.Arguments != "" {
			call := s.tool(0)
			if fc.Name != "" {
				call.name = fc.Name
			}
			call.arguments.WriteString(fc.Arguments)
			index := s.contentIndexFor(streamingBlockRef{kind: "tool", toolIndex: 0})
			updates = append(updates, StreamUpdate{Type: "toolcall_delta", ContentIndex: index, Delta: fc.Arguments})
		}
		for _, tc := range choice.Delta.ToolCalls {
			call := s.tool(tc.Index)
			if tc.ID != "" {
				call.id = tc.ID
			}
			if tc.Function.Name != "" {
				call.name = tc.Function.Name
			}
			call.arguments.WriteString(tc.Function.Arguments)
			index := s.contentIndexFor(streamingBlockRef{kind: "tool", toolIndex: tc.Index})
			updates = append(updates, StreamUpdate{Type: "toolcall_delta", ContentIndex: index, Delta: tc.Function.Arguments})
		}
	}
	return updates
}

func (s *StreamAccumulator) ApplyRaw(raw []byte) ([]StreamUpdate, error) {
	var errorPayload struct {
		Error *struct {
			Message  string `json:"message"`
			Metadata struct {
				Raw string `json:"raw"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &errorPayload); err == nil && errorPayload.Error != nil {
		message := errorPayload.Error.Message
		// Some providers via OpenRouter put extra diagnostics in error.metadata.raw;
		// append it to match openai-completions.ts.
		if rawMetadata := errorPayload.Error.Metadata.Raw; rawMetadata != "" {
			message += "\n" + rawMetadata
		}
		return nil, errors.New(message)
	}
	var chunk openai.ChatCompletionChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return nil, err
	}
	updates := s.Apply(chunk)
	rawUpdates, err := s.applyRawExtensions(raw)
	if err != nil {
		return nil, err
	}
	return append(updates, rawUpdates...), nil
}

func (s *StreamAccumulator) Parsed(final bool) aiproviders.OpenAIChatParsed {
	blocks := s.blocks(final)
	calls := make([]aiproviders.OpenAIChatToolCall, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "toolCall" {
			calls = append(calls, aiproviders.OpenAIChatToolCall{ID: block.ID, Name: block.Name, Arguments: block.Arguments, ThoughtSignature: block.ThoughtSignature})
		}
	}
	stop, errorMessage := aiproviders.OpenAIChatStopReason(s.finishReason, len(calls) > 0)
	if stop == "stop" && len(calls) > 0 {
		stop = "toolUse"
	}
	return aiproviders.OpenAIChatParsed{Blocks: blocks, ToolCalls: calls, Usage: s.usage, StopReason: stop, ErrorMessage: errorMessage, ResponseID: s.responseID, ResponseModel: s.responseModel}
}

func (s *StreamAccumulator) tool(index int64) *streamingToolCall {
	if call, ok := s.toolCalls[index]; ok {
		return call
	}
	call := &streamingToolCall{}
	s.toolCalls[index] = call
	s.blockOrder = append(s.blockOrder, streamingBlockRef{kind: "tool", toolIndex: index})
	return call
}

func (s *StreamAccumulator) blocks(final bool) []aiproviders.OpenAIChatBlock {
	blocks := []aiproviders.OpenAIChatBlock{}
	for _, ref := range s.blockOrder {
		switch ref.kind {
		case "text":
			if text := s.text.String(); text != "" {
				blocks = append(blocks, aiproviders.OpenAIChatBlock{Type: "text", Text: text})
			}
		case "thinking":
			if thinking := s.thinking.String(); thinking != "" {
				blocks = append(blocks, aiproviders.OpenAIChatBlock{Type: "thinking", Thinking: thinking, ThinkingSignature: s.thinkingSignature})
			}
		case "tool":
			call := s.toolCalls[ref.toolIndex]
			if call == nil {
				continue
			}
			id := call.id
			if id == "" && final {
				id = aiproviders.ShortID()
			}
			rawArgs := call.arguments.String()
			args := json.RawMessage(`{}`)
			if final {
				args = aiproviders.NormalizeToolArguments(json.RawMessage(rawArgs))
			} else if parsed := aiutils.ParseStreamingJSON(rawArgs); len(parsed) > 0 {
				if encoded, err := json.Marshal(parsed); err == nil {
					args = encoded
				}
			}
			blocks = append(blocks, aiproviders.OpenAIChatBlock{Type: "toolCall", ID: id, Name: call.name, Arguments: args, ThoughtSignature: s.toolThoughtSignatures[id]})
		}
	}
	return blocks
}

func (s *StreamAccumulator) noteText() int {
	if s.textSeen {
		return s.contentIndexFor(streamingBlockRef{kind: "text"})
	}
	s.textSeen = true
	s.blockOrder = append(s.blockOrder, streamingBlockRef{kind: "text"})
	return s.contentIndexFor(streamingBlockRef{kind: "text"})
}

func (s *StreamAccumulator) noteThinking(signature string) int {
	if s.thinkingSignature == "" {
		s.thinkingSignature = signature
	}
	if s.thinkingSeen {
		return s.contentIndexFor(streamingBlockRef{kind: "thinking"})
	}
	s.thinkingSeen = true
	s.blockOrder = append(s.blockOrder, streamingBlockRef{kind: "thinking"})
	return s.contentIndexFor(streamingBlockRef{kind: "thinking"})
}

func (s *StreamAccumulator) contentIndexFor(ref streamingBlockRef) int {
	index := 0
	for _, current := range s.blockOrder {
		if current == ref {
			return index
		}
		if s.refHasBlock(current) {
			index++
		}
	}
	return index
}

func (s *StreamAccumulator) refHasBlock(ref streamingBlockRef) bool {
	switch ref.kind {
	case "text":
		return s.text.String() != ""
	case "thinking":
		return s.thinking.String() != ""
	case "tool":
		return s.toolCalls[ref.toolIndex] != nil
	default:
		return false
	}
}

func (s *StreamAccumulator) applyRawExtensions(raw []byte) ([]StreamUpdate, error) {
	var extras struct {
		Choices []struct {
			Usage *struct {
				PromptTokens         int `json:"prompt_tokens"`
				CompletionTokens     int `json:"completion_tokens"`
				TotalTokens          int `json:"total_tokens"`
				PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
				PromptDetails        struct {
					CachedTokens     int `json:"cached_tokens"`
					CacheWriteTokens int `json:"cache_write_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
			Delta struct {
				ReasoningContent string            `json:"reasoning_content"`
				Reasoning        string            `json:"reasoning"`
				ReasoningText    string            `json:"reasoning_text"`
				ReasoningDetails []json.RawMessage `json:"reasoning_details"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &extras); err != nil {
		return nil, err
	}
	var updates []StreamUpdate
	for _, choice := range extras.Choices {
		if choice.Usage != nil {
			// Mirror parseChunkUsage for the non-standard choice.usage location
			// (Moonshot): include cache_write_tokens and the prompt_cache_hit_tokens
			// fallback so cacheWrite/cacheRead are not under-reported.
			s.usage = aiproviders.OpenAIChatUsageFromValues(
				choice.Usage.PromptTokens,
				choice.Usage.CompletionTokens,
				choice.Usage.TotalTokens,
				choice.Usage.PromptDetails.CachedTokens,
				choice.Usage.PromptDetails.CacheWriteTokens,
				choice.Usage.PromptCacheHitTokens,
			)
		}
		if thinking, signature := aiproviders.OpenAIChatReasoningText(choice.Delta.ReasoningContent, choice.Delta.Reasoning, choice.Delta.ReasoningText, s.provider); thinking != "" {
			s.thinking.WriteString(thinking)
			index := s.noteThinking(signature)
			updates = append(updates, StreamUpdate{Type: "thinking_delta", ContentIndex: index, Delta: thinking})
		}
		for id, signature := range aiproviders.OpenAIChatReasoningDetails(choice.Delta.ReasoningDetails) {
			s.toolThoughtSignatures[id] = signature
		}
	}
	return updates, nil
}
