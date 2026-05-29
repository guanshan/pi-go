package tui

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"
)

func TestTextWrapAndContainerRender(t *testing.T) {
	c := &Container{}
	c.AddChild(NewText("hello world from tui", 0, 0))
	lines := c.Render(20)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	if VisibleWidth("你好") != 4 {
		t.Fatal("wide width mismatch")
	}
}

func TestInputSettingsAndBuffer(t *testing.T) {
	input := &Input{}
	input.HandleInput("a")
	input.HandleInput("b")
	if input.Value != "ab" {
		t.Fatalf("input=%q", input.Value)
	}
	list := &SettingsList{Items: []SettingItem{{Label: "x"}}}
	list.HandleInput(" ")
	if !list.Items[0].Enabled {
		t.Fatal("setting did not toggle")
	}
	var pasted string
	buffer := &StdinBuffer{OnPaste: func(s string) { pasted = s }}
	buffer.Process("\x1b[200~hello\x1b[201~")
	if pasted != "hello" {
		t.Fatalf("paste=%q", pasted)
	}
	var ring KillRing
	ring.Push("a", KillRingPushOptions{})
	ring.Push("b", KillRingPushOptions{})
	if v, _ := ring.Peek(); v != "b" {
		t.Fatal("kill ring peek b")
	}
	ring.Rotate()
	if v, _ := ring.Peek(); v != "a" {
		t.Fatal("kill ring rotate to a")
	}
}

func TestTerminalImageAndKeybindings(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	dim, err := GetPngDimensions(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if dim.Width != 2 || dim.Height != 3 {
		t.Fatalf("dim=%#v", dim)
	}
	manager := NewKeybindingsManager(KeybindingDefinitions{
		"quit": {DefaultKeys: []KeyID{Ctrl("c")}},
	}, nil)
	if !manager.Matches("\x03", "quit") {
		t.Fatal("keybinding mismatch")
	}
}

func TestProcessTerminalLifecycleSequences(t *testing.T) {
	var out bytes.Buffer
	term := NewProcessTerminalWithWriter(&out)
	term.Start(nil, nil)
	term.SetKittyProtocolActive(true)
	term.SetProgress(true)
	term.DrainInput(time.Nanosecond, time.Nanosecond)
	if term.KittyProtocolActive() {
		t.Fatal("kitty protocol should be inactive after drain")
	}
	term.SetProgress(false)
	term.Stop()
	output := out.String()
	for _, seq := range []string{
		"\x1b[?2004h",
		"\x1b[?u",
		"\x1b[>4;2m",
		"\x1b[<u",
		terminalProgressActive,
		terminalProgressClear,
		"\x1b[?2004l",
		"\x1b[>4;0m",
	} {
		if !strings.Contains(output, seq) {
			t.Fatalf("missing terminal sequence %q in %q", seq, output)
		}
	}
}

func TestTerminalAppleInputAndWriteLogPath(t *testing.T) {
	if NormalizeAppleTerminalInput("\r", true, true) != appleShiftEnterSequence {
		t.Fatal("shift-enter should normalize for Apple Terminal")
	}
	if NormalizeAppleTerminalInput("\r", true, false) != "\r" {
		t.Fatal("plain enter should not normalize")
	}
	if !IsAppleTerminalSessionFor("darwin", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}) {
		t.Fatal("expected Apple Terminal detection")
	}
	if IsAppleTerminalSessionFor("linux", map[string]string{"TERM_PROGRAM": "Apple_Terminal"}) {
		t.Fatal("Apple Terminal detection should be darwin-only")
	}
	dir := t.TempDir()
	path := terminalWriteLogPath(dir, time.Date(2026, 5, 27, 1, 2, 3, 0, time.UTC))
	if !strings.Contains(path, "tui-2026-05-27_01-02-03-") || !strings.HasPrefix(path, dir) {
		t.Fatalf("write log path=%q", path)
	}
	if terminalWriteLogPath("/tmp/pi-tui.log", time.Time{}) != "/tmp/pi-tui.log" {
		t.Fatal("file write log path should be used as-is")
	}
}
