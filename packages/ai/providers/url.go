package providers

import "strings"

func OpenAIChatURL(base string) string {
	if strings.Contains(base, "/chat/completions") || strings.Contains(base, "/chatcompletion") {
		return base
	}
	if strings.TrimSpace(base) == "" {
		base = "https://api.openai.com/v1"
	}
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/chat/completions"
	}
	if strings.HasSuffix(trimmed, "/compat") || strings.HasSuffix(trimmed, "/openai") {
		return trimmed + "/chat/completions"
	}
	hostAndPath := trimmed
	if index := strings.Index(hostAndPath, "://"); index >= 0 {
		hostAndPath = hostAndPath[index+3:]
	}
	if !strings.Contains(hostAndPath, "/") {
		return trimmed + "/v1/chat/completions"
	}
	return trimmed
}

func AnthropicBaseURL(base string) string {
	endpoint := AnthropicMessagesURL(base)
	trimmed := strings.TrimRight(endpoint, "/")
	return strings.TrimRight(strings.TrimSuffix(trimmed, "/v1/messages"), "/")
}

func AnthropicMessagesURL(base string) string {
	if strings.Contains(base, "/v1/messages") {
		return base
	}
	if strings.TrimSpace(base) == "" {
		base = "https://api.anthropic.com"
	}
	trimmed := strings.TrimRight(base, "/")
	return trimmed + "/v1/messages"
}

func MistralChatURL(base string) string {
	if strings.TrimSpace(base) == "" {
		base = "https://api.mistral.ai"
	}
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/chat/completions") {
		return trimmed
	}
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/chat/completions"
	}
	return trimmed + "/v1/chat/completions"
}

func GoogleVertexCustomBaseURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" || strings.Contains(trimmed, "{location}") {
		return ""
	}
	return trimmed
}
