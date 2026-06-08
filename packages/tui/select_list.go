package tui

import (
	"fmt"
	"strings"
)

// SelectItem represents a single entry in a SelectList.
type SelectItem struct {
	// Value is the unique identifier; what callbacks receive.
	Value string
	// Label is what gets rendered. Falls back to Value if empty.
	Label string
	// Description is optional secondary text shown next to Label when the
	// terminal is wide enough.
	Description string
}

// SelectListTheme styles the various parts of a SelectList. Each function
// receives plain text and returns ANSI-styled text.
type SelectListTheme struct {
	SelectedPrefix func(string) string
	SelectedText   func(string) string
	Description    func(string) string
	ScrollInfo     func(string) string
	NoMatch        func(string) string
}

func defaultSelectTheme() SelectListTheme {
	identity := func(s string) string { return s }
	dim := func(s string) string { return "\x1b[2m" + s + "\x1b[0m" }
	bold := func(s string) string { return "\x1b[1m" + s + "\x1b[0m" }
	return SelectListTheme{
		SelectedPrefix: identity,
		SelectedText:   bold,
		Description:    dim,
		ScrollInfo:     dim,
		NoMatch:        dim,
	}
}

// SelectListLayoutOptions controls primary-column sizing and an optional
// hook for per-item primary-column truncation.
type SelectListLayoutOptions struct {
	MinPrimaryColumnWidth int
	MaxPrimaryColumnWidth int
	TruncatePrimary       func(text string, maxWidth, columnWidth int, item SelectItem, isSelected bool) string
}

const (
	defaultPrimaryColumnWidth = 32
	primaryColumnGap          = 2
	minDescriptionWidth       = 10
)

// SelectList is a vertical list of selectable items with arrow-key navigation,
// wrap-around, optional fuzzy filter, scrollable viewport, and a two-column
// label/description layout.
type SelectList struct {
	items    []SelectItem
	filtered []SelectItem
	selected int
	maxVis   int
	theme    SelectListTheme
	layout   SelectListLayoutOptions
	OnSelect func(SelectItem)
	OnCancel func()
	OnChange func(SelectItem)
}

// NewSelectList constructs a SelectList. maxVisible <= 0 falls back to 5.
func NewSelectList(items []SelectItem, maxVisible int, theme SelectListTheme, layout SelectListLayoutOptions) *SelectList {
	if maxVisible <= 0 {
		maxVisible = 5
	}
	if theme.SelectedPrefix == nil && theme.SelectedText == nil &&
		theme.Description == nil && theme.ScrollInfo == nil && theme.NoMatch == nil {
		theme = defaultSelectTheme()
	}
	return &SelectList{
		items:    items,
		filtered: items,
		maxVis:   maxVisible,
		theme:    theme,
		layout:   layout,
	}
}

// Items returns the current (post-filter) list.
func (s *SelectList) Items() []SelectItem { return s.filtered }

// SetItems replaces the underlying items and resets filter / selection.
func (s *SelectList) SetItems(items []SelectItem) {
	s.items = items
	s.filtered = items
	s.selected = 0
}

// SetFilter applies a case-insensitive prefix filter on item.Value, mirroring
// upstream's setFilter. Pass "" to clear.
func (s *SelectList) SetFilter(filter string) {
	if filter == "" {
		s.filtered = s.items
	} else {
		needle := strings.ToLower(filter)
		out := make([]SelectItem, 0, len(s.items))
		for _, it := range s.items {
			if strings.HasPrefix(strings.ToLower(it.Value), needle) {
				out = append(out, it)
			}
		}
		s.filtered = out
	}
	s.selected = 0
}

// SetSelectedIndex moves the highlight to index, clamped.
func (s *SelectList) SetSelectedIndex(index int) {
	if index < 0 {
		index = 0
	}
	if max := len(s.filtered) - 1; index > max {
		index = max
	}
	if index < 0 {
		index = 0
	}
	s.selected = index
}

// SelectedItem returns the highlighted item (or zero value if list is empty).
func (s *SelectList) SelectedItem() (SelectItem, bool) {
	if s.selected < 0 || s.selected >= len(s.filtered) {
		return SelectItem{}, false
	}
	return s.filtered[s.selected], true
}

