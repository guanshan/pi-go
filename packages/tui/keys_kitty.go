package tui

import (
	"regexp"
	"strconv"
)

// kittyParsed holds the components of a Kitty CSI-u or arrow/function key
// sequence.
type kittyParsed struct {
	codepoint     int
	shiftedKey    int
	baseLayoutKey int
	modifier      int
	eventType     KittyEventType
}

// KittyEventType is the event type carried in Kitty flag-2 sequences.
type KittyEventType int

const (
	KittyEventPress   KittyEventType = 1
	KittyEventRepeat  KittyEventType = 2
	KittyEventRelease KittyEventType = 3
)

// CSI-u patterns:
//
//	\x1b[<codepoint>u
//	\x1b[<codepoint>;<mod>u
//	\x1b[<codepoint>;<mod>:<event>u
//	\x1b[<codepoint>:<shifted>;<mod>u
//	\x1b[<codepoint>:<shifted>:<base>;<mod>u
//	\x1b[<codepoint>::<base>;<mod>u
var (
	kittyCsiURegex = regexp.MustCompile(`^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$`)
	// Arrow key with modifier, optional event: \x1b[1;<mod>[:<event>][ABCD]
	kittyArrowRegex = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([ABCD])$`)
	// Functional key: \x1b[<num>;<mod>[:<event>]~
	kittyFunctionalRegex = regexp.MustCompile(`^\x1b\[(\d+)(?:;(\d+))?(?::(\d+))?~$`)
	// Home/End with modifier: \x1b[1;<mod>H or \x1b[1;<mod>F
	kittyHomeEndRegex = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([HF])$`)
)

func parseEventType(s string) KittyEventType {
	if s == "" {
		return KittyEventPress
	}
	v, _ := strconv.Atoi(s)
	switch v {
	case int(KittyEventRepeat):
		return KittyEventRepeat
	case int(KittyEventRelease):
		return KittyEventRelease
	default:
		return KittyEventPress
	}
}

func parseKittySequence(data string) *kittyParsed {
	if m := kittyCsiURegex.FindStringSubmatch(data); m != nil {
		cp, _ := strconv.Atoi(m[1])
		shifted := 0
		if m[2] != "" {
			shifted, _ = strconv.Atoi(m[2])
		}
		base := 0
		if m[3] != "" {
			base, _ = strconv.Atoi(m[3])
		}
		mod := 1
		if m[4] != "" {
			mod, _ = strconv.Atoi(m[4])
		}
		return &kittyParsed{
			codepoint:     cp,
			shiftedKey:    shifted,
			baseLayoutKey: base,
			modifier:      mod - 1,
			eventType:     parseEventType(m[5]),
		}
	}
	if m := kittyArrowRegex.FindStringSubmatch(data); m != nil {
		mod, _ := strconv.Atoi(m[1])
		var cp int
		switch m[3] {
		case "A":
			cp = cpArrowUp
		case "B":
			cp = cpArrowDown
		case "C":
			cp = cpArrowRight
		case "D":
			cp = cpArrowLeft
		}
		return &kittyParsed{
			codepoint: cp,
			modifier:  mod - 1,
			eventType: parseEventType(m[2]),
		}
	}
	if m := kittyHomeEndRegex.FindStringSubmatch(data); m != nil {
		mod, _ := strconv.Atoi(m[1])
		var cp int
		if m[3] == "H" {
			cp = cpHome
		} else {
			cp = cpEnd
		}
		return &kittyParsed{
			codepoint: cp,
			modifier:  mod - 1,
			eventType: parseEventType(m[2]),
		}
	}
	if m := kittyFunctionalRegex.FindStringSubmatch(data); m != nil {
		num, _ := strconv.Atoi(m[1])
		mod := 1
		if m[2] != "" {
			mod, _ = strconv.Atoi(m[2])
		}
		var cp int
		switch num {
		case 2:
			cp = cpInsert
		case 3:
			cp = cpDelete
		case 5:
			cp = cpPageUp
		case 6:
			cp = cpPageDown
		case 7:
			cp = cpHome
		case 8:
			cp = cpEnd
		default:
			return nil
		}
		return &kittyParsed{
			codepoint: cp,
			modifier:  mod - 1,
			eventType: parseEventType(m[3]),
		}
	}
	return nil
}

// matchesKittySequence reports whether data is a Kitty sequence whose
// canonical codepoint and modifier match.
func matchesKittySequence(data string, expectedCodepoint, expectedModifier int) bool {
	k := parseKittySequence(data)
	if k == nil {
		return false
	}
	actualMod := k.modifier & ^modLockMask
	expectedMod := expectedModifier & ^modLockMask
	if actualMod != expectedMod {
		return false
	}
	cp := normalizeShiftedLetterIdentity(normalizeKittyFunctional(k.codepoint), k.modifier)
	expCP := normalizeShiftedLetterIdentity(normalizeKittyFunctional(expectedCodepoint), expectedModifier)
	if cp == expCP {
		return true
	}
	// Fall back to base-layout key for non-Latin layouts.
	if k.baseLayoutKey != 0 && k.baseLayoutKey == expectedCodepoint {
		isLatinLetter := cp >= 'a' && cp <= 'z'
		isKnownSymbol := cp > 0 && cp < 128 && isSymbolKey(rune(cp))
		if !isLatinLetter && !isKnownSymbol {
			return true
		}
	}
	return false
}
