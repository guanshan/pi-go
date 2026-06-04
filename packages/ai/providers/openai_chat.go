package providers

import (
	"encoding/json"
	"errors"
	"strings"

	openai "github.com/openai/openai-go/v3"
)

type PreparedOpenAIChatRequest struct {
	Key        string
	Headers    map[string]string
	Body       map[string]any
	BearerAuth bool
}

type OpenAIChatRequestOptions struct {
	ModelID                                     string
	Provider                                    string
	BaseURL                                     string
	SystemPrompt                                string
	Messages                                    []OpenAIChatMessage
	Tools                                       []map[string]any
	SupportsImages                              bool
	ModelHeaders                                map[string]string
	RequestHeaders                              map[string]string
	MaxTokens                                   int
	MaxOutput                                   int
	Temperature                                 *float64
	ToolChoice                                  any
	ThinkingLevel                               string
	ThinkingLevelMap                            map[string]*string
	Reasoning                                   bool
	CacheRetention                              string
	SessionID                                   string
	MaxTokensField                              string
	SupportsStore                               bool
	SupportsDeveloperRole                       bool
	SupportsReasoningEffort                     bool
	CacheControlFormat                          string
	SendSessionAffinityHeaders                  bool
	SupportsLongCacheRetention                  *bool
	RequiresToolResultName                      bool
	RequiresAssistantAfterToolResult            bool
	RequiresThinkingAsText                      bool
	RequiresReasoningContentOnAssistantMessages bool
	ThinkingFormat                              string
	ZaiToolStream                               bool
	SupportsStrictMode                          bool
	OpenRouterRouting                           map[string]any
	VercelGatewayRouting                        map[string]any
}

type OpenAIChatMessage struct {
	Role       string
	Text       string
	ToolCallID string
	ToolName   string
	IsError    bool
	Blocks     []OpenAIChatMessageBlock
}

type OpenAIChatMessageBlock struct {
	Type              string
	Text              string
	MimeType          string
	Data              string
	Thinking          string
	ID                string
	Name              string
	Arguments         json.RawMessage
	ThinkingSignature string
	ThoughtSignature  string
}

type OpenAIChatParsed struct {
	Blocks        []OpenAIChatBlock
	ToolCalls     []OpenAIChatToolCall
	Usage         OpenAIChatUsage
	StopReason    string
	ErrorMessage  string
	ResponseID    string
	ResponseModel string
}

type OpenAIChatBlock struct {
	Type              string
	Text              string
	Thinking          string
	ThinkingSignature string
	ID                string
	Name              string
	Arguments         json.RawMessage
	ThoughtSignature  string
}

type OpenAIChatToolCall struct {
	ID               string
	Name             string
	Arguments        json.RawMessage
	ThoughtSignature string
}

type OpenAIChatUsage struct {
	Input       int
	Output      int
	CacheRead   int
	CacheWrite  int
	TotalTokens int
}

