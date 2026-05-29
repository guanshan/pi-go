package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

type OpenAIResponsesParsed struct {
	Blocks        []OpenAIResponsesBlock
	ToolCalls     []OpenAIResponsesToolCall
	Usage         OpenAIResponsesUsage
	StopReason    string
	ErrorMessage  string
	ResponseID    string
	ResponseModel string
	ServiceTier   string
}

type OpenAIResponsesBlock struct {
	Type              string
	Text              string
	Thinking          string
	ThinkingSignature string
	RawItem           json.RawMessage
	TextSignature     string
	ID                string
	Name              string
	Arguments         json.RawMessage
	Data              string
}

type OpenAIResponsesToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type OpenAIResponsesUsage struct {
	Input       int
	Output      int
	CacheRead   int
	TotalTokens int
}

type OpenAIResponsesRequestOptions struct {
	API                          string
	Provider                     string
	ModelID                      string
	BaseURL                      string
	SystemPrompt                 string
	Messages                     []OpenAIResponsesMessage
	Tools                        []map[string]any
	ModelHeaders                 map[string]string
	RequestHeaders               map[string]string
	CacheRetention               string
	SessionID                    string
	MaxTokens                    int
	MaxOutput                    int
	Temperature                  *float64
	Reasoning                    bool
	ThinkingLevel                string
	ThinkingLevelMap             map[string]*string
	ReasoningSummary             string
	ServiceTier                  string
	TextVerbosity                string
	ToolChoice                   any
	SupportsLongCacheRetention   *bool
	SendSessionIDHeader          *bool
	SupportsImageToolResultInput bool
}

type OpenAIResponsesMessage struct {
	Role       string
	Text       string
	ToolCallID string
	Blocks     []OpenAIResponsesMessageBlock
	API        string
	Provider   string
	Model      string
}

type OpenAIResponsesMessageBlock struct {
	Type              string
	Text              string
	Data              string
	MimeType          string
	ThinkingSignature string
	RawItem           json.RawMessage
	TextSignature     string
	ID                string
	Name              string
	Arguments         json.RawMessage
}

func OpenAIResponsesBody(options OpenAIResponsesRequestOptions) map[string]any {
	switch options.API {
	case "openai-codex-responses":
		return openAICodexResponsesBody(options)
	default:
		return openAIStandardResponsesBody(options)
	}
}

func openAIStandardResponsesBody(options OpenAIResponsesRequestOptions) map[string]any {
	body := map[string]any{
		"model":  options.ModelID,
		"input":  ResponsesMessages(options, true),
		"stream": false,
	}
	if options.API == "openai-responses" {
		body["store"] = false
	}
	if options.API == "azure-openai-responses" {
		body["model"] = AzureResponsesDeploymentName(options.ModelID)
	}
	cacheRetention := ResolveCacheRetention(options.CacheRetention)
	if cacheRetention != "none" && options.SessionID != "" {
		body["prompt_cache_key"] = ClampOpenAIPromptCacheKey(options.SessionID)
		if options.API == "openai-responses" && cacheRetention == "long" && OpenAICompatSupportsLongCacheRetention(options.SupportsLongCacheRetention) {
			body["prompt_cache_retention"] = "24h"
		}
	}
	if options.MaxTokens > 0 {
		body["max_output_tokens"] = options.MaxTokens
	} else if options.MaxOutput > 0 {
		body["max_output_tokens"] = options.MaxOutput
	}
	if options.Temperature != nil {
		body["temperature"] = *options.Temperature
	}
	if len(options.Tools) > 0 {
		body["tools"] = ResponsesTools(options.Tools, false)
	}
	if options.ServiceTier != "" {
		body["service_tier"] = options.ServiceTier
	}
	if options.ToolChoice != nil {
		body["tool_choice"] = ResponsesToolChoice(options.ToolChoice)
	}
	if reasoning := ResponsesReasoning(options); reasoning != nil {
		body["reasoning"] = reasoning
		if ResponsesReasoningIncludesEncryptedContent(options) {
			body["include"] = []string{"reasoning.encrypted_content"}
		}
	}
	return body
}

