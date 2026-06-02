package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	model.messages = append(model.messages, interactiveMessage{
		Role: interactiveRoleSystem,
		Text: fmt.Sprintf("pi-go %s  cwd=%s  model=%s/%s", Version, agent.Session.CWD(), agent.Model.Provider, agent.Model.ID),
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
	}
	m.input, cmd = m.input.Update(msg)
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

func (m *interactiveModel) View() tea.View {
	width := max(1, m.width)
	header := interactiveHeaderStyle.Render(m.header())
	body := m.viewport.View()
	suggestions := m.renderSuggestions()
	input := interactiveInputStyle.Width(max(1, width-2)).Render(m.input.View())
	footer := interactiveFooterStyle.Render(m.footer())
	parts := []string{header, body}
	if suggestions != "" {
		parts = append(parts, suggestions)
	}
	parts = append(parts, input, footer)
	view := tea.NewView(strings.Join(parts, "\n"))
	view.KeyboardEnhancements.ReportEventTypes = true
	view.KeyboardEnhancements.ReportAlternateKeys = true
	return view
}

func (m *interactiveModel) submitInput() tea.Cmd {
	return m.submitInputWithBehavior("")
}

func (m *interactiveModel) submitInputWithBehavior(behavior StreamingBehavior) tea.Cmd {
	raw := m.input.Value()
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	m.input.Reset()
	m.autoScroll = true
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
	return func() tea.Msg {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		done, err := handleSlashWithPrompt(ctx, m.runtime, line, nil, &stdout, &stderr)
		return interactiveCommandDoneMsg{
			Stdout: stdout.String(),
			Stderr: stderr.String(),
			Err:    err,
			Quit:   done,
		}
	}
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
	inputHeight := lipgloss.Height(m.input.View()) + 2
	suggestionHeight := 0
	if m.renderSuggestions() != "" {
		suggestionHeight = 1
	}
	bodyHeight := height - inputHeight - suggestionHeight - 3
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
	text := fmt.Sprintf("pi-go %s  %s/%s", Version, agent.Model.Provider, agent.Model.ID)
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
		parts = append(parts, "tab="+suggestions[0])
	}
	return tui.TruncateToWidth(strings.Join(parts, "  "), max(1, m.width), "...")
}

func (m *interactiveModel) renderSuggestions() string {
	suggestions := m.currentSuggestions()
	if len(suggestions) == 0 {
		return ""
	}
	return interactiveSuggestionStyle.Render("  " + strings.Join(suggestions, "  "))
}

func (m *interactiveModel) completeSlashCommand() bool {
	suggestions := m.currentSuggestions()
	if len(suggestions) == 0 {
		return false
	}
	m.input.SetValue(suggestions[0] + " ")
	return true
}

func slashCommandSuggestions(input string) []string {
	return interactiveSuggestions(input, nil)
}

func (m *interactiveModel) currentSuggestions() []string {
	agent, _ := runtimeAgent(m.runtime)
	return interactiveSuggestions(m.input.Value(), agent)
}

func interactiveSuggestions(input string, agent *AgentSession) []string {
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

func (m *interactiveModel) bindSession(agent *AgentSession) {
	m.unbindSession()
	if agent == nil {
		return
	}
	m.sessionUnsubscribe = agent.Subscribe(func(event SessionEvent) {
		if m.post != nil {
			m.post(interactiveSessionEventMsg{Event: event})
		}
	})
}

func (m *interactiveModel) unbindSession() {
	if m.sessionUnsubscribe != nil {
		m.sessionUnsubscribe()
		m.sessionUnsubscribe = nil
	}
}