func BuildOpenAIChatRequest(key string, options OpenAIChatRequestOptions) PreparedOpenAIChatRequest {
	messages := OpenAIChatMessages(options)
	body := map[string]any{
		"model":    options.ModelID,
		"messages": messages,
	}
	if options.SupportsStore {
		body["store"] = false
	}
	maxTokens := options.MaxTokens
	if maxTokens == 0 {
		maxTokens = options.MaxOutput
	}
	if maxTokens > 0 {
		if options.MaxTokensField == "max_completion_tokens" {
			body["max_completion_tokens"] = maxTokens
		} else {
			body["max_tokens"] = maxTokens
		}
	}
	if options.Temperature != nil {
		body["temperature"] = *options.Temperature
	}
	var tools []map[string]any
	if len(options.Tools) > 0 {
		tools = OpenAIChatTools(options.Tools, options.SupportsStrictMode)
		body["tools"] = tools
		if options.ToolChoice == nil {
			body["tool_choice"] = "auto"
		}
		if options.ZaiToolStream {
			body["tool_stream"] = true
		}
	} else if hasOpenAIChatToolHistory(options.Messages) {
		tools = []map[string]any{}
		body["tools"] = tools
	}
	if options.ToolChoice != nil {
		body["tool_choice"] = OpenAIToolChoice(options.ToolChoice)
	}
	cacheRetention := ResolveCacheRetention(options.CacheRetention)
	supportsLongCacheRetention := OpenAICompatSupportsLongCacheRetention(options.SupportsLongCacheRetention)
	if key := OpenAIPromptCacheKey(options.BaseURL, supportsLongCacheRetention, cacheRetention, options.SessionID); key != "" {
		body["prompt_cache_key"] = key
	}
	if cacheRetention == "long" && supportsLongCacheRetention {
		body["prompt_cache_retention"] = "24h"
	}
	cacheControlFormat := OpenAICompatCacheControlFormat(options.CacheControlFormat, options.Provider, options.ModelID)
	if cacheControl := OpenAICacheControl(cacheControlFormat, supportsLongCacheRetention, cacheRetention); cacheControl != nil {
		ApplyOpenAIAnthropicCacheControl(messages, tools, cacheControl)
	}
	if options.BaseURL != "" && strings.Contains(options.BaseURL, "openrouter.ai") && len(options.OpenRouterRouting) > 0 {
		body["provider"] = options.OpenRouterRouting
	}
	if options.BaseURL != "" && strings.Contains(options.BaseURL, "ai-gateway.vercel.sh") && len(options.VercelGatewayRouting) > 0 {
		body["providerOptions"] = map[string]any{"gateway": options.VercelGatewayRouting}
	}
	applyOpenAIChatThinking(body, options)
	headers := MergeHeaders(options.ModelHeaders, OpenAIExtraHeaders(cacheRetention, options.SessionID, options.SendSessionAffinityHeaders, options.RequestHeaders))
	if options.Provider == "github-copilot" {
		headers = MergeHeaders(headers, OpenAIChatCopilotHeaders(options.Messages))
	}
	bearerAuth := true
	switch options.Provider {
	case "azure-openai":
		bearerAuth = false
		headers = MergeHeaders(headers, map[string]string{"api-key": key})
	case "cloudflare-ai-gateway":
		bearerAuth = false
		headers = applyCloudflareGatewayAuthHeaders(headers, key)
	}
	return PreparedOpenAIChatRequest{Key: key, Headers: headers, Body: body, BearerAuth: bearerAuth}
}

