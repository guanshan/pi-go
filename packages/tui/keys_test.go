package tui

import (
	"strings"
	"testing"
)

// =============================================================================
// MatchesKey — table-driven coverage for the upstream branches.
// =============================================================================

type matchCase struct {
	name string
	data string
	key  KeyID
	want bool
}

func runMatchTable(t *testing.T, cases []matchCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MatchesKey(c.data, c.key); got != c.want {
				t.Errorf("MatchesKey(%q, %q) = %v, want %v", c.data, c.key, got, c.want)
			}
		})
	}
}

func TestMatchesKeyLegacyArrows(t *testing.T) {
	runMatchTable(t, []matchCase{
		{"up_csi", "\x1b[A", KeyUp, true},
		{"down_csi", "\x1b[B", KeyDown, true},
		{"right_csi", "\x1b[C", KeyRight, true},
		{"left_csi", "\x1b[D", KeyLeft, true},
		{"up_ss3", "\x1bOA", KeyUp, true},
		{"down_ss3", "\x1bOB", KeyDown, true},
		{"mismatched", "\x1b[A", KeyDown, false},
		{"ctrl_left", "\x1b[1;5D", Ctrl(KeyLeft), true},
		{"ctrl_right", "\x1b[1;5C", Ctrl(KeyRight), true},
		{"alt_up", "\x1b[1;3A", Alt(KeyUp), true},
		{"alt_down", "\x1b[1;3B", Alt(KeyDown), true},
		{"shift_up_legacy", "\x1b[a", Shift(KeyUp), true},
		{"shift_down_legacy", "\x1b[b", Shift(KeyDown), true},
		{"alt_left_legacy", "\x1bb", Alt(KeyLeft), true},
		{"alt_right_legacy", "\x1bf", Alt(KeyRight), true},
	})
}

func TestMatchesKeyEnterEscapeTab(t *testing.T) {
	runMatchTable(t, []matchCase{
		{"plain_cr", "\r", KeyEnter, true},
		{"plain_lf", "\n", KeyEnter, true},
		{"return_alias", "\r", KeyReturn, true},
		{"escape_raw", "\x1b", KeyEscape, true},
		{"esc_alias", "\x1b", KeyEsc, true},
		{"tab_raw", "\t", KeyTab, true},
		{"shift_tab", "\x1b[Z", Shift(KeyTab), true},
		{"backspace_127", "\x7f", KeyBackspace, true},
		{"alt_backspace", "\x1b\x7f", Alt(KeyBackspace), true},
		{"alt_backspace_b", "\x1b\b", Alt(KeyBackspace), true},
		{"ctrl_space", "\x00", Ctrl(KeySpace), true},
		{"alt_space_legacy", "\x1b ", Alt(KeySpace), true},
		{"space_plain", " ", KeySpace, true},
	})
}

func TestMatchesKeyCtrlLetters(t *testing.T) {
	runMatchTable(t, []matchCase{
		{"ctrl_a", "\x01", Ctrl("a"), true},
		{"ctrl_c", "\x03", Ctrl("c"), true},
		{"ctrl_z", "\x1a", Ctrl("z"), true},
		// Raw 0x08 (Ctrl+H) overlaps with backspace on legacy terminals; on
		// non-Windows-Terminal sessions ctrl+h still matches \x08.
		{"ctrl_h_via_raw", "\x08", Ctrl("h"), true},
	})
}

func TestMatchesKeyAltLetters(t *testing.T) {
	runMatchTable(t, []matchCase{
		{"alt_a", "\x1ba", Alt("a"), true},
		{"alt_z", "\x1bz", Alt("z"), true},
		{"alt_5", "\x1b5", Alt("5"), true},
	})
}

func TestMatchesKeyKittyCSIu(t *testing.T) {
	runMatchTable(t, []matchCase{
		{"plain_a_csi_u", "\x1b[97u", "a", true},
		{"ctrl_a_csi_u", "\x1b[97;5u", Ctrl("a"), true},
		{"shift_a_csi_u", "\x1b[97;2u", Shift("a"), true},
		{"alt_a_csi_u", "\x1b[97;3u", Alt("a"), true},
		{"ctrl_shift_p", "\x1b[112;6u", CtrlShift("p"), true},
		{"escape_csi_u", "\x1b[27u", KeyEscape, true},
		{"enter_csi_u", "\x1b[13u", KeyEnter, true},
	})
}

