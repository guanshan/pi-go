package providers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

func ResolveCacheRetention(value string) string {
	switch value {
	case "none", "short", "long":
		return value
	}
	if os.Getenv("PI_CACHE_RETENTION") == "long" {
		return "long"
	}
	return "short"
}

func OpenAICompatSupportsLongCacheRetention(value *bool) bool {
	if value != nil {
		return *value
	}
	return true
}

func OpenAICompatCacheControlFormat(format, provider, modelID string) string {
	return format
}

func OpenAIPromptCacheKey(baseURL string, supportsLong bool, cacheRetention string, sessionID string) string {
	if cacheRetention == "none" || sessionID == "" {
		return ""
	}
	if strings.Contains(baseURL, "api.openai.com") || (cacheRetention == "long" && supportsLong) {
		return ClampOpenAIPromptCacheKey(sessionID)
	}
	return ""
}

func ClampOpenAIPromptCacheKey(key string) string {
	const maxLength = 64
	chars := []rune(key)
	if len(chars) <= maxLength {
		return key
	}
	return string(chars[:maxLength])
}

func OpenAICacheControl(format string, supportsLong bool, cacheRetention string) map[string]any {
	if format != "anthropic" || cacheRetention == "none" {
		return nil
	}
	cacheControl := map[string]any{"type": "ephemeral"}
	if cacheRetention == "long" && supportsLong {
		cacheControl["ttl"] = "1h"
	}
	return cacheControl
}

func ApplyOpenAIAnthropicCacheControl(messages []map[string]any, tools []map[string]any, cacheControl map[string]any) {
	for _, message := range messages {
		role, _ := message["role"].(string)
		if role == "system" || role == "developer" {
			addOpenAICacheControlToTextContent(message, cacheControl)
			break
		}
	}
	if len(tools) > 0 {
		tools[len(tools)-1]["cache_control"] = cacheControl
	}
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := messages[i]["role"].(string)
		if role == "user" || role == "assistant" {
			if addOpenAICacheControlToTextContent(messages[i], cacheControl) {
				return
			}
		}
	}
}

func addOpenAICacheControlToTextContent(message map[string]any, cacheControl map[string]any) bool {
	switch content := message["content"].(type) {
	case string:
		if content == "" {
			return false
		}
		message["content"] = []map[string]any{{
			"type":          "text",
			"text":          content,
			"cache_control": cacheControl,
		}}
		return true
	case []map[string]any:
		for i := len(content) - 1; i >= 0; i-- {
			if content[i]["type"] == "text" {
				content[i]["cache_control"] = cacheControl
				return true
			}
		}
	case []any:
		for i := len(content) - 1; i >= 0; i-- {
			if part, ok := content[i].(map[string]any); ok && part["type"] == "text" {
				part["cache_control"] = cacheControl
				return true
			}
		}
	}
	return false
}

func OpenAIExtraHeaders(cacheRetention string, sessionID string, sendSessionAffinity bool, headers map[string]string) map[string]string {
	out := map[string]string{}
	if cacheRetention != "none" && sessionID != "" && sendSessionAffinity {
		out["session_id"] = sessionID
		out["x-client-request-id"] = sessionID
		out["x-session-affinity"] = sessionID
	}
	for key, value := range headers {
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func OpenAIResponsesSendSessionIDHeader(value *bool) bool {
	if value != nil {
		return *value
	}
	return true
}

func OpenAIResponsesURL(base string) string {
	if strings.TrimSpace(base) == "" {
		base = "https://api.openai.com/v1"
	}
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/responses") {
		return trimmed
	}
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/responses"
	}
	return trimmed + "/v1/responses"
}

func CodexResponsesURL(base string) string {
	if strings.TrimSpace(base) == "" {
		base = "https://chatgpt.com/backend-api"
	}
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/codex/responses") {
		return trimmed
	}
	if strings.HasSuffix(trimmed, "/codex") {
		return trimmed + "/responses"
	}
	return trimmed + "/codex/responses"
}

func AzureResponsesURL(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = strings.TrimSpace(os.Getenv("AZURE_OPENAI_BASE_URL"))
	}
	if base == "" && os.Getenv("AZURE_OPENAI_RESOURCE_NAME") != "" {
		base = "https://" + os.Getenv("AZURE_OPENAI_RESOURCE_NAME") + ".openai.azure.com/openai/v1"
	}
	if base == "" {
		base = "https://example.openai.azure.com/openai/v1"
	}
	trimmed := strings.TrimRight(base, "/")
	hostAndPath := trimmed
	if index := strings.Index(hostAndPath, "://"); index >= 0 {
		hostAndPath = hostAndPath[index+3:]
	}
	if !strings.Contains(hostAndPath, "/") {
		trimmed += "/openai/v1"
	} else if strings.HasSuffix(trimmed, "/openai") {
		trimmed += "/v1"
	}
	if !strings.Contains(trimmed, "/responses") {
		trimmed += "/responses"
	}
	version := os.Getenv("AZURE_OPENAI_API_VERSION")
	if version == "" {
		version = "v1"
	}
	if strings.Contains(trimmed, "?") {
		return trimmed
	}
	return trimmed + "?api-version=" + version
}

