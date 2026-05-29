package providers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockdocument "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

func BedrockRegion(baseURL string) string {
	if region := os.Getenv("AWS_REGION"); region != "" {
		return region
	}
	if region := os.Getenv("AWS_DEFAULT_REGION"); region != "" {
		return region
	}
	if region := BedrockEndpointRegion(baseURL); region != "" {
		return region
	}
	return ""
}

func BedrockEndpointRegion(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	prefix := "bedrock-runtime."
	if strings.HasPrefix(host, prefix) {
		rest := strings.TrimPrefix(host, prefix)
		if index := strings.Index(rest, ".amazonaws.com"); index > 0 {
			return rest[:index]
		}
	}
	fipsPrefix := "bedrock-runtime-fips."
	if strings.HasPrefix(host, fipsPrefix) {
		rest := strings.TrimPrefix(host, fipsPrefix)
		if index := strings.Index(rest, ".amazonaws.com"); index > 0 {
			return rest[:index]
		}
	}
	return ""
}

func BedrockBaseEndpoint(baseURL, region string) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return "https://bedrock-runtime." + region + ".amazonaws.com"
	}
	return strings.ReplaceAll(base, "{region}", region)
}

func BedrockEnvCredentials() (string, string, bool) {
	access := os.Getenv("AWS_ACCESS_KEY_ID")
	secret := os.Getenv("AWS_SECRET_ACCESS_KEY")
	ok := access != "" && secret != ""
	ok = ok ||
		os.Getenv("AWS_PROFILE") != "" ||
		os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "" ||
		os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "" ||
		os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI") != "" ||
		os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE") != ""
	return access, secret, ok
}

func BedrockStopReason(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens", "model_context_window_exceeded":
		return "length"
	case "tool_use":
		return "toolUse"
	default:
		return "error"
	}
}

func BedrockThinkingEffort(level string, levelMap map[string]*string, supportsNativeXHigh bool) string {
	if level == "xhigh" && supportsNativeXHigh {
		return "xhigh"
	}
	if mapped, ok := levelMap[level]; ok && mapped != nil {
		return *mapped
	}
	switch level {
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	default:
		return "high"
	}
}

func BedrockThinkingBudget(level string, budgets ThinkingBudgets) int {
	if level == "xhigh" {
		return 16384
	}
	return ThinkingBudgetWithBudgets(level, budgets)
}

func BedrockImageFormat(mimeType string) bedrocktypes.ImageFormat {
	switch strings.ToLower(mimeType) {
	case "image/jpeg", "image/jpg":
		return bedrocktypes.ImageFormatJpeg
	case "image/gif":
		return bedrocktypes.ImageFormatGif
	case "image/webp":
		return bedrocktypes.ImageFormatWebp
	default:
		return bedrocktypes.ImageFormatPng
	}
}

func BedrockCachePoint(cacheRetention string) bedrocktypes.CachePointBlock {
	block := bedrocktypes.CachePointBlock{Type: bedrocktypes.CachePointTypeDefault}
	if cacheRetention == "long" {
		block.Ttl = bedrocktypes.CacheTTLOneHour
	}
	return block
}

func BedrockNormalizeToolCallID(id string) string {
	normalized := NormalizeIDPart(id)
	if normalized == "" {
		return ShortID()
	}
	return normalized
}

func BedrockIsAnthropicClaude(modelID, modelName string) bool {
	for _, value := range BedrockModelCandidates(modelID, modelName) {
		if strings.Contains(value, "anthropic.claude") || strings.Contains(value, "anthropic/claude") || strings.Contains(value, "claude") {
			return true
		}
	}
	return false
}

func BedrockSupportsPromptCaching(modelID, modelName string) bool {
	if os.Getenv("AWS_BEDROCK_FORCE_CACHE") == "1" {
		return true
	}
	candidates := BedrockModelCandidates(modelID, modelName)
	hasClaude := false
	for _, value := range candidates {
		if strings.Contains(value, "claude") {
			hasClaude = true
			break
		}
	}
	if !hasClaude {
		return false
	}
	for _, value := range candidates {
		if strings.Contains(value, "-4-") || strings.Contains(value, "claude-3-7-sonnet") || strings.Contains(value, "claude-3-5-haiku") {
			return true
		}
	}
	return false
}