func openAICodexResponsesBody(options OpenAIResponsesRequestOptions) map[string]any {
	instructions := options.SystemPrompt
	if strings.TrimSpace(instructions) == "" {
		instructions = "You are a helpful assistant."
	}
	body := map[string]any{
		"model":               options.ModelID,
		"store":               false,
		"stream":              true,
		"instructions":        instructions,
		"input":               ResponsesMessages(OpenAIResponsesRequestOptions{Messages: options.Messages, SupportsImageToolResultInput: options.SupportsImageToolResultInput}, false),
		"text":                map[string]any{"verbosity": CodexTextVerbosity(options.TextVerbosity)},
		"include":             []string{"reasoning.encrypted_content"},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}
	if options.SessionID != "" {
		body["prompt_cache_key"] = ClampOpenAIPromptCacheKey(options.SessionID)
	}
	if options.Temperature != nil {
		body["temperature"] = *options.Temperature
	}
	if options.ServiceTier != "" {
		body["service_tier"] = options.ServiceTier
	}
	if len(options.Tools) > 0 {
		body["tools"] = ResponsesTools(options.Tools, nil)
	}
	if reasoning := ResponsesReasoning(options); reasoning != nil {
		body["reasoning"] = reasoning
	}
	return body
}

func CodexTextVerbosity(value string) string {
	switch value {
	case "medium", "high":
		return value
	default:
		return "low"
	}
}

func ResponsesReasoning(options OpenAIResponsesRequestOptions) map[string]any {
	if !options.Reasoning {
		return nil
	}
	if options.API == "openai-codex-responses" {
		if options.ThinkingLevel == "" {
			return nil
		}
		effort := ResponsesReasoningEffort(options.ThinkingLevelMap, options.ThinkingLevel)
		if options.ThinkingLevel == "off" {
			effort = ResponsesReasoningOffEffort(options.ThinkingLevelMap)
		}
		if effort == "" {
			return nil
		}
		return map[string]any{"effort": effort, "summary": ResponsesReasoningSummary(options.ReasoningSummary)}
	}
	if options.ThinkingLevel == "" || options.ThinkingLevel == "off" {
		if options.ReasoningSummary != "" && options.ThinkingLevel != "off" {
			return map[string]any{
				"effort":  ResponsesReasoningEffort(options.ThinkingLevelMap, "medium"),
				"summary": ResponsesReasoningSummary(options.ReasoningSummary),
			}
		}
		if options.Provider == "github-copilot" {
			return nil
		}
		effort := ResponsesReasoningOffEffort(options.ThinkingLevelMap)
		if effort == "" {
			return nil
		}
		return map[string]any{"effort": effort}
	}
	return map[string]any{
		"effort":  ResponsesReasoningEffort(options.ThinkingLevelMap, options.ThinkingLevel),
		"summary": ResponsesReasoningSummary(options.ReasoningSummary),
	}
}

func ResponsesReasoningIncludesEncryptedContent(options OpenAIResponsesRequestOptions) bool {
	return (options.ThinkingLevel != "" && options.ThinkingLevel != "off") || (options.ReasoningSummary != "" && options.ThinkingLevel != "off")
}

func ResponsesReasoningSummary(summary string) string {
	if summary == "" {
		return "auto"
	}
	return summary
}

func ResponsesReasoningOffEffort(levelMap map[string]*string) string {
	if mapped, ok := levelMap["off"]; ok {
		if mapped == nil {
			return ""
		}
		if *mapped == "" {
			return "none"
		}
		return *mapped
	}
	return "none"
}

