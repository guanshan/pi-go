package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func MistralStopReason(reason string) string {
	switch reason {
	case "length", "model_length":
		return "length"
	case "tool_calls":
		return "toolUse"
	case "error":
		return "error"
	default:
		return "stop"
	}
}

type MistralParsed struct {
	Blocks        []MistralBlock
	ToolCalls     []MistralToolCall
	Usage         MistralUsage
	StopReason    string
	ResponseID    string
	ResponseModel string
}

type MistralBlock struct {
	Type      string
	Text      string
	Thinking  string
	MimeType  string
	Data      string
	ID        string
	Name      string
	Arguments json.RawMessage
}

type MistralToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type MistralUsage struct {
	Input       int
	Output      int
	TotalTokens int
}

type MistralRequestOptions struct {
	ModelID          string
	SystemPrompt     string
	Messages         []MistralMessage
	Tools            []map[string]any
	MaxTokens        int
	MaxOutput        int
	Temperature      *float64
	ToolChoice       any
	ThinkingLevel    string
	ThinkingLevelMap map[string]*string
	SupportsImages   bool
}

type MistralMessage struct {
	Role       string
	Text       string
	ToolCallID string
	ToolName   string
	IsError    bool
	Blocks     []MistralBlock
}

func BuildMistralBody(options MistralRequestOptions) map[string]any {
	body := map[string]any{
		"model":    options.ModelID,
		"stream":   false,
		"messages": MistralMessages(options.SystemPrompt, options.Messages, options.SupportsImages),
	}
	if len(options.Tools) > 0 {
		body["tools"] = MistralTools(options.Tools)
	}
	if options.ToolChoice != nil {
		body["tool_choice"] = MistralToolChoice(options.ToolChoice)
	}
	maxTokens := options.MaxTokens
	if maxTokens == 0 {
		maxTokens = options.MaxOutput
	}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	if options.Temperature != nil {
		body["temperature"] = *options.Temperature
	}
	if options.ThinkingLevel != "" && options.ThinkingLevel != "off" {
		if MistralUsesReasoningEffort(options.ModelID) {
			body["reasoning_effort"] = MistralReasoningEffort(options.ThinkingLevel, options.ThinkingLevelMap)
		} else {
			body["prompt_mode"] = "reasoning"
		}
	}
	return body
}

func MistralMessages(system string, messages []MistralMessage, supportsImages bool) []map[string]any {
	out := []map[string]any{}
	if strings.TrimSpace(system) != "" {
		out = append(out, map[string]any{"role": "system", "content": SanitizeProviderText(system)})
	}
	normalizer := NewMistralToolCallIDNormalizer()
	for _, msg := range messages {
		switch msg.Role {
		case "user", "compactionSummary", "branchSummary", "custom":
			if mapped, ok := mistralUserMessage(msg, supportsImages); ok {
				out = append(out, mapped)
			}
		case "assistant":
			if mapped, ok := mistralAssistantMessage(msg, normalizer); ok {
				out = append(out, mapped)
			}
		case "toolResult":
			out = append(out, mistralToolResultMessage(msg, supportsImages, normalizer))
		}
	}
	return out
}

func mistralUserMessage(msg MistralMessage, supportsImages bool) (map[string]any, bool) {
	if len(msg.Blocks) == 0 {
		return map[string]any{"role": "user", "content": SanitizeProviderText(msg.Text)}, true
	}
	hadImages := false
	content := []map[string]any{}
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			content = append(content, map[string]any{"type": "text", "text": SanitizeProviderText(block.Text)})
		case "image":
			hadImages = true
			if supportsImages {
				content = append(content, map[string]any{"type": "image_url", "image_url": DataURL(block.MimeType, block.Data)})
			}
		}
	}
	if len(content) > 0 {
		return map[string]any{"role": "user", "content": content}, true
	}
	if hadImages && !supportsImages {
		return map[string]any{"role": "user", "content": "(image omitted: model does not support images)"}, true
	}
	return nil, false
}

