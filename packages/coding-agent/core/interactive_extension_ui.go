package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
	"github.com/guanshan/pi-go/packages/tui"
)

type interactiveExtensionUIRequestMsg struct {
	Request *interactiveExtensionUIRequest
}

type interactiveExtensionStatusMsg struct {
	Key  string
	Text *string
}

type interactiveExtensionUICancelMsg struct {
	Request *interactiveExtensionUIRequest
}

// interactiveExtensionUIStateMsg carries a fire-and-forget lightweight ctx.ui.*
// state mutation (setWorkingMessage/Visible/Indicator, setHiddenThinkingLabel,
// setTitle, pasteToEditor, setEditorText). It is applied on the Update goroutine
// so model/editor state is never touched from the script-request goroutine.
type interactiveExtensionUIStateMsg struct {
	Method string
	Params json.RawMessage
}

// interactiveExtensionGetEditorTextMsg requests the current (paste-expanded)
// editor text, read on the Update goroutine and returned over Response.
type interactiveExtensionGetEditorTextMsg struct {
	Response chan string
}

// interactiveExtensionEditorMsg requests a multi-line editor (ctx.ui.editor).
// The result (or error) is delivered over Response; Done refreshes the view.
type interactiveExtensionEditorMsg struct {
	Title    string
	Prefill  string
	Response chan extensionUIResult
}

// interactiveExtensionEditorDoneMsg fires after ctx.ui.editor's external editor
// exits; the result already went to the request's Response channel.
type interactiveExtensionEditorDoneMsg struct{}

type interactiveExtensionTriggerTurnMsg struct{}

type interactiveExtensionUserMessageMsg struct {
	Options  SendUserMessageOptions
	Response chan error
}

// interactiveExtensionCustomMessageMsg carries a display pi.sendMessage custom
// message to the Update goroutine, which renders it (once, via the registered
// renderer) and appends it to the transcript.
type interactiveExtensionCustomMessageMsg struct {
	CustomType string
	Content    any
	Details    any
}

type interactiveExtensionUIRequest struct {
	Method   string
	Prompt   extensionUIPrompt
	Input    string
	Selected int
	Response chan extensionUIResult
}

