package tui

type EditorComponent interface {
	Component
	GetText() string
	SetText(string)
	HandleInput(string)
	SetOnSubmit(func(string))
	SetOnChange(func(string))
	AddToHistory(string)
	InsertTextAtCursor(string)
	GetExpandedText() string
	SetAutocompleteProvider(AutocompleteProvider)
	SetPaddingX(int)
	SetAutocompleteMaxVisible(int)
}
