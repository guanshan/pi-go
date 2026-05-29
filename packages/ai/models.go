package ai

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
