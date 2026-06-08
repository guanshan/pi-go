package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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

type interactiveMessageKind string

const (
	interactiveMessagePlain  interactiveMessageKind = ""
	interactiveMessageTool   interactiveMessageKind = "tool"
	interactiveMessageBash   interactiveMessageKind = "bash"
	interactiveMessageCustom interactiveMessageKind = "custom"
)

const interactiveTranscriptPreviewLines = 20

type interactiveMessage struct {
	Role               interactiveRole
	Text               string
	Kind               interactiveMessageKind
	ToolName           string
	ToolCallID         string
	ToolArgs           json.RawMessage
	ToolIsError        bool
	ToolPartial        bool
	BashCommand        string
	BashExitCode       *int
	BashCancelled      bool
	BashTruncated      bool
	BashFullOutputPath string
	BashExclude        bool
	// Custom (extension pi.sendMessage) message rendering. CustomLines holds the
	// registered renderer's pre-rendered output (rendered once on receipt to keep
	// the per-keystroke render path off the Node bridge); when nil the transcript
	// falls back to a bold [customType] label + markdown of CustomText.
	CustomType  string
	CustomText  string
	CustomLines []string
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
	Bash   *interactiveBashResult
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

type externalEditorDoneMsg struct {
	Text string
	Err  error
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

type interactiveBashResult struct {
	Command        string
	Output         string
	ExitCode       *int
	Cancelled      bool
	Truncated      bool
	FullOutputPath string
	Exclude        bool
	IsError        bool
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
	inputImages        []ai.ContentBlock
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
	styles             interactiveThemeStyles
	keybindings        *KeybindingsManager
	toolsExpanded      bool
	pastes             map[int]string
	pasteCounter       int
	// extensionCompletions memoizes the result of the (potentially slow, 250ms-
	// bounded) extension autocomplete RPC for extensionCompletionInput, so the
	// several currentCompletions/currentSuggestions calls per render reuse one
	// computation instead of each re-invoking the provider. extensionCompletionValid
	// distinguishes a cached empty result from "not yet computed".
	extensionCompletionInput string
	extensionCompletions     []interactiveCompletion
	extensionCompletionValid bool
	builtinCompletionCache   interactiveCompletionCache
	extensionStatuses        map[string]string
	// Lightweight extension UI state (ctx.ui.*). windowTitle drives View().
	// WindowTitle; the working* fields adjust the footer's busy indicator;
	// hiddenThinkingLabel is stored for the (not-yet-rendered) collapsed-thinking
	// surface. workingHidden defaults to false so the zero value is "visible".
	windowTitle              string
	workingMessage           *string
	workingHidden            bool
	workingIndicatorSet      bool
	workingIndicatorFrames   []string
	workingIndicatorInterval int
	hiddenThinkingLabel      string
	// extensionWidgets hold plain-text ctx.ui.setWidget content keyed by widget
	// key, rendered above/below the editor (TS WidgetPlacement). Nil until first use.
	extensionWidgetsAbove map[string][]string
	extensionWidgetsBelow map[string][]string
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

var interactiveSlashCommandDescriptions = map[string]string{
	"login":         "Configure provider authentication",
	"logout":        "Remove provider authentication",
	"model":         "Select model (opens selector UI)",
	"thinking":      "Cycle thinking level",
	"scoped-models": "Enable/disable models for Ctrl+P cycling",
	"settings":      "Open settings menu",
	"resume":        "Resume a different session",
	"new":           "Start a new session",
	"import":        "Import and resume a session from a JSONL file",
	"name":          "Set session display name",
	"session":       "Show session info and stats",
	"compact":       "Manually compact the session context",
	"export":        "Export session (HTML default, or specify path: .html/.jsonl)",
	"copy":          "Copy last agent message to clipboard",
	"share":         "Share session as a secret GitHub gist",
	"tree":          "Navigate session tree (switch branches)",
	"fork":          "Create a new fork from a previous user message",
	"clone":         "Duplicate the current session at the current position",
	"changelog":     "Show changelog entries",
	"reload":        "Reload keybindings, extensions, skills, prompts, and themes",
	"debug":         "Show debug information",
	"help":          "Show commands",
	"hotkeys":       "Show all keyboard shortcuts",
	"quit":          "Quit",
	"exit":          "Quit",
	"q":             "Quit",
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
		styles:        interactiveThemeStylesFor(agent.Theme),
		keybindings:   agent.Keybindings,
		pastes:        map[int]string{},
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
		// Route keys to whichever overlay View() renders on top. View() draws the
		// model selector first (it replaces the editor region), so the selector must
		// win key handling too; an extension UI request that arrives while the
		// selector is open stays pending and is shown once the selector closes.
		// Keeping these in sync avoids a visible-but-unresponsive selector.
		if m.modelSelector != nil {
			cmd = m.handleModelSelectorKey(msg.String())
			m.refreshViewport()
			return m, cmd
		}
		if m.extensionUI != nil {
			cmd = m.handleExtensionUIKey(msg)
			m.refreshViewport()
			return m, cmd
		}
		key := msg.String()
		switch {
		case m.appKey(key, AppClipboardPasteImage):
			m.handleClipboardImagePaste()
			m.refreshViewport()
			return m, nil
		case m.appKey(key, AppClear):
			cmd = m.handleCtrlC()
			m.refreshViewport()
			return m, cmd
		case m.appKey(key, AppExit):
			if strings.TrimSpace(m.input.Value()) == "" {
				return m, tea.Quit
			}
		case m.appKey(key, AppInterrupt):
			cmd = m.handleEscape()
			m.refreshViewport()
			return m, cmd
		case m.appKey(key, AppSuspend):
			return m, tea.Suspend
		case key == "pgup":
			m.autoScroll = false
			m.viewport.PageUp()
			return m, nil
		case key == "pgdown":
			m.viewport.PageDown()
			m.autoScroll = m.viewport.AtBottom()
			return m, nil
		case key == "end":
			m.autoScroll = true
			m.viewport.GotoBottom()
			return m, nil
		case m.appKey(key, AppModelSelect):
			m.openModelSelector()
			m.refreshViewport()
			return m, nil
		case m.appKey(key, AppModelCycleForward):
			cmd = m.cycleModel(false)
			m.refreshViewport()
			return m, cmd
		case m.appKey(key, AppModelCycleBackward):
			cmd = m.cycleModel(true)
			m.refreshViewport()
			return m, cmd
		case m.appKey(key, AppThinkingCycle):
			cmd = m.cycleThinking()
			m.refreshViewport()
			return m, cmd
		case m.appKey(key, AppToolsExpand):
			m.toggleToolsExpanded()
			m.refreshViewport()
			return m, nil
		case m.appKey(key, AppThinkingToggle):
			m.toggleThinkingBlockVisibility()
			m.refreshViewport()
			return m, nil
		case m.appKey(key, AppEditorExternal):
			cmd = m.openExternalEditor()
			m.refreshViewport()
			return m, cmd
		case m.appKey(key, AppMessageFollowUp):
			cmd = m.submitInputWithBehavior(StreamingFollowUp)
			m.refreshViewport()
			return m, cmd
		case m.appKey(key, AppMessageDequeue):
			m.handleDequeue()
			m.refreshViewport()
			return m, nil
		case key == "up":
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
		case key == "down":
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
		case key == "tab":
			if m.completeSlashCommand() {
				m.refreshViewport()
				return m, nil
			}
		case key == "ctrl+j" || key == "shift+enter":
			m.input.InsertString("\n")
			m.refreshViewport()
			return m, nil
		case key == "enter":
			cmd = m.submitInputWithBehavior("")
			m.refreshViewport()
			return m, cmd
		}
		if cmd = m.extensionShortcutCommand(key); cmd != nil {
			m.refreshViewport()
			return m, cmd
		}
	case tea.PasteMsg:
		m.handlePaste(msg.Content)
		m.refreshViewport()
		return m, nil
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
		if msg.Bash != nil {
			m.appendBashMessage(*msg.Bash)
		} else if strings.TrimSpace(msg.Stdout) != "" {
			m.appendMessage(interactiveRoleSystem, strings.TrimSpace(msg.Stdout))
		}
		if msg.Bash == nil && strings.TrimSpace(msg.Stderr) != "" {
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
	case externalEditorDoneMsg:
		if msg.Err != nil {
			m.appendMessage(interactiveRoleError, msg.Err.Error())
		} else {
			m.input.SetValue(msg.Text)
			m.clearPastes()
			m.historyIndex = -1
			m.autocompleteIndex = 0
			m.setStatus("External editor updated the draft.")
		}
		m.refreshViewport()
		return m, nil
	case interactiveExtensionUIStateMsg:
		m.applyExtensionUIState(msg.Method, msg.Params)
		m.refreshViewport()
		return m, nil
	case interactiveExtensionGetEditorTextMsg:
		if msg.Response != nil {
			msg.Response <- m.expandedInputText()
		}
		return m, nil
	case interactiveExtensionEditorMsg:
		cmd = m.runExtensionEditor(msg.Title, msg.Prefill, msg.Response)
		m.refreshViewport()
		return m, cmd
	case interactiveExtensionEditorDoneMsg:
		m.refreshViewport()
		return m, nil
	case interactiveExtensionUIRequestMsg:
		m.enqueueExtensionUIRequest(msg.Request)
		m.refreshViewport()
		return m, nil
	case interactiveExtensionStatusMsg:
		m.setExtensionStatus(msg.Key, msg.Text)
		m.refreshViewport()
		return m, nil
	case interactiveExtensionUICancelMsg:
		m.cancelExtensionUIRequest(msg.Request)
		m.refreshViewport()
		return m, nil
	case interactiveExtensionTriggerTurnMsg:
		cmd = m.handleExtensionTriggerTurn()
		m.refreshViewport()
		return m, cmd
	case interactiveExtensionUserMessageMsg:
		cmd = m.handleExtensionUserMessage(msg.Options, msg.Response)
		m.refreshViewport()
		return m, cmd
	case interactiveExtensionCustomMessageMsg:
		m.handleExtensionCustomMessage(msg)
		m.refreshViewport()
		return m, nil
	case interactiveExtensionShortcutDoneMsg:
		if msg.Err != nil {
			m.appendMessage(interactiveRoleError, msg.Err.Error())
		} else {
			m.setStatus(fmt.Sprintf("Extension shortcut complete: %s", firstNonEmpty(msg.Description, msg.Key)))
		}
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

func (m *interactiveModel) View() tea.View {
	width := max(1, m.width)
	header := m.styles.Header.Render(m.header())
	body := m.viewport.View()
	footer := m.styles.Footer.Render(m.footer())
	parts := []string{header, body}
	if m.modelSelector != nil {
		// While the selector is open, its rendered lines replace the input
		// region (and suggestions), mirroring TS showModelSelector swapping the
		// editor container for the selector component. Header/body/footer stay.
		parts = append(parts, m.modelSelector.Render(width)...)
	} else if m.extensionUI != nil {
		parts = append(parts, m.renderExtensionUI(width)...)
	} else {
		// ctx.ui.setWidget plain-text widgets bracket the editor region.
		parts = append(parts, m.renderExtensionWidgets("aboveEditor")...)
		if suggestions := m.renderSuggestions(); suggestions != "" {
			parts = append(parts, suggestions)
		}
		input := m.styles.Input.Width(max(1, width-2)).Render(m.input.View())
		parts = append(parts, input)
		parts = append(parts, m.renderExtensionWidgets("belowEditor")...)
	}
	parts = append(parts, footer)
	view := tea.NewView(strings.Join(parts, "\n"))
	view.KeyboardEnhancements.ReportEventTypes = true
	view.KeyboardEnhancements.ReportAlternateKeys = true
	// ctx.ui.setTitle sets the terminal window title (bubbletea emits the OSC
	// sequence when WindowTitle changes between renders).
	view.WindowTitle = m.windowTitle
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
	expandedRaw := m.expandPasteMarkers(raw)
	text := strings.TrimSpace(expandedRaw)
	displayText := strings.TrimSpace(raw)
	if displayText == "" {
		displayText = text
	}
	if text == "" {
		return nil
	}
	m.addToHistory(expandedRaw)
	m.historyIndex = -1
	m.input.Reset()
	m.clearPastes()
	images := append([]ai.ContentBlock(nil), m.inputImages...)
	m.inputImages = nil
	m.autoScroll = true
	if !m.busy && text == "/model" {
		// Bare `/model` opens the navigable selector overlay, mirroring TS
		// interactive-mode showModelSelector. `/model provider/id` (with an
		// argument) still routes to the slash handler -> SetModel below.
		m.openModelSelector()
		return nil
	}
	if m.busy {
		return m.queueBusyInput(text, displayText, images, behavior)
	}
	m.appendMessage(interactiveRoleUser, displayText)
	m.busy = true
	switch {
	case len(images) == 0 && strings.HasPrefix(text, "/"):
		m.busyKind = interactiveBusyCommand
		return m.runSlashCommand(m.beginCommand(), text)
	case len(images) == 0 && strings.HasPrefix(text, "!"):
		m.busyKind = interactiveBusyCommand
		return m.runBashCommand(m.beginCommand(), text)
	default:
		m.busyKind = interactiveBusyAgent
		return m.runAgentPrompt(text, images)
	}
}

func (m *interactiveModel) queueBusyInput(text, displayText string, images []ai.ContentBlock, behavior StreamingBehavior) tea.Cmd {
	m.appendMessage(interactiveRoleUser, displayText)
	if (len(images) == 0 && (strings.HasPrefix(text, "/") || strings.HasPrefix(text, "!"))) || m.busyKind != interactiveBusyAgent {
		m.localQueue = append(m.localQueue, interactiveQueuedInput{Text: text, Images: append([]ai.ContentBlock(nil), images...)})
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
	return m.runQueuePrompt(text, images, behavior)
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
	case len(next.Images) == 0 && strings.HasPrefix(next.Text, "/"):
		m.busyKind = interactiveBusyCommand
		return m.runSlashCommand(m.beginCommand(), next.Text), true
	case len(next.Images) == 0 && strings.HasPrefix(next.Text, "!"):
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
		result := (catools.BashTool{CWD: agent.Session.CWD(), BinDir: agentSessionBinDir(agent)}).Execute(ctx, mustJSON(map[string]any{"command": command}), nil)
		text := ai.MessageText(ai.ToolResultMessage{Content: result.Content})
		exit := 0
		if result.IsError {
			exit = 1
		}
		_ = agent.Session.Append(SessionEntry{Type: "message", Message: BashExecutionMessage{Role: "bashExecution", Command: command, Output: text, ExitCode: &exit, ExcludeFromContext: exclude}})
		return interactiveCommandDoneMsg{Bash: &interactiveBashResult{
			Command:        command,
			Output:         text,
			ExitCode:       &exit,
			Truncated:      toolDetailBool(result.Details, "truncated"),
			FullOutputPath: toolDetailString(result.Details, "fullOutputPath"),
			Exclude:        exclude,
			IsError:        result.IsError,
		}}
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
		id := strings.TrimSpace(fmt.Sprint(event["toolCallId"]))
		slot := m.appendMessageEntry(interactiveMessage{
			Role:        interactiveRoleTool,
			Kind:        interactiveMessageTool,
			ToolName:    name,
			ToolCallID:  id,
			ToolArgs:    interactiveRawMessage(event["args"]),
			ToolPartial: true,
		})
		if id != "" && id != "<nil>" {
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
	widgetHeight := 0
	if m.modelSelector != nil {
		controlHeight = len(m.modelSelector.Render(width))
	} else if m.extensionUI != nil {
		controlHeight = len(m.renderExtensionUI(width))
	} else {
		// View() brackets the editor with ctx.ui.setWidget rows; reserve their
		// height too so the transcript body doesn't overflow the terminal.
		widgetHeight = len(m.renderExtensionWidgets("aboveEditor")) + len(m.renderExtensionWidgets("belowEditor"))
		if m.renderSuggestions() != "" {
			suggestionHeight = 1
		}
	}
	bodyHeight := height - controlHeight - suggestionHeight - widgetHeight - 3
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

func (m *interactiveModel) interactiveMessagePrefix(role interactiveRole) (string, lipgloss.Style) {
	switch role {
	case interactiveRoleUser:
		return "you  ", m.styles.User
	case interactiveRoleAssistant:
		return "pi   ", m.styles.Assistant
	case interactiveRoleTool:
		return "tool ", m.styles.Tool
	case interactiveRoleError:
		return "err  ", m.styles.Error
	default:
		return "info ", m.styles.System
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
		status = m.workingFooterStatus()
	}
	stats := agent.GetSessionStats()
	parts := []string{}
	if status != "" {
		parts = append(parts, status)
	}
	parts = append(parts,
		"cwd="+agent.Session.CWD(),
		fmt.Sprintf("messages=%d", stats.TotalMessages),
	)
	if stats.Tokens.Total > 0 {
		parts = append(parts, fmt.Sprintf("tokens=%d", stats.Tokens.Total))
	}
	if queued := len(m.queuedSteering) + len(m.queuedFollowUp) + len(m.localQueue) + len(m.initialQueue); queued > 0 {
		parts = append(parts, fmt.Sprintf("queued=%d", queued))
	}
	if m.statusMessage != "" {
		parts = append(parts, m.statusMessage)
	}
	parts = append(parts, m.extensionStatusValues()...)
	if suggestions := m.currentSuggestions(); len(suggestions) > 0 {
		parts = append(parts, "tab="+suggestions[m.selectedSuggestionIndex(suggestions)])
	}
	return tui.TruncateToWidth(strings.Join(parts, "  "), max(1, m.width), "...")
}

func (m *interactiveModel) handleCtrlC() tea.Cmd {
	now := time.Now()
	if now.Sub(m.lastCtrlC) < 500*time.Millisecond {
		return tea.Quit
	}
	m.lastCtrlC = now
	if strings.TrimSpace(m.input.Value()) != "" {
		m.input.Reset()
		m.inputImages = nil
		m.clearPastes()
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
		m.inputImages = nil
		m.clearPastes()
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

func (m *interactiveModel) bindSession(agent *AgentSession) {
	m.unbindSession()
	if agent == nil {
		return
	}
	m.sessionAgent = agent
	m.styles = interactiveThemeStylesFor(agent.Theme)
	m.keybindings = agent.Keybindings
	tui.SetKeybindings(m.keybindings.Manager())
	m.sessionAgent.SetExtensionMode("tui")
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
		m.sessionAgent.SetExtensionTriggerTurnHandler(nil)
		m.sessionAgent.SetExtensionUserMessageHandler(nil)
		m.sessionAgent.SetExtensionCustomMessageHandler(nil)
		m.sessionAgent = nil
	}
	if m.sessionUnsubscribe != nil {
		m.sessionUnsubscribe()
		m.sessionUnsubscribe = nil
	}
}
