package ai

import (
	"reflect"
	"testing"
)

// TestEnvKeysPerProviderMatchTS asserts ProviderEnvKeys mirrors getApiKeyEnvVars
// in packages/ai/src/env-api-keys.ts 1:1 for every provider in the TS map,
// including the three providers added by the 964-model catalog bump
// (ant-ling, nvidia, zai-coding-cn). It also pins the deliberate divergences:
// providers that TS resolves via ambient credentials (amazon-bedrock) or that
// are OAuth-only (openai-codex) enumerate NO env keys here, and unknown
// providers return an empty slice (TS findEnvKeys returns undefined).
func TestEnvKeysPerProviderMatchTS(t *testing.T) {
	// Mirror of the TS getApiKeyEnvVars table plus the two hard-coded branches
	// (github-copilot, anthropic). Keep in lockstep with env-api-keys.ts.
	want := map[string][]string{
		"github-copilot":         {"COPILOT_GITHUB_TOKEN"},
		"anthropic":              {"ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
		"ant-ling":               {"ANT_LING_API_KEY"},
		"openai":                 {"OPENAI_API_KEY"},
		"azure-openai-responses": {"AZURE_OPENAI_API_KEY"},
		"nvidia":                 {"NVIDIA_API_KEY"},
		"deepseek":               {"DEEPSEEK_API_KEY"},
		"google":                 {"GEMINI_API_KEY"},
		"google-vertex":          {"GOOGLE_CLOUD_API_KEY"},
		"groq":                   {"GROQ_API_KEY"},
		"cerebras":               {"CEREBRAS_API_KEY"},
		"xai":                    {"XAI_API_KEY"},
		"openrouter":             {"OPENROUTER_API_KEY"},
		"vercel-ai-gateway":      {"AI_GATEWAY_API_KEY"},
		"zai":                    {"ZAI_API_KEY"},
		"zai-coding-cn":          {"ZAI_CODING_CN_API_KEY"},
		"mistral":                {"MISTRAL_API_KEY"},
		"minimax":                {"MINIMAX_API_KEY"},
		"minimax-cn":             {"MINIMAX_CN_API_KEY"},
		"moonshotai":             {"MOONSHOT_API_KEY"},
		"moonshotai-cn":          {"MOONSHOT_API_KEY"},
		"huggingface":            {"HF_TOKEN"},
		"fireworks":              {"FIREWORKS_API_KEY"},
		"together":               {"TOGETHER_API_KEY"},
		"opencode":               {"OPENCODE_API_KEY"},
		"opencode-go":            {"OPENCODE_API_KEY"},
		"kimi-coding":            {"KIMI_API_KEY"},
		"cloudflare-workers-ai":  {"CLOUDFLARE_API_KEY"},
		"cloudflare-ai-gateway":  {"CLOUDFLARE_API_KEY"},
		"xiaomi":                 {"XIAOMI_API_KEY"},
		"xiaomi-token-plan-cn":   {"XIAOMI_TOKEN_PLAN_CN_API_KEY"},
		"xiaomi-token-plan-ams":  {"XIAOMI_TOKEN_PLAN_AMS_API_KEY"},
		"xiaomi-token-plan-sgp":  {"XIAOMI_TOKEN_PLAN_SGP_API_KEY"},
	}
	for provider, expected := range want {
		got := ProviderEnvKeys(provider)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("ProviderEnvKeys(%q)=%v, want %v", provider, got, expected)
		}
	}

	// Providers TS does NOT put in getApiKeyEnvVars: ambient-credential and
	// OAuth-only providers, plus unknown providers, enumerate no env keys.
	for _, provider := range []string{
		"amazon-bedrock", // resolved via ambient AWS credentials
		"openai-codex",   // OAuth-only
		"azure-openai",   // only azure-openai-responses is mapped in TS
		"kimi",           // TS only maps kimi-coding
		"this-provider-does-not-exist",
		"",
	} {
		if got := ProviderEnvKeys(provider); len(got) != 0 {
			t.Errorf("ProviderEnvKeys(%q)=%v, want empty (not in TS getApiKeyEnvVars)", provider, got)
		}
	}
}

// TestEnvKeysPerProviderResolution exercises GetEnvAPIKey for the three new
// providers and confirms the removed Go-only fallbacks (GOOGLE_API_KEY for
// google, MOONSHOT_API_KEY for kimi) no longer resolve, matching TS.
func TestEnvKeysPerProviderResolution(t *testing.T) {
	t.Setenv("ANT_LING_API_KEY", "ant-key")
	t.Setenv("NVIDIA_API_KEY", "nvidia-key")
	t.Setenv("ZAI_CODING_CN_API_KEY", "zai-cn-key")
	if got := GetEnvAPIKey("ant-ling"); got != "ant-key" {
		t.Fatalf("GetEnvAPIKey(ant-ling)=%q, want ant-key", got)
	}
	if got := GetEnvAPIKey("nvidia"); got != "nvidia-key" {
		t.Fatalf("GetEnvAPIKey(nvidia)=%q, want nvidia-key", got)
	}
	if got := GetEnvAPIKey("zai-coding-cn"); got != "zai-cn-key" {
		t.Fatalf("GetEnvAPIKey(zai-coding-cn)=%q, want zai-cn-key", got)
	}

	// GOOGLE_API_KEY is no longer a google fallback (TS only uses GEMINI_API_KEY).
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "google-only")
	if got := GetEnvAPIKey("google"); got != "" {
		t.Fatalf("GetEnvAPIKey(google) with only GOOGLE_API_KEY=%q, want empty", got)
	}

	// MOONSHOT_API_KEY is no longer a kimi fallback (TS kimi-coding -> KIMI_API_KEY).
	t.Setenv("KIMI_API_KEY", "")
	t.Setenv("MOONSHOT_API_KEY", "moonshot-only")
	if got := GetEnvAPIKey("kimi"); got != "" {
		t.Fatalf("GetEnvAPIKey(kimi) with only MOONSHOT_API_KEY=%q, want empty", got)
	}

	t.Setenv("THIS_PROVIDER_DOES_NOT_EXIST_API_KEY", "custom-key")
	if got := GetEnvAPIKey("this-provider-does-not-exist"); got != "" {
		t.Fatalf("GetEnvAPIKey(unknown)=%q, want empty", got)
	}
	auth := NewAuthStorage(t.TempDir())
	model := Model{Provider: "this-provider-does-not-exist", ID: "custom", API: "openai-completions"}
	if got := auth.APIKey(model); got != "" {
		t.Fatalf("AuthStorage.APIKey(unknown)=%q, want empty", got)
	}
	model.EnvKey = "THIS_PROVIDER_DOES_NOT_EXIST_API_KEY"
	if got := auth.APIKey(model); got != "custom-key" {
		t.Fatalf("AuthStorage.APIKey(explicit EnvKey)=%q, want custom-key", got)
	}
}