func AzureResponsesDeploymentName(modelID string) string {
	if mapped := AzureDeploymentFromMap(modelID, os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP")); mapped != "" {
		return mapped
	}
	if deployment := os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"); deployment != "" {
		return deployment
	}
	return modelID
}

func AzureDeploymentFromMap(modelID, value string) string {
	for _, entry := range strings.Split(value, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == modelID {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func ExtractCodexAccountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("failed to extract accountId from token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("failed to extract accountId from token")
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", errors.New("failed to extract accountId from token")
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	accountID, _ := auth["chatgpt_account_id"].(string)
	if accountID == "" {
		return "", errors.New("failed to extract accountId from token")
	}
	return accountID, nil
}

func NormalizeIDPart(part string) string {
	var b strings.Builder
	for _, r := range part {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	return strings.TrimRight(b.String(), "_")
}

// ResponsesTextMessageID resolves the id for an assistant text item. When the
// text block carries a v1 signature its id is used as-is, falling back to a
// short hash when it exceeds OpenAI's 64-character limit (a hash, not a
// truncation, so distinct ids cannot collide). Without a usable signature id, a
// per-message/per-text-block fallback id is generated so multiple text blocks
// in one turn stay unique. Mirrors openai-responses-shared.ts.
func ResponsesTextMessageID(signature string, messageIndex, textBlockIndex int) string {
	id := responsesTextSignatureID(signature)
	if id == "" {
		if textBlockIndex == 0 {
			return fmt.Sprintf("msg_pi_%d", messageIndex)
		}
		return fmt.Sprintf("msg_pi_%d_%d", messageIndex, textBlockIndex)
	}
	if len([]rune(id)) > 64 {
		return "msg_" + MistralShortHash(id)
	}
	return id
}

func responsesTextSignatureID(signature string) string {
	if signature == "" {
		return ""
	}
	if strings.HasPrefix(signature, "{") {
		var parsed struct {
			Version int    `json:"v"`
			ID      string `json:"id"`
		}
		if json.Unmarshal([]byte(signature), &parsed) == nil && parsed.Version == 1 && parsed.ID != "" {
			return parsed.ID
		}
	}
	return signature
}

func ResponsesToolCallIDParts(id string) (string, string) {
	parts := strings.SplitN(id, "|", 2)
	callID := NormalizeIDPart(parts[0])
	if callID == "" {
		callID = "call_" + NormalizeIDPart(MistralShortHash(id))
	}
	if len(parts) == 1 {
		return callID, ""
	}
	itemID := NormalizeIDPart(parts[1])
	if itemID == "" {
		itemID = "fc_" + NormalizeIDPart(MistralShortHash(id))
	} else if !strings.HasPrefix(itemID, "fc_") {
		itemID = NormalizeIDPart("fc_" + itemID)
	}
	return callID, itemID
}

func ResponsesStopReason(status string) string {
	stop, _ := ResponsesStopReasonResult(status)
	return stop
}

func ResponsesStopReasonResult(status string) (string, string) {
	switch status {
	case "", "completed", "in_progress", "queued":
		return "stop", ""
	case "incomplete":
		return "length", ""
	case "failed", "cancelled":
		return "error", ""
	default:
		return "error", "Unhandled response status: " + status
	}
}

func ResponsesReasoningEffort(levelMap map[string]*string, level string) string {
	if mapped, ok := levelMap[level]; ok {
		if mapped == nil {
			return "none"
		}
		return *mapped
	}
	return level
}

func AnthropicStopReason(reason string, hasToolCall bool) (string, string) {
	switch reason {
	case "", "end_turn", "pause_turn", "stop_sequence":
		if hasToolCall {
			return "toolUse", ""
		}
		return "stop", ""
	case "max_tokens":
		return "length", ""
	case "tool_use":
		return "toolUse", ""
	case "refusal", "sensitive":
		return "error", "Provider stop_reason: " + reason
	default:
		return "error", "Unhandled stop reason: " + reason
	}
}

func BaseURLIncludesAPIVersion(baseURL string) bool {
	parts := strings.Split(strings.Trim(baseURL, "/"), "/")
	for _, part := range parts {
		if len(part) >= 2 && part[0] == 'v' && part[1] >= '0' && part[1] <= '9' {
			return true
		}
	}
	return false
}

func GoogleStopReason(reason string) string {
	switch reason {
	case "", "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	default:
		return "error"
	}
}

func HasHeaderName(headers map[string]string, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func SDKBaseURLForEndpoint(endpoint, suffix string) (string, bool) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return "", false
	}
	if !strings.HasSuffix(endpoint, suffix) {
		return "", false
	}
	return strings.TrimRight(strings.TrimSuffix(endpoint, suffix), "/"), true
}

func IsOpenAIHostedBaseURL(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, "api.openai.com")
}

func SDKHeadersWithoutAuth(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "authorization", "content-type":
			continue
		default:
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
