package tui

import (
	"regexp"
	"strconv"
)

// xterm modifyOtherKeys: CSI 27 ; modifier ; codepoint ~
//
// Modifier values are 1-indexed: 2=shift, 3=alt, 5=ctrl, 7=ctrl+alt, …
var modifyOtherKeysRegex = regexp.MustCompile(`^\x1b\[27;(\d+);(\d+)~$`)

type modifyOtherKeysParsed struct {
	codepoint int
	modifier  int
}

func parseModifyOtherKeysSequence(data string) *modifyOtherKeysParsed {
	m := modifyOtherKeysRegex.FindStringSubmatch(data)
	if m == nil {
		return nil
	}
	mod, _ := strconv.Atoi(m[1])
	cp, _ := strconv.Atoi(m[2])
	return &modifyOtherKeysParsed{codepoint: cp, modifier: mod - 1}
}

func matchesModifyOtherKeys(data string, expectedCodepoint, expectedModifier int) bool {
	mok := parseModifyOtherKeysSequence(data)
	if mok == nil {
		return false
	}
	return mok.codepoint == expectedCodepoint && mok.modifier == expectedModifier
}

func matchesPrintableModifyOtherKeys(data string, expectedCodepoint, expectedModifier int) bool {
	if expectedModifier == 0 {
		return false
	}
	mok := parseModifyOtherKeysSequence(data)
	if mok == nil || mok.modifier != expectedModifier {
		return false
	}
	return normalizeShiftedLetterIdentity(mok.codepoint, mok.modifier) ==
		normalizeShiftedLetterIdentity(expectedCodepoint, expectedModifier)
}
