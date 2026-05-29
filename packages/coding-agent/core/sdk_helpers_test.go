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
	foundResume := false
	foundImport := false
	for _, command := range commands {
		if command.Name == "resume" {
			foundResume = true
		}
		if command.Name == "import" {
			foundImport = true
		}
	}
	if !foundResume || !foundImport {
		t.Fatalf("builtin slash commands=%#v", commands)
	}
}