func OpenAIResponsesRequest(options OpenAIResponsesRequestOptions, key string) (string, map[string]string, error) {
	switch options.API {
	case "azure-openai-responses":
		headers := RequestHeaders(options.ModelHeaders, options.RequestHeaders)
		headers["api-key"] = key
		return AzureResponsesURL(options.BaseURL), headers, nil
	case "openai-codex-responses":
		headers, err := CodexResponsesHeaders(options, key)
		if err != nil {
			return "", nil, err
		}
		return CodexResponsesURL(options.BaseURL), headers, nil
	default:
		headers := RequestHeaders(options.ModelHeaders, options.RequestHeaders)
		headers["Authorization"] = "Bearer " + key
		cacheRetention := ResolveCacheRetention(options.CacheRetention)
		if cacheRetention != "none" && options.SessionID != "" {
			if OpenAIResponsesSendSessionIDHeader(options.SendSessionIDHeader) {
				headers["session_id"] = options.SessionID
			}
			headers["x-client-request-id"] = options.SessionID
		}
		return OpenAIResponsesURL(options.BaseURL), headers, nil
	}
}

func CodexResponsesHeaders(options OpenAIResponsesRequestOptions, token string) (map[string]string, error) {
	accountID, err := ExtractCodexAccountID(token)
	if err != nil {
		return nil, err
	}
	headers := RequestHeaders(options.ModelHeaders, options.RequestHeaders)
	headers["Authorization"] = "Bearer " + token
	headers["chatgpt-account-id"] = accountID
	headers["originator"] = "pi"
	headers["OpenAI-Beta"] = "responses=experimental"
	headers["accept"] = "text/event-stream"
	headers["content-type"] = "application/json"
	if options.SessionID != "" {
		headers["session-id"] = options.SessionID
		headers["x-client-request-id"] = options.SessionID
	}
	return headers, nil
}

func ResponsesMessages(options OpenAIResponsesRequestOptions, includeSystem bool) []map[string]any {
	out := []map[string]any{}
	if includeSystem && strings.TrimSpace(options.SystemPrompt) != "" {
		role := "system"
		if options.Reasoning {
			role = "developer"
		}
		out = append(out, map[string]any{"role": role, "content": SanitizeProviderText(options.SystemPrompt)})
	}
	messageIndex := 0
	for _, msg := range options.Messages {
		switch msg.Role {
		case "user", "compactionSummary", "branchSummary", "custom":
			if mapped, ok := ResponsesUserMessage(msg); ok {
				out = append(out, mapped)
			}
		case "assistant":
			out = append(out, ResponsesAssistantItems(options, msg, messageIndex)...)
		case "toolResult":
			out = append(out, ResponsesToolResultMessage(options, msg))
		}
		messageIndex++
	}
	return out
}

func ResponsesUserMessage(msg OpenAIResponsesMessage) (map[string]any, bool) {
	if msg.Text != "" && len(msg.Blocks) == 0 {
		return map[string]any{"role": "user", "content": []map[string]any{{"type": "input_text", "text": SanitizeProviderText(msg.Text)}}}, true
	}
	if len(msg.Blocks) == 0 {
		return map[string]any{"role": "user", "content": []map[string]any{{"type": "input_text", "text": SanitizeProviderText(msg.Text)}}}, true
	}
	content := []map[string]any{}
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			content = append(content, map[string]any{"type": "input_text", "text": SanitizeProviderText(block.Text)})
		case "image":
			content = append(content, map[string]any{"type": "input_image", "detail": "auto", "image_url": DataURL(block.MimeType, block.Data)})
		}
	}
	if len(content) == 0 {
		return nil, false
	}
	return map[string]any{"role": "user", "content": content}, true
}

func ResponsesAssistantItems(options OpenAIResponsesRequestOptions, msg OpenAIResponsesMessage, messageIndex int) []map[string]any {
	out := []map[string]any{}
	isDifferentModel := msg.Model != "" && msg.Model != options.ModelID && msg.Provider == options.Provider && msg.API == options.API
	for _, block := range msg.Blocks {
		switch block.Type {
		case "thinking":
			if len(block.RawItem) > 0 {
				var item map[string]any
				if json.Unmarshal(block.RawItem, &item) == nil {
					out = append(out, item)
				}
			} else if block.ThinkingSignature != "" {
				var item map[string]any
				if json.Unmarshal([]byte(block.ThinkingSignature), &item) == nil {
					out = append(out, item)
				}
			}
		case "text":
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			out = append(out, map[string]any{
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"id":     ResponsesTextMessageID(block.TextSignature, messageIndex),
				"content": []map[string]any{{
					"type":        "output_text",
					"text":        SanitizeProviderText(block.Text),
					"annotations": []any{},
				}},
			})
		case "toolCall":
			callID, itemID := ResponsesToolCallIDParts(block.ID)
			item := map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      block.Name,
				"arguments": MistralRequestArguments(block.Arguments),
			}
			if itemID != "" && !isDifferentModel {
				item["id"] = itemID
			}
			out = append(out, item)
		}
	}
	return out
}