func (m *interactiveModel) extensionUIHandler(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if m == nil || m.post == nil {
		return nil, fmt.Errorf("pi.ui.%s requires an interactive TUI host, which is not available", method)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if method == "setStatus" {
		key, text := parseExtensionUIStatus(params)
		m.post(interactiveExtensionStatusMsg{Key: key, Text: text})
		return json.RawMessage("null"), nil
	}
	switch method {
	case "setWorkingMessage", "setWorkingVisible", "setWorkingIndicator",
		"setHiddenThinkingLabel", "setTitle", "pasteToEditor", "setEditorText", "setWidget":
		// Fire-and-forget state mutations applied on the Update goroutine.
		m.post(interactiveExtensionUIStateMsg{Method: method, Params: append(json.RawMessage(nil), params...)})
		return json.RawMessage("null"), nil
	case "getEditorText":
		resp := make(chan string, 1)
		m.post(interactiveExtensionGetEditorTextMsg{Response: resp})
		select {
		case v := <-resp:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			return b, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.ctx.Done():
			return nil, m.ctx.Err()
		}
	case "editor":
		var p struct {
			Title   string `json:"title"`
			Prefill string `json:"prefill"`
		}
		_ = json.Unmarshal(params, &p)
		resp := make(chan extensionUIResult, 1)
		m.post(interactiveExtensionEditorMsg{Title: p.Title, Prefill: p.Prefill, Response: resp})
		select {
		case result := <-resp:
			return result.Result, result.Err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.ctx.Done():
			return nil, m.ctx.Err()
		}
	}
	req := &interactiveExtensionUIRequest{
		Method:   method,
		Prompt:   parseExtensionUIPrompt(method, params),
		Response: make(chan extensionUIResult, 1),
	}
	if method == "input" {
		req.Input = req.Prompt.DefaultValue
	}
	m.post(interactiveExtensionUIRequestMsg{Request: req})
	select {
	case result := <-req.Response:
		return result.Result, result.Err
	case <-ctx.Done():
		m.post(interactiveExtensionUICancelMsg{Request: req})
		return nil, ctx.Err()
	case <-m.ctx.Done():
		m.post(interactiveExtensionUICancelMsg{Request: req})
		return nil, m.ctx.Err()
	}
}

// applyExtensionUIState mutates lightweight ctx.ui.* state on the Update
// goroutine. Each setter mirrors the TS interactive ExtensionUIContext: a nil
// pointer / omitted JSON field means "restore default" (TS `?? default`), while
// setWorkingIndicator distinguishes an omitted argument (restore the default
// spinner) from an explicit empty frame array (hide the indicator).
func (m *interactiveModel) applyExtensionUIState(method string, params json.RawMessage) {
	switch method {
	case "setWorkingMessage":
		var p struct {
			Message *string `json:"message"`
		}
		_ = json.Unmarshal(params, &p)
		m.workingMessage = p.Message
	case "setWorkingVisible":
		var p struct {
			Visible bool `json:"visible"`
		}
		_ = json.Unmarshal(params, &p)
		m.workingHidden = !p.Visible
	case "setWorkingIndicator":
		var p struct {
			Frames     *[]string `json:"frames"`
			IntervalMs *int      `json:"intervalMs"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Frames == nil {
			m.workingIndicatorSet = false
			m.workingIndicatorFrames = nil
		} else {
			m.workingIndicatorSet = true
			m.workingIndicatorFrames = *p.Frames
		}
		if p.IntervalMs != nil {
			m.workingIndicatorInterval = *p.IntervalMs
		}
	case "setHiddenThinkingLabel":
		var p struct {
			Label *string `json:"label"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Label == nil {
			m.hiddenThinkingLabel = ""
		} else {
			m.hiddenThinkingLabel = *p.Label
		}
	case "setTitle":
		var p struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(params, &p)
		m.windowTitle = p.Title
	case "pasteToEditor":
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(params, &p)
		m.handlePaste(p.Text)
	case "setEditorText":
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(params, &p)
		m.input.SetValue(p.Text)
		m.clearPastes()
		m.historyIndex = -1
		m.autocompleteIndex = 0
	case "setWidget":
		var p struct {
			Key       string    `json:"key"`
			Lines     *[]string `json:"lines"`
			Placement string    `json:"placement"`
		}
		_ = json.Unmarshal(params, &p)
		m.setExtensionWidget(p.Key, p.Lines, p.Placement)
	}
}

// interactiveMaxWidgetLines caps a single extension widget's rendered lines,
// mirroring TS InteractiveMode.MAX_WIDGET_LINES.
const interactiveMaxWidgetLines = 10

// setExtensionWidget stores (or with lines==nil removes) a plain-text widget for
// key. Mirroring TS setExtensionWidget, the key is removed from BOTH placement
// maps first so re-setting a key with a different placement moves it. Content is
// capped to interactiveMaxWidgetLines with a trailing muted truncation marker.
func (m *interactiveModel) setExtensionWidget(key string, lines *[]string, placement string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if m.extensionWidgetsAbove == nil {
		m.extensionWidgetsAbove = map[string][]string{}
	}
	if m.extensionWidgetsBelow == nil {
		m.extensionWidgetsBelow = map[string][]string{}
	}
	delete(m.extensionWidgetsAbove, key)
	delete(m.extensionWidgetsBelow, key)
	if lines == nil {
		return
	}
	content := *lines
	if len(content) > interactiveMaxWidgetLines {
		content = append(append([]string(nil), content[:interactiveMaxWidgetLines]...), "... (widget truncated)")
	} else {
		content = append([]string(nil), content...)
	}
	if placement == "belowEditor" {
		m.extensionWidgetsBelow[key] = content
	} else {
		m.extensionWidgetsAbove[key] = content
	}
}

// renderExtensionWidgets flattens the widgets for a placement into width-truncated
// lines, ordered by key (Go maps are unordered; deterministic sort is an
// unobservable cross-process divergence from TS insertion order).
func (m *interactiveModel) renderExtensionWidgets(placement string) []string {
	source := m.extensionWidgetsAbove
	if placement == "belowEditor" {
		source = m.extensionWidgetsBelow
	}
	if len(source) == 0 {
		return nil
	}
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []string
	for _, key := range keys {
		for _, line := range source[key] {
			out = append(out, tui.TruncateToWidth(line, max(1, m.width), "..."))
		}
	}
	return out
}

// runExtensionEditor backs ctx.ui.editor(title, prefill). It writes prefill to a
// temp file, suspends the TUI to run $VISUAL/$EDITOR (mirroring openExternalEditor),
// and delivers the edited text (or an error) over resp. When no editor is
// configured it resolves the request with an error instead of leaving the script
// hanging. The title is not shown when delegating to an external editor.
func (m *interactiveModel) runExtensionEditor(title, prefill string, resp chan extensionUIResult) tea.Cmd {
	reply := func(result extensionUIResult) {
		if resp != nil {
			resp <- result
		}
	}
	_ = title
	editorCmd := strings.TrimSpace(firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR")))
	if editorCmd == "" {
		reply(extensionUIResult{Err: fmt.Errorf("no editor configured; set $VISUAL or $EDITOR")})
		return nil
	}
	parts := strings.Fields(editorCmd)
	if len(parts) == 0 {
		reply(extensionUIResult{Err: fmt.Errorf("no editor configured; set $VISUAL or $EDITOR")})
		return nil
	}
	tmp, err := os.CreateTemp("", "pi-ext-editor-*.pi.md")
	if err != nil {
		reply(extensionUIResult{Err: err})
		return nil
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(prefill); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		reply(extensionUIResult{Err: err})
		return nil
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		reply(extensionUIResult{Err: err})
		return nil
	}
	cmd := exec.Command(parts[0], append(parts[1:], tmpPath)...)
	return tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		defer os.Remove(tmpPath)
		if execErr != nil {
			reply(extensionUIResult{Err: execErr})
			return interactiveExtensionEditorDoneMsg{}
		}
		raw, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			reply(extensionUIResult{Err: readErr})
			return interactiveExtensionEditorDoneMsg{}
		}
		text := strings.TrimSuffix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		b, marshalErr := json.Marshal(text)
		if marshalErr != nil {
			reply(extensionUIResult{Err: marshalErr})
			return interactiveExtensionEditorDoneMsg{}
		}
		reply(extensionUIResult{Result: b})
		return interactiveExtensionEditorDoneMsg{}
	})
}

