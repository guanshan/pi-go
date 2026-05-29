package tui

import (
	"strings"

	"github.com/rivo/uniseg"
)

// Input is a single-line, grapheme-aware text input component.
//
// It supports:
//   - Cursor movement by grapheme cluster and by word
//   - Backspace / delete-forward / delete-word / delete-to-line-start/end
//   - Emacs-style kill ring with yank / yank-pop
//   - Undo (Ctrl+_) with snapshot coalescing while typing
//   - Bracketed-paste content (newlines stripped)
//   - Optional autocomplete provider (UI rendering of suggestions is left to
//     the embedding layer)
//   - Horizontal viewport scroll when value exceeds available width
//   - Hardware cursor marker for IME positioning when focused
type Input struct {
	// Value is the current text content. Read-only from callers; mutate via
	// SetText / InsertTextAtCursor / HandleInput.
	Value string

	// Cursor is the byte index into Value where the cursor sits. It must
	// align to a grapheme-cluster boundary; out-of-range values are clamped.
	Cursor int

	OnSubmit func(string)
	OnChange func(string)

	// IsFocused reports whether the input currently has focus.
	IsFocused bool

	// History is an append-only history of submitted values, populated via
	// AddToHistory. The component does not currently implement up/down
	// recall; it's exposed for downstream UIs.
	History []string

	// PaddingX adds horizontal padding to the rendered output.
	PaddingX int

	// AutocompleteProvider is consulted when present; suggestion display is
	// the embedder's responsibility.
	AutocompleteProvider AutocompleteProvider

	// AutocompleteMaxVisible is the suggested cap on visible suggestions.
	AutocompleteMaxVisible int

	// internals
	killRing   KillRing
	undo       UndoStack[inputSnapshot]
	lastKill   bool       // last action was a kill (for accumulating into ring)
	lastTyped  bool       // last action was inserting a word character (for undo coalescing)
	lastAction editAction // tracks yank for yank-pop sequencing
	pasteBuf   string     // bracketed-paste content currently being assembled
	inPaste    bool
}

type inputSnapshot struct {
	Value  string
	Cursor int
}

// =============================================================================
// Public API
// =============================================================================

// SetFocused sets the focus flag.
func (i *Input) SetFocused(focused bool) { i.IsFocused = focused }

// Focused reports whether the input has focus.
func (i *Input) Focused() bool { return i.IsFocused }

// GetText returns the current value.
func (i *Input) GetText() string { return i.Value }

// SetText replaces the value and moves the cursor to the end. Emits OnChange.
func (i *Input) SetText(text string) {
	i.Value = text
	i.Cursor = len(text)
	i.lastKill = false
	i.lastTyped = false
	i.emitChange()
}

// SetOnSubmit installs the submit callback.
func (i *Input) SetOnSubmit(cb func(string)) { i.OnSubmit = cb }

// SetOnChange installs the change callback.
func (i *Input) SetOnChange(cb func(string)) { i.OnChange = cb }

// AddToHistory appends text to the input's history.
func (i *Input) AddToHistory(text string) { i.History = append(i.History, text) }

// InsertTextAtCursor inserts text at the current cursor position. Emits
// OnChange but does NOT push undo (use during paste, programmatic edits).
func (i *Input) InsertTextAtCursor(text string) {
	if text == "" {
		return
	}
	i.clampCursor()
	i.Value = i.Value[:i.Cursor] + text + i.Value[i.Cursor:]
	i.Cursor += len(text)
	i.emitChange()
}

// GetExpandedText returns Value (no expansion is performed at this layer).
func (i *Input) GetExpandedText() string { return i.Value }

// SetAutocompleteProvider installs / replaces the autocomplete provider.
func (i *Input) SetAutocompleteProvider(provider AutocompleteProvider) {
	i.AutocompleteProvider = provider
}

// SetPaddingX configures left/right padding (clamped to >= 0).
func (i *Input) SetPaddingX(padding int) {
	if padding < 0 {
		padding = 0
	}
	i.PaddingX = padding
}

// SetAutocompleteMaxVisible configures the suggestion cap (clamped to >= 0).
func (i *Input) SetAutocompleteMaxVisible(maxVisible int) {
	if maxVisible < 0 {
		maxVisible = 0
	}
	i.AutocompleteMaxVisible = maxVisible
}

// Invalidate is currently a no-op (no cached render state).
func (i *Input) Invalidate() {}

// =============================================================================
// HandleInput
// =============================================================================