func ResponsesToolChoice(choice any) any {
	switch value := choice.(type) {
	case nil:
		return nil
	case string:
		return value
	case map[string]any, map[string]string:
		typ := ToolChoiceType(value)
		name := ToolChoiceName(value)
		if name != "" && (typ == "tool" || typ == "function" || typ == "") {
			return map[string]any{"type": "function", "name": name}
		}
	}
	return choice
}

func ResponsesToolResultMessage(options OpenAIResponsesRequestOptions, msg OpenAIResponsesMessage) map[string]any {
	callID, _ := ResponsesToolCallIDParts(msg.ToolCallID)
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
	text := strings.Join(textParts, "\n")
	var output any
	if hasImages && options.SupportsImageToolResultInput {
		content := []map[string]any{}
		if text != "" {
			content = append(content, map[string]any{"type": "input_text", "text": text})
		}
		for _, block := range msg.Blocks {
			if block.Type == "image" {
				content = append(content, map[string]any{"type": "input_image", "detail": "auto", "image_url": DataURL(block.MimeType, block.Data)})
			}
		}
		output = content
	} else if text != "" {
		output = text
	} else {
		output = "(see attached image)"
	}
	return map[string]any{"type": "function_call_output", "call_id": callID, "output": output}
}

func ResponsesTools(defs []map[string]any, strict any) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		out = append(out, map[string]any{
			"type":        "function",
			"name":        d["name"],
			"description": d["description"],
			"parameters":  d["parameters"],
			"strict":      strict,
		})
	}
	return out
}

