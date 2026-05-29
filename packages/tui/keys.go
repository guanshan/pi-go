// Package tui — keyboard input parsing & matching.
//
// Mirrors @earendil-works/pi-tui's keys.ts. Supports legacy CSI sequences,
// Kitty keyboard protocol (CSI-u with disambiguation, event types, alternate
// keys), and xterm modifyOtherKeys (CSI 27;mod;keycode~).

package tui

import (
	"os"
	"strings"
	"sync/atomic"
	"unicode"
)

// KeyID identifies a logical key, optionally prefixed with modifiers joined by
// "+", e.g. "escape", "ctrl+c", "shift+ctrl+p", "alt+enter", "f5".
type KeyID string

// Modifier bit masks used internally. Mirrors upstream MODIFIERS.
const (
	modShift    = 1
	modAlt      = 2
	modCtrl     = 4
	modSuper    = 8
	modLockMask = 64 + 128 // CapsLock | NumLock
)

// Codepoints used to represent specific keys when matching. Negative values
// represent functional / arrow keys that have no Unicode codepoint.
const (
	cpEscape    = 27
	cpTab       = 9
	cpEnter     = 13
	cpSpace     = 32
	cpBackspace = 127
	cpKpEnter   = 57414

	cpArrowUp    = -1
	cpArrowDown  = -2
	cpArrowRight = -3
	cpArrowLeft  = -4

	cpDelete   = -10
	cpInsert   = -11
	cpPageUp   = -12
	cpPageDown = -13
	cpHome     = -14
	cpEnd      = -15
)

// kittyFunctionalEquivalents maps Kitty's high-codepoint function-key codes to
// their canonical equivalents (digits, symbols, arrows, functional keys).
var kittyFunctionalEquivalents = map[int]int{
	57399: 48, // KP_0  → 0
	57400: 49, // KP_1  → 1
	57401: 50, // KP_2  → 2
	57402: 51, // KP_3  → 3
	57403: 52, // KP_4  → 4
	57404: 53, // KP_5  → 5
	57405: 54, // KP_6  → 6
	57406: 55, // KP_7  → 7
	57407: 56, // KP_8  → 8
	57408: 57, // KP_9  → 9
	57409: 46, // KP_DECIMAL  → .
	57410: 47, // KP_DIVIDE   → /
	57411: 42, // KP_MULTIPLY → *
	57412: 45, // KP_SUBTRACT → -
	57413: 43, // KP_ADD      → +
	57415: 61, // KP_EQUAL    → =
	57416: 44, // KP_SEPARATOR→ ,
	57417: cpArrowLeft,
	57418: cpArrowRight,
	57419: cpArrowUp,
	57420: cpArrowDown,
	57421: cpPageUp,
	57422: cpPageDown,
	57423: cpHome,
	57424: cpEnd,
	57425: cpInsert,
	57426: cpDelete,
}

func normalizeKittyFunctional(cp int) int {
	if v, ok := kittyFunctionalEquivalents[cp]; ok {
		return v
	}
	return cp
}

// normalizeShiftedLetterIdentity maps shifted uppercase ASCII letters back to
// lowercase so that Shift+A and Shift+a are treated identically when no
// dedicated shifted-key code is reported.
func normalizeShiftedLetterIdentity(cp, modifier int) int {
	effective := modifier & ^modLockMask
	if effective&modShift != 0 && cp >= 'A' && cp <= 'Z' {
		return cp + 32
	}
	return cp
}

// _kittyProtocolActive is set to 1 when Kitty keyboard protocol negotiation
// has succeeded.
var _kittyProtocolActive atomic.Bool

// SetKittyProtocolActive records whether Kitty keyboard protocol is in use.
func SetKittyProtocolActive(active bool) { _kittyProtocolActive.Store(active) }

// IsKittyProtocolActive reports whether Kitty keyboard protocol is active.
func IsKittyProtocolActive() bool { return _kittyProtocolActive.Load() }

