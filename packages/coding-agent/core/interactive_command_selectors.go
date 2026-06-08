package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/guanshan/pi-go/packages/tui"
)

// commandSelectorKind identifies which navigable overlay is open so the parent
// model dispatches the right action when an item is chosen.
type commandSelectorKind int

const (
	commandSelectorTheme commandSelectorKind = iota
	commandSelectorResume
	commandSelectorTree
	commandSelectorFork
)

// commandSelectorOverlay is a generic navigable picker for /theme, /resume,
// /tree and /fork. Like modelSelectorOverlay it swaps the input region in
// View() and is a thin stateful wrapper around tui.SelectList that owns the
// typed filter (SelectList does not consume printable characters). The chosen
// Value is acted on by the parent in a tea.Cmd OFF the Update goroutine: the
// session-mutating actions (SwitchSession/NavigateTree/Fork, theme reapply)
// emit events via program.Send, so calling them inline would deadlock — the
// same hazard modelSelectorOverlay documents.
type commandSelectorOverlay struct {
	kind       commandSelectorKind
	title      string
	hint       string
	list       *tui.SelectList
	all        []tui.SelectItem
	filter     string
	titleStyle lipgloss.Style
	hintStyle  lipgloss.Style
}

func newCommandSelectorOverlay(kind commandSelectorKind, title, hint string, items []tui.SelectItem, current string, styles interactiveThemeStyles) *commandSelectorOverlay {
	if len(items) == 0 {
		return nil
	}
	o := &commandSelectorOverlay{
		kind:       kind,
		title:      title,
		hint:       hint,
		all:        items,
		titleStyle: styles.SelectorTitle,
		hintStyle:  styles.SelectorHint,
	}
	o.list = tui.NewSelectList(items, interactiveSelectorMaxVisible, styles.SelectorTheme, tui.SelectListLayoutOptions{})
	if current != "" {
		o.selectValue(current)
	}
	return o
}

func (o *commandSelectorOverlay) selectValue(value string) {
	for i, item := range o.list.Items() {
		if item.Value == value {
			o.list.SetSelectedIndex(i)
			return
		}
	}
}

// HandleKey mirrors modelSelectorOverlay.HandleKey: navigation keys are
// translated to the raw escape sequences SelectList expects; printable
// characters and backspace drive the local substring filter.
func (o *commandSelectorOverlay) HandleKey(key string) modelSelectorAction {
	switch key {
	case "up":
		o.list.HandleInput("\x1b[A")
		return modelSelectorNone
	case "down":
		o.list.HandleInput("\x1b[B")
		return modelSelectorNone
	case "enter":
		if _, ok := o.list.SelectedItem(); ok {
			return modelSelectorSelect
		}
		return modelSelectorNone
	case "esc", "ctrl+c":
		return modelSelectorCancel
	case "backspace":
		if o.filter != "" {
			runes := []rune(o.filter)
			o.filter = string(runes[:len(runes)-1])
			o.applyFilter()
		}
		return modelSelectorNone
	case "space":
		o.filter += " "
		o.applyFilter()
		return modelSelectorNone
	}
	if isPrintableKeyString(key) {
		o.filter += key
		o.applyFilter()
	}
	return modelSelectorNone
}

func (o *commandSelectorOverlay) applyFilter() {
	previous := ""
	if item, ok := o.list.SelectedItem(); ok {
		previous = item.Value
	}
	needle := strings.ToLower(strings.TrimSpace(o.filter))
	if needle == "" {
		o.list.SetItems(o.all)
	} else {
		matched := make([]tui.SelectItem, 0, len(o.all))
		for _, item := range o.all {
			haystack := strings.ToLower(item.Label + " " + item.Value + " " + item.Description)
			if strings.Contains(haystack, needle) {
				matched = append(matched, item)
			}
		}
		o.list.SetItems(matched)
	}
	if previous != "" {
		o.selectValue(previous)
	}
}

func (o *commandSelectorOverlay) SelectedValue() (string, bool) {
	item, ok := o.list.SelectedItem()
	if !ok {
		return "", false
	}
	return item.Value, true
}