type ResponseObject struct {
	ID          string         `json:"id"`
	Model       string         `json:"model"`
	Status      string         `json:"status"`
	Output      []ResponseItem `json:"output"`
	Usage       ResponseUsage  `json:"usage"`
	ServiceTier string         `json:"service_tier"`
	Error       *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type ResponseItem struct {
	Type      string                `json:"type"`
	ID        string                `json:"id"`
	Role      string                `json:"role"`
	Status    string                `json:"status"`
	Phase     string                `json:"phase"`
	Content   []ResponseContentPart `json:"content"`
	Summary   []ResponseContentPart `json:"summary"`
	CallID    string                `json:"call_id"`
	Name      string                `json:"name"`
	Arguments json.RawMessage       `json:"arguments"`
	Raw       map[string]any        `json:"-"`
}

func (i *ResponseItem) UnmarshalJSON(data []byte) error {
	type alias ResponseItem
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	*i = ResponseItem(out)
	i.Raw = raw
	return nil
}

type ResponseContentPart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type ResponseUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func ParseOpenAIResponses(raw []byte) (OpenAIResponsesParsed, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.HasPrefix(trimmed, []byte("data:")) || bytes.Contains(trimmed, []byte("\ndata:")) {
		return ParseOpenAIResponsesEvents(trimmed)
	}
	var response ResponseObject
	if err := json.Unmarshal(trimmed, &response); err != nil {
		return OpenAIResponsesParsed{}, err
	}
	return response.ToParsed()
}

func ParseOpenAIResponsesEvents(raw []byte) (OpenAIResponsesParsed, error) {
	response := ResponseObject{Status: "completed"}
	itemIndex := map[string]int{}
	for _, event := range aiutils.ParseSSEEvents(raw) {
		eventType, _ := event["type"].(string)
		switch eventType {
		case "error":
			message, _ := event["message"].(string)
			code, _ := event["code"].(string)
			if message == "" {
				message = code
			}
			return OpenAIResponsesParsed{}, errors.New(message)
		case "response.created":
			if res, ok := event["response"].(map[string]any); ok {
				if id, ok := res["id"].(string); ok {
					response.ID = id
				}
			}
		case "response.output_item.added":
			if item, ok := ResponseItemFromEvent(event["item"]); ok {
				itemIndex[item.ID] = len(response.Output)
				response.Output = append(response.Output, item)
			}
		case "response.output_item.done":
			if item, ok := ResponseItemFromEvent(event["item"]); ok {
				if index, found := itemIndex[item.ID]; found {
					response.Output[index] = item
				} else {
					itemIndex[item.ID] = len(response.Output)
					response.Output = append(response.Output, item)
				}
			}
		case "response.completed", "response.done", "response.incomplete":
			if res, ok := ResponseObjectFromEvent(event["response"]); ok {
				res.Output = MergeResponseOutput(response.Output, res.Output)
				response = res
			}
		case "response.failed":
			if res, ok := ResponseObjectFromEvent(event["response"]); ok {
				return res.ToParsed()
			}
			return OpenAIResponsesParsed{}, errors.New("OpenAI response failed")
		}
	}
	return response.ToParsed()
}

func (r ResponseObject) ToParsed() (OpenAIResponsesParsed, error) {
	if r.Error != nil {
		return OpenAIResponsesParsed{}, errors.New(ResponseFailureMessage(r))
	}
	if r.Status == "failed" {
		return OpenAIResponsesParsed{}, errors.New(ResponseFailureMessage(r))
	}
	stopReason, errorMessage := ResponsesStopReasonResult(r.Status)
	parsed := OpenAIResponsesParsed{
		Usage:         ResponseUsageToUsage(r.Usage),
		StopReason:    stopReason,
		ErrorMessage:  errorMessage,
		ResponseID:    r.ID,
		ResponseModel: r.Model,
		ServiceTier:   r.ServiceTier,
	}
	for _, item := range r.Output {
		switch item.Type {
		case "reasoning":
			text := ResponseReasoningText(item)
			if text == "" {
				continue
			}
			raw, _ := json.Marshal(item.Raw)
			parsed.Blocks = append(parsed.Blocks, OpenAIResponsesBlock{Type: "thinking", Thinking: text, RawItem: raw})
		case "message":
			text := ResponseMessageText(item)
			if text == "" {
				continue
			}
			parsed.Blocks = append(parsed.Blocks, OpenAIResponsesBlock{Type: "text", Text: text, TextSignature: EncodeResponsesTextSignature(item.ID, item.Phase)})
		case "function_call":
			id := item.CallID
			if item.ID != "" {
				id += "|" + item.ID
			}
			args := MistralNormalizeToolArguments(item.Arguments)
			parsed.Blocks = append(parsed.Blocks, OpenAIResponsesBlock{Type: "toolCall", ID: id, Name: item.Name, Arguments: args})
			parsed.ToolCalls = append(parsed.ToolCalls, OpenAIResponsesToolCall{ID: id, Name: item.Name, Arguments: args})
		}
	}
	if len(parsed.ToolCalls) > 0 && parsed.StopReason == "stop" {
		parsed.StopReason = "toolUse"
	}
	return parsed, nil
}

func ResponseFailureMessage(response ResponseObject) string {
	if response.Error != nil {
		code := response.Error.Code
		if code == "" {
			code = "unknown"
		}
		message := response.Error.Message
		if message == "" {
			message = "no message"
		}
		return code + ": " + message
	}
	if response.IncompleteDetails != nil && response.IncompleteDetails.Reason != "" {
		return "incomplete: " + response.IncompleteDetails.Reason
	}
	return "OpenAI response failed"
}

func ResponseReasoningText(item ResponseItem) string {
	if text := responsePartsText(item.Summary); text != "" {
		return text
	}
	return responsePartsText(item.Content)
}

func ResponseMessageText(item ResponseItem) string {
	return responsePartsText(item.Content)
}

func responsePartsText(parts []ResponseContentPart) string {
	var out []string
	for _, part := range parts {
		switch part.Type {
		case "output_text":
			out = append(out, part.Text)
		case "refusal":
			out = append(out, part.Refusal)
		case "summary_text", "reasoning_text", "text":
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "")
}

func EncodeResponsesTextSignature(id, phase string) string {
	if id == "" {
		return ""
	}
	payload := map[string]any{"v": 1, "id": id}
	if phase == "commentary" || phase == "final_answer" {
		payload["phase"] = phase
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func ResponseItemFromEvent(value any) (ResponseItem, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return ResponseItem{}, false
	}
	var item ResponseItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return ResponseItem{}, false
	}
	return item, true
}

func ResponseObjectFromEvent(value any) (ResponseObject, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return ResponseObject{}, false
	}
	var response ResponseObject
	if err := json.Unmarshal(raw, &response); err != nil {
		return ResponseObject{}, false
	}
	return response, true
}

func MergeResponseOutput(existing, final []ResponseItem) []ResponseItem {
	if len(final) > 0 {
		return final
	}
	return existing
}

func ResponseUsageToUsage(usage ResponseUsage) OpenAIResponsesUsage {
	cached := usage.InputTokensDetails.CachedTokens
	input := usage.InputTokens - cached
	if input < 0 {
		input = 0
	}
	return OpenAIResponsesUsage{Input: input, Output: usage.OutputTokens, CacheRead: cached, TotalTokens: usage.TotalTokens}
}

func ResponseItemStreamBlock(item ResponseItem) OpenAIResponsesBlock {
	switch item.Type {
	case "reasoning":
		raw, _ := json.Marshal(item.Raw)
		return OpenAIResponsesBlock{Type: "thinking", Thinking: ResponseReasoningText(item), RawItem: raw}
	case "message":
		return OpenAIResponsesBlock{Type: "text", Text: ResponseMessageText(item), TextSignature: EncodeResponsesTextSignature(item.ID, item.Phase)}
	case "function_call":
		id := item.CallID
		if item.ID != "" {
			id += "|" + item.ID
		}
		return OpenAIResponsesBlock{Type: "toolCall", ID: id, Name: item.Name, Arguments: MistralNormalizeToolArguments(item.Arguments), Data: ResponseRawArgumentsString(item.Arguments)}
	default:
		return OpenAIResponsesBlock{}
	}
}

func ResponseRawArgumentsString(raw json.RawMessage) string {
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		return encoded
	}
	return string(raw)
}

