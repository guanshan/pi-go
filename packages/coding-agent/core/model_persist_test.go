package core

import (
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// buildPersistAgent constructs an AgentSession backed by an on-disk settings
// directory plus a small registry of thinking-capable models with configured
// auth, so model/thinking persistence can be re-read from disk.
func buildPersistAgent(t *testing.T, models []ai.Model, model ai.Model) (*AgentSession, *SettingsManager) {
	t.Helper()
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := NewSettingsManager(cwd, agentDir)
	auth := ai.NewAuthStorage(agentDir)
	registry := ai.NewModelRegistry(agentDir, auth)
	registry.Models = append([]ai.Model(nil), models...)
	seen := map[string]struct{}{}
	for _, m := range models {
		if _, ok := seen[m.Provider]; ok {
			continue
		}
		seen[m.Provider] = struct{}{}
		auth.SetRuntime(m.Provider, "k")
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: agentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(InMemorySession(cwd), settings, registry, resources, model, ai.ThinkingMedium, ToolSet{}, "system")
	return agent, settings
}

// TestSetModelPersistsDefault locks P1-1: SetModel writes the selected
// model+provider back to settings.json so a fresh launch remembers it
// (agent-session.ts:1448 setModel -> setDefaultModelAndProvider).
func TestSetModelPersistsDefault(t *testing.T) {
	models := []ai.Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic", ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingHigh}},
		{Provider: "openai", ID: "gpt-5", API: "openai", ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingHigh}},
	}
	agent, settings := buildPersistAgent(t, models, models[0])

	if _, err := agent.SetModel("openai", "gpt-5"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}

	// Re-read from disk: the persisted default must reflect the new model.
	reloaded := NewSettingsManager(settings.CWD, settings.AgentDir)
	if reloaded.DefaultProvider() != "openai" || reloaded.DefaultModel() != "gpt-5" {
		t.Fatalf("default not persisted: provider=%q model=%q", reloaded.DefaultProvider(), reloaded.DefaultModel())
	}
}

// TestCycleModelPersistsDefault locks P1-1 for the Ctrl+P cycle path
// (agent-session.ts:1485/1513 -> setDefaultModelAndProvider).
func TestCycleModelPersistsDefault(t *testing.T) {
	models := []ai.Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic", ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingHigh}},
		{Provider: "openai", ID: "gpt-5", API: "openai", ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingHigh}},
	}
	agent, settings := buildPersistAgent(t, models, models[0])

	data, ok := agent.CycleModel()
	if !ok {
		t.Fatalf("CycleModel returned false; data=%#v", data)
	}
	next := agent.CurrentModel()

	reloaded := NewSettingsManager(settings.CWD, settings.AgentDir)
	if reloaded.DefaultProvider() != next.Provider || reloaded.DefaultModel() != next.ID {
		t.Fatalf("cycled default not persisted: got provider=%q model=%q want %q/%q",
			reloaded.DefaultProvider(), reloaded.DefaultModel(), next.Provider, next.ID)
	}
}

// TestSetThinkingLevelPersistsOnChange locks P1-1's thinking-level rules: the
// level is persisted only when it actually changes, and only when the model
// supports thinking or the level is non-off (agent-session.ts:1532-1546).
func TestSetThinkingLevelPersistsOnChange(t *testing.T) {
	// Thinking-capable model: a real change to "high" is persisted.
	thinkingModel := ai.Model{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic", Reasoning: true, ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingLow, ai.ThinkingHigh}}
	agent, settings := buildPersistAgent(t, []ai.Model{thinkingModel}, thinkingModel)

	if err := agent.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatalf("SetThinkingLevel high: %v", err)
	}
	reloaded := NewSettingsManager(settings.CWD, settings.AgentDir)
	if reloaded.Global.DefaultThinkingLevel != ai.ThinkingHigh {
		t.Fatalf("changed thinking level not persisted: %q", reloaded.Global.DefaultThinkingLevel)
	}

	// No-op set (same level) must not rewrite settings: clear the persisted
	// value and assert SetThinkingLevel to the current level leaves it cleared.
	settings.Global.DefaultThinkingLevel = ""
	if err := settings.SaveGlobal(); err != nil {
		t.Fatal(err)
	}
	if err := agent.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatalf("SetThinkingLevel no-op: %v", err)
	}
	reloaded = NewSettingsManager(settings.CWD, settings.AgentDir)
	if reloaded.Global.DefaultThinkingLevel != "" {
		t.Fatalf("no-op thinking change should not persist, got %q", reloaded.Global.DefaultThinkingLevel)
	}
}

