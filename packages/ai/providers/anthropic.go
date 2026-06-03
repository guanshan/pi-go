package providers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	anthropicparam "github.com/anthropics/anthropic-sdk-go/packages/param"
)

// unescapeJSONHTMLBodyMiddleware rewrites the outgoing JSON request body so the
// HTML-significant characters < > & appear literally instead of HTML-escaped
// (\uXXXX), matching the TypeScript upstream's JSON.stringify output. The
// Anthropic SDK encoder HTML-escapes by default and offers no toggle, so the fix
// is applied at the serialized-body layer. Bodies are only rewritten when they
// are buffered JSON; streaming/unknown bodies are passed through untouched.
func unescapeJSONHTMLBodyMiddleware(req *http.Request, next anthropicoption.MiddlewareNext) (*http.Response, error) {
	if req != nil && req.Body != nil && req.GetBody != nil {
		if raw, err := io.ReadAll(req.Body); err == nil {
			_ = req.Body.Close()
			rewritten := UnescapeJSONHTML(raw)
			req.Body = io.NopCloser(bytes.NewReader(rewritten))
			req.ContentLength = int64(len(rewritten))
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(rewritten)), nil
			}
		}
	}
	return next(req)
}

func NewAnthropicClient(key, baseURL string, headers map[string]string, httpClient *http.Client, requestOptions ...RequestOptions) anthropic.Client {
	return NewAnthropicClientWithAuth(key, baseURL, headers, httpClient, false, requestOptions...)
}

func NewAnthropicClientWithAuth(key, baseURL string, headers map[string]string, httpClient *http.Client, bearerAuth bool, requestOptions ...RequestOptions) anthropic.Client {
	return NewAnthropicClientWithMode(key, baseURL, headers, httpClient, bearerAuth, false, requestOptions...)
}

// NewAnthropicClientWithMode builds an Anthropic client. When gatewayAuth is
// true the request routes through Cloudflare AI Gateway: the token is sent as
// the cf-aig-authorization header and the SDK does not set x-api-key /
// Authorization. A caller-supplied upstream Authorization header (BYOK) in
// headers is preserved.
func NewAnthropicClientWithMode(key, baseURL string, headers map[string]string, httpClient *http.Client, bearerAuth, gatewayAuth bool, requestOptions ...RequestOptions) anthropic.Client {
	options := firstRequestOptions(requestOptions)
	opts := []anthropicoption.RequestOption{
		anthropicoption.WithRequestTimeout(RequestTimeout(options)),
		// The Anthropic Go SDK's internal JSON encoder HTML-escapes < > & by
		// default (EscapeHTMLByDefault = true), which diverges from the TS upstream
		// (JSON.stringify) wire bytes. Rewrite the serialized body to the literal
		// characters so the request bytes match TS and prompt-cache hashing stays
		// stable. The OpenAI Go SDK already disables this escaping, so no equivalent
		// is needed there.
		anthropicoption.WithMiddleware(unescapeJSONHTMLBodyMiddleware),
	}
	if gatewayAuth {
		// Do not let the SDK contribute x-api-key / Authorization from the
		// environment; the gateway authenticates via cf-aig-authorization.
		opts = append(opts, anthropicoption.WithoutEnvironmentDefaults())
		opts = append(opts, anthropicoption.WithHeader("cf-aig-authorization", "Bearer "+key))
	} else if bearerAuth {
		opts = append(opts, anthropicoption.WithAuthToken(key))
	} else {
		opts = append(opts, anthropicoption.WithAPIKey(key))
	}
	if ShouldSetMaxRetries(options) {
		opts = append(opts, anthropicoption.WithMaxRetries(MaxRetries(options)))
	}
	if httpClient != nil {
		opts = append(opts, anthropicoption.WithHTTPClient(httpClient))
	}
	if baseURL != "" {
		opts = append(opts, anthropicoption.WithBaseURL(baseURL))
	}
	for k, v := range headers {
		opts = append(opts, anthropicoption.WithHeader(k, v))
	}
	if options.OnResponse != nil {
		opts = append(opts, anthropicoption.WithMiddleware(func(req *http.Request, next anthropicoption.MiddlewareNext) (*http.Response, error) {
			resp, err := next(req)
			if resp != nil {
				if responseErr := options.OnResponse(ProviderResponseFromHTTP(resp)); responseErr != nil {
					return resp, responseErr
				}
			}
			return resp, err
		}))
	}
	return anthropic.NewClient(opts...)
}