func OpenAIResponsesNormalizeToolCallID(id, targetProvider, targetAPI, sourceProvider, sourceAPI string) string {
	if !OpenAIResponsesAllowsToolCallProvider(targetAPI, targetProvider) {
		return NormalizeIDPart(id)
	}
	parts := strings.SplitN(id, "|", 2)
	if len(parts) == 1 {
		return NormalizeIDPart(id)
	}
	callID := NormalizeIDPart(parts[0])
	itemID := parts[1]
	if sourceProvider != targetProvider || sourceAPI != targetAPI {
		itemID = "fc_" + NormalizeIDPart(MistralShortHash(itemID))
	} else {
		itemID = NormalizeIDPart(itemID)
		if !strings.HasPrefix(itemID, "fc_") {
			itemID = NormalizeIDPart("fc_" + itemID)
		}
	}
	if itemID == "" {
		itemID = "fc_" + NormalizeIDPart(MistralShortHash(id))
	}
	return callID + "|" + itemID
}

func OpenAIResponsesAllowsToolCallProvider(api, provider string) bool {
	switch api {
	case "azure-openai-responses":
		return provider == "openai" || provider == "openai-codex" || provider == "opencode" || provider == "azure-openai-responses"
	default:
		return provider == "openai" || provider == "openai-codex" || provider == "opencode"
	}
}

func OpenAIResponsesMessageFromEventMaps(events []map[string]any) (OpenAIResponsesParsed, error) {
	var b strings.Builder
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			continue
		}
		b.WriteString("data: ")
		b.Write(raw)
		b.WriteString("\n\n")
	}
	return ParseOpenAIResponsesEvents([]byte(b.String()))
}
