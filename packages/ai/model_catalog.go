package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

// datedModelIDPattern matches a trailing -YYYYMMDD date suffix on a model id,
// mirroring the /-\d{8}$/ test in TS model-resolver.ts isAlias.
var datedModelIDPattern = regexp.MustCompile(`-\d{8}$`)

func GeneratedModels() []Model {
	return append([]Model(nil), generatedModels...)
}

func BuiltinModels() []Model {
	models := GeneratedModels()
	return append([]Model{{Provider: "faux", ID: "faux", Name: "Faux deterministic test model", API: "faux", Input: []string{"text"}, ThinkingLevels: []ThinkingLevel{ThinkingOff}}}, models...)
}

func AllKnownModels() []Model {
	return BuiltinModels()
}

func LoadModels(agentDir string) []Model {
	models := AllKnownModels()
	return MergeModels(models, LoadCustomModels(filepath.Join(agentDir, "models.json")))
}

func LoadModelsWithAuth(agentDir string, auth *AuthStorage) []Model {
	return ApplyOAuthModelModifiers(LoadModels(agentDir), auth)
}

func ApplyOAuthModelModifiers(models []Model, auth *AuthStorage) []Model {
	out := append([]Model(nil), models...)
	if auth == nil || len(auth.Records) == 0 {
		return out
	}
	providerIDs := make([]string, 0, len(auth.Records))
	for providerID := range auth.Records {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		raw := auth.Records[providerID]
		var meta struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil || meta.Type != "oauth" {
			continue
		}
		var credentials OAuthCredentials
		if err := json.Unmarshal(raw, &credentials); err != nil {
			continue
		}
		provider, ok := GetOAuthProvider(OAuthProviderID(providerID))
		if !ok {
			continue
		}
		out = provider.ModifyModels(out, credentials)
	}
	return out
}

func LoadCustomModels(path string) []Model {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var list []Model
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var config customModelsConfig
	if err := json.Unmarshal(raw, &config); err == nil && len(config.Providers) > 0 {
		return parseProviderConfigModels(config, AllKnownModels())
	}
	var wrapped struct {
		Models []Model `json:"models"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return wrapped.Models
	}
	return nil
}

func MergeModels(base, custom []Model) []Model {
	out := append([]Model(nil), base...)
	index := map[string]int{}
	for i, model := range out {
		index[strings.ToLower(model.Provider)+"\x00"+model.ID] = i
	}
	for _, model := range custom {
		if model.Provider == "" || model.ID == "" {
			continue
		}
		key := strings.ToLower(model.Provider) + "\x00" + model.ID
		if i, ok := index[key]; ok {
			out[i] = model
			continue
		}
		index[key] = len(out)
		out = append(out, model)
	}
	return out
}

type customModelsConfig struct {
	Providers map[string]providerConfig `json:"providers"`
}

// ProviderModelConfig is the in-memory form of a models.json provider config.
// It is exported so extension hosts can reuse the same catalog parser when a
// script calls pi.registerProvider(name, config).
type ProviderModelConfig = providerConfig

type ProviderModelDefinition = customModelDefinition

type ProviderModelOverride = modelOverride

type PartialModelCost = partialModelCost

type providerConfig struct {
	Name           string                   `json:"name"`
	BaseURL        string                   `json:"baseUrl"`
	APIKey         string                   `json:"apiKey"`
	API            string                   `json:"api"`
	Headers        map[string]string        `json:"headers"`
	Compat         OpenAICompat             `json:"compat"`
	Models         []customModelDefinition  `json:"models"`
	ModelOverrides map[string]modelOverride `json:"modelOverrides"`
}

type customModelDefinition struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	API              string             `json:"api"`
	BaseURL          string             `json:"baseUrl"`
	Reasoning        *bool              `json:"reasoning"`
	ThinkingLevels   []ThinkingLevel    `json:"thinkingLevels"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap"`
	Input            []string           `json:"input"`
	Cost             ModelCost          `json:"cost"`
	ContextWindow    int                `json:"contextWindow"`
	MaxTokens        int                `json:"maxTokens"`
	MaxOutput        int                `json:"maxOutput"`
	Headers          map[string]string  `json:"headers"`
	Compat           OpenAICompat       `json:"compat"`
}

type modelOverride struct {
	Name             string             `json:"name"`
	Reasoning        *bool              `json:"reasoning"`
	ThinkingLevels   []ThinkingLevel    `json:"thinkingLevels"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap"`
	Input            []string           `json:"input"`
	Cost             partialModelCost   `json:"cost"`
	ContextWindow    int                `json:"contextWindow"`
	MaxTokens        int                `json:"maxTokens"`
	MaxOutput        int                `json:"maxOutput"`
	Headers          map[string]string  `json:"headers"`
	Compat           OpenAICompat       `json:"compat"`
}

type partialModelCost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cacheRead"`
	CacheWrite *float64 `json:"cacheWrite"`
}