// claudeCodeTools is the canonical casing of Claude Code 2.x tool names.
// Source: https://cchistory.mariozechner.at/data/prompts-2.1.11.md
var claudeCodeTools = []string{
	"Read",
	"Write",
	"Edit",
	"Bash",
	"Grep",
	"Glob",
	"AskUserQuestion",
	"EnterPlanMode",
	"ExitPlanMode",
	"KillShell",
	"NotebookEdit",
	"Skill",
	"Task",
	"TaskOutput",
	"TodoWrite",
	"WebFetch",
	"WebSearch",
}

var ccToolLookup = func() map[string]string {
	m := make(map[string]string, len(claudeCodeTools))
	for _, name := range claudeCodeTools {
		m[strings.ToLower(name)] = name
	}
	return m
}()

// ToClaudeCodeName maps a tool name to Claude Code canonical casing if it
// matches (case-insensitive), otherwise returns the name unchanged.
func ToClaudeCodeName(name string) string {
	if canonical, ok := ccToolLookup[strings.ToLower(name)]; ok {
		return canonical
	}
	return name
}

// FromClaudeCodeName maps an inbound tool name back to the original tool name
// from the request tool list (case-insensitive), otherwise returns it unchanged.
func FromClaudeCodeName(name string, tools []map[string]any) string {
	if len(tools) == 0 {
		return name
	}
	lower := strings.ToLower(name)
	for _, tool := range tools {
		if toolName, _ := tool["name"].(string); strings.ToLower(toolName) == lower {
			return toolName
		}
	}
	return name
}

func AnthropicCacheControlParam(retention string, supportsLong bool) (anthropic.CacheControlEphemeralParam, bool) {
	if ResolveCacheRetention(retention) == "none" {
		return anthropic.CacheControlEphemeralParam{}, false
	}
	cacheControl := anthropic.NewCacheControlEphemeralParam()
	if ResolveCacheRetention(retention) == "long" && supportsLong {
		cacheControl.TTL = anthropic.CacheControlEphemeralTTLTTL1h
	}
	return cacheControl, true
}

func AnthropicHeaders(modelHeaders, requestHeaders map[string]string) map[string]string {
	return MergeHeaders(modelHeaders, map[string]string{
		"anthropic-version": "2023-06-01",
		"anthropic-beta":    "prompt-caching-2024-07-31",
	}, requestHeaders)
}

type AnthropicRequestOptions struct {
	ModelID                     string
	SystemPrompt                string
	Messages                    []AnthropicMessage
	Tools                       []map[string]any
	CacheRetention              string
	MaxTokens                   int
	MaxOutput                   int
	Temperature                 *float64
	ToolChoice                  any
	Reasoning                   bool
	ThinkingLevel               string
	ThinkingLevelMap            map[string]*string
	ThinkingBudgets             ThinkingBudgets
	ThinkingDisplay             string
	Metadata                    map[string]any
	SupportsEagerToolStreaming  bool
	SupportsLongCacheRetention  bool
	SupportsCacheControlOnTools bool
	SupportsTemperature         bool
	ForceAdaptiveThinking       bool
	AllowEmptySignature         bool
	IsOAuth                     bool
}

type AnthropicMessage struct {
	Role       string
	Text       string
	ToolCallID string
	IsError    bool
	Blocks     []AnthropicBlock
}

type AnthropicBlock struct {
	Type              string
	Text              string
	Data              string
	MimeType          string
	Thinking          string
	ID                string
	Name              string
	Arguments         json.RawMessage
	ThinkingSignature string
	Redacted          bool
}

type AnthropicParsed struct {
	Blocks       []AnthropicBlock
	ToolCalls    []AnthropicToolCall
	Usage        AnthropicUsageCounts
	StopReason   string
	ErrorMessage string
	ResponseID   string
}

type AnthropicToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type AnthropicUsageCounts struct {
	Input       int
	Output      int
	CacheRead   int
	CacheWrite  int
	TotalTokens int
}

func AnthropicMessageParams(options AnthropicRequestOptions) anthropic.MessageNewParams {
	cacheControl, useCacheControl := AnthropicCacheControlParam(options.CacheRetention, options.SupportsLongCacheRetention)
	maxTokens := options.MaxTokens
	if maxTokens == 0 {
		maxTokens = MaxInt(1024, options.MaxOutput)
	}
	params := anthropic.MessageNewParams{
		Model:     options.ModelID,
		MaxTokens: int64(maxTokens),
		Messages:  AnthropicMessages(options.Messages, cacheControl, useCacheControl, options.AllowEmptySignature, options.IsOAuth),
	}
	if strings.TrimSpace(options.SystemPrompt) != "" {
		block := anthropic.TextBlockParam{Text: options.SystemPrompt}
		if useCacheControl {
			block.CacheControl = cacheControl
		}
		params.System = []anthropic.TextBlockParam{block}
	}
	if len(options.Tools) > 0 {
		params.Tools = AnthropicTools(options.Tools, cacheControl, useCacheControl && options.SupportsCacheControlOnTools, options.SupportsEagerToolStreaming, options.IsOAuth)
	}
	if choice, ok := AnthropicToolChoice(options.ToolChoice); ok {
		params.ToolChoice = choice
	}
	thinkingEnabled := false
	if options.Reasoning && options.ThinkingLevel != "" {
		if options.ThinkingLevel == "off" {
			disabled := anthropic.NewThinkingConfigDisabledParam()
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &disabled}
		} else {
			thinkingEnabled = true
			display := AnthropicThinkingDisplay(options.ThinkingDisplay)
			if options.ForceAdaptiveThinking {
				params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{
					Display: anthropic.ThinkingConfigAdaptiveDisplay(display),
				}}
				if effort := AnthropicThinkingEffort(options.ThinkingLevel, options.ThinkingLevelMap); effort != "" {
					params.OutputConfig.Effort = effort
				}
			} else {
				params.Thinking = anthropic.ThinkingConfigParamUnion{OfEnabled: &anthropic.ThinkingConfigEnabledParam{
					BudgetTokens: int64(ThinkingBudgetWithBudgets(options.ThinkingLevel, options.ThinkingBudgets)),
					Display:      anthropic.ThinkingConfigEnabledDisplay(display),
				}}
			}
		}
	}
	// Temperature is incompatible with extended thinking and unsupported on
	// Claude Opus 4.7+ (compat.supportsTemperature == false).
	if !thinkingEnabled && options.SupportsTemperature && options.Temperature != nil {
		params.Temperature = anthropicparam.NewOpt(*options.Temperature)
	}
	if userID, _ := options.Metadata["user_id"].(string); userID != "" {
		params.Metadata.UserID = anthropicparam.NewOpt(userID)
	}
	return params
}

func AnthropicThinkingDisplay(display string) string {
	if display == "omitted" {
		return "omitted"
	}
	return "summarized"
}

func AnthropicThinkingEffort(level string, levelMap map[string]*string) anthropic.OutputConfigEffort {
	if mapped, ok := levelMap[level]; ok && mapped != nil && *mapped != "" {
		return anthropic.OutputConfigEffort(*mapped)
	}
	switch level {
	case "minimal", "low":
		return anthropic.OutputConfigEffortLow
	case "medium":
		return anthropic.OutputConfigEffortMedium
	case "xhigh":
		return anthropic.OutputConfigEffortXhigh
	case "high":
		return anthropic.OutputConfigEffortHigh
	default:
		return anthropic.OutputConfigEffortHigh
	}
}

func AnthropicToolChoice(choice any) (anthropic.ToolChoiceUnionParam, bool) {
	switch ToolChoiceType(choice) {
	case "auto":
		return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}, true
	case "any":
		return anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}, true
	case "none":
		none := anthropic.NewToolChoiceNoneParam()
		return anthropic.ToolChoiceUnionParam{OfNone: &none}, true
	case "tool", "function", "":
		if name := ToolChoiceName(choice); name != "" {
			return anthropic.ToolChoiceParamOfTool(name), true
		}
	}
	return anthropic.ToolChoiceUnionParam{}, false
}

