package tui

import "testing"

type staticModifierHelper map[ModifierKey]bool

func (s staticModifierHelper) IsModifierPressed(key ModifierKey) bool {
	return s[key]
}

type staticAutocomplete struct{}

func (staticAutocomplete) Suggest(input string, cursor int) AutocompleteSuggestions {
	return AutocompleteSuggestions{Items: []AutocompleteItem{{Label: input, Value: input}}}
}

func TestInputEditorComponentSurface(t *testing.T) {
	var editor EditorComponent = &Input{}
	var changed string
	var submitted string
	editor.SetOnChange(func(text string) { changed = text })
	editor.SetOnSubmit(func(text string) { submitted = text })
	editor.SetText("ab")
	editor.InsertTextAtCursor("c")
	if editor.GetText() != "abc" || editor.GetExpandedText() != "abc" || changed != "abc" {
		t.Fatalf("text=%q expanded=%q changed=%q", editor.GetText(), editor.GetExpandedText(), changed)
	}
	editor.AddToHistory("abc")
	editor.SetPaddingX(2)
	editor.SetAutocompleteProvider(staticAutocomplete{})
	editor.SetAutocompleteMaxVisible(3)
	editor.HandleInput("\n")
	if submitted != "abc" {
		t.Fatalf("submitted=%q", submitted)
	}
	lines := editor.Render(8)
	if len(lines) != 1 || VisibleWidth(lines[0]) > 8 {
		t.Fatalf("lines=%#v", lines)
	}
	input := editor.(*Input)
	if len(input.History) != 1 || input.PaddingX != 2 || input.AutocompleteMaxVisible != 3 || input.AutocompleteProvider == nil {
		t.Fatalf("input=%#v", input)
	}
}

func TestNativeModifiersHelper(t *testing.T) {
	SetNativeModifiersHelper(staticModifierHelper{ModifierCommand: true})
	defer SetNativeModifiersHelper(nil)
	if !IsNativeModifierPressed(ModifierCommand) {
		t.Fatal("expected command modifier")
	}
	if IsNativeModifierPressed("invalid") {
		t.Fatal("invalid modifier should be false")
	}
}
