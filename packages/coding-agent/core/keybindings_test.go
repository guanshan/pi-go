package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestKeybindingsManagerLoadsOverridesAndDiagnostics(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "keybindings.json"), []byte(`{
		"cycleModelForward": "ctrl+x",
		"app.clear": [],
		"app.model.select": ["shift+ctrl+l", 42],
		"app.exit": "not-a-key",
		"app.unknown": "ctrl+z"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := NewKeybindingsManager(agentDir)
	if !manager.MatchesBubbleKey("ctrl+x", AppModelCycleForward) {
		t.Fatal("legacy cycleModelForward override was not migrated")
	}
	if manager.MatchesBubbleKey("ctrl+p", AppModelCycleForward) {
		t.Fatal("user override should replace the default model-cycle key")
	}
	if manager.MatchesBubbleKey("ctrl+c", AppClear) {
		t.Fatal("empty array should unbind app.clear")
	}
	if !manager.MatchesBubbleKey("ctrl+shift+l", AppModelSelect) {
		t.Fatal("array override should accept valid entries")
	}
	if !manager.MatchesBubbleKey("ctrl+d", AppExit) {
		t.Fatal("invalid override should leave the default app.exit binding intact")
	}
	diagnostics := manager.Diagnostics()
	for _, needle := range []string{"unknown keybinding ignored: app.unknown", "invalid keybinding for app.model.select", "invalid keybinding for app.exit"} {
		if !diagnosticsContain(diagnostics, needle) {
			t.Fatalf("diagnostics missing %q: %#v", needle, diagnostics)
		}
	}
}

func TestKeybindingShiftLetterMatchesPressedUppercaseLetter(t *testing.T) {
	manager := NewKeybindingsManager(t.TempDir())
	// Terminals deliver Shift+L as the printable "L" (msg.String()) with no modifier
	// token; it must still match the default "shift+l" binding. Regression: it used
	// to normalize to "l" and never fire.
	if !manager.MatchesBubbleKey("L", AppKeybinding("app.tree.editLabel")) {
		t.Fatal("pressed \"L\" should match the shift+l binding")
	}
	if !manager.MatchesBubbleKey("T", AppKeybinding("app.tree.toggleLabelTimestamp")) {
		t.Fatal("pressed \"T\" should match the shift+t binding")
	}
	// A bare lowercase letter must not match a shift binding.
	if manager.MatchesBubbleKey("l", AppKeybinding("app.tree.editLabel")) {
		t.Fatal("pressed \"l\" should not match the shift+l binding")
	}
}

func TestInteractiveCustomKeybindingOpensModelSelector(t *testing.T) {
	models := selectorTestModels()
	runtime := selectorInteractiveRuntime(t, models, models[0])
	agent := runtime.Session()
	if err := os.WriteFile(filepath.Join(agent.Settings.AgentDir, "keybindings.json"), []byte(`{
		"app.model.select": "ctrl+x"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	agent.Keybindings = NewKeybindingsManager(agent.Settings.AgentDir)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if model.modelSelector != nil {
		t.Fatal("default ctrl+l should not open selector after user override")
	}
	model.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if model.modelSelector == nil {
		t.Fatal("custom ctrl+x binding did not open selector")
	}
}

func TestInteractiveExtensionShortcutExecutes(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	called := make(chan struct{}, 1)
	api.RegisterShortcut(coreext.ShortcutDefinition{
		Key:         "ctrl+alt+p",
		Description: "Probe",
		Execute: func(context.Context) error {
			called <- struct{}{}
			return nil
		},
	})
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, cmd := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModAlt})
	if cmd == nil {
		t.Fatal("extension shortcut keypress was not consumed")
	}
	msg := cmd()
	model.Update(msg)
	select {
	case <-called:
	default:
		t.Fatal("extension shortcut handler was not executed")
	}
	if !strings.Contains(model.statusMessage, "Extension shortcut complete: Probe") {
		t.Fatalf("status=%q", model.statusMessage)
	}
}

func TestInteractiveExtensionShortcutDoesNotOverrideBuiltInKey(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	called := false
	api.RegisterShortcut(coreext.ShortcutDefinition{
		Key:         "ctrl+l",
		Description: "Conflicting",
		Execute: func(context.Context) error {
			called = true
			return nil
		},
	})
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, cmd := model.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("built-in ctrl+l should not dispatch extension shortcut")
	}
	if called {
		t.Fatal("extension shortcut stole a built-in keybinding")
	}
	if model.modelSelector == nil {
		t.Fatal("built-in ctrl+l did not open the model selector")
	}
}

