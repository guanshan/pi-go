package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
	"github.com/guanshan/pi-go/packages/tui"
)

type AppKeybinding string

const (
	AppInterrupt                AppKeybinding = "app.interrupt"
	AppClear                    AppKeybinding = "app.clear"
	AppExit                     AppKeybinding = "app.exit"
	AppSuspend                  AppKeybinding = "app.suspend"
	AppThinkingCycle            AppKeybinding = "app.thinking.cycle"
	AppModelCycleForward        AppKeybinding = "app.model.cycleForward"
	AppModelCycleBackward       AppKeybinding = "app.model.cycleBackward"
	AppModelSelect              AppKeybinding = "app.model.select"
	AppToolsExpand              AppKeybinding = "app.tools.expand"
	AppThinkingToggle           AppKeybinding = "app.thinking.toggle"
	AppSessionToggleNamedFilter AppKeybinding = "app.session.toggleNamedFilter"
	AppEditorExternal           AppKeybinding = "app.editor.external"
	AppMessageFollowUp          AppKeybinding = "app.message.followUp"
	AppMessageDequeue           AppKeybinding = "app.message.dequeue"
	AppClipboardPasteImage      AppKeybinding = "app.clipboard.pasteImage"
	AppSessionNew               AppKeybinding = "app.session.new"
	AppSessionTree              AppKeybinding = "app.session.tree"
	AppSessionFork              AppKeybinding = "app.session.fork"
	AppSessionResume            AppKeybinding = "app.session.resume"
)

type KeybindingsManager struct {
	manager     *tui.KeybindingsManager
	configPath  string
	diagnostics []Diagnostic
}

func NewKeybindingsManager(agentDir string) *KeybindingsManager {
	configPath := filepath.Join(agentDir, "keybindings.json")
	user, diagnostics := loadKeybindingsConfig(configPath, appKeybindingDefinitions())
	return &KeybindingsManager{
		manager:     tui.NewKeybindingsManager(appKeybindingDefinitions(), user),
		configPath:  configPath,
		diagnostics: diagnostics,
	}
}

func (k *KeybindingsManager) Manager() *tui.KeybindingsManager {
	if k == nil || k.manager == nil {
		return tui.NewKeybindingsManager(appKeybindingDefinitions(), nil)
	}
	return k.manager
}

func (k *KeybindingsManager) Diagnostics() []Diagnostic {
	if k == nil {
		return nil
	}
	return append([]Diagnostic(nil), k.diagnostics...)
}

func (k *KeybindingsManager) Reload() {
	if k == nil || k.configPath == "" {
		return
	}
	user, diagnostics := loadKeybindingsConfig(k.configPath, appKeybindingDefinitions())
	k.manager.SetUserBindings(user)
	k.diagnostics = diagnostics
}

func (k *KeybindingsManager) MatchesBubbleKey(key string, action AppKeybinding) bool {
	if k == nil || k.manager == nil {
		return defaultKeybindingsManager().MatchesBubbleKey(key, action)
	}
	input, ok := normalizeBubbleKey(key)
	if !ok {
		return false
	}
	for _, candidate := range k.manager.Keys(tui.Keybinding(action)) {
		normalized, ok := normalizeKeyID(string(candidate))
		if ok && normalized == input {
			return true
		}
	}
	return false
}

func (k *KeybindingsManager) KeyDisplay(action AppKeybinding) string {
	if k == nil || k.manager == nil {
		return defaultKeybindingsManager().KeyDisplay(action)
	}
	keys := k.manager.Keys(tui.Keybinding(action))
	if len(keys) == 0 {
		return "unbound"
	}
	return string(keys[0])
}

func (k *KeybindingsManager) MatchesAnyBubbleKey(key string) bool {
	manager := k
	if manager == nil || manager.manager == nil {
		manager = defaultKeybindingsManager()
	}
	input, ok := normalizeBubbleKey(key)
	if !ok {
		return false
	}
	for action := range appKeybindingDefinitions() {
		for _, candidate := range manager.manager.Keys(action) {
			normalized, ok := normalizeKeyID(string(candidate))
			if ok && normalized == input {
				return true
			}
		}
	}
	return false
}

type extensionShortcutConflict struct {
	keybinding       string
	restrictOverride bool
}

type resolvedExtensionShortcut struct {
	key      string
	shortcut coreext.ShortcutDefinition
}

