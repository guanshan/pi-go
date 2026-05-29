package tui

import "testing"

func TestKeybindingsDefaults(t *testing.T) {
	m := NewKeybindingsManager(TUIKeybindings, nil)
	if !m.Matches("\r", "tui.input.submit") {
		t.Error("default Enter should submit")
	}
	if !m.Matches("\x1b[A", "tui.editor.cursorUp") {
		t.Error("default Up arrow")
	}
	if !m.Matches("\x03", "tui.input.copy") {
		t.Error("ctrl+c → copy")
	}
}

func TestKeybindingsUserOverride(t *testing.T) {
	m := NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{
		"tui.input.submit": {Ctrl("j")},
	})
	if m.Matches("\r", "tui.input.submit") {
		t.Error("Enter should no longer match after override")
	}
	if !m.Matches("\x0a", "tui.input.submit") {
		t.Error("ctrl+j should now submit")
	}
}

func TestKeybindingsConflicts(t *testing.T) {
	m := NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{
		"tui.editor.undo":  {Ctrl("z")},
		"tui.editor.yank":  {Ctrl("z")},
		"tui.input.submit": {KeyEnter},
	})
	conflicts := m.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %#v", conflicts)
	}
	if conflicts[0].Key != Ctrl("z") {
		t.Errorf("conflict key: %v", conflicts[0].Key)
	}
	if len(conflicts[0].Bindings) != 2 {
		t.Errorf("conflict bindings: %v", conflicts[0].Bindings)
	}
}

func TestGlobalKeybindings(t *testing.T) {
	SetKeybindings(nil)
	kb := GetKeybindings()
	if kb == nil {
		t.Fatal("global manager nil")
	}
	if !kb.Matches("\r", "tui.input.submit") {
		t.Error("global submit")
	}
}