// Render produces the overlay lines that replace the input region in View().
func (o *commandSelectorOverlay) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	title := o.title
	if o.filter != "" {
		title += "  filter: " + o.filter
	}
	lines := []string{o.titleStyle.Render(tui.TruncateToWidth(title, width, "..."))}
	lines = append(lines, o.list.Render(width)...)
	hint := o.hint
	if hint == "" {
		hint = "↑/↓ move · enter select · esc cancel · type to filter"
	}
	lines = append(lines, o.hintStyle.Render(tui.TruncateToWidth(hint, width, "...")))
	return lines
}

// listResolvedThemes returns the built-in (dark/light) themes plus any
// discovered/package/CLI themes, de-duplicated by name and sorted, for /theme.
func listResolvedThemes(resources ResourceLoader) []ResolvedTheme {
	out := []ResolvedTheme{}
	seen := map[string]bool{}
	for _, name := range []string{"dark", "light"} {
		theme, err := builtinResolvedTheme(name)
		if err != nil {
			continue
		}
		out = append(out, theme)
		seen[theme.Name] = true
	}
	extra := []ResolvedTheme{}
	for _, path := range resources.Themes {
		theme, err := loadResolvedThemeFromPath(path)
		if err != nil || theme.Name == "" || seen[theme.Name] {
			continue
		}
		seen[theme.Name] = true
		extra = append(extra, theme)
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Name < extra[j].Name })
	return append(out, extra...)
}

// openThemeSelector opens the navigable /theme picker. Selecting a theme applies
// it live and persists it as the global default.
func (m *interactiveModel) openThemeSelector() {
	if m.commandSelector != nil || m.modelSelector != nil {
		return
	}
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		m.setStatus(err.Error())
		return
	}
	// Snapshot Resources/Theme under a.mu — Reload (extension goroutine) writes
	// them while this runs on the Update goroutine.
	agent.mu.Lock()
	resources := agent.Resources
	currentTheme := agent.Theme.Name
	agent.mu.Unlock()
	themes := listResolvedThemes(resources)
	items := make([]tui.SelectItem, 0, len(themes))
	for _, theme := range themes {
		desc := "built-in"
		if theme.SourcePath != "" {
			desc = theme.SourcePath
		}
		items = append(items, tui.SelectItem{Value: theme.Name, Label: theme.Name, Description: desc})
	}
	overlay := newCommandSelectorOverlay(commandSelectorTheme, "Select theme", "↑/↓ move · enter apply · esc cancel · type to filter", items, currentTheme, m.styles)
	if overlay == nil {
		m.setStatus("No themes available")
		return
	}
	m.commandSelector = overlay
}

// openResumeSelector opens the navigable /resume session picker.
func (m *interactiveModel) openResumeSelector() {
	if m.commandSelector != nil || m.modelSelector != nil {
		return
	}
	if m.busy {
		m.setStatus("Can't switch session while a response is streaming")
		return
	}
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		m.setStatus(err.Error())
		return
	}
	sessions, err := ListSessions(agent.Session.CWD(), agent.Settings.SessionDir())
	if err != nil {
		m.setStatus(err.Error())
		return
	}
	current := agent.Session.SessionID()
	items := make([]tui.SelectItem, 0, len(sessions))
	for _, s := range sessions {
		label := normalizeSelectorLabel(firstNonEmpty(s.Name, s.Preview, s.ID))
		desc := s.UpdatedAt.Format("2006-01-02 15:04")
		if s.ID == current {
			desc += " · current"
		}
		items = append(items, tui.SelectItem{Value: s.ID, Label: label, Description: desc})
	}
	overlay := newCommandSelectorOverlay(commandSelectorResume, "Resume session", "↑/↓ move · enter resume · esc cancel · type to filter", items, current, m.styles)
	if overlay == nil {
		m.setStatus("No sessions to resume")
		return
	}
	m.commandSelector = overlay
}

// forkableSelectorItems builds the navigable entry list shared by /tree and
// /fork from the session's forkable user messages (the branch points).
// normalizeSelectorLabel collapses newlines/tabs/CRs to single spaces so a
// selector label can't corrupt width calculation or alignment (mirrors TS
// tree-selector getEntryDisplayText's /[\n\t]/g normalization; CR is added for
// Windows-authored content).
func normalizeSelectorLabel(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\n", " ", "\t", " ", "\r", " ").Replace(s))
}