func OpenAIChatMessages(options OpenAIChatRequestOptions) []map[string]any {
	out := []map[string]any{}
	if strings.TrimSpace(options.SystemPrompt) != "" {
		role := "system"
		if options.Reasoning && options.SupportsDeveloperRole {
			role = "developer"
		}
		out = append(out, map[string]any{"role": role, "content": options.SystemPrompt})
	}
	lastRole := ""
	for i := 0; i < len(options.Messages); i++ {
		msg := options.Messages[i]
		if options.RequiresAssistantAfterToolResult && lastRole == "toolResult" && msg.Role == "user" {
			out = append(out, map[string]any{"role": "assistant", "content": "I have processed the tool results."})
		}
		switch msg.Role {
		case "user":
			out = append(out, map[string]any{"role": "user", "content": openAIChatContent(msg)})
		case "assistant":
			// Default content mirrors TS openai-completions.ts: some providers reject
			// null content, so use "" when an assistant turn is forcibly inserted after
			// a tool result; otherwise default to JSON null. The default is overwritten
			// only when there is actual text/thinking content (see below).
			var defaultContent any
			if options.RequiresAssistantAfterToolResult {
				defaultContent = ""
			}
			m := map[string]any{"role": "assistant", "content": defaultContent}
			var toolCalls []map[string]any
			text := ""
			var textParts []map[string]any
			var thinkingTexts []string
			var replayThinkingTexts []string
			replayThinkingSignature := ""
			for _, b := range msg.Blocks {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						text += b.Text
						textParts = append(textParts, map[string]any{"type": "text", "text": SanitizeProviderText(b.Text)})
					}
				case "thinking":
					if options.RequiresThinkingAsText {
						if strings.TrimSpace(b.Thinking) != "" {
							thinkingTexts = append(thinkingTexts, SanitizeProviderText(b.Thinking))
						}
					} else if b.Thinking != "" {
						if len(replayThinkingTexts) == 0 {
							replayThinkingSignature = b.ThinkingSignature
							if options.Provider == "opencode-go" && replayThinkingSignature == "reasoning" {
								replayThinkingSignature = "reasoning_content"
							}
						}
						replayThinkingTexts = append(replayThinkingTexts, b.Thinking)
					}
				case "toolCall":
					toolCalls = append(toolCalls, map[string]any{
						"id":   b.ID,
						"type": "function",
						"function": map[string]any{
							"name":      b.Name,
							"arguments": string(b.Arguments),
						},
					})
				}
			}
			if options.RequiresThinkingAsText && len(thinkingTexts) > 0 {
				content := []map[string]any{{"type": "text", "text": strings.Join(thinkingTexts, "\n\n")}}
				content = append(content, textParts...)
				m["content"] = content
			} else if text != "" {
				// Match TS: only overwrite the default content (null / "") when there is
				// real text. An empty assistant turn keeps its null/"" default so that
				// providers which reject null vs. those which require it both behave as
				// in the TypeScript provider.
				m["content"] = text
			}
			if !options.RequiresThinkingAsText && replayThinkingSignature != "" && len(replayThinkingTexts) > 0 {
				m[replayThinkingSignature] = strings.Join(replayThinkingTexts, "\n")
			}
			if len(toolCalls) > 0 {
				m["tool_calls"] = toolCalls
			}
			var details []any
			for _, b := range msg.Blocks {
				if b.Type != "toolCall" || b.ThoughtSignature == "" {
					continue
				}
				var detail any
				if json.Unmarshal([]byte(b.ThoughtSignature), &detail) == nil {
					details = append(details, detail)
				}
			}
			if len(details) > 0 {
				m["reasoning_details"] = details
			}
			if options.RequiresReasoningContentOnAssistantMessages && options.Reasoning {
				if _, ok := m["reasoning_content"]; !ok {
					m["reasoning_content"] = ""
				}
			}
			// Skip assistant messages with no content and no tool calls. Some
			// providers reject empty assistant messages (e.g. aborted responses);
			// messages carrying reasoning_content but real text/tool calls are kept.
			if !openAIChatMessageHasContent(m["content"]) && len(toolCalls) == 0 {
				continue
			}
			out = append(out, m)
		case "toolResult":
			j := i
			var imageParts []map[string]any
			for ; j < len(options.Messages) && options.Messages[j].Role == "toolResult"; j++ {
				toolMsg := options.Messages[j]
				text := openAIChatToolResultText(toolMsg)
				toolResult := map[string]any{"role": "tool", "tool_call_id": toolMsg.ToolCallID, "content": text}
				if options.RequiresToolResultName && toolMsg.ToolName != "" {
					toolResult["name"] = toolMsg.ToolName
				}
				out = append(out, toolResult)
				for _, block := range toolMsg.Blocks {
					if options.SupportsImages && block.Type == "image" {
						imageParts = append(imageParts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": DataURL(block.MimeType, block.Data)}})
					}
				}
			}
			i = j - 1
			if len(imageParts) > 0 {
				if options.RequiresAssistantAfterToolResult {
					out = append(out, map[string]any{"role": "assistant", "content": "I have processed the tool results."})
				}
				content := append([]map[string]any{{"type": "text", "text": "Attached image(s) from tool result:"}}, imageParts...)
				out = append(out, map[string]any{"role": "user", "content": content})
				lastRole = "user"
				continue
			}
			lastRole = "toolResult"
			continue
		case "compactionSummary", "branchSummary", "custom":
			out = append(out, map[string]any{"role": "user", "content": msg.Text})
		}
		lastRole = msg.Role
	}
	return out
}

