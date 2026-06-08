package providers

import (
	"os"
	"regexp"
	"strings"
)

var envKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// configEnvVarNameRE mirrors ENV_VAR_NAME_RE in resolve-config-value.ts: an
// environment-variable name may start with a letter or underscore and may be
// lower- or mixed-case (broader than the legacy all-caps envKeyPattern).
var configEnvVarNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// GetEnvAPIKey resolves an API key for a provider from the known environment
// variables mirrored from TS env-api-keys.ts. Unknown/custom providers return
// empty unless the model itself declares an explicit EnvKey.
//
// Mirroring TS getEnvApiKey, the two ambient-credential providers return the
// "<authenticated>" sentinel when ambient credentials are present but no
// explicit API-key env var is set: google-vertex (Application Default
// Credentials + GOOGLE_CLOUD_PROJECT + GOOGLE_CLOUD_LOCATION) and amazon-bedrock
// (any of the AWS credential sources).
func GetEnvAPIKey(provider string) string {
	keys := ProviderEnvKeys(provider)
	for _, env := range keys {
		if value := os.Getenv(env); value != "" {
			return value
		}
	}
	switch provider {
	case "google-vertex":
		if HasGoogleVertexADC() {
			return "<authenticated>"
		}
	case "amazon-bedrock":
		if _, _, ok := BedrockEnvCredentials(); ok {
			return "<authenticated>"
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

// EnvKeyFromAPIKey returns the single environment-variable name when the
// configured apiKey value is exactly one "$VAR" or "${VAR}" reference, mirroring
// TS getConfigValueEnvVarName. This is the lazily-resolved single-env-var path
// (the resolved value is read via os.Getenv at request time). It returns "" for
// "!command" forms, multi-part templates (e.g. "Bearer $TOKEN"), escaped
// literals, and plain literals — those are carried verbatim on Model.APIKey and
// resolved through ai.ResolveConfigValue at key-fetch time.
//
// A bare all-caps name (e.g. "OPENAI_API_KEY") is still accepted for backwards
// compatibility, matching the prior behaviour and the legacy migration path.
func EnvKeyFromAPIKey(value string) string {
	if value == "" || strings.HasPrefix(value, "!") {
		return ""
	}
	if name, ok := singleEnvVarReference(value); ok {
		return name
	}
	if envKeyPattern.MatchString(value) {
		return value
	}
	return ""
}

// singleEnvVarReference reports whether value is exactly one "$VAR" / "${VAR}"
// reference and returns the referenced env-var name. It mirrors the single-part
// template case of resolve-config-value.ts.
func singleEnvVarReference(value string) (string, bool) {
	if !strings.HasPrefix(value, "$") || len(value) < 2 {
		return "", false
	}
	rest := value[1:]
	if strings.HasPrefix(rest, "{") {
		if !strings.HasSuffix(rest, "}") {
			return "", false
		}
		name := rest[1 : len(rest)-1]
		if configEnvVarNameRE.MatchString(name) {
			return name, true
		}
		return "", false
	}
	// Bare "$VAR": the whole remainder must be a valid env-var name (otherwise it
	// is a multi-part template such as "$TOKEN extra" handled downstream).
	if configEnvVarNameRE.MatchString(rest) {
		return rest, true
	}
	return "", false
}

// LiteralAPIKey returns the configured value to carry verbatim on Model.APIKey
// for any value that is NOT a single-env-var reference (i.e. "!command",
// multi-part templates, escaped literals, and plain literals). Such values are
// resolved through ai.ResolveConfigValue at key-fetch time. Single-env-var
// references return "" because they are handled lazily via Model.EnvKey.
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
