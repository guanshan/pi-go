package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	agentcore "github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
	"github.com/guanshan/pi-go/packages/tui"
)

const (
	interactiveDefaultWidth  = 100
	interactiveDefaultHeight = 28
)

type interactiveRole string

const (
	interactiveRoleUser      interactiveRole = "user"
	interactiveRoleAssistant interactiveRole = "assistant"
	interactiveRoleTool      interactiveRole = "tool"
	interactiveRoleSystem    interactiveRole = "system"
	interactiveRoleError     interactiveRole = "error"
)

type interactiveMessage struct {
	Role interactiveRole
	Text string
}

type interactiveAgentEventMsg struct {
	Event ai.Event
}

type interactivePromptDoneMsg struct {
	Err error
}

type interactiveCommandDoneMsg struct {
	Stdout string
	Stderr string
	Err    error
	Quit   bool
}

type interactiveQueueDoneMsg struct {
	Err error
}

type interactiveAbortDoneMsg struct {
	Err error
}

type interactiveSessionEventMsg struct {
	Event SessionEvent
}

type modelCycleDoneMsg struct {
	ok     bool
	scoped bool
	busy   bool
}

// thinkingCycleDoneMsg reports the outcome of an off-goroutine CycleThinkingLevel
// so the "no other thinking levels" / "while streaming" feedback is surfaced on
// the Update goroutine. A successful change rides the emitted
// ThinkingLevelChangedEvent status, so ok==true needs no extra status.
type thinkingCycleDoneMsg struct {
	ok   bool
	busy bool
}

type modelSelectDoneMsg struct {
	Err error
}

type interactiveExtensionUIRequestMsg struct {
	Request *interactiveExtensionUIRequest
}

type interactiveExtensionUICancelMsg struct {
	Request *interactiveExtensionUIRequest
}

type interactiveExtensionUIRequest struct {
	Method   string
	Prompt   extensionUIPrompt
	Input    string
	Selected int
	Response chan extensionUIResult
}

type interactiveBusyKind string

const (
	interactiveBusyNone    interactiveBusyKind = ""
	interactiveBusyAgent   interactiveBusyKind = "agent"
	interactiveBusyCommand interactiveBusyKind = "command"
)

type interactiveQueuedInput struct {
	Text   string
	Images []ai.ContentBlock
}

type interactiveModel struct {
	ctx                context.Context
	runtime            *AgentSessionRuntime
	post               func(tea.Msg)
	input              textarea.Model
	viewport           viewport.Model
	messages           []interactiveMessage
	busy               bool
	busyKind           interactiveBusyKind
	cyclingModel       bool
	cyclingThinking    bool
	width              int
	height             int
	assistantSlot      int
	autoScroll         bool
	initial            string
	initialImages      []ai.ContentBlock
	initialQueue       []string
	localQueue         []interactiveQueuedInput
	queuedSteering     []string
	queuedFollowUp     []string
	lastCtrlC          time.Time
	lastEscape         time.Time
	statusMessage      string
	toolSlots          map[string]int
	sessionUnsubscribe func()
	commandCancel      context.CancelFunc
	modelSelector      *modelSelectorOverlay
	extensionUI        *interactiveExtensionUIRequest
	extensionUIQueue   []*interactiveExtensionUIRequest
	history            []string
	historyIndex       int
	autocompleteIndex  int
	sessionAgent       *AgentSession
}

// beginCommand derives a per-command child context from m.ctx and records its
// cancel func so Escape can interrupt a running slash/bash command. The returned
// context is captured by the command goroutine; m.commandCancel is only touched
// from the Bubble Tea update loop.
func (m *interactiveModel) beginCommand() context.Context {
	m.clearCommandCancel()
	ctx, cancel := context.WithCancel(m.ctx)
	m.commandCancel = cancel
	return ctx
}

// clearCommandCancel cancels and forgets any pending per-command context. Calling
// cancel after the command already finished is a no-op that just releases the
// context's resources.
func (m *interactiveModel) clearCommandCancel() {
	if m.commandCancel != nil {
		m.commandCancel()
		m.commandCancel = nil
	}
}

var (
	interactiveHeaderStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C8A99"))
	interactiveUserStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#70A5FF")).Bold(true)
	interactiveAssistantStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#D6DEE8"))
	interactiveToolStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#C6A15B"))
	interactiveSystemStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#87B58B"))
	interactiveErrorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
	interactiveFooterStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C8A99"))
	interactiveSuggestionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9AA7B4"))
	interactiveInputStyle      = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).BorderForeground(lipgloss.Color("#4F5B66")).Padding(0, 1)
)

var interactiveSlashCommands = []string{
	"login",
	"logout",
	"model",
	"thinking",
	"scoped-models",
	"settings",
	"resume",
	"new",
	"import",
	"name",
	"session",
	"compact",
	"export",
	"copy",
	"share",
	"tree",
	"fork",
	"clone",
	"changelog",
	"reload",
	"debug",
	"help",
	"hotkeys",
	"quit",
	"exit",
	"q",
}

func shouldRunBubbleInteractive(stdin io.Reader, stdout io.Writer) bool {
	if strings.EqualFold(os.Getenv("PI_GO_TUI"), "0") {
		return false
	}
	return isTerminalFile(stdin) && isTerminalFile(stdout)
}

func isTerminalFile(value any) bool {
	file, ok := value.(*os.File)
	if !ok || file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func runBubbleInteractiveMode(ctx context.Context, runtime *AgentSessionRuntime, initial string, images []ai.ContentBlock, stdin io.Reader, stdout, stderr io.Writer, remaining ...string) error {
	model, err := newInteractiveModel(ctx, runtime, initial, images, remaining...)
	if err != nil {
		return err
	}
	program := tea.NewProgram(model,
		tea.WithContext(ctx),
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
	)
	model.post = program.Send
	model.bindExtensionUIHandler()
	_, err = program.Run()
	if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	if err != nil && stderr != nil {
		fmt.Fprintln(stderr, "TUI error:", err)
	}
	return err
}

func newInteractiveModel(ctx context.Context, runtime *AgentSessionRuntime, initial string, images []ai.ContentBlock, remaining ...string) (*interactiveModel, error) {
	agent, err := runtimeAgent(runtime)
	if err != nil {
		return nil, err
	}
	input := textarea.New()
	input.Prompt = "> "
	input.Placeholder = "Ask pi-go or type /help"
	input.ShowLineNumbers = false
	input.EndOfBufferCharacter = ' '
	input.DynamicHeight = true
	input.MinHeight = 1
	input.MaxHeight = 6
	input.SetWidth(interactiveDefaultWidth - 4)
	input.SetHeight(1)

	vp := viewport.New(
		viewport.WithWidth(interactiveDefaultWidth),
		viewport.WithHeight(interactiveDefaultHeight-6),
	)
	vp.SoftWrap = true
	vp.FillHeight = true
	vp.MouseWheelEnabled = true

	model := &interactiveModel{
		ctx:           ctx,
		runtime:       runtime,
		input:         input,
		viewport:      vp,
		width:         interactiveDefaultWidth,
		height:        interactiveDefaultHeight,
		assistantSlot: -1,
		autoScroll:    true,
		initial:       initial,
		initialImages: append([]ai.ContentBlock(nil), images...),
		initialQueue:  append([]string(nil), remaining...),
		toolSlots:     map[string]int{},
		historyIndex:  -1,
	}
	model.bindSession(agent)
	if runtime != nil {
		runtime.SetBeforeSessionInvalidate(func() {
			model.unbindSession()
		})
		runtime.SetRebindSession(func(agent *AgentSession) error {
			model.bindSession(agent)
			return nil
		})
	}
	currentModel := agent.CurrentModel()
	model.messages = append(model.messages, interactiveMessage{
		Role: interactiveRoleSystem,
		Text: fmt.Sprintf("pi-go %s  cwd=%s  model=%s/%s", Version, agent.Session.CWD(), currentModel.Provider, currentModel.ID),
	})
	model.refreshViewport()
	return model, nil
}

func (m *interactiveModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus()}
	if strings.TrimSpace(m.initial) != "" || len(m.initialImages) > 0 {
		text := strings.TrimSpace(m.initial)
		if text == "" {
			text = "[image input]"
		}
		m.appendMessage(interactiveRoleUser, text)
		m.busy = true
		m.busyKind = interactiveBusyAgent
		cmds = append(cmds, m.runAgentPrompt(m.initial, m.initialImages))
	} else if next, ok := m.popInitialQueue(); ok {
		m.appendMessage(interactiveRoleUser, next)
		m.busy = true
		m.busyKind = interactiveBusyAgent
		cmds = append(cmds, m.runAgentPrompt(next, nil))
	}
	m.refreshViewport()
	return tea.Batch(cmds...)
}

