package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

// isolateModelEnv points the agent dir at an empty temp dir and clears every
// known provider API-key env var so the model registry resolves to "no models"
// (only the faux placeholder, which is not auth-configured). It returns the
// temp agent dir.
func isolateModelEnv(t *testing.T) string {
	t.Helper()
	agentDir := t.TempDir()
	t.Setenv("PI_AGENT_DIR", agentDir)
	t.Setenv("PI_SESSION_DIR", filepath.Join(agentDir, "sessions"))
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PI_SKIP_VERSION_CHECK", "1")
	seen := map[string]struct{}{}
	for _, model := range ai.BuiltinModels() {
		for _, key := range ai.ProviderEnvKeys(model.Provider) {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			t.Setenv(key, "")
		}
	}
	return agentDir
}

// runMainArgs runs MainWithOptions with stdin/stdout/stderr redirected to temp
// files (so print mode does not block on a TTY) and returns the resulting error.
func runMainArgs(t *testing.T, argv []string) error {
	t.Helper()
	dir := t.TempDir()
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })
	stdin, err := os.CreateTemp(dir, "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	out, err := os.CreateTemp(dir, "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdin, out, out
	defer func() { os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr }()
	return MainWithOptions(context.Background(), argv, MainOptions{})
}

// runMainArgsWithOptions is like runMainArgs but lets the caller pass MainOptions
// (e.g. extension factories).
func runMainArgsWithOptions(t *testing.T, argv []string, opts MainOptions) error {
	t.Helper()
	dir := t.TempDir()
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCwd) })
	stdin, err := os.CreateTemp(dir, "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	out, err := os.CreateTemp(dir, "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdin, out, out
	defer func() { os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr }()
	return MainWithOptions(context.Background(), argv, opts)
}