// Render produces the list lines for the given width.
func (s *SelectList) Render(width int) []string {
	if len(s.filtered) == 0 {
		return []string{s.theme.NoMatch("  No matching commands")}
	}
	primary := s.primaryColumnWidth()
	maxVis := s.maxVis
	if maxVis <= 0 {
		maxVis = 5
	}

	half := maxVis / 2
	start := s.selected - half
	if start > len(s.filtered)-maxVis {
		start = len(s.filtered) - maxVis
	}
	if start < 0 {
		start = 0
	}
	end := start + maxVis
	if end > len(s.filtered) {
		end = len(s.filtered)
	}

	lines := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		lines = append(lines, s.renderItem(s.filtered[i], i == s.selected, width, primary))
	}
	if start > 0 || end < len(s.filtered) {
		info := fmt.Sprintf("  (%d/%d)", s.selected+1, len(s.filtered))
		lines = append(lines, s.theme.ScrollInfo(TruncateToWidth(info, width-2, "")))
	}
	return lines
}

func (s *SelectList) renderItem(item SelectItem, selected bool, width, primary int) string {
	prefix := "  "
	if selected {
		prefix = "→ "
	}
	prefixWidth := VisibleWidth(prefix)
	desc := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(item.Description, "\r", " "), "\n", " "))

	if desc != "" && width > 40 {
		colWidth := primary
		if colWidth > width-prefixWidth-4 {
			colWidth = width - prefixWidth - 4
		}
		if colWidth < 1 {
			colWidth = 1
		}
		maxPrimary := colWidth - primaryColumnGap
		if maxPrimary < 1 {
			maxPrimary = 1
		}
		value := s.truncatePrimary(item, selected, maxPrimary, colWidth)
		valueWidth := VisibleWidth(value)
		spacing := strings.Repeat(" ", maxInt(1, colWidth-valueWidth))
		descStart := prefixWidth + valueWidth + len(spacing)
		remaining := width - descStart - 2
		if remaining > minDescriptionWidth {
			truncatedDesc := TruncateToWidth(desc, remaining, "")
			if selected {
				return s.theme.SelectedText(prefix + value + spacing + truncatedDesc)
			}
			descStyled := s.theme.Description(spacing + truncatedDesc)
			return prefix + value + descStyled
		}
	}

	maxWidth := width - prefixWidth - 2
	value := s.truncatePrimary(item, selected, maxWidth, maxWidth)
	if selected {
		return s.theme.SelectedText(prefix + value)
	}
	return prefix + value
}

func (s *SelectList) primaryColumnWidth() int {
	rawMin := s.layout.MinPrimaryColumnWidth
	rawMax := s.layout.MaxPrimaryColumnWidth
	if rawMin == 0 && rawMax == 0 {
		rawMin = defaultPrimaryColumnWidth
		rawMax = defaultPrimaryColumnWidth
	}
	if rawMin == 0 {
		rawMin = rawMax
	}
	if rawMax == 0 {
		rawMax = rawMin
	}
	if rawMin > rawMax {
		rawMin, rawMax = rawMax, rawMin
	}
	if rawMin < 1 {
		rawMin = 1
	}
	widest := 0
	for _, item := range s.filtered {
		v := s.displayValue(item)
		if w := VisibleWidth(v) + primaryColumnGap; w > widest {
			widest = w
		}
	}
	if widest < rawMin {
		return rawMin
	}
	if widest > rawMax {
		return rawMax
	}
	return widest
}

func (s *SelectList) truncatePrimary(item SelectItem, selected bool, maxWidth, columnWidth int) string {
	display := s.displayValue(item)
	var out string
	if s.layout.TruncatePrimary != nil {
		out = s.layout.TruncatePrimary(display, maxWidth, columnWidth, item, selected)
	} else {
		out = TruncateToWidth(display, maxWidth, "")
	}
	return TruncateToWidth(out, maxWidth, "")
}

func (s *SelectList) displayValue(item SelectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}

// HandleInput dispatches arrow / enter / escape via the global keybindings.
func (s *SelectList) HandleInput(data string) {
	kb := GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.up"):
		if len(s.filtered) == 0 {
			return
		}
		if s.selected == 0 {
			s.selected = len(s.filtered) - 1
		} else {
			s.selected--
		}
		s.notifyChange()
	case kb.Matches(data, "tui.select.down"):
		if len(s.filtered) == 0 {
			return
		}
		if s.selected == len(s.filtered)-1 {
			s.selected = 0
		} else {
			s.selected++
		}
		s.notifyChange()
	case kb.Matches(data, "tui.select.confirm"):
		if it, ok := s.SelectedItem(); ok && s.OnSelect != nil {
			s.OnSelect(it)
		}
	case kb.Matches(data, "tui.select.cancel"):
		if s.OnCancel != nil {
			s.OnCancel()
		}
	}
}

func (s *SelectList) notifyChange() {
	if s.OnChange == nil {
		return
	}
	if it, ok := s.SelectedItem(); ok {
		s.OnChange(it)
	}
}