func forkableSelectorItems(agent *AgentSession) []tui.SelectItem {
	messages := agent.GetUserMessagesForForking()
	items := make([]tui.SelectItem, 0, len(messages))
	for _, msg := range messages {
		label := normalizeSelectorLabel(msg.Text)
		if label == "" {
			label = msg.EntryID
		}
		items = append(items, tui.SelectItem{Value: msg.EntryID, Label: label, Description: msg.EntryID})
	}
	return items
}

// treeSelectorItems builds the /tree picker list from the FULL session tree —
// every entry (all branches, assistant/tool/label/summary rows, the current
// leaf) — not just the current-branch fork points. This mirrors TS
// showTreeSelector, which feeds TreeSelectorComponent from
// sessionManager.getTree(). The overlay is a simplified flat SelectList rather
// than the TS ASCII tree, but the data source is complete so no navigable entry
// is hidden. Entries are listed in append (chronological) order; the current
// leaf is tagged so the user can see where they are.
func treeSelectorItems(agent *AgentSession) []tui.SelectItem {
	if agent == nil || agent.Session == nil {
		return nil
	}
	entries := agent.Session.EntriesSnapshot()
	leaf := agent.Session.CurrentLeafID()
	items := make([]tui.SelectItem, 0, len(entries))
	for _, entry := range entries {
		if entry.ID == "" {
			continue
		}
		role := exportEntryRole(entry)
		preview := normalizeSelectorLabel(exportEntryPreview(entry))
		label := role
		if preview != "" {
			label = role + ": " + preview
		}
		desc := entry.ID
		if entry.ID == leaf {
			desc += " · current"
		}
		items = append(items, tui.SelectItem{Value: entry.ID, Label: label, Description: desc})
	}
	return items
}

// openTreeSelector opens the navigable /tree picker; selecting an entry
// navigates the session tree to it.
func (m *interactiveModel) openTreeSelector() {
	if m.commandSelector != nil || m.modelSelector != nil {
		return
	}
	if m.busy {
		m.setStatus("Can't navigate the tree while a response is streaming")
		return
	}
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		m.setStatus(err.Error())
		return
	}
	overlay := newCommandSelectorOverlay(commandSelectorTree, "Navigate session tree", "↑/↓ move · enter go · esc cancel · type to filter", treeSelectorItems(agent), agent.Session.CurrentLeafID(), m.styles)
	if overlay == nil {
		m.setStatus("No entries to navigate")
		return
	}
	m.commandSelector = overlay
}

// openForkSelector opens the navigable /fork picker; selecting an entry forks a
// new session at it.
func (m *interactiveModel) openForkSelector() {
	if m.commandSelector != nil || m.modelSelector != nil {
		return
	}
	if m.busy {
		m.setStatus("Can't fork while a response is streaming")
		return
	}
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		m.setStatus(err.Error())
		return
	}
	overlay := newCommandSelectorOverlay(commandSelectorFork, "Fork from entry", "↑/↓ move · enter fork · esc cancel · type to filter", forkableSelectorItems(agent), "", m.styles)
	if overlay == nil {
		m.setStatus("No entries to fork from")
		return
	}
	m.commandSelector = overlay
}

// themeApplyMsg carries the resolved theme back to the Update goroutine, where
// agent.Theme and m.styles (read during View) are safe to mutate.
type themeApplyMsg struct {
	theme ResolvedTheme
	err   error
}

// commandActionDoneMsg reports a self-contained selector action result (resume/
// tree/fork) for a status line; the session-replaced rebind + session events
// drive the actual UI refresh.
type commandActionDoneMsg struct {
	status string
	err    error
}

// handleCommandSelectorKey routes a key to the open overlay and acts on the
// result. Each action runs in the returned tea.Cmd off the Update goroutine.
func (m *interactiveModel) handleCommandSelectorKey(key string) tea.Cmd {
	if m.commandSelector == nil {
		return nil
	}
	kind := m.commandSelector.kind
	switch m.commandSelector.HandleKey(key) {
	case modelSelectorSelect:
		value, ok := m.commandSelector.SelectedValue()
		m.commandSelector = nil
		if !ok {
			return nil
		}
		switch kind {
		case commandSelectorTheme:
			return m.applyThemeSelection(value)
		case commandSelectorResume:
			return m.applyResumeSelection(value)
		case commandSelectorTree:
			return m.applyTreeSelection(value)
		case commandSelectorFork:
			return m.applyForkSelection(value)
		}
		return nil
	case modelSelectorCancel:
		m.commandSelector = nil
		m.setStatus("Selection cancelled.")
		return nil
	default:
		return nil
	}
}

