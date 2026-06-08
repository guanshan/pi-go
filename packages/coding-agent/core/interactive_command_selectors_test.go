package core

import (
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/tui"
)

// newToggleTestAgent builds a minimal in-memory AgentSession for exercising the
// settings editor / tree selector against a real session.
func newToggleTestAgent(t *testing.T) *AgentSession {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model := ai.Model{Provider: "unit", ID: "toggle-model", API: "toggle-api", MaxOutput: 2048}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	return NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
}

func TestListResolvedThemesIncludesBuiltins(t *testing.T) {
	themes := listResolvedThemes(ResourceLoader{})
	names := map[string]bool{}
	for _, theme := range themes {
		names[theme.Name] = true
	}
	if !names["dark"] || !names["light"] {
		t.Fatalf("expected dark+light builtins, got %v", names)
	}
}

func TestResolveThemeByNameFallsBack(t *testing.T) {
	if got, found := resolveThemeByName("dark", ResourceLoader{}); got.Name != "dark" || !found {
		t.Fatalf("resolveThemeByName(dark)=%q found=%v", got.Name, found)
	}
	if got, found := resolveThemeByName("does-not-exist", ResourceLoader{}); found || got.Name == "" {
		t.Fatalf("unknown theme should report found=false with a non-empty fallback, got found=%v name=%q", found, got.Name)
	}
}

func TestCommandSelectorOverlayNavigateAndSelect(t *testing.T) {
	items := []tui.SelectItem{
		{Value: "a", Label: "Alpha"},
		{Value: "b", Label: "Bravo"},
		{Value: "c", Label: "Charlie"},
	}
	o := newCommandSelectorOverlay(commandSelectorResume, "Resume", "", items, "", defaultInteractiveThemeStyles())
	if o == nil {
		t.Fatal("overlay should not be nil for non-empty items")
	}
	if act := o.HandleKey("down"); act != modelSelectorNone {
		t.Fatalf("down action=%v", act)
	}
	o.HandleKey("down")
	if act := o.HandleKey("enter"); act != modelSelectorSelect {
		t.Fatalf("enter action=%v", act)
	}
	value, ok := o.SelectedValue()
	if !ok || value != "c" {
		t.Fatalf("selected=%q ok=%v, want c", value, ok)
	}
	if act := o.HandleKey("esc"); act != modelSelectorCancel {
		t.Fatalf("esc action=%v", act)
	}
}

func TestCommandSelectorOverlayFilters(t *testing.T) {
	items := []tui.SelectItem{
		{Value: "openai/gpt-5", Label: "GPT-5"},
		{Value: "anthropic/claude", Label: "Claude"},
	}
	o := newCommandSelectorOverlay(commandSelectorResume, "x", "", items, "", defaultInteractiveThemeStyles())
	o.HandleKey("c")
	o.HandleKey("l")
	value, ok := o.SelectedValue()
	if !ok || value != "anthropic/claude" {
		t.Fatalf("filtered select=%q ok=%v, want anthropic/claude", value, ok)
	}
}

func TestSettingsEditorTogglesPersist(t *testing.T) {
	settings := NewSettingsManager(t.TempDir(), t.TempDir())
	o := newSettingsEditorOverlay(settings, nil, "dark", defaultInteractiveThemeStyles())
	// rows: theme(0), hideThinking(1), showImages(2), autoCompaction(3), autoRetry(4)
	before := settings.ShowImages()
	o.HandleKey("down") // -> hideThinking
	o.HandleKey("down") // -> showImages
	if act := o.HandleKey("enter"); act != settingsEditorToggled {
		t.Fatalf("toggle action=%v", act)
	}
	if settings.ShowImages() == before {
		t.Fatalf("ShowImages did not toggle (still %v)", before)
	}
	// theme row opens the theme selector.
	o.selected = 0
	if act := o.HandleKey("enter"); act != settingsEditorOpenTheme {
		t.Fatalf("theme row action=%v, want open-theme", act)
	}
}

