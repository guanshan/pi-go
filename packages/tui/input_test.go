package tui

import (
	"strings"
	"testing"
)

func newInput() *Input {
	SetKeybindings(nil)
	return &Input{}
}

func TestInputBasicTyping(t *testing.T) {
	i := newInput()
	i.HandleInput("h")
	i.HandleInput("i")
	if i.Value != "hi" {
		t.Errorf("value: %q", i.Value)
	}
	if i.Cursor != 2 {
		t.Errorf("cursor: %d", i.Cursor)
	}
}

func TestInputBackspace(t *testing.T) {
	i := newInput()
	i.SetText("hello")
	if i.Cursor != 5 {
		t.Errorf("cursor: %d", i.Cursor)
	}
	i.HandleInput("\x7f") // backspace
	if i.Value != "hell" || i.Cursor != 4 {
		t.Errorf("after bs: %q %d", i.Value, i.Cursor)
	}
}

func TestInputArrowKeys(t *testing.T) {
	i := newInput()
	i.SetText("abc")
	i.HandleInput("\x1b[D") // left
	if i.Cursor != 2 {
		t.Errorf("after left: %d", i.Cursor)
	}
	i.HandleInput("\x1b[D")
	i.HandleInput("\x1b[D")
	if i.Cursor != 0 {
		t.Errorf("after 3 lefts: %d", i.Cursor)
	}
	i.HandleInput("\x1b[C") // right
	if i.Cursor != 1 {
		t.Errorf("after right: %d", i.Cursor)
	}
}

func TestInputWordNav(t *testing.T) {
	i := newInput()
	i.SetText("hello world")
	// ctrl+left → word back: should land at start of "world"
	i.HandleInput("\x1b[1;5D")
	if i.Cursor != 6 {
		t.Errorf("ctrl+left: cursor=%d (want 6)", i.Cursor)
	}
	i.HandleInput("\x1b[1;5C") // ctrl+right
	if i.Cursor != 11 {
		t.Errorf("ctrl+right: cursor=%d (want 11)", i.Cursor)
	}
}

func TestInputDeleteWordBackward(t *testing.T) {
	i := newInput()
	i.SetText("hello world")
	i.HandleInput("\x17") // ctrl+w
	if i.Value != "hello " {
		t.Errorf("ctrl+w: value=%q", i.Value)
	}
	if i.Cursor != 6 {
		t.Errorf("ctrl+w: cursor=%d", i.Cursor)
	}
}

func TestInputUndo(t *testing.T) {
	i := newInput()
	i.HandleInput("h")
	i.HandleInput(" ") // whitespace creates a snapshot before insertion
	i.HandleInput("w")
	// ctrl+_ undo: drops the typing-burst since the snapshot before " ".
	i.HandleInput("\x1f")
	if i.Value == "h w" {
		t.Errorf("undo did nothing: %q", i.Value)
	}
}

func TestInputKillYank(t *testing.T) {
	i := newInput()
	i.SetText("hello")
	i.Cursor = 0
	i.HandleInput("\x0b") // ctrl+k → kill to line end
	if i.Value != "" {
		t.Errorf("after kill: %q", i.Value)
	}
	i.HandleInput("\x19") // ctrl+y → yank
	if i.Value != "hello" {
		t.Errorf("after yank: %q", i.Value)
	}
}

func TestInputSubmit(t *testing.T) {
	i := newInput()
	var got string
	i.SetOnSubmit(func(s string) { got = s })
	i.SetText("hi")
	i.HandleInput("\r")
	if got != "hi" {
		t.Errorf("submit: %q", got)
	}
}

func TestInputBracketedPaste(t *testing.T) {
	i := newInput()
	i.HandleInput("\x1b[200~hello\nworld\x1b[201~")
	if i.Value != "helloworld" {
		t.Errorf("paste: %q", i.Value)
	}
}

func TestInputUTF8(t *testing.T) {
	i := newInput()
	i.HandleInput("你")
	i.HandleInput("好")
	if i.Value != "你好" {
		t.Errorf("UTF8: %q", i.Value)
	}
	if i.Cursor != len("你好") {
		t.Errorf("cursor: %d", i.Cursor)
	}
	// Backspace should remove one Chinese character (one grapheme).
	i.HandleInput("\x7f")
	if i.Value != "你" {
		t.Errorf("after bs: %q", i.Value)
	}
}

func TestInputRender(t *testing.T) {
	i := newInput()
	i.SetText("hi")
	i.SetFocused(true)
	lines := i.Render(20)
	if len(lines) != 1 {
		t.Fatalf("lines: %d", len(lines))
	}
	if !strings.Contains(lines[0], "hi") {
		t.Errorf("render: %q", lines[0])
	}
	if !strings.Contains(lines[0], CursorMarker) {
		t.Errorf("render missing cursor marker: %q", lines[0])
	}
}