func isWindowsTerminalSession() bool {
	return os.Getenv("WT_SESSION") != "" &&
		os.Getenv("SSH_CONNECTION") == "" &&
		os.Getenv("SSH_CLIENT") == "" &&
		os.Getenv("SSH_TTY") == ""
}

// =============================================================================
// Legacy escape-sequence tables
// =============================================================================

var legacyKeySequences = map[string][]string{
	"up":       {"\x1b[A", "\x1bOA"},
	"down":     {"\x1b[B", "\x1bOB"},
	"right":    {"\x1b[C", "\x1bOC"},
	"left":     {"\x1b[D", "\x1bOD"},
	"home":     {"\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~"},
	"end":      {"\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~"},
	"insert":   {"\x1b[2~"},
	"delete":   {"\x1b[3~"},
	"pageup":   {"\x1b[5~", "\x1b[[5~"},
	"pagedown": {"\x1b[6~", "\x1b[[6~"},
	"clear":    {"\x1b[E", "\x1bOE"},
	"f1":       {"\x1bOP", "\x1b[11~", "\x1b[[A"},
	"f2":       {"\x1bOQ", "\x1b[12~", "\x1b[[B"},
	"f3":       {"\x1bOR", "\x1b[13~", "\x1b[[C"},
	"f4":       {"\x1bOS", "\x1b[14~", "\x1b[[D"},
	"f5":       {"\x1b[15~", "\x1b[[E"},
	"f6":       {"\x1b[17~"},
	"f7":       {"\x1b[18~"},
	"f8":       {"\x1b[19~"},
	"f9":       {"\x1b[20~"},
	"f10":      {"\x1b[21~"},
	"f11":      {"\x1b[23~"},
	"f12":      {"\x1b[24~"},
}

var legacyShiftSequences = map[string][]string{
	"up":       {"\x1b[a"},
	"down":     {"\x1b[b"},
	"right":    {"\x1b[c"},
	"left":     {"\x1b[d"},
	"clear":    {"\x1b[e"},
	"insert":   {"\x1b[2$"},
	"delete":   {"\x1b[3$"},
	"pageup":   {"\x1b[5$"},
	"pagedown": {"\x1b[6$"},
	"home":     {"\x1b[7$"},
	"end":      {"\x1b[8$"},
}

var legacyCtrlSequences = map[string][]string{
	"up":       {"\x1bOa"},
	"down":     {"\x1bOb"},
	"right":    {"\x1bOc"},
	"left":     {"\x1bOd"},
	"clear":    {"\x1bOe"},
	"insert":   {"\x1b[2^"},
	"delete":   {"\x1b[3^"},
	"pageup":   {"\x1b[5^"},
	"pagedown": {"\x1b[6^"},
	"home":     {"\x1b[7^"},
	"end":      {"\x1b[8^"},
}

func matchesLegacyModifier(data, key string, modifier int) bool {
	switch modifier {
	case modShift:
		for _, seq := range legacyShiftSequences[key] {
			if data == seq {
				return true
			}
		}
	case modCtrl:
		for _, seq := range legacyCtrlSequences[key] {
			if data == seq {
				return true
			}
		}
	}
	return false
}

func matchesLegacy(data string, sequences []string) bool {
	for _, seq := range sequences {
		if data == seq {
			return true
		}
	}
	return false
}

// symbolKeys is the set of supported single-character symbol keys.
var symbolKeys = map[rune]bool{
	'`': true, '-': true, '=': true, '[': true, ']': true, '\\': true,
	';': true, '\'': true, ',': true, '.': true, '/': true,
	'!': true, '@': true, '#': true, '$': true, '%': true,
	'^': true, '&': true, '*': true, '(': true, ')': true,
	'_': true, '+': true, '|': true, '~': true, '{': true, '}': true,
	':': true, '<': true, '>': true, '?': true,
}

func isSymbolKey(r rune) bool { return symbolKeys[r] }