func (m *interactiveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.extensionUI != nil {
			cmd = m.handleExtensionUIKey(msg)
			m.refreshViewport()
			return m, cmd
		}
		if m.modelSelector != nil {
			cmd = m.handleModelSelectorKey(msg.String())
			m.refreshViewport()
			return m, cmd
		}
		switch msg.String() {
		case "ctrl+c":
			cmd = m.handleCtrlC()
			m.refreshViewport()
			return m, cmd
		case "ctrl+d":
			if strings.TrimSpace(m.input.Value()) == "" {
				return m, tea.Quit
			}
		case "esc":
			cmd = m.handleEscape()
			m.refreshViewport()
			return m, cmd
		case "pgup":
			m.autoScroll = false
			m.viewport.PageUp()
			return m, nil
		case "pgdown":
			m.viewport.PageDown()
			m.autoScroll = m.viewport.AtBottom()
			return m, nil
		case "end":
			m.autoScroll = true
			m.viewport.GotoBottom()
			return m, nil
		case "ctrl+l":
			m.openModelSelector()
			m.refreshViewport()
			return m, nil
		case "ctrl+p":
			cmd = m.cycleModel(false)
			m.refreshViewport()
			return m, cmd
		case "ctrl+shift+p", "shift+ctrl+p":
			cmd = m.cycleModel(true)
			m.refreshViewport()
			return m, cmd
		case "shift+tab":
			cmd = m.cycleThinking()
			m.refreshViewport()
			return m, cmd
		case "up":
			if m.navigateAutocomplete(-1) {
				m.refreshViewport()
				return m, nil
			}
			// Browse prompt history when the editor is empty, or when already
			// browsing and the cursor is on the first line (TS editor.ts cursorUp).
			if m.editorEmpty() || (m.historyIndex > -1 && m.editorOnFirstLine()) {
				m.navigateHistory(-1)
				m.refreshViewport()
				return m, nil
			}
		case "down":
			if m.navigateAutocomplete(1) {
				m.refreshViewport()
				return m, nil
			}
			// Continue browsing history forward only while already browsing and on
			// the last line (TS editor.ts cursorDown); otherwise fall through to the
			// textarea so the cursor moves normally.
			if m.historyIndex > -1 && m.editorOnLastLine() {
				m.navigateHistory(1)
				m.refreshViewport()
				return m, nil
			}
		case "tab":
			if m.completeSlashCommand() {
				m.refreshViewport()
				return m, nil
			}
		case "ctrl+j", "shift+enter":
			m.input.InsertString("\n")
			m.refreshViewport()
			return m, nil
		case "alt+enter":
			cmd = m.submitInputWithBehavior(StreamingFollowUp)
			m.refreshViewport()
			return m, cmd
		case "enter":
			cmd = m.submitInputWithBehavior("")
			m.refreshViewport()
			return m, cmd
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.refreshViewport()
		return m, nil
	case interactiveAgentEventMsg:
		m.applyAgentEvent(msg.Event)
		m.refreshViewport()
		return m, nil
	case interactiveSessionEventMsg:
		m.applySessionEvent(msg.Event)
		m.refreshViewport()
		return m, nil
	case interactiveQueueDoneMsg:
		if msg.Err != nil {
			m.appendMessage(interactiveRoleError, msg.Err.Error())
		}
		m.refreshViewport()
		return m, nil
	case interactiveAbortDoneMsg:
		if msg.Err != nil {
			m.appendMessage(interactiveRoleError, msg.Err.Error())
		} else {
			m.setStatus("Abort requested.")
		}
		m.refreshViewport()
		return m, nil
	case interactivePromptDoneMsg:
		m.busy = false
		m.busyKind = interactiveBusyNone
		m.assistantSlot = -1
		m.queuedSteering = nil
		m.queuedFollowUp = nil
		if msg.Err != nil {
			m.appendMessage(interactiveRoleError, msg.Err.Error())
		}
		if msg.Err == nil {
			if next, ok := m.popInitialQueue(); ok {
				m.appendMessage(interactiveRoleUser, next)
				m.busy = true
				m.busyKind = interactiveBusyAgent
				m.refreshViewport()
				return m, m.runAgentPrompt(next, nil)
			}
		}
		if cmd, ok := m.popLocalQueuedCommand(); ok {
			m.refreshViewport()
			return m, cmd
		}
		m.refreshViewport()
		return m, nil
	case interactiveCommandDoneMsg:
		m.busy = false
		m.busyKind = interactiveBusyNone
		m.clearCommandCancel()
		if strings.TrimSpace(msg.Stdout) != "" {
			m.appendMessage(interactiveRoleSystem, strings.TrimSpace(msg.Stdout))
		}
		if strings.TrimSpace(msg.Stderr) != "" {
			m.appendMessage(interactiveRoleError, strings.TrimSpace(msg.Stderr))
		}
		if msg.Err != nil {
			m.appendMessage(interactiveRoleError, msg.Err.Error())
		}
		m.refreshViewport()
		if msg.Quit {
			return m, tea.Quit
		}
		if cmd, ok := m.popLocalQueuedCommand(); ok {
			return m, cmd
		}
		return m, nil
	case modelCycleDoneMsg:
		m.cyclingModel = false
		if !msg.ok {
			switch {
			case msg.busy:
				m.setStatus("Can't switch model while a response is streaming")
			case msg.scoped:
				m.setStatus("Only one model in scope")
			default:
				m.setStatus("Only one model available")
			}
		}
		m.refreshViewport()
		return m, nil
	case thinkingCycleDoneMsg:
		m.cyclingThinking = false
		if !msg.ok {
			if msg.busy {
				m.setStatus("Can't switch thinking level while a response is streaming")
			} else {
				m.setStatus("No other thinking levels available")
			}
		}
		m.refreshViewport()
		return m, nil
	case modelSelectDoneMsg:
		// Success feedback rides the ModelChangedEvent emitted by SetModel; only
		// surface the failure path here.
		if msg.Err != nil {
			m.appendMessage(interactiveRoleError, msg.Err.Error())
		}
		m.refreshViewport()
		return m, nil
	case interactiveExtensionUIRequestMsg:
		m.enqueueExtensionUIRequest(msg.Request)
		m.refreshViewport()
		return m, nil
	case interactiveExtensionUICancelMsg:
		m.cancelExtensionUIRequest(msg.Request)
		m.refreshViewport()
		return m, nil
	}
	// Forward to the textarea. If the input text actually changed (an edit — a
	// keypress, a paste, etc., but not a pure cursor move), exit history browsing,
	// mirroring TS editor.ts which resets historyIndex on edits. The history
	// Up/Down branches return earlier, so their SetValue never reaches here.
	before := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.historyIndex = -1
		m.autocompleteIndex = 0
	}
	m.refreshViewport()
	return m, cmd
}

