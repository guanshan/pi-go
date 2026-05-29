package tui

import (
	"strings"
	"testing"
)

func TestSelectListNavigationWraps(t *testing.T) {
	SetKeybindings(nil)
	items := []SelectItem{
		{Value: "a", Label: "Apple"},
		{Value: "b", Label: "Banana"},
		{Value: "c", Label: "Cherry"},
	}
	s := NewSelectList(items, 5, SelectListTheme{}, SelectListLayoutOptions{})

	// Down 3 times wraps to 0.
	s.HandleInput("\x1b[B")
	s.HandleInput("\x1b[B")
	s.HandleInput("\x1b[B")
	if it, _ := s.SelectedItem(); it.Value != "a" {
		t.Errorf("wrap down: %s", it.Value)
	}
	// Up wraps to last.
	s.HandleInput("\x1b[A")
	if it, _ := s.SelectedItem(); it.Value != "c" {
		t.Errorf("wrap up: %s", it.Value)
	}
}

func TestSelectListFilter(t *testing.T) {
	items := []SelectItem{
		{Value: "alpha"},
		{Value: "beta"},
		{Value: "alphabet"},
	}
	s := NewSelectList(items, 5, SelectListTheme{}, SelectListLayoutOptions{})
	s.SetFilter("al")
	if got := s.Items(); len(got) != 2 {
		t.Errorf("filter: %#v", got)
	}
	s.SetFilter("")
	if got := s.Items(); len(got) != 3 {
		t.Errorf("clear: %#v", got)
	}
}

func TestSelectListScrollIndicator(t *testing.T) {
	items := make([]SelectItem, 10)
	for i := range items {
		items[i] = SelectItem{Value: string(rune('a' + i))}
	}
	s := NewSelectList(items, 3, SelectListTheme{}, SelectListLayoutOptions{})
	s.SetSelectedIndex(0)
	out := s.Render(40)
	// 3 items + scroll info line.
	if len(out) != 4 {
		t.Errorf("expected 4 lines (3 items + scroll), got %d: %#v", len(out), out)
	}
	if !strings.Contains(out[3], "/10") {
		t.Errorf("scroll line: %q", out[3])
	}
}

func TestSelectListConfirm(t *testing.T) {
	got := SelectItem{}
	s := NewSelectList([]SelectItem{{Value: "x"}}, 5, SelectListTheme{}, SelectListLayoutOptions{})
	s.OnSelect = func(it SelectItem) { got = it }
	s.HandleInput("\r")
	if got.Value != "x" {
		t.Errorf("confirm: %#v", got)
	}
}

func TestBoxRenderCache(t *testing.T) {
	b := NewBox(2, 1, nil)
	b.AddChild(NewText("hi", 0, 0))
	first := b.Render(10)
	second := b.Render(10)
	if !stringSliceEq(first, second) {
		t.Errorf("cached output differs: %v vs %v", first, second)
	}
	// Width change should invalidate cache.
	wider := b.Render(15)
	if len(wider) == 0 {
		t.Error("width-change render empty")
	}
	// All lines respect the requested width.
	for _, line := range wider {
		if VisibleWidth(line) > 15 {
			t.Errorf("line %q exceeds width", line)
		}
	}
}

func TestSettingsListToggle(t *testing.T) {
	SetKeybindings(nil)
	s := &SettingsList{Items: []SettingItem{{Label: "x"}, {Label: "y"}}}
	s.HandleInput(" ")
	if !s.Items[0].Enabled {
		t.Error("space should toggle item 0")
	}
	s.HandleInput("\x1b[B")
	s.HandleInput(" ")
	if !s.Items[1].Enabled {
		t.Error("space should toggle item 1")
	}
}