var reservedKeybindingsForExtensionConflicts = map[string]bool{
	string(AppInterrupt):          true,
	string(AppClear):              true,
	string(AppExit):               true,
	string(AppSuspend):            true,
	string(AppThinkingCycle):      true,
	string(AppModelCycleForward):  true,
	string(AppModelCycleBackward): true,
	string(AppModelSelect):        true,
	string(AppToolsExpand):        true,
	string(AppThinkingToggle):     true,
	string(AppEditorExternal):     true,
	string(AppMessageFollowUp):    true,
	"tui.input.submit":            true,
	"tui.select.confirm":          true,
	"tui.select.cancel":           true,
	"tui.input.copy":              true,
	"tui.editor.deleteToLineEnd":  true,
}

func resolveExtensionShortcuts(runtime *coreext.Runner, keybindings *KeybindingsManager) ([]resolvedExtensionShortcut, []Diagnostic) {
	if runtime == nil {
		return nil, nil
	}
	builtins := builtinKeybindingsForExtensionConflicts(keybindings)
	byKey := map[string]resolvedExtensionShortcut{}
	order := []string{}
	var diagnostics []Diagnostic
	for _, shortcut := range runtime.RegisteredShortcuts() {
		key, ok := normalizeKeyID(shortcut.Key)
		if !ok {
			continue
		}
		source := firstNonEmpty(shortcut.Source, "extension")
		if builtin, ok := builtins[key]; ok {
			if builtin.restrictOverride {
				diagnostics = append(diagnostics, Diagnostic{
					Type:    DiagWarning,
					Message: fmt.Sprintf("Extension shortcut '%s' from %s conflicts with built-in shortcut. Skipping.", shortcut.Key, source),
				})
				continue
			}
			diagnostics = append(diagnostics, Diagnostic{
				Type:    DiagWarning,
				Message: fmt.Sprintf("Extension shortcut conflict: '%s' is built-in shortcut for %s and %s. Using %s.", shortcut.Key, builtin.keybinding, source, source),
			})
		}
		if existing, ok := byKey[key]; ok {
			diagnostics = append(diagnostics, Diagnostic{
				Type:    DiagWarning,
				Message: fmt.Sprintf("Extension shortcut conflict: '%s' registered by both %s and %s. Using %s.", shortcut.Key, firstNonEmpty(existing.shortcut.Source, "extension"), source, source),
			})
		} else {
			order = append(order, key)
		}
		byKey[key] = resolvedExtensionShortcut{key: key, shortcut: shortcut}
	}
	resolved := make([]resolvedExtensionShortcut, 0, len(order))
	for _, key := range order {
		if shortcut, ok := byKey[key]; ok {
			resolved = append(resolved, shortcut)
		}
	}
	return resolved, diagnostics
}

func builtinKeybindingsForExtensionConflicts(keybindings *KeybindingsManager) map[string]extensionShortcutConflict {
	manager := keybindings
	if manager == nil || manager.manager == nil {
		manager = defaultKeybindingsManager()
	}
	defs := appKeybindingDefinitions()
	actions := make([]string, 0, len(defs))
	for action := range defs {
		actions = append(actions, string(action))
	}
	sort.Strings(actions)
	out := map[string]extensionShortcutConflict{}
	for _, action := range actions {
		restrict := reservedKeybindingsForExtensionConflicts[action]
		for _, candidate := range manager.manager.Keys(tui.Keybinding(action)) {
			key, ok := normalizeKeyID(string(candidate))
			if !ok {
				continue
			}
			existing, exists := out[key]
			if !exists || (!existing.restrictOverride && restrict) {
				out[key] = extensionShortcutConflict{keybinding: action, restrictOverride: restrict}
			}
		}
	}
	return out
}

func (m *interactiveModel) appKey(key string, action AppKeybinding) bool {
	manager := m.keybindings
	if manager == nil {
		manager = defaultKeybindingsManager()
	}
	return manager.MatchesBubbleKey(key, action)
}

type interactiveExtensionShortcutDoneMsg struct {
	Key         string
	Description string
	Err         error
}

