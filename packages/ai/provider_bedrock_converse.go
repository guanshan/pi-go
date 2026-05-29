package ai

import (
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

func bedrockConverseInput(req ChatRequest) *bedrockruntime.ConverseInput {
	return aiproviders.BedrockConverseInput(bedrockRequestOptions(req))
}

func parseBedrockConverseOutput(out *bedrockruntime.ConverseOutput, model Model) (AssistantMessage, []ToolCall, error) {
	parsed, err := aiproviders.ParseBedrockConverseOutput(out)
	if err != nil {
		return AssistantMessage{}, nil, err
	}
	return bedrockMessage(parsed, model), bedrockToolCalls(parsed.ToolCalls), nil
}

func bedrockRequestOptions(req ChatRequest) aiproviders.BedrockRequestOptions {
	return aiproviders.BedrockRequestOptions{
		ModelID:             req.Model.ID,
		ModelName:           req.Model.Name,
		SystemPrompt:        req.SystemPrompt,
		Messages:            bedrockMessages(req.Messages, req.Model),
		Tools:               ToolDefinitions(req.Tools),
		CacheRetention:      req.CacheRetention,
		MaxTokens:           req.MaxTokens,
		MaxOutput:           req.Model.MaxOutput,
		Temperature:         req.Temperature,
		ToolChoice:          requestToolChoice(req),
		RequestMetadata:     requestMetadata(req),
		Reasoning:           req.Model.Reasoning,
		ThinkingLevel:       string(req.ThinkingLevel),
		ThinkingLevelMap:    effectiveThinkingLevelMap(req.Model),
		ThinkingBudgets:     providerThinkingBudgets(req),
		ThinkingDisplay:     metadataString(req.Metadata, "thinkingDisplay"),
		InterleavedThinking: metadataBoolPointer(req.Metadata, "interleavedThinking"),
	}
}

func bedrockMessages(messages []Message, model Model) []aiproviders.BedrockMessage {
	messages = transformMessages(messages, model, nil)
	out := make([]aiproviders.BedrockMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, aiproviders.BedrockMessage{
			Role:       MessageRole(msg),
			Text:       MessageText(msg),
			ToolCallID: MessageToolCallID(msg),
			IsError:    MessageIsError(msg),
			Blocks:     bedrockBlocks(MessageBlocks(msg)),
		})
	}
	return out
}

func bedrockBlocks(blocks []ContentBlock) []aiproviders.BedrockBlock {
	out := make([]aiproviders.BedrockBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, aiproviders.BedrockBlock{
			Type:              b.Type,
			Text:              b.Text,
			Data:              b.Data,
			MimeType:          b.MimeType,
			Thinking:          b.Thinking,
			ID:                b.ID,
			Name:              b.Name,
			Arguments:         b.Arguments,
			ThinkingSignature: thinkingBlockSignature(b),
		})
	}
	return out
}

func bedrockMessage(parsed aiproviders.BedrockParsed, model Model) AssistantMessage {
	return NewAssistantMessageForModel(model, bedrockContentBlocks(parsed.Blocks), usageWithCost(model, bedrockUsage(parsed.Usage)), parsed.StopReason)
}

func bedrockContentBlocks(blocks []aiproviders.BedrockBlock) []ContentBlock {
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
		})
	}
	return out
}

func bedrockToolCalls(calls []aiproviders.BedrockToolCall) []ToolCall {
	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	}
	return out
}

func bedrockUsage(usage aiproviders.BedrockUsageCounts) Usage {
	return Usage{
		Input:       usage.Input,
		Output:      usage.Output,
		CacheRead:   usage.CacheRead,
		CacheWrite:  usage.CacheWrite,
		TotalTokens: usage.TotalTokens,
	}
}