func TestMainBrokenExtensionIsFatal(t *testing.T) {
	isolateModelEnv(t)
	opts := MainOptions{ExtensionFactories: []coreext.Factory{
		func(*coreext.API) error { return errFake("boom: extension failed to load") },
	}}
	// A valid model (faux) keeps the no-model check from firing first; the broken
	// extension must still abort with a non-zero exit before any turn runs.
	err := runMainArgsWithOptions(t, []string{"-p", "hello", "--model", "faux/faux", "--no-session", "--no-tools"}, opts)
	if err == nil {
		t.Fatal("broken explicit extension must be fatal")
	}
	if !strings.Contains(err.Error(), "failed to load required resources") {
		t.Fatalf("error=%v", err)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

func TestMainPrintNoModelExitsNonZero(t *testing.T) {
	isolateModelEnv(t)
	err := runMainArgs(t, []string{"-p", "hello", "--no-session", "--no-tools"})
	if err == nil {
		t.Fatal("expected non-zero exit when no model available")
	}
	if !strings.Contains(err.Error(), "No models available") {
		t.Fatalf("error=%v", err)
	}
}

func TestMainPrintExplicitFauxSucceeds(t *testing.T) {
	isolateModelEnv(t)
	if err := runMainArgs(t, []string{"-p", "hello", "--model", "faux/faux", "--no-session", "--no-tools"}); err != nil {
		t.Fatalf("explicit faux should succeed, got %v", err)
	}
}

func TestMainApiKeyWithoutModelErrors(t *testing.T) {
	isolateModelEnv(t)
	err := runMainArgs(t, []string{"--print", "hi", "--api-key", "k", "--no-session"})
	if err == nil || !strings.Contains(err.Error(), "--api-key requires a model") {
		t.Fatalf("error=%v", err)
	}
}

// scopeRegistry builds a registry containing exactly the given models, all
// marked auth-configured via runtime keys so they appear in
// AvailableConfigured(). The builtin catalog is replaced for deterministic
// glob-expansion assertions.
func scopeRegistry(t *testing.T, models []ai.Model) *ai.ModelRegistry {
	t.Helper()
	auth := ai.NewAuthStorage(t.TempDir())
	registry := ai.NewModelRegistry(t.TempDir(), auth)
	registry.Models = append([]ai.Model(nil), models...)
	seen := map[string]struct{}{}
	for _, model := range models {
		if _, ok := seen[model.Provider]; ok {
			continue
		}
		seen[model.Provider] = struct{}{}
		auth.SetRuntime(model.Provider, "k")
	}
	return registry
}

func TestResolveScopedModelsExpandsGlob(t *testing.T) {
	registry := scopeRegistry(t, []ai.Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic"},
		{Provider: "anthropic", ID: "claude-opus-4-1", API: "anthropic"},
		{Provider: "github-copilot", ID: "gpt-5", API: "openai"},
		{Provider: "github-copilot", ID: "claude-sonnet", API: "anthropic"},
		{Provider: "openai", ID: "gpt-4.1", API: "openai"},
	})

	t.Run("provider/glob matches all in provider", func(t *testing.T) {
		resolved, warnings := resolveScopedModels(registry, []string{"anthropic/claude-*"})
		if len(warnings) != 0 {
			t.Fatalf("warnings=%#v", warnings)
		}
		got := map[string]bool{}
		for _, sm := range resolved {
			got[sm.Model.Provider+"/"+sm.Model.ID] = true
		}
		for _, want := range []string{"anthropic/claude-sonnet-4-5", "anthropic/claude-opus-4-1"} {
			if !got[want] {
				t.Fatalf("missing %s in %#v", want, got)
			}
		}
		if got["openai/gpt-4.1"] {
			t.Fatalf("unexpected openai model in %#v", got)
		}
	})

	t.Run("github-copilot/* matches all copilot models", func(t *testing.T) {
		resolved, _ := resolveScopedModels(registry, []string{"github-copilot/*"})
		if len(resolved) != 2 {
			t.Fatalf("resolved=%#v", resolved)
		}
	})

	t.Run("bare *sonnet* matches by id across providers", func(t *testing.T) {
		resolved, _ := resolveScopedModels(registry, []string{"*sonnet*"})
		got := map[string]bool{}
		for _, sm := range resolved {
			got[sm.Model.Provider+"/"+sm.Model.ID] = true
		}
		if !got["anthropic/claude-sonnet-4-5"] || !got["github-copilot/claude-sonnet"] {
			t.Fatalf("bare-id glob missed matches: %#v", got)
		}
	})

	t.Run("thinking suffix applies to every expanded model", func(t *testing.T) {
		resolved, _ := resolveScopedModels(registry, []string{"anthropic/claude-*:high"})
		if len(resolved) == 0 {
			t.Fatal("expected matches")
		}
		for _, sm := range resolved {
			if sm.ThinkingLevel != ai.ThinkingHigh {
				t.Fatalf("model %s/%s thinking=%q", sm.Model.Provider, sm.Model.ID, sm.ThinkingLevel)
			}
		}
	})

	t.Run("no-match pattern warns", func(t *testing.T) {
		resolved, warnings := resolveScopedModels(registry, []string{"nope-*"})
		if len(resolved) != 0 {
			t.Fatalf("resolved=%#v", resolved)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "nope-*") {
			t.Fatalf("warnings=%#v", warnings)
		}
	})
}

// TestEnabledModelsConstrainScopeWhenNoModelsFlag locks the main.ts:625 fallback
// (`parsed.models ?? settingsManager.getEnabledModels()`): with no --models flag
// the enabledModels setting must constrain the scoped/cycle set rather than
// falling through to the full configured registry.
func TestEnabledModelsConstrainScopeWhenNoModelsFlag(t *testing.T) {
	registry := scopeRegistry(t, []ai.Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic"},
		{Provider: "anthropic", ID: "claude-opus-4-1", API: "anthropic"},
		{Provider: "openai", ID: "gpt-4.1", API: "openai"},
	})
	settings := NewSettingsManager(t.TempDir(), t.TempDir())
	settings.Global.EnabledModels = []string{"anthropic/*"}

	// Mirror the main.go composition: no --models flag falls back to enabledModels.
	var argsModels []string
	modelPatterns := argsModels
	if len(modelPatterns) == 0 {
		modelPatterns = settings.EnabledModels()
	}
	resolved, warnings := resolveScopedModels(registry, modelPatterns)
	if len(warnings) != 0 {
		t.Fatalf("warnings=%#v", warnings)
	}
	got := map[string]bool{}
	for _, sm := range resolved {
		got[sm.Model.Provider+"/"+sm.Model.ID] = true
	}
	if !got["anthropic/claude-sonnet-4-5"] || !got["anthropic/claude-opus-4-1"] {
		t.Fatalf("enabledModels did not constrain scope to anthropic: %#v", got)
	}
	if got["openai/gpt-4.1"] {
		t.Fatalf("openai model leaked into scope despite enabledModels=anthropic/*: %#v", got)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected exactly the 2 enabled anthropic models, got %#v", got)
	}
}

func TestApiKeyBindsToResolvedProviderOnly(t *testing.T) {
	// Mirrors the inline --api-key binding in MainWithOptions: the key is bound to
	// the resolved model's provider only.
	auth := ai.NewAuthStorage(t.TempDir())
	registry := ai.NewModelRegistry(t.TempDir(), auth)
	registry.Models = append(registry.Models, ai.Model{Provider: "openai", ID: "gpt-x", API: "openai"})

	model, ok, _ := registry.Match("", "openai/gpt-x")
	if !ok || model.Provider != "openai" {
		t.Fatalf("match ok=%v model=%#v", ok, model)
	}
	auth.SetRuntime(model.Provider, "k")
	if auth.RuntimeKey["openai"] != "k" {
		t.Fatalf("openai runtime key=%q", auth.RuntimeKey["openai"])
	}
	if auth.RuntimeKey["anthropic"] != "" {
		t.Fatalf("anthropic runtime key should be empty, got %q", auth.RuntimeKey["anthropic"])
	}
}