func TestMatchesKeyFunctional(t *testing.T) {
	runMatchTable(t, []matchCase{
		{"delete", "\x1b[3~", KeyDelete, true},
		{"insert", "\x1b[2~", KeyInsert, true},
		{"pageup", "\x1b[5~", KeyPageUp, true},
		{"pagedown", "\x1b[6~", KeyPageDown, true},
		{"home_csi", "\x1b[H", KeyHome, true},
		{"home_ss3", "\x1bOH", KeyHome, true},
		{"end_csi", "\x1b[F", KeyEnd, true},
		{"end_ss3", "\x1bOF", KeyEnd, true},
		{"f1", "\x1bOP", KeyF1, true},
		{"f2", "\x1bOQ", KeyF2, true},
		{"f5", "\x1b[15~", KeyF5, true},
		{"f12", "\x1b[24~", KeyF12, true},
	})
}

func TestMatchesKeyFunctionalWithModifier(t *testing.T) {
	runMatchTable(t, []matchCase{
		{"shift_pageup", "\x1b[5$", Shift(KeyPageUp), true},
		{"ctrl_pageup", "\x1b[5^", Ctrl(KeyPageUp), true},
		{"shift_home", "\x1b[7$", Shift(KeyHome), true},
		{"ctrl_home", "\x1b[7^", Ctrl(KeyHome), true},
	})
}

func TestMatchesKeyCtrlSymbols(t *testing.T) {
	runMatchTable(t, []matchCase{
		// ctrl+[ shares the byte 0x1b with raw ESC; matchesPrintableKey's
		// rawCtrl path returns true.
		{"ctrl_lbracket_via_raw", "\x1b", Ctrl("["), true},
		{"ctrl_backslash", "\x1c", Ctrl("\\"), true},
		{"ctrl_rbracket", "\x1d", Ctrl("]"), true},
		{"ctrl_underscore", "\x1f", Ctrl("_"), true},
	})
}

func TestParseKeyComprehensive(t *testing.T) {
	cases := []struct {
		data string
		want string
	}{
		{"\x1b", "escape"},
		{"\r", "enter"},
		{"\n", "enter"},
		{"\t", "tab"},
		{"\x7f", "backspace"},
		{" ", "space"},
		{"\x00", "ctrl+space"},
		{"\x03", "ctrl+c"},
		{"\x01", "ctrl+a"},
		{"\x1a", "ctrl+z"},
		{"\x1b[A", "up"},
		{"\x1b[B", "down"},
		{"\x1b[C", "right"},
		{"\x1b[D", "left"},
		{"\x1bOA", "up"},
		{"\x1b[Z", "shift+tab"},
		{"\x1b[3~", "delete"},
		{"\x1b[5~", "pageUp"},
		{"\x1b[6~", "pageDown"},
		{"\x1b[H", "home"},
		{"\x1b[F", "end"},
		{"\x1b[1;5D", "ctrl+left"},
		{"\x1b[1;5C", "ctrl+right"},
		{"\x1b[1;3D", "alt+left"},
		{"\x1b[a", "shift+up"},
		{"\x1bOa", "ctrl+up"},
		{"\x1ba", "alt+a"},
		{"\x1b5", "alt+5"},
		{"\x1c", "ctrl+\\"},
		{"\x1d", "ctrl+]"},
		{"\x1f", "ctrl+-"},
		{"\x1b\x7f", "alt+backspace"},
		{"\x1b\b", "alt+backspace"},
		{"a", "a"},
		{"Z", "Z"},
		{"5", "5"},
	}
	for _, c := range cases {
		t.Run(strings.ReplaceAll(c.want, "+", "_"), func(t *testing.T) {
			if got := ParseKey(c.data); got != c.want {
				t.Errorf("ParseKey(%q) = %q, want %q", c.data, got, c.want)
			}
		})
	}
}