// applyThemeSelection resolves the named theme + persists it off the Update
// goroutine, then hands the resolved theme back via themeApplyMsg so the live
// styles are rebuilt on the Update goroutine.
func (m *interactiveModel) applyThemeSelection(name string) tea.Cmd {
	return func() tea.Msg {
		agent, err := runtimeAgent(m.runtime)
		if err != nil {
			return themeApplyMsg{err: err}
		}
		// Reload (extension ctx.reload(), another goroutine) writes a.Resources
		// under a.mu, so snapshot it under the same lock before iterating.
		agent.mu.Lock()
		resources := agent.Resources
		agent.mu.Unlock()
		theme, found := resolveThemeByName(name, resources)
		if !found {
			// Mirror TS setTheme's failure feedback instead of silently
			// persisting a junk name and reporting the default as success.
			return themeApplyMsg{err: fmt.Errorf("theme not found: %s", name)}
		}
		if agent.Settings != nil {
			_ = agent.Settings.SetTheme(name)
		}
		return themeApplyMsg{theme: theme}
	}
}

// resolveThemeByName finds the named theme among built-ins + resources. The bool
// reports whether the name actually matched (false => the returned default is a
// fallback, not the requested theme).
func resolveThemeByName(name string, resources ResourceLoader) (ResolvedTheme, bool) {
	for _, theme := range listResolvedThemes(resources) {
		if theme.Name == name {
			return theme, true
		}
	}
	return DefaultResolvedTheme(), false
}

// applyTheme rebuilds the live interactive styles for the resolved theme. It
// runs on the Update goroutine (themeApplyMsg handler); the agent.Theme writes
// are guarded by a.mu because Reload (a different goroutine) also writes it.
func (m *interactiveModel) applyTheme(theme ResolvedTheme) {
	if agent, err := runtimeAgent(m.runtime); err == nil {
		agent.mu.Lock()
		agent.Theme = theme
		agent.mu.Unlock()
	}
	if m.sessionAgent != nil {
		m.sessionAgent.mu.Lock()
		m.sessionAgent.Theme = theme
		m.sessionAgent.mu.Unlock()
	}
	m.styles = interactiveThemeStylesFor(theme)
	m.refreshViewport()
	m.setStatus("Theme: " + theme.Name)
}

func (m *interactiveModel) applyResumeSelection(sessionID string) tea.Cmd {
	return func() tea.Msg {
		// The m.busy guard lives on the Update goroutine in openResumeSelector;
		// reading it here (a tea.Cmd goroutine) would race the Update writes.
		if _, err := m.runtime.SwitchSession(m.ctx, sessionID, SwitchSessionOptions{}); err != nil {
			return commandActionDoneMsg{err: err}
		}
		return commandActionDoneMsg{status: "Resumed session " + sessionID}
	}
}

// branchSummaryOptionLabels are the choices offered before navigating to a
// non-current entry, mirroring TS showTreeSelector's "Summarize branch?" select.
var branchSummaryOptionLabels = []string{"No summary", "Summarize", "Summarize with custom prompt"}

// branchSummaryOptionsFor maps a chosen "Summarize branch?" label (plus any
// custom instructions) to the NavigateTreeOptions handed to NavigateTree.
func branchSummaryOptionsFor(choice, customInstructions string) NavigateTreeOptions {
	switch choice {
	case "Summarize":
		return NavigateTreeOptions{Summarize: true}
	case "Summarize with custom prompt":
		return NavigateTreeOptions{Summarize: true, CustomInstructions: customInstructions}
	default: // "No summary"
		return NavigateTreeOptions{}
	}
}