// rawCtrlChar returns the legacy control-character byte for a key, or "" if
// the key has no legacy ctrl mapping. Mirrors upstream rawCtrlChar:
//
//	letters a-z → 1-26
//	[ \\ ] _    → 27, 28, 29, 31
//	-           → 31 (US keyboards: same physical key as _)
func rawCtrlChar(key string) string {
	if key == "" {
		return ""
	}
	r := []rune(strings.ToLower(key))[0]
	if (r >= 'a' && r <= 'z') || r == '[' || r == '\\' || r == ']' || r == '_' {
		return string(rune(int(r) & 0x1f))
	}
	if r == '-' {
		return string(rune(31))
	}
	return ""
}

// =============================================================================
// Public Key constants & helpers
// =============================================================================

// Special key names. Use these as KeyID values directly.
const (
	KeyEscape    KeyID = "escape"
	KeyEsc       KeyID = "esc"
	KeyEnter     KeyID = "enter"
	KeyReturn    KeyID = "return"
	KeyTab       KeyID = "tab"
	KeySpace     KeyID = "space"
	KeyBackspace KeyID = "backspace"
	KeyDelete    KeyID = "delete"
	KeyInsert    KeyID = "insert"
	KeyClear     KeyID = "clear"
	KeyHome      KeyID = "home"
	KeyEnd       KeyID = "end"
	KeyPageUp    KeyID = "pageUp"
	KeyPageDown  KeyID = "pageDown"
	KeyUp        KeyID = "up"
	KeyDown      KeyID = "down"
	KeyLeft      KeyID = "left"
	KeyRight     KeyID = "right"
	KeyF1        KeyID = "f1"
	KeyF2        KeyID = "f2"
	KeyF3        KeyID = "f3"
	KeyF4        KeyID = "f4"
	KeyF5        KeyID = "f5"
	KeyF6        KeyID = "f6"
	KeyF7        KeyID = "f7"
	KeyF8        KeyID = "f8"
	KeyF9        KeyID = "f9"
	KeyF10       KeyID = "f10"
	KeyF11       KeyID = "f11"
	KeyF12       KeyID = "f12"
)

// Ctrl returns "ctrl+<base>".
func Ctrl(base KeyID) KeyID { return KeyID("ctrl+" + string(base)) }

// Shift returns "shift+<base>".
func Shift(base KeyID) KeyID { return KeyID("shift+" + string(base)) }

// Alt returns "alt+<base>".
func Alt(base KeyID) KeyID { return KeyID("alt+" + string(base)) }

// Super returns "super+<base>".
func Super(base KeyID) KeyID { return KeyID("super+" + string(base)) }

// CtrlShift returns "ctrl+shift+<base>".
func CtrlShift(base KeyID) KeyID { return KeyID("ctrl+shift+" + string(base)) }

// CtrlAlt returns "ctrl+alt+<base>".
func CtrlAlt(base KeyID) KeyID { return KeyID("ctrl+alt+" + string(base)) }

// ShiftAlt returns "shift+alt+<base>".
func ShiftAlt(base KeyID) KeyID { return KeyID("shift+alt+" + string(base)) }

// =============================================================================
// Parsed key id
// =============================================================================

// parsedKeyID represents a "<mods>+<key>" id.
type parsedKeyID struct {
	key   string
	ctrl  bool
	shift bool
	alt   bool
	supr  bool
}

func parseKeyID(keyID string) (parsedKeyID, bool) {
	parts := strings.Split(strings.ToLower(keyID), "+")
	if len(parts) == 0 {
		return parsedKeyID{}, false
	}
	key := parts[len(parts)-1]
	if key == "" {
		return parsedKeyID{}, false
	}
	pk := parsedKeyID{key: key}
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl", "control":
			pk.ctrl = true
		case "shift":
			pk.shift = true
		case "alt", "meta":
			pk.alt = true
		case "super", "cmd", "command":
			pk.supr = true
		}
	}
	return pk, true
}

// =============================================================================
// MatchesKey
// =============================================================================