func (m *interactiveModel) popInitialQueue() (string, bool) {
	for len(m.initialQueue) > 0 {
		next := strings.TrimSpace(m.initialQueue[0])
		m.initialQueue = m.initialQueue[1:]
		if next != "" {
			return next, true
		}
	}
	return "", false
}

func (m *interactiveModel) extensionUIHandler(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if m == nil || m.post == nil {
		return nil, fmt.Errorf("pi.ui.%s requires an interactive TUI host, which is not available", method)
	}
	if ctx == nil {
		ctx = context.Background()
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
		interactiveSystemStyle.Render("ctx.ui."+req.Method) + " " + tui.TruncateToWidth(req.Prompt.Message, lineWidth, "..."),
	}
	if req.Prompt.Detail != "" {
		lines = append(lines, interactiveFooterStyle.Render(tui.TruncateToWidth(req.Prompt.Detail, lineWidth, "...")))
	}
	switch req.Method {
	case "confirm":
		lines = append(lines, interactiveSuggestionStyle.Render("  y = yes    n/enter/esc = no"))
	case "select":
		for i, choice := range req.Prompt.Choices {
			marker := "  "
			if i == req.Selected {
				marker = "> "
			}
			lines = append(lines, interactiveSuggestionStyle.Render(tui.TruncateToWidth(fmt.Sprintf("%s%d. %s", marker, i+1, choice.Label), lineWidth, "...")))
		}
	case "input":
		value := req.Input
		if value == "" {
			value = req.Prompt.Placeholder
		}
		lines = append(lines, interactiveInputStyle.Width(lineWidth).Render("> "+value))
	}
	return lines
}

func (m *interactiveModel) View() tea.View {
	width := max(1, m.width)
	header := interactiveHeaderStyle.Render(m.header())
	body := m.viewport.View()
	footer := interactiveFooterStyle.Render(m.footer())
	parts := []string{header, body}
	if m.modelSelector != nil {
		// While the selector is open, its rendered lines replace the input
		// region (and suggestions), mirroring TS showModelSelector swapping the
		// editor container for the selector component. Header/body/footer stay.
		parts = append(parts, m.modelSelector.Render(width)...)
	} else if m.extensionUI != nil {
		parts = append(parts, m.renderExtensionUI(width)...)
	} else {
		if suggestions := m.renderSuggestions(); suggestions != "" {
			parts = append(parts, suggestions)
		}
		input := interactiveInputStyle.Width(max(1, width-2)).Render(m.input.View())
		parts = append(parts, input)
	}
	parts = append(parts, footer)
	view := tea.NewView(strings.Join(parts, "\n"))
	view.KeyboardEnhancements.ReportEventTypes = true
	view.KeyboardEnhancements.ReportAlternateKeys = true
	return view
}

// addToHistory records a submitted prompt for Up/Down browsing, mirroring TS
// editor.ts addToHistory: skip empty, skip a consecutive duplicate of the most
// recent entry, prepend (most-recent-first), and cap at 100 entries.
func (m *interactiveModel) addToHistory(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if len(m.history) > 0 && m.history[0] == trimmed {
		return
	}
	m.history = append([]string{trimmed}, m.history...)
	if len(m.history) > 100 {
		m.history = m.history[:100]
	}
}

// navigateHistory browses prompt history (direction -1 = older/Up, +1 =
// newer/Down), mirroring TS editor.ts navigateHistory. historyIndex -1 means not
// browsing; 0 is the most recent entry. Reaching index -1 again restores an
// empty editor.
func (m *interactiveModel) navigateHistory(direction int) {
	if len(m.history) == 0 {
		return
	}
	newIndex := m.historyIndex - direction
	if newIndex < -1 || newIndex >= len(m.history) {
		return
	}
	m.historyIndex = newIndex
	if m.historyIndex == -1 {
		m.input.SetValue("")
	} else {
		m.input.SetValue(m.history[m.historyIndex])
	}
	m.input.MoveToEnd()
}

// editorEmpty reports whether the input is empty; editorOnFirstLine /
// editorOnLastLine report cursor position by logical line (the Go textarea has no
// visual-line API, so wrapped lines are treated as their logical line — a small
// divergence from TS isOnFirstVisualLine/isOnLastVisualLine).
func (m *interactiveModel) editorEmpty() bool       { return m.input.Value() == "" }
func (m *interactiveModel) editorOnFirstLine() bool { return m.input.Line() == 0 }
func (m *interactiveModel) editorOnLastLine() bool  { return m.input.Line() == m.input.LineCount()-1 }

func (m *interactiveModel) submitInputWithBehavior(behavior StreamingBehavior) tea.Cmd {
	raw := m.input.Value()
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	m.addToHistory(raw)
	m.historyIndex = -1
	m.input.Reset()
	m.autoScroll = true
	if !m.busy && text == "/model" {
		// Bare `/model` opens the navigable selector overlay, mirroring TS
		// interactive-mode showModelSelector. `/model provider/id` (with an
		// argument) still routes to the slash handler -> SetModel below.
		m.openModelSelector()
		return nil
	}
	if m.busy {
		return m.queueBusyInput(text, behavior)
	}
	m.appendMessage(interactiveRoleUser, text)
	m.busy = true
	switch {
	case strings.HasPrefix(text, "/"):
		m.busyKind = interactiveBusyCommand
		return m.runSlashCommand(m.beginCommand(), text)
	case strings.HasPrefix(text, "!"):
		m.busyKind = interactiveBusyCommand
		return m.runBashCommand(m.beginCommand(), text)
	default:
		m.busyKind = interactiveBusyAgent
		return m.runAgentPrompt(text, nil)
	}
}

func (m *interactiveModel) queueBusyInput(text string, behavior StreamingBehavior) tea.Cmd {
	m.appendMessage(interactiveRoleUser, text)
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, "!") || m.busyKind != interactiveBusyAgent {
		m.localQueue = append(m.localQueue, interactiveQueuedInput{Text: text})
		m.setStatus("Queued until the current command finishes.")
		return nil
	}
	if behavior == "" {
		behavior = StreamingSteer
	}
	switch behavior {
	case StreamingFollowUp:
		m.queuedFollowUp = append(m.queuedFollowUp, text)
		m.setStatus("Queued as follow-up.")
	default:
		behavior = StreamingSteer
		m.queuedSteering = append(m.queuedSteering, text)
		m.setStatus("Sent as steering input.")
	}
	return m.runQueuePrompt(text, nil, behavior)
}