func openAIChatMessageHasContent(content any) bool {
	switch value := content.(type) {
	case nil:
		return false
	case string:
		return len(value) > 0
	case []map[string]any:
		return len(value) > 0
	case []any:
		return len(value) > 0
	default:
		return true
	}
}

func openAIChatContent(msg OpenAIChatMessage) any {
	if len(msg.Blocks) == 0 {
		return msg.Text
	}
	var out []map[string]any
	for _, b := range msg.Blocks {
		switch b.Type {
		case "text":
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case "image":
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": DataURL(b.MimeType, b.Data)}})
		}
	}
	if len(out) == 1 && out[0]["type"] == "text" {
		return out[0]["text"]
	}
	return out
}

func OpenAIChatTools(defs []map[string]any, supportsStrict bool) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		function := map[string]any{
			"name":        d["name"],
			"description": d["description"],
			"parameters":  d["parameters"],
		}
		if supportsStrict {
			function["strict"] = false
		}
		out = append(out, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return out
}

func hasOpenAIChatToolHistory(messages []OpenAIChatMessage) bool {
	for _, msg := range messages {
		if msg.Role == "toolResult" {
			return true
		}
		for _, block := range msg.Blocks {
			if block.Type == "toolCall" {
				return true
			}
		}
	}
	return false
}

func openAIChatToolResultText(msg OpenAIChatMessage) string {
	var parts []string
	hasImage := false
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "image":
			hasImage = true
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" && hasImage {
		return "(see attached image)"
	}
	return text
}

// applyCloudflareGatewayAuthHeaders rewrites auth headers for the
// cloudflare-ai-gateway path. The gateway authenticates with
// cf-aig-authorization: Bearer <apiKey>, while any upstream Authorization
// header (BYOK) is preserved as-is and left unset when absent (BearerAuth is
// disabled so the gateway key is not sent as the upstream Authorization).
func applyCloudflareGatewayAuthHeaders(headers map[string]string, key string) map[string]string {
	out := map[string]string{}
	for k, v := range headers {
		out[k] = v
	}
	ApplyCloudflareGatewayAuthHeadersInPlace(out, key)
	return out
}

// ApplyCloudflareGatewayAuthHeadersInPlace mutates headers for the
// cloudflare-ai-gateway path: any upstream Authorization header (BYOK) is kept,
// otherwise Authorization is left unset, and cf-aig-authorization carries the
// gateway key.
func ApplyCloudflareGatewayAuthHeadersInPlace(headers map[string]string, key string) {
	headers["cf-aig-authorization"] = "Bearer " + key
}

func OpenAIChatCopilotHeaders(messages []OpenAIChatMessage) map[string]string {
	initiator := "user"
	if len(messages) > 0 && messages[len(messages)-1].Role != "user" {
		initiator = "agent"
	}
	headers := map[string]string{
		"X-Initiator":   initiator,
		"Openai-Intent": "conversation-edits",
	}
	for _, msg := range messages {
		if msg.Role != "user" && msg.Role != "toolResult" {
			continue
		}
		for _, block := range msg.Blocks {
			if block.Type == "image" {
				headers["Copilot-Vision-Request"] = "true"
				return headers
			}
		}
	}
	return headers
}