func (m *interactiveModel) extensionTriggerTurn(ctx context.Context) error {
	if m == nil || m.post == nil {
		return fmt.Errorf("pi.sendMessage triggerTurn requires an interactive TUI host, which is not available")
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	m.post(interactiveExtensionTriggerTurnMsg{})
	return nil
}

func (m *interactiveModel) extensionUserMessage(ctx context.Context, opts SendUserMessageOptions) error {
	if m == nil || m.post == nil {
		return fmt.Errorf("pi.sendUserMessage requires an interactive TUI host, which is not available")
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	response := make(chan error, 1)
	m.post(interactiveExtensionUserMessageMsg{Options: opts, Response: response})
	select {
	case err := <-response:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-m.ctx.Done():
		return m.ctx.Err()
	}
}

func (m *interactiveModel) handleExtensionTriggerTurn() tea.Cmd {
	if m.busy {
		return m.runQueuePrompt("", nil, StreamingFollowUp)
	}
	m.busy = true
	m.busyKind = interactiveBusyAgent
	return m.runAgentPrompt("", nil)
}

// handleExtensionCustomMessage renders a display pi.sendMessage custom message
// once (via the registered renderer, with a short timeout) and appends it to the
// transcript. The renderer round-trip happens here on receipt, not on the
// per-keystroke render path; when no renderer is registered (or it returns
// empty/errors) CustomLines stays nil and the transcript falls back to the default
// [customType] + markdown rendering.
func (m *interactiveModel) handleExtensionCustomMessage(msg interactiveExtensionCustomMessageMsg) {
	entry := interactiveMessage{
		Role:        interactiveRoleSystem,
		Kind:        interactiveMessageCustom,
		CustomType:  msg.CustomType,
		CustomText:  customMessageDisplayText(msg.Content),
		CustomLines: m.renderCustomMessageOnReceipt(msg),
	}
	m.messages = append(m.messages, entry)
	m.autoScroll = true
}

func (m *interactiveModel) renderCustomMessageOnReceipt(msg interactiveExtensionCustomMessageMsg) []string {
	agent, err := runtimeAgent(m.runtime)
	if err != nil || agent == nil || agent.extensionRuntime == nil {
		return nil
	}
	width := m.width
	if width <= 0 {
		width = interactiveDefaultWidth
	}
	ctx, cancel := context.WithTimeout(m.ctx, 250*time.Millisecond)
	defer cancel()
	result, handled, rerr := agent.extensionRuntime.RenderMessage(ctx, coreext.MessageRenderRequest{
		CustomType: msg.CustomType,
		Content:    msg.Content,
		Display:    true,
		Details:    msg.Details,
		Expanded:   m.toolsExpanded,
		Width:      max(1, width-2),
	})
	if rerr != nil || !handled || len(result.Lines) == 0 {
		return nil
	}
	return result.Lines
}

// customMessageDisplayText extracts plain text from a custom message's content for
// the default fallback rendering, mirroring TS: string -> as-is; array of {text}
// parts -> joined with newlines; otherwise empty.
func customMessageDisplayText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			switch part := item.(type) {
			case string:
				parts = append(parts, part)
			case map[string]any:
				if text, ok := part["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func (m *interactiveModel) handleExtensionUserMessage(opts SendUserMessageOptions, response chan error) tea.Cmd {
	respond := func(err error) {
		if response != nil {
			response <- err
		}
	}
	text := opts.Text
	if strings.TrimSpace(text) == "" && len(opts.Images) == 0 {
		respond(fmt.Errorf("message is required"))
		return nil
	}
	displayText := strings.TrimSpace(text)
	if displayText == "" {
		displayText = "[image input]"
	}
	if m.busy {
		if opts.StreamingBehavior == "" {
			respond(fmt.Errorf("agent is already streaming; use steer or follow_up"))
			return nil
		}
		m.appendMessage(interactiveRoleUser, displayText)
		switch opts.StreamingBehavior {
		case StreamingFollowUp:
			m.queuedFollowUp = append(m.queuedFollowUp, text)
			m.setStatus("Queued as follow-up.")
		default:
			opts.StreamingBehavior = StreamingSteer
			m.queuedSteering = append(m.queuedSteering, text)
			m.setStatus("Sent as steering input.")
		}
		respond(nil)
		return m.runQueuePrompt(text, opts.Images, opts.StreamingBehavior)
	}
	m.appendMessage(interactiveRoleUser, displayText)
	m.busy = true
	m.busyKind = interactiveBusyAgent
	respond(nil)
	return m.runAgentPrompt(text, opts.Images)
}

// oauthSlashPrompter returns a slashPrompter that drives /login OAuth prompts
// through the existing extension-UI input overlay. It is invoked only from the
// runSlashCommand tea.Cmd goroutine, so blocking on the overlay's result channel
// is safe (it never runs on the Update goroutine). The overlay request is posted
// via m.post (goroutine-safe) and the model field is never mutated directly from
// here. esc/ctrl+c resolves the channel with a JSON null, which aborts the login
// with an error so a cancelled prompt cannot hang the command goroutine.
func (m *interactiveModel) oauthSlashPrompter(ctx context.Context) slashPrompter {
	return func(prompt ai.OAuthPrompt) (string, error) {
		if m == nil || m.post == nil {
			return "", fmt.Errorf("interactive prompt is not available")
		}
		if ctx == nil {
			ctx = context.Background()
		}
		req := &interactiveExtensionUIRequest{
			Method: "input",
			Prompt: extensionUIPrompt{
				Message:     firstNonEmpty(prompt.Message, "Enter value"),
				Placeholder: prompt.Placeholder,
			},
			Response: make(chan extensionUIResult, 1),
		}
		m.post(interactiveExtensionUIRequestMsg{Request: req})
		var raw json.RawMessage
		select {
		case result := <-req.Response:
			if result.Err != nil {
				return "", result.Err
			}
			raw = result.Result
		case <-ctx.Done():
			m.post(interactiveExtensionUICancelMsg{Request: req})
			return "", ctx.Err()
		case <-m.ctx.Done():
			m.post(interactiveExtensionUICancelMsg{Request: req})
			return "", m.ctx.Err()
		}
		// The input overlay resolves esc/ctrl+c to a JSON null; treat that as a
		// cancellation that aborts the OAuth flow (unless the prompt explicitly
		// allows an empty answer, in which case an empty string flows through).
		if len(raw) == 0 || string(raw) == "null" {
			if prompt.AllowEmpty {
				return "", nil
			}
			return "", fmt.Errorf("login cancelled")
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value == "" && !prompt.AllowEmpty {
			return "", fmt.Errorf("login cancelled")
		}
		return value, nil
	}
}

func (r *interactiveExtensionUIRequest) respond(result json.RawMessage, err error) {
	if r == nil || r.Response == nil {
		return
	}
	r.Response <- extensionUIResult{Result: result, Err: err}
}

func (m *interactiveModel) enqueueExtensionUIRequest(req *interactiveExtensionUIRequest) {
	if req == nil {
		return
	}
	switch req.Method {
	case "notify":
		level := firstNonEmpty(req.Prompt.Level, "info")
		m.appendMessage(interactiveRoleSystem, fmt.Sprintf("[%s] %s", level, req.Prompt.Message))
		req.respond(json.RawMessage("null"), nil)
	case "confirm", "select", "input":
		if req.Method == "select" && len(req.Prompt.Choices) == 0 {
			req.respond(nil, fmt.Errorf("pi.ui.select requires at least one choice"))
			return
		}
		if m.extensionUI == nil {
			m.extensionUI = req
		} else {
			m.extensionUIQueue = append(m.extensionUIQueue, req)
		}
	default:
		req.respond(nil, fmt.Errorf("pi.ui.%s is not supported in this host", req.Method))
	}
}

func (m *interactiveModel) finishExtensionUI(result json.RawMessage, err error) {
	req := m.extensionUI
	if req == nil {
		return
	}
	m.extensionUI = nil
	req.respond(result, err)
	for m.extensionUI == nil && len(m.extensionUIQueue) > 0 {
		next := m.extensionUIQueue[0]
		copy(m.extensionUIQueue, m.extensionUIQueue[1:])
		m.extensionUIQueue[len(m.extensionUIQueue)-1] = nil
		m.extensionUIQueue = m.extensionUIQueue[:len(m.extensionUIQueue)-1]
		m.enqueueExtensionUIRequest(next)
	}
}

func (m *interactiveModel) cancelExtensionUIRequest(req *interactiveExtensionUIRequest) {
	if req == nil {
		return
	}
	if m.extensionUI == req {
		m.extensionUI = nil
		for m.extensionUI == nil && len(m.extensionUIQueue) > 0 {
			next := m.extensionUIQueue[0]
			copy(m.extensionUIQueue, m.extensionUIQueue[1:])
			m.extensionUIQueue[len(m.extensionUIQueue)-1] = nil
			m.extensionUIQueue = m.extensionUIQueue[:len(m.extensionUIQueue)-1]
			m.enqueueExtensionUIRequest(next)
		}
		return
	}
	for i, queued := range m.extensionUIQueue {
		if queued == req {
			copy(m.extensionUIQueue[i:], m.extensionUIQueue[i+1:])
			m.extensionUIQueue[len(m.extensionUIQueue)-1] = nil
			m.extensionUIQueue = m.extensionUIQueue[:len(m.extensionUIQueue)-1]
			return
		}
	}
}

func (m *interactiveModel) handleExtensionUIKey(msg tea.KeyPressMsg) tea.Cmd {
	req := m.extensionUI
	if req == nil {
		return nil
	}
	key := msg.String()
	switch req.Method {
	case "confirm":
		switch strings.ToLower(key) {
		case "y", "yes":
			m.finishExtensionUI(json.RawMessage("true"), nil)
		case "n", "no", "enter", "esc", "ctrl+c":
			m.finishExtensionUI(json.RawMessage("false"), nil)
		}
	case "select":
		switch key {
		case "up":
			if req.Selected > 0 {
				req.Selected--
			}
		case "down":
			if req.Selected < len(req.Prompt.Choices)-1 {
				req.Selected++
			}
		case "enter":
			m.finishExtensionUI(req.Prompt.Choices[req.Selected].Raw, nil)
		case "esc", "ctrl+c":
			m.finishExtensionUI(json.RawMessage("null"), nil)
		default:
			if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
				index := int(key[0] - '1')
				if index >= 0 && index < len(req.Prompt.Choices) {
					req.Selected = index
					m.finishExtensionUI(req.Prompt.Choices[index].Raw, nil)
				}
			}
		}
	case "input":
		switch key {
		case "enter":
			result, err := extensionUIJSON(req.Input)
			m.finishExtensionUI(result, err)
		case "esc", "ctrl+c":
			m.finishExtensionUI(json.RawMessage("null"), nil)
		case "backspace":
			if req.Input != "" {
				runes := []rune(req.Input)
				req.Input = string(runes[:len(runes)-1])
			}
		default:
			if text := msg.Key().Text; text != "" {
				req.Input += text
			}
		}
	}
	return nil
}

func (m *interactiveModel) renderExtensionUI(width int) []string {
	req := m.extensionUI
	if req == nil {
		return nil
	}
	lineWidth := max(1, width-2)
	lines := []string{
		m.styles.System.Render("ctx.ui."+req.Method) + " " + tui.TruncateToWidth(req.Prompt.Message, lineWidth, "..."),
	}
	if req.Prompt.Detail != "" {
		lines = append(lines, m.styles.Footer.Render(tui.TruncateToWidth(req.Prompt.Detail, lineWidth, "...")))
	}
	switch req.Method {
	case "confirm":
		lines = append(lines, m.styles.Suggestion.Render("  y = yes    n/enter/esc = no"))
	case "select":
		for i, choice := range req.Prompt.Choices {
			marker := "  "
			if i == req.Selected {
				marker = "> "
			}
			style := m.styles.Suggestion
			if i == req.Selected {
				style = m.styles.SelectorSelected
			}
			lines = append(lines, style.Render(tui.TruncateToWidth(fmt.Sprintf("%s%d. %s", marker, i+1, choice.Label), lineWidth, "...")))
		}
	case "input":
		value := req.Input
		if value == "" {
			value = req.Prompt.Placeholder
		}
		lines = append(lines, m.styles.Input.Width(lineWidth).Render("> "+value))
	}
	return lines
}

// workingFooterStatus renders the busy indicator for the footer, honoring the
// lightweight ctx.ui working-state setters. A hidden indicator (setWorkingVisible
// (false)) yields "", which the footer omits entirely. The Go footer is a single
// static line, so an animated indicator collapses to its first frame; an explicit
// empty frame array hides the glyph.
func (m *interactiveModel) workingFooterStatus() string {
	if m.workingHidden {
		return ""
	}
	label := "working"
	if m.workingMessage != nil {
		label = *m.workingMessage
	}
	if glyph := m.workingIndicatorGlyph(); glyph != "" {
		label = glyph + " " + label
	}
	if m.busyKind != interactiveBusyNone {
		label += ":" + string(m.busyKind)
	}
	return label
}

// workingIndicatorGlyph returns the static glyph shown before the working label.
// The default spinner is not animated in the line footer (empty glyph); an
// explicit empty frame array hides it; custom frames show their first frame.
func (m *interactiveModel) workingIndicatorGlyph() string {
	if !m.workingIndicatorSet || len(m.workingIndicatorFrames) == 0 {
		return ""
	}
	return m.workingIndicatorFrames[0]
}

func (m *interactiveModel) setExtensionStatus(key string, text *string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if text == nil {
		delete(m.extensionStatuses, key)
		return
	}
	value := strings.TrimSpace(*text)
	if value == "" {
		delete(m.extensionStatuses, key)
		return
	}
	if m.extensionStatuses == nil {
		m.extensionStatuses = map[string]string{}
	}
	m.extensionStatuses[key] = sanitizeStatusText(value)
}

func (m *interactiveModel) extensionStatusValues() []string {
	if len(m.extensionStatuses) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m.extensionStatuses))
	for key := range m.extensionStatuses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := strings.TrimSpace(m.extensionStatuses[key]); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func sanitizeStatusText(text string) string {
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	return strings.Join(strings.Fields(text), " ")
}

func (m *interactiveModel) bindExtensionUIHandler() {
	if m == nil || m.sessionAgent == nil || m.post == nil {
		return
	}
	m.sessionAgent.SetExtensionUIHandler(m.extensionUIHandler)
	m.sessionAgent.SetExtensionTriggerTurnHandler(m.extensionTriggerTurn)
	m.sessionAgent.SetExtensionUserMessageHandler(m.extensionUserMessage)
	m.sessionAgent.SetExtensionCustomMessageHandler(m.extensionCustomMessage)
}

// extensionCustomMessage is the host callback for a display pi.sendMessage custom
// message. It runs on the extension request goroutine and only posts to the model;
// the renderer round-trip + append happen on the Update goroutine.
func (m *interactiveModel) extensionCustomMessage(customType string, content any, details any) {
	if m == nil || m.post == nil {
		return
	}
	m.post(interactiveExtensionCustomMessageMsg{CustomType: customType, Content: content, Details: details})
}
