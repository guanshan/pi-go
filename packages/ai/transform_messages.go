package ai

import (
	"fmt"
	"time"
)

const (
	nonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
	nonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"
	CompactionSummaryPrefix       = `The conversation history before this point was compacted into the following summary:

<summary>
`
	CompactionSummarySuffix = `
</summary>`
	BranchSummaryPrefix = `The following is a summary of a branch that this conversation came back from:

<summary>
`
	BranchSummarySuffix = `</summary>`
)

func CompactionSummaryText(summary string) string {
	return CompactionSummaryPrefix + summary + CompactionSummarySuffix
}

func BranchSummaryText(summary string) string {
	return BranchSummaryPrefix + summary + BranchSummarySuffix
}

func FormatBashExecutionText(command string, output string, exitCode *int, cancelled bool, truncated bool, fullOutputPath string) string {
	text := fmt.Sprintf("Ran `%s`\n", command)
	if output != "" {
		text += "```\n" + output + "\n```"
	} else {
		text += "(no output)"
	}
	if cancelled {
		text += "\n\n(command cancelled)"
	} else if exitCode != nil && *exitCode != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", *exitCode)
	}
	if truncated && fullOutputPath != "" {
		text += "\n\n[Output truncated. Full output: " + fullOutputPath + "]"
	}
	return text
}

type toolCallIDNormalizer func(id string, model Model, source AssistantMessage) string

func transformMessages(messages []Message, model Model, normalizeToolCallID toolCallIDNormalizer) []Message {
	if len(messages) == 0 {
		return messages
	}
	toolCallIDMap := map[string]string{}
	transformed := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if normalized, handled := normalizeProviderCustomMessage(msg); handled {
			for _, next := range normalized {
				transformed = appendTransformedMessage(transformed, next, model, normalizeToolCallID, toolCallIDMap)
			}
			continue
		}
		transformed = appendTransformedMessage(transformed, msg, model, normalizeToolCallID, toolCallIDMap)
	}

	result := make([]Message, 0, len(transformed))
	var pending []ToolCall
	existingToolResultIDs := map[string]bool{}
	insertSyntheticToolResults := func() {
		for _, call := range pending {
			if existingToolResultIDs[call.ID] {
				continue
			}
			result = append(result, ToolResultMessage{
				Role:        "toolResult",
				ToolCallID:  call.ID,
				ToolName:    call.Name,
				Content:     TextBlocks("No result provided"),
				IsError:     true,
				TimestampMs: time.Now().UnixMilli(),
			})
		}
		pending = nil
		existingToolResultIDs = map[string]bool{}
	}

	for _, msg := range transformed {
		switch m := msg.(type) {
		case AssistantMessage:
			insertSyntheticToolResults()
			if m.StopReason == "error" || m.StopReason == "aborted" {
				continue
			}
			pending = assistantToolCalls(m)
			existingToolResultIDs = map[string]bool{}
			result = append(result, m)
		case ToolResultMessage:
			existingToolResultIDs[m.ToolCallID] = true
			result = append(result, m)
		case UserMessage:
			insertSyntheticToolResults()
			result = append(result, m)
		default:
			result = append(result, msg)
		}
	}
	insertSyntheticToolResults()
	return result
}

