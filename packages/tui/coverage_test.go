package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Loader / CancellableLoader
// =============================================================================

func TestLoaderRender(t *testing.T) {
	l := NewLoader("Working", LoaderIndicatorOptions{}, nil)
	out := l.Render(20)
	if len(out) != 1 || !strings.Contains(out[0], "Working") {
		t.Errorf("Render: %#v", out)
	}
}

func TestLoaderEmptyFramesShowsLabelOnly(t *testing.T) {
	l := NewLoader("Hi", LoaderIndicatorOptions{Frames: []string{}}, nil)
	out := l.Render(20)
	if len(out) != 1 || out[0] != "Hi" {
		t.Errorf("empty frames: %#v", out)
	}
}

func TestLoaderDefaultFramesIsCopy(t *testing.T) {
	a := DefaultLoaderFrames()
	b := DefaultLoaderFrames()
	if &a[0] == &b[0] {
		t.Error("DefaultLoaderFrames returned shared slice")
	}
}

func TestLoaderStartStopAdvancesFrames(t *testing.T) {
	notified := atomic.Int32{}
	l := NewLoader("Hi", LoaderIndicatorOptions{
		Frames:   []string{"a", "b", "c"},
		Interval: 5 * time.Millisecond,
	}, func() { notified.Add(1) })
	l.Start()
	defer l.Stop()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if notified.Load() >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("notify not called enough times: %d", notified.Load())
}

func TestLoaderSetLabelTriggersNotify(t *testing.T) {
	called := false
	l := NewLoader("a", LoaderIndicatorOptions{}, func() { called = true })
	l.SetLabel("b")
	if !called {
		t.Error("notify not called on SetLabel")
	}
	if l.Label() != "b" {
		t.Errorf("label: %q", l.Label())
	}
}

func TestLoaderSetIndicator(t *testing.T) {
	l := NewLoader("Hi", LoaderIndicatorOptions{}, nil)
	l.SetIndicator(LoaderIndicatorOptions{Frames: []string{"."}})
	out := l.Render(20)
	if !strings.Contains(out[0], ".") {
		t.Errorf("custom frame: %q", out[0])
	}
}

func TestCancellableLoaderCtrlC(t *testing.T) {
	called := false
	l := NewCancellableLoader("X", LoaderIndicatorOptions{}, nil)
	l.OnCancel = func() { called = true }
	l.HandleInput("\x03") // ctrl+c
	if !called {
		t.Error("OnCancel not invoked on ctrl+c")
	}
	if !l.Cancelled {
		t.Error("Cancelled flag not set")
	}
	// Calling again should not double-fire.
	called = false
	l.HandleInput("\x03")
	if called {
		t.Error("OnCancel invoked twice")
	}
}

func TestCancellableLoaderEsc(t *testing.T) {
	called := false
	l := NewCancellableLoader("X", LoaderIndicatorOptions{}, nil)
	l.OnCancel = func() { called = true }
	l.HandleInput("\x1b")
	if !called {
		t.Error("Esc should cancel")
	}
}

// =============================================================================
// Spacer / TruncatedText / Image / Text
// =============================================================================

func TestSpacerRender(t *testing.T) {
	if got := (&Spacer{Height: 0}).Render(10); got != nil {
		t.Errorf("zero height: %#v", got)
	}
	got := (&Spacer{Height: 3}).Render(10)
	if len(got) != 3 {
		t.Errorf("got %#v", got)
	}
}

func TestTruncatedTextRender(t *testing.T) {
	got := (&TruncatedText{Text: "hello world"}).Render(8)
	if len(got) != 1 || VisibleWidth(got[0]) > 8 {
		t.Errorf("got %#v", got)
	}
}

func TestImageRender(t *testing.T) {
	got := (&Image{AltText: "logo", Width: 32, Height: 16}).Render(40)
	if len(got) != 1 || !strings.Contains(got[0], "logo") {
		t.Errorf("got %#v", got)
	}
	got = (&Image{}).Render(40)
	if !strings.Contains(got[0], "image") {
		t.Errorf("default alt: %q", got[0])
	}
}

