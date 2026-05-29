package tui

import (
	"sort"
	"sync"
)

// Keybinding identifies a logical action (e.g. "tui.editor.cursorLeft").
type Keybinding string

// KeybindingDefinition declares the default keys and an optional human
// description for a Keybinding.
type KeybindingDefinition struct {
	DefaultKeys []KeyID
	Description string
}

// KeybindingDefinitions is the canonical map of all known actions plus their
// defaults.
type KeybindingDefinitions map[Keybinding]KeybindingDefinition

// KeybindingsConfig is the user override of an action's bound keys. nil keys
// (or an absent entry) means "use defaults"; an explicit empty slice unbinds
// the action.
type KeybindingsConfig map[Keybinding][]KeyID

// KeybindingConflict is reported when two actions claim the same KeyID in the
// user config.
type KeybindingConflict struct {
	Key      KeyID
	Bindings []Keybinding
}

// TUIKeybindings is the canonical set of keybindings used by the built-in
// components (editor, input, select-list).
var TUIKeybindings = KeybindingDefinitions{
	"tui.editor.cursorUp":           {DefaultKeys: []KeyID{KeyUp}, Description: "Move cursor up"},
	"tui.editor.cursorDown":         {DefaultKeys: []KeyID{KeyDown}, Description: "Move cursor down"},
	"tui.editor.cursorLeft":         {DefaultKeys: []KeyID{KeyLeft, Ctrl("b")}, Description: "Move cursor left"},
	"tui.editor.cursorRight":        {DefaultKeys: []KeyID{KeyRight, Ctrl("f")}, Description: "Move cursor right"},
	"tui.editor.cursorWordLeft":     {DefaultKeys: []KeyID{Alt(KeyLeft), Ctrl(KeyLeft), Alt("b")}, Description: "Move cursor word left"},
	"tui.editor.cursorWordRight":    {DefaultKeys: []KeyID{Alt(KeyRight), Ctrl(KeyRight), Alt("f")}, Description: "Move cursor word right"},
	"tui.editor.cursorLineStart":    {DefaultKeys: []KeyID{KeyHome, Ctrl("a")}, Description: "Move to line start"},
	"tui.editor.cursorLineEnd":      {DefaultKeys: []KeyID{KeyEnd, Ctrl("e")}, Description: "Move to line end"},
	"tui.editor.jumpForward":        {DefaultKeys: []KeyID{Ctrl("]")}, Description: "Jump forward to character"},
	"tui.editor.jumpBackward":       {DefaultKeys: []KeyID{KeyID("ctrl+alt+]")}, Description: "Jump backward to character"},
	"tui.editor.pageUp":             {DefaultKeys: []KeyID{KeyPageUp}, Description: "Page up"},
	"tui.editor.pageDown":           {DefaultKeys: []KeyID{KeyPageDown}, Description: "Page down"},
	"tui.editor.deleteCharBackward": {DefaultKeys: []KeyID{KeyBackspace}, Description: "Delete character backward"},
	"tui.editor.deleteCharForward":  {DefaultKeys: []KeyID{KeyDelete, Ctrl("d")}, Description: "Delete character forward"},
	"tui.editor.deleteWordBackward": {DefaultKeys: []KeyID{Ctrl("w"), Alt(KeyBackspace)}, Description: "Delete word backward"},
	"tui.editor.deleteWordForward":  {DefaultKeys: []KeyID{Alt("d"), Alt(KeyDelete)}, Description: "Delete word forward"},
	"tui.editor.deleteToLineStart":  {DefaultKeys: []KeyID{Ctrl("u")}, Description: "Delete to line start"},
	"tui.editor.deleteToLineEnd":    {DefaultKeys: []KeyID{Ctrl("k")}, Description: "Delete to line end"},
	"tui.editor.yank":               {DefaultKeys: []KeyID{Ctrl("y")}, Description: "Yank"},
	"tui.editor.yankPop":            {DefaultKeys: []KeyID{Alt("y")}, Description: "Yank pop"},
	"tui.editor.undo":               {DefaultKeys: []KeyID{Ctrl("-")}, Description: "Undo"},
	"tui.input.newLine":             {DefaultKeys: []KeyID{Shift(KeyEnter)}, Description: "Insert newline"},
	"tui.input.submit":              {DefaultKeys: []KeyID{KeyEnter}, Description: "Submit input"},
	"tui.input.tab":                 {DefaultKeys: []KeyID{KeyTab}, Description: "Tab / autocomplete"},
	"tui.input.copy":                {DefaultKeys: []KeyID{Ctrl("c")}, Description: "Copy selection"},
	"tui.select.up":                 {DefaultKeys: []KeyID{KeyUp}, Description: "Move selection up"},
	"tui.select.down":               {DefaultKeys: []KeyID{KeyDown}, Description: "Move selection down"},
	"tui.select.pageUp":             {DefaultKeys: []KeyID{KeyPageUp}, Description: "Selection page up"},
	"tui.select.pageDown":           {DefaultKeys: []KeyID{KeyPageDown}, Description: "Selection page down"},
	"tui.select.confirm":            {DefaultKeys: []KeyID{KeyEnter}, Description: "Confirm selection"},
	"tui.select.cancel":             {DefaultKeys: []KeyID{KeyEscape, Ctrl("c")}, Description: "Cancel selection"},
}