func applyOpenAIChatThinking(body map[string]any, options OpenAIChatRequestOptions) {
	if !options.Reasoning {
		return
	}
	effort := ""
	if options.ThinkingLevel != "" && options.ThinkingLevel != "off" {
		effort = openAIChatThinkingEffort(options, options.ThinkingLevel)
	}
	switch options.ThinkingFormat {
	case "zai", "qwen":
		body["enable_thinking"] = effort != ""
	case "qwen-chat-template":
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": effort != "", "preserve_thinking": true}
	case "deepseek":
		if effort != "" {
			body["thinking"] = map[string]any{"type": "enabled"}
			body["reasoning_effort"] = effort
		} else {
			body["thinking"] = map[string]any{"type": "disabled"}
		}
	case "openrouter":
		if effort != "" {
			body["reasoning"] = map[string]any{"effort": effort}
		} else if off, ok := openAIChatThinkingMapValue(options, "off"); ok {
			body["reasoning"] = map[string]any{"effort": off}
		} else if _, exists := options.ThinkingLevelMap["off"]; !exists {
			body["reasoning"] = map[string]any{"effort": "none"}
		}
	case "ant-ling":
		// ant-ling only emits reasoning when an effort was requested AND the
		// model's thinkingLevelMap maps it to a string. Mirrors
		// openai-completions.ts: reasoning = { effort } only when typeof
		// model.thinkingLevelMap?.[reasoningEffort] === "string".
		if effort != "" {
			if mapped, ok := openAIChatThinkingMapValue(options, options.ThinkingLevel); ok {
				body["reasoning"] = map[string]any{"effort": mapped}
			}
		}
	case "together":
		body["reasoning"] = map[string]any{"enabled": effort != ""}
		if effort != "" && options.SupportsReasoningEffort {
			body["reasoning_effort"] = effort
		}
	default:
		if effort != "" && options.SupportsReasoningEffort {
			body["reasoning_effort"] = effort
		} else if effort == "" && options.SupportsReasoningEffort {
			if off, ok := openAIChatThinkingMapValue(options, "off"); ok {
				body["reasoning_effort"] = off
			}
		}
	}
}

func openAIChatThinkingEffort(options OpenAIChatRequestOptions, level string) string {
	if mapped, ok := openAIChatThinkingMapValue(options, level); ok {
		return mapped
	}
	return ReasoningEffort(level)
}

func openAIChatThinkingMapValue(options OpenAIChatRequestOptions, level string) (string, bool) {
	if options.ThinkingLevelMap == nil {
		return "", false
	}
	mapped, ok := options.ThinkingLevelMap[level]
	if !ok || mapped == nil {
		return "", false
	}
	return *mapped, true
}

func OpenAIChatCompletionResponse(resp *openai.ChatCompletion) (OpenAIChatParsed, error) {
	if resp == nil || len(resp.Choices) == 0 {
		return OpenAIChatParsed{}, errors.New("empty OpenAI response")
	}
	if raw := resp.RawJSON(); raw != "" {
		if parsed, err := ParseOpenAIChatCompletionRaw([]byte(raw)); err == nil {
			return parsed, nil
		}
	}
	choice := resp.Choices[0]
	stopReason, errorMessage := OpenAIChatStopReason(choice.FinishReason, len(choice.Message.ToolCalls) > 0)
	parsed := OpenAIChatParsed{
		Usage: OpenAIChatUsage{
			Input:       MaxInt(0, int(resp.Usage.PromptTokens-resp.Usage.PromptTokensDetails.CachedTokens)),
			Output:      int(resp.Usage.CompletionTokens),
			CacheRead:   int(resp.Usage.PromptTokensDetails.CachedTokens),
			TotalTokens: int(resp.Usage.TotalTokens),
		},
		StopReason:    stopReason,
		ErrorMessage:  errorMessage,
		ResponseID:    resp.ID,
		ResponseModel: resp.Model,
	}
	if choice.Message.Content != "" {
		parsed.Blocks = append(parsed.Blocks, OpenAIChatBlock{Type: "text", Text: choice.Message.Content})
	} else if choice.Message.Refusal != "" {
		parsed.Blocks = append(parsed.Blocks, OpenAIChatBlock{Type: "text", Text: choice.Message.Refusal})
	}
	for _, tc := range choice.Message.ToolCalls {
		if tc.Type != "" && tc.Type != "function" {
			continue
		}
		id := tc.ID
		if id == "" {
			id = ShortID()
		}
		args := NormalizeToolArguments(json.RawMessage(tc.Function.Arguments))
		parsed.Blocks = append(parsed.Blocks, OpenAIChatBlock{Type: "toolCall", ID: id, Name: tc.Function.Name, Arguments: args})
		parsed.ToolCalls = append(parsed.ToolCalls, OpenAIChatToolCall{ID: id, Name: tc.Function.Name, Arguments: args})
	}
	if len(parsed.ToolCalls) > 0 && parsed.StopReason == "stop" {
		parsed.StopReason = "toolUse"
	}
	return parsed, nil
}

