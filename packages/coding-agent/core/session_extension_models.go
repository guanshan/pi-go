package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func (a *AgentSession) applyExtensionProviderDefinition(def coreext.ProviderDefinition, registered bool) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	registry := a.Registry
	a.mu.Unlock()
	changed, err := applyProviderModelConfigToRegistry(registry, def, registered)
	var modelChanged bool
	var model ai.Model
	var thinking ai.ThinkingLevel
	if err == nil && changed {
		a.mu.Lock()
		modelChanged, model, thinking = a.refreshCurrentModelFromRegistryLocked()
		a.mu.Unlock()
	}
	if modelChanged {
		a.emitSessionEvent(ModelChangedEvent{Model: model, ThinkingLevel: thinking})
	}
	return err
}

func applyProviderModelConfigToRegistry(registry *ai.ModelRegistry, def coreext.ProviderDefinition, registered bool) (bool, error) {
	if registry == nil || !hasProviderModelConfig(def.ModelConfig) {
		return false, nil
	}
	providerName := strings.TrimSpace(def.ProviderName)
	if providerName == "" {
		providerName = strings.TrimSpace(def.API)
	}
	if providerName == "" {
		return false, nil
	}
	sourceID := extensionProviderModelSource(def, providerName)
	var config ai.ProviderModelConfig
	if err := json.Unmarshal(def.ModelConfig, &config); err != nil {
		return false, fmt.Errorf("invalid extension provider model config for %s: %w", providerName, err)
	}
	changed := false
	registry.MutateModels(func(current []ai.Model) []ai.Model {
		next, removed := removeRegistryProviderModels(current, providerName, sourceID)
		changed = removed
		if !registered {
			return next
		}
		builtins := append([]ai.Model(nil), next...)
		if len(config.Models) > 0 {
			builtins = ai.AllKnownModels()
		}
		models := ai.ModelsFromProviderConfig(providerName, config, builtins)
		for _, model := range models {
			model.Source = sourceID
			model.Shadowed = nil
			var updated bool
			next, updated = upsertRegistryProviderModel(next, model)
			if updated {
				changed = true
			}
		}
		return next
	})
	return changed, nil
}

func extensionProviderModelSource(def coreext.ProviderDefinition, providerName string) string {
	source := strings.TrimSpace(def.Source)
	if source != "" {
		return source
	}
	if providerName != "" {
		return "extension:" + providerName
	}
	return "extension"
}

func removeRegistryProviderModels(models []ai.Model, providerName, sourceID string) ([]ai.Model, bool) {
	providerName = strings.TrimSpace(providerName)
	sourceID = strings.TrimSpace(sourceID)
	if providerName == "" || sourceID == "" {
		return models, false
	}
	out := models[:0]
	changed := false
	for _, model := range models {
		if strings.EqualFold(model.Provider, providerName) && strings.TrimSpace(model.Source) == sourceID {
			changed = true
			if model.Shadowed != nil {
				out = append(out, *model.Shadowed)
			}
			continue
		}
		out = append(out, model)
	}
	return out, changed
}

func upsertRegistryProviderModel(models []ai.Model, model ai.Model) ([]ai.Model, bool) {
	if strings.TrimSpace(model.Provider) == "" || strings.TrimSpace(model.ID) == "" {
		return models, false
	}
	for i, existing := range models {
		if !strings.EqualFold(existing.Provider, model.Provider) || existing.ID != model.ID {
			continue
		}
		if strings.TrimSpace(existing.Source) != strings.TrimSpace(model.Source) {
			shadow := existing
			model.Shadowed = &shadow
		} else if existing.Shadowed != nil && model.Shadowed == nil {
			shadow := *existing.Shadowed
			model.Shadowed = &shadow
		}
		if reflect.DeepEqual(existing, model) {
			return models, false
		}
		models[i] = model
		return models, true
	}
	models = append(models, model)
	return models, true
}

func (a *AgentSession) refreshCurrentModelFromRegistryLocked() (bool, ai.Model, ai.ThinkingLevel) {
	if a == nil || a.Registry == nil {
		return false, ai.Model{}, ""
	}
	if strings.TrimSpace(a.Model.Provider) == "" || strings.TrimSpace(a.Model.ID) == "" {
		return false, ai.Model{}, ""
	}
	model, ok := a.Registry.Find(a.Model.Provider, a.Model.ID)
	if !ok || reflect.DeepEqual(a.Model, model) {
		return false, ai.Model{}, ""
	}
	a.Model = model
	a.syncReadToolModelSupportLocked(model)
	return true, model, a.ThinkingLevel
}

func hasProviderModelConfig(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
}
