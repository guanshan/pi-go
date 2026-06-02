package tui

import (
	"strings"
	"testing"
)

func TestVisibleWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 5},
		{"hello world", 11},
		{"你好", 4},
		{"\x1b[31mred\x1b[0m", 3},
		{"\x1b[1;31mhello\x1b[0m", 5},
		{"a\tb", 1 + 4 + 1},
		{"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", 4},
		{"👨‍👩‍👧", 2}, // family ZWJ sequence: single grapheme cluster, width 2
		{"🇨", 2},     // regional-indicator singleton is rendered as an emoji cell
		{"é", 1},     // combining acute on e: width 1
		{"\x07", 0},  // BEL
	}
	for _, c := range cases {
		got := VisibleWidth(c.in)
		if got != c.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestRegionalIndicatorAndEmojiWidths(t *testing.T) {
	for r := rune(0x1F1E6); r <= rune(0x1F1FF); r++ {
		if got := VisibleWidth(string(r)); got != 2 {
			t.Fatalf("regional indicator U+%04X width=%d, want 2", r, got)
		}
	}
	for _, sample := range []string{"🇺🇸", "👍", "👍🏻", "✅", "⚡", "⚡️", "👨", "👨‍💻", "🏳️‍🌈"} {
		if got := VisibleWidth(sample); got != 2 {
			t.Fatalf("VisibleWidth(%q)=%d, want 2", sample, got)
		}
	}

	lines := WrapTextWithANSI("1234567🇨", 8)
	if len(lines) != 2 {
		t.Fatalf("wrapped regional indicator lines=%#v", lines)
	}
	if VisibleWidth(lines[0]) != 7 || VisibleWidth(lines[1]) != 2 {
		t.Fatalf("wrapped regional indicator widths=%d/%d lines=%#v", VisibleWidth(lines[0]), VisibleWidth(lines[1]), lines)
	}
}

func TestTruncateToWidth(t *testing.T) {
	cases := []struct {
		in     string
		width  int
		suffix string
		want   string
	}{
		{"hello", 5, "...", "hello"},
		{"hello world", 8, "...", "hello..."},
		{"hello", 0, "...", ""},
		{"你好世界", 4, "", "你好"},
		{"\x1b[31mhello world\x1b[0m", 8, "...", "\x1b[31mhello..."},
		{"abc", 2, ".", "a."},
		{"abc", 1, "..", "a"}, // suffix wider than target → drop suffix
	}
	for _, c := range cases {
		got := TruncateToWidth(c.in, c.width, c.suffix)
		if got != c.want {
			t.Errorf("TruncateToWidth(%q, %d, %q) = %q, want %q", c.in, c.width, c.suffix, got, c.want)
		}
	}
}

func TestWrapTextWithANSI(t *testing.T) {
	got := WrapTextWithANSI("hello world from go", 10)
	if len(got) == 0 || strings.Join(got, "|") != "hello|world from|go" {
		t.Errorf("WrapTextWithANSI hello world: %#v", got)
	}
	// Long word is hard-wrapped.
	got = WrapTextWithANSI("supercalifragilistic", 5)
	for _, line := range got {
		if VisibleWidth(line) > 5 {
			t.Errorf("hard-wrap: line %q has width %d > 5", line, VisibleWidth(line))
		}
	}
	// ANSI codes should not break wrapping.
	got = WrapTextWithANSI("\x1b[31mred bold text here\x1b[0m", 8)
	for _, line := range got {
		if VisibleWidth(line) > 8 {
			t.Errorf("ANSI wrap: line %q has width %d > 8", line, VisibleWidth(line))
		}
	}
}

func TestSliceByColumn(t *testing.T) {
	s := "hello world"
	if got := SliceByColumn(s, 0, 5, false); got != "hello" {
		t.Errorf("[0,5)=%q", got)
	}
	if got := SliceByColumn(s, 6, 5, false); got != "world" {
		t.Errorf("[6,11)=%q", got)
	}
	if got := SliceByColumn("你好世界", 0, 4, true); got != "你好" {
		t.Errorf("wide [0,4)=%q", got)
	}
	if got := SliceByColumn("你好世界", 0, 3, true); got != "你" {
		t.Errorf("strict wide [0,3)=%q", got)
	}
	// ANSI codes preserved when slicing past their position.
	colored := "\x1b[31mhello\x1b[0m world"
	got := SliceByColumn(colored, 0, 5, false)
	if !strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "hello") {
		t.Errorf("ANSI preserved: %q", got)
	}
}

func TestNormalizeTerminalOutput(t *testing.T) {
	// Plain ASCII unchanged.
	if got := NormalizeTerminalOutput("hello"); got != "hello" {
		t.Errorf("plain: %q", got)
	}
	// Thai SARA AM should be split into ํ + า.
	got := NormalizeTerminalOutput("กำ")
	if !strings.Contains(got, "ก") || strings.ContainsRune(got, 'ำ') {
		t.Errorf("Thai SARA AM: %q (runes=%v)", got, []rune(got))
	}
}

func TestIsWhitespaceAndPunctuation(t *testing.T) {
	if !IsWhitespaceChar(" ") || !IsWhitespaceChar("\t") || IsWhitespaceChar("a") {
		t.Errorf("whitespace classification wrong")
	}
	if !IsPunctuationChar(".") || !IsPunctuationChar(",") || IsPunctuationChar("a") {
		t.Errorf("punctuation classification wrong")
	}
}

func TestApplyBackgroundToLine(t *testing.T) {
	bg := func(s string) string { return "[" + s + "]" }
	got := ApplyBackgroundToLine("hi", 5, bg)
	if got != "[hi   ]" {
		t.Errorf("got %q", got)
	}
	if got := ApplyBackgroundToLine("hi", 5, nil); got != "hi" {
		t.Errorf("nil bg: %q", got)
	}
}

func TestExtractAnsiCode(t *testing.T) {
	cases := []struct {
		in   string
		pos  int
		want string
	}{
		{"\x1b[31m", 0, "\x1b[31m"},
		{"hello\x1b[0m", 5, "\x1b[0m"},
		{"\x1b]8;;https://x\x1b\\", 0, "\x1b]8;;https://x\x1b\\"},
		{"abc", 0, ""},
	}
	for _, c := range cases {
		got, _ := ExtractAnsiCode(c.in, c.pos)
		if got != c.want {
			t.Errorf("ExtractAnsiCode(%q, %d) = %q, want %q", c.in, c.pos, got, c.want)
		}
	}
}

func TestStripAnsi(t *testing.T) {
	if got := StripAnsi("\x1b[31mred\x1b[0m"); got != "red" {
		t.Errorf("strip: %q", got)
	}
	if got := StripAnsi("plain"); got != "plain" {
		t.Errorf("plain: %q", got)
	}
}
