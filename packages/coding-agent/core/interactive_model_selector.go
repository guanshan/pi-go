package core

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/tui"
)

// modelSelectorOverlay is the interactive `/model` (and Ctrl+L) picker. It
// mirrors the TS interactive-mode showModelSelector: instead of floating a box
// over the transcript, the parent View() swaps the input region for this
// overlay's rendered lines (header + body + footer stay). The overlay is a thin
// stateful wrapper around tui.SelectList: SelectList owns navigation/rendering,
// while the overlay owns the typed filter string (SelectList.HandleInput does
// not consume printable characters) and the substring matching that mirrors
// modelCommandSuggestions / TS app.model filtering.
//
// Deadlock note: the overlay never calls AgentSession.SetModel itself. Enter is
// resolved by the parent interactiveModel, which records the chosen value,
// closes the overlay, and returns a tea.Cmd that performs the switch off the
// Bubble Tea Update goroutine (SetModel -> emitSessionEvent -> program.Send
// blocks on the unbuffered msg channel; calling it inline would deadlock — the
// same hazard slice 1's cycleModel guards against).
type modelSelectorOverlay struct {
	list    *tui.SelectList
	all     []tui.SelectItem
	filter  string
	current string // provider/id of the active model, for the highlight marker.
}

// interactiveSelectorTheme styles the overlay. defaultSelectTheme() in
// packages/tui is unexported, so production code supplies its own theme
// literal (all SelectListTheme fields are exported func(string)string).
var interactiveSelectorTheme = tui.SelectListTheme{
	SelectedPrefix: func(s string) string { return s },
	SelectedText:   func(s string) string { return interactiveSelectorSelectedStyle.Render(s) },
	Description:    func(s string) string { return interactiveSelectorDescStyle.Render(s) },
	ScrollInfo:     func(s string) string { return interactiveSelectorDescStyle.Render(s) },
	NoMatch:        func(s string) string { return interactiveSelectorDescStyle.Render(s) },
}

var (
	interactiveSelectorTitleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#70A5FF")).Bold(true)
	interactiveSelectorSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#70A5FF")).Bold(true)
	interactiveSelectorDescStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C8A99"))
	interactiveSelectorHintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C8A99"))
)

const interactiveSelectorMaxVisible = 8

// newModelSelectorOverlay builds an overlay from the available models, marking
// the current model (provider/id) so it can be highlighted. Returns nil when
// there are no models to choose from.
func newModelSelectorOverlay(models []ai.Model, current ai.Model) *modelSelectorOverlay {
	items := make([]tui.SelectItem, 0, len(models))
	currentValue := current.Provider + "/" + current.ID
	currentSeen := false
	for _, model := range models {
		if model.Provider == "" {
			continue
		}
		value := model.Provider + "/" + model.ID
		if value == currentValue {
			currentSeen = true
		}
		items = append(items, tui.SelectItem{
			Value:       value,
			Label:       modelSelectorLabel(model),
			Description: model.Provider,
		})
	}
	if len(items) == 0 {
		return nil
	}
	overlay := &modelSelectorOverlay{
		all:     items,
		current: currentValue,
	}
	overlay.list = tui.NewSelectList(items, interactiveSelectorMaxVisible, interactiveSelectorTheme, tui.SelectListLayoutOptions{})
	if currentSeen {
		overlay.selectValue(currentValue)
	}
	return overlay
}

// modelSelectorLabel mirrors the /model suggestion display: prefer the friendly
// Name, fall back to the bare id.
func modelSelectorLabel(model ai.Model) string {
	if strings.TrimSpace(model.Name) != "" {
		return model.Name
	}
	return model.ID
}

// selectValue moves the highlight onto the item with the given Value, if it is
// present in the currently filtered set.
func (o *modelSelectorOverlay) selectValue(value string) {
	for i, item := range o.list.Items() {
		if item.Value == value {
			o.list.SetSelectedIndex(i)
			return
		}
	}
}