// HandleInput dispatches a single input sequence (already split by
// StdinBuffer or equivalent) to the appropriate editor action.
func (i *Input) HandleInput(data string) {
	// Bracketed paste handling: assemble into pasteBuf and only emit once
	// the closing marker arrives.
	if strings.Contains(data, "\x1b[200~") {
		i.inPaste = true
		i.pasteBuf = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}
	if i.inPaste {
		i.pasteBuf += data
		if end := strings.Index(i.pasteBuf, "\x1b[201~"); end >= 0 {
			content := i.pasteBuf[:end]
			rest := i.pasteBuf[end+len("\x1b[201~"):]
			i.inPaste = false
			i.pasteBuf = ""
			i.handlePaste(content)
			if rest != "" {
				i.HandleInput(rest)
			}
		}
		return
	}

	kb := GetKeybindings()

	// Submit / cancel (cancel currently has no callback; mirrors upstream).
	switch {
	case kb.Matches(data, "tui.input.submit"), data == "\n":
		if i.OnSubmit != nil {
			i.OnSubmit(i.Value)
		}
		return
	case kb.Matches(data, "tui.editor.undo"):
		i.undoOnce()
		return
	case kb.Matches(data, "tui.editor.deleteCharBackward"):
		i.deleteCharBackward()
		return
	case kb.Matches(data, "tui.editor.deleteCharForward"):
		i.deleteCharForward()
		return
	case kb.Matches(data, "tui.editor.deleteWordBackward"):
		i.deleteWordBackward()
		return
	case kb.Matches(data, "tui.editor.deleteWordForward"):
		i.deleteWordForward()
		return
	case kb.Matches(data, "tui.editor.deleteToLineStart"):
		i.deleteToLineStart()
		return
	case kb.Matches(data, "tui.editor.deleteToLineEnd"):
		i.deleteToLineEnd()
		return
	case kb.Matches(data, "tui.editor.yank"):
		i.yank()
		return
	case kb.Matches(data, "tui.editor.yankPop"):
		i.yankPop()
		return
	case kb.Matches(data, "tui.editor.cursorLeft"):
		i.cursorLeft()
		return
	case kb.Matches(data, "tui.editor.cursorRight"):
		i.cursorRight()
		return
	case kb.Matches(data, "tui.editor.cursorLineStart"):
		i.lastKill = false
		i.lastTyped = false
		i.Cursor = 0
		return
	case kb.Matches(data, "tui.editor.cursorLineEnd"):
		i.lastKill = false
		i.lastTyped = false
		i.Cursor = len(i.Value)
		return
	case kb.Matches(data, "tui.editor.cursorWordLeft"):
		i.lastKill = false
		i.lastTyped = false
		i.Cursor = FindWordBackward(i.Value, i.Cursor)
		return
	case kb.Matches(data, "tui.editor.cursorWordRight"):
		i.lastKill = false
		i.lastTyped = false
		i.Cursor = FindWordForward(i.Value, i.Cursor)
		return
	}

	// Kitty CSI-u may carry a printable character.
	if printable := DecodeKittyPrintable(data); printable != "" {
		i.insertCharacter(printable)
		return
	}
	// Reject control characters; insert anything else verbatim.
	if hasControlChars(data) {
		return
	}
	i.insertCharacter(data)
}

// =============================================================================
// Render
// =============================================================================

