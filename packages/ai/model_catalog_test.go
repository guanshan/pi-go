package ai

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestModelCatalogIncludesGeneratedModels(t *testing.T) {
	if len(GeneratedModels()) != 924 {
		t.Fatalf("generated model count=%d", len(GeneratedModels()))
	}
	models := AllKnownModels()
	seen := map[string]bool{}
	for _, model := range models {
		key := model.Provider + "\x00" + model.ID
		if seen[key] {
			t.Fatalf("duplicate model in catalog: %s/%s", model.Provider, model.ID)
		}
		seen[key] = true
	}
	bedrock, ok := Find(models, "amazon-bedrock", "amazon.nova-2-lite-v1:0")
	if !ok {
		t.Fatal("missing generated Bedrock model")
	}
	if bedrock.API != "bedrock-converse-stream" || bedrock.ContextWindow != 128000 || bedrock.Cost.Output != 2.75 {
		t.Fatalf("bedrock model=%#v", bedrock)
	}
	mistral, ok := Find(models, "mistral", "magistral-small")
	if !ok {
		t.Fatal("missing generated Mistral model")
	}
	if mistral.API != "mistral-conversations" || !mistral.Reasoning || !SupportsInput(mistral, "text") {
		t.Fatalf("mistral model=%#v", mistral)
	}
}

func TestCalculateCost(t *testing.T) {
	cost := CalculateCost(Model{
		Cost: ModelCost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	}, Usage{Input: 1_000_000, Output: 2_000_000, CacheRead: 500_000, CacheWrite: 100_000})
	if cost.Input != 3 || cost.Output != 30 || cost.CacheRead != 0.15 || cost.CacheWrite != 0.375 || cost.Total != 33.525 {
		t.Fatalf("cost=%#v", cost)
	}
}

func TestGetSupportedThinkingLevelsXHighCompat(t *testing.T) {
	models := AllKnownModels()
	requireLevels := func(provider, modelID string) []ThinkingLevel {
		t.Helper()
		model, ok := Find(models, provider, modelID)
		if !ok {
			t.Fatalf("missing model %s/%s", provider, modelID)
		}
		return GetSupportedThinkingLevels(model)
	}
	requireContains := func(provider, modelID string, level ThinkingLevel) {
		t.Helper()
		levels := requireLevels(provider, modelID)
		if !slices.Contains(levels, level) {
			t.Fatalf("%s/%s levels=%v, want %s", provider, modelID, levels, level)
		}
	}
	requireNotContains := func(provider, modelID string, level ThinkingLevel) {
		t.Helper()
		levels := requireLevels(provider, modelID)
		if slices.Contains(levels, level) {
			t.Fatalf("%s/%s levels=%v, did not want %s", provider, modelID, levels, level)
		}
	}
	requireExact := func(provider, modelID string, want []ThinkingLevel) {
		t.Helper()
		levels := requireLevels(provider, modelID)
		if !reflect.DeepEqual(levels, want) {
			t.Fatalf("%s/%s levels=%v, want %v", provider, modelID, levels, want)
		}
	}

	requireContains("anthropic", "claude-opus-4-6", ThinkingXHigh)
	requireContains("anthropic", "claude-opus-4-7", ThinkingXHigh)
	requireNotContains("anthropic", "claude-sonnet-4-5", ThinkingXHigh)
	requireContains("openai-codex", "gpt-5.4", ThinkingXHigh)
	requireContains("openai-codex", "gpt-5.5", ThinkingXHigh)
	requireExact("deepseek", "deepseek-v4-flash", []ThinkingLevel{ThinkingOff, ThinkingHigh, ThinkingXHigh})
	requireExact("opencode-go", "deepseek-v4-flash", []ThinkingLevel{ThinkingOff, ThinkingHigh, ThinkingXHigh})
	requireExact("openrouter", "deepseek/deepseek-v4-flash", []ThinkingLevel{ThinkingOff, ThinkingHigh, ThinkingXHigh})
	requireContains("openrouter", "anthropic/claude-opus-4.6", ThinkingXHigh)
}

func TestLoadModelsProviderConfigAndOverrides(t *testing.T) {
	agentDir := t.TempDir()
	raw := `{
		"providers": {
			"localai": {
				"api": "openai-completions",
				"baseUrl": "http://localhost:11434/v1/chat/completions",
				"apiKey": "LOCALAI_API_KEY",
				"headers": {"X-Provider": "local"},
				"models": [
					{"id": "coder", "name": "Local Coder", "input": ["text"], "maxTokens": 2048}
				]
			},
			"openai": {
				"modelOverrides": {
					"gpt-4.1": {
						"name": "GPT Override",
						"maxTokens": 1234,
						"headers": {"X-Model": "override"}
					}
				}
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	models := LoadModels(agentDir)
	local, ok := Find(models, "localai", "coder")
	if !ok {
		t.Fatal("missing custom provider model")
	}
	if local.API != "openai-completions" || local.BaseURL != "http://localhost:11434/v1/chat/completions" || local.EnvKey != "LOCALAI_API_KEY" || local.Headers["X-Provider"] != "local" || local.MaxOutput != 2048 {
		t.Fatalf("local model=%#v", local)
	}
	openai, ok := Find(models, "openai", "gpt-4.1")
	if !ok {
		t.Fatal("missing overridden openai model")
	}
	if openai.Name != "GPT Override" || openai.MaxOutput != 1234 || openai.Headers["X-Model"] != "override" {
		t.Fatalf("openai override=%#v", openai)
	}
}

func TestLoadCustomModelsBareArrayKeepsMaxTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	raw := `[{"provider":"localai","id":"coder","api":"openai-completions","baseUrl":"http://localhost/v1/chat/completions","maxTokens":3072}]`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	models := LoadCustomModels(path)
	model, ok := Find(models, "localai", "coder")
	if !ok {
		t.Fatal("missing bare custom model")
	}
	if model.MaxOutput != 3072 {
		t.Fatalf("MaxOutput=%d", model.MaxOutput)
	}
}
