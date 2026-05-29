package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

func (r *ModelRegistry) bedrockChatStream(ctx context.Context, req ChatRequest) *AssistantMessageEventStream {
	return providerStream(ctx, req.Model, 16, func(stream *AssistantMessageEventStream) (AssistantMessage, error) {
		return r.runBedrockChatStream(ctx, req, stream)
	})
}

func (r *ModelRegistry) runBedrockChatStream(ctx context.Context, req ChatRequest, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial := NewAssistantMessageForModel(req.Model, nil, Usage{}, "stop")
	headers := aiproviders.RequestHeaders(req.Model.Headers, req.Headers)
	input := bedrockConverseStreamInput(req)
	input, err := applyOnPayloadAs[*bedrockruntime.ConverseStreamInput](req, input)
	if err != nil {
		return bedrockStreamError(partial, err, stream)
	}
	out, err := r.doBedrockConverseStream(ctx, req, headers, input)
	if err != nil {
		return bedrockStreamError(partial, err, stream)
	}
	eventStream := out.GetStream()
	if eventStream == nil {
		return bedrockStreamError(partial, errors.New("empty Bedrock stream"), stream)
	}
	defer eventStream.Close()
	stopCloseOnContext := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			eventStream.Close()
		case <-stopCloseOnContext:
		}
	}()
	defer close(stopCloseOnContext)

	stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
	blocks := []ContentBlock{}
	blockByIndex := map[int32]int{}
	for event := range eventStream.Events() {
		if err := ctx.Err(); err != nil {
			return bedrockStreamError(partial, err, stream)
		}
		if err := bedrockApplyStreamOutput(event, &partial, &blocks, blockByIndex, stream); err != nil {
			return bedrockStreamError(partial, err, stream)
		}
	}
	if err := ctx.Err(); err != nil {
		return bedrockStreamError(partial, err, stream)
	}
	if err := eventStream.Err(); err != nil {
		return bedrockStreamError(partial, err, stream)
	}
	if hasToolCallBlock(blocks) && partial.StopReason == "stop" {
		partial.StopReason = "toolUse"
	}
	partial.Content = append([]ContentBlock(nil), blocks...)
	partial.Usage = usageWithCost(req.Model, partial.Usage)
	stream.Push(AssistantMessageEvent{Type: "done", Reason: doneReason(partial.StopReason), Partial: partial, Message: partial})
	return partial, nil
}
func bedrockConverseStreamInput(req ChatRequest) *bedrockruntime.ConverseStreamInput {
	input := bedrockConverseInput(req)
	return &bedrockruntime.ConverseStreamInput{
		ModelId:                      input.ModelId,
		Messages:                     input.Messages,
		System:                       input.System,
		InferenceConfig:              input.InferenceConfig,
		ToolConfig:                   input.ToolConfig,
		AdditionalModelRequestFields: input.AdditionalModelRequestFields,
	}
}
func bedrockApplyStreamOutput(event bedrocktypes.ConverseStreamOutput, partial *AssistantMessage, blocks *[]ContentBlock, blockByIndex map[int32]int, stream *AssistantMessageEventStream) error {
	switch item := event.(type) {
	case *bedrocktypes.ConverseStreamOutputMemberMessageStart:
		if item.Value.Role != "" && item.Value.Role != bedrocktypes.ConversationRoleAssistant {
			return fmt.Errorf("unexpected Bedrock stream role %q", item.Value.Role)
		}
	case *bedrocktypes.ConverseStreamOutputMemberContentBlockStart:
		bedrockApplyStreamBlockStart(item.Value, partial, blocks, blockByIndex, stream)
	case *bedrocktypes.ConverseStreamOutputMemberContentBlockDelta:
		bedrockApplyStreamBlockDelta(item.Value, partial, blocks, blockByIndex, stream)
	case *bedrocktypes.ConverseStreamOutputMemberContentBlockStop:
		bedrockApplyStreamBlockStop(item.Value, partial, blocks, blockByIndex, stream)
	case *bedrocktypes.ConverseStreamOutputMemberMessageStop:
		partial.StopReason = aiproviders.BedrockStopReason(string(item.Value.StopReason))
	case *bedrocktypes.ConverseStreamOutputMemberMetadata:
		partial.Usage = bedrockUsage(aiproviders.BedrockUsageFromTokenUsage(item.Value.Usage))
	}
	return nil
}

func bedrockApplyStreamBlockStart(event bedrocktypes.ContentBlockStartEvent, partial *AssistantMessage, blocks *[]ContentBlock, blockByIndex map[int32]int, stream *AssistantMessageEventStream) {
	index := aws.ToInt32(event.ContentBlockIndex)
	switch start := event.Start.(type) {
	case *bedrocktypes.ContentBlockStartMemberToolUse:
		block := ContentBlock{
			Type:      "toolCall",
			ID:        aws.ToString(start.Value.ToolUseId),
			Name:      aws.ToString(start.Value.Name),
			Arguments: jsonRawObject(),
		}
		*blocks = append(*blocks, block)
		blockByIndex[index] = len(*blocks) - 1
		partial.Content = append([]ContentBlock(nil), (*blocks)...)
		stream.Push(AssistantMessageEvent{Type: "toolcall_start", ContentIndex: len(*blocks) - 1, Partial: *partial})
	}
}