// MatchesKey reports whether the input data matches the key identifier.
func MatchesKey(data string, keyID KeyID) bool {
	pk, ok := parseKeyID(string(keyID))
	if !ok {
		return false
	}
	modifier := 0
	if pk.shift {
		modifier |= modShift
	}
	if pk.alt {
		modifier |= modAlt
	}
	if pk.ctrl {
		modifier |= modCtrl
	}
	if pk.supr {
		modifier |= modSuper
	}

	switch pk.key {
	case "escape", "esc":
		if modifier != 0 {
			return false
		}
		return data == "\x1b" ||
			matchesKittySequence(data, cpEscape, 0) ||
			matchesModifyOtherKeys(data, cpEscape, 0)

	case "space":
		if !IsKittyProtocolActive() {
			if modifier == modCtrl && data == "\x00" {
				return true
			}
			if modifier == modAlt && data == "\x1b " {
				return true
			}
		}
		if modifier == 0 {
			return data == " " ||
				matchesKittySequence(data, cpSpace, 0) ||
				matchesModifyOtherKeys(data, cpSpace, 0)
		}
		return matchesKittySequence(data, cpSpace, modifier) ||
			matchesModifyOtherKeys(data, cpSpace, modifier)

	case "tab":
		if modifier == modShift {
			return data == "\x1b[Z" ||
				matchesKittySequence(data, cpTab, modShift) ||
				matchesModifyOtherKeys(data, cpTab, modShift)
		}
		if modifier == 0 {
			return data == "\t" || matchesKittySequence(data, cpTab, 0)
		}
		return matchesKittySequence(data, cpTab, modifier) ||
			matchesModifyOtherKeys(data, cpTab, modifier)

	case "enter", "return":
		if modifier == modShift {
			if matchesKittySequence(data, cpEnter, modShift) ||
				matchesKittySequence(data, cpKpEnter, modShift) ||
				matchesModifyOtherKeys(data, cpEnter, modShift) {
				return true
			}
			if IsKittyProtocolActive() {
				return data == "\x1b\r" || data == "\n"
			}
			return false
		}
		if modifier == modAlt {
			if matchesKittySequence(data, cpEnter, modAlt) ||
				matchesKittySequence(data, cpKpEnter, modAlt) ||
				matchesModifyOtherKeys(data, cpEnter, modAlt) {
				return true
			}
			if !IsKittyProtocolActive() {
				return data == "\x1b\r"
			}
			return false
		}
		if modifier == 0 {
			return data == "\r" ||
				(!IsKittyProtocolActive() && data == "\n") ||
				data == "\x1bOM" ||
				matchesKittySequence(data, cpEnter, 0) ||
				matchesKittySequence(data, cpKpEnter, 0)
		}
		return matchesKittySequence(data, cpEnter, modifier) ||
			matchesKittySequence(data, cpKpEnter, modifier) ||
			matchesModifyOtherKeys(data, cpEnter, modifier)

	case "backspace":
		if modifier == modAlt {
			if data == "\x1b\x7f" || data == "\x1b\b" {
				return true
			}
			return matchesKittySequence(data, cpBackspace, modAlt) ||
				matchesModifyOtherKeys(data, cpBackspace, modAlt)
		}
		if modifier == modCtrl {
			if matchesRawBackspace(data, modCtrl) {
				return true
			}
			return matchesKittySequence(data, cpBackspace, modCtrl) ||
				matchesModifyOtherKeys(data, cpBackspace, modCtrl)
		}
		if modifier == 0 {
			return matchesRawBackspace(data, 0) ||
				matchesKittySequence(data, cpBackspace, 0) ||
				matchesModifyOtherKeys(data, cpBackspace, 0)
		}
		return matchesKittySequence(data, cpBackspace, modifier) ||
			matchesModifyOtherKeys(data, cpBackspace, modifier)

	case "insert", "delete", "clear", "home", "end", "pageup", "pagedown":
		return matchesFunctional(data, pk.key, modifier)

	case "up":
		if modifier == modAlt {
			return data == "\x1bp" || matchesKittySequence(data, cpArrowUp, modAlt)
		}
		if modifier == 0 {
			return matchesLegacy(data, legacyKeySequences["up"]) ||
				matchesKittySequence(data, cpArrowUp, 0)
		}
		if matchesLegacyModifier(data, "up", modifier) {
			return true
		}
		return matchesKittySequence(data, cpArrowUp, modifier)

	case "down":
		if modifier == modAlt {
			return data == "\x1bn" || matchesKittySequence(data, cpArrowDown, modAlt)
		}
		if modifier == 0 {
			return matchesLegacy(data, legacyKeySequences["down"]) ||
				matchesKittySequence(data, cpArrowDown, 0)
		}
		if matchesLegacyModifier(data, "down", modifier) {
			return true
		}
		return matchesKittySequence(data, cpArrowDown, modifier)

	case "left":
		if modifier == modAlt {
			return data == "\x1b[1;3D" ||
				(!IsKittyProtocolActive() && data == "\x1bB") ||
				data == "\x1bb" ||
				matchesKittySequence(data, cpArrowLeft, modAlt)
		}
		if modifier == modCtrl {
			return data == "\x1b[1;5D" ||
				matchesLegacyModifier(data, "left", modCtrl) ||
				matchesKittySequence(data, cpArrowLeft, modCtrl)
		}
		if modifier == 0 {
			return matchesLegacy(data, legacyKeySequences["left"]) ||
				matchesKittySequence(data, cpArrowLeft, 0)
		}
		if matchesLegacyModifier(data, "left", modifier) {
			return true
		}
		return matchesKittySequence(data, cpArrowLeft, modifier)

	case "right":
		if modifier == modAlt {
			return data == "\x1b[1;3C" ||
				(!IsKittyProtocolActive() && data == "\x1bF") ||
				data == "\x1bf" ||
				matchesKittySequence(data, cpArrowRight, modAlt)
		}
		if modifier == modCtrl {
			return data == "\x1b[1;5C" ||
				matchesLegacyModifier(data, "right", modCtrl) ||
				matchesKittySequence(data, cpArrowRight, modCtrl)
		}
		if modifier == 0 {
			return matchesLegacy(data, legacyKeySequences["right"]) ||
				matchesKittySequence(data, cpArrowRight, 0)
		}
		if matchesLegacyModifier(data, "right", modifier) {
			return true
		}
		return matchesKittySequence(data, cpArrowRight, modifier)

	case "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12":
		if modifier != 0 {
			return false
		}
		return matchesLegacy(data, legacyKeySequences[pk.key])
	}

	// Single letter / digit / symbol key.
	if len([]rune(pk.key)) == 1 {
		runeKey := []rune(pk.key)[0]
		if (runeKey >= 'a' && runeKey <= 'z') || (runeKey >= '0' && runeKey <= '9') || isSymbolKey(runeKey) {
			return matchesPrintableKey(data, pk.key, runeKey, modifier)
		}
	}
	return false
}