func BedrockSupportsThinkingSignature(modelID, modelName string) bool {
	return BedrockIsAnthropicClaude(modelID, modelName)
}

func BedrockSupportsAdaptiveThinking(modelID, modelName string) bool {
	for _, value := range BedrockModelCandidates(modelID, modelName) {
		if strings.Contains(value, "opus-4-6") || strings.Contains(value, "opus-4-7") || strings.Contains(value, "sonnet-4-6") {
			return true
		}
	}
	return false
}

func BedrockSupportsNativeXHigh(modelID, modelName string) bool {
	for _, value := range BedrockModelCandidates(modelID, modelName) {
		if strings.Contains(value, "opus-4-7") {
			return true
		}
	}
	return false
}

func BedrockModelCandidates(modelID, modelName string) []string {
	values := []string{modelID}
	if modelName != "" {
		values = append(values, modelName)
	}
	var out []string
	replacer := strings.NewReplacer(" ", "-", "_", "-", ".", "-", ":", "-")
	for _, value := range values {
		lower := strings.ToLower(value)
		out = append(out, lower, replacer.Replace(lower))
	}
	return out
}

type BedrockRequestOptions struct {
	ModelID             string
	ModelName           string
	SystemPrompt        string
	Messages            []BedrockMessage
	Tools               []map[string]any
	CacheRetention      string
	MaxTokens           int
	MaxOutput           int
	Temperature         *float64
	ToolChoice          any
	RequestMetadata     map[string]string
	Reasoning           bool
	ThinkingLevel       string
	ThinkingLevelMap    map[string]*string
	ThinkingBudgets     ThinkingBudgets
	ThinkingDisplay     string
	InterleavedThinking *bool
}

type BedrockMessage struct {
	Role       string
	Text       string
	ToolCallID string
	IsError    bool
	Blocks     []BedrockBlock
}

type BedrockBlock struct {
	Type              string
	Text              string
	Data              string
	MimeType          string
	Thinking          string
	ID                string
	Name              string
	Arguments         json.RawMessage
	ThinkingSignature string
}

type BedrockParsed struct {
	Blocks     []BedrockBlock
	ToolCalls  []BedrockToolCall
	Usage      BedrockUsageCounts
	StopReason string
}

type BedrockToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type BedrockUsageCounts struct {
	Input       int
	Output      int
	CacheRead   int
	CacheWrite  int
	TotalTokens int
}

func BedrockConverseInput(options BedrockRequestOptions) *bedrockruntime.ConverseInput {
	cacheRetention := ResolveCacheRetention(options.CacheRetention)
	input := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(options.ModelID),
		Messages: BedrockMessages(options.Messages, options.ModelID, options.ModelName, cacheRetention),
		System:   BedrockSystemPrompt(options.SystemPrompt, options.ModelID, options.ModelName, cacheRetention),
	}
	maxTokens := options.MaxTokens
	if maxTokens == 0 && BedrockIsAnthropicClaude(options.ModelID, options.ModelName) {
		maxTokens = options.MaxOutput
	}
	if maxTokens > 0 || options.Temperature != nil {
		config := &bedrocktypes.InferenceConfiguration{}
		if maxTokens > 0 {
			value := int32(maxTokens)
			config.MaxTokens = &value
		}
		if options.Temperature != nil {
			value := float32(*options.Temperature)
			config.Temperature = &value
		}
		input.InferenceConfig = config
	}
	if toolConfig := BedrockToolConfig(options.Tools, options.ToolChoice); toolConfig != nil {
		input.ToolConfig = toolConfig
	}
	if additional := BedrockAdditionalModelRequestFields(options); additional != nil {
		input.AdditionalModelRequestFields = bedrockdocument.NewLazyDocument(additional)
	}
	if len(options.RequestMetadata) > 0 {
		input.RequestMetadata = options.RequestMetadata
	}
	return input
}