func ModelsFromProviderConfig(provider string, config ProviderModelConfig, builtins []Model) []Model {
	if strings.TrimSpace(provider) == "" {
		return nil
	}
	if len(builtins) == 0 {
		builtins = AllKnownModels()
	}
	return parseProviderConfigModels(customModelsConfig{Providers: map[string]providerConfig{provider: config}}, builtins)
}

func parseProviderConfigModels(config customModelsConfig, builtins []Model) []Model {
	var out []Model
	defaults := providerDefaults(builtins)
	for provider, providerConfig := range config.Providers {
		def := defaults[strings.ToLower(provider)]
		for _, modelDef := range providerConfig.Models {
			if modelDef.ID == "" {
				continue
			}
			model := Model{
				Provider:         provider,
				ID:               modelDef.ID,
				Name:             firstNonEmpty(modelDef.Name, modelDef.ID),
				API:              firstNonEmpty(modelDef.API, providerConfig.API, def.API),
				BaseURL:          firstNonEmpty(modelDef.BaseURL, providerConfig.BaseURL, def.BaseURL),
				EnvKey:           aiproviders.EnvKeyFromAPIKey(providerConfig.APIKey),
				APIKey:           aiproviders.LiteralAPIKey(providerConfig.APIKey),
				Input:            firstStringSlice(modelDef.Input, def.Input, []string{"text"}),
				ThinkingLevelMap: modelDef.ThinkingLevelMap,
				ThinkingLevels:   firstThinkingLevels(modelDef.ThinkingLevels, def.ThinkingLevels),
				ContextWindow:    firstPositive(modelDef.ContextWindow, def.ContextWindow),
				MaxOutput:        firstPositive(modelDef.MaxTokens, modelDef.MaxOutput, def.MaxOutput),
				Cost:             firstCost(modelDef.Cost, def.Cost),
				Headers:          mergeStringMaps(providerConfig.Headers, modelDef.Headers),
				Compat:           mergeCompat(def.Compat, providerConfig.Compat, modelDef.Compat),
			}
			if modelDef.Reasoning != nil {
				model.Reasoning = *modelDef.Reasoning
			} else {
				model.Reasoning = def.Reasoning
			}
			out = append(out, model)
		}
		for modelID, override := range providerConfig.ModelOverrides {
			base, ok := findBuiltin(defaults, builtins, provider, modelID)
			if !ok {
				continue
			}
			base = applyProviderOverride(base, providerConfig)
			out = append(out, applyModelOverride(base, override))
		}
		if len(providerConfig.Models) == 0 && (providerConfig.BaseURL != "" || len(providerConfig.Headers) > 0 || !isZeroCompat(providerConfig.Compat)) {
			for _, model := range builtins {
				if strings.EqualFold(model.Provider, provider) {
					out = append(out, applyProviderOverride(model, providerConfig))
				}
			}
		}
	}
	return out
}

func providerDefaults(models []Model) map[string]Model {
	out := map[string]Model{}
	for _, model := range models {
		key := strings.ToLower(model.Provider)
		if _, ok := out[key]; !ok {
			out[key] = model
		}
	}
	return out
}