// KeybindingsManager resolves Keybinding → []KeyID, with user-config overrides
// and conflict detection.
type KeybindingsManager struct {
	mu          sync.RWMutex
	definitions KeybindingDefinitions
	user        KeybindingsConfig
	resolved    map[Keybinding][]KeyID
	conflicts   []KeybindingConflict
}

// NewKeybindingsManager returns a manager that merges definitions with the
// (optional) user overrides.
func NewKeybindingsManager(definitions KeybindingDefinitions, user KeybindingsConfig) *KeybindingsManager {
	if definitions == nil {
		definitions = KeybindingDefinitions{}
	}
	if user == nil {
		user = KeybindingsConfig{}
	}
	m := &KeybindingsManager{definitions: definitions, user: user}
	m.rebuild()
	return m
}

func (m *KeybindingsManager) rebuild() {
	resolved := make(map[Keybinding][]KeyID, len(m.definitions))
	userClaims := map[KeyID]map[Keybinding]struct{}{}

	// Resolve.
	for id, def := range m.definitions {
		if userKeys, ok := m.user[id]; ok {
			resolved[id] = dedupKeys(userKeys)
		} else {
			resolved[id] = dedupKeys(def.DefaultKeys)
		}
	}
	// Conflict scan only over user-supplied bindings (so default overlaps
	// like "ctrl+c" used both for "tui.input.copy" and "tui.select.cancel"
	// remain by-design).
	for id, keys := range m.user {
		if _, known := m.definitions[id]; !known {
			continue
		}
		for _, k := range dedupKeys(keys) {
			set, ok := userClaims[k]
			if !ok {
				set = map[Keybinding]struct{}{}
				userClaims[k] = set
			}
			set[id] = struct{}{}
		}
	}
	var conflicts []KeybindingConflict
	for k, set := range userClaims {
		if len(set) <= 1 {
			continue
		}
		var ids []Keybinding
		for id := range set {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
		conflicts = append(conflicts, KeybindingConflict{Key: k, Bindings: ids})
	}
	sort.Slice(conflicts, func(i, j int) bool { return string(conflicts[i].Key) < string(conflicts[j].Key) })

	m.resolved = resolved
	m.conflicts = conflicts
}

func dedupKeys(in []KeyID) []KeyID {
	if len(in) == 0 {
		return nil
	}
	seen := map[KeyID]struct{}{}
	out := make([]KeyID, 0, len(in))
	for _, k := range in {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// Matches reports whether the given input matches any bound key for keybinding.
func (m *KeybindingsManager) Matches(data string, keybinding Keybinding) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, k := range m.resolved[keybinding] {
		if MatchesKey(data, k) {
			return true
		}
	}
	return false
}

// Keys returns a copy of the keys currently bound to keybinding.
func (m *KeybindingsManager) Keys(keybinding Keybinding) []KeyID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.resolved[keybinding]
	out := make([]KeyID, len(src))
	copy(out, src)
	return out
}

// Definition returns the canonical definition for keybinding.
func (m *KeybindingsManager) Definition(keybinding Keybinding) KeybindingDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.definitions[keybinding]
}

// Conflicts returns user-config keys that are bound to multiple actions.
func (m *KeybindingsManager) Conflicts() []KeybindingConflict {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]KeybindingConflict, len(m.conflicts))
	for i, c := range m.conflicts {
		ids := make([]Keybinding, len(c.Bindings))
		copy(ids, c.Bindings)
		out[i] = KeybindingConflict{Key: c.Key, Bindings: ids}
	}
	return out
}

// SetUserBindings replaces the entire user-bindings layer and rebuilds.
func (m *KeybindingsManager) SetUserBindings(user KeybindingsConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if user == nil {
		user = KeybindingsConfig{}
	}
	m.user = user
	m.rebuild()
}

// UserBindings returns a copy of the current user overrides.
func (m *KeybindingsManager) UserBindings() KeybindingsConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(KeybindingsConfig, len(m.user))
	for k, v := range m.user {
		cp := make([]KeyID, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// Resolved returns the effective bindings (defaults + user overrides) for
// every defined keybinding.
func (m *KeybindingsManager) Resolved() KeybindingsConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(KeybindingsConfig, len(m.resolved))
	for k, v := range m.resolved {
		cp := make([]KeyID, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// =============================================================================
// Process-global default keybindings manager
// =============================================================================

var (
	globalKbMu sync.RWMutex
	globalKb   *KeybindingsManager
)

// SetKeybindings sets the process-global keybindings manager. Pass nil to
// reset to the default (TUIKeybindings only, no user overrides).
func SetKeybindings(m *KeybindingsManager) {
	globalKbMu.Lock()
	globalKb = m
	globalKbMu.Unlock()
}

// GetKeybindings returns the process-global keybindings manager, lazily
// initialized to use TUIKeybindings on first call.
func GetKeybindings() *KeybindingsManager {
	globalKbMu.Lock()
	defer globalKbMu.Unlock()
	if globalKb == nil {
		globalKb = NewKeybindingsManager(TUIKeybindings, nil)
	}
	return globalKb
}
