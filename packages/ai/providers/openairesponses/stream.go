package openairesponses

import (
	"encoding/json"
	"strings"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

type StreamUpdate struct {
	Type         string
	ContentIndex int
	Delta        string
}

type StreamState struct {
	blocks        []aiproviders.OpenAIResponsesBlock
	itemToBlock   map[string]int
	outputToBlock map[int64]int
	usage         aiproviders.OpenAIResponsesUsage
	stopReason    string
	errorMessage  string
	responseID    string
	responseModel string
	serviceTier   string
}

func NewStreamState() *StreamState {
	return &StreamState{
		itemToBlock:   map[string]int{},
		outputToBlock: map[int64]int{},
		stopReason:    "stop",
	}
}

func (s *StreamState) Apply(event map[string]any) []StreamUpdate {
	eventType, _ := event["type"].(string)
	switch eventType {
	case "response.output_item.added", "response.output_item.done":
		item, ok := aiproviders.ResponseItemFromEvent(event["item"])
		if !ok {
			return nil
		}
		s.setBlock(int64FromAny(event["output_index"]), item)
	case "response.output_text.delta", "response.refusal.delta":
		delta, _ := event["delta"].(string)
		index := s.appendText(int64FromAny(event["output_index"]), stringFromAny(event["item_id"]), delta)
		return []StreamUpdate{{Type: "text_delta", ContentIndex: index, Delta: delta}}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		delta, _ := event["delta"].(string)
		index := s.appendThinking(int64FromAny(event["output_index"]), stringFromAny(event["item_id"]), delta)
		return []StreamUpdate{{Type: "thinking_delta", ContentIndex: index, Delta: delta}}
	case "response.function_call_arguments.delta":
		delta, _ := event["delta"].(string)
		index := s.appendToolArgs(int64FromAny(event["output_index"]), stringFromAny(event["item_id"]), delta)
		return []StreamUpdate{{Type: "toolcall_delta", ContentIndex: index, Delta: delta}}
	case "response.function_call_arguments.done":
		arguments, _ := event["arguments"].(string)
		index, delta := s.finishToolArgs(int64FromAny(event["output_index"]), stringFromAny(event["item_id"]), arguments)
		if delta != "" {
			return []StreamUpdate{{Type: "toolcall_delta", ContentIndex: index, Delta: delta}}
		}
	case "response.created":
		if response, ok := aiproviders.ResponseObjectFromEvent(event["response"]); ok {
			s.applyResponseMetadata(response)
		}
	case "response.completed", "response.done", "response.incomplete", "response.failed":
		if response, ok := aiproviders.ResponseObjectFromEvent(event["response"]); ok {
			for index, item := range response.Output {
				s.setBlock(int64(index), item)
			}
			s.applyResponseMetadata(response)
			s.usage = aiproviders.ResponseUsageToUsage(response.Usage)
			stopReason, errorMessage := aiproviders.ResponsesStopReasonResult(response.Status)
			s.stopReason = stopReason
			s.errorMessage = errorMessage
			if response.Status == "failed" || response.Status == "cancelled" {
				s.errorMessage = aiproviders.ResponseFailureMessage(response)
			}
		}
	case "error":
		message := stringFromAny(event["message"])
		code := stringFromAny(event["code"])
		if message == "" {
			message = code
		} else if code != "" {
			message = "Error Code " + code + ": " + message
		}
		if message == "" {
			message = "OpenAI response stream error"
		}
		s.stopReason = "error"
		s.errorMessage = message
	}
	return nil
}

func (s *StreamState) Parsed() aiproviders.OpenAIResponsesParsed {
	stop := s.stopReason
	if stop == "" {
		stop = "stop"
	}
	blocks := append([]aiproviders.OpenAIResponsesBlock(nil), s.blocks...)
	toolCalls := make([]aiproviders.OpenAIResponsesToolCall, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "toolCall" {
			toolCalls = append(toolCalls, aiproviders.OpenAIResponsesToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Arguments,
			})
		}
	}
	if stop == "stop" && len(toolCalls) > 0 {
		stop = "toolUse"
	}
	return aiproviders.OpenAIResponsesParsed{
		Blocks:        blocks,
		ToolCalls:     toolCalls,
		Usage:         s.usage,
		StopReason:    stop,
		ErrorMessage:  s.errorMessage,
		ResponseID:    s.responseID,
		ResponseModel: s.responseModel,
		ServiceTier:   s.serviceTier,
	}
}