func BedrockConverseStreamInput(options BedrockRequestOptions) *bedrockruntime.ConverseStreamInput {
	input := BedrockConverseInput(options)
	return &bedrockruntime.ConverseStreamInput{
		ModelId:                      input.ModelId,
		Messages:                     input.Messages,
		System:                       input.System,
		InferenceConfig:              input.InferenceConfig,
		ToolConfig:                   input.ToolConfig,
		AdditionalModelRequestFields: input.AdditionalModelRequestFields,
		RequestMetadata:              input.RequestMetadata,
	}
}

func BedrockAdditionalModelRequestFields(options BedrockRequestOptions) map[string]any {
	if !options.Reasoning || options.ThinkingLevel == "" || options.ThinkingLevel == "off" {
		return nil
	}
	if !BedrockIsAnthropicClaude(options.ModelID, options.ModelName) {
		return nil
	}
	if BedrockSupportsAdaptiveThinking(options.ModelID, options.ModelName) {
		thinking := map[string]any{"type": "adaptive", "display": BedrockThinkingDisplay(options.ThinkingDisplay)}
		return map[string]any{
			"thinking":      thinking,
			"output_config": map[string]any{"effort": BedrockThinkingEffort(options.ThinkingLevel, options.ThinkingLevelMap, BedrockSupportsNativeXHigh(options.ModelID, options.ModelName))},
		}
	}
	result := map[string]any{
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": BedrockThinkingBudget(options.ThinkingLevel, options.ThinkingBudgets),
			"display":       BedrockThinkingDisplay(options.ThinkingDisplay),
		},
	}
	if options.InterleavedThinking == nil || *options.InterleavedThinking {
		result["anthropic_beta"] = []string{"interleaved-thinking-2025-05-14"}
	}
	return result
}

func BedrockThinkingDisplay(display string) string {
	if display == "omitted" {
		return "omitted"
	}
	return "summarized"
}

func BedrockSystemPrompt(system, modelID, modelName, cacheRetention string) []bedrocktypes.SystemContentBlock {
	if strings.TrimSpace(system) == "" {
		return nil
	}
	blocks := []bedrocktypes.SystemContentBlock{
		&bedrocktypes.SystemContentBlockMemberText{Value: SanitizeProviderText(system)},
	}
	if cacheRetention != "none" && BedrockSupportsPromptCaching(modelID, modelName) {
		blocks = append(blocks, &bedrocktypes.SystemContentBlockMemberCachePoint{Value: BedrockCachePoint(cacheRetention)})
	}
	return blocks
}

func BedrockMessages(messages []BedrockMessage, modelID, modelName, cacheRetention string) []bedrocktypes.Message {
	result := []bedrocktypes.Message{}
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		switch msg.Role {
		case "user", "compactionSummary", "branchSummary", "custom":
			if content := BedrockUserContent(msg); len(content) > 0 {
				result = append(result, bedrocktypes.Message{Role: bedrocktypes.ConversationRoleUser, Content: content})
			}
		case "assistant":
			if content := BedrockAssistantContent(msg, modelID, modelName); len(content) > 0 {
				result = append(result, bedrocktypes.Message{Role: bedrocktypes.ConversationRoleAssistant, Content: content})
			}
		case "toolResult":
			toolResults := []bedrocktypes.ContentBlock{BedrockToolResultContent(msg)}
			for i+1 < len(messages) && messages[i+1].Role == "toolResult" {
				i++
				toolResults = append(toolResults, BedrockToolResultContent(messages[i]))
			}
			result = append(result, bedrocktypes.Message{Role: bedrocktypes.ConversationRoleUser, Content: toolResults})
		}
	}
	if cacheRetention != "none" && BedrockSupportsPromptCaching(modelID, modelName) && len(result) > 0 {
		last := &result[len(result)-1]
		if last.Role == bedrocktypes.ConversationRoleUser {
			last.Content = append(last.Content, &bedrocktypes.ContentBlockMemberCachePoint{Value: BedrockCachePoint(cacheRetention)})
		}
	}
	return result
}