func (m *interactiveModel) extensionShortcutCommand(key string) tea.Cmd {
	if m == nil {
		return nil
	}
	shortcut, runtime, ok := m.extensionShortcutForKey(key)
	if !ok || shortcut.Execute == nil {
		return nil
	}
	label := firstNonEmpty(shortcut.Description, shortcut.Key)
	m.setStatus(fmt.Sprintf("Running extension shortcut: %s", label))
	return func() tea.Msg {
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		err := shortcut.Execute(ctx)
		if err != nil {
			runtime.EmitError(err)
		}
		return interactiveExtensionShortcutDoneMsg{Key: shortcut.Key, Description: shortcut.Description, Err: err}
	}
}

func (m *interactiveModel) extensionShortcutForKey(key string) (coreext.ShortcutDefinition, *coreext.Runner, bool) {
	input, ok := normalizeBubbleKey(key)
	if !ok {
		return coreext.ShortcutDefinition{}, nil, false
	}
	agent, err := runtimeAgent(m.runtime)
	if err != nil || agent == nil || agent.extensionRuntime == nil {
		return coreext.ShortcutDefinition{}, nil, false
	}
	shortcuts, _ := resolveExtensionShortcuts(agent.extensionRuntime, m.keybindings)
	for _, shortcut := range shortcuts {
		if shortcut.key == input {
			return shortcut.shortcut, agent.extensionRuntime, true
		}
	}
	return coreext.ShortcutDefinition{}, nil, false
}

func (m *interactiveModel) handleDequeue() {
	restored := m.restoreQueuedMessagesToEditor()
	if restored == 0 {
		m.setStatus("No queued messages to restore")
		return
	}
	suffix := ""
	if restored > 1 {
		suffix = "s"
	}
	m.setStatus(fmt.Sprintf("Restored %d queued message%s to editor", restored, suffix))
}

func (m *interactiveModel) restoreQueuedMessagesToEditor() int {
	var queued []string
	if agent, err := runtimeAgent(m.runtime); err == nil && agent != nil {
		cleared := agent.ClearQueue()
		queued = append(queued, cleared.Steering...)
		queued = append(queued, cleared.FollowUp...)
	} else {
		queued = append(queued, m.queuedSteering...)
		queued = append(queued, m.queuedFollowUp...)
	}
	for _, item := range m.localQueue {
		if strings.TrimSpace(item.Text) != "" {
			queued = append(queued, item.Text)
		}
	}
	m.localQueue = nil
	m.queuedSteering = nil
	m.queuedFollowUp = nil
	if len(queued) == 0 {
		return 0
	}
	queuedText := strings.Join(queued, "\n\n")
	current := m.input.Value()
	combined := strings.Join(nonEmpty([]string{queuedText, current}), "\n\n")
	m.input.SetValue(combined)
	m.historyIndex = -1
	m.autocompleteIndex = 0
	return len(queued)
}

func (m *interactiveModel) toggleToolsExpanded() {
	m.toolsExpanded = !m.toolsExpanded
	if m.toolsExpanded {
		m.setStatus("Tool output expansion: on")
	} else {
		m.setStatus("Tool output expansion: off")
	}
}

func (m *interactiveModel) toggleThinkingBlockVisibility() {
	agent, err := runtimeAgent(m.runtime)
	if err != nil || agent == nil || agent.Settings == nil {
		m.setStatus("Thinking block setting is unavailable")
		return
	}
	next := !agent.Settings.HideThinkingBlock()
	if err := agent.Settings.SetHideThinkingBlock(next); err != nil {
		m.appendMessage(interactiveRoleError, err.Error())
		return
	}
	if next {
		m.setStatus("Thinking blocks: hidden")
	} else {
		m.setStatus("Thinking blocks: visible")
	}
}

func (m *interactiveModel) openExternalEditor() tea.Cmd {
	editorCmd := strings.TrimSpace(firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR")))
	if editorCmd == "" {
		m.setStatus("No editor configured. Set $VISUAL or $EDITOR.")
		return nil
	}
	tmp, err := os.CreateTemp("", "pi-editor-*.pi.md")
	if err != nil {
		m.appendMessage(interactiveRoleError, err.Error())
		return nil
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(m.expandedInputText()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		m.appendMessage(interactiveRoleError, err.Error())
		return nil
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		m.appendMessage(interactiveRoleError, err.Error())
		return nil
	}
	parts := strings.Fields(editorCmd)
	if len(parts) == 0 {
		_ = os.Remove(tmpPath)
		m.setStatus("No editor configured. Set $VISUAL or $EDITOR.")
		return nil
	}
	cmd := exec.Command(parts[0], append(parts[1:], tmpPath)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(tmpPath)
		if err != nil {
			return externalEditorDoneMsg{Err: err}
		}
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return externalEditorDoneMsg{Err: readErr}
		}
		text := strings.TrimSuffix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		return externalEditorDoneMsg{Text: text}
	})
}

