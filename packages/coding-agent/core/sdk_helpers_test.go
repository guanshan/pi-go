package core

import (
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestCoreResolveModelScopeAndEventBus(t *testing.T) {
	dir := t.TempDir()
	registry := ai.NewModelRegistry(dir, ai.NewAuthStorage(dir))
	resolved, thinking, err := ResolveCliModel(registry, "", "faux/faux:high")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Provider != "faux" || thinking != ai.ThinkingHigh {
		t.Fatalf("resolved=%#v thinking=%s", resolved, thinking)
	}
	scoped := ResolveModelScope(registry, []string{"faux/faux:high", "missing/model"})
	if len(scoped) != 1 || scoped[0].Model.Provider != "faux" || scoped[0].ThinkingLevel != ai.ThinkingHigh {
		t.Fatalf("scoped=%#v", scoped)
	}
	bus := NewEventBus()
	called := false
	bus.On("event", func(payload any) { called = payload.(string) == "ok" })
	bus.Emit("event", "ok")
	if !called {
		t.Fatal("event not delivered")
	}
	commands := BuiltinSlashCommands()
	want := []SlashCommandInfo{
		{Name: "settings", Description: "Open settings menu", Source: "builtin"},
		{Name: "model", Description: "Select model (opens selector UI)", Source: "builtin"},
		{Name: "scoped-models", Description: "Enable/disable models for Ctrl+P cycling", Source: "builtin"},
		{Name: "export", Description: "Export session (HTML default, or specify path: .html/.jsonl)", Source: "builtin"},
		{Name: "import", Description: "Import and resume a session from a JSONL file", Source: "builtin"},
		{Name: "share", Description: "Share session as a secret GitHub gist", Source: "builtin"},
		{Name: "copy", Description: "Copy last agent message to clipboard", Source: "builtin"},
		{Name: "name", Description: "Set session display name", Source: "builtin"},
		{Name: "session", Description: "Show session info and stats", Source: "builtin"},
		{Name: "changelog", Description: "Show changelog entries", Source: "builtin"},
		{Name: "hotkeys", Description: "Show all keyboard shortcuts", Source: "builtin"},
		{Name: "fork", Description: "Create a new fork from a previous user message", Source: "builtin"},
		{Name: "clone", Description: "Duplicate the current session at the current position", Source: "builtin"},
		{Name: "tree", Description: "Navigate session tree (switch branches)", Source: "builtin"},
		{Name: "trust", Description: "Save project trust decision for future sessions", Source: "builtin"},
		{Name: "login", Description: "Configure provider authentication", Source: "builtin"},
		{Name: "logout", Description: "Remove provider authentication", Source: "builtin"},
		{Name: "new", Description: "Start a new session", Source: "builtin"},
		{Name: "compact", Description: "Manually compact the session context", Source: "builtin"},
		{Name: "resume", Description: "Resume a different session", Source: "builtin"},
		{Name: "reload", Description: "Reload keybindings, extensions, skills, prompts, and themes", Source: "builtin"},
		{Name: "quit", Description: "Quit " + AppName, Source: "builtin"},
	}
	if len(commands) != len(want) {
		t.Fatalf("builtin slash command count=%d want=%d: %#v", len(commands), len(want), commands)
	}
	for i := range want {
		if commands[i] != want[i] {
			t.Fatalf("builtin slash command[%d]=%#v want=%#v", i, commands[i], want[i])
		}
	}
}