func BedrockUserContent(msg BedrockMessage) []bedrocktypes.ContentBlock {
	if msg.Text != "" && len(msg.Blocks) == 0 {
		return []bedrocktypes.ContentBlock{&bedrocktypes.ContentBlockMemberText{Value: SanitizeProviderText(msg.Text)}}
	}
	var content []bedrocktypes.ContentBlock
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				content = append(content, &bedrocktypes.ContentBlockMemberText{Value: SanitizeProviderText(block.Text)})
			}
		case "image":
			content = append(content, &bedrocktypes.ContentBlockMemberImage{Value: BedrockImageBlock(block)})
		}
	}
	return content
}

func BedrockAssistantContent(msg BedrockMessage, modelID, modelName string) []bedrocktypes.ContentBlock {
	var content []bedrocktypes.ContentBlock
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				content = append(content, &bedrocktypes.ContentBlockMemberText{Value: SanitizeProviderText(block.Text)})
			}
		case "toolCall":
			content = append(content, &bedrocktypes.ContentBlockMemberToolUse{Value: bedrocktypes.ToolUseBlock{
				ToolUseId: aws.String(BedrockNormalizeToolCallID(block.ID)),
				Name:      aws.String(block.Name),
				Input:     bedrockdocument.NewLazyDocument(aiutils.RawJSONMap(block.Arguments)),
			}})
		case "thinking":
			if strings.TrimSpace(block.Thinking) == "" {
				continue
			}
			text := SanitizeProviderText(block.Thinking)
			if BedrockSupportsThinkingSignature(modelID, modelName) && strings.TrimSpace(block.ThinkingSignature) != "" {
				content = append(content, &bedrocktypes.ContentBlockMemberReasoningContent{Value: &bedrocktypes.ReasoningContentBlockMemberReasoningText{Value: bedrocktypes.ReasoningTextBlock{
					Text:      aws.String(text),
					Signature: aws.String(block.ThinkingSignature),
				}}})
			} else if BedrockSupportsThinkingSignature(modelID, modelName) {
				content = append(content, &bedrocktypes.ContentBlockMemberText{Value: text})
			} else {
				content = append(content, &bedrocktypes.ContentBlockMemberReasoningContent{Value: &bedrocktypes.ReasoningContentBlockMemberReasoningText{Value: bedrocktypes.ReasoningTextBlock{
					Text: aws.String(text),
				}}})
			}
		}
	}
	return content
}

func BedrockToolResultContent(msg BedrockMessage) bedrocktypes.ContentBlock {
	var content []bedrocktypes.ToolResultContentBlock
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			content = append(content, &bedrocktypes.ToolResultContentBlockMemberText{Value: SanitizeProviderText(block.Text)})
		case "image":
			content = append(content, &bedrocktypes.ToolResultContentBlockMemberImage{Value: BedrockImageBlock(block)})
		}
	}
	status := bedrocktypes.ToolResultStatusSuccess
	if msg.IsError {
		status = bedrocktypes.ToolResultStatusError
	}
	return &bedrocktypes.ContentBlockMemberToolResult{Value: bedrocktypes.ToolResultBlock{
		ToolUseId: aws.String(BedrockNormalizeToolCallID(msg.ToolCallID)),
		Content:   content,
		Status:    status,
	}}
}

func BedrockToolConfig(defs []map[string]any, toolChoice any) *bedrocktypes.ToolConfiguration {
	if len(defs) == 0 || ToolChoiceType(toolChoice) == "none" {
		return nil
	}
	out := make([]bedrocktypes.Tool, 0, len(defs))
	for _, d := range defs {
		name, _ := d["name"].(string)
		description, _ := d["description"].(string)
		out = append(out, &bedrocktypes.ToolMemberToolSpec{Value: bedrocktypes.ToolSpecification{
			Name:        aws.String(name),
			Description: aws.String(description),
			InputSchema: &bedrocktypes.ToolInputSchemaMemberJson{Value: bedrockdocument.NewLazyDocument(d["parameters"])},
		}})
	}
	return &bedrocktypes.ToolConfiguration{Tools: out, ToolChoice: BedrockToolChoice(toolChoice)}
}