func defaultKeybindingsManager() *KeybindingsManager {
	return &KeybindingsManager{manager: tui.NewKeybindingsManager(appKeybindingDefinitions(), nil)}
}

func appKeybindingDefinitions() tui.KeybindingDefinitions {
	defs := tui.KeybindingDefinitions{}
	for key, def := range tui.TUIKeybindings {
		defs[key] = def
	}
	add := func(action AppKeybinding, keys []tui.KeyID, description string) {
		defs[tui.Keybinding(action)] = tui.KeybindingDefinition{DefaultKeys: keys, Description: description}
	}
	add(AppInterrupt, keyIDs("escape"), "Cancel or abort")
	add(AppClear, keyIDs("ctrl+c"), "Clear editor")
	add(AppExit, keyIDs("ctrl+d"), "Exit when editor is empty")
	if runtime.GOOS == "windows" {
		add(AppSuspend, nil, "Suspend to background")
		add(AppClipboardPasteImage, keyIDs("alt+v"), "Paste image from clipboard")
	} else {
		add(AppSuspend, keyIDs("ctrl+z"), "Suspend to background")
		add(AppClipboardPasteImage, keyIDs("ctrl+v"), "Paste image from clipboard")
	}
	add(AppThinkingCycle, keyIDs("shift+tab"), "Cycle thinking level")
	add(AppModelCycleForward, keyIDs("ctrl+p"), "Cycle to next model")
	add(AppModelCycleBackward, keyIDs("shift+ctrl+p"), "Cycle to previous model")
	add(AppModelSelect, keyIDs("ctrl+l"), "Open model selector")
	add(AppToolsExpand, keyIDs("ctrl+o"), "Toggle tool output")
	add(AppThinkingToggle, keyIDs("ctrl+t"), "Toggle thinking blocks")
	add(AppSessionToggleNamedFilter, keyIDs("ctrl+n"), "Toggle named session filter")
	add(AppEditorExternal, keyIDs("ctrl+g"), "Open external editor")
	add(AppMessageFollowUp, keyIDs("alt+enter"), "Queue follow-up message")
	add(AppMessageDequeue, keyIDs("alt+up"), "Restore queued messages")
	add(AppSessionNew, nil, "Start a new session")
	add(AppSessionTree, nil, "Open session tree")
	add(AppSessionFork, nil, "Fork current session")
	add(AppSessionResume, nil, "Resume a session")
	add(AppKeybinding("app.tree.foldOrUp"), keyIDs("ctrl+left", "alt+left"), "Fold tree branch or move up")
	add(AppKeybinding("app.tree.unfoldOrDown"), keyIDs("ctrl+right", "alt+right"), "Unfold tree branch or move down")
	add(AppKeybinding("app.tree.editLabel"), keyIDs("shift+l"), "Edit tree label")
	add(AppKeybinding("app.tree.toggleLabelTimestamp"), keyIDs("shift+t"), "Toggle tree label timestamps")
	add(AppKeybinding("app.session.togglePath"), keyIDs("ctrl+p"), "Toggle session path display")
	add(AppKeybinding("app.session.toggleSort"), keyIDs("ctrl+s"), "Toggle session sort mode")
	add(AppKeybinding("app.session.rename"), keyIDs("ctrl+r"), "Rename session")
	add(AppKeybinding("app.session.delete"), keyIDs("ctrl+d"), "Delete session")
	add(AppKeybinding("app.session.deleteNoninvasive"), keyIDs("ctrl+backspace"), "Delete session when query is empty")
	add(AppKeybinding("app.models.save"), keyIDs("ctrl+s"), "Save model selection")
	add(AppKeybinding("app.models.enableAll"), keyIDs("ctrl+a"), "Enable all models")
	add(AppKeybinding("app.models.clearAll"), keyIDs("ctrl+x"), "Clear all models")
	add(AppKeybinding("app.models.toggleProvider"), keyIDs("ctrl+p"), "Toggle all models for provider")
	add(AppKeybinding("app.models.reorderUp"), keyIDs("alt+up"), "Move model up in order")
	add(AppKeybinding("app.models.reorderDown"), keyIDs("alt+down"), "Move model down in order")
	add(AppKeybinding("app.tree.filter.default"), keyIDs("ctrl+d"), "Tree filter: default view")
	add(AppKeybinding("app.tree.filter.noTools"), keyIDs("ctrl+t"), "Tree filter: hide tool results")
	add(AppKeybinding("app.tree.filter.userOnly"), keyIDs("ctrl+u"), "Tree filter: user messages only")
	add(AppKeybinding("app.tree.filter.labeledOnly"), keyIDs("ctrl+l"), "Tree filter: labeled entries only")
	add(AppKeybinding("app.tree.filter.all"), keyIDs("ctrl+a"), "Tree filter: show all entries")
	add(AppKeybinding("app.tree.filter.cycleForward"), keyIDs("ctrl+o"), "Tree filter: cycle forward")
	add(AppKeybinding("app.tree.filter.cycleBackward"), keyIDs("shift+ctrl+o"), "Tree filter: cycle backward")
	return defs
}

