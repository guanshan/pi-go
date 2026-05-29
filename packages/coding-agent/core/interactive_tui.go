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

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

type interactiveModel struct {
	ctx           context.Context
	runtime       *AgentSessionRuntime
	post          func(tea.Msg)
	input         textarea.Model
	viewport      viewport.Model
	messages      []interactiveMessage
	busy          bool
	width         int
	height        int
	assistantSlot int
	autoScroll    bool
	initial       string
	initialImages []ai.ContentBlock
	initialQueue  []string
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
		cmds = append(cmds, m.runAgentPrompt(m.initial, m.initialImages))
	} else if next, ok := m.popInitialQueue(); ok {
		m.appendMessage(interactiveRoleUser, next)
		m.busy = true
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
			return m, tea.Quit
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
		case "enter":
			cmd = m.submitInput()
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
	case interactivePromptDoneMsg:
		m.busy = false
		m.assistantSlot = -1
		if msg.Err != nil {
			m.appendMessage(interactiveRoleError, msg.Err.Error())
		}
		if msg.Err == nil {
			if next, ok := m.popInitialQueue(); ok {
				m.appendMessage(interactiveRoleUser, next)
				m.busy = true
				m.refreshViewport()
				return m, m.runAgentPrompt(next, nil)
			}
		}
		m.refreshViewport()
		return m, nil
	case interactiveCommandDoneMsg:
		m.busy = false
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
	return tea.NewView(strings.Join(parts, "\n"))
}

func (m *interactiveModel) submitInput() tea.Cmd {
	raw := m.input.Value()
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	m.input.Reset()
	m.autoScroll = true
	m.appendMessage(interactiveRoleUser, text)
	if m.busy {
		m.appendMessage(interactiveRoleSystem, "A turn is still running. Wait for it to finish, then send the next message.")
		return nil
	}
	m.busy = true
	switch {
	case strings.HasPrefix(text, "/"):
		return m.runSlashCommand(text)
	case strings.HasPrefix(text, "!"):
		return m.runBashCommand(text)
	default:
		return m.runAgentPrompt(text, nil)
	}
}

func (m *interactiveModel) runAgentPrompt(text string, images []ai.ContentBlock) tea.Cmd {
	return func() tea.Msg {
		agent, err := runtimeAgent(m.runtime)
		if err != nil {
			return interactivePromptDoneMsg{Err: err}
		}
		err = agent.Prompt(m.ctx, text, images, func(event ai.Event) {
			if m.post != nil {
				m.post(interactiveAgentEventMsg{Event: event})
			}
		})
		return interactivePromptDoneMsg{Err: err}
	}
}

func (m *interactiveModel) runSlashCommand(line string) tea.Cmd {
	return func() tea.Msg {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		done, err := handleSlashWithPrompt(m.ctx, m.runtime, line, nil, &stdout, &stderr)
		return interactiveCommandDoneMsg{
			Stdout: stdout.String(),
			Stderr: stderr.String(),
			Err:    err,
			Quit:   done,
		}
	}
}

func (m *interactiveModel) runBashCommand(line string) tea.Cmd {
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
		result := (catools.BashTool{CWD: agent.Session.CWD()}).Execute(m.ctx, mustJSON(map[string]any{"command": command}), nil)
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
	case "tool_execution_start":
		name := fmt.Sprint(event["toolName"])
		if strings.TrimSpace(name) == "" || name == "<nil>" {
			name = "tool"
		}
		m.assistantSlot = -1
		m.appendMessage(interactiveRoleTool, "["+name+"]")
	case "tool_execution_end":
		if result, ok := event["result"].(ai.ToolResult); ok {
			text := strings.TrimSpace(ai.MessageText(ai.ToolResultMessage{Content: result.Content}))
			if text != "" {
				m.appendMessage(interactiveRoleTool, text)
			}
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

func (m *interactiveModel) appendAssistantDelta(delta string) {
	if m.assistantSlot < 0 || m.assistantSlot >= len(m.messages) || m.messages[m.assistantSlot].Role != interactiveRoleAssistant {
		m.messages = append(m.messages, interactiveMessage{Role: interactiveRoleAssistant})
		m.assistantSlot = len(m.messages) - 1
	}
	m.messages[m.assistantSlot].Text += delta
}

func (m *interactiveModel) appendMessage(role interactiveRole, text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	m.messages = append(m.messages, interactiveMessage{Role: role, Text: text})
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
		lines := strings.Split(text, "\n")
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
	if suggestions := slashCommandSuggestions(m.input.Value()); len(suggestions) > 0 {
		parts = append(parts, "tab="+suggestions[0])
	}
	return tui.TruncateToWidth(strings.Join(parts, "  "), max(1, m.width), "...")
}

func (m *interactiveModel) renderSuggestions() string {
	suggestions := slashCommandSuggestions(m.input.Value())
	if len(suggestions) == 0 {
		return ""
	}
	return interactiveSuggestionStyle.Render("  " + strings.Join(suggestions, "  "))
}

func (m *interactiveModel) completeSlashCommand() bool {
	suggestions := slashCommandSuggestions(m.input.Value())
	if len(suggestions) == 0 {
		return false
	}
	m.input.SetValue(suggestions[0] + " ")
	return true
}

func slashCommandSuggestions(input string) []string {
	text := strings.TrimSpace(input)
	if !strings.HasPrefix(text, "/") || strings.Contains(text, " ") {
		return nil
	}
	prefix := strings.TrimPrefix(text, "/")
	matches := make([]string, 0, 6)
	for _, command := range interactiveSlashCommands {
		if strings.HasPrefix(command, prefix) {
			matches = append(matches, "/"+command)
		}
	}
	sort.Strings(matches)
	if len(matches) > 6 {
		matches = matches[:6]
	}
	return matches
}