func TestInteractiveExtensionShortcutCanOverrideNonReservedBuiltInKey(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	called := make(chan struct{}, 1)
	api.RegisterShortcut(coreext.ShortcutDefinition{
		Key:         "ctrl+x",
		Description: "Override non-reserved",
		Source:      "ext-a",
		Execute: func(context.Context) error {
			called <- struct{}{}
			return nil
		},
	})
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, cmd := model.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("non-reserved built-in conflict should dispatch extension shortcut")
	}
	model.Update(cmd())
	select {
	case <-called:
	default:
		t.Fatal("extension shortcut did not override non-reserved built-in key")
	}
}

func TestResolveExtensionShortcutsMatchesTSConflictRules(t *testing.T) {
	api := coreext.NewAPI()
	api.RegisterShortcut(coreext.ShortcutDefinition{
		Key:         "ctrl+l",
		Description: "reserved",
		Source:      "reserved-ext",
		Execute:     func(context.Context) error { return nil },
	})
	api.RegisterShortcut(coreext.ShortcutDefinition{
		Key:         "ctrl+x",
		Description: "first",
		Source:      "first-ext",
		Execute:     func(context.Context) error { return nil },
	})
	api.RegisterShortcut(coreext.ShortcutDefinition{
		Key:         "Ctrl+X",
		Description: "second",
		Source:      "second-ext",
		Execute:     func(context.Context) error { return nil },
	})

	shortcuts, diagnostics := resolveExtensionShortcuts(coreext.NewRunnerWithAPI(api), nil)
	if len(shortcuts) != 1 {
		t.Fatalf("shortcuts=%#v, want only non-reserved duplicate winner", shortcuts)
	}
	if shortcuts[0].key != "ctrl+x" || shortcuts[0].shortcut.Description != "second" {
		t.Fatalf("winner=%#v, want second ctrl+x shortcut", shortcuts[0])
	}
	for _, needle := range []string{
		"Extension shortcut 'ctrl+l' from reserved-ext conflicts with built-in shortcut. Skipping.",
		"Extension shortcut conflict: 'ctrl+x' is built-in shortcut for app.models.clearAll and first-ext. Using first-ext.",
		"Extension shortcut conflict: 'Ctrl+X' registered by both first-ext and second-ext. Using second-ext.",
	} {
		if !diagnosticsContain(diagnostics, needle) {
			t.Fatalf("diagnostics missing %q: %#v", needle, diagnostics)
		}
	}
}

func TestSlashHelpListsNonConflictingExtensionShortcuts(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	api.RegisterShortcut(coreext.ShortcutDefinition{
		Key:         "ctrl+alt+p",
		Description: "Probe shortcut",
		Execute:     func(context.Context) error { return nil },
	})
	api.RegisterShortcut(coreext.ShortcutDefinition{
		Key:         "ctrl+l",
		Description: "Conflicting shortcut",
		Execute:     func(context.Context) error { return nil },
	})
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	help := slashHelp(runtime.Session())
	if !strings.Contains(help, "Extension shortcuts:") || !strings.Contains(help, "Ctrl+Alt+P") || !strings.Contains(help, "Probe shortcut") {
		t.Fatalf("extension shortcut help missing:\n%s", help)
	}
	if strings.Contains(help, "Conflicting shortcut") {
		t.Fatalf("conflicting shortcut should be hidden from help:\n%s", help)
	}
}

func TestInteractiveDequeueRestoresQueuedMessagesToEditor(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	agent := runtime.Session()
	agent.QueueSteer("steer one", nil)
	agent.QueueFollowUp("follow two", nil)
	model.localQueue = append(model.localQueue, interactiveQueuedInput{Text: "/debug"})
	model.input.SetValue("draft")

	model.handleDequeue()
	got := model.input.Value()
	for _, want := range []string{"steer one", "follow two", "/debug", "draft"} {
		if !strings.Contains(got, want) {
			t.Fatalf("restored editor=%q missing %q", got, want)
		}
	}
	if len(model.localQueue) != 0 || len(model.queuedSteering) != 0 || len(model.queuedFollowUp) != 0 {
		t.Fatalf("queues not cleared: local=%#v steering=%#v followUp=%#v", model.localQueue, model.queuedSteering, model.queuedFollowUp)
	}
	if !strings.Contains(model.statusMessage, "Restored 3 queued messages") {
		t.Fatalf("status=%q", model.statusMessage)
	}
}

func TestInteractiveThinkingTogglePersistsSetting(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	model.toggleThinkingBlockVisibility()
	if !runtime.Session().Settings.HideThinkingBlock() {
		t.Fatal("thinking toggle did not persist hideThinkingBlock")
	}
	if !strings.Contains(model.statusMessage, "hidden") {
		t.Fatalf("status=%q", model.statusMessage)
	}
}