func keyIDs(values ...string) []tui.KeyID {
	out := make([]tui.KeyID, 0, len(values))
	for _, value := range values {
		if normalized, ok := normalizeKeyID(value); ok {
			out = append(out, tui.KeyID(normalized))
		}
	}
	return out
}

func loadKeybindingsConfig(path string, definitions tui.KeybindingDefinitions) (tui.KeybindingsConfig, []Diagnostic) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []Diagnostic{{Type: DiagWarning, Message: fmt.Sprintf("failed to read keybindings: %v", err)}}
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, []Diagnostic{{Type: DiagWarning, Message: fmt.Sprintf("failed to parse keybindings: %v", err)}}
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, []Diagnostic{{Type: DiagWarning, Message: "keybindings.json must contain an object"}}
	}
	migrated := migrateKeybindingsObject(object)
	config := tui.KeybindingsConfig{}
	var diagnostics []Diagnostic
	for _, key := range sortedMapKeys(migrated) {
		binding := migrated[key]
		action := tui.Keybinding(key)
		if _, ok := definitions[action]; !ok {
			diagnostics = append(diagnostics, Diagnostic{Type: DiagWarning, Message: fmt.Sprintf("unknown keybinding ignored: %s", key)})
			continue
		}
		keys, valid, bindingDiagnostics := parseKeybindingValue(key, binding)
		diagnostics = append(diagnostics, bindingDiagnostics...)
		if !valid {
			continue
		}
		config[action] = keys
	}
	return config, diagnostics
}

func parseKeybindingValue(action string, value any) ([]tui.KeyID, bool, []Diagnostic) {
	switch v := value.(type) {
	case string:
		key, ok := normalizeKeyID(v)
		if !ok {
			return nil, false, []Diagnostic{{Type: DiagWarning, Message: fmt.Sprintf("invalid keybinding for %s: %s", action, v)}}
		}
		return []tui.KeyID{tui.KeyID(key)}, true, nil
	case []any:
		if len(v) == 0 {
			return nil, true, nil
		}
		var keys []tui.KeyID
		var diagnostics []Diagnostic
		for _, entry := range v {
			raw, ok := entry.(string)
			if !ok {
				diagnostics = append(diagnostics, Diagnostic{Type: DiagWarning, Message: fmt.Sprintf("invalid keybinding for %s: expected string entries", action)})
				continue
			}
			key, ok := normalizeKeyID(raw)
			if !ok {
				diagnostics = append(diagnostics, Diagnostic{Type: DiagWarning, Message: fmt.Sprintf("invalid keybinding for %s: %s", action, raw)})
				continue
			}
			keys = append(keys, tui.KeyID(key))
		}
		return keys, len(keys) > 0, diagnostics
	default:
		return nil, false, []Diagnostic{{Type: DiagWarning, Message: fmt.Sprintf("invalid keybinding for %s: expected string or string array", action)}}
	}
}

func migrateKeybindingsObject(raw map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range raw {
		next := key
		if migrated, ok := legacyKeybindingNameMigrations[key]; ok {
			next = string(migrated)
		}
		if key != next {
			if _, exists := raw[next]; exists {
				continue
			}
		}
		out[next] = value
	}
	return out
}

