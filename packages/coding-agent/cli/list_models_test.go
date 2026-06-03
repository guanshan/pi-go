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

// TestPrintModelsThinkingAndMaxOutMatchTS verifies two TS-parity fixes:
//   - the thinking column uses only model.Reasoning (TS `m.reasoning ? ...`), so
//     a model with multiple ThinkingLevels but reasoning=false shows "no";
//   - formatTokenCount(0) renders "0" (TS count.toString()), not "-".
func TestPrintModelsThinkingAndMaxOutMatchTS(t *testing.T) {
	var out bytes.Buffer
	PrintModels(&out, fakeModelLister{
		{
			Provider:       "openai",
			ID:             "zero-out",
			ContextWindow:  0,
			MaxOutput:      0,
			Reasoning:      false,
			ThinkingLevels: []ai.ThinkingLevel{"off", "low", "high"},
			Input:          []string{"text"},
		},
	}, "")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var row string
	for _, l := range lines {
		if strings.Contains(l, "zero-out") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("model row not found:\n%s", out.String())
	}
	fields := strings.Fields(row)
	// columns: provider model context max-out thinking images
	if len(fields) < 6 {
		t.Fatalf("unexpected row columns: %q", row)
	}
	if fields[2] != "0" || fields[3] != "0" {
		t.Fatalf("context/max-out should be 0 0, got %q %q (row %q)", fields[2], fields[3], row)
	}
	if fields[4] != "no" {
		t.Fatalf("thinking should be 'no' when reasoning=false despite ThinkingLevels, got %q (row %q)", fields[4], row)
	}
}

func TestFormatTokenCountMatchesTS(t *testing.T) {
	cases := map[int]string{
		0:         "0",
		500:       "500",
		1_000:     "1K",
		1_500:     "1.5K",
		200_000:   "200K",
		1_000_000: "1M",
		1_500_000: "1.5M",
	}
	for count, want := range cases {
		if got := formatTokenCount(count); got != want {
			t.Errorf("formatTokenCount(%d)=%q, want %q", count, got, want)
		}
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