func TestTextNewAndRender(t *testing.T) {
	tx := NewText("hello world", 1, 1)
	out := tx.Render(20)
	// Two padding lines + at least one content line.
	if len(out) < 3 {
		t.Errorf("padded text: %#v", out)
	}
	tx.SetText("changed")
	out = tx.Render(20)
	if !strings.Contains(strings.Join(out, " "), "changed") {
		t.Errorf("SetText: %#v", out)
	}
}

// =============================================================================
// Container / Box
// =============================================================================

func TestContainerAddRemoveClear(t *testing.T) {
	c := &Container{}
	a := &Spacer{Height: 1}
	b := &Spacer{Height: 1}
	c.AddChild(a)
	c.AddChild(b)
	if len(c.Children) != 2 {
		t.Errorf("add: %d", len(c.Children))
	}
	c.RemoveChild(a)
	if len(c.Children) != 1 || c.Children[0] != b {
		t.Errorf("remove: %#v", c.Children)
	}
	c.Clear()
	if len(c.Children) != 0 {
		t.Error("clear")
	}
}

func TestBoxInvalidateAndSetBgFn(t *testing.T) {
	b := NewBox(0, 0, nil)
	b.AddChild(NewText("hi", 0, 0))
	first := b.Render(10)
	b.Invalidate()
	second := b.Render(10)
	if !stringSliceEq(first, second) {
		t.Errorf("after invalidate render differs: %v vs %v", first, second)
	}
	b.SetBgFn(func(s string) string { return "[" + s + "]" })
	out := b.Render(10)
	if len(out) == 0 || !strings.Contains(out[0], "[") {
		t.Errorf("SetBgFn: %#v", out)
	}
}

// =============================================================================
// Input — extra branches
// =============================================================================

func TestInputDeleteCharForward(t *testing.T) {
	SetKeybindings(nil)
	i := newInput()
	i.SetText("abc")
	i.Cursor = 1
	i.HandleInput("\x04") // ctrl+d → tui.editor.deleteCharForward
	if i.Value != "ac" {
		t.Errorf("delete forward: %q", i.Value)
	}
}

func TestInputDeleteToLineStart(t *testing.T) {
	SetKeybindings(nil)
	i := newInput()
	i.SetText("hello world")
	i.Cursor = 6
	i.HandleInput("\x15") // ctrl+u
	if i.Value != "world" {
		t.Errorf("delete to start: %q", i.Value)
	}
}

func TestInputDeleteWordForward(t *testing.T) {
	SetKeybindings(nil)
	i := newInput()
	i.SetText("hello world")
	i.Cursor = 0
	i.HandleInput("\x1bd") // alt+d
	if i.Value != " world" {
		t.Errorf("delete word fwd: %q", i.Value)
	}
}

func TestInputYankPopCycles(t *testing.T) {
	SetKeybindings(nil)
	i := newInput()
	// kill two distinct words.
	i.SetText("foo")
	i.Cursor = 0
	i.HandleInput("\x0b") // ctrl+k → kill from 0 → ring=["foo"]
	if i.Value != "" {
		t.Fatalf("after first kill: %q", i.Value)
	}
	// Set up a second value and kill from start again.
	i.SetText("bar")
	i.Cursor = 0
	i.HandleInput("\x0b") // ctrl+k → ring=["foo","bar"]
	// Yank latest.
	i.HandleInput("\x19")
	if i.Value != "bar" {
		t.Fatalf("yank: %q", i.Value)
	}
	i.HandleInput("\x1by") // alt+y → yank-pop
	if i.Value == "bar" {
		t.Errorf("yank-pop should change content")
	}
}

func TestInputFocusedAccessor(t *testing.T) {
	i := newInput()
	if i.Focused() {
		t.Error("default focused")
	}
	i.SetFocused(true)
	if !i.Focused() {
		t.Error("after SetFocused")
	}
}

func TestInputInvalidate(t *testing.T) {
	i := newInput()
	i.Invalidate() // no-op, must not panic
}

// =============================================================================
// SelectList — setItems
// =============================================================================

func TestSelectListSetItems(t *testing.T) {
	s := NewSelectList(nil, 5, SelectListTheme{}, SelectListLayoutOptions{})
	s.SetItems([]SelectItem{{Value: "a"}, {Value: "b"}})
	if len(s.Items()) != 2 {
		t.Errorf("setitems: %#v", s.Items())
	}
}