func (m *interactiveModel) popLocalQueuedCommand() (tea.Cmd, bool) {
	if len(m.localQueue) == 0 {
		return nil, false
	}
	next := m.localQueue[0]
	copy(m.localQueue, m.localQueue[1:])
	m.localQueue[len(m.localQueue)-1] = interactiveQueuedInput{}
	m.localQueue = m.localQueue[:len(m.localQueue)-1]
	m.busy = true
	switch {
	case strings.HasPrefix(next.Text, "/"):
		m.busyKind = interactiveBusyCommand
		return m.runSlashCommand(m.beginCommand(), next.Text), true
	case strings.HasPrefix(next.Text, "!"):
		m.busyKind = interactiveBusyCommand
		return m.runBashCommand(m.beginCommand(), next.Text), true
	default:
		m.busyKind = interactiveBusyAgent
		return m.runAgentPrompt(next.Text, next.Images), true
	}
}

func (m *interactiveModel) runAgentPrompt(text string, images []ai.ContentBlock) tea.Cmd {
	return func() tea.Msg {
		agent, err := runtimeAgent(m.runtime)
		if err != nil {
			return interactivePromptDoneMsg{Err: err}
		}
		err = agent.Send(m.ctx, text, images, "", nil, func(event ai.Event) {
			if m.post != nil {
				m.post(interactiveAgentEventMsg{Event: event})
			}
		})
		return interactivePromptDoneMsg{Err: err}
	}
}

func (m *interactiveModel) runQueuePrompt(text string, images []ai.ContentBlock, behavior StreamingBehavior) tea.Cmd {
	return func() tea.Msg {
		agent, err := runtimeAgent(m.runtime)
		if err != nil {
			return interactiveQueueDoneMsg{Err: err}
		}
		switch behavior {
		case StreamingFollowUp:
			err = agent.FollowUp(m.ctx, text, images)
		default:
			err = agent.Steer(m.ctx, text, images)
		}
		return interactiveQueueDoneMsg{Err: err}
	}
}

func (m *interactiveModel) runAbort() tea.Cmd {
	return func() tea.Msg {
		agent, err := runtimeAgent(m.runtime)
		if err != nil {
			return interactiveAbortDoneMsg{Err: err}
		}
		return interactiveAbortDoneMsg{Err: agent.Abort(m.ctx)}
	}
}

func (m *interactiveModel) runSlashCommand(ctx context.Context, line string) tea.Cmd {
	// Provide a real interactive prompter for /login so OAuth flows
	// (anthropic / github-copilot / openai-codex) can complete inside the TUI:
	// modes.go calls prompter for each OAuthPrompt and the prompter blocks on the
	// input overlay. Other commands keep a nil prompter so they stay
	// non-blocking. The prompter is only ever invoked from this tea.Cmd
	// goroutine, so blocking on the overlay channel never stalls the Update loop.
	var prompter slashPrompter
	if isLoginCommand(line) {
		prompter = m.oauthSlashPrompter(ctx)
	}
	return func() tea.Msg {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		done, err := handleSlashWithPrompt(ctx, m.runtime, line, prompter, &stdout, &stderr)
		return interactiveCommandDoneMsg{
			Stdout: stdout.String(),
			Stderr: stderr.String(),
			Err:    err,
			Quit:   done,
		}
	}
}

// isLoginCommand reports whether the submitted slash line is a /login invocation
// (the only command that may need the blocking OAuth prompter).
func isLoginCommand(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return false
	}
	return strings.TrimPrefix(fields[0], "/") == "login"
}

func (m *interactiveModel) runBashCommand(ctx context.Context, line string) tea.Cmd {
	return func() tea.Msg {
		agent, err := runtimeAgent(m.runtime)
		if err != nil {
			return interactiveCommandDoneMsg{Err: err}
		}
		exclude := strings.HasPrefix(line, "!!")
		command := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "!"), "!"))
		if command == "" {
			return interactiveCommandDoneMsg{Err: errorsString("empty bash command")}
		}
		result := (catools.BashTool{CWD: agent.Session.CWD(), BinDir: BinDir()}).Execute(ctx, mustJSON(map[string]any{"command": command}), nil)
		text := ai.MessageText(ai.ToolResultMessage{Content: result.Content})
		exit := 0
		if result.IsError {
			exit = 1
		}
		_ = agent.Session.Append(SessionEntry{Type: "message", Message: ai.CustomMessage{Role: "bashExecution", Command: command, Output: text, ExitCode: &exit, ExcludeFromContext: exclude}})
		if result.IsError {
			return interactiveCommandDoneMsg{Stderr: text}
		}
		return interactiveCommandDoneMsg{Stdout: text}
	}
}

func (m *interactiveModel) applyAgentEvent(event ai.Event) {
	switch event["type"] {
	case "agent_start":
		m.busy = true
		m.busyKind = interactiveBusyAgent
	case "agent_end":
		m.busyKind = interactiveBusyNone
	case "tool_execution_start":
		name := fmt.Sprint(event["toolName"])
		if strings.TrimSpace(name) == "" || name == "<nil>" {
			name = "tool"
		}
		m.assistantSlot = -1
		slot := m.appendMessage(interactiveRoleTool, "["+name+"]")
		if id := strings.TrimSpace(fmt.Sprint(event["toolCallId"])); id != "" && id != "<nil>" {
			m.toolSlots[id] = slot
		}
	case "tool_execution_update":
		m.updateToolResult(event, "partialResult")
	case "tool_execution_end":
		m.updateToolResult(event, "result")
		if id := strings.TrimSpace(fmt.Sprint(event["toolCallId"])); id != "" {
			delete(m.toolSlots, id)
		}
	case "message_update":
		if assistantEvent, ok := event["assistantMessageEvent"].(ai.AssistantMessageEvent); ok && assistantEvent.Type == "text_delta" && assistantEvent.Delta != "" {
			m.appendAssistantDelta(assistantEvent.Delta)
		}
	case "message_end":
		if msg, ok := event["message"].(ai.Message); ok && ai.MessageRole(msg) == "assistant" {
			text := strings.TrimSpace(ai.MessageText(msg))
			if m.assistantSlot < 0 && text != "" {
				m.appendMessage(interactiveRoleAssistant, text)
			}
			m.assistantSlot = -1
		}
	}
}

func (m *interactiveModel) updateToolResult(event ai.Event, key string) {
	result, ok := interactiveToolResult(event[key])
	if !ok {
		return
	}
	text := strings.TrimSpace(ai.MessageText(ai.ToolResultMessage{Content: result.Content}))
	if text == "" {
		return
	}
	id := strings.TrimSpace(fmt.Sprint(event["toolCallId"]))
	if slot, ok := m.toolSlots[id]; ok && slot >= 0 && slot < len(m.messages) {
		prefix := strings.TrimSpace(fmt.Sprint(event["toolName"]))
		if prefix == "" || prefix == "<nil>" {
			prefix = "tool"
		}
		m.messages[slot].Text = "[" + prefix + "]\n" + text
		return
	}
	m.appendMessage(interactiveRoleTool, text)
}

func interactiveToolResult(value any) (ai.ToolResult, bool) {
	switch result := value.(type) {
	case ai.ToolResult:
		return result, true
	case agentcore.AgentToolResult:
		return ai.ToolResult{Content: result.Content, Details: result.Details, IsError: result.IsError}, true
	default:
		return ai.ToolResult{}, false
	}
}