// TestSetThinkingLevelOffOnNonThinkingModelDoesNotPersist locks the
// `supportsThinking() || level !== "off"` guard: a non-thinking model clamping
// to "off" must not persist a default thinking level.
func TestSetThinkingLevelOffOnNonThinkingModelDoesNotPersist(t *testing.T) {
	// Model that only supports "off" -> does not support thinking.
	nonThinking := ai.Model{Provider: "bedrock", ID: "nova", API: "bedrock-converse", ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff}}
	agent, settings := buildPersistAgent(t, []ai.Model{nonThinking}, nonThinking)
	// Start from a non-off level so the set is an actual change down to "off".
	agent.mu.Lock()
	agent.ThinkingLevel = ai.ThinkingHigh
	agent.mu.Unlock()

	if err := agent.SetThinkingLevel(ai.ThinkingOff); err != nil {
		t.Fatalf("SetThinkingLevel off: %v", err)
	}
	if got := agent.CurrentThinkingLevel(); got != ai.ThinkingOff {
		t.Fatalf("expected clamp to off, got %q", got)
	}
	reloaded := NewSettingsManager(settings.CWD, settings.AgentDir)
	if reloaded.Global.DefaultThinkingLevel != "" {
		t.Fatalf("off on non-thinking model must not persist, got %q", reloaded.Global.DefaultThinkingLevel)
	}
}

func TestSetModelRejectedWhileStreaming(t *testing.T) {
	models := []ai.Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic"},
		{Provider: "openai", ID: "gpt-5", API: "openai"},
	}
	agent, _ := buildPersistAgent(t, models, models[0])
	agent.mu.Lock()
	agent.streaming = true
	agent.mu.Unlock()

	if _, err := agent.SetModel("openai", "gpt-5"); err == nil {
		t.Fatal("SetModel while streaming should be rejected")
	}
	if got := agent.CurrentModel(); got.Provider != "anthropic" || got.ID != "claude-sonnet-4-5" {
		t.Fatalf("model changed while streaming: %s/%s", got.Provider, got.ID)
	}
}

func TestCycleModelRejectedWhileStreaming(t *testing.T) {
	models := []ai.Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic"},
		{Provider: "openai", ID: "gpt-5", API: "openai"},
	}
	agent, _ := buildPersistAgent(t, models, models[0])
	agent.mu.Lock()
	agent.streaming = true
	agent.mu.Unlock()

	// Rejected while streaming: ok=false with a busy marker so the TUI can
	// distinguish this from a "only one model" refusal.
	data, ok := agent.CycleModel()
	if ok {
		t.Fatalf("CycleModel while streaming should be rejected, got ok=%v", ok)
	}
	if busy, _ := data["busy"].(bool); !busy {
		t.Fatalf("expected busy marker on streaming rejection, got %#v", data)
	}
	if got := agent.CurrentModel(); got.Provider != "anthropic" || got.ID != "claude-sonnet-4-5" {
		t.Fatalf("model changed while streaming: %s/%s", got.Provider, got.ID)
	}
}

func TestSetThinkingLevelRejectedWhileStreaming(t *testing.T) {
	model := ai.Model{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic", Reasoning: true, ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingLow, ai.ThinkingHigh}}
	agent, settings := buildPersistAgent(t, []ai.Model{model}, model)
	start := agent.CurrentThinkingLevel()
	agent.mu.Lock()
	agent.streaming = true
	agent.mu.Unlock()

	if err := agent.SetThinkingLevel(ai.ThinkingHigh); err == nil {
		t.Fatal("SetThinkingLevel while streaming should be rejected")
	}
	if got := agent.CurrentThinkingLevel(); got != start {
		t.Fatalf("thinking level changed while streaming: got %q want %q", got, start)
	}
	reloaded := NewSettingsManager(settings.CWD, settings.AgentDir)
	if reloaded.Global.DefaultThinkingLevel != "" {
		t.Fatalf("streaming rejection should not persist thinking level, got %q", reloaded.Global.DefaultThinkingLevel)
	}
}

func TestCycleThinkingLevelRejectedWhileStreaming(t *testing.T) {
	model := ai.Model{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic", Reasoning: true, ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingLow, ai.ThinkingHigh}}
	agent, _ := buildPersistAgent(t, []ai.Model{model}, model)
	start := agent.CurrentThinkingLevel()
	agent.mu.Lock()
	agent.streaming = true
	agent.mu.Unlock()

	level, ok := agent.CycleThinkingLevel()
	if ok {
		t.Fatalf("CycleThinkingLevel while streaming should be rejected, got ok=%v level=%q", ok, level)
	}
	if level != start {
		t.Fatalf("rejected cycle returned level=%q want %q", level, start)
	}
	if got := agent.CurrentThinkingLevel(); got != start {
		t.Fatalf("thinking level changed while streaming: got %q want %q", got, start)
	}
}
