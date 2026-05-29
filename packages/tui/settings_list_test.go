package tui

import (
	"strings"
	"testing"
)

func TestSettingsListBasicRender(t *testing.T) {
	s := &SettingsList{
		Items: []SettingItem{
			{Label: "Theme", Value: "dark"},
			{Label: "Verbose", Enabled: true},
		},
	}
	lines := s.Render(40)
	if len(lines) != 2 {
		t.Fatalf("lines: %d", len(lines))
	}
	if !strings.Contains(lines[0], "Theme") || !strings.Contains(lines[0], "dark") {
		t.Errorf("first line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "[x]") {
		t.Errorf("second line missing [x]: %q", lines[1])
	}
}

func TestSettingsListDescriptionColumn(t *testing.T) {
	s := &SettingsList{
		Items: []SettingItem{
			{Label: "Theme", Description: "Color theme used by the editor"},
		},
	}
	out := strings.Join(s.Render(80), "\n")
	if !strings.Contains(out, "Color theme") {
		t.Errorf("description not rendered: %q", out)
	}
	// Narrow width should drop the description column.
	out = strings.Join(s.Render(20), "\n")
	if strings.Contains(out, "Color theme") {
		t.Errorf("description rendered at narrow width: %q", out)
	}
}

func TestSettingsListSelectedPrefix(t *testing.T) {
	s := &SettingsList{
		Items: []SettingItem{
			{Label: "A"},
			{Label: "B"},
		},
		Selected: 1,
	}
	lines := s.Render(40)
	if !strings.Contains(lines[1], "> ") {
		t.Errorf("selected prefix missing: %q", lines[1])
	}
	// Item 0 unselected → no chevron.
	if strings.Contains(lines[0], "> ") {
		t.Errorf("non-selected has chevron: %q", lines[0])
	}
}

func TestSettingsListWrapAround(t *testing.T) {
	SetKeybindings(nil)
	s := &SettingsList{Items: []SettingItem{{Label: "A"}, {Label: "B"}, {Label: "C"}}}
	// Up at top wraps to last.
	s.HandleInput("\x1b[A")
	if s.Selected != 2 {
		t.Errorf("up wrap: %d", s.Selected)
	}
	// Down at bottom wraps to first.
	s.HandleInput("\x1b[B")
	if s.Selected != 0 {
		t.Errorf("down wrap: %d", s.Selected)
	}
}

func TestSettingsListToggleAndCallback(t *testing.T) {
	SetKeybindings(nil)
	var toggleIdx = -1
	var changeIdx = -1
	s := &SettingsList{
		Items:    []SettingItem{{Label: "A"}, {Label: "B"}},
		OnToggle: func(i int, _ SettingItem) { toggleIdx = i },
		OnChange: func(i int, _ SettingItem) { changeIdx = i },
	}
	s.HandleInput(" ")
	if !s.Items[0].Enabled {
		t.Error("space did not toggle")
	}
	if toggleIdx != 0 {
		t.Errorf("OnToggle: %d", toggleIdx)
	}
	s.HandleInput("\x1b[B")
	if changeIdx != 1 {
		t.Errorf("OnChange: %d", changeIdx)
	}
}

func TestSettingsListScrollViewport(t *testing.T) {
	items := make([]SettingItem, 10)
	for i := range items {
		items[i].Label = string(rune('A' + i))
	}
	s := &SettingsList{Items: items, MaxVisible: 3, Selected: 5}
	out := s.Render(40)
	// 3 items + scroll info.
	if len(out) != 4 {
		t.Fatalf("expected 4 lines, got %d: %#v", len(out), out)
	}
	if !strings.Contains(out[3], "/10") {
		t.Errorf("scroll info: %q", out[3])
	}
}

func TestSettingsListEmpty(t *testing.T) {
	s := &SettingsList{}
	if got := s.Render(20); len(got) != 0 {
		t.Errorf("empty render: %#v", got)
	}
	// HandleInput should be safe no-op.
	s.HandleInput(" ")
	s.HandleInput("\x1b[A")
}

func TestSettingsListConfirmKey(t *testing.T) {
	SetKeybindings(nil)
	var idx = -1
	s := &SettingsList{
		Items:    []SettingItem{{Label: "A"}},
		OnToggle: func(i int, _ SettingItem) { idx = i },
	}
	s.HandleInput("\r") // tui.select.confirm default
	if idx != 0 || !s.Items[0].Enabled {
		t.Errorf("confirm did not toggle")
	}
}

func TestSettingsListThemeOverride(t *testing.T) {
	red := func(s string) string { return "\x1b[31m" + s + "\x1b[0m" }
	s := &SettingsList{
		Items:    []SettingItem{{Label: "x"}},
		Selected: 0,
		Theme:    SettingsListTheme{SelectedText: red},
	}
	out := s.Render(40)
	if !strings.Contains(out[0], "\x1b[31m") {
		t.Errorf("theme override missing: %q", out[0])
	}
}
