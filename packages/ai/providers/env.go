package providers

import (
	"os"
	"regexp"
	"strings"
)

var envKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// GetEnvAPIKey resolves an API key for a provider from the known environment
// variables mirrored from TS env-api-keys.ts. Unknown/custom providers return
// empty unless the model itself declares an explicit EnvKey.
func GetEnvAPIKey(provider string) string {
	keys := ProviderEnvKeys(provider)
	for _, env := range keys {
		if value := os.Getenv(env); value != "" {
			return value
		}
	}
	return ""
}

// SynthesizedEnvKey returns the conventional <PROVIDER>_API_KEY env var for a
// provider, or "" for the empty provider. It remains available for callers that
// need to suggest or normalize an explicit custom EnvKey, but ProviderEnvKeys /
// GetEnvAPIKey do not use it implicitly.
func SynthesizedEnvKey(provider string) string {
	if provider == "" {
		return ""
	}
	return strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_API_KEY"
}

// ProviderEnvKeys mirrors getApiKeyEnvVars in packages/ai/src/env-api-keys.ts
// 1:1. Providers absent from the TS map (including unknown/custom providers and
// ambient-credential providers like amazon-bedrock) return an empty slice — TS
// findEnvKeys returns undefined for them. Ambient-credential providers
// (amazon-bedrock via AWS creds, google-vertex via ADC) are resolved separately
// through their ambient auth paths, and openai-codex is OAuth-only.
func ProviderEnvKeys(provider string) []string {
	switch provider {
	case "github-copilot":
		return []string{"COPILOT_GITHUB_TOKEN"}
	case "anthropic":
		return []string{"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}
	case "ant-ling":
		return []string{"ANT_LING_API_KEY"}
	case "openai":
		return []string{"OPENAI_API_KEY"}
	case "azure-openai-responses":
		return []string{"AZURE_OPENAI_API_KEY"}
	case "nvidia":
		return []string{"NVIDIA_API_KEY"}
	case "deepseek":
		return []string{"DEEPSEEK_API_KEY"}
	case "google":
		return []string{"GEMINI_API_KEY"}
	case "google-vertex":
		return []string{"GOOGLE_CLOUD_API_KEY"}
	case "groq":
		return []string{"GROQ_API_KEY"}
	case "cerebras":
		return []string{"CEREBRAS_API_KEY"}
	case "xai":
		return []string{"XAI_API_KEY"}
	case "openrouter":
		return []string{"OPENROUTER_API_KEY"}
	case "vercel-ai-gateway":
		return []string{"AI_GATEWAY_API_KEY"}
	case "zai":
		return []string{"ZAI_API_KEY"}
	case "zai-coding-cn":
		return []string{"ZAI_CODING_CN_API_KEY"}
	case "mistral":
		return []string{"MISTRAL_API_KEY"}
	case "minimax":
		return []string{"MINIMAX_API_KEY"}
	case "minimax-cn":
		return []string{"MINIMAX_CN_API_KEY"}
	case "moonshotai", "moonshotai-cn":
		return []string{"MOONSHOT_API_KEY"}
	case "huggingface":
		return []string{"HF_TOKEN"}
	case "fireworks":
		return []string{"FIREWORKS_API_KEY"}
	case "together":
		return []string{"TOGETHER_API_KEY"}
	case "opencode", "opencode-go":
		return []string{"OPENCODE_API_KEY"}
	case "kimi-coding":
		return []string{"KIMI_API_KEY"}
	case "cloudflare-workers-ai", "cloudflare-ai-gateway":
		return []string{"CLOUDFLARE_API_KEY"}
	case "xiaomi":
		return []string{"XIAOMI_API_KEY"}
	case "xiaomi-token-plan-cn":
		return []string{"XIAOMI_TOKEN_PLAN_CN_API_KEY"}
	case "xiaomi-token-plan-ams":
		return []string{"XIAOMI_TOKEN_PLAN_AMS_API_KEY"}
	case "xiaomi-token-plan-sgp":
		return []string{"XIAOMI_TOKEN_PLAN_SGP_API_KEY"}
	default:
		return []string{}
	}
}

func EnvKeyFromAPIKey(value string) string {
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "$") {
		return strings.TrimPrefix(value, "$")
	}
	if envKeyPattern.MatchString(value) {
		return value
	}
	return ""
}

func LiteralAPIKey(value string) string {
	if value == "" || EnvKeyFromAPIKey(value) != "" {
		return ""
	}
	return value
}

func AzureBaseURL() string {
	if base := os.Getenv("AZURE_OPENAI_BASE_URL"); base != "" {
		version := os.Getenv("AZURE_OPENAI_API_VERSION")
		if version == "" {
			version = "2024-10-21"
		}
		deployment := os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")
		if deployment == "" {
			deployment = "gpt-4.1"
		}
		return strings.TrimRight(base, "/") + "/openai/deployments/" + deployment + "/chat/completions?api-version=" + version
	}
	return "https://example.openai.azure.com/openai/deployments/gpt-4.1/chat/completions?api-version=2024-10-21"
}