func mistralAssistantMessage(msg MistralMessage, normalizer *MistralToolCallIDNormalizer) (map[string]any, bool) {
	contentParts := []map[string]any{}
	toolCalls := []map[string]any{}
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				contentParts = append(contentParts, map[string]any{"type": "text", "text": SanitizeProviderText(block.Text)})
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				contentParts = append(contentParts, map[string]any{
					"type":     "thinking",
					"thinking": []map[string]any{{"type": "text", "text": SanitizeProviderText(block.Thinking)}},
				})
			}
		case "toolCall":
			toolCalls = append(toolCalls, map[string]any{
				"id":   normalizer.Normalize(block.ID),
				"type": "function",
				"function": map[string]any{
					"name":      block.Name,
					"arguments": MistralRequestArguments(block.Arguments),
				},
			})
		}
	}
	if len(contentParts) == 0 && len(toolCalls) == 0 {
		return nil, false
	}
	mapped := map[string]any{"role": "assistant"}
	if len(contentParts) > 0 {
		mapped["content"] = contentParts
	}
	if len(toolCalls) > 0 {
		mapped["tool_calls"] = toolCalls
	}
	return mapped, true
}

func mistralToolResultMessage(msg MistralMessage, supportsImages bool, normalizer *MistralToolCallIDNormalizer) map[string]any {
	textParts := []string{}
	hasImages := false
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, SanitizeProviderText(block.Text))
		case "image":
			hasImages = true
		}
	}
	content := []map[string]any{{
		"type": "text",
		"text": MistralToolResultText(strings.Join(textParts, "\n"), hasImages, supportsImages, msg.IsError),
	}}
	if supportsImages {
		for _, block := range msg.Blocks {
			if block.Type == "image" {
				content = append(content, map[string]any{"type": "image_url", "image_url": DataURL(block.MimeType, block.Data)})
			}
		}
	}
	return map[string]any{
		"role":         "tool",
		"tool_call_id": normalizer.Normalize(msg.ToolCallID),
		"name":         msg.ToolName,
		"content":      content,
	}
}

func MistralTools(defs []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        d["name"],
				"description": d["description"],
				"parameters":  MistralJSONCompatible(d["parameters"]),
				"strict":      false,
			},
		})
	}
	return out
}