func (m *interactiveModel) applySessionEvent(event SessionEvent) {
	switch ev := event.(type) {
	case QueueUpdateEvent:
		m.queuedSteering = append([]string(nil), ev.Steering...)
		m.queuedFollowUp = append([]string(nil), ev.FollowUp...)
	case ModelChangedEvent:
		m.setStatus("Model: " + ev.Model.Provider + "/" + ev.Model.ID)
	case ThinkingLevelChangedEvent:
		m.setStatus("Thinking: " + string(ev.Level))
	case SessionInfoChangedEvent:
		if ev.Name != "" {
			m.setStatus("Session: " + ev.Name)
		}
	case CompactionStartEvent:
		m.setStatus("Compacting context...")
	case CompactionEndEvent:
		if ev.Aborted {
			m.setStatus("Compaction aborted.")
		} else if ev.ErrorMessage != "" {
			m.appendMessage(interactiveRoleError, ev.ErrorMessage)
		} else {
			m.setStatus("Compaction complete.")
		}
	case AutoRetryStartEvent:
		m.setStatus(fmt.Sprintf("Retrying in %d ms (%d/%d).", ev.DelayMs, ev.Attempt, ev.MaxAttempts))
	case AutoRetryEndEvent:
		if ev.FinalError != "" {
			m.appendMessage(interactiveRoleError, ev.FinalError)
		}
	}
}

func (m *interactiveModel) appendAssistantDelta(delta string) {
	if m.assistantSlot < 0 || m.assistantSlot >= len(m.messages) || m.messages[m.assistantSlot].Role != interactiveRoleAssistant {
		m.messages = append(m.messages, interactiveMessage{Role: interactiveRoleAssistant})
		m.assistantSlot = len(m.messages) - 1
	}
	m.messages[m.assistantSlot].Text += delta
}

func (m *interactiveModel) appendMessage(role interactiveRole, text string) int {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return -1
	}
	m.messages = append(m.messages, interactiveMessage{Role: role, Text: text})
	return len(m.messages) - 1
}

func (m *interactiveModel) refreshViewport() {
	width := m.width
	if width <= 0 {
		width = interactiveDefaultWidth
	}
	height := m.height
	if height <= 0 {
		height = interactiveDefaultHeight
	}
	inputWidth := max(1, width-4)
	m.input.SetWidth(inputWidth)
	controlHeight := lipgloss.Height(m.input.View()) + 2
	suggestionHeight := 0
	if m.modelSelector != nil {
		controlHeight = len(m.modelSelector.Render(width))
	} else if m.extensionUI != nil {
		controlHeight = len(m.renderExtensionUI(width))
	} else if m.renderSuggestions() != "" {
		suggestionHeight = 1
	}
	bodyHeight := height - controlHeight - suggestionHeight - 3
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	m.viewport.SetWidth(width)
	m.viewport.SetHeight(bodyHeight)
	m.viewport.SetContent(m.renderTranscript(width))
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *interactiveModel) renderTranscript(width int) string {
	var rendered []string
	for _, msg := range m.messages {
		prefix, style := interactiveMessagePrefix(msg.Role)
		text := strings.TrimRight(msg.Text, "\n")
		if text == "" {
			continue
		}
		lines := renderInteractiveMessageLines(msg, max(1, width-tui.VisibleWidth(prefix)))
		for i, line := range lines {
			currentPrefix := prefix
			if i > 0 {
				currentPrefix = strings.Repeat(" ", tui.VisibleWidth(prefix))
			}
			rendered = append(rendered, style.Render(currentPrefix)+line)
		}
		rendered = append(rendered, "")
	}
	if len(rendered) == 0 {
		rendered = append(rendered, "Type /help for commands.")
	}
	for len(rendered) > 0 && rendered[len(rendered)-1] == "" {
		rendered = rendered[:len(rendered)-1]
	}
	return strings.Join(rendered, "\n")
}

func renderInteractiveMessageLines(msg interactiveMessage, width int) []string {
	text := strings.TrimRight(msg.Text, "\n")
	switch msg.Role {
	case interactiveRoleUser, interactiveRoleAssistant:
		lines := tui.NewMarkdown(text, 0, 0, tui.MarkdownTheme{}).Render(width)
		if len(lines) > 0 {
			return lines
		}
	}
	return strings.Split(text, "\n")
}

func interactiveMessagePrefix(role interactiveRole) (string, lipgloss.Style) {
	switch role {
	case interactiveRoleUser:
		return "you  ", interactiveUserStyle
	case interactiveRoleAssistant:
		return "pi   ", interactiveAssistantStyle
	case interactiveRoleTool:
		return "tool ", interactiveToolStyle
	case interactiveRoleError:
		return "err  ", interactiveErrorStyle
	default:
		return "info ", interactiveSystemStyle
	}
}

func (m *interactiveModel) header() string {
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		return "pi-go"
	}
	currentModel := agent.CurrentModel()
	text := fmt.Sprintf("pi-go %s  %s/%s", Version, currentModel.Provider, currentModel.ID)
	return tui.TruncateToWidth(text, max(1, m.width), "...")
}

func (m *interactiveModel) footer() string {
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		return err.Error()
	}
	status := "ready"
	if m.busy {
		status = "working"
		if m.busyKind != interactiveBusyNone {
			status += ":" + string(m.busyKind)
		}
	}
	stats := agent.GetSessionStats()
	parts := []string{
		status,
		"cwd=" + agent.Session.CWD(),
		fmt.Sprintf("messages=%d", stats.TotalMessages),
	}
	if stats.Tokens.Total > 0 {
		parts = append(parts, fmt.Sprintf("tokens=%d", stats.Tokens.Total))
	}
	if queued := len(m.queuedSteering) + len(m.queuedFollowUp) + len(m.localQueue) + len(m.initialQueue); queued > 0 {
		parts = append(parts, fmt.Sprintf("queued=%d", queued))
	}
	if m.statusMessage != "" {
		parts = append(parts, m.statusMessage)
	}
	if suggestions := m.currentSuggestions(); len(suggestions) > 0 {
		parts = append(parts, "tab="+suggestions[m.selectedSuggestionIndex(suggestions)])
	}
	return tui.TruncateToWidth(strings.Join(parts, "  "), max(1, m.width), "...")
}

func (m *interactiveModel) renderSuggestions() string {
	suggestions := m.currentSuggestions()
	if len(suggestions) == 0 {
		return ""
	}
	selected := m.selectedSuggestionIndex(suggestions)
	maxVisible := 5
	if agent, err := runtimeAgent(m.runtime); err == nil && agent != nil && agent.Settings != nil {
		maxVisible = agent.Settings.AutocompleteMaxVisible()
	}
	if maxVisible <= 0 {
		maxVisible = 5
	}
	half := maxVisible / 2
	start := selected - half
	if start > len(suggestions)-maxVisible {
		start = len(suggestions) - maxVisible
	}
	if start < 0 {
		start = 0
	}
	end := start + maxVisible
	if end > len(suggestions) {
		end = len(suggestions)
	}
	width := max(1, m.width)
	lines := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		prefix := "  "
		style := interactiveSuggestionStyle
		if i == selected {
			prefix = "> "
			style = interactiveSelectorSelectedStyle
		}
		lines = append(lines, style.Render(tui.TruncateToWidth(prefix+suggestions[i], width, "...")))
	}
	if start > 0 || end < len(suggestions) {
		lines = append(lines, interactiveSuggestionStyle.Render(tui.TruncateToWidth(fmt.Sprintf("  (%d/%d)", selected+1, len(suggestions)), width, "...")))
	}
	return strings.Join(lines, "\n")
}

