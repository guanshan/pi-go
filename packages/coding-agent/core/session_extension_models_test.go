package core

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestCreateAgentSessionServicesAppliesExtensionProviderModels(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	raw := json.RawMessage(`{
		"api": "openai-completions",
		"baseUrl": "https://llm.test/v1",
		"apiKey": "literal-key",
		"headers": { "X-Provider": "yes" },
		"models": [{
			"id": "coder",
			"name": "Coder",
			"input": ["text", "image"],
			"reasoning": true,
			"contextWindow": 123,
			"maxTokens": 456,
			"cost": { "input": 1, "output": 2 },
			"headers": { "X-Model": "ok" },
			"compat": { "maxTokensField": "max_completion_tokens" }
		}]
	}`)
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:    true,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
			ExtensionFactories: []coreext.Factory{
				func(api *coreext.API) error {
					api.RegisterProvider(coreext.ProviderDefinition{
						API:          "openai-completions",
						ProviderName: "script-catalog",
						Source:       "factory",
						ModelConfig:  raw,
					})
					return nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model, ok := services.ModelRegistry.Find("script-catalog", "coder")
	if !ok {
		t.Fatalf("model not registered; models=%#v", services.ModelRegistry.Models)
	}
	if model.API != "openai-completions" || model.BaseURL != "https://llm.test/v1" || model.MaxOutput != 456 || !model.Reasoning {
		t.Fatalf("model=%#v", model)
	}
	if model.Headers["X-Provider"] != "yes" || model.Headers["X-Model"] != "ok" || model.Compat.MaxTokensField != "max_completion_tokens" {
		t.Fatalf("model headers/compat=%#v", model)
	}
	key, err := services.ModelRegistry.APIKey(context.Background(), model)
	if err != nil {
		t.Fatal(err)
	}
	if key != "literal-key" || !services.ModelRegistry.HasAuth(model) {
		t.Fatalf("key=%q hasAuth=%v", key, services.ModelRegistry.HasAuth(model))
	}
}

func TestExtensionProviderModelRegistryConcurrentReadAndWrite(t *testing.T) {
	registry := &ai.ModelRegistry{Models: []ai.Model{{Provider: "faux", ID: "faux", API: "faux"}}}
	raw, _ := json.Marshal(ai.ProviderModelConfig{
		Models: []ai.ProviderModelDefinition{{ID: "x", API: "faux"}},
	})
	def := coreext.ProviderDefinition{ProviderName: "faux", Source: "probe", ModelConfig: raw}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_, _ = applyProviderModelConfigToRegistry(registry, def, i%2 == 0)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = registry.AvailableConfigured()
		}
	}()
	wg.Wait()
}

func TestAgentSessionAppliesDynamicExtensionProviderModels(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	faux, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(InMemorySession(cwd), settings, registry, resources, faux, ai.ThinkingOff, ToolSet{}, "system")
	runtime := coreext.NewRunnerWithAPI(coreext.NewAPI())
	agent.extensionRuntime = runtime
	agent.installExtensionContextBridge()
	defer agent.Dispose()

	raw := json.RawMessage(`{
		"api": "openai-completions",
		"baseUrl": "https://live.test/v1",
		"apiKey": "live-key",
		"models": [{ "id": "live", "name": "Live", "input": ["text"] }]
	}`)
	runtime.API.RegisterProvider(coreext.ProviderDefinition{
		API:          "openai-completions",
		ProviderName: "live-catalog",
		Source:       "dynamic",
		ModelConfig:  raw,
	})
	if model, ok := registry.Find("live-catalog", "live"); !ok || model.BaseURL != "https://live.test/v1" {
		t.Fatalf("dynamic model=%#v ok=%v", model, ok)
	}
	runtime.API.UnregisterProviderSource("live-catalog", "dynamic")
	if model, ok := registry.Find("live-catalog", "live"); ok {
		t.Fatalf("dynamic model should be removed: %#v", model)
	}
}

func TestExtensionProviderModelUnregisterRestoresShadowedBuiltin(t *testing.T) {
	registry := &ai.ModelRegistry{Models: []ai.Model{
		{Provider: "openai", ID: "gpt-test", Name: "Builtin", API: "openai-completions", BaseURL: "https://builtin.test/v1"},
		{Provider: "anthropic", ID: "claude-test", Name: "Claude", API: "anthropic-messages"},
	}}
	def := coreext.ProviderDefinition{
		ProviderName: "openai",
		API:          "openai-completions",
		Source:       "extension-a",
		ModelConfig: json.RawMessage(`{
			"api": "openai-completions",
			"baseUrl": "https://extension.test/v1",
			"models": [{ "id": "gpt-test", "name": "Extension GPT", "input": ["text"] }]
		}`),
	}

	changed, err := applyProviderModelConfigToRegistry(registry, def, true)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("registering override should change registry")
	}
	model, ok := registry.Find("openai", "gpt-test")
	if !ok || model.BaseURL != "https://extension.test/v1" || model.Source != "extension-a" || model.Shadowed == nil {
		t.Fatalf("extension override not installed with shadow: %#v ok=%v", model, ok)
	}

	changed, err = applyProviderModelConfigToRegistry(registry, def, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("unregistering override should change registry")
	}
	model, ok = registry.Find("openai", "gpt-test")
	if !ok || model.BaseURL != "https://builtin.test/v1" || model.Source != "" || model.Shadowed != nil {
		t.Fatalf("builtin model was not restored: %#v ok=%v", model, ok)
	}
	if _, ok := registry.Find("anthropic", "claude-test"); !ok {
		t.Fatalf("unrelated provider model was removed: %#v", registry.Models)
	}
}