func matchesFunctional(data, key string, modifier int) bool {
	cp := 0
	switch key {
	case "insert":
		cp = cpInsert
	case "delete":
		cp = cpDelete
	case "home":
		cp = cpHome
	case "end":
		cp = cpEnd
	case "pageup":
		cp = cpPageUp
	case "pagedown":
		cp = cpPageDown
	case "clear":
		// Clear has no Kitty codepoint; only legacy.
		if modifier == 0 {
			return matchesLegacy(data, legacyKeySequences["clear"])
		}
		return matchesLegacyModifier(data, "clear", modifier)
	}
	if modifier == 0 {
		return matchesLegacy(data, legacyKeySequences[key]) ||
			matchesKittySequence(data, cp, 0)
	}
	if matchesLegacyModifier(data, key, modifier) {
		return true
	}
	return matchesKittySequence(data, cp, modifier)
}

func matchesPrintableKey(data, key string, r rune, modifier int) bool {
	codepoint := int(r)
	rawCtrl := rawCtrlChar(key)
	isLetter := r >= 'a' && r <= 'z'
	isDigit := r >= '0' && r <= '9'

	// Legacy ctrl+alt+letter: ESC + control character.
	if modifier == modCtrl|modAlt && !IsKittyProtocolActive() && rawCtrl != "" {
		if data == "\x1b"+rawCtrl {
			return true
		}
	}
	// Legacy alt+letter/digit: ESC + key.
	if modifier == modAlt && !IsKittyProtocolActive() && (isLetter || isDigit) {
		if data == "\x1b"+key {
			return true
		}
	}
	if modifier == modCtrl {
		if rawCtrl != "" && data == rawCtrl {
			return true
		}
		return matchesKittySequence(data, codepoint, modCtrl) ||
			matchesPrintableModifyOtherKeys(data, codepoint, modCtrl)
	}
	if modifier == modShift|modCtrl {
		return matchesKittySequence(data, codepoint, modifier) ||
			matchesPrintableModifyOtherKeys(data, codepoint, modifier)
	}
	if modifier == modShift {
		if isLetter && data == strings.ToUpper(key) {
			return true
		}
		return matchesKittySequence(data, codepoint, modShift) ||
			matchesPrintableModifyOtherKeys(data, codepoint, modShift)
	}
	if modifier != 0 {
		return matchesKittySequence(data, codepoint, modifier) ||
			matchesPrintableModifyOtherKeys(data, codepoint, modifier)
	}
	// Plain key.
	return data == key || matchesKittySequence(data, codepoint, 0)
}