func (m *interactiveModel) completeSlashCommand() bool {
	suggestions := m.currentSuggestions()
	if len(suggestions) == 0 {
		return false
	}
	value := m.input.Value()
	first := suggestions[m.selectedSuggestionIndex(suggestions)]
	if _, start, ok := trailingFileRefToken(value); ok {
		// Replace just the trailing @token. A directory completion ends with "/"
		// so the user can keep descending without a separating space.
		completed := value[:start] + first
		if !completionIsDirectory(first) {
			completed += " "
		}
		m.input.SetValue(completed)
		m.input.MoveToEnd()
		return true
	}
	if _, start, ok := trailingPathCompletionToken(value); ok {
		completed := value[:start] + first
		if !completionIsDirectory(first) {
			completed += " "
		}
		m.input.SetValue(completed)
		m.input.MoveToEnd()
		return true
	}
	m.input.SetValue(first + " ")
	m.input.MoveToEnd()
	return true
}

// trailingFileRefToken returns the final whitespace-delimited token of value, the
// byte index where it starts, and whether it is a file-reference token (begins
// with "@"). Used for @-attachment autocomplete.
func trailingFileRefToken(value string) (token string, start int, ok bool) {
	// An open @"..." quote lets a file reference contain spaces, so the token runs
	// from that @ to end-of-input rather than from the last whitespace (mirrors TS
	// findUnclosedQuoteStart/extractQuotedPrefix).
	if at := unclosedAtQuoteStart(value); at >= 0 {
		return value[at:], at, true
	}
	start = strings.LastIndexAny(value, " \t\r\n") + 1
	token = value[start:]
	return token, start, strings.HasPrefix(token, "@")
}

// unclosedAtQuoteStart returns the index of an '@' that opens an unclosed @"..."
// quote at a token boundary, or -1. Only @-prefixed quotes qualify (a bare
// unclosed quote is not a file reference).
func unclosedAtQuoteStart(value string) int {
	inQuotes := false
	quoteIdx := -1
	for i := 0; i < len(value); i++ {
		if value[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				quoteIdx = i
			}
		}
	}
	if !inQuotes || quoteIdx <= 0 || value[quoteIdx-1] != '@' {
		return -1
	}
	at := quoteIdx - 1
	if at == 0 || strings.ContainsRune(" \t\r\n", rune(value[at-1])) {
		return at
	}
	return -1
}

// fileReferenceSuggestions lists files/directories under cwd that complete the
// given "@<partial>" token, returning full replacement values like "@src/" or
// "@main.go" (mirrors the TS @-attachment provider: directories get a trailing
// slash, values with spaces are quoted as @"...", hidden entries are shown only
// when explicitly typed).
func fileReferenceSuggestions(token, cwd string) []string {
	rawPrefix := strings.TrimPrefix(token, "@")
	quoted := false
	if strings.HasPrefix(rawPrefix, "\"") {
		quoted = true
		rawPrefix = rawPrefix[1:]
	}
	displayPrefix := filepath.ToSlash(rawPrefix)
	absolute := filepath.IsAbs(rawPrefix) || strings.HasPrefix(displayPrefix, "/")
	var dir, base string
	if slash := strings.LastIndex(displayPrefix, "/"); slash >= 0 {
		dir, base = displayPrefix[:slash], displayPrefix[slash+1:]
		if dir == "" {
			dir = "/" // "@/abc" -> list the filesystem root, not cwd
		} else if len(dir) == 2 && dir[1] == ':' {
			dir += "/" // "@C:/abc" -> list C:/, not C:'s cwd
		}
	} else {
		dir, base = ".", displayPrefix
	}
	readDir := filepath.FromSlash(dir)
	if !absolute {
		readDir = filepath.Join(cwd, readDir)
	}
	entries, err := os.ReadDir(readDir)
	if err != nil {
		return nil
	}
	matches := make([]string, 0, 8)
	for _, entry := range entries {
		name := entry.Name()
		// Hide dotfiles only at the bare top level (a plain "@" with no path and no
		// dot typed) to avoid .git/node_modules noise; once the user descends into a
		// directory or types a leading dot, surface hidden entries like TS fd --hidden.
		if strings.HasPrefix(name, ".") && dir == "." && !strings.HasPrefix(base, ".") {
			continue
		}
		if base != "" && !strings.HasPrefix(name, base) {
			continue
		}
		var rel string
		switch {
		case dir == "/":
			rel = "/" + name
		case dir != ".":
			rel = strings.TrimSuffix(dir, "/") + "/" + name
		default:
			rel = name
		}
		if entry.IsDir() {
			rel += "/"
		}
		matches = append(matches, buildFileRefCompletion(rel, quoted))
	}
	sort.Strings(matches)
	if len(matches) > 8 {
		matches = matches[:8]
	}
	return matches
}

// buildFileRefCompletion wraps a path as an "@"-prefixed completion value,
// quoting it as @"..." when the original token was quoted or the path contains a
// space (mirrors TS buildCompletionValue).
func buildFileRefCompletion(path string, quoted bool) string {
	if quoted || strings.Contains(path, " ") {
		if strings.HasSuffix(path, "/") {
			return "@\"" + path
		}
		return "@\"" + path + "\""
	}
	return "@" + path
}

func completionIsDirectory(value string) bool {
	return strings.HasSuffix(value, "/") || strings.HasSuffix(value, "/\"")
}

func trailingPathCompletionToken(value string) (token string, start int, ok bool) {
	if at := unclosedPlainQuoteStart(value); at >= 0 {
		return value[at:], at, true
	}
	start = strings.LastIndexAny(value, " \t\r\n=") + 1
	token = value[start:]
	return token, start, looksLikePathCompletionToken(token)
}

func unclosedPlainQuoteStart(value string) int {
	inQuotes := false
	quoteIdx := -1
	for i := 0; i < len(value); i++ {
		if value[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				quoteIdx = i
			}
		}
	}
	if !inQuotes || quoteIdx < 0 {
		return -1
	}
	if quoteIdx > 0 && value[quoteIdx-1] == '@' {
		return -1
	}
	if quoteIdx == 0 || strings.ContainsRune(" \t\r\n=", rune(value[quoteIdx-1])) {
		return quoteIdx
	}
	return -1
}

func looksLikePathCompletionToken(token string) bool {
	if token == "" || strings.HasPrefix(token, "@") {
		return false
	}
	return strings.HasPrefix(token, "\"") ||
		strings.HasPrefix(token, "./") ||
		strings.HasPrefix(token, "../") ||
		strings.HasPrefix(token, "~/") ||
		strings.Contains(token, "/")
}

