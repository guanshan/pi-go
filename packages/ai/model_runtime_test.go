package ai

import "testing"

func TestInitialModelPrefersAuthenticatedRealModelOverFaux(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	registry := &ModelRegistry{
		Auth: NewAuthStorage(t.TempDir()),
		Models: []Model{
			{Provider: "faux", ID: "faux", API: "faux"},
			{Provider: "openai", ID: "gpt-test", API: "openai-completions"},
		},
	}

	model, ok, warning := registry.InitialModel(InitialModelOptions{})
	if !ok || warning != "" {
		t.Fatalf("ok=%v warning=%q", ok, warning)
	}
	if model.Provider != "openai" || model.ID != "gpt-test" {
		t.Fatalf("model=%#v", model)
	}
}

func TestInitialModelTreatsLiteralModelAPIKeyAsConfigured(t *testing.T) {
	registry := &ModelRegistry{
		Auth: NewAuthStorage(t.TempDir()),
		Models: []Model{
			{Provider: "faux", ID: "faux", API: "faux"},
			{Provider: "literalai", ID: "coder", API: "openai-completions", APIKey: "literal-key"},
		},
	}

	model, ok, warning := registry.InitialModel(InitialModelOptions{})
	if !ok || warning != "" {
		t.Fatalf("ok=%v warning=%q", ok, warning)
	}
	if model.Provider != "literalai" || model.ID != "coder" {
		t.Fatalf("model=%#v", model)
	}
}

func TestInitialModelDoesNotFallbackToFauxWithoutConfiguredAuth(t *testing.T) {
	t.Setenv("NOAUTH_API_KEY", "")
	registry := &ModelRegistry{
		Auth: NewAuthStorage(t.TempDir()),
		Models: []Model{
			{Provider: "faux", ID: "faux", API: "faux"},
			{Provider: "noauth", ID: "model", API: "openai-completions"},
		},
	}

	model, ok, warning := registry.InitialModel(InitialModelOptions{})
	if ok || warning != "No models available" {
		t.Fatalf("ok=%v warning=%q", ok, warning)
	}
	if model.Provider != "" || model.ID != "" {
		t.Fatalf("model=%#v", model)
	}
}
