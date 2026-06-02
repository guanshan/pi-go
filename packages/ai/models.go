package ai

// Regenerate models_generated.go and image_models_generated.go from the
// upstream TypeScript source of truth (badlogic/pi-mono packages/ai/src). The
// generator imports the committed MODELS/IMAGE_MODELS objects and reuses
// getSupportedThinkingLevels, then gofmt-aligns the output. Point it at a TS
// checkout with the first arg or the PI_TS_AI_SRC env var (default
// /root/guanshan/pi/packages/ai/src). It never hits the network.
//
//go:generate node scripts/generate-go-models.ts
func GetProviders(agentDir string) []string {
	seen := map[string]bool{}
	var providers []string
	for _, model := range LoadModels(agentDir) {
		if !seen[model.Provider] {
			seen[model.Provider] = true
			providers = append(providers, model.Provider)
		}
	}
	return providers
}

func GetModels(agentDir, provider string) []Model {
	var out []Model
	for _, model := range LoadModels(agentDir) {
		if provider == "" || model.Provider == provider {
			out = append(out, model)
		}
	}
	return out
}

func GetModel(agentDir, provider, modelID string) (Model, bool) {
	return Find(LoadModels(agentDir), provider, modelID)
}

func GetSupportedThinkingLevels(model Model) []ThinkingLevel {
	if !model.Reasoning {
		return []ThinkingLevel{ThinkingOff}
	}
	if len(model.ThinkingLevels) > 0 {
		return append([]ThinkingLevel(nil), model.ThinkingLevels...)
	}
	levels := []ThinkingLevel{ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh}
	out := make([]ThinkingLevel, 0, len(levels))
	for _, level := range levels {
		mapped, exists := model.ThinkingLevelMap[string(level)]
		if exists && mapped == nil {
			continue
		}
		if level == ThinkingXHigh && !exists {
			continue
		}
		out = append(out, level)
	}
	if len(out) == 0 {
		return []ThinkingLevel{ThinkingOff}
	}
	return out
}