// =============================================================================
// Keybindings — extra accessors
// =============================================================================

func TestKeybindingsAccessors(t *testing.T) {
	m := NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{
		"tui.input.submit": {Ctrl("j")},
	})
	if got := m.Keys("tui.input.submit"); len(got) != 1 || got[0] != Ctrl("j") {
		t.Errorf("Keys: %#v", got)
	}
	def := m.Definition("tui.input.submit")
	if def.Description == "" {
		t.Error("Definition")
	}
	user := m.UserBindings()
	if _, ok := user["tui.input.submit"]; !ok {
		t.Error("UserBindings missing override")
	}
	resolved := m.Resolved()
	if _, ok := resolved["tui.editor.cursorUp"]; !ok {
		t.Error("Resolved missing default")
	}
	m.SetUserBindings(KeybindingsConfig{})
	if !m.Matches("\r", "tui.input.submit") {
		t.Error("after clear user bindings, default should match")
	}
}

// =============================================================================
// keys.go helpers
// =============================================================================

func TestSuperHelper(t *testing.T) {
	if Super(KeyEnter) != "super+enter" {
		t.Errorf("Super: %q", Super(KeyEnter))
	}
}

// =============================================================================
// Terminal — Columns / Rows / cursor / clear
// =============================================================================

func TestProcessTerminalSizeFallback(t *testing.T) {
	term := NewProcessTerminalWithWriter(&strings.Builder{})
	// In a non-tty test we expect the env / 80x24 fallback.
	if term.Columns() <= 0 {
		t.Error("Columns fallback")
	}
	if term.Rows() <= 0 {
		t.Error("Rows fallback")
	}
}