func appendTransformedMessage(transformed []Message, msg Message, model Model, normalizeToolCallID toolCallIDNormalizer, toolCallIDMap map[string]string) []Message {
	switch m := msg.(type) {
	case UserMessage:
		m.Content = downgradeUnsupportedImages(m.Content, model, nonVisionUserImagePlaceholder)
		return append(transformed, m)
	case *UserMessage:
		if m == nil {
			return transformed
		}
		copy := *m
		copy.Content = downgradeUnsupportedImages(copy.Content, model, nonVisionUserImagePlaceholder)
		return append(transformed, copy)
	case ToolResultMessage:
		if normalized := toolCallIDMap[m.ToolCallID]; normalized != "" {
			m.ToolCallID = normalized
		}
		m.Content = downgradeUnsupportedImages(m.Content, model, nonVisionToolImagePlaceholder)
		return append(transformed, m)
	case *ToolResultMessage:
		if m == nil {
			return transformed
		}
		copy := *m
		if normalized := toolCallIDMap[copy.ToolCallID]; normalized != "" {
			copy.ToolCallID = normalized
		}
		copy.Content = downgradeUnsupportedImages(copy.Content, model, nonVisionToolImagePlaceholder)
		return append(transformed, copy)
	case AssistantMessage:
		return append(transformed, transformAssistantMessage(m, model, normalizeToolCallID, toolCallIDMap))
	case *AssistantMessage:
		if m == nil {
			return transformed
		}
		return append(transformed, transformAssistantMessage(*m, model, normalizeToolCallID, toolCallIDMap))
	default:
		return append(transformed, msg)
	}
}

// TransformMessagesForProvider normalizes session-only/custom message shapes
// into provider-ready user/assistant/tool messages for providers that do not have
// a provider-specific transform hook.
func TransformMessagesForProvider(messages []Message, model Model) []Message {
	return transformMessages(messages, model, nil)
}

func normalizeProviderCustomMessage(msg Message) ([]Message, bool) {
	custom, ok := AsCustomMessage(msg)
	if !ok {
		return normalizeProviderExtendedMessage(msg)
	}
	switch custom.MessageRole() {
	case "bashExecution":
		if custom.ExcludeFromContext {
			return nil, true
		}
		return []Message{UserMessage{Role: "user", Content: TextBlocks(FormatBashExecutionText(custom.Command, custom.Output, custom.ExitCode, custom.Cancelled, custom.Truncated, custom.FullOutputPath)), TimestampMs: custom.Timestamp()}}, true
	case "custom":
		if blocks, ok := CustomContentBlocks(custom.Content); ok && len(blocks) > 0 {
			return []Message{UserMessage{Role: "user", Content: blocks, TimestampMs: custom.Timestamp()}}, true
		}
		return nil, true
	case "branchSummary":
		return []Message{UserMessage{Role: "user", Content: TextBlocks(BranchSummaryText(custom.Summary)), TimestampMs: custom.Timestamp()}}, true
	case "compactionSummary":
		return []Message{UserMessage{Role: "user", Content: TextBlocks(CompactionSummaryText(custom.Summary)), TimestampMs: custom.Timestamp()}}, true
	default:
		if blocks, ok := CustomContentBlocks(custom.Content); ok && len(blocks) > 0 {
			return []Message{UserMessage{Role: "user", Content: blocks, TimestampMs: custom.Timestamp()}}, true
		}
		return nil, true
	}
}

// ProviderContentMessage renders non-standard session messages as provider-ready blocks.
type ProviderContentMessage interface {
	ProviderContentBlocks() []ContentBlock
}

// NormalizeProviderContentMessage converts ProviderContentMessage to a user message.
func NormalizeProviderContentMessage(msg Message) ([]Message, bool) {
	providerMessage, ok := msg.(ProviderContentMessage)
	if !ok {
		return nil, false
	}
	switch MessageRole(msg) {
	case "", "user", "assistant", "toolResult":
		return nil, false
	default:
		blocks := providerMessage.ProviderContentBlocks()
		if len(blocks) == 0 {
			return nil, true
		}
		return []Message{UserMessage{Role: "user", Content: blocks, TimestampMs: MessageTimestamp(msg)}}, true
	}
}

func normalizeProviderExtendedMessage(msg Message) ([]Message, bool) {
	if converted, ok := NormalizeProviderContentMessage(msg); ok {
		return converted, true
	}
	role := MessageRole(msg)
	switch role {
	case "", "user", "assistant", "toolResult":
		return nil, false
	}
	blocks := []ContentBlock(nil)
	if blockMessage, ok := msg.(interface{ ContentBlocks() []ContentBlock }); ok {
		blocks = blockMessage.ContentBlocks()
	}
	if len(blocks) == 0 {
		return nil, true
	}
	return []Message{UserMessage{Role: "user", Content: blocks, TimestampMs: MessageTimestamp(msg)}}, true
}

