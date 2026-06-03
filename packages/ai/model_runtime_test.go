package ai

import (
	"strings"
	"testing"
)

// TestVersionSourceOfTruth pins UpstreamVersion to the upstream TS
// packages/ai/package.json version (0.78.0) and verifies Version derives from it
// with the "-go" suffix. coding-agent/core derives its version from ai.Version,
// so this is the single source of truth (P1-09).
func TestVersionSourceOfTruth(t *testing.T) {
	if UpstreamVersion != "0.78.0" {
		t.Fatalf("UpstreamVersion=%q, want 0.78.0 (TS packages/ai/package.json)", UpstreamVersion)
	}
	if Version != UpstreamVersion+"-go" {
		t.Fatalf("Version=%q, want %q", Version, UpstreamVersion+"-go")
	}
	if !strings.HasSuffix(Version, "-go") {
		t.Fatalf("Version=%q must carry the -go suffix", Version)
	}
}

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