func TestProcessTerminalCursorAndClear(t *testing.T) {
	var b strings.Builder
	term := NewProcessTerminalWithWriter(&b)
	term.HideCursor()
	term.ShowCursor()
	term.ClearLine()
	term.ClearFromCursor()
	term.ClearScreen()
	term.MoveBy(2)
	term.MoveBy(-1)
	term.SetTitle("x")
	out := b.String()
	for _, want := range []string{"\x1b[?25l", "\x1b[?25h", "\x1b[K", "\x1b[J", "\x1b[2J", "\x1b[H", "\x1b]0;x\a"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestProcessTerminalNewProcessTerminal(t *testing.T) {
	if NewProcessTerminal() == nil {
		t.Error("NewProcessTerminal nil")
	}
}

// =============================================================================
// Markdown — fallback rendering for HTML / paragraph
// =============================================================================

func TestMarkdownHTMLBlock(t *testing.T) {
	src := "<div>hello</div>"
	out := strings.Join((&Markdown{Text: src}).Render(40), "\n")
	if !strings.Contains(out, "<div>hello</div>") {
		t.Errorf("html block lost: %q", out)
	}
}

// =============================================================================
// Word navigation Options
// =============================================================================

func TestFindWordBackwardWithSegment(t *testing.T) {
	calls := 0
	custom := func(text string) []string {
		calls++
		return []string{"abc", " ", "def"}[:len([]string{"abc", " ", "def"})]
	}
	pos := FindWordBackward("abc def", 7, WordNavigationOptions{Segment: custom})
	if calls == 0 {
		t.Error("custom segmenter not used")
	}
	if pos == 7 {
		t.Errorf("did not move: %d", pos)
	}
}

func TestFindWordBackwardAtomic(t *testing.T) {
	atomic := "[paste #1]"
	text := "hello " + atomic + " world"
	cursor := len(text) - len(" world")
	pos := FindWordBackward(text, cursor, WordNavigationOptions{
		Segment: func(s string) []string {
			// Treat the marker as a single cluster.
			out := []string{}
			i := 0
			for i < len(s) {
				if strings.HasPrefix(s[i:], atomic) {
					out = append(out, atomic)
					i += len(atomic)
					continue
				}
				out = append(out, string(s[i]))
				i++
			}
			return out
		},
		IsAtomic: func(c string) bool { return c == atomic },
	})
	// Should land at start of the atomic segment (after "hello ").
	if pos != len("hello ") {
		t.Errorf("atomic backward: %d (want %d)", pos, len("hello "))
	}
}

// =============================================================================
// Capability detection — additional terminals
// =============================================================================

func TestDetectCapabilitiesAppleTerminal(t *testing.T) {
	c := DetectCapabilitiesFromEnv(map[string]string{"TERM_PROGRAM": "Apple_Terminal"})
	if c.TrueColor || c.Hyperlinks {
		t.Errorf("Apple Terminal: %#v", c)
	}
}

func TestDetectCapabilitiesHyper(t *testing.T) {
	c := DetectCapabilitiesFromEnv(map[string]string{"TERM_PROGRAM": "Hyper"})
	if !c.TrueColor || !c.Hyperlinks {
		t.Errorf("Hyper: %#v", c)
	}
}

func TestDetectCapabilitiesKonsole(t *testing.T) {
	c := DetectCapabilitiesFromEnv(map[string]string{"KONSOLE_VERSION": "210400"})
	if !c.TrueColor || !c.Hyperlinks {
		t.Errorf("Konsole: %#v", c)
	}
}

func TestDetectCapabilitiesJediTerm(t *testing.T) {
	c := DetectCapabilitiesFromEnv(map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm"})
	if !c.TrueColor || c.Hyperlinks {
		t.Errorf("JediTerm: %#v", c)
	}
}

func TestDetectCapabilitiesMultiplexer(t *testing.T) {
	c := DetectCapabilitiesFromEnv(map[string]string{"TMUX": "1", "COLORTERM": "truecolor"})
	if !c.TrueColor || c.Hyperlinks || c.Images != "" {
		t.Errorf("tmux: %#v", c)
	}
}

// =============================================================================
// Native modifiers helper
// =============================================================================

type fakeMods struct {
	pressed map[ModifierKey]bool
}

func (f fakeMods) IsModifierPressed(key ModifierKey) bool { return f.pressed[key] }

func TestNativeModifiersHelperAccessor(t *testing.T) {
	defer SetNativeModifiersHelper(nil)
	SetNativeModifiersHelper(fakeMods{pressed: map[ModifierKey]bool{ModifierShift: true}})
	if !IsNativeModifierPressed(ModifierShift) {
		t.Error("shift")
	}
	if IsNativeModifierPressed(ModifierCommand) {
		t.Error("command false")
	}
	// Unknown modifier is always false.
	if IsNativeModifierPressed("garbage") {
		t.Error("unknown")
	}
}

func TestNativeModifiersNoHelper(t *testing.T) {
	SetNativeModifiersHelper(nil)
	if IsNativeModifierPressed(ModifierShift) {
		t.Error("no helper")
	}
}

// =============================================================================
// utils — extra public wrappers
// =============================================================================

func TestSliceWithWidthExtractSegments(t *testing.T) {
	text, w := SliceWithWidth("hello world", 0, 5, false)
	if text != "hello" || w != 5 {
		t.Errorf("slice: %q w=%d", text, w)
	}
	seg := ExtractSegments("hello world", 5, 6, 5, false)
	if seg.Before != "hello" {
		t.Errorf("before: %q", seg.Before)
	}
	if seg.After != "world" {
		t.Errorf("after: %q", seg.After)
	}
}

// =============================================================================
// Autocomplete fd path — exercised via cancelled context (ensure no panic).
// =============================================================================

func TestAutocompleteFdGracefulFailure(t *testing.T) {
	// fd may or may not be installed; either way Suggest should not panic.
	resetFdAvailableCache()
	defer resetFdAvailableCache()
	p := PathAutocompleteProvider{FdTimeout: time.Millisecond}
	got := p.Suggest("./", 2)
	_ = got
}

func TestRegexEscape(t *testing.T) {
	if got := regexEscape("a.b"); got != "a\\.b" {
		t.Errorf("escape: %q", got)
	}
	if got := regexEscape(""); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestItoa(t *testing.T) {
	if itoa(0) != "0" || itoa(42) != "42" || itoa(-7) != "-7" {
		t.Error("itoa")
	}
}

// =============================================================================
// Misc helpers ensuring the static-analysis-quiet code paths compile.
// =============================================================================

func TestContextCompiles(t *testing.T) {
	// Just touch context to keep import live in case loader test removes
	// notify-based path. Using context here is intentional.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx
}
