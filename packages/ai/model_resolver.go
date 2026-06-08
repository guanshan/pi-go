package ai

// DefaultModelPerProvider maps each known provider to its curated default model
// id. It mirrors defaultModelPerProvider in the upstream TypeScript
// model-resolver.ts (lines 14-50). Initial-model auto-selection iterates this
// map in insertion order and prefers each provider's default model over a bare
// "first auth-configured catalog model" fallback.
//
// defaultModelPerProviderOrder preserves the TS object key order so the
// provider iteration order matches upstream exactly.
var defaultModelPerProviderOrder = []string{
	"amazon-bedrock",
	"ant-ling",
	"anthropic",
	"openai",
	"azure-openai-responses",
	"openai-codex",
	"nvidia",
	"deepseek",
	"google",
	"google-vertex",
	"github-copilot",
	"openrouter",
	"vercel-ai-gateway",
	"xai",
	"groq",
	"cerebras",
	"zai",
	"zai-coding-cn",
	"mistral",
	"minimax",
	"minimax-cn",
	"moonshotai",
	"moonshotai-cn",
	"huggingface",
	"fireworks",
	"together",
	"opencode",
	"opencode-go",
	"kimi-coding",
	"cloudflare-workers-ai",
	"cloudflare-ai-gateway",
	"xiaomi",
	"xiaomi-token-plan-cn",
	"xiaomi-token-plan-ams",
	"xiaomi-token-plan-sgp",
}

// DefaultModelPerProvider holds the curated default model id for each known
// provider. Mirrors defaultModelPerProvider in TS model-resolver.ts.
var DefaultModelPerProvider = map[string]string{
	"amazon-bedrock":         "us.anthropic.claude-opus-4-6-v1",
	"ant-ling":               "Ring-2.6-1T",
	"anthropic":              "claude-opus-4-8",
	"openai":                 "gpt-5.4",
	"azure-openai-responses": "gpt-5.4",
	"openai-codex":           "gpt-5.5",
	"nvidia":                 "nvidia/nemotron-3-super-120b-a12b",
	"deepseek":               "deepseek-v4-pro",
	"google":                 "gemini-3.1-pro-preview",
	"google-vertex":          "gemini-3.1-pro-preview",
	"github-copilot":         "gpt-5.4",
	"openrouter":             "moonshotai/kimi-k2.6",
	"vercel-ai-gateway":      "zai/glm-5.1",
	"xai":                    "grok-4.20-0309-reasoning",
	"groq":                   "openai/gpt-oss-120b",
	"cerebras":               "zai-glm-4.7",
	"zai":                    "glm-5.1",
	"zai-coding-cn":          "glm-5.1",
	"mistral":                "devstral-medium-latest",
	"minimax":                "MiniMax-M2.7",
	"minimax-cn":             "MiniMax-M2.7",
	"moonshotai":             "kimi-k2.6",
	"moonshotai-cn":          "kimi-k2.6",
	"huggingface":            "moonshotai/Kimi-K2.6",
	"fireworks":              "accounts/fireworks/models/kimi-k2p6",
	"together":               "moonshotai/Kimi-K2.6",
	"opencode":               "kimi-k2.6",
	"opencode-go":            "kimi-k2.6",
	"kimi-coding":            "kimi-for-coding",
	"cloudflare-workers-ai":  "@cf/moonshotai/kimi-k2.6",
	"cloudflare-ai-gateway":  "workers-ai/@cf/moonshotai/kimi-k2.6",
	"xiaomi":                 "mimo-v2.5-pro",
	"xiaomi-token-plan-cn":   "mimo-v2.5-pro",
	"xiaomi-token-plan-ams":  "mimo-v2.5-pro",
	"xiaomi-token-plan-sgp":  "mimo-v2.5-pro",
}

// defaultModelForAvailable returns the curated default model among the supplied
// available models, iterating DefaultModelPerProvider in TS key order and
// returning the first provider whose curated default id is present. Mirrors the
// step-4 loop in TS findInitialModel.
func defaultModelForAvailable(available []Model) (Model, bool) {
	for _, provider := range defaultModelPerProviderOrder {
		defaultID := DefaultModelPerProvider[provider]
		for _, m := range available {
			if m.Provider == provider && m.ID == defaultID {
				return m, true
			}
		}
	}
	return Model{}, false
}