func ParseAnthropicMessage(resp *anthropic.Message, isOAuth bool, tools []map[string]any) AnthropicParsed {
	if resp == nil {
		return AnthropicParsed{StopReason: "stop"}
	}
	var blocks []AnthropicBlock
	var calls []AnthropicToolCall
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			blocks = append(blocks, AnthropicBlock{Type: "text", Text: c.Text})
		case "thinking":
			blocks = append(blocks, AnthropicBlock{Type: "thinking", Thinking: c.Thinking, ThinkingSignature: c.Signature})
		case "redacted_thinking":
			blocks = append(blocks, AnthropicBlock{Type: "thinking", Thinking: "[Reasoning redacted]", ThinkingSignature: c.Data, Redacted: true})
		case "tool_use":
			id := c.ID
			if id == "" {
				id = ShortID()
			}
			name := c.Name
			if isOAuth {
				name = FromClaudeCodeName(name, tools)
			}
			args := NormalizeToolArguments(c.Input)
			blocks = append(blocks, AnthropicBlock{Type: "toolCall", ID: id, Name: name, Arguments: args})
			calls = append(calls, AnthropicToolCall{ID: id, Name: name, Arguments: args})
		}
	}
	stop, errorMessage := AnthropicStopReason(string(resp.StopReason))
	return AnthropicParsed{
		Blocks:       blocks,
		ToolCalls:    calls,
		Usage:        AnthropicUsageFromMessageUsage(resp.Usage),
		StopReason:   stop,
		ErrorMessage: errorMessage,
		ResponseID:   resp.ID,
	}
}

func AnthropicUsageFromMessageUsage(usage anthropic.Usage) AnthropicUsageCounts {
	return AnthropicUsageCounts{
		Input:       int(usage.InputTokens),
		Output:      int(usage.OutputTokens),
		CacheRead:   int(usage.CacheReadInputTokens),
		CacheWrite:  int(usage.CacheCreationInputTokens),
		TotalTokens: int(usage.InputTokens + usage.OutputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens),
	}
}

func AnthropicUsageFromDeltaUsage(usage anthropic.MessageDeltaUsage) AnthropicUsageCounts {
	return AnthropicUsageCounts{
		Input:       int(usage.InputTokens),
		Output:      int(usage.OutputTokens),
		CacheRead:   int(usage.CacheReadInputTokens),
		CacheWrite:  int(usage.CacheCreationInputTokens),
		TotalTokens: int(usage.InputTokens + usage.OutputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens),
	}
}

func AnthropicMessages(messages []AnthropicMessage, cacheControl anthropic.CacheControlEphemeralParam, useCacheControl bool, allowEmptySignature bool, isOAuth bool) []anthropic.MessageParam {
	out := []anthropic.MessageParam{}
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		switch msg.Role {
		case "user":
			out = append(out, anthropic.NewUserMessage(AnthropicContentBlocks(msg.Blocks)...))
		case "assistant":
			blocks := []anthropic.ContentBlockParamUnion{}
			text := ""
			for _, b := range msg.Blocks {
				switch b.Type {
				case "text":
					text += b.Text
				case "thinking":
					if text != "" {
						blocks = append(blocks, anthropic.NewTextBlock(text))
						text = ""
					}
					if b.Redacted {
						if strings.TrimSpace(b.ThinkingSignature) != "" {
							blocks = append(blocks, anthropic.NewRedactedThinkingBlock(b.ThinkingSignature))
						}
					} else if b.Thinking != "" && (strings.TrimSpace(b.ThinkingSignature) != "" || allowEmptySignature) {
						signature := b.ThinkingSignature
						if strings.TrimSpace(signature) == "" {
							signature = ""
						}
						blocks = append(blocks, anthropic.NewThinkingBlock(signature, b.Thinking))
					} else if b.Thinking != "" {
						blocks = append(blocks, anthropic.NewTextBlock(b.Thinking))
					}
				case "toolCall":
					if text != "" {
						blocks = append(blocks, anthropic.NewTextBlock(text))
						text = ""
					}
					var input any
					_ = json.Unmarshal(b.Arguments, &input)
					if input == nil {
						input = map[string]any{}
					}
					name := b.Name
					if isOAuth {
						name = ToClaudeCodeName(name)
					}
					blocks = append(blocks, anthropic.NewToolUseBlock(b.ID, input, name))
				}
			}
			if text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(text))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
		case "toolResult":
			blocks := []anthropic.ContentBlockParamUnion{AnthropicToolResultBlock(msg)}
			for i+1 < len(messages) && messages[i+1].Role == "toolResult" {
				i++
				blocks = append(blocks, AnthropicToolResultBlock(messages[i]))
			}
			out = append(out, anthropic.NewUserMessage(blocks...))
		case "compactionSummary", "branchSummary", "custom":
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Text)))
		}
	}
	if useCacheControl {
		ApplyAnthropicCacheControl(out, cacheControl)
	}
	return out
}

