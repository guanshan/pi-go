package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/tui"
)

// ModelLister is the small registry surface needed by PrintModels.
type ModelLister interface {
	List(search string) []ai.Model
	// AvailableConfigured returns the models whose provider has auth configured
	// (plus the faux placeholder). It mirrors the TS ModelRegistry.getAvailable
	// surface used by --list-models.
	AvailableConfigured() []ai.Model
}

// PrintModels renders the TS-compatible model table used by --list-models. It
// lists only the available (auth-configured) models, matching TS listModels
// (cli/list-models.ts), rather than the full catalog.
func PrintModels(w io.Writer, registry ModelLister, searchPattern string) {
	models := availableModelsForListing(registry)
	if len(models) == 0 {
		// No configured models at all: show the no-models guidance regardless of
		// any search pattern, matching TS listModels.
		fmt.Fprintln(w, noModelsAvailableGuidance)
		return
	}
	if searchPattern != "" {
		models = fuzzyFilterModels(models, searchPattern)
	}
	if len(models) == 0 {
		fmt.Fprintf(w, "No models matching %q\n", searchPattern)
		return
	}

	sort.Slice(models, func(i, j int) bool {
		if models[i].Provider != models[j].Provider {
			return models[i].Provider < models[j].Provider
		}
		return models[i].ID < models[j].ID
	})

	rows := make([]modelListRow, 0, len(models))
	for _, model := range models {
		rows = append(rows, modelListRow{
			Provider: model.Provider,
			Model:    model.ID,
			Context:  formatTokenCount(model.ContextWindow),
			MaxOut:   formatTokenCount(model.MaxOutput),
			// TS list-models.ts uses `m.reasoning ? "yes" : "no"` — only the
			// reasoning flag, NOT the count of thinking levels.
			Thinking: yesNo(model.Reasoning),
			Images:   yesNo(modelSupportsInput(model, "image")),
		})
	}
	printModelRows(w, rows)
}

// noModelsAvailableGuidance mirrors core.formatNoModelsAvailableMessage. It is
// duplicated here because the cli package cannot import core (import cycle).
const noModelsAvailableGuidance = "No models available. Use /login to log into a provider via OAuth or API key, or configure API keys or models.json and try again."

// availableModelsForListing returns the auth-configured models, excluding the
// faux placeholder (which AvailableConfigured includes unconditionally but TS
// getAvailable does not, since faux has no configured auth).
func availableModelsForListing(registry ModelLister) []ai.Model {
	available := registry.AvailableConfigured()
	out := make([]ai.Model, 0, len(available))
	for _, model := range available {
		if model.Provider == "faux" {
			continue
		}
		out = append(out, model)
	}
	return out
}

type modelListRow struct {
	Provider string
	Model    string
	Context  string
	MaxOut   string
	Thinking string
	Images   string
}

func printModelRows(w io.Writer, rows []modelListRow) {
	widths := modelListRow{
		Provider: "provider",
		Model:    "model",
		Context:  "context",
		MaxOut:   "max-out",
		Thinking: "thinking",
		Images:   "images",
	}
	for _, row := range rows {
		widths.Provider = wider(widths.Provider, row.Provider)
		widths.Model = wider(widths.Model, row.Model)
		widths.Context = wider(widths.Context, row.Context)
		widths.MaxOut = wider(widths.MaxOut, row.MaxOut)
		widths.Thinking = wider(widths.Thinking, row.Thinking)
		widths.Images = wider(widths.Images, row.Images)
	}

	fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %-*s  %-*s\n",
		len(widths.Provider), "provider",
		len(widths.Model), "model",
		len(widths.Context), "context",
		len(widths.MaxOut), "max-out",
		len(widths.Thinking), "thinking",
		len(widths.Images), "images",
	)
	for _, row := range rows {
		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %-*s  %-*s\n",
			len(widths.Provider), row.Provider,
			len(widths.Model), row.Model,
			len(widths.Context), row.Context,
			len(widths.MaxOut), row.MaxOut,
			len(widths.Thinking), row.Thinking,
			len(widths.Images), row.Images,
		)
	}
}

func fuzzyFilterModels(models []ai.Model, pattern string) []ai.Model {
	type scored struct {
		model ai.Model
		score int
	}
	var matches []scored
	for _, model := range models {
		haystack := model.Provider + " " + model.ID + " " + model.Name
		match, ok := tui.FuzzyMatchString(pattern, haystack)
		if !ok {
			continue
		}
		matches = append(matches, scored{model: model, score: match.Score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		left := matches[i].model.Provider + "/" + matches[i].model.ID
		right := matches[j].model.Provider + "/" + matches[j].model.ID
		return left < right
	})
	out := make([]ai.Model, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.model)
	}
	return out
}

func formatTokenCount(count int) string {
	// Mirrors formatTokenCount in src/cli/list-models.ts: counts below 1000 (incl.
	// 0) are rendered as the plain decimal (TS count.toString()), not "-".
	switch {
	case count >= 1_000_000:
		if count%1_000_000 == 0 {
			return fmt.Sprintf("%dM", count/1_000_000)
		}
		return fmt.Sprintf("%.1fM", float64(count)/1_000_000)
	case count >= 1_000:
		if count%1_000 == 0 {
			return fmt.Sprintf("%dK", count/1_000)
		}
		return fmt.Sprintf("%.1fK", float64(count)/1_000)
	default:
		return fmt.Sprintf("%d", count)
	}
}

func modelSupportsInput(model ai.Model, input string) bool {
	for _, value := range model.Input {
		if strings.EqualFold(value, input) {
			return true
		}
	}
	return false
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func wider(left, right string) string {
	if len(right) > len(left) {
		return right
	}
	return left
}