// applyFilter narrows the item set with the same substring matching as
// modelCommandSuggestions (case-insensitive over label + value), rather than
// SelectList.SetFilter's prefix-on-Value, so typing "gpt" still surfaces
// "openai/gpt-5". The previously highlighted value is preserved when it
// survives the filter; otherwise selection resets to the first match.
func (o *modelSelectorOverlay) applyFilter() {
	previous := ""
	if item, ok := o.list.SelectedItem(); ok {
		previous = item.Value
	}
	needle := strings.ToLower(strings.TrimSpace(o.filter))
	if needle == "" {
		o.list.SetItems(o.all)
	} else {
		matched := make([]tui.SelectItem, 0, len(o.all))
		for _, item := range o.all {
			haystack := strings.ToLower(item.Label + " " + item.Value)
			if strings.Contains(haystack, needle) {
				matched = append(matched, item)
			}
		}
		o.list.SetItems(matched)
	}
	if previous != "" {
		o.selectValue(previous)
	}
}

// modelSelectorAction reports what the parent model should do after the overlay
// consumed a key.
type modelSelectorAction int

const (
	// modelSelectorNone: key handled, overlay stays open, no further action.
	modelSelectorNone modelSelectorAction = iota
	// modelSelectorSelect: Enter pressed; SelectedValue holds the chosen model.
	modelSelectorSelect
	// modelSelectorCancel: Esc/Ctrl+C pressed; overlay should close.
	modelSelectorCancel
)

// HandleKey processes a Bubble Tea key (msg.String()) for the overlay and
// reports the resulting action. Navigation keys are translated to the raw
// terminal escape sequences SelectList.HandleInput / MatchesKey expect
// (msg.String() yields names like "up", not "\x1b[A"); printable characters and
// backspace drive the local filter, which SelectList does not handle itself.
func (o *modelSelectorOverlay) HandleKey(key string) modelSelectorAction {
	switch key {
	case "up":
		o.list.HandleInput("\x1b[A")
		return modelSelectorNone
	case "down":
		o.list.HandleInput("\x1b[B")
		return modelSelectorNone
	case "enter":
		if _, ok := o.list.SelectedItem(); ok {
			return modelSelectorSelect
		}
		return modelSelectorNone
	case "esc", "ctrl+c":
		return modelSelectorCancel
	case "backspace":
		if o.filter != "" {
			runes := []rune(o.filter)
			o.filter = string(runes[:len(runes)-1])
			o.applyFilter()
		}
		return modelSelectorNone
	case "space":
		o.filter += " "
		o.applyFilter()
		return modelSelectorNone
	}
	// Printable single characters extend the filter. Bubble Tea reports plain
	// keys as their literal rune (e.g. "g"), so anything that is a single
	// printable rune with no modifier prefix is filter input.
	if isPrintableKeyString(key) {
		o.filter += key
		o.applyFilter()
	}
	return modelSelectorNone
}

// SelectedValue returns the provider/id of the highlighted model.
func (o *modelSelectorOverlay) SelectedValue() (string, bool) {
	item, ok := o.list.SelectedItem()
	if !ok {
		return "", false
	}
	return item.Value, true
}

// Render produces the overlay's lines (title, list, filter/help footer) that
// replace the input region in interactiveModel.View().
func (o *modelSelectorOverlay) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	title := "Select model"
	if o.filter != "" {
		title += "  filter: " + o.filter
	}
	lines := []string{interactiveSelectorTitleStyle.Render(tui.TruncateToWidth(title, width, "..."))}
	lines = append(lines, o.list.Render(width)...)
	hint := "↑/↓ move · enter select · esc cancel · type to filter"
	lines = append(lines, interactiveSelectorHintStyle.Render(tui.TruncateToWidth(hint, width, "...")))
	return lines
}

// isPrintableKeyString reports whether a Bubble Tea key string is a single
// printable character with no modifier prefix (so it is filter text rather than
// a navigation/control key). Modifier combos contain "+"; named keys are longer
// than one rune.
func isPrintableKeyString(key string) bool {
	if strings.Contains(key, "+") {
		return false
	}
	runes := []rune(key)
	if len(runes) != 1 {
		return false
	}
	return runes[0] >= 0x20 && runes[0] != 0x7f
}