func pathCompletionSuggestions(token, cwd string) []string {
	rawPrefix := token
	quoted := false
	if strings.HasPrefix(rawPrefix, "\"") {
		quoted = true
		rawPrefix = rawPrefix[1:]
	}
	displayBase := ""
	readPrefix := rawPrefix
	if strings.HasPrefix(rawPrefix, "~/") {
		home := HomeDir()
		if home == "" {
			return nil
		}
		displayBase = "~/"
		readPrefix = filepath.Join(home, strings.TrimPrefix(rawPrefix, "~/"))
	}
	absolute := filepath.IsAbs(readPrefix)
	var dir, base string
	if slash := strings.LastIndex(readPrefix, "/"); slash >= 0 {
		dir, base = readPrefix[:slash], readPrefix[slash+1:]
		if dir == "" {
			dir = "/"
		}
	} else {
		dir, base = ".", readPrefix
	}
	readDir := dir
	if !absolute && displayBase == "" {
		readDir = filepath.Join(cwd, dir)
	}
	entries, err := os.ReadDir(readDir)
	if err != nil {
		return nil
	}
	matches := make([]string, 0, 8)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && dir == "." && !strings.HasPrefix(base, ".") {
			continue
		}
		if base != "" && !strings.HasPrefix(name, base) {
			continue
		}
		var rel string
		switch {
		case displayBase != "":
			typedDir := strings.TrimSuffix(strings.TrimPrefix(rawPrefix, "~/"), "/")
			if typedDir != "" && typedDir != base {
				parent := filepath.Dir(strings.TrimPrefix(rawPrefix, "~/"))
				if parent != "." {
					rel = displayBase + strings.TrimSuffix(parent, "/") + "/" + name
				} else {
					rel = displayBase + name
				}
			} else {
				rel = displayBase + name
			}
		case dir == "/":
			rel = "/" + name
		case dir != ".":
			rel = strings.TrimSuffix(dir, "/") + "/" + name
		default:
			rel = name
		}
		if entry.IsDir() {
			rel += "/"
		}
		matches = append(matches, buildPathCompletion(rel, quoted))
	}
	sort.Strings(matches)
	if len(matches) > 8 {
		matches = matches[:8]
	}
	return matches
}

func buildPathCompletion(path string, quoted bool) string {
	if quoted || strings.Contains(path, " ") {
		if strings.HasSuffix(path, "/") {
			return "\"" + path
		}
		return "\"" + path + "\""
	}
	return path
}

func slashCommandSuggestions(input string) []string {
	return interactiveSuggestions(input, nil)
}

func (m *interactiveModel) currentSuggestions() []string {
	agent, _ := runtimeAgent(m.runtime)
	return interactiveSuggestions(m.input.Value(), agent)
}

func interactiveSuggestions(input string, agent *AgentSession) []string {
	// A trailing "@<partial>" token requests file-reference completion against the
	// session cwd (mirrors the TS @-attachment autocomplete), and may appear after
	// any text, including a slash command's arguments.
	if token, _, ok := trailingFileRefToken(input); ok {
		if agent != nil {
			if cwd := agent.Session.CWD(); cwd != "" {
				return fileReferenceSuggestions(token, cwd)
			}
		}
		return nil
	}
	if token, _, ok := trailingPathCompletionToken(input); ok {
		if agent != nil {
			if cwd := agent.Session.CWD(); cwd != "" {
				if suggestions := pathCompletionSuggestions(token, cwd); len(suggestions) > 0 {
					return suggestions
				}
			}
		}
	}
	raw := strings.TrimLeft(input, " \t\r\n")
	text := strings.TrimSpace(input)
	if strings.HasPrefix(raw, "/model ") {
		return modelCommandSuggestions(raw, agent)
	}
	if !strings.HasPrefix(text, "/") || strings.Contains(text, " ") {
		if strings.HasPrefix(text, "/model ") {
			return modelCommandSuggestions(text, agent)
		}
		return nil
	}
	prefix := strings.TrimPrefix(text, "/")
	matches := make([]string, 0, 6)
	for _, command := range interactiveSlashCommands {
		if strings.HasPrefix(command, prefix) {
			matches = append(matches, "/"+command)
		}
	}
	if agent != nil {
		for name := range agent.Resources.PromptTemplates {
			if strings.HasPrefix(name, prefix) {
				matches = append(matches, "/"+name)
			}
		}
		for name := range agent.Resources.Skills {
			command := "skill:" + name
			if strings.HasPrefix(command, prefix) {
				matches = append(matches, "/"+command)
			}
		}
	}
	sort.Strings(matches)
	if len(matches) > 6 {
		matches = matches[:6]
	}
	return matches
}

func (m *interactiveModel) selectedSuggestionIndex(suggestions []string) int {
	if len(suggestions) == 0 {
		m.autocompleteIndex = 0
		return 0
	}
	if m.autocompleteIndex < 0 {
		m.autocompleteIndex = 0
	}
	if m.autocompleteIndex >= len(suggestions) {
		m.autocompleteIndex = len(suggestions) - 1
	}
	return m.autocompleteIndex
}

func (m *interactiveModel) navigateAutocomplete(delta int) bool {
	suggestions := m.currentSuggestions()
	if len(suggestions) == 0 {
		m.autocompleteIndex = 0
		return false
	}
	index := m.selectedSuggestionIndex(suggestions)
	index = (index + delta) % len(suggestions)
	if index < 0 {
		index += len(suggestions)
	}
	m.autocompleteIndex = index
	m.historyIndex = -1
	return true
}

func modelCommandSuggestions(text string, agent *AgentSession) []string {
	if agent == nil {
		return nil
	}
	prefix := strings.TrimSpace(strings.TrimPrefix(text, "/model"))
	prefix = strings.ToLower(prefix)
	models := agent.availableModels()
	if len(models) == 0 && agent.Registry != nil {
		models = agent.Registry.List("")
	}
	matches := make([]string, 0, 6)
	for _, model := range models {
		label := model.Provider + "/" + model.ID
		search := strings.ToLower(model.ID + " " + model.Provider + " " + label)
		if prefix == "" || strings.Contains(search, prefix) {
			matches = append(matches, "/model "+label)
		}
	}
	sort.Strings(matches)
	if len(matches) > 6 {
		matches = matches[:6]
	}
	return matches
}

func (m *interactiveModel) handleCtrlC() tea.Cmd {
	now := time.Now()
	if now.Sub(m.lastCtrlC) < 500*time.Millisecond {
		return tea.Quit
	}
	m.lastCtrlC = now
	if strings.TrimSpace(m.input.Value()) != "" {
		m.input.Reset()
		m.setStatus("Editor cleared. Press Ctrl+C again to exit.")
		return nil
	}
	m.setStatus("Press Ctrl+C again to exit.")
	return nil
}

func (m *interactiveModel) handleEscape() tea.Cmd {
	if m.busy && m.busyKind == interactiveBusyAgent {
		m.setStatus("Abort requested.")
		return m.runAbort()
	}
	if m.busy && m.busyKind == interactiveBusyCommand {
		if m.commandCancel != nil {
			m.clearCommandCancel()
			m.setStatus("Cancelling command…")
		}
		return nil
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		m.input.Reset()
		m.setStatus("Editor cleared.")
		return nil
	}
	now := time.Now()
	if now.Sub(m.lastEscape) < 500*time.Millisecond {
		m.lastEscape = time.Time{}
		agent, err := runtimeAgent(m.runtime)
		if err != nil || agent == nil || agent.Settings == nil {
			return nil
		}
		switch agent.Settings.DoubleEscapeAction() {
		case "tree":
			m.appendMessage(interactiveRoleUser, "/tree")
			m.busy = true
			m.busyKind = interactiveBusyCommand
			return m.runSlashCommand(m.beginCommand(), "/tree")
		case "fork":
			m.appendMessage(interactiveRoleUser, "/fork")
			m.busy = true
			m.busyKind = interactiveBusyCommand
			return m.runSlashCommand(m.beginCommand(), "/fork")
		}
		return nil
	}
	m.lastEscape = now
	m.setStatus("Press Escape again for " + m.doubleEscapeActionLabel() + ".")
	return nil
}