// TestSettingsEditorTogglesAffectLiveSession asserts that flipping
// auto-compaction / auto-retry reaches the live AgentSession (agent.State()),
// not just settings.json — the parity gap this round fixes.
func TestSettingsEditorTogglesAffectLiveSession(t *testing.T) {
	agent := newToggleTestAgent(t)
	settings := agent.Settings
	o := newSettingsEditorOverlay(settings, agent, "dark", defaultInteractiveThemeStyles())

	if !agent.State().AutoCompactionEnabled {
		t.Fatal("expected auto-compaction enabled by default")
	}
	o.selected = 3 // settingsRowAutoCompaction
	if act := o.HandleKey("enter"); act != settingsEditorToggled {
		t.Fatalf("auto-compaction toggle action=%v", act)
	}
	if agent.State().AutoCompactionEnabled {
		t.Fatal("auto-compaction toggle did not reach the live session")
	}
	if settings.AutoCompactionEnabled() {
		t.Fatal("auto-compaction toggle was not persisted to settings")
	}

	beforeRetry := agent.State().AutoRetryEnabled
	o.selected = 4 // settingsRowAutoRetry
	if act := o.HandleKey("enter"); act != settingsEditorToggled {
		t.Fatalf("auto-retry toggle action=%v", act)
	}
	if agent.State().AutoRetryEnabled == beforeRetry {
		t.Fatal("auto-retry toggle did not reach the live session")
	}
	if settings.AutoRetryEnabled() == beforeRetry {
		t.Fatal("auto-retry toggle was not persisted to settings")
	}
}

// TestTreeSelectorItemsCoverFullTree asserts the /tree picker is built from the
// whole session tree (all branches + assistant/tool rows + current leaf), not
// just the current-branch fork points.
func TestTreeSelectorItemsCoverFullTree(t *testing.T) {
	agent := newToggleTestAgent(t)
	session := agent.Session
	model := agent.Model

	rootID := appendSessionMessage(t, session, ai.NewUserMessage("root question", nil))
	assistantID := appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("first reply"), ai.Usage{}, "stop"))
	branchAID := appendSessionMessage(t, session, ai.NewUserMessage("branch A follow-up", nil))
	if err := session.SetLeaf(rootID); err != nil {
		t.Fatal(err)
	}
	branchBID := appendSessionMessage(t, session, ai.NewUserMessage("branch B follow-up", nil))

	items := treeSelectorItems(agent)
	byID := map[string]tui.SelectItem{}
	for _, it := range items {
		byID[it.Value] = it
	}
	for _, id := range []string{rootID, assistantID, branchAID, branchBID} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("tree selector missing entry %s; items=%v", id, byID)
		}
	}
	if !strings.Contains(byID[branchBID].Description, "current") {
		t.Fatalf("current leaf not marked: %q", byID[branchBID].Description)
	}
	// forkableSelectorItems (current branch user messages only) would yield 2;
	// the full tree must be strictly larger.
	if len(items) <= len(forkableSelectorItems(agent)) {
		t.Fatalf("tree items (%d) should exceed forkable items (%d)", len(items), len(forkableSelectorItems(agent)))
	}
}

func TestBranchSummaryOptionsFor(t *testing.T) {
	if got := branchSummaryOptionsFor("No summary", "ignored"); got.Summarize || got.CustomInstructions != "" {
		t.Fatalf("No summary opts=%+v", got)
	}
	if got := branchSummaryOptionsFor("Summarize", ""); !got.Summarize || got.CustomInstructions != "" {
		t.Fatalf("Summarize opts=%+v", got)
	}
	got := branchSummaryOptionsFor("Summarize with custom prompt", "keep it short")
	if !got.Summarize || got.CustomInstructions != "keep it short" {
		t.Fatalf("custom opts=%+v", got)
	}
}