func findBuiltin(_ map[string]Model, models []Model, provider, id string) (Model, bool) {
	for _, model := range models {
		if strings.EqualFold(model.Provider, provider) && model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

func applyProviderOverride(model Model, config providerConfig) Model {
	if config.BaseURL != "" {
		model.BaseURL = config.BaseURL
	}
	if config.API != "" {
		model.API = config.API
	}
	if config.APIKey != "" {
		model.EnvKey = aiproviders.EnvKeyFromAPIKey(config.APIKey)
		model.APIKey = aiproviders.LiteralAPIKey(config.APIKey)
	}
	model.Headers = mergeStringMaps(model.Headers, config.Headers)
	model.Compat = mergeCompat(model.Compat, config.Compat)
	return model
}

func applyModelOverride(model Model, override modelOverride) Model {
	if override.Name != "" {
		model.Name = override.Name
	}
	if override.Reasoning != nil {
		model.Reasoning = *override.Reasoning
	}
	if len(override.ThinkingLevels) > 0 {
		model.ThinkingLevels = append([]ThinkingLevel(nil), override.ThinkingLevels...)
	}
	if override.ThinkingLevelMap != nil {
		model.ThinkingLevelMap = override.ThinkingLevelMap
	}
	if len(override.Input) > 0 {
		model.Input = append([]string(nil), override.Input...)
	}
	if override.ContextWindow > 0 {
		model.ContextWindow = override.ContextWindow
	}
	if override.MaxTokens > 0 {
		model.MaxOutput = override.MaxTokens
	} else if override.MaxOutput > 0 {
		model.MaxOutput = override.MaxOutput
	}
	model.Cost = applyPartialCost(model.Cost, override.Cost)
	model.Headers = mergeStringMaps(model.Headers, override.Headers)
	model.Compat = mergeCompat(model.Compat, override.Compat)
	return model
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstStringSlice(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return append([]string(nil), value...)
		}
	}
	return nil
}

func firstThinkingLevels(values ...[]ThinkingLevel) []ThinkingLevel {
	for _, value := range values {
		if len(value) > 0 {
			return append([]ThinkingLevel(nil), value...)
		}
	}
	return nil
}

func firstCost(values ...ModelCost) ModelCost {
	for _, value := range values {
		if value != (ModelCost{}) {
			return value
		}
	}
	return ModelCost{}
}

func applyPartialCost(cost ModelCost, override partialModelCost) ModelCost {
	if override.Input != nil {
		cost.Input = *override.Input
	}
	if override.Output != nil {
		cost.Output = *override.Output
	}
	if override.CacheRead != nil {
		cost.CacheRead = *override.CacheRead
	}
	if override.CacheWrite != nil {
		cost.CacheWrite = *override.CacheWrite
	}
	return cost
}

func mergeStringMaps(values ...map[string]string) map[string]string {
	var out map[string]string
	for _, value := range values {
		for k, v := range value {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = v
		}
	}
	return out
}

func mergeCompat(values ...OpenAICompat) OpenAICompat {
	var out OpenAICompat
	for _, value := range values {
		if value.SupportsStore != nil {
			out.SupportsStore = value.SupportsStore
		}
		if value.SupportsDeveloperRole != nil {
			out.SupportsDeveloperRole = value.SupportsDeveloperRole
		}
		if value.SupportsReasoningEffort != nil {
			out.SupportsReasoningEffort = value.SupportsReasoningEffort
		}
		if value.SupportsUsageInStreaming != nil {
			out.SupportsUsageInStreaming = value.SupportsUsageInStreaming
		}
		if value.MaxTokensField != "" {
			out.MaxTokensField = value.MaxTokensField
		}
		if value.RequiresToolResultName != nil {
			out.RequiresToolResultName = value.RequiresToolResultName
		}
		if value.RequiresAssistantAfterToolResult != nil {
			out.RequiresAssistantAfterToolResult = value.RequiresAssistantAfterToolResult
		}
		if value.RequiresThinkingAsText != nil {
			out.RequiresThinkingAsText = value.RequiresThinkingAsText
		}
		if value.RequiresReasoningContentOnAssistantMessages != nil {
			out.RequiresReasoningContentOnAssistantMessages = value.RequiresReasoningContentOnAssistantMessages
		}
		if value.ThinkingFormat != "" {
			out.ThinkingFormat = value.ThinkingFormat
		}
		if value.OpenRouterRouting != nil {
			out.OpenRouterRouting = value.OpenRouterRouting
		}
		if value.VercelGatewayRouting != nil {
			out.VercelGatewayRouting = value.VercelGatewayRouting
		}
		if value.ZaiToolStream != nil {
			out.ZaiToolStream = value.ZaiToolStream
		}
		if value.SupportsStrictMode != nil {
			out.SupportsStrictMode = value.SupportsStrictMode
		}
		if value.CacheControlFormat != "" {
			out.CacheControlFormat = value.CacheControlFormat
		}
		if value.SendSessionAffinityHeaders {
			out.SendSessionAffinityHeaders = true
		}
		if value.SupportsLongCacheRetention != nil {
			out.SupportsLongCacheRetention = value.SupportsLongCacheRetention
		}
		if value.SendSessionIDHeader != nil {
			out.SendSessionIDHeader = value.SendSessionIDHeader
		}
		if value.SupportsEagerToolInputStreaming != nil {
			out.SupportsEagerToolInputStreaming = value.SupportsEagerToolInputStreaming
		}
		if value.SupportsCacheControlOnTools != nil {
			out.SupportsCacheControlOnTools = value.SupportsCacheControlOnTools
		}
		if value.ForceAdaptiveThinking != nil {
			out.ForceAdaptiveThinking = value.ForceAdaptiveThinking
		}
		if value.AllowEmptySignature != nil {
			out.AllowEmptySignature = value.AllowEmptySignature
		}
	}
	return out
}

func isZeroCompat(value OpenAICompat) bool {
	return value.SupportsStore == nil &&
		value.SupportsDeveloperRole == nil &&
		value.SupportsReasoningEffort == nil &&
		value.SupportsUsageInStreaming == nil &&
		value.MaxTokensField == "" &&
		value.RequiresToolResultName == nil &&
		value.RequiresAssistantAfterToolResult == nil &&
		value.RequiresThinkingAsText == nil &&
		value.RequiresReasoningContentOnAssistantMessages == nil &&
		value.ThinkingFormat == "" &&
		value.OpenRouterRouting == nil &&
		value.VercelGatewayRouting == nil &&
		value.ZaiToolStream == nil &&
		value.SupportsStrictMode == nil &&
		value.CacheControlFormat == "" &&
		!value.SendSessionAffinityHeaders &&
		value.SendSessionIDHeader == nil &&
		value.SupportsLongCacheRetention == nil &&
		value.SupportsEagerToolInputStreaming == nil &&
		value.SupportsCacheControlOnTools == nil &&
		value.ForceAdaptiveThinking == nil &&
		value.AllowEmptySignature == nil
}

func Find(models []Model, provider, id string) (Model, bool) {
	for _, m := range models {
		if strings.EqualFold(m.Provider, provider) && m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

func Match(models []Model, provider, pattern string) (Model, bool, string) {
	if strings.Contains(pattern, "/") && provider == "" {
		parts := strings.SplitN(pattern, "/", 2)
		provider = parts[0]
		pattern = parts[1]
	}
	if idx := strings.LastIndex(pattern, ":"); idx > 0 && IsValidThinkingLevel(pattern[idx+1:]) {
		pattern = pattern[:idx]
	}
	candidates := models
	if provider != "" {
		candidates = nil
		for _, m := range models {
			if strings.EqualFold(m.Provider, provider) {
				candidates = append(candidates, m)
			}
		}
	}

	// 1. Exact reference match (mirrors TS findExactModelReferenceMatch):
	// prefer an exact id/name match, but reject a bare id that is ambiguous
	// across multiple providers rather than silently picking the first.
	if model, decided, ok := findExactModelReferenceMatch(pattern, provider, candidates); decided {
		if ok {
			return model, true, ""
		}
		return Model{}, false, fmt.Sprintf("No model found matching %q", pattern)
	}

	// 2. Glob match (Go-specific): among glob matches, prefer the highest-sorting
	// alias, otherwise the latest dated version (mirrors tryMatchModel's tie-break).
	var globMatches []Model
	for _, m := range candidates {
		if aiutils.GlobToRegexp(strings.ToLower(pattern)).MatchString(strings.ToLower(m.ID)) {
			globMatches = append(globMatches, m)
		}
	}
	if len(globMatches) > 0 {
		return preferAliasOrLatestModel(globMatches), true, ""
	}

	// 3. Partial substring match (mirrors tryMatchModel partial phase): prefer
	// alias over dated version, choosing the highest-sorting alias or latest dated.
	patternLower := strings.ToLower(pattern)
	var partialMatches []Model
	for _, m := range candidates {
		if strings.Contains(strings.ToLower(m.ID), patternLower) || (m.Name != "" && strings.Contains(strings.ToLower(m.Name), patternLower)) {
			partialMatches = append(partialMatches, m)
		}
	}
	if len(partialMatches) > 0 {
		return preferAliasOrLatestModel(partialMatches), true, ""
	}

	return Model{}, false, fmt.Sprintf("No model found matching %q", pattern)
}

// modelIDIsAlias reports whether a model id looks like an alias (no -YYYYMMDD
// date suffix and not the -latest pointer is treated as dated), mirroring TS
// isAlias in model-resolver.ts.
func modelIDIsAlias(id string) bool {
	if strings.HasSuffix(id, "-latest") {
		return true
	}
	return !datedModelIDPattern.MatchString(id)
}

// preferAliasOrLatestModel picks the best candidate among partial/glob matches:
// prefer aliases (sorted by id descending, mirroring localeCompare), otherwise
// pick the latest dated version (sorted by id descending). Mirrors the
// alias-vs-dated tie-break in TS tryMatchModel.
func preferAliasOrLatestModel(matches []Model) Model {
	var aliases, dated []Model
	for _, m := range matches {
		if modelIDIsAlias(m.ID) {
			aliases = append(aliases, m)
		} else {
			dated = append(dated, m)
		}
	}
	pool := dated
	if len(aliases) > 0 {
		pool = aliases
	}
	best := pool[0]
	for _, m := range pool[1:] {
		if m.ID > best.ID {
			best = m
		}
	}
	return best
}

// findExactModelReferenceMatch mirrors TS findExactModelReferenceMatch. The
// returned decided flag reports whether the exact-match phase reached a verdict:
// when decided is true and ok is false, the reference was ambiguous and the
// caller must NOT fall through to fuzzy matching (TS returns undefined).
func findExactModelReferenceMatch(reference, provider string, candidates []Model) (model Model, decided bool, ok bool) {
	trimmed := strings.TrimSpace(reference)
	if trimmed == "" {
		return Model{}, false, false
	}
	normalized := strings.ToLower(trimmed)

	// Canonical provider/id match against the full (unfiltered-by-provider) form.
	var canonical []Model
	for _, m := range candidates {
		if strings.ToLower(m.Provider+"/"+m.ID) == normalized {
			canonical = append(canonical, m)
		}
	}
	if len(canonical) == 1 {
		return canonical[0], true, true
	}
	if len(canonical) > 1 {
		return Model{}, true, false
	}

	// Bare id match: reject when the same id resolves across multiple providers.
	var idMatches []Model
	for _, m := range candidates {
		if strings.ToLower(m.ID) == normalized {
			idMatches = append(idMatches, m)
		}
	}
	if len(idMatches) == 1 {
		return idMatches[0], true, true
	}
	if len(idMatches) > 1 {
		return Model{}, true, false
	}

	// Name match (Go-specific convenience retained from prior behaviour): an
	// exact, unambiguous display-name match resolves directly.
	var nameMatches []Model
	for _, m := range candidates {
		if m.Name != "" && strings.EqualFold(m.Name, trimmed) {
			nameMatches = append(nameMatches, m)
		}
	}
	if len(nameMatches) == 1 {
		return nameMatches[0], true, true
	}

	return Model{}, false, false
}

func List(models []Model, search string) []Model {
	out := append([]Model(nil), models...)
	if search != "" {
		var filtered []Model
		search = strings.ToLower(search)
		for _, m := range out {
			if strings.Contains(strings.ToLower(m.Provider+"/"+m.ID+" "+m.Name), search) {
				filtered = append(filtered, m)
			}
		}
		out = filtered
	}
	sort.Slice(out, func(i, j int) bool {
		a := out[i].Provider + "/" + out[i].ID
		b := out[j].Provider + "/" + out[j].ID
		return a < b
	})
	return out
}

func SupportsInput(model Model, input string) bool {
	for _, value := range model.Input {
		if value == input {
			return true
		}
	}
	return false
}

func ClampThinking(model Model, level ThinkingLevel) ThinkingLevel {
	available := GetSupportedThinkingLevels(model)
	for _, l := range available {
		if l == level {
			return level
		}
	}
	levels := []ThinkingLevel{ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh}
	requestedIndex := -1
	for i, candidate := range levels {
		if candidate == level {
			requestedIndex = i
			break
		}
	}
	if requestedIndex == -1 {
		return available[0]
	}
	for i := requestedIndex; i < len(levels); i++ {
		if containsThinkingLevel(available, levels[i]) {
			return levels[i]
		}
	}
	for i := requestedIndex - 1; i >= 0; i-- {
		if containsThinkingLevel(available, levels[i]) {
			return levels[i]
		}
	}
	return available[0]
}

func containsThinkingLevel(levels []ThinkingLevel, level ThinkingLevel) bool {
	for _, candidate := range levels {
		if candidate == level {
			return true
		}
	}
	return false
}

func CalculateCost(model Model, usage Usage) Cost {
	cost := Cost{
		Input:      model.Cost.Input / 1_000_000 * float64(usage.Input),
		Output:     model.Cost.Output / 1_000_000 * float64(usage.Output),
		CacheRead:  model.Cost.CacheRead / 1_000_000 * float64(usage.CacheRead),
		CacheWrite: model.Cost.CacheWrite / 1_000_000 * float64(usage.CacheWrite),
	}
	cost.Total = cost.Input + cost.Output + cost.CacheRead + cost.CacheWrite
	return cost
}
