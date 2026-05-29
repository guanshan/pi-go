package core

import (
	"slices"
	"testing"
)

func TestSlashCommandSuggestions(t *testing.T) {
	suggestions := slashCommandSuggestions("/mo")
	if !slices.Contains(suggestions, "/model") {
		t.Fatalf("expected /model suggestion, got %#v", suggestions)
	}
	if got := slashCommandSuggestions("/model gpt"); len(got) != 0 {
		t.Fatalf("expected no suggestions after arguments, got %#v", got)
	}
}
