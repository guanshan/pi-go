package core

import (
	"errors"
	"sync"

	"github.com/guanshan/pi-go/packages/ai"
)

type EventBus struct {
	mu        sync.RWMutex
	listeners map[string][]func(any)
}

func NewEventBus() *EventBus {
	return &EventBus{listeners: map[string][]func(any){}}
}

func (b *EventBus) On(event string, listener func(any)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners[event] = append(b.listeners[event], listener)
	index := len(b.listeners[event]) - 1
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if index >= 0 && index < len(b.listeners[event]) {
			b.listeners[event][index] = nil
		}
	}
}

func (b *EventBus) Emit(event string, payload any) {
	b.mu.RLock()
	listeners := append([]func(any){}, b.listeners[event]...)
	b.mu.RUnlock()
	for _, listener := range listeners {
		if listener != nil {
			listener(payload)
		}
	}
}

func ResolveCliModel(registry *ai.ModelRegistry, provider, modelPattern string) (ai.Model, ai.ThinkingLevel, error) {
	modelText, thinking, hasThinking := splitThinking(modelPattern)
	model, ok, warning := registry.Match(provider, modelText)
	if !ok {
		return ai.Model{}, "", errors.New(warning)
	}
	if !hasThinking {
		thinking = ""
	}
	return model, thinking, nil
}

func ResolveModelScope(registry *ai.ModelRegistry, patterns []string) []ScopedModel {
	var out []ScopedModel
	for _, pattern := range patterns {
		modelText, thinking, _ := splitThinking(pattern)
		if model, ok, _ := registry.Match("", modelText); ok {
			out = append(out, ScopedModel{Model: model, ThinkingLevel: thinking})
		}
	}
	return out
}

type SlashCommandInfo struct {
	Name        string
	Description string
	Source      string
}

func BuiltinSlashCommands() []SlashCommandInfo {
	return []SlashCommandInfo{
		{Name: "login", Description: "Save an API key", Source: "builtin"},
		{Name: "logout", Description: "Remove credentials", Source: "builtin"},
		{Name: "model", Description: "Switch model", Source: "builtin"},
		{Name: "settings", Description: "Show settings", Source: "builtin"},
		{Name: "resume", Description: "List or resume sessions", Source: "builtin"},
		{Name: "new", Description: "Start new session", Source: "builtin"},
		{Name: "import", Description: "Import a session JSONL file", Source: "builtin"},
		{Name: "session", Description: "Show session information", Source: "builtin"},
		{Name: "compact", Description: "Compact context", Source: "builtin"},
		{Name: "export", Description: "Export session", Source: "builtin"},
		{Name: "share", Description: "Share session as a secret GitHub gist", Source: "builtin"},
		{Name: "copy", Description: "Copy last agent message to clipboard", Source: "builtin"},
		{Name: "quit", Description: "Quit", Source: "builtin"},
	}
}

func splitThinking(model string) (string, ai.ThinkingLevel, bool) {
	idx := -1
	for i := len(model) - 1; i >= 0; i-- {
		if model[i] == ':' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return model, "", false
	}
	level := model[idx+1:]
	if !ai.IsValidThinkingLevel(level) {
		return model, "", false
	}
	return model[:idx], ai.ThinkingLevel(level), true
}
