package tui

import (
	"fmt"
	"strings"
)

// SettingItem represents a single toggleable setting.
type SettingItem struct {
	Label       string
	Description string
	Value       string
	Enabled     bool
}

// SettingsListTheme styles the list. Each style fn maps plain text to ANSI.
type SettingsListTheme struct {
	SelectedPrefix func(string) string
	SelectedText   func(string) string
	Description    func(string) string
	ScrollInfo     func(string) string
}

func defaultSettingsTheme() SettingsListTheme {
	identity := func(s string) string { return s }
	dim := func(s string) string { return "\x1b[2m" + s + "\x1b[0m" }
	bold := func(s string) string { return "\x1b[1m" + s + "\x1b[0m" }
	return SettingsListTheme{
		SelectedPrefix: identity,
		SelectedText:   bold,
		Description:    dim,
		ScrollInfo:     dim,
	}
}

// SettingsList renders a list of settings with arrow-key navigation,
// wrap-around, scrollable viewport (when MaxVisible > 0), and Description /
// Value columns.
type SettingsList struct {
	Items      []SettingItem
	Selected   int
	MaxVisible int
	Theme      SettingsListTheme
	OnToggle   func(int, SettingItem)
	OnChange   func(int, SettingItem)
}

func (s *SettingsList) effectiveTheme() SettingsListTheme {
	t := s.Theme
	def := defaultSettingsTheme()
	if t.SelectedPrefix == nil {
		t.SelectedPrefix = def.SelectedPrefix
	}
	if t.SelectedText == nil {
		t.SelectedText = def.SelectedText
	}
	if t.Description == nil {
		t.Description = def.Description
	}
	if t.ScrollInfo == nil {
		t.ScrollInfo = def.ScrollInfo
	}
	return t
}

func (s *SettingsList) clampSelection() {
	if len(s.Items) == 0 {
		s.Selected = 0
		return
	}
	if s.Selected < 0 {
		s.Selected = 0
	}
	if s.Selected >= len(s.Items) {
		s.Selected = len(s.Items) - 1
	}
}

// Render produces one line per visible setting; if MaxVisible > 0 the list
// scrolls to keep the highlighted item visible.
func (s *SettingsList) Render(width int) []string {
	s.clampSelection()
	theme := s.effectiveTheme()
	if len(s.Items) == 0 {
		return nil
	}

	maxVis := s.MaxVisible
	if maxVis <= 0 || maxVis > len(s.Items) {
		maxVis = len(s.Items)
	}
	half := maxVis / 2
	start := s.Selected - half
	if start > len(s.Items)-maxVis {
		start = len(s.Items) - maxVis
	}
	if start < 0 {
		start = 0
	}
	end := start + maxVis
	if end > len(s.Items) {
		end = len(s.Items)
	}

	lines := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		lines = append(lines, s.renderItem(s.Items[i], i == s.Selected, width, theme))
	}
	if start > 0 || end < len(s.Items) {
		info := fmt.Sprintf("  (%d/%d)", s.Selected+1, len(s.Items))
		lines = append(lines, theme.ScrollInfo(TruncateToWidth(info, maxInt(1, width-2), "")))
	}
	return lines
}

func (s *SettingsList) renderItem(item SettingItem, selected bool, width int, theme SettingsListTheme) string {
	prefix := "  "
	if selected {
		prefix = theme.SelectedPrefix("> ")
	}
	check := "[ ]"
	if item.Enabled {
		check = "[x]"
	}
	head := prefix + check + " " + item.Label
	value := strings.TrimSpace(item.Value)
	if value != "" {
		head += " " + value
	}
	desc := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(item.Description, "\r", " "), "\n", " "))
	if desc != "" && width > 40 {
		headWidth := VisibleWidth(head)
		remaining := width - headWidth - 2
		if remaining > 8 {
			truncated := TruncateToWidth(desc, remaining, "")
			descCol := theme.Description("  " + truncated)
			out := head + descCol
			if selected {
				return TruncateToWidth(theme.SelectedText(out), width, "...")
			}
			return TruncateToWidth(out, width, "...")
		}
	}
	out := TruncateToWidth(head, width, "...")
	if selected {
		return theme.SelectedText(out)
	}
	return out
}

// HandleInput supports up/down navigation (with wrap) and space/enter to
// toggle the highlighted setting.
func (s *SettingsList) HandleInput(data string) {
	kb := GetKeybindings()
	switch {
	case kb.Matches(data, "tui.select.up"):
		s.clampSelection()
		if len(s.Items) == 0 {
			return
		}
		if s.Selected == 0 {
			s.Selected = len(s.Items) - 1
		} else {
			s.Selected--
		}
		s.notifyChange()
	case kb.Matches(data, "tui.select.down"):
		s.clampSelection()
		if len(s.Items) == 0 {
			return
		}
		if s.Selected == len(s.Items)-1 {
			s.Selected = 0
		} else {
			s.Selected++
		}
		s.notifyChange()
	case data == " " || kb.Matches(data, "tui.select.confirm"):
		s.clampSelection()
		if len(s.Items) == 0 {
			return
		}
		s.Items[s.Selected].Enabled = !s.Items[s.Selected].Enabled
		if s.OnToggle != nil {
			s.OnToggle(s.Selected, s.Items[s.Selected])
		}
	}
}

func (s *SettingsList) notifyChange() {
	if s.OnChange == nil {
		return
	}
	s.clampSelection()
	if s.Selected >= 0 && s.Selected < len(s.Items) {
		s.OnChange(s.Selected, s.Items[s.Selected])
	}
}
