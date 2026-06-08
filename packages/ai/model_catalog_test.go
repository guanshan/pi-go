package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestModelCatalogIncludesGeneratedModels(t *testing.T) {
	// No magic count: AllKnownModels must be a superset of the generated catalog
	// and contain every generated (provider, id). Drift in the generated count is
	// caught by TestGeneratedTextModelCatalogMatchesTS.
	models := AllKnownModels()
	if len(models) < len(GeneratedModels()) {
		t.Fatalf("AllKnownModels=%d < GeneratedModels=%d", len(models), len(GeneratedModels()))
	}
	seen := map[string]bool{}
	for _, model := range models {
		key := model.Provider + "\x00" + model.ID
		if seen[key] {
			t.Fatalf("duplicate model in catalog: %s/%s", model.Provider, model.ID)
		}
		seen[key] = true
	}
	for _, gm := range GeneratedModels() {
		if !seen[gm.Provider+"\x00"+gm.ID] {
			t.Fatalf("AllKnownModels missing generated model %s/%s", gm.Provider, gm.ID)
		}
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

// Opus 4.8's adaptive-thinking + temperature-suppression behavior must come
// from the generated catalog compat, not runtime id inference:
// inferredAnthropicForceAdaptiveThinking deliberately omits opus-4-8, so this
// guards that the regenerated catalog carries forceAdaptiveThinking=true and
// supportsTemperature=false for the Anthropic Opus 4.8 entry.
func TestOpus48CatalogDrivenAnthropicCompat(t *testing.T) {
	model, ok := Find(AllKnownModels(), "anthropic", "claude-opus-4-8")
	if !ok {
		t.Fatal("missing anthropic/claude-opus-4-8")
	}
	if model.Compat.ForceAdaptiveThinking == nil || !*model.Compat.ForceAdaptiveThinking {
		t.Fatalf("catalog ForceAdaptiveThinking=%v, want true", model.Compat.ForceAdaptiveThinking)
	}
	if model.Compat.SupportsTemperature == nil || *model.Compat.SupportsTemperature {
		t.Fatalf("catalog SupportsTemperature=%v, want false", model.Compat.SupportsTemperature)
	}
	// inferredAnthropicForceAdaptiveThinking must NOT know about opus-4-8: the
	// adaptive-thinking decision has to ride on the catalog value alone.
	if inferredAnthropicForceAdaptiveThinking(model) {
		t.Fatal("inferredAnthropicForceAdaptiveThinking should not match opus-4-8; behavior must be catalog-driven")
	}
	compat := GetAnthropicMessagesCompat(model)
	if !compat.ForceAdaptiveThinking {
		t.Fatal("GetAnthropicMessagesCompat ForceAdaptiveThinking=false, want true")
	}
	if compat.SupportsTemperature {
		t.Fatal("GetAnthropicMessagesCompat SupportsTemperature=true, want false")
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

// P2-10: Model JSON must always emit `reasoning` (a required boolean upstream),
// even when false, and must OMIT `compat` entirely when it carries no overrides
// (TS compat is optional). This mirrors the @earendil-works/pi-ai Model shape.
func TestModelJSONReasoningAndCompatShape(t *testing.T) {
	raw, err := json.Marshal(Model{Provider: "openai", ID: "gpt-4o", API: "openai-completions", Reasoning: false})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["reasoning"]; !ok || string(v) != "false" {
		t.Fatalf("reasoning must always be present and false: %s", raw)
	}
	if _, ok := got["compat"]; ok {
		t.Fatalf("compat must be omitted when zero: %s", raw)
	}

	// reasoning:true still serializes, and a non-zero compat is emitted.
	enabled := true
	withCompat := Model{Provider: "openai", ID: "gpt-5", API: "openai-completions", Reasoning: true, Compat: OpenAICompat{SupportsStore: &enabled}}
	raw2, err := json.Marshal(withCompat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw2), `"reasoning":true`) {
		t.Fatalf("reasoning:true expected: %s", raw2)
	}
	if !strings.Contains(string(raw2), `"compat"`) {
		t.Fatalf("non-zero compat expected: %s", raw2)
	}

	// Round-trip preserves both fields.
	var back Model
	if err := json.Unmarshal(raw2, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Reasoning || back.Compat.SupportsStore == nil || !*back.Compat.SupportsStore {
		t.Fatalf("round-trip lost fields: %#v", back)
	}
}

// P2-18: partial matching prefers an alias (no -YYYYMMDD suffix) over a dated
// version, and among aliases picks the highest-sorting id.
func TestMatchPrefersAliasOverDated(t *testing.T) {
	models := []Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5-20250929", API: "anthropic"},
		{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic"},
	}
	m, ok, _ := Match(models, "", "sonnet")
	if !ok || m.ID != "claude-sonnet-4-5" {
		t.Fatalf("expected alias claude-sonnet-4-5, got ok=%v id=%q", ok, m.ID)
	}

	// With only dated versions, the latest (highest-sorting) dated id wins.
	dated := []Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5-20250101", API: "anthropic"},
		{Provider: "anthropic", ID: "claude-sonnet-4-5-20250929", API: "anthropic"},
	}
	m2, ok2, _ := Match(dated, "", "sonnet")
	if !ok2 || m2.ID != "claude-sonnet-4-5-20250929" {
		t.Fatalf("expected latest dated, got ok=%v id=%q", ok2, m2.ID)
	}
}

// P2-18: a bare model id that matches the same id across multiple providers is
// ambiguous and must NOT resolve to an arbitrary first match (TS returns
// undefined / no match).
func TestMatchRejectsAmbiguousBareID(t *testing.T) {
	models := []Model{
		{Provider: "openai", ID: "gpt-5", API: "openai-completions"},
		{Provider: "github-copilot", ID: "gpt-5", API: "openai-completions"},
	}
	if _, ok, _ := Match(models, "", "gpt-5"); ok {
		t.Fatal("ambiguous bare id must not match across providers")
	}
	// A provider-qualified reference disambiguates and resolves.
	if m, ok, _ := Match(models, "", "openai/gpt-5"); !ok || m.Provider != "openai" {
		t.Fatalf("qualified reference should resolve to openai: ok=%v provider=%q", ok, m.Provider)
	}
	// Single-provider bare id still resolves.
	single := []Model{{Provider: "openai", ID: "gpt-5", API: "openai-completions"}}
	if m, ok, _ := Match(single, "", "gpt-5"); !ok || m.Provider != "openai" {
		t.Fatalf("unambiguous bare id should resolve: ok=%v provider=%q", ok, m.Provider)
	}
}

// P3-08: ClampThinking with an empty level must fall through to the model's
// first supported level (off for non-reasoning models), mirroring TS
// clampThinkingLevel which has no empty-string special case.
func TestClampThinkingEmptyFallsThrough(t *testing.T) {
	nonReasoning := Model{Provider: "openai", ID: "gpt-4o", API: "openai-completions", Reasoning: false}
	if got := ClampThinking(nonReasoning, ""); got != ThinkingOff {
		t.Fatalf("empty level on non-reasoning model: got %q, want off", got)
	}
}

// P1-07: with only an Anthropic key configured, initial-model auto-selection
// must prefer the curated default (claude-opus-4-8) over the first
// auth-configured catalog entry.
func TestDefaultModelForAvailablePrefersCuratedDefault(t *testing.T) {
	available := []Model{
		{Provider: "anthropic", ID: "claude-3-5-haiku-20241022", API: "anthropic"},
		{Provider: "anthropic", ID: "claude-opus-4-8", API: "anthropic"},
	}
	m, ok := defaultModelForAvailable(available)
	if !ok || m.ID != "claude-opus-4-8" {
		t.Fatalf("expected curated default claude-opus-4-8, got ok=%v id=%q", ok, m.ID)
	}
}

// P3-11: ModelsAreEqual must treat a zero-value Model (empty provider AND id)
// as not equal, mirroring the TS null/undefined guard.
func TestModelsAreEqualZeroValueGuard(t *testing.T) {
	if ModelsAreEqual(Model{}, Model{}) {
		t.Fatal("two zero-value models must not be equal")
	}
	real := Model{Provider: "anthropic", ID: "claude-opus-4-8"}
	if ModelsAreEqual(real, Model{}) || ModelsAreEqual(Model{}, real) {
		t.Fatal("a real model must not equal a zero-value model")
	}
	if !ModelsAreEqual(real, real) {
		t.Fatal("identical models must be equal")
	}
}