func ParseMistralResponse(raw []byte) (MistralParsed, error) {
	var parsed struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   any `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
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
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return MistralParsed{}, err
	}
	if parsed.Error != nil {
		return MistralParsed{}, fmt.Errorf("%s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		if parsed.Message != "" {
			return MistralParsed{}, fmt.Errorf("%s", parsed.Message)
		}
		return MistralParsed{}, fmt.Errorf("empty Mistral response")
	}
	choice := parsed.Choices[0]
	out := MistralParsed{
		Blocks: MistralResponseContentBlocks(choice.Message.Content),
		Usage: MistralUsage{
			Input:       parsed.Usage.PromptTokens,
			Output:      parsed.Usage.CompletionTokens,
			TotalTokens: parsed.Usage.TotalTokens,
		},
		StopReason:    MistralStopReason(choice.FinishReason),
		ResponseID:    parsed.ID,
		ResponseModel: parsed.Model,
	}
	for index, tc := range choice.Message.ToolCalls {
		id := tc.ID
		if id == "" || id == "null" {
			id = DeriveMistralToolCallID(fmt.Sprintf("toolcall:%d", index), 0)
		}
		args := MistralNormalizeToolArguments(tc.Function.Arguments)
		out.Blocks = append(out.Blocks, MistralBlock{Type: "toolCall", ID: id, Name: tc.Function.Name, Arguments: args})
		out.ToolCalls = append(out.ToolCalls, MistralToolCall{ID: id, Name: tc.Function.Name, Arguments: args})
	}
	if len(out.ToolCalls) > 0 {
		out.StopReason = "toolUse"
	}
	return out, nil
}

func MistralResponseContentBlocks(content any) []MistralBlock {
	switch value := content.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []MistralBlock{{Type: "text", Text: SanitizeProviderText(value)}}
	case []any:
		blocks := []MistralBlock{}
		for _, item := range value {
			switch part := item.(type) {
			case string:
				if part != "" {
					blocks = append(blocks, MistralBlock{Type: "text", Text: SanitizeProviderText(part)})
				}
			case map[string]any:
				switch part["type"] {
				case "text":
					if text, ok := part["text"].(string); ok && text != "" {
						blocks = append(blocks, MistralBlock{Type: "text", Text: SanitizeProviderText(text)})
					}
				case "thinking":
					if thinking := MistralThinkingText(part["thinking"]); thinking != "" {
						blocks = append(blocks, MistralBlock{Type: "thinking", Thinking: SanitizeProviderText(thinking)})
					}
				}
			}
		}
		return blocks
	default:
		return nil
	}
}

func MistralToolResultText(text string, hasImages, supportsImages, isError bool) string {
	trimmed := strings.TrimSpace(text)
	errorPrefix := ""
	if isError {
		errorPrefix = "[tool error] "
	}
	if trimmed != "" {
		imageSuffix := ""
		if hasImages && !supportsImages {
			imageSuffix = "\n[tool image omitted: model does not support images]"
		}
		return errorPrefix + trimmed + imageSuffix
	}
	if hasImages {
		if supportsImages {
			if isError {
				return "[tool error] (see attached image)"
			}
			return "(see attached image)"
		}
		if isError {
			return "[tool error] (image omitted: model does not support images)"
		}
		return "(image omitted: model does not support images)"
	}
	if isError {
		return "[tool error] (no tool output)"
	}
	return "(no tool output)"
}

func MistralJSONCompatible(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return value
	}
	return out
}

func MistralThinkingText(value any) string {
	switch thinking := value.(type) {
	case string:
		return thinking
	case []any:
		parts := []string{}
		for _, item := range thinking {
			switch part := item.(type) {
			case string:
				parts = append(parts, part)
			case map[string]any:
				if text, ok := part["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func MistralRequestArguments(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "{}"
	}
	return string(trimmed)
}

func MistralNormalizeToolArguments(raw json.RawMessage) json.RawMessage {
	return NormalizeToolArguments(raw)
}

func MistralUsesReasoningEffort(modelID string) bool {
	switch modelID {
	case "mistral-small-2603", "mistral-small-latest", "mistral-medium-3.5":
		return true
	default:
		return false
	}
}

func MistralReasoningEffort(level string, levelMap map[string]*string) string {
	if mapped, ok := levelMap[level]; ok && mapped != nil && *mapped != "" {
		return *mapped
	}
	return "high"
}

func MistralExtraHeaders(modelHeaders, requestHeaders map[string]string, sessionID string) map[string]string {
	headers := map[string]string{}
	for key, value := range requestHeaders {
		headers[key] = value
	}
	if sessionID != "" && !HasHeaderName(modelHeaders, "x-affinity") && !HasHeaderName(headers, "x-affinity") {
		headers["x-affinity"] = sessionID
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

type MistralToolCallIDNormalizer struct {
	ids     map[string]string
	reverse map[string]string
}

func NewMistralToolCallIDNormalizer() *MistralToolCallIDNormalizer {
	return &MistralToolCallIDNormalizer{ids: map[string]string{}, reverse: map[string]string{}}
}

func (n *MistralToolCallIDNormalizer) Normalize(id string) string {
	if existing := n.ids[id]; existing != "" {
		return existing
	}
	for attempt := 0; ; attempt++ {
		candidate := DeriveMistralToolCallID(id, attempt)
		owner := n.reverse[candidate]
		if owner == "" || owner == id {
			n.ids[id] = candidate
			n.reverse[candidate] = id
			return candidate
		}
	}
}

func DeriveMistralToolCallID(id string, attempt int) string {
	normalized := MistralAlphaNum(id)
	if attempt == 0 && len(normalized) == 9 {
		return normalized
	}
	seedBase := normalized
	if seedBase == "" {
		seedBase = id
	}
	seed := seedBase
	if attempt != 0 {
		seed = fmt.Sprintf("%s:%d", seedBase, attempt)
	}
	hashed := MistralAlphaNum(MistralShortHash(seed))
	if len(hashed) > 9 {
		return hashed[:9]
	}
	return hashed
}

func MistralAlphaNum(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func MistralShortHash(s string) string {
	var h1 uint32 = 0xdeadbeef
	var h2 uint32 = 0x41c6ce57
	for _, r := range s {
		ch := uint32(r)
		h1 = mistralBitsMul(h1^ch, 2654435761)
		h2 = mistralBitsMul(h2^ch, 1597334677)
	}
	h1 = mistralBitsMul(h1^(h1>>16), 2246822507) ^ mistralBitsMul(h2^(h2>>13), 3266489909)
	h2 = mistralBitsMul(h2^(h2>>16), 2246822507) ^ mistralBitsMul(h1^(h1>>13), 3266489909)
	return strings.ToLower(mistralStrconv36(h2) + mistralStrconv36(h1))
}

func mistralBitsMul(a, b uint32) uint32 {
	return uint32(uint64(a) * uint64(b))
}

func mistralStrconv36(v uint32) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = alphabet[v%36]
		v /= 36
	}
	return string(buf[i:])
}
