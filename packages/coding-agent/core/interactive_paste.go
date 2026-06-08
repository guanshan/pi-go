package core

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	largePasteLineThreshold = 10
	largePasteCharThreshold = 1000
)

var pasteCSIUControlPattern = regexp.MustCompile(`\x1b\[(\d+);5u`)
var interactiveReadDir = os.ReadDir

type interactiveCompletionCache struct {
	Input       string
	CWD         string
	Completions []interactiveCompletion
	Valid       bool
}

func (m *interactiveModel) builtinCompletionsFor(input string, agent *AgentSession) []interactiveCompletion {
	if !interactiveBuiltinCompletionReadsDir(input) {
		return interactiveBuiltinCompletions(input, agent)
	}
	cwd := ""
	if agent != nil && agent.Session != nil {
		cwd = agent.Session.CWD()
	}
	cache := &m.builtinCompletionCache
	if cache.Valid && cache.Input == input && cache.CWD == cwd {
		return cache.Completions
	}
	cache.Completions = interactiveBuiltinCompletions(input, agent)
	cache.Input = input
	cache.CWD = cwd
	cache.Valid = true
	return cache.Completions
}

func interactiveBuiltinCompletionReadsDir(input string) bool {
	raw := strings.TrimLeft(input, " \t\r\n")
	if strings.HasPrefix(raw, "/model ") {
		return false
	}
	if _, _, ok := trailingFileRefToken(input); ok {
		return true
	}
	if _, _, ok := trailingPathCompletionToken(input); ok {
		return true
	}
	return false
}

func (m *interactiveModel) handlePaste(content string) {
	filtered := cleanPastedText(content)
	if filtered == "" {
		return
	}
	if shouldPrependSpaceForPastedPath(filtered, m.charBeforeInputCursor()) {
		filtered = " " + filtered
	}
	lines := strings.Split(filtered, "\n")
	chars := len([]rune(filtered))
	if len(lines) > largePasteLineThreshold || chars > largePasteCharThreshold {
		m.pasteCounter++
		if m.pastes == nil {
			m.pastes = map[int]string{}
		}
		pasteID := m.pasteCounter
		m.pastes[pasteID] = filtered
		m.input.InsertString(pasteMarker(pasteID, len(lines), chars))
	} else {
		m.input.InsertString(filtered)
	}
	m.historyIndex = -1
	m.autocompleteIndex = 0
}

func cleanPastedText(content string) string {
	decoded := pasteCSIUControlPattern.ReplaceAllStringFunc(content, func(match string) string {
		parts := pasteCSIUControlPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		code, err := strconv.Atoi(parts[1])
		if err != nil {
			return match
		}
		switch {
		case code >= 'a' && code <= 'z':
			return string(rune(code - 'a' + 1))
		case code >= 'A' && code <= 'Z':
			return string(rune(code - 'A' + 1))
		default:
			return match
		}
	})
	clean := strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "    ").Replace(decoded)
	var out strings.Builder
	for _, r := range clean {
		if r == '\n' || r >= 32 {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func pasteMarker(id, lineCount, charCount int) string {
	if lineCount > largePasteLineThreshold {
		return fmt.Sprintf("[paste #%d +%d lines]", id, lineCount)
	}
	return fmt.Sprintf("[paste #%d %d chars]", id, charCount)
}

func shouldPrependSpaceForPastedPath(text string, before rune) bool {
	if text == "" || before == 0 {
		return false
	}
	switch []rune(text)[0] {
	case '/', '~', '.':
		return isWordRune(before)
	default:
		return false
	}
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (m *interactiveModel) charBeforeInputCursor() rune {
	if m == nil {
		return 0
	}
	lines := strings.Split(m.input.Value(), "\n")
	row := m.input.Line()
	if row < 0 || row >= len(lines) {
		return 0
	}
	runes := []rune(lines[row])
	col := m.input.Column()
	if col <= 0 || col > len(runes) {
		return 0
	}
	return runes[col-1]
}

func (m *interactiveModel) expandedInputText() string {
	if m == nil {
		return ""
	}
	return m.expandPasteMarkers(m.input.Value())
}

func (m *interactiveModel) expandPasteMarkers(text string) string {
	if m == nil || len(m.pastes) == 0 || !strings.Contains(text, "[paste #") {
		return text
	}
	// Expand in ascending id (oldest first) order so the result is
	// deterministic even when one paste's content embeds another's marker text.
	ids := make([]int, 0, len(m.pastes))
	for id := range m.pastes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := text
	for _, id := range ids {
		content := m.pastes[id]
		pattern := regexp.MustCompile(fmt.Sprintf(`\[paste #%d( (\+\d+ lines|\d+ chars))?\]`, id))
		out = pattern.ReplaceAllStringFunc(out, func(string) string { return content })
	}
	return out
}

func (m *interactiveModel) clearPastes() {
	if m == nil {
		return
	}
	m.pastes = map[int]string{}
	m.pasteCounter = 0
}
