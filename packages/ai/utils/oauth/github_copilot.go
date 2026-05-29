package oauth

import (
	"net/url"
	"regexp"
	"strings"
)

const gitHubCopilotDefaultBaseURL = "https://api.individual.githubcopilot.com"

func NormalizeDomain(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func GitHubCopilotBaseURL(token string, enterpriseDomain string) string {
	if token != "" {
		if match := regexp.MustCompile(`proxy-ep=([^;]+)`).FindStringSubmatch(token); len(match) == 2 {
			return "https://" + strings.Replace(match[1], "proxy.", "api.", 1)
		}
	}
	if enterpriseDomain != "" {
		if domain := NormalizeDomain(enterpriseDomain); domain != "" {
			return "https://copilot-api." + domain
		}
		return "https://copilot-api." + enterpriseDomain
	}
	return gitHubCopilotDefaultBaseURL
}

func GitHubCopilotHeaders() map[string]string {
	return map[string]string{
		"User-Agent":             "GitHubCopilotChat/0.35.0",
		"Editor-Version":         "vscode/1.107.0",
		"Editor-Plugin-Version":  "copilot-chat/0.35.0",
		"Copilot-Integration-Id": "vscode-chat",
	}
}