// matchesRawBackspace handles the raw 0x7F / 0x08 ambiguity.
func matchesRawBackspace(data string, expectedModifier int) bool {
	if data == "\x7f" {
		return expectedModifier == 0
	}
	if data != "\x08" {
		return false
	}
	if isWindowsTerminalSession() {
		return expectedModifier == modCtrl
	}
	return expectedModifier == 0
}

// =============================================================================
// IsKeyRelease / IsKeyRepeat (Kitty event types)
// =============================================================================

// IsKeyRelease reports whether data is a Kitty release event. Bracketed paste
// content always reports false, even if it contains substrings like ":3F".
func IsKeyRelease(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, marker := range []string{":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F"} {
		if strings.Contains(data, marker) {
			return true
		}
	}
	return false
}

// IsKeyRepeat reports whether data is a Kitty repeat event.
func IsKeyRepeat(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, marker := range []string{":2u", ":2~", ":2A", ":2B", ":2C", ":2D", ":2H", ":2F"} {
		if strings.Contains(data, marker) {
			return true
		}
	}
	return false
}

// =============================================================================
// ParseKey
// =============================================================================

// ParseKey returns a canonical "ctrl+c"-style id for the given input data, or
// "" if the input is not recognized.
func ParseKey(data string) string {
	if k := parseKittySequence(data); k != nil {
		if id := formatParsedKey(k.codepoint, k.modifier, k.baseLayoutKey); id != "" {
			return id
		}
	}
	if mok := parseModifyOtherKeysSequence(data); mok != nil {
		if id := formatParsedKey(mok.codepoint, mok.modifier, 0); id != "" {
			return id
		}
	}

	if IsKittyProtocolActive() {
		if data == "\x1b\r" || data == "\n" {
			return "shift+enter"
		}
	}
	if id, ok := legacySequenceKeyIDs[data]; ok {
		return id
	}
	switch data {
	case "\x1b":
		return "escape"
	case "\x1c":
		return "ctrl+\\"
	case "\x1d":
		return "ctrl+]"
	case "\x1f":
		return "ctrl+-"
	case "\x1b\x1b":
		return "ctrl+alt+["
	case "\x1b\x1c":
		return "ctrl+alt+\\"
	case "\x1b\x1d":
		return "ctrl+alt+]"
	case "\x1b\x1f":
		return "ctrl+alt+-"
	case "\t":
		return "tab"
	case "\r":
		return "enter"
	case "\x00":
		return "ctrl+space"
	case " ":
		return "space"
	case "\x7f":
		return "backspace"
	case "\x08":
		if isWindowsTerminalSession() {
			return "ctrl+backspace"
		}
		return "backspace"
	case "\x1b[Z":
		return "shift+tab"
	}
	if !IsKittyProtocolActive() && data == "\n" {
		return "enter"
	}
	if !IsKittyProtocolActive() && data == "\x1b\r" {
		return "alt+enter"
	}
	if !IsKittyProtocolActive() && data == "\x1b " {
		return "alt+space"
	}
	if data == "\x1b\x7f" || data == "\x1b\b" {
		return "alt+backspace"
	}
	if !IsKittyProtocolActive() && data == "\x1bB" {
		return "alt+left"
	}
	if !IsKittyProtocolActive() && data == "\x1bF" {
		return "alt+right"
	}
	if !IsKittyProtocolActive() && len(data) == 2 && data[0] == '\x1b' {
		c := data[1]
		if c >= 1 && c <= 26 {
			return "ctrl+alt+" + string(rune(c)+96)
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return "alt+" + string(rune(c))
		}
	}
	switch data {
	case "\x1b[A":
		return "up"
	case "\x1b[B":
		return "down"
	case "\x1b[C":
		return "right"
	case "\x1b[D":
		return "left"
	case "\x1b[H", "\x1bOH":
		return "home"
	case "\x1b[F", "\x1bOF":
		return "end"
	case "\x1b[3~":
		return "delete"
	case "\x1b[5~":
		return "pageUp"
	case "\x1b[6~":
		return "pageDown"
	}
	if len(data) == 1 {
		c := data[0]
		if c >= 1 && c <= 26 {
			return "ctrl+" + string(rune(c)+96)
		}
		if c >= 32 && c <= 126 {
			return data
		}
	}
	return ""
}

