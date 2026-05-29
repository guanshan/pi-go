package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

type fakeModelLister []ai.Model

func (f fakeModelLister) List(search string) []ai.Model {
	return append([]ai.Model(nil), f...)
}

func TestPrintModelsMatchesTypeScriptColumns(t *testing.T) {
	var out bytes.Buffer
	PrintModels(&out, fakeModelLister{
		{Provider: "openai", ID: "gpt-4.1", ContextWindow: 1_000_000, MaxOutput: 32_000, Input: []string{"text", "image"}},
		{Provider: "anthropic", ID: "claude-sonnet", ContextWindow: 200_000, MaxOutput: 8192, Reasoning: true, Input: []string{"text"}},
	}, "son")

	text := out.String()
	if !strings.Contains(text, "provider") || !strings.Contains(text, "max-out") || !strings.Contains(text, "thinking") {
		t.Fatalf("missing TS list-models headers:\n%s", text)
	}
	if strings.Contains(text, "gpt-4.1") {
		t.Fatalf("fuzzy search should filter non-matching model:\n%s", text)
	}
	if !strings.Contains(text, "claude-sonnet") || !strings.Contains(text, "200K") || !strings.Contains(text, "yes") {
		t.Fatalf("missing formatted model row:\n%s", text)
	}
}

func TestPrintModelsNoMatches(t *testing.T) {
	var out bytes.Buffer
	PrintModels(&out, fakeModelLister{{Provider: "openai", ID: "gpt-4.1"}}, "zzz")
	if got := strings.TrimSpace(out.String()); got != `No models matching "zzz"` {
		t.Fatalf("output=%q", got)
	}
}
