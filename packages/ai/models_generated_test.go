package ai

import (
	"testing"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

func TestGeneratedTextModelCatalog(t *testing.T) {
	if len(GeneratedModels()) != 924 {
		t.Fatalf("generated model count=%d", len(GeneratedModels()))
	}
	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	seen := map[string]bool{}
	for _, model := range registry.Models {
		key := model.Provider + "\x00" + model.ID
		if seen[key] {
			t.Fatalf("duplicate model in registry: %s/%s", model.Provider, model.ID)
		}
		seen[key] = true
	}
	bedrock, ok := registry.Find("amazon-bedrock", "amazon.nova-2-lite-v1:0")
	if !ok {
		t.Fatal("missing generated Bedrock model")
	}
	if bedrock.API != "bedrock-converse-stream" || bedrock.ContextWindow != 128000 || bedrock.Cost.Output != 2.75 {
		t.Fatalf("bedrock model=%#v", bedrock)
	}
	if len(bedrock.ThinkingLevels) != 1 || bedrock.ThinkingLevels[0] != ThinkingOff {
		t.Fatalf("bedrock thinking levels=%#v", bedrock.ThinkingLevels)
	}
	mistral, ok := registry.Find("mistral", "magistral-small")
	if !ok {
		t.Fatal("missing generated Mistral model")
	}
	if mistral.API != "mistral-conversations" || !mistral.Reasoning || !SupportsInput(mistral, "text") {
		t.Fatalf("mistral model=%#v", mistral)
	}
	if mistral.ThinkingLevels[len(mistral.ThinkingLevels)-1] != ThinkingHigh {
		t.Fatalf("mistral thinking levels=%#v", mistral.ThinkingLevels)
	}
	openrouter, ok := registry.Find("openrouter", "mistralai/codestral-2508")
	if !ok {
		t.Fatal("missing generated OpenRouter model")
	}
	if openrouter.API != "openai-completions" || openrouter.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("openrouter model=%#v", openrouter)
	}
}

func TestGeneratedModelBaseURLHelpers(t *testing.T) {
	if got := aiproviders.OpenAIChatURL("https://openrouter.ai/api/v1"); got != "https://openrouter.ai/api/v1/chat/completions" {
		t.Fatalf("openrouter chat URL=%q", got)
	}
	minimax := "https://api.minimax.io/v1/text/chatcompletion_v2"
	if got := aiproviders.OpenAIChatURL(minimax); got != minimax {
		t.Fatalf("minimax chat URL=%q", got)
	}
	if got := aiproviders.AnthropicMessagesURL("https://api.anthropic.com"); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("anthropic messages URL=%q", got)
	}
}