// Render produces a single line for the given width. When focused, an inline
// reverse-video cursor is shown and a CURSOR_MARKER is emitted at the cursor
// position so a host renderer can position the hardware cursor for IME.
func (i *Input) Render(width int) []string {
	if width <= 0 {
		return []string{""}
	}
	inner := width - i.PaddingX*2
	if inner < 1 {
		inner = 1
	}
	value := i.Value
	cursor := i.Cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}

	prompt := "> "
	available := inner - VisibleWidth(prompt)
	if available < 1 {
		available = 1
	}

	totalWidth := VisibleWidth(value)
	cursorCol := VisibleWidth(value[:cursor])
	startCol := 0
	if totalWidth >= available {
		half := available / 2
		switch {
		case cursorCol < half:
			startCol = 0
		case cursorCol > totalWidth-half:
			startCol = totalWidth - available
			if startCol < 0 {
				startCol = 0
			}
		default:
			startCol = cursorCol - half
			if startCol < 0 {
				startCol = 0
			}
		}
	}
	visible := SliceByColumn(value, startCol, available, true)
	visibleCursor := cursorCol - startCol
	if visibleCursor < 0 {
		visibleCursor = 0
	}
	if visibleCursor > VisibleWidth(visible) {
		visibleCursor = VisibleWidth(visible)
	}

	// Split visible at cursor column.
	beforeCursor := SliceByColumn(visible, 0, visibleCursor, true)
	afterCursor := tailFromColumn(visible, visibleCursor)

	// Cursor character: first grapheme of afterCursor, or a space if at end.
	atCursor := " "
	rest := afterCursor
	gr := uniseg.NewGraphemes(afterCursor)
	if gr.Next() {
		atCursor = gr.Str()
		rest = afterCursor[len(atCursor):]
	}

	var b strings.Builder
	if i.PaddingX > 0 {
		b.WriteString(strings.Repeat(" ", i.PaddingX))
	}
	b.WriteString(prompt)
	b.WriteString(beforeCursor)
	if i.IsFocused {
		b.WriteString(CursorMarker)
		b.WriteString("\x1b[7m")
		b.WriteString(atCursor)
		b.WriteString("\x1b[27m")
	} else {
		b.WriteString(atCursor)
	}
	b.WriteString(rest)
	if i.PaddingX > 0 {
		b.WriteString(strings.Repeat(" ", i.PaddingX))
	}
	out := b.String()
	if VisibleWidth(out) > width {
		out = TruncateToWidth(out, width, "")
	}
	return []string{out}
}

// =============================================================================
// Editing operations
// =============================================================================

func (i *Input) insertCharacter(s string) {
	if s == "" {
		return
	}
	if !i.lastTyped || isWhitespaceString(s) {
		i.pushUndo()
	}
	i.lastTyped = true
	i.lastKill = false
	i.clampCursor()
	i.Value = i.Value[:i.Cursor] + s + i.Value[i.Cursor:]
	i.Cursor += len(s)
	i.emitChange()
}

func (i *Input) deleteCharBackward() {
	i.lastKill = false
	i.lastTyped = false
	if i.Cursor <= 0 {
		return
	}
	i.pushUndo()
	prev := previousGraphemeBoundary(i.Value, i.Cursor)
	i.Value = i.Value[:prev] + i.Value[i.Cursor:]
	i.Cursor = prev
	i.emitChange()
}

func (i *Input) deleteCharForward() {
	i.lastKill = false
	i.lastTyped = false
	if i.Cursor >= len(i.Value) {
		return
	}
	i.pushUndo()
	next := nextGraphemeBoundary(i.Value, i.Cursor)
	i.Value = i.Value[:i.Cursor] + i.Value[next:]
	i.emitChange()
}

func (i *Input) deleteWordBackward() {
	if i.Cursor <= 0 {
		return
	}
	wasKill := i.lastKill
	i.pushUndo()
	deleteFrom := FindWordBackward(i.Value, i.Cursor)
	deleted := i.Value[deleteFrom:i.Cursor]
	i.killRing.Push(deleted, KillRingPushOptions{Prepend: true, Accumulate: wasKill})
	i.lastKill = true
	i.lastTyped = false
	i.Value = i.Value[:deleteFrom] + i.Value[i.Cursor:]
	i.Cursor = deleteFrom
	i.emitChange()
}

func (i *Input) deleteWordForward() {
	if i.Cursor >= len(i.Value) {
		return
	}
	wasKill := i.lastKill
	i.pushUndo()
	deleteTo := FindWordForward(i.Value, i.Cursor)
	deleted := i.Value[i.Cursor:deleteTo]
	i.killRing.Push(deleted, KillRingPushOptions{Prepend: false, Accumulate: wasKill})
	i.lastKill = true
	i.lastTyped = false
	i.Value = i.Value[:i.Cursor] + i.Value[deleteTo:]
	i.emitChange()
}

func (i *Input) deleteToLineStart() {
	if i.Cursor <= 0 {
		return
	}
	wasKill := i.lastKill
	i.pushUndo()
	deleted := i.Value[:i.Cursor]
	i.killRing.Push(deleted, KillRingPushOptions{Prepend: true, Accumulate: wasKill})
	i.lastKill = true
	i.lastTyped = false
	i.Value = i.Value[i.Cursor:]
	i.Cursor = 0
	i.emitChange()
}

func (i *Input) deleteToLineEnd() {
	if i.Cursor >= len(i.Value) {
		return
	}
	wasKill := i.lastKill
	i.pushUndo()
	deleted := i.Value[i.Cursor:]
	i.killRing.Push(deleted, KillRingPushOptions{Prepend: false, Accumulate: wasKill})
	i.lastKill = true
	i.lastTyped = false
	i.Value = i.Value[:i.Cursor]
	i.emitChange()
}