func AnthropicToolResultBlock(msg AnthropicMessage) anthropic.ContentBlockParamUnion {
	content := []anthropic.ToolResultBlockParamContentUnion{}
	if len(msg.Blocks) == 0 && strings.TrimSpace(msg.Text) != "" {
		content = append(content, anthropic.ToolResultBlockParamContentUnion{OfText: &anthropic.TextBlockParam{Text: msg.Text}})
	}
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				content = append(content, anthropic.ToolResultBlockParamContentUnion{OfText: &anthropic.TextBlockParam{Text: block.Text}})
			}
		case "image":
			image := anthropic.NewImageBlockBase64(block.MimeType, block.Data)
			if image.OfImage != nil {
				content = append(content, anthropic.ToolResultBlockParamContentUnion{OfImage: image.OfImage})
			}
		}
	}
	if len(content) == 0 {
		content = append(content, anthropic.ToolResultBlockParamContentUnion{OfText: &anthropic.TextBlockParam{Text: ""}})
	}
	block := anthropic.ToolResultBlockParam{
		ToolUseID: msg.ToolCallID,
		Content:   content,
		IsError:   anthropic.Bool(msg.IsError),
	}
	return anthropic.ContentBlockParamUnion{OfToolResult: &block}
}

func AnthropicContentBlocks(blocks []AnthropicBlock) []anthropic.ContentBlockParamUnion {
	if len(blocks) == 0 {
		return []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("")}
	}
	out := []anthropic.ContentBlockParamUnion{}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, anthropic.NewTextBlock(b.Text))
		case "image":
			out = append(out, anthropic.NewImageBlockBase64(b.MimeType, b.Data))
		}
	}
	if len(out) == 0 {
		return []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("")}
	}
	return out
}

func AnthropicTools(defs []map[string]any, cacheControl anthropic.CacheControlEphemeralParam, useCacheControl bool, supportsEagerToolStreaming bool, isOAuth bool) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		name, _ := d["name"].(string)
		if isOAuth {
			name = ToClaudeCodeName(name)
		}
		schema := anthropicparam.Override[anthropic.ToolInputSchemaParam](d["parameters"])
		tool := anthropic.ToolUnionParamOfTool(schema, name)
		if description, _ := d["description"].(string); description != "" {
			tool.OfTool.Description = anthropicparam.NewOpt(description)
		}
		if supportsEagerToolStreaming {
			tool.OfTool.EagerInputStreaming = anthropicparam.NewOpt(true)
		}
		out = append(out, tool)
	}
	if useCacheControl && len(out) > 0 && out[len(out)-1].OfTool != nil {
		out[len(out)-1].OfTool.CacheControl = cacheControl
	}
	return out
}

func AnthropicNormalizeToolCallID(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return ShortID()
	}
	return b.String()
}

func ApplyAnthropicCacheControl(messages []anthropic.MessageParam, cacheControl anthropic.CacheControlEphemeralParam) {
	for i := len(messages) - 1; i >= 0; i-- {
		for j := len(messages[i].Content) - 1; j >= 0; j-- {
			if control := messages[i].Content[j].GetCacheControl(); control != nil {
				*control = cacheControl
				return
			}
		}
	}
}
