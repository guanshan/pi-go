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

// AvailableConfigured treats every fake model as auth-configured.
func (f fakeModelLister) AvailableConfigured() []ai.Model {
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

// configurableLister lets a test distinguish the full catalog (List) from the
// available/auth-configured subset (AvailableConfigured).
type configurableLister struct {
	all       []ai.Model
	available []ai.Model
}

func (c configurableLister) List(string) []ai.Model { return append([]ai.Model(nil), c.all...) }
func (c configurableLister) AvailableConfigured() []ai.Model {
	return append([]ai.Model(nil), c.available...)
}

func TestPrintModelsNoConfiguredModelsShowsGuidance(t *testing.T) {
	// Empty auth: AvailableConfigured returns only faux, which is excluded, so the
	// no-models guidance is shown even though the full catalog is non-empty.
	var out bytes.Buffer
	PrintModels(&out, configurableLister{
		all:       []ai.Model{{Provider: "openai", ID: "gpt-4.1"}, {Provider: "faux", ID: "faux"}},
		available: []ai.Model{{Provider: "faux", ID: "faux"}},
	}, "")
	got := strings.TrimSpace(out.String())
	if !strings.HasPrefix(got, "No models available.") {
		t.Fatalf("expected no-models guidance, got %q", got)
	}
	if strings.Contains(got, "gpt-4.1") {
		t.Fatalf("full catalog leaked into output: %q", got)
	}
}

func TestPrintModelsListsOnlyAvailable(t *testing.T) {
	// One provider configured: only that provider's models appear, not the full
	// catalog (and never faux).
	var out bytes.Buffer
	PrintModels(&out, configurableLister{
		all: []ai.Model{
			{Provider: "openai", ID: "gpt-4.1"},
			{Provider: "anthropic", ID: "claude-sonnet"},
			{Provider: "faux", ID: "faux"},
		},
		available: []ai.Model{
			{Provider: "openai", ID: "gpt-4.1"},
			{Provider: "faux", ID: "faux"},
		},
	}, "")
	text := out.String()
	if !strings.Contains(text, "gpt-4.1") {
		t.Fatalf("missing available model: %s", text)
	}
	if strings.Contains(text, "claude-sonnet") {
		t.Fatalf("unconfigured provider model leaked: %s", text)
	}
	if strings.Contains(text, "faux") {
		t.Fatalf("faux should be excluded: %s", text)
	}
}