func (i *Input) yank() {
	text, ok := i.killRing.Peek()
	if !ok || text == "" {
		return
	}
	i.pushUndo()
	i.lastKill = false
	i.lastTyped = false
	i.Value = i.Value[:i.Cursor] + text + i.Value[i.Cursor:]
	i.Cursor += len(text)
	i.emitChange()
	i.lastKill = false
	// Mark this action so yankPop can run.
	i.lastAction = actionYank
}

func (i *Input) yankPop() {
	if i.lastAction != actionYank || i.killRing.Len() <= 1 {
		return
	}
	prev, _ := i.killRing.Peek()
	i.pushUndo()
	// Remove the previously yanked text (it sits just before the cursor).
	if i.Cursor >= len(prev) && i.Value[i.Cursor-len(prev):i.Cursor] == prev {
		i.Value = i.Value[:i.Cursor-len(prev)] + i.Value[i.Cursor:]
		i.Cursor -= len(prev)
	}
	i.killRing.Rotate()
	text, _ := i.killRing.Peek()
	i.Value = i.Value[:i.Cursor] + text + i.Value[i.Cursor:]
	i.Cursor += len(text)
	i.emitChange()
	i.lastAction = actionYank
}

// editAction tracks the most recent editing action for yank-pop sequencing.
type editAction int

const (
	actionNone editAction = iota
	actionYank
)

func (i *Input) cursorLeft() {
	i.lastKill = false
	i.lastTyped = false
	if i.Cursor <= 0 {
		return
	}
	i.Cursor = previousGraphemeBoundary(i.Value, i.Cursor)
}

func (i *Input) cursorRight() {
	i.lastKill = false
	i.lastTyped = false
	if i.Cursor >= len(i.Value) {
		return
	}
	i.Cursor = nextGraphemeBoundary(i.Value, i.Cursor)
}

func (i *Input) handlePaste(content string) {
	i.lastKill = false
	i.lastTyped = false
	i.pushUndo()
	clean := strings.NewReplacer("\r\n", "", "\r", "", "\n", "", "\t", "    ").Replace(content)
	i.Value = i.Value[:i.Cursor] + clean + i.Value[i.Cursor:]
	i.Cursor += len(clean)
	i.emitChange()
}

func (i *Input) pushUndo() {
	i.undo.Push(inputSnapshot{Value: i.Value, Cursor: i.Cursor})
}

func (i *Input) undoOnce() {
	snap, ok := i.undo.Pop()
	if !ok {
		return
	}
	i.Value = snap.Value
	i.Cursor = snap.Cursor
	i.lastKill = false
	i.lastTyped = false
	i.emitChange()
}

func (i *Input) clampCursor() {
	if i.Cursor < 0 {
		i.Cursor = 0
	}
	if i.Cursor > len(i.Value) {
		i.Cursor = len(i.Value)
	}
}

func (i *Input) emitChange() {
	if i.OnChange != nil {
		i.OnChange(i.Value)
	}
}

// =============================================================================
// Helpers
// =============================================================================

func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

func isWhitespaceString(s string) bool {
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
			continue
		}
		return false
	}
	return s != ""
}

// previousGraphemeBoundary returns the byte index of the start of the
// grapheme cluster immediately before pos.
func previousGraphemeBoundary(s string, pos int) int {
	if pos <= 0 || pos > len(s) {
		if pos > len(s) {
			return len(s)
		}
		return 0
	}
	// Walk forward, recording cluster boundaries up to pos.
	gr := uniseg.NewGraphemes(s[:pos])
	last := 0
	cur := 0
	for gr.Next() {
		last = cur
		cur += len(gr.Str())
	}
	return last
}

// nextGraphemeBoundary returns the byte index of the start of the grapheme
// cluster immediately after pos.
func nextGraphemeBoundary(s string, pos int) int {
	if pos >= len(s) {
		return len(s)
	}
	gr := uniseg.NewGraphemes(s[pos:])
	if gr.Next() {
		return pos + len(gr.Str())
	}
	return len(s)
}

// tailFromColumn returns the suffix of s starting at the given visible column.
func tailFromColumn(s string, column int) string {
	if column <= 0 {
		return s
	}
	w := VisibleWidth(s)
	if column >= w {
		return ""
	}
	return SliceByColumn(s, column, w-column, false)
}