var legacyKeybindingNameMigrations = map[string]AppKeybinding{
	"interrupt":                AppInterrupt,
	"clear":                    AppClear,
	"exit":                     AppExit,
	"suspend":                  AppSuspend,
	"cycleThinkingLevel":       AppThinkingCycle,
	"cycleModelForward":        AppModelCycleForward,
	"cycleModelBackward":       AppModelCycleBackward,
	"selectModel":              AppModelSelect,
	"expandTools":              AppToolsExpand,
	"toggleThinking":           AppThinkingToggle,
	"toggleSessionNamedFilter": AppSessionToggleNamedFilter,
	"externalEditor":           AppEditorExternal,
	"followUp":                 AppMessageFollowUp,
	"dequeue":                  AppMessageDequeue,
	"pasteImage":               AppClipboardPasteImage,
	"newSession":               AppSessionNew,
	"tree":                     AppSessionTree,
	"fork":                     AppSessionFork,
	"resume":                   AppSessionResume,
	"treeFoldOrUp":             AppKeybinding("app.tree.foldOrUp"),
	"treeUnfoldOrDown":         AppKeybinding("app.tree.unfoldOrDown"),
	"treeEditLabel":            AppKeybinding("app.tree.editLabel"),
	"treeToggleLabelTimestamp": AppKeybinding("app.tree.toggleLabelTimestamp"),
	"toggleSessionPath":        AppKeybinding("app.session.togglePath"),
	"toggleSessionSort":        AppKeybinding("app.session.toggleSort"),
	"renameSession":            AppKeybinding("app.session.rename"),
	"deleteSession":            AppKeybinding("app.session.delete"),
	"deleteSessionNoninvasive": AppKeybinding("app.session.deleteNoninvasive"),
}

func normalizeBubbleKey(key string) (string, bool) {
	return normalizeKeyID(key)
}

func normalizeKeyID(key string) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	// A bare uppercase ASCII letter is a shifted-letter keypress: terminals report
	// e.g. Shift+L as "L" with no modifier token, so normalize it to "shift+l" so it
	// matches an explicit "shift+<letter>" binding. Only applies with no other
	// modifier ("ctrl+L" means ctrl+l, not ctrl+shift+l).
	if len(key) == 1 && key[0] >= 'A' && key[0] <= 'Z' {
		return "shift+" + strings.ToLower(key), true
	}
	key = strings.ToLower(key)
	if key == " " {
		return "space", true
	}
	parts := strings.Split(key, "+")
	modSeen := map[string]bool{}
	keyPart := ""
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", false
		}
		if i == len(parts)-1 {
			keyPart = normalizeKeyName(part)
			break
		}
		mod := normalizeKeyModifier(part)
		if mod == "" || modSeen[mod] {
			return "", false
		}
		modSeen[mod] = true
	}
	if keyPart == "" {
		return "", false
	}
	if len([]rune(keyPart)) != 1 && !knownSpecialKeyNames[keyPart] {
		return "", false
	}
	order := []string{"ctrl", "shift", "alt", "super"}
	var normalized []string
	for _, mod := range order {
		if modSeen[mod] {
			normalized = append(normalized, mod)
		}
	}
	normalized = append(normalized, keyPart)
	return strings.Join(normalized, "+"), true
}

func normalizeKeyModifier(value string) string {
	switch value {
	case "ctrl", "control":
		return "ctrl"
	case "shift":
		return "shift"
	case "alt", "meta":
		return "alt"
	case "super", "cmd", "command":
		return "super"
	default:
		return ""
	}
}

func normalizeKeyName(value string) string {
	switch strings.ToLower(value) {
	case "esc":
		return "escape"
	case "return":
		return "enter"
	case "pgup", "pageup", "page-up", "page_up":
		return "pageup"
	case "pgdown", "pagedown", "page-down", "page_down":
		return "pagedown"
	default:
		return strings.ToLower(value)
	}
}

var knownSpecialKeyNames = map[string]bool{
	"escape": true, "enter": true, "tab": true, "space": true, "backspace": true,
	"delete": true, "insert": true, "clear": true, "home": true, "end": true,
	"pageup": true, "pagedown": true, "up": true, "down": true, "left": true, "right": true,
	"f1": true, "f2": true, "f3": true, "f4": true, "f5": true, "f6": true,
	"f7": true, "f8": true, "f9": true, "f10": true, "f11": true, "f12": true,
}

func sortedMapKeys(input map[string]any) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