// applyTreeSelection navigates the session tree to the chosen entry. Mirroring
// TS showTreeSelector: selecting the current leaf is a no-op ("Already at this
// point"); selecting any other entry first asks whether to summarize the
// abandoned branch (unless BranchSummarySkipPrompt suppresses the prompt) and
// threads the answer into NavigateTreeOptions. It runs in a tea.Cmd off the
// Update goroutine, so blocking on the select/input overlay channels is safe.
func (m *interactiveModel) applyTreeSelection(entryID string) tea.Cmd {
	return func() tea.Msg {
		agent, err := runtimeAgent(m.runtime)
		if err != nil {
			return commandActionDoneMsg{err: err}
		}
		// Selecting the point we are already at does nothing (matches TS).
		if entryID == agent.Session.CurrentLeafID() {
			return commandActionDoneMsg{status: "Already at this point"}
		}
		opts := NavigateTreeOptions{}
		skip := agent.Settings != nil && agent.Settings.BranchSummarySkipPrompt()
		if !skip {
			chosen, cancelled, perr := m.promptBranchSummary(m.ctx)
			if perr != nil {
				return commandActionDoneMsg{err: perr}
			}
			if cancelled {
				return commandActionDoneMsg{status: "Navigation cancelled."}
			}
			opts = chosen
		}
		if _, err := agent.NavigateTree(m.ctx, entryID, opts); err != nil {
			return commandActionDoneMsg{err: err}
		}
		return commandActionDoneMsg{status: "Navigated to entry " + entryID}
	}
}

// promptBranchSummary drives the "Summarize branch?" select (and, for the custom
// option, a follow-up instructions input) through the navigable extension-UI
// overlays. It returns the resolved NavigateTreeOptions, or cancelled=true when
// the user escapes out of the summary select. Cancelling the custom-prompt input
// loops back to the select, mirroring TS. Must only be called off the Update
// goroutine (it blocks on the overlay channels).
func (m *interactiveModel) promptBranchSummary(ctx context.Context) (NavigateTreeOptions, bool, error) {
	for {
		idx, ok, err := m.requestExtensionChoice(ctx, "Summarize branch?", branchSummaryOptionLabels)
		if err != nil {
			return NavigateTreeOptions{}, false, err
		}
		if !ok {
			return NavigateTreeOptions{}, true, nil
		}
		choice := branchSummaryOptionLabels[idx]
		if choice != "Summarize with custom prompt" {
			return branchSummaryOptionsFor(choice, ""), false, nil
		}
		custom, ok, err := m.requestExtensionInput(ctx, "Custom summarization instructions", "")
		if err != nil {
			return NavigateTreeOptions{}, false, err
		}
		if !ok {
			// Cancelled the instructions input — re-show the summary select.
			continue
		}
		return branchSummaryOptionsFor(choice, custom), false, nil
	}
}