func TestParseKeyKittyCSIu(t *testing.T) {
	cases := []struct {
		data string
		want string
	}{
		{"\x1b[97u", "a"},
		{"\x1b[97;5u", "ctrl+a"},
		{"\x1b[97;3u", "alt+a"},
		{"\x1b[97;2u", "shift+a"},
		{"\x1b[27u", "escape"},
		{"\x1b[13u", "enter"},
	}
	for _, c := range cases {
		if got := ParseKey(c.data); got != c.want {
			t.Errorf("ParseKey(%q) = %q, want %q", c.data, got, c.want)
		}
	}
}

func TestIsKeyReleaseRepeat(t *testing.T) {
	cases := []struct {
		data    string
		release bool
		repeat  bool
	}{
		{"\x1b[97;1:3u", true, false},
		{"\x1b[97;1:2u", false, true},
		{"\x1b[97;1:1u", false, false},
		{"\x1b[200~hello\x1b[201~", false, false}, // paste content immune
	}
	for _, c := range cases {
		if got := IsKeyRelease(c.data); got != c.release {
			t.Errorf("IsKeyRelease(%q) = %v want %v", c.data, got, c.release)
		}
		if got := IsKeyRepeat(c.data); got != c.repeat {
			t.Errorf("IsKeyRepeat(%q) = %v want %v", c.data, got, c.repeat)
		}
	}
}

func TestDecodeKittyPrintable(t *testing.T) {
	cases := []struct {
		data string
		want string
	}{
		{"\x1b[97u", "a"},
		{"\x1b[97:65;2u", "A"}, // shift gives shifted-key field
		{"\x1b[97;5u", ""},     // ctrl rejected
		{"\x1b[97;3u", ""},     // alt rejected
		{"\x1b[97;9u", ""},     // super rejected
		{"\x1b[1u", ""},        // codepoint < 32
		{"plain", ""},
	}
	for _, c := range cases {
		if got := DecodeKittyPrintable(c.data); got != c.want {
			t.Errorf("DecodeKittyPrintable(%q) = %q, want %q", c.data, got, c.want)
		}
	}
}

func TestDecodePrintableKey(t *testing.T) {
	if got := DecodePrintableKey("\x1b[97u"); got != "a" {
		t.Errorf("kitty path: %q", got)
	}
	// modifyOtherKeys printable.
	if got := DecodePrintableKey("\x1b[27;1;65~"); got != "A" {
		t.Errorf("modifyOtherKeys A: %q", got)
	}
	// Ctrl rejected via modifyOtherKeys.
	if got := DecodePrintableKey("\x1b[27;5;97~"); got != "" {
		t.Errorf("ctrl+a via modifyOtherKeys should not decode: %q", got)
	}
}

func TestKittyProtocolToggle(t *testing.T) {
	SetKittyProtocolActive(false)
	if !MatchesKey("\n", KeyEnter) {
		t.Error("plain LF should match enter when kitty inactive")
	}
	SetKittyProtocolActive(true)
	defer SetKittyProtocolActive(false)
	// With kitty active, \r\n style "shift+enter" is recognised.
	if !MatchesKey("\x1b\r", Shift(KeyEnter)) {
		t.Error("kitty mode shift+enter via \\x1b\\r")
	}
}

func TestModifyOtherKeysSequences(t *testing.T) {
	runMatchTable(t, []matchCase{
		{"shift_enter_modify", "\x1b[27;2;13~", Shift(KeyEnter), true},
		{"alt_enter_modify", "\x1b[27;3;13~", Alt(KeyEnter), true},
	})
}

func TestKeyHelpers(t *testing.T) {
	if Ctrl("c") != "ctrl+c" {
		t.Errorf("Ctrl: %q", Ctrl("c"))
	}
	if Shift(KeyEnter) != "shift+enter" {
		t.Errorf("Shift: %q", Shift(KeyEnter))
	}
	if CtrlShift("p") != "ctrl+shift+p" {
		t.Errorf("CtrlShift: %q", CtrlShift("p"))
	}
	if CtrlAlt("x") != "ctrl+alt+x" {
		t.Errorf("CtrlAlt: %q", CtrlAlt("x"))
	}
	if ShiftAlt("a") != "shift+alt+a" {
		t.Errorf("ShiftAlt: %q", ShiftAlt("a"))
	}
}