func bedrockApplyStreamBlockDelta(event bedrocktypes.ContentBlockDeltaEvent, partial *AssistantMessage, blocks *[]ContentBlock, blockByIndex map[int32]int, stream *AssistantMessageEventStream) {
	switch delta := event.Delta.(type) {
	case *bedrocktypes.ContentBlockDeltaMemberText:
		index := bedrockEnsureStreamBlock(aws.ToInt32(event.ContentBlockIndex), "text", partial, blocks, blockByIndex, stream)
		(*blocks)[index].Text += delta.Value
		partial.Content = append([]ContentBlock(nil), (*blocks)...)
		stream.Push(AssistantMessageEvent{Type: "text_delta", ContentIndex: index, Delta: delta.Value, Partial: *partial})
	case *bedrocktypes.ContentBlockDeltaMemberToolUse:
		index, ok := blockByIndex[aws.ToInt32(event.ContentBlockIndex)]
		if !ok || index < 0 || index >= len(*blocks) || (*blocks)[index].Type != "toolCall" {
			return
		}
		input := aws.ToString(delta.Value.Input)
		(*blocks)[index].Data += input
		(*blocks)[index].Arguments = aiutils.StreamingToolArguments((*blocks)[index].Data)
		partial.Content = append([]ContentBlock(nil), (*blocks)...)
		stream.Push(AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: index, Delta: input, Partial: *partial})
	case *bedrocktypes.ContentBlockDeltaMemberReasoningContent:
		bedrockApplyReasoningDelta(aws.ToInt32(event.ContentBlockIndex), delta.Value, partial, blocks, blockByIndex, stream)
	}
}

func bedrockApplyReasoningDelta(contentBlockIndex int32, delta bedrocktypes.ReasoningContentBlockDelta, partial *AssistantMessage, blocks *[]ContentBlock, blockByIndex map[int32]int, stream *AssistantMessageEventStream) {
	index := bedrockEnsureStreamBlock(contentBlockIndex, "thinking", partial, blocks, blockByIndex, stream)
	switch reasoning := delta.(type) {
	case *bedrocktypes.ReasoningContentBlockDeltaMemberText:
		(*blocks)[index].Thinking += reasoning.Value
		partial.Content = append([]ContentBlock(nil), (*blocks)...)
		stream.Push(AssistantMessageEvent{Type: "thinking_delta", ContentIndex: index, Delta: reasoning.Value, Partial: *partial})
	case *bedrocktypes.ReasoningContentBlockDeltaMemberSignature:
		(*blocks)[index].Signature += reasoning.Value
		partial.Content = append([]ContentBlock(nil), (*blocks)...)
	}
}

func bedrockEnsureStreamBlock(contentBlockIndex int32, blockType string, partial *AssistantMessage, blocks *[]ContentBlock, blockByIndex map[int32]int, stream *AssistantMessageEventStream) int {
	if index, ok := blockByIndex[contentBlockIndex]; ok && index >= 0 && index < len(*blocks) {
		return index
	}
	*blocks = append(*blocks, ContentBlock{Type: blockType})
	index := len(*blocks) - 1
	blockByIndex[contentBlockIndex] = index
	partial.Content = append([]ContentBlock(nil), (*blocks)...)
	switch blockType {
	case "thinking":
		stream.Push(AssistantMessageEvent{Type: "thinking_start", ContentIndex: index, Partial: *partial})
	case "text":
		stream.Push(AssistantMessageEvent{Type: "text_start", ContentIndex: index, Partial: *partial})
	}
	return index
}

func bedrockApplyStreamBlockStop(event bedrocktypes.ContentBlockStopEvent, partial *AssistantMessage, blocks *[]ContentBlock, blockByIndex map[int32]int, stream *AssistantMessageEventStream) {
	index, ok := blockByIndex[aws.ToInt32(event.ContentBlockIndex)]
	if !ok || index < 0 || index >= len(*blocks) {
		return
	}
	switch (*blocks)[index].Type {
	case "text":
		partial.Content = append([]ContentBlock(nil), (*blocks)...)
		stream.Push(contentEndEvent((*blocks)[index], index, *partial))
	case "thinking":
		partial.Content = append([]ContentBlock(nil), (*blocks)...)
		stream.Push(contentEndEvent((*blocks)[index], index, *partial))
	case "toolCall":
		(*blocks)[index].Arguments = aiproviders.NormalizeToolArguments(json.RawMessage((*blocks)[index].Data))
		(*blocks)[index].Data = ""
		partial.Content = append([]ContentBlock(nil), (*blocks)...)
		stream.Push(contentEndEvent((*blocks)[index], index, *partial))
	}
	delete(blockByIndex, aws.ToInt32(event.ContentBlockIndex))
}

func bedrockStreamError(partial AssistantMessage, err error, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial.StopReason = stopReasonForError(err)
	if err != nil {
		partial.ErrorMessage = err.Error()
	}
	stream.Push(AssistantMessageEvent{Type: "error", Reason: errorReason(partial.StopReason), Partial: partial, Error: partial})
	return partial, err
}

func jsonRawObject() json.RawMessage {
	return json.RawMessage(`{}`)
}
