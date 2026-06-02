package ai

import (
	"fmt"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

type AssistantMessageDiagnostic = aiutils.AssistantMessageDiagnostic

func SanitizeUnicode(s string) string {
	return aiutils.SanitizeUnicode(s)
}

func sanitizeChatRequest(req ChatRequest) ChatRequest {
	req.SystemPrompt = aiutils.SanitizeUnicode(req.SystemPrompt)
	req.Messages = sanitizeMessages(req.Messages)
	if req.ThinkingLevel != "" {
		req.ThinkingLevel = ClampThinking(req.Model, req.ThinkingLevel)
	}
	return req
}

func requestToolChoice(req ChatRequest) any {
	if req.ToolChoice != nil {
		return req.ToolChoice
	}
	if req.Metadata != nil {
		return req.Metadata["toolChoice"]
	}
	return nil
}

func requestMetadata(req ChatRequest) map[string]string {
	if len(req.RequestMetadata) > 0 {
		return req.RequestMetadata
	}
	raw := req.Metadata["requestMetadata"]
	if raw == nil {
		return nil
	}
	return stringMap(raw)
}

func stringMap(raw any) map[string]string {
	switch value := raw.(type) {
	case map[string]string:
		if len(value) == 0 {
			return nil
		}
		out := make(map[string]string, len(value))
		for k, v := range value {
			out[k] = v
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(value))
		for k, v := range value {
			if s, ok := v.(string); ok {
				out[k] = s
			} else if v != nil {
				out[k] = fmt.Sprint(v)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func sanitizeMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]Message, len(messages))
	for i, msg := range messages {
		out[i] = sanitizeMessage(msg)
	}
	return out
}

func sanitizeMessage(msg Message) Message {
	switch m := msg.(type) {
	case UserMessage:
		m.Content = sanitizeContentBlocks(m.Content)
		return m
	case AssistantMessage:
		m.Content = sanitizeContentBlocks(m.Content)
		m.ErrorMessage = aiutils.SanitizeUnicode(m.ErrorMessage)
		return m
	case ToolResultMessage:
		m.Content = sanitizeContentBlocks(m.Content)
		return m
	case CustomMessage:
		if text, ok := m.Content.(string); ok {
			m.Content = aiutils.SanitizeUnicode(text)
		} else if blocks, ok := m.Content.([]ContentBlock); ok {
			m.Content = sanitizeContentBlocks(blocks)
		}
		return m
	}
	return msg
}

func sanitizeContentBlocks(blocks []ContentBlock) []ContentBlock {
	if len(blocks) == 0 {
		return blocks
	}
	out := make([]ContentBlock, len(blocks))
	for i, block := range blocks {
		out[i] = sanitizeContentBlock(block)
	}
	return out
}

func sanitizeContentBlock(block ContentBlock) ContentBlock {
	block.Text = aiutils.SanitizeUnicode(block.Text)
	block.Thinking = aiutils.SanitizeUnicode(block.Thinking)
	return block
}

func IsContextOverflow(message Message, contextWindow int) bool {
	return aiutils.IsContextOverflow(aiutils.ContextOverflowMessage{
		StopReason:   MessageStopReason(message),
		ErrorMessage: MessageErrorMessage(message),
		Input:        MessageUsage(message).Input,
		CacheRead:    MessageUsage(message).CacheRead,
		Output:       MessageUsage(message).Output,
	}, contextWindow)
}

func RepairJSON(input string) string {
	return aiutils.RepairJSON(input)
}

func ParseJSONWithRepair[T any](input string) (T, error) {
	return aiutils.ParseJSONWithRepair[T](input)
}

func ParseStreamingJSON(input string) map[string]any {
	return aiutils.ParseStreamingJSON(input)
}

func ShortHash(s string) string {
	return aiutils.ShortHash(s)
}

func MergeHeaders(base map[string]string, override map[string]string) map[string]string {
	return aiutils.MergeHeaders(base, override)
}

// marshalNoHTMLEscape serializes value to JSON without escaping the
// HTML-significant characters < > &, matching the TypeScript upstream's
// JSON.stringify wire bytes. Use it for any provider request body serialized by
// our own code in this package.
func marshalNoHTMLEscape(value any) ([]byte, error) {
	return aiproviders.MarshalJSON(value)
}

// unescapeJSONHTML rewrites the < > & escapes that some third-party
// SDKs (Anthropic) emit back into literal < > &, matching JSON.stringify.
func unescapeJSONHTML(data []byte) []byte {
	return aiproviders.UnescapeJSONHTML(data)
}

type SessionResourceCleanup = aiutils.SessionResourceCleanup

func RegisterSessionResourceCleanup(cleanup SessionResourceCleanup) func() {
	return aiutils.RegisterSessionResourceCleanup(cleanup)
}

func CleanupSessionResources(sessionID string) error {
	return aiutils.CleanupSessionResources(sessionID)
}