func formatParsedKey(codepoint, modifier, baseLayoutKey int) string {
	normalized := normalizeKittyFunctional(codepoint)
	identity := normalizeShiftedLetterIdentity(normalized, modifier)

	isLatinLetter := identity >= 'a' && identity <= 'z'
	isDigit := identity >= '0' && identity <= '9'
	isSymbol := identity > 0 && identity < 128 && isSymbolKey(rune(identity))
	effective := identity
	if (!isLatinLetter && !isDigit && !isSymbol) && baseLayoutKey != 0 {
		effective = baseLayoutKey
	}

	keyName := ""
	switch {
	case effective == cpEscape:
		keyName = "escape"
	case effective == cpTab:
		keyName = "tab"
	case effective == cpEnter || effective == cpKpEnter:
		keyName = "enter"
	case effective == cpSpace:
		keyName = "space"
	case effective == cpBackspace:
		keyName = "backspace"
	case effective == cpDelete:
		keyName = "delete"
	case effective == cpInsert:
		keyName = "insert"
	case effective == cpHome:
		keyName = "home"
	case effective == cpEnd:
		keyName = "end"
	case effective == cpPageUp:
		keyName = "pageUp"
	case effective == cpPageDown:
		keyName = "pageDown"
	case effective == cpArrowUp:
		keyName = "up"
	case effective == cpArrowDown:
		keyName = "down"
	case effective == cpArrowLeft:
		keyName = "left"
	case effective == cpArrowRight:
		keyName = "right"
	case effective >= '0' && effective <= '9':
		keyName = string(rune(effective))
	case effective >= 'a' && effective <= 'z':
		keyName = string(rune(effective))
	default:
		if effective > 0 && effective < 128 && isSymbolKey(rune(effective)) {
			keyName = string(rune(effective))
		}
	}
	if keyName == "" {
		return ""
	}
	return formatKeyNameWithModifiers(keyName, modifier)
}

func formatKeyNameWithModifiers(keyName string, modifier int) string {
	supported := modShift | modCtrl | modAlt | modSuper
	effective := modifier & ^modLockMask
	if effective&^supported != 0 {
		return ""
	}
	var mods []string
	if effective&modShift != 0 {
		mods = append(mods, "shift")
	}
	if effective&modCtrl != 0 {
		mods = append(mods, "ctrl")
	}
	if effective&modAlt != 0 {
		mods = append(mods, "alt")
	}
	if effective&modSuper != 0 {
		mods = append(mods, "super")
	}
	if len(mods) == 0 {
		return keyName
	}
	return strings.Join(mods, "+") + "+" + keyName
}