func transformAssistantMessage(message AssistantMessage, model Model, normalizeToolCallID toolCallIDNormalizer, toolCallIDMap map[string]string) AssistantMessage {
	sameModel := assistantMatchesModel(message, model)
	blocks := make([]ContentBlock, 0, len(message.Content))
	for _, block := range message.Content {
		switch block.Type {
		case "thinking":
			signature := thinkingBlockSignature(block)
			if block.Redacted {
				if sameModel {
					block.Signature = signature
					blocks = append(blocks, block)
				}
				continue
			}
			if sameModel {
				block.Signature = signature
				if block.Thinking != "" || signature != "" || len(block.RawItem) > 0 {
					blocks = append(blocks, block)
				}
				continue
			}
			if block.Thinking != "" {
				blocks = append(blocks, ContentBlock{Type: "text", Text: block.Thinking})
			}
		case "text":
			if sameModel {
				blocks = append(blocks, block)
			} else {
				blocks = append(blocks, ContentBlock{Type: "text", Text: block.Text})
			}
		case "toolCall":
			next := block
			if !sameModel {
				next.ThoughtSignature = ""
				if normalizeToolCallID != nil {
					if normalized := normalizeToolCallID(block.ID, model, message); normalized != "" && normalized != block.ID {
						toolCallIDMap[block.ID] = normalized
						next.ID = normalized
					}
				}
			}
			blocks = append(blocks, next)
		default:
			blocks = append(blocks, block)
		}
	}
	message.Content = blocks
	return message
}

// assistantMatchesModel reports whether a historical assistant message was
// produced by exactly the model we are now sending to. This mirrors the TS
// strict three-field identity check in transform-messages.ts:92-95
// (provider === model.provider && api === model.api && model === model.id).
//
// The comparison is intentionally strict: an empty API or an API that merely
// equals the provider name does NOT count as the same model. Treating those as
// the same model would replay encrypted reasoning / provider signatures
// (OpenAI encrypted reasoning, Anthropic thinking signatures, thoughtSignature)
// to a provider that never produced them, triggering API errors such as
// OpenAI "reasoning without following item" or Anthropic invalid signature.
// Persisted assistant messages always carry a non-empty api (it is a required
// field upstream and is set by NewAssistantMessage), so there is no legitimate
// empty-API source that needs a loose escape hatch here.
func assistantMatchesModel(message AssistantMessage, model Model) bool {
	return message.Provider == model.Provider &&
		message.API == model.API &&
		message.Model == model.ID
}

func assistantToolCalls(message AssistantMessage) []ToolCall {
	calls := []ToolCall{}
	for _, block := range message.Content {
		if block.Type != "toolCall" {
			continue
		}
		args := block.Arguments
		if len(args) == 0 {
			args = jsonRawObject()
		}
		calls = append(calls, ToolCall{ID: block.ID, Name: block.Name, Arguments: args, ThoughtSignature: block.ThoughtSignature})
	}
	return calls
}

func downgradeUnsupportedImages(blocks []ContentBlock, model Model, placeholder string) []ContentBlock {
	if len(blocks) == 0 || len(model.Input) == 0 || SupportsInput(model, "image") {
		return blocks
	}
	out := make([]ContentBlock, 0, len(blocks))
	previousWasPlaceholder := false
	for _, block := range blocks {
		if block.Type == "image" {
			if !previousWasPlaceholder {
				out = append(out, ContentBlock{Type: "text", Text: placeholder})
			}
			previousWasPlaceholder = true
			continue
		}
		out = append(out, block)
		previousWasPlaceholder = block.Type == "text" && block.Text == placeholder
	}
	return out
}