func (m *interactiveModel) doubleEscapeActionLabel() string {
	agent, err := runtimeAgent(m.runtime)
	if err != nil || agent == nil || agent.Settings == nil {
		return "tree"
	}
	switch agent.Settings.DoubleEscapeAction() {
	case "fork":
		return "fork"
	case "none":
		return "nothing"
	default:
		return "tree"
	}
}

func (m *interactiveModel) setStatus(text string) {
	m.statusMessage = strings.TrimSpace(text)
}

// cycleModel switches to the next (forward) or previous (backward) available
// model in response to Ctrl+P / Shift+Ctrl+P, mirroring TS
// app.model.cycleForward / cycleBackward. The switch runs in the returned
// tea.Cmd goroutine: CycleModel emits ModelChangedEvent -> m.post ->
// program.Send, which blocks on Bubble Tea's unbuffered msg channel, so
// invoking it on the Update goroutine would deadlock. m.cyclingModel (toggled
// only on the Update goroutine) serializes presses; cycling is also suppressed
// mid-turn (m.busy). Success feedback rides the emitted ModelChangedEvent; only
// the "nothing to cycle" case sets a status, via modelCycleDoneMsg.
func (m *interactiveModel) cycleModel(backward bool) tea.Cmd {
	if m.busy {
		m.setStatus("Can't switch model while a response is streaming")
		return nil
	}
	if m.cyclingModel {
		return nil
	}
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		m.setStatus(err.Error())
		return nil
	}
	m.cyclingModel = true
	return func() tea.Msg {
		var ok bool
		var data map[string]any
		if backward {
			data, ok = agent.CycleModelBackward()
		} else {
			data, ok = agent.CycleModel()
		}
		scoped, _ := data["isScoped"].(bool)
		busy, _ := data["busy"].(bool)
		if data == nil {
			scoped = agent.hasScopedModels()
		}
		return modelCycleDoneMsg{ok: ok, scoped: scoped, busy: busy}
	}
}

// cycleThinking advances to the next thinking level in response to Shift+Tab,
// mirroring TS app.thinking.cycle. Like cycleModel, the switch must run in the
// returned tea.Cmd goroutine: CycleThinkingLevel -> SetThinkingLevel ->
// emitSessionEvent -> m.post -> program.Send blocks on Bubble Tea's unbuffered
// channel, so invoking it on the Update goroutine would deadlock. m.cyclingThinking
// (toggled only on the Update goroutine) serializes rapid presses; cycling is
// suppressed mid-turn (m.busy). Success feedback rides the emitted
// ThinkingLevelChangedEvent status; only the "no other levels" case reports via
// thinkingCycleDoneMsg.
func (m *interactiveModel) cycleThinking() tea.Cmd {
	if m.busy {
		m.setStatus("Can't switch thinking level while a response is streaming")
		return nil
	}
	if m.cyclingThinking {
		return nil
	}
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		m.setStatus(err.Error())
		return nil
	}
	m.cyclingThinking = true
	return func() tea.Msg {
		_, ok := agent.CycleThinkingLevel()
		return thinkingCycleDoneMsg{ok: ok}
	}
}

// openModelSelector opens the navigable /model overlay. Entry points are Ctrl+L
// and a bare `/model` submission. It is suppressed mid-turn (m.busy) so the
// switch can't race a streaming response, and reports when no models or only
// one model are available rather than opening an empty/pointless picker.
func (m *interactiveModel) openModelSelector() {
	if m.modelSelector != nil {
		return
	}
	if m.busy {
		m.setStatus("Can't switch model while a response is streaming")
		return
	}
	agent, err := runtimeAgent(m.runtime)
	if err != nil {
		m.setStatus(err.Error())
		return
	}
	models := agent.availableModels()
	overlay := newModelSelectorOverlay(models, agent.CurrentModel())
	if overlay == nil {
		m.setStatus("No models available")
		return
	}
	if len(models) == 1 {
		m.setStatus("Only one model available")
		return
	}
	m.modelSelector = overlay
}

// handleModelSelectorKey routes a key to the open overlay and acts on the
// result. Selecting closes the overlay and returns a tea.Cmd that performs the
// model switch off the Update goroutine: SetModel emits ModelChangedEvent ->
// m.post -> program.Send, which blocks on Bubble Tea's unbuffered msg channel
// whose only reader is the Update goroutine, so a synchronous SetModel here
// would deadlock (the same hazard slice 1's cycleModel guards against).
// Success feedback rides the emitted ModelChangedEvent status line.
func (m *interactiveModel) handleModelSelectorKey(key string) tea.Cmd {
	if m.modelSelector == nil {
		return nil
	}
	switch m.modelSelector.HandleKey(key) {
	case modelSelectorSelect:
		value, ok := m.modelSelector.SelectedValue()
		m.modelSelector = nil
		if !ok {
			return nil
		}
		if m.busy {
			m.setStatus("Can't switch model while a response is streaming")
			return nil
		}
		return m.applyModelSelection(value)
	case modelSelectorCancel:
		m.modelSelector = nil
		m.setStatus("Model selection cancelled.")
		return nil
	default:
		return nil
	}
}

// applyModelSelection switches to the provider/id value chosen in the overlay.
// It runs in the returned tea.Cmd goroutine (never inline on Update) for the
// deadlock reason documented on handleModelSelectorKey. A no-op (already the
// active model) skips SetModel to avoid an unnecessary session model-change
// entry and event.
func (m *interactiveModel) applyModelSelection(value string) tea.Cmd {
	provider, id, ok := strings.Cut(value, "/")
	if !ok {
		return nil
	}
	return func() tea.Msg {
		agent, err := runtimeAgent(m.runtime)
		if err != nil {
			return modelSelectDoneMsg{Err: err}
		}
		currentModel := agent.CurrentModel()
		if currentModel.Provider == provider && currentModel.ID == id {
			return modelSelectDoneMsg{}
		}
		_, err = agent.SetModel(provider, id)
		return modelSelectDoneMsg{Err: err}
	}
}

func (m *interactiveModel) bindSession(agent *AgentSession) {
	m.unbindSession()
	if agent == nil {
		return
	}
	m.sessionAgent = agent
	m.sessionUnsubscribe = agent.Subscribe(func(event SessionEvent) {
		if m.post != nil {
			m.post(interactiveSessionEventMsg{Event: event})
		}
	})
	m.bindExtensionUIHandler()
}

func (m *interactiveModel) unbindSession() {
	if m.sessionAgent != nil {
		m.sessionAgent.SetExtensionUIHandler(nil)
		m.sessionAgent = nil
	}
	if m.sessionUnsubscribe != nil {
		m.sessionUnsubscribe()
		m.sessionUnsubscribe = nil
	}
}

func (m *interactiveModel) bindExtensionUIHandler() {
	if m == nil || m.sessionAgent == nil || m.post == nil {
		return
	}
	m.sessionAgent.SetExtensionUIHandler(m.extensionUIHandler)
}