func (s *StreamState) applyResponseMetadata(response aiproviders.ResponseObject) {
	if response.ID != "" {
		s.responseID = response.ID
	}
	if response.Model != "" {
		s.responseModel = response.Model
	}
	if response.ServiceTier != "" {
		s.serviceTier = response.ServiceTier
	}
}

func (s *StreamState) setBlock(outputIndex int64, item aiproviders.ResponseItem) {
	block := aiproviders.ResponseItemStreamBlock(item)
	if block.Type == "" {
		return
	}
	index, ok := s.itemToBlock[item.ID]
	if !ok && outputIndex >= 0 {
		index, ok = s.outputToBlock[outputIndex]
	}
	if !ok {
		index = len(s.blocks)
		if outputIndex >= 0 {
			s.outputToBlock[outputIndex] = index
		}
		if item.ID != "" {
			s.itemToBlock[item.ID] = index
		}
		s.blocks = append(s.blocks, block)
	} else {
		s.blocks[index] = mergeResponseStreamBlock(s.blocks[index], block)
	}
	if item.ID != "" {
		s.itemToBlock[item.ID] = index
	}
}

func (s *StreamState) appendText(outputIndex int64, itemID, delta string) int {
	index := s.ensureBlock(outputIndex, itemID, aiproviders.OpenAIResponsesBlock{Type: "text"})
	s.blocks[index].Text += delta
	return index
}

func (s *StreamState) appendThinking(outputIndex int64, itemID, delta string) int {
	index := s.ensureBlock(outputIndex, itemID, aiproviders.OpenAIResponsesBlock{Type: "thinking"})
	s.blocks[index].Thinking += delta
	return index
}

func (s *StreamState) appendToolArgs(outputIndex int64, itemID, delta string) int {
	index := s.ensureBlock(outputIndex, itemID, aiproviders.OpenAIResponsesBlock{Type: "toolCall", Arguments: json.RawMessage(`{}`)})
	s.blocks[index].Data += delta
	s.blocks[index].Arguments = aiutils.StreamingToolArguments(s.blocks[index].Data)
	return index
}

func (s *StreamState) finishToolArgs(outputIndex int64, itemID, arguments string) (int, string) {
	index := s.ensureBlock(outputIndex, itemID, aiproviders.OpenAIResponsesBlock{Type: "toolCall", Arguments: json.RawMessage(`{}`)})
	previous := s.blocks[index].Data
	s.blocks[index].Data = arguments
	s.blocks[index].Arguments = aiutils.StreamingToolArguments(arguments)
	if strings.HasPrefix(arguments, previous) {
		return index, arguments[len(previous):]
	}
	return index, ""
}

func (s *StreamState) ensureBlock(outputIndex int64, itemID string, fallback aiproviders.OpenAIResponsesBlock) int {
	if itemID != "" {
		if index, ok := s.itemToBlock[itemID]; ok {
			return index
		}
	}
	if outputIndex >= 0 {
		if index, ok := s.outputToBlock[outputIndex]; ok {
			if itemID != "" {
				s.itemToBlock[itemID] = index
			}
			return index
		}
	}
	index := len(s.blocks)
	if outputIndex >= 0 {
		s.outputToBlock[outputIndex] = index
	}
	if itemID != "" {
		s.itemToBlock[itemID] = index
	}
	s.blocks = append(s.blocks, fallback)
	return index
}

func int64FromAny(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return -1
	}
}

func stringFromAny(value any) string {
	text, _ := value.(string)
	return text
}

func mergeResponseStreamBlock(old, next aiproviders.OpenAIResponsesBlock) aiproviders.OpenAIResponsesBlock {
	if next.Text == "" {
		next.Text = old.Text
	}
	if next.Thinking == "" {
		next.Thinking = old.Thinking
	}
	if len(next.Arguments) == 0 || string(next.Arguments) == `{}` {
		next.Arguments = old.Arguments
		next.Data = old.Data
	}
	if next.ID == "" {
		next.ID = old.ID
	}
	if next.Name == "" {
		next.Name = old.Name
	}
	return next
}