func BedrockToolChoice(choice any) bedrocktypes.ToolChoice {
	switch ToolChoiceType(choice) {
	case "auto":
		return &bedrocktypes.ToolChoiceMemberAuto{Value: bedrocktypes.AutoToolChoice{}}
	case "any", "required":
		return &bedrocktypes.ToolChoiceMemberAny{Value: bedrocktypes.AnyToolChoice{}}
	case "tool", "function", "":
		if name := ToolChoiceName(choice); name != "" {
			return &bedrocktypes.ToolChoiceMemberTool{Value: bedrocktypes.SpecificToolChoice{Name: aws.String(name)}}
		}
	}
	return nil
}

func BedrockImageBlock(block BedrockBlock) bedrocktypes.ImageBlock {
	data, err := base64.StdEncoding.DecodeString(block.Data)
	if err != nil {
		data = []byte(block.Data)
	}
	return bedrocktypes.ImageBlock{
		Format: BedrockImageFormat(block.MimeType),
		Source: &bedrocktypes.ImageSourceMemberBytes{Value: data},
	}
}

func ParseBedrockConverseOutput(out *bedrockruntime.ConverseOutput) (BedrockParsed, error) {
	if out == nil {
		return BedrockParsed{}, errors.New("empty Bedrock response")
	}
	var blocks []BedrockBlock
	var calls []BedrockToolCall
	if message, ok := out.Output.(*bedrocktypes.ConverseOutputMemberMessage); ok {
		for _, content := range message.Value.Content {
			switch item := content.(type) {
			case *bedrocktypes.ContentBlockMemberText:
				if item.Value != "" {
					blocks = append(blocks, BedrockBlock{Type: "text", Text: item.Value})
				}
			case *bedrocktypes.ContentBlockMemberReasoningContent:
				switch reasoning := item.Value.(type) {
				case *bedrocktypes.ReasoningContentBlockMemberReasoningText:
					text := ""
					signature := ""
					if reasoning.Value.Text != nil {
						text = *reasoning.Value.Text
					}
					if reasoning.Value.Signature != nil {
						signature = *reasoning.Value.Signature
					}
					blocks = append(blocks, BedrockBlock{Type: "thinking", Thinking: text, ThinkingSignature: signature})
				}
			case *bedrocktypes.ContentBlockMemberToolUse:
				args := json.RawMessage(`{}`)
				if item.Value.Input != nil {
					if marshaled, err := item.Value.Input.MarshalSmithyDocument(); err == nil {
						args = MistralNormalizeToolArguments(marshaled)
					}
				}
				id := aws.ToString(item.Value.ToolUseId)
				name := aws.ToString(item.Value.Name)
				blocks = append(blocks, BedrockBlock{Type: "toolCall", ID: id, Name: name, Arguments: args})
				calls = append(calls, BedrockToolCall{ID: id, Name: name, Arguments: args})
			}
		}
	}
	stop := BedrockStopReason(string(out.StopReason))
	if len(calls) > 0 && stop == "stop" {
		stop = "toolUse"
	}
	return BedrockParsed{
		Blocks:     blocks,
		ToolCalls:  calls,
		Usage:      BedrockUsageFromTokenUsage(out.Usage),
		StopReason: stop,
	}, nil
}

func BedrockUsageFromTokenUsage(usage *bedrocktypes.TokenUsage) BedrockUsageCounts {
	if usage == nil {
		return BedrockUsageCounts{}
	}
	return BedrockUsageCounts{
		Input:       int(aws.ToInt32(usage.InputTokens)),
		Output:      int(aws.ToInt32(usage.OutputTokens)),
		CacheRead:   int(aws.ToInt32(usage.CacheReadInputTokens)),
		CacheWrite:  int(aws.ToInt32(usage.CacheWriteInputTokens)),
		TotalTokens: int(aws.ToInt32(usage.TotalTokens)),
	}
}