// legacySequenceKeyIDs maps unambiguous legacy escape sequences to their
// canonical key id.
var legacySequenceKeyIDs = map[string]string{
	"\x1bOA":   "up",
	"\x1bOB":   "down",
	"\x1bOC":   "right",
	"\x1bOD":   "left",
	"\x1bOH":   "home",
	"\x1bOF":   "end",
	"\x1b[E":   "clear",
	"\x1bOE":   "clear",
	"\x1bOe":   "ctrl+clear",
	"\x1b[e":   "shift+clear",
	"\x1b[2~":  "insert",
	"\x1b[2$":  "shift+insert",
	"\x1b[2^":  "ctrl+insert",
	"\x1b[3$":  "shift+delete",
	"\x1b[3^":  "ctrl+delete",
	"\x1b[[5~": "pageUp",
	"\x1b[[6~": "pageDown",
	"\x1b[a":   "shift+up",
	"\x1b[b":   "shift+down",
	"\x1b[c":   "shift+right",
	"\x1b[d":   "shift+left",
	"\x1bOa":   "ctrl+up",
	"\x1bOb":   "ctrl+down",
	"\x1bOc":   "ctrl+right",
	"\x1bOd":   "ctrl+left",
	"\x1b[5$":  "shift+pageUp",
	"\x1b[6$":  "shift+pageDown",
	"\x1b[7$":  "shift+home",
	"\x1b[8$":  "shift+end",
	"\x1b[5^":  "ctrl+pageUp",
	"\x1b[6^":  "ctrl+pageDown",
	"\x1b[7^":  "ctrl+home",
	"\x1b[8^":  "ctrl+end",
	"\x1bOP":   "f1",
	"\x1bOQ":   "f2",
	"\x1bOR":   "f3",
	"\x1bOS":   "f4",
	"\x1b[11~": "f1",
	"\x1b[12~": "f2",
	"\x1b[13~": "f3",
	"\x1b[14~": "f4",
	"\x1b[[A":  "f1",
	"\x1b[[B":  "f2",
	"\x1b[[C":  "f3",
	"\x1b[[D":  "f4",
	"\x1b[[E":  "f5",
	"\x1b[15~": "f5",
	"\x1b[17~": "f6",
	"\x1b[18~": "f7",
	"\x1b[19~": "f8",
	"\x1b[20~": "f9",
	"\x1b[21~": "f10",
	"\x1b[23~": "f11",
	"\x1b[24~": "f12",
	"\x1bb":    "alt+left",
	"\x1bf":    "alt+right",
	"\x1bp":    "alt+up",
	"\x1bn":    "alt+down",
}

// =============================================================================
// Printable decoding
// =============================================================================

// DecodeKittyPrintable returns the printable character for a Kitty CSI-u
// sequence, or "" if data is not such a sequence or carries unsupported
// modifiers (Ctrl/Alt/Super).
func DecodeKittyPrintable(data string) string {
	k := parseKittySequence(data)
	if k == nil {
		return ""
	}
	modifier := k.modifier & ^modLockMask
	if modifier&(modAlt|modCtrl|modSuper) != 0 {
		return ""
	}
	cp := k.codepoint
	if modifier&modShift != 0 && k.shiftedKey != 0 {
		cp = k.shiftedKey
	}
	cp = normalizeKittyFunctional(cp)
	if cp < 32 {
		return ""
	}
	r := rune(cp)
	if !unicode.IsPrint(r) {
		return ""
	}
	return string(r)
}

// DecodePrintableKey tries Kitty CSI-u then xterm modifyOtherKeys to extract a
// printable character.
func DecodePrintableKey(data string) string {
	if s := DecodeKittyPrintable(data); s != "" {
		return s
	}
	mok := parseModifyOtherKeysSequence(data)
	if mok == nil {
		return ""
	}
	modifier := mok.modifier & ^modLockMask
	if modifier&^modShift != 0 {
		return ""
	}
	if mok.codepoint < 32 {
		return ""
	}
	r := rune(mok.codepoint)
	if !unicode.IsPrint(r) {
		return ""
	}
	return string(r)
}
