package ai

import (
	"encoding/json"
	"strings"
	"time"

	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

var validThinkingLevels = map[ThinkingLevel]bool{
	ThinkingOff: true, ThinkingMinimal: true, ThinkingLow: true,
	ThinkingMedium: true, ThinkingHigh: true, ThinkingXHigh: true,
}

func IsValidThinkingLevel(level string) bool {
	return validThinkingLevels[ThinkingLevel(level)]
}

type Model struct {
	Provider         string             `json:"provider"`
	ID               string             `json:"id"`
	Name             string             `json:"name,omitempty"`
	API              string             `json:"api"`
	BaseURL          string             `json:"baseUrl"`
	EnvKey           string             `json:"envKey,omitempty"`
	APIKey           string             `json:"-"`
	Input            []string           `json:"input,omitempty"`
	Reasoning        bool               `json:"reasoning,omitempty"`
	ThinkingLevels   []ThinkingLevel    `json:"thinkingLevels,omitempty"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap,omitempty"`
	ContextWindow    int                `json:"contextWindow,omitempty"`
	MaxOutput        int                `json:"maxOutput,omitempty"`
	Cost             ModelCost          `json:"cost,omitempty"`
	Headers          map[string]string  `json:"headers,omitempty"`
	Compat           OpenAICompat       `json:"compat,omitempty"`
	Raw              json.RawMessage    `json:"-"`
}

type modelJSON struct {
	Provider         string             `json:"provider"`
	ID               string             `json:"id"`
	Name             string             `json:"name,omitempty"`
	API              string             `json:"api"`
	BaseURL          string             `json:"baseUrl"`
	EnvKey           string             `json:"envKey,omitempty"`
	Input            []string           `json:"input,omitempty"`
	Reasoning        bool               `json:"reasoning,omitempty"`
	ThinkingLevels   []ThinkingLevel    `json:"thinkingLevels,omitempty"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap,omitempty"`
	ContextWindow    int                `json:"contextWindow,omitempty"`
	MaxTokens        int                `json:"maxTokens,omitempty"`
	MaxOutput        int                `json:"maxOutput,omitempty"`
	Cost             ModelCost          `json:"cost,omitempty"`
	Headers          map[string]string  `json:"headers,omitempty"`
	Compat           OpenAICompat       `json:"compat,omitempty"`
}

func (m Model) MarshalJSON() ([]byte, error) {
	return json.Marshal(modelJSON{
		Provider:         m.Provider,
		ID:               m.ID,
		Name:             m.Name,
		API:              m.API,
		BaseURL:          m.BaseURL,
		EnvKey:           m.EnvKey,
		Input:            m.Input,
		Reasoning:        m.Reasoning,
		ThinkingLevels:   m.ThinkingLevels,
		ThinkingLevelMap: m.ThinkingLevelMap,
		ContextWindow:    m.ContextWindow,
		MaxTokens:        m.MaxOutput,
		Cost:             m.Cost,
		Headers:          m.Headers,
		Compat:           m.Compat,
	})
}

func (m *Model) UnmarshalJSON(data []byte) error {
	var raw modelJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = Model{
		Provider:         raw.Provider,
		ID:               raw.ID,
		Name:             raw.Name,
		API:              raw.API,
		BaseURL:          raw.BaseURL,
		EnvKey:           raw.EnvKey,
		Input:            raw.Input,
		Reasoning:        raw.Reasoning,
		ThinkingLevels:   raw.ThinkingLevels,
		ThinkingLevelMap: raw.ThinkingLevelMap,
		ContextWindow:    raw.ContextWindow,
		MaxOutput:        firstPositive(raw.MaxTokens, raw.MaxOutput),
		Cost:             raw.Cost,
		Headers:          raw.Headers,
		Compat:           raw.Compat,
		Raw:              cloneRawMessage(data),
	}
	return nil
}

type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

type OpenAICompat struct {
	SupportsStore                               *bool          `json:"supportsStore,omitempty"`
	SupportsDeveloperRole                       *bool          `json:"supportsDeveloperRole,omitempty"`
	SupportsReasoningEffort                     *bool          `json:"supportsReasoningEffort,omitempty"`
	SupportsUsageInStreaming                    *bool          `json:"supportsUsageInStreaming,omitempty"`
	MaxTokensField                              string         `json:"maxTokensField,omitempty"`
	RequiresToolResultName                      *bool          `json:"requiresToolResultName,omitempty"`
	RequiresAssistantAfterToolResult            *bool          `json:"requiresAssistantAfterToolResult,omitempty"`
	RequiresThinkingAsText                      *bool          `json:"requiresThinkingAsText,omitempty"`
	RequiresReasoningContentOnAssistantMessages *bool          `json:"requiresReasoningContentOnAssistantMessages,omitempty"`
	ThinkingFormat                              string         `json:"thinkingFormat,omitempty"`
	OpenRouterRouting                           map[string]any `json:"openRouterRouting,omitempty"`
	VercelGatewayRouting                        map[string]any `json:"vercelGatewayRouting,omitempty"`
	ZaiToolStream                               *bool          `json:"zaiToolStream,omitempty"`
	SupportsStrictMode                          *bool          `json:"supportsStrictMode,omitempty"`
	CacheControlFormat                          string         `json:"cacheControlFormat,omitempty"`
	SendSessionAffinityHeaders                  bool           `json:"sendSessionAffinityHeaders,omitempty"`
	SupportsLongCacheRetention                  *bool          `json:"supportsLongCacheRetention,omitempty"`
	SendSessionIDHeader                         *bool          `json:"sendSessionIdHeader,omitempty"`
	SupportsEagerToolInputStreaming             *bool          `json:"supportsEagerToolInputStreaming,omitempty"`
	SupportsCacheControlOnTools                 *bool          `json:"supportsCacheControlOnTools,omitempty"`
	ForceAdaptiveThinking                       *bool          `json:"forceAdaptiveThinking,omitempty"`
	AllowEmptySignature                         *bool          `json:"allowEmptySignature,omitempty"`
}

type OpenAICompletionsCompat struct {
	SupportsStore                               bool
	SupportsDeveloperRole                       bool
	SupportsReasoningEffort                     bool
	SupportsUsageInStreaming                    bool
	MaxTokensField                              string
	RequiresToolResultName                      bool
	RequiresAssistantAfterToolResult            bool
	RequiresThinkingAsText                      bool
	RequiresReasoningContentOnAssistantMessages bool
	ThinkingFormat                              string
	OpenRouterRouting                           map[string]any
	VercelGatewayRouting                        map[string]any
	ZaiToolStream                               bool
	SupportsStrictMode                          bool
	CacheControlFormat                          string
	SendSessionAffinityHeaders                  bool
	SupportsLongCacheRetention                  bool
}

type OpenAIResponsesCompat struct {
	SendSessionIDHeader        bool
	SupportsLongCacheRetention bool
}

type AnthropicMessagesCompat struct {
	SupportsEagerToolInputStreaming bool
	SupportsLongCacheRetention      bool
	SendSessionAffinityHeaders      bool
	SupportsCacheControlOnTools     bool
	ForceAdaptiveThinking           bool
	AllowEmptySignature             bool
}

func GetOpenAICompletionsCompat(model Model) OpenAICompletionsCompat {
	compat := detectOpenAICompletionsCompat(model)
	raw := model.Compat
	compat.SupportsStore = compatBool(raw.SupportsStore, compat.SupportsStore)
	compat.SupportsDeveloperRole = compatBool(raw.SupportsDeveloperRole, compat.SupportsDeveloperRole)
	compat.SupportsReasoningEffort = compatBool(raw.SupportsReasoningEffort, compat.SupportsReasoningEffort)
	compat.SupportsUsageInStreaming = compatBool(raw.SupportsUsageInStreaming, compat.SupportsUsageInStreaming)
	if raw.MaxTokensField != "" {
		compat.MaxTokensField = raw.MaxTokensField
	}
	compat.RequiresToolResultName = compatBool(raw.RequiresToolResultName, compat.RequiresToolResultName)
	compat.RequiresAssistantAfterToolResult = compatBool(raw.RequiresAssistantAfterToolResult, compat.RequiresAssistantAfterToolResult)
	compat.RequiresThinkingAsText = compatBool(raw.RequiresThinkingAsText, compat.RequiresThinkingAsText)
	compat.RequiresReasoningContentOnAssistantMessages = compatBool(raw.RequiresReasoningContentOnAssistantMessages, compat.RequiresReasoningContentOnAssistantMessages)
	if raw.ThinkingFormat != "" {
		compat.ThinkingFormat = raw.ThinkingFormat
	}
	if raw.OpenRouterRouting != nil {
		compat.OpenRouterRouting = raw.OpenRouterRouting
	}
	if raw.VercelGatewayRouting != nil {
		compat.VercelGatewayRouting = raw.VercelGatewayRouting
	}
	compat.ZaiToolStream = compatBool(raw.ZaiToolStream, compat.ZaiToolStream)
	compat.SupportsStrictMode = compatBool(raw.SupportsStrictMode, compat.SupportsStrictMode)
	if raw.CacheControlFormat != "" {
		compat.CacheControlFormat = raw.CacheControlFormat
	}
	if raw.SendSessionAffinityHeaders {
		compat.SendSessionAffinityHeaders = true
	}
	compat.SupportsLongCacheRetention = compatBool(raw.SupportsLongCacheRetention, compat.SupportsLongCacheRetention)
	return compat
}

func GetOpenAIResponsesCompat(model Model) OpenAIResponsesCompat {
	return OpenAIResponsesCompat{
		SendSessionIDHeader:        compatBool(model.Compat.SendSessionIDHeader, true),
		SupportsLongCacheRetention: compatBool(model.Compat.SupportsLongCacheRetention, true),
	}
}

func GetAnthropicMessagesCompat(model Model) AnthropicMessagesCompat {
	isFireworks := model.Provider == "fireworks"
	isCloudflareAIGatewayAnthropic := model.Provider == "cloudflare-ai-gateway" && strings.Contains(model.BaseURL, "anthropic")
	return AnthropicMessagesCompat{
		SupportsEagerToolInputStreaming: compatBool(model.Compat.SupportsEagerToolInputStreaming, !isFireworks),
		SupportsLongCacheRetention:      compatBool(model.Compat.SupportsLongCacheRetention, !isFireworks),
		SendSessionAffinityHeaders:      model.Compat.SendSessionAffinityHeaders || isFireworks || isCloudflareAIGatewayAnthropic,
		SupportsCacheControlOnTools:     compatBool(model.Compat.SupportsCacheControlOnTools, !isFireworks),
		ForceAdaptiveThinking:           compatBool(model.Compat.ForceAdaptiveThinking, inferredAnthropicForceAdaptiveThinking(model)),
		AllowEmptySignature:             compatBool(model.Compat.AllowEmptySignature, false),
	}
}

func effectiveThinkingLevelMap(model Model) map[string]*string {
	var out map[string]*string
	if len(model.ThinkingLevelMap) > 0 {
		out = make(map[string]*string, len(model.ThinkingLevelMap))
		for key, value := range model.ThinkingLevelMap {
			out[key] = value
		}
	}
	if len(model.ThinkingLevels) > 0 {
		if out == nil {
			out = map[string]*string{}
		}
		for _, level := range []ThinkingLevel{ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh} {
			key := string(level)
			if _, ok := out[key]; !ok && !containsThinkingLevel(model.ThinkingLevels, level) {
				out[key] = nil
			}
		}
		if _, ok := out[string(ThinkingXHigh)]; !ok && containsThinkingLevel(model.ThinkingLevels, ThinkingXHigh) {
			if effort, ok := inferredXHighThinkingEffort(model); ok {
				value := effort
				out[string(ThinkingXHigh)] = &value
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func inferredAnthropicForceAdaptiveThinking(model Model) bool {
	if model.API != "anthropic-messages" {
		return false
	}
	identity := modelIdentity(model)
	return strings.Contains(identity, "opus-4-6") ||
		strings.Contains(identity, "opus-4.6") ||
		strings.Contains(identity, "opus-4-7") ||
		strings.Contains(identity, "opus-4.7") ||
		strings.Contains(identity, "sonnet-4-6") ||
		strings.Contains(identity, "sonnet-4.6")
}

func inferredXHighThinkingEffort(model Model) (string, bool) {
	identity := modelIdentity(model)
	if strings.Contains(identity, "opus-4-6") || strings.Contains(identity, "opus-4.6") || strings.Contains(identity, "deepseek-v4") {
		return "max", true
	}
	return "", false
}

func modelIdentity(model Model) string {
	return strings.ToLower(model.Provider + " " + model.ID + " " + model.Name + " " + model.API)
}

func detectOpenAICompletionsCompat(model Model) OpenAICompletionsCompat {
	provider := model.Provider
	baseURL := model.BaseURL
	baseLower := strings.ToLower(baseURL)
	isZai := provider == "zai" || strings.Contains(baseLower, "api.z.ai")
	isTogether := provider == "together" || strings.Contains(baseLower, "api.together.ai") || strings.Contains(baseLower, "api.together.xyz")
	isMoonshot := provider == "moonshotai" || provider == "moonshotai-cn" || strings.Contains(baseLower, "api.moonshot.")
	isCloudflareWorkersAI := provider == "cloudflare-workers-ai" || strings.Contains(baseLower, "api.cloudflare.com")
	isCloudflareAIGateway := provider == "cloudflare-ai-gateway" || strings.Contains(baseLower, "gateway.ai.cloudflare.com")
	isOpenRouter := provider == "openrouter" || strings.Contains(baseLower, "openrouter.ai")
	isDeepSeekThinkingFormat := provider == "deepseek" || strings.Contains(baseLower, "deepseek.com") || strings.Contains(strings.ToLower(model.ID), "deepseek-v4")
	isNonStandard := provider == "cerebras" ||
		strings.Contains(baseLower, "cerebras.ai") ||
		provider == "xai" ||
		strings.Contains(baseLower, "api.x.ai") ||
		isTogether ||
		strings.Contains(baseLower, "chutes.ai") ||
		strings.Contains(baseLower, "deepseek.com") ||
		isZai ||
		isMoonshot ||
		provider == "opencode" ||
		strings.Contains(baseLower, "opencode.ai") ||
		isCloudflareWorkersAI ||
		isCloudflareAIGateway
	useMaxTokens := strings.Contains(baseLower, "chutes.ai") || isMoonshot || isCloudflareAIGateway || isTogether
	isGrok := provider == "xai" || strings.Contains(baseLower, "api.x.ai")
	cacheControlFormat := ""
	if provider == "openrouter" && strings.HasPrefix(model.ID, "anthropic/") {
		cacheControlFormat = "anthropic"
	}
	maxTokensField := "max_completion_tokens"
	if useMaxTokens {
		maxTokensField = "max_tokens"
	}
	thinkingFormat := "openai"
	switch {
	case isDeepSeekThinkingFormat:
		thinkingFormat = "deepseek"
	case isZai:
		thinkingFormat = "zai"
	case isTogether:
		thinkingFormat = "together"
	case isOpenRouter:
		thinkingFormat = "openrouter"
	}
	return OpenAICompletionsCompat{
		SupportsStore: !isNonStandard,
		// OpenRouter reasoning models use the standard `system` role, not
		// `developer` (most OpenRouter backends reject developer). Mirrors
		// openai-completions.ts: supportsDeveloperRole: !isNonStandard && !isOpenRouter.
		SupportsDeveloperRole:                       !isNonStandard && !isOpenRouter,
		SupportsReasoningEffort:                     !isGrok && !isZai && !isMoonshot && !isTogether && !isCloudflareAIGateway,
		SupportsUsageInStreaming:                    true,
		MaxTokensField:                              maxTokensField,
		RequiresToolResultName:                      false,
		RequiresAssistantAfterToolResult:            false,
		RequiresThinkingAsText:                      false,
		RequiresReasoningContentOnAssistantMessages: isDeepSeekThinkingFormat,
		ThinkingFormat:                              thinkingFormat,
		OpenRouterRouting:                           map[string]any{},
		VercelGatewayRouting:                        map[string]any{},
		ZaiToolStream:                               false,
		SupportsStrictMode:                          !isMoonshot && !isTogether && !isCloudflareAIGateway,
		CacheControlFormat:                          cacheControlFormat,
		SendSessionAffinityHeaders:                  false,
		SupportsLongCacheRetention:                  !isTogether && !isCloudflareWorkersAI && !isCloudflareAIGateway,
	}
}

func compatBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func boolPtr(value bool) *bool {
	return &value
}

type ContentBlock struct {
	Type              string          `json:"type"`
	Text              string          `json:"text,omitempty"`
	Data              string          `json:"data,omitempty"`
	MimeType          string          `json:"mimeType,omitempty"`
	Thinking          string          `json:"thinking,omitempty"`
	ID                string          `json:"id,omitempty"`
	Name              string          `json:"name,omitempty"`
	Arguments         json.RawMessage `json:"arguments,omitempty"`
	TextSignature     string          `json:"textSignature,omitempty"`
	Signature         string          `json:"signature,omitempty"`
	RawItem           json.RawMessage `json:"rawItem,omitempty"`
	Redacted          bool            `json:"redacted,omitempty"`
	ThoughtSignature  string          `json:"thoughtSignature,omitempty"`
	ThinkingSignature string          `json:"thinkingSignature,omitempty"` // Deprecated: read-only compatibility for older persisted sessions.
}

type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type Usage struct {
	Input       int  `json:"input"`
	Output      int  `json:"output"`
	CacheRead   int  `json:"cacheRead"`
	CacheWrite  int  `json:"cacheWrite"`
	TotalTokens int  `json:"totalTokens"`
	Cost        Cost `json:"cost"`
}

func (u Usage) IsZero() bool {
	return u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheWrite == 0 && u.TotalTokens == 0 && u.Cost == (Cost{})
}

func usageWithCost(model Model, usage Usage) Usage {
	usage.Cost = CalculateCost(model, usage)
	return usage
}

type Message interface {
	MessageRole() string
	Timestamp() int64
}

func UnmarshalMessageJSON(data []byte) (Message, error) {
	var header struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	switch header.Role {
	case "user":
		var raw struct {
			Role        string          `json:"role"`
			Content     json.RawMessage `json:"content"`
			TimestampMs int64           `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		blocks, err := unmarshalContentBlocks(raw.Content)
		if err != nil {
			return nil, err
		}
		return UserMessage{Role: "user", Content: blocks, TimestampMs: raw.TimestampMs}, nil
	case "assistant":
		var msg AssistantMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		if msg.Role == "" {
			msg.Role = "assistant"
		}
		return msg, nil
	case "toolResult":
		var msg ToolResultMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		if msg.Role == "" {
			msg.Role = "toolResult"
		}
		return msg, nil
	default:
		var msg CustomMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		if msg.Role == "" {
			msg.Role = header.Role
		}
		return msg, nil
	}
}

func unmarshalContentBlocks(raw json.RawMessage) ([]ContentBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return TextBlocks(text), nil
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

type UserMessage struct {
	Role        string         `json:"role"`
	Content     []ContentBlock `json:"content,omitempty"`
	TimestampMs int64          `json:"timestamp,omitempty"`
}

func (m UserMessage) MessageRole() string { return "user" }
func (m UserMessage) Timestamp() int64    { return m.TimestampMs }

type AssistantMessage struct {
	Role          string                       `json:"role"`
	Content       []ContentBlock               `json:"content,omitempty"`
	API           string                       `json:"api,omitempty"`
	Provider      string                       `json:"provider,omitempty"`
	Model         string                       `json:"model,omitempty"`
	ResponseModel string                       `json:"responseModel,omitempty"`
	ResponseID    string                       `json:"responseId,omitempty"`
	Diagnostics   []AssistantMessageDiagnostic `json:"diagnostics,omitempty"`
	Usage         Usage                        `json:"usage"`
	StopReason    string                       `json:"stopReason,omitempty"`
	ErrorMessage  string                       `json:"errorMessage,omitempty"`
	TimestampMs   int64                        `json:"timestamp,omitempty"`
}

func (m AssistantMessage) MessageRole() string { return "assistant" }
func (m AssistantMessage) Timestamp() int64    { return m.TimestampMs }

type ToolResultMessage struct {
	Role        string         `json:"role"`
	ToolCallID  string         `json:"toolCallId"`
	ToolName    string         `json:"toolName"`
	Content     []ContentBlock `json:"content,omitempty"`
	Details     any            `json:"details,omitempty"`
	IsError     bool           `json:"isError"`
	TimestampMs int64          `json:"timestamp,omitempty"`
}

func (m ToolResultMessage) MessageRole() string { return "toolResult" }
func (m ToolResultMessage) Timestamp() int64    { return m.TimestampMs }

// CustomMessage is a layering exception kept for persisted agent session
// compatibility. The upstream TS ai layer only has user, assistant, and
// toolResult messages; migrate this shape to packages/agent once the
// coding-agent and harness session readers no longer construct ai.CustomMessage
// directly. TODO(ai-custom-message-layering): track and complete that migration.
type CustomMessage struct {
	Role               string `json:"role"`
	Command            string `json:"command,omitempty"`
	Output             string `json:"output,omitempty"`
	ExitCode           *int   `json:"exitCode,omitempty"`
	Cancelled          bool   `json:"cancelled,omitempty"`
	Truncated          bool   `json:"truncated,omitempty"`
	FullOutputPath     string `json:"fullOutputPath,omitempty"`
	ExcludeFromContext bool   `json:"excludeFromContext,omitempty"`
	CustomType         string `json:"customType,omitempty"`
	Content            any    `json:"content,omitempty"`
	Display            bool   `json:"display,omitempty"`
	Details            any    `json:"details,omitempty"`
	Summary            string `json:"summary,omitempty"`
	FromID             string `json:"fromId,omitempty"`
	TokensBefore       int    `json:"tokensBefore,omitempty"`
	TimestampMs        int64  `json:"timestamp,omitempty"`
}

func (m CustomMessage) MessageRole() string {
	if m.Role != "" {
		return m.Role
	}
	return "custom"
}
func (m CustomMessage) Timestamp() int64 { return m.TimestampMs }

func NewUserMessage(text string, images []ContentBlock) UserMessage {
	text = aiutils.SanitizeUnicode(text)
	blocks := []ContentBlock{}
	if text != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: text})
	}
	blocks = append(blocks, sanitizeContentBlocks(images)...)
	return UserMessage{Role: "user", Content: blocks, TimestampMs: time.Now().UnixMilli()}
}

func NewAssistantMessage(api, provider, model string, blocks []ContentBlock, usage Usage, stopReason string) AssistantMessage {
	blocks = sanitizeContentBlocks(blocks)
	return AssistantMessage{
		Role:        "assistant",
		Content:     blocks,
		API:         api,
		Provider:    provider,
		Model:       model,
		Usage:       usage,
		StopReason:  stopReason,
		TimestampMs: time.Now().UnixMilli(),
	}
}

func NewAssistantMessageForModel(model Model, blocks []ContentBlock, usage Usage, stopReason string) AssistantMessage {
	return NewAssistantMessage(model.API, model.Provider, model.ID, blocks, usage, stopReason)
}

func NewToolResultMessage(callID, toolName string, blocks []ContentBlock, details any, isError bool) ToolResultMessage {
	blocks = sanitizeContentBlocks(blocks)
	return ToolResultMessage{
		Role:        "toolResult",
		ToolCallID:  callID,
		ToolName:    toolName,
		Content:     blocks,
		Details:     details,
		IsError:     isError,
		TimestampMs: time.Now().UnixMilli(),
	}
}

func TextBlocks(text string) []ContentBlock {
	text = aiutils.SanitizeUnicode(text)
	if text == "" {
		return []ContentBlock{}
	}
	return []ContentBlock{{Type: "text", Text: text}}
}

func MessageText(msg Message) string {
	blocks := MessageBlocks(msg)
	out := ""
	for _, b := range blocks {
		if b.Type == "text" {
			if out != "" {
				out += "\n"
			}
			out += b.Text
		}
	}
	if out != "" {
		return out
	}
	if custom, ok := msg.(CustomMessage); ok {
		if text, ok := custom.Content.(string); ok {
			return text
		}
		if custom.Summary != "" {
			return custom.Summary
		}
	}
	if custom, ok := msg.(*CustomMessage); ok && custom != nil {
		if text, ok := custom.Content.(string); ok {
			return text
		}
		if custom.Summary != "" {
			return custom.Summary
		}
	}
	return ""
}

func AssistantThinkingText(msg Message) string {
	out := ""
	for _, b := range MessageBlocks(msg) {
		if b.Type == "thinking" {
			if out != "" {
				out += "\n"
			}
			out += b.Thinking
		}
	}
	return out
}

func MessageBlocks(msg Message) []ContentBlock {
	switch m := msg.(type) {
	case nil:
		return nil
	case UserMessage:
		return m.Content
	case *UserMessage:
		if m == nil {
			return nil
		}
		return m.Content
	case AssistantMessage:
		return m.Content
	case *AssistantMessage:
		if m == nil {
			return nil
		}
		return m.Content
	case ToolResultMessage:
		return m.Content
	case *ToolResultMessage:
		if m == nil {
			return nil
		}
		return m.Content
	case CustomMessage:
		if blocks, ok := m.Content.([]ContentBlock); ok {
			return blocks
		}
		if text, ok := m.Content.(string); ok && text != "" {
			return []ContentBlock{{Type: "text", Text: text}}
		}
	case *CustomMessage:
		if m == nil {
			return nil
		}
		if blocks, ok := m.Content.([]ContentBlock); ok {
			return blocks
		}
		if text, ok := m.Content.(string); ok && text != "" {
			return []ContentBlock{{Type: "text", Text: text}}
		}
	case interface{ ContentBlocks() []ContentBlock }:
		return m.ContentBlocks()
	default:
		return nil
	}
	return nil
}

func MessageRole(msg Message) string {
	if msg == nil {
		return ""
	}
	return msg.MessageRole()
}

func MessageTimestamp(msg Message) int64 {
	if msg == nil {
		return 0
	}
	return msg.Timestamp()
}

func MessageUsage(msg Message) Usage {
	if assistant, ok := AsAssistantMessage(msg); ok {
		return assistant.Usage
	}
	return Usage{}
}

func MessageStopReason(msg Message) string {
	if assistant, ok := AsAssistantMessage(msg); ok {
		return assistant.StopReason
	}
	return ""
}

func MessageErrorMessage(msg Message) string {
	if assistant, ok := AsAssistantMessage(msg); ok {
		return assistant.ErrorMessage
	}
	return ""
}

func MessageToolCallID(msg Message) string {
	if toolResult, ok := AsToolResultMessage(msg); ok {
		return toolResult.ToolCallID
	}
	return ""
}

func MessageToolName(msg Message) string {
	if toolResult, ok := AsToolResultMessage(msg); ok {
		return toolResult.ToolName
	}
	return ""
}

func MessageIsError(msg Message) bool {
	if toolResult, ok := AsToolResultMessage(msg); ok {
		return toolResult.IsError
	}
	return false
}

func AsAssistantMessage(msg Message) (AssistantMessage, bool) {
	switch m := msg.(type) {
	case AssistantMessage:
		return m, true
	case *AssistantMessage:
		if m == nil {
			return AssistantMessage{}, false
		}
		return *m, true
	default:
		return AssistantMessage{}, false
	}
}

func AsUserMessage(msg Message) (UserMessage, bool) {
	switch m := msg.(type) {
	case UserMessage:
		return m, true
	case *UserMessage:
		if m == nil {
			return UserMessage{}, false
		}
		return *m, true
	default:
		return UserMessage{}, false
	}
}

func AsToolResultMessage(msg Message) (ToolResultMessage, bool) {
	switch m := msg.(type) {
	case ToolResultMessage:
		return m, true
	case *ToolResultMessage:
		if m == nil {
			return ToolResultMessage{}, false
		}
		return *m, true
	default:
		return ToolResultMessage{}, false
	}
}

func AsCustomMessage(msg Message) (CustomMessage, bool) {
	switch m := msg.(type) {
	case CustomMessage:
		return m, true
	case *CustomMessage:
		if m == nil {
			return CustomMessage{}, false
		}
		return *m, true
	default:
		return CustomMessage{}, false
	}
}

func thinkingBlockSignature(block ContentBlock) string {
	if block.Signature != "" {
		return block.Signature
	}
	return block.ThinkingSignature
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

type ToolCall struct {
	Type             string          `json:"type,omitempty"`
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
}

type ToolResult struct {
	Content []ContentBlock `json:"content"`
	Details any            `json:"details,omitempty"`
	IsError bool           `json:"isError"`
}

type Event map[string]any

type EventSink func(Event)