// requestExtensionChoice posts a navigable select overlay (the same overlay
// /model and ctx.ui.select use) and blocks for the chosen index. ok=false on
// cancel. Like oauthSlashSelectPrompter it only runs from a tea.Cmd goroutine,
// so blocking on the overlay channel never stalls the Update loop.
func (m *interactiveModel) requestExtensionChoice(ctx context.Context, message string, labels []string) (int, bool, error) {
	if m == nil || m.post == nil {
		return 0, false, fmt.Errorf("interactive prompt is not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(labels) == 0 {
		return 0, false, nil
	}
	choices := make([]extensionUIChoice, 0, len(labels))
	for i, label := range labels {
		raw, err := json.Marshal(i)
		if err != nil {
			return 0, false, err
		}
		choices = append(choices, extensionUIChoice{Raw: raw, Label: label})
	}
	req := &interactiveExtensionUIRequest{
		Method:   "select",
		Prompt:   extensionUIPrompt{Message: message, Choices: choices},
		Response: make(chan extensionUIResult, 1),
	}
	raw, err := m.awaitExtensionUIRequest(ctx, req)
	if err != nil {
		return 0, false, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false, nil
	}
	var idx int
	if err := json.Unmarshal(raw, &idx); err != nil {
		return 0, false, err
	}
	if idx < 0 || idx >= len(labels) {
		return 0, false, nil
	}
	return idx, true, nil
}

// requestExtensionInput posts a single-line input overlay and blocks for the
// entered text. ok=false when the user escapes out. Same goroutine constraints
// as requestExtensionChoice.
func (m *interactiveModel) requestExtensionInput(ctx context.Context, message, placeholder string) (string, bool, error) {
	if m == nil || m.post == nil {
		return "", false, fmt.Errorf("interactive prompt is not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req := &interactiveExtensionUIRequest{
		Method:   "input",
		Prompt:   extensionUIPrompt{Message: message, Placeholder: placeholder},
		Response: make(chan extensionUIResult, 1),
	}
	raw, err := m.awaitExtensionUIRequest(ctx, req)
	if err != nil {
		return "", false, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, err
	}
	return value, true, nil
}

// awaitExtensionUIRequest posts an extension-UI overlay request and waits for
// its result, cancelling the pending overlay if ctx (or the model) is torn down
// first. Shared by requestExtensionChoice/requestExtensionInput.
func (m *interactiveModel) awaitExtensionUIRequest(ctx context.Context, req *interactiveExtensionUIRequest) (json.RawMessage, error) {
	m.post(interactiveExtensionUIRequestMsg{Request: req})
	select {
	case result := <-req.Response:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Result, nil
	case <-ctx.Done():
		m.post(interactiveExtensionUICancelMsg{Request: req})
		return nil, ctx.Err()
	case <-m.ctx.Done():
		m.post(interactiveExtensionUICancelMsg{Request: req})
		return nil, m.ctx.Err()
	}
}

func (m *interactiveModel) applyForkSelection(entryID string) tea.Cmd {
	return func() tea.Msg {
		if _, err := m.runtime.Fork(m.ctx, entryID, ForkOptions{Position: ForkPositionAt}); err != nil {
			return commandActionDoneMsg{err: err}
		}
		return commandActionDoneMsg{status: "Forked new session at " + entryID}
	}
}

// settingsRowKind identifies a row in the /settings editor.
type settingsRowKind int

const (
	settingsRowTheme settingsRowKind = iota
	settingsRowHideThinking
	settingsRowShowImages
	settingsRowAutoCompaction
	settingsRowAutoRetry
)

// settingsEditorOverlay is the navigable /settings editor that replaces the
// read-only JSON dump. Enter on the theme row opens the theme selector; Enter on
// a boolean row toggles + persists it in place. The toggles write only to
// settings (no session event), so mutating them on the Update goroutine is safe
// — unlike the model/session selectors there is no program.Send deadlock risk.
type settingsEditorOverlay struct {
	rows       []settingsRowKind
	selected   int
	settings   *SettingsManager
	agent      *AgentSession
	lastErr    error
	themeName  string
	titleStyle lipgloss.Style
	selStyle   lipgloss.Style
	rowStyle   lipgloss.Style
	hintStyle  lipgloss.Style
}

// newSettingsEditorOverlay builds the /settings editor. The agent is the live
// session whose auto-compaction/auto-retry toggles must change immediately (not
// only on next launch); it may be nil in tests that exercise persistence alone.
func newSettingsEditorOverlay(settings *SettingsManager, agent *AgentSession, themeName string, styles interactiveThemeStyles) *settingsEditorOverlay {
	return &settingsEditorOverlay{
		rows: []settingsRowKind{
			settingsRowTheme,
			settingsRowHideThinking,
			settingsRowShowImages,
			settingsRowAutoCompaction,
			settingsRowAutoRetry,
		},
		settings:   settings,
		agent:      agent,
		themeName:  themeName,
		titleStyle: styles.SelectorTitle,
		selStyle:   styles.SelectorSelected,
		rowStyle:   styles.Suggestion,
		hintStyle:  styles.SelectorHint,
	}
}

// recordErr remembers the most recent persistence failure so the parent can
// surface it (rather than silently swallowing the save error).
func (o *settingsEditorOverlay) recordErr(err error) {
	if err != nil {
		o.lastErr = err
	}
}

func boolOnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func (o *settingsEditorOverlay) rowLabel(kind settingsRowKind) string {
	switch kind {
	case settingsRowTheme:
		return "Theme: " + o.themeName + "  (enter to choose)"
	case settingsRowHideThinking:
		return "Hide thinking block: " + boolOnOff(o.settings.HideThinkingBlock())
	case settingsRowShowImages:
		return "Show inline images: " + boolOnOff(o.settings.ShowImages())
	case settingsRowAutoCompaction:
		return "Auto-compaction: " + boolOnOff(o.settings.AutoCompactionEnabled())
	case settingsRowAutoRetry:
		return "Auto-retry: " + boolOnOff(o.settings.AutoRetryEnabled())
	}
	return ""
}

// settingsEditorAction tells the parent what to do after a key.
type settingsEditorAction int

const (
	settingsEditorNone settingsEditorAction = iota
	settingsEditorOpenTheme
	settingsEditorCancel
	// settingsEditorToggled means a boolean row was flipped; the parent reads
	// lastErr to surface any persistence failure.
	settingsEditorToggled
)

func (o *settingsEditorOverlay) HandleKey(key string) settingsEditorAction {
	switch key {
	case "up":
		if o.selected > 0 {
			o.selected--
		}
	case "down":
		if o.selected < len(o.rows)-1 {
			o.selected++
		}
	case "esc", "ctrl+c":
		return settingsEditorCancel
	case "enter", "space":
		switch o.rows[o.selected] {
		case settingsRowTheme:
			return settingsEditorOpenTheme
		case settingsRowHideThinking:
			o.recordErr(o.settings.SetHideThinkingBlock(!o.settings.HideThinkingBlock()))
			return settingsEditorToggled
		case settingsRowShowImages:
			o.recordErr(o.settings.SetShowImages(!o.settings.ShowImages()))
			return settingsEditorToggled
		case settingsRowAutoCompaction:
			value := !o.settings.AutoCompactionEnabled()
			// Apply to the live session first so the change takes effect for the
			// current conversation regardless of whether persistence succeeds
			// (mirrors TS onAutoCompactChange -> session.setAutoCompactionEnabled).
			if o.agent != nil {
				o.agent.SetAutoCompactionEnabled(value)
			}
			o.recordErr(o.settings.SetAutoCompactionEnabled(value))
			return settingsEditorToggled
		case settingsRowAutoRetry:
			value := !o.settings.AutoRetryEnabled()
			if o.agent != nil {
				o.agent.SetAutoRetryEnabled(value)
			}
			o.recordErr(o.settings.SetAutoRetryEnabled(value))
			return settingsEditorToggled
		}
	}
	return settingsEditorNone
}

func (o *settingsEditorOverlay) Render(width int) []string {
	if width < 1 {
		width = 1
	}
	lines := []string{o.titleStyle.Render(tui.TruncateToWidth("Settings", width, "..."))}
	for i, kind := range o.rows {
		prefix := "  "
		style := o.rowStyle
		if i == o.selected {
			prefix = "> "
			style = o.selStyle
		}
		lines = append(lines, style.Render(tui.TruncateToWidth(prefix+o.rowLabel(kind), width, "...")))
	}
	lines = append(lines, o.hintStyle.Render(tui.TruncateToWidth("↑/↓ move · enter toggle/choose · esc close", width, "...")))
	return lines
}

// openSettingsEditor opens the navigable /settings editor (replaces the JSON
// dump in interactive mode).
func (m *interactiveModel) openSettingsEditor() {
	if m.settingsEditor != nil || m.commandSelector != nil || m.modelSelector != nil {
		return
	}
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		m.setStatus(err.Error())
		return
	}
	if agent.Settings == nil {
		m.setStatus("Settings are not available")
		return
	}
	agent.mu.Lock()
	themeName := agent.Theme.Name
	agent.mu.Unlock()
	m.settingsEditor = newSettingsEditorOverlay(agent.Settings, agent, themeName, m.styles)
}

// handleSettingsEditorKey routes a key to the settings editor. Toggles mutate in
// place (overlay stays open); choosing the theme row closes settings and opens
// the theme selector.
func (m *interactiveModel) handleSettingsEditorKey(key string) {
	if m.settingsEditor == nil {
		return
	}
	switch m.settingsEditor.HandleKey(key) {
	case settingsEditorOpenTheme:
		m.settingsEditor = nil
		m.openThemeSelector()
	case settingsEditorCancel:
		m.settingsEditor = nil
	case settingsEditorToggled:
		if err := m.settingsEditor.lastErr; err != nil {
			m.settingsEditor.lastErr = nil
			m.setStatus("Failed to save setting: " + err.Error())
		}
	}
}