func ParseOpenAIChatCompletionRaw(raw []byte) (OpenAIChatParsed, error) {
	return ParseOpenAIChatCompletionRawForProvider(raw, "")
}

func ParseOpenAIChatCompletionRawForProvider(raw []byte, provider string) (OpenAIChatParsed, error) {
	var parsed struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          any               `json:"content"`
				ReasoningContent string            `json:"reasoning_content"`
				Reasoning        string            `json:"reasoning"`
				ReasoningText    string            `json:"reasoning_text"`
				ReasoningDetails []json.RawMessage `json:"reasoning_details"`
				ToolCalls        []struct {
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
			PromptTokens         int `json:"prompt_tokens"`
			CompletionTokens     int `json:"completion_tokens"`
			TotalTokens          int `json:"total_tokens"`
			PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
			PromptTokensDetails  struct {
				CachedTokens     int `json:"cached_tokens"`
				CacheWriteTokens int `json:"cache_write_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return OpenAIChatParsed{}, err
	}
	if parsed.Error != nil {
		return OpenAIChatParsed{}, errors.New(parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return OpenAIChatParsed{}, errors.New("empty OpenAI response")
	}
	choice := parsed.Choices[0]
	stopReason, errorMessage := OpenAIChatStopReason(choice.FinishReason, len(choice.Message.ToolCalls) > 0)
	out := OpenAIChatParsed{
		Usage:         openAIChatParsedUsage(parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens, parsed.Usage.TotalTokens, parsed.Usage.PromptTokensDetails.CachedTokens, parsed.Usage.PromptTokensDetails.CacheWriteTokens, parsed.Usage.PromptCacheHitTokens),
		StopReason:    stopReason,
		ErrorMessage:  errorMessage,
		ResponseID:    parsed.ID,
		ResponseModel: parsed.Model,
	}
	if text := ContentToString(choice.Message.Content); text != "" {
		out.Blocks = append(out.Blocks, OpenAIChatBlock{Type: "text", Text: text})
	}
	if thinking, signature := OpenAIChatReasoningText(choice.Message.ReasoningContent, choice.Message.Reasoning, choice.Message.ReasoningText, provider); thinking != "" {
		out.Blocks = append([]OpenAIChatBlock{{Type: "thinking", Thinking: thinking, ThinkingSignature: signature}}, out.Blocks...)
	}
	reasoningDetails := OpenAIChatReasoningDetails(choice.Message.ReasoningDetails)
	for _, tc := range choice.Message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = ShortID()
		}
		args := NormalizeToolArguments(tc.Function.Arguments)
		thoughtSignature := reasoningDetails[id]
		out.Blocks = append(out.Blocks, OpenAIChatBlock{Type: "toolCall", ID: id, Name: tc.Function.Name, Arguments: args, ThoughtSignature: thoughtSignature})
		out.ToolCalls = append(out.ToolCalls, OpenAIChatToolCall{ID: id, Name: tc.Function.Name, Arguments: args, ThoughtSignature: thoughtSignature})
	}
	if len(out.ToolCalls) > 0 && out.StopReason == "stop" {
		out.StopReason = "toolUse"
	}
	return out, nil
}

func OpenAIChatReasoningText(reasoningContent, reasoning, reasoningText, provider string) (string, string) {
	switch {
	case reasoningContent != "":
		return reasoningContent, "reasoning_content"
	case reasoning != "":
		if provider == "opencode-go" {
			return reasoning, "reasoning_content"
		}
		return reasoning, "reasoning"
	case reasoningText != "":
		return reasoningText, "reasoning_text"
	default:
		return "", ""
	}
}

func OpenAIChatReasoningDetails(rawDetails []json.RawMessage) map[string]string {
	out := map[string]string{}
	for _, raw := range rawDetails {
		var detail struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Data string `json:"data"`
		}
		if json.Unmarshal(raw, &detail) != nil || detail.Type != "reasoning.encrypted" || detail.ID == "" || detail.Data == "" {
			continue
		}
		out[detail.ID] = string(raw)
	}
	return out
}

func OpenAIChatStopReason(reason string, hasToolCall bool) (string, string) {
	switch reason {
	case "", "stop", "end":
		if hasToolCall {
			return "toolUse", ""
		}
		return "stop", ""
	case "length":
		return "length", ""
	case "function_call", "tool_calls":
		return "toolUse", ""
	case "content_filter", "network_error":
		return "error", "Provider finish_reason: " + reason
	default:
		return "error", "Provider finish_reason: " + reason
	}
}

func openAIChatParsedUsage(promptTokens, completionTokens, totalTokens, cachedTokens, cacheWriteTokens, promptCacheHitTokens int) OpenAIChatUsage {
	if cachedTokens == 0 {
		cachedTokens = promptCacheHitTokens
	}
	input := MaxInt(0, promptTokens-cachedTokens-cacheWriteTokens)
	if totalTokens == 0 {
		totalTokens = input + completionTokens + cachedTokens + cacheWriteTokens
	}
	return OpenAIChatUsage{
		Input:       input,
		Output:      completionTokens,
		CacheRead:   cachedTokens,
		CacheWrite:  cacheWriteTokens,
		TotalTokens: totalTokens,
	}
}

// OpenAIChatUsageFromValues applies the shared OpenAI usage accounting rules
// (DeepSeek prompt_cache_hit_tokens fallback, cache_write subtraction, total
// derivation) to already-extracted token counts. Exported so the streaming
// accumulator computes usage identically to the non-streaming path.
func OpenAIChatUsageFromValues(promptTokens, completionTokens, totalTokens, cachedTokens, cacheWriteTokens, promptCacheHitTokens int) OpenAIChatUsage {
	return openAIChatParsedUsage(promptTokens, completionTokens, totalTokens, cachedTokens, cacheWriteTokens, promptCacheHitTokens)
}

// OpenAIChatStreamUsageFromRaw parses a streaming chunk's top-level `usage`
// object (the chunk's raw JSON) into OpenAIChatUsage using exactly the same rules
// as the non-streaming path. Crucially this includes the non-standard
// cache_write_tokens (OpenRouter/DS4) and the DeepSeek-style
// prompt_cache_hit_tokens fallback, both of which the typed SDK chunk struct does
// not expose. Mirrors parseChunkUsage in openai-completions.ts. The second return
// value reports whether a usage object was present.
func OpenAIChatStreamUsageFromRaw(raw []byte) (OpenAIChatUsage, bool) {
	var parsed struct {
		Usage *struct {
			PromptTokens         int `json:"prompt_tokens"`
			CompletionTokens     int `json:"completion_tokens"`
			TotalTokens          int `json:"total_tokens"`
			PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
			PromptTokensDetails  struct {
				CachedTokens     int `json:"cached_tokens"`
				CacheWriteTokens int `json:"cache_write_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Usage == nil {
		return OpenAIChatUsage{}, false
	}
	u := parsed.Usage
	return openAIChatParsedUsage(u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.PromptTokensDetails.CachedTokens, u.PromptTokensDetails.CacheWriteTokens, u.PromptCacheHitTokens), true
}
