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
