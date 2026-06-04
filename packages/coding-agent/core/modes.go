package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
	cautils "github.com/guanshan/pi-go/packages/coding-agent/utils"
	"github.com/guanshan/pi-go/packages/tui"
)

// RunPrintMode mirrors the TS print (single-shot) mode
// (src/modes/print-mode.ts:32-158). It runs every prompt turn and then emits
// the result: for JSON mode it streams the session header plus every event; for
// text mode it prints ONLY the final assistant message's text content to
// stdout, with no per-turn streaming or "[tool]" noise. An assistant message
// that stopped with "error"/"aborted" is reported to stderr and yields exit
// code 1 (print-mode.ts:128-145). Any prompt error is written to stderr and
// also yields exit code 1 (print-mode.ts:148-150).
func RunPrintMode(ctx context.Context, runtime *AgentSessionRuntime, mode cli.Mode, message string, images []ai.ContentBlock, stdout, stderr io.Writer, remainingMessages ...[]string) (int, error) {
	agent, err := runtimeAgent(runtime)
	if err != nil {
		return 1, err
	}
	turns := promptTurns(message, images, remainingMessages...)
	if len(turns) == 0 {
		return 0, nil
	}
	if mode == cli.ModeJSON {
		_ = writeJSONLine(stdout, agent.Session.Header)
		sink := func(event ai.Event) { _ = writeJSONLine(stdout, event) }
		for _, turn := range turns {
			if err := agent.Prompt(ctx, turn.Message, turn.Images, sink); err != nil {
				fmt.Fprintln(stderr, err)
				return 1, nil
			}
		}
		return 0, nil
	}
	// Text mode: run all turns first (no streaming/tool output), then emit the
	// final assistant text only. Mirrors print-mode.ts:120-126,148-150.
	for _, turn := range turns {
		if err := agent.Prompt(ctx, turn.Message, turn.Images, nil); err != nil {
			fmt.Fprintln(stderr, err)
			return 1, nil
		}
	}
	return printFinalAssistantText(agent, stdout, stderr), nil
}

// printFinalAssistantText mirrors print-mode.ts:128-145: it inspects the last
// message in the session state and, when it is an assistant message, either
// reports the error/aborted stop reason to stderr (exit code 1) or writes each
// text content block to stdout. Non-assistant trailing messages produce no
// output and exit code 0.
func printFinalAssistantText(agent *AgentSession, stdout, stderr io.Writer) int {
	messages := agent.Session.BuildContext().Messages
	if len(messages) == 0 {
		return 0
	}
	last := messages[len(messages)-1]
	if ai.MessageRole(last) != "assistant" {
		return 0
	}
	assistant, _ := ai.AsAssistantMessage(last)
	if assistant.StopReason == "error" || assistant.StopReason == "aborted" {
		errorMessage := assistant.ErrorMessage
		if errorMessage == "" {
			errorMessage = fmt.Sprintf("Request %s", assistant.StopReason)
		}
		fmt.Fprintln(stderr, errorMessage)
		return 1
	}
	for _, block := range ai.MessageBlocks(last) {
		if block.Type == "text" {
			fmt.Fprintln(stdout, block.Text)
		}
	}
	return 0
}

func RunInteractiveMode(ctx context.Context, runtime *AgentSessionRuntime, initial string, images []ai.ContentBlock, stdin io.Reader, stdout, stderr io.Writer, remainingMessages ...[]string) error {
	remaining := flattenPromptMessages(remainingMessages...)
	showStartupChangelog(runtime, stdout)
	if shouldRunBubbleInteractive(stdin, stdout) {
		return runBubbleInteractiveMode(ctx, runtime, initial, images, stdin, stdout, stderr, remaining...)
	}
	return runLineInteractiveMode(ctx, runtime, initial, images, stdin, stdout, stderr, remaining...)
}

// showStartupChangelog displays the post-upgrade changelog exactly once after a
// version change, then persists the recorded version (lastChangelogVersion) so it
// is not shown again. Mirrors the changelog display wired into interactive-mode.ts.
func showStartupChangelog(runtime *AgentSessionRuntime, stdout io.Writer) {
	if runtime == nil {
		return
	}
	agent := runtime.Session()
	if agent == nil || agent.Settings == nil || agent.Session == nil {
		return
	}
	hasMessages := len(agent.Session.BuildContext().Messages) > 0
	before := agent.Settings.LastChangelogVersion()
	changelog := ChangelogForDisplay(agent.Settings, hasMessages, cautils.ChangelogPath())
	// Persist only when ChangelogForDisplay recorded a new version (fresh install
	// or after an upgrade), even if there is nothing to display this run.
	if agent.Settings.LastChangelogVersion() != before {
		_ = agent.Settings.SaveGlobal()
	}
	if changelog == "" || stdout == nil {
		return
	}
	fmt.Fprintln(stdout, "What's New")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, changelog)
}

func runLineInteractiveMode(ctx context.Context, runtime *AgentSessionRuntime, initial string, images []ai.ContentBlock, stdin io.Reader, stdout, stderr io.Writer, remaining ...string) error {
	agent, err := runtimeAgent(runtime)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdin)
	uiHandler := newLineExtensionUIHandler(scanner, stdout)
	bindRuntimeExtensionUI(runtime, uiHandler)
	defer clearRuntimeExtensionUI(runtime)
	currentModel := agent.CurrentModel()
	fmt.Fprintln(stdout, tui.TruncateToWidth(fmt.Sprintf("pi-go %s  cwd=%s  model=%s/%s", Version, agent.Session.CWD(), currentModel.Provider, currentModel.ID), 120, "..."))
	fmt.Fprintln(stdout, "Type /help for commands, /quit to exit.")
	if strings.TrimSpace(initial) != "" || len(images) > 0 {
		if err := interactivePrompt(ctx, agent, initial, images, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
		}
	}
	for _, message := range remaining {
		if strings.TrimSpace(message) == "" {
			continue
		}
		agent, err = runtimeAgent(runtime)
		if err != nil {
			return err
		}
		if err := interactivePrompt(ctx, agent, message, nil, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
		}
	}
	for {
		fmt.Fprint(stdout, "> ")
		if !scanner.Scan() {
			break
		}
		agent, err := runtimeAgent(runtime)
		if err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			prompt := func(prompt ai.OAuthPrompt) (string, error) {
				label := prompt.Message
				if label == "" {
					label = "Enter value"
				}
				if prompt.Placeholder != "" {
					fmt.Fprintf(stdout, "%s [%s]: ", label, prompt.Placeholder)
				} else {
					fmt.Fprintf(stdout, "%s: ", label)
				}
				if !scanner.Scan() {
					if err := scanner.Err(); err != nil {
						return "", err
					}
					return "", io.EOF
				}
				value := scanner.Text()
				if strings.TrimSpace(value) == "" && !prompt.AllowEmpty {
					return "", errorsString("input cannot be empty")
				}
				return value, nil
			}
			if done, err := handleSlashWithPrompt(ctx, runtime, line, prompt, stdout, stderr); err != nil {
				fmt.Fprintln(stderr, "Error:", err)
			} else if done {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, "!") {
			exclude := strings.HasPrefix(line, "!!")
			command := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "!"), "!"))
			result := (catools.BashTool{CWD: agent.Session.CWD(), BinDir: BinDir()}).Execute(ctx, mustJSON(map[string]any{"command": command}), nil)
			text := ai.MessageText(ai.ToolResultMessage{Content: result.Content})
			fmt.Fprintln(stdout, text)
			exit := 0
			if result.IsError {
				exit = 1
			}
			_ = agent.Session.Append(SessionEntry{Type: "message", Message: ai.CustomMessage{Role: "bashExecution", Command: command, Output: text, ExitCode: &exit, ExcludeFromContext: exclude}})
			continue
		}
		if err := interactivePrompt(ctx, agent, line, nil, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "Error:", err)
		}
	}
	return scanner.Err()
}

type promptTurn struct {
	Message string
	Images  []ai.ContentBlock
}

func promptTurns(initial string, images []ai.ContentBlock, remainingMessages ...[]string) []promptTurn {
	var turns []promptTurn
	if strings.TrimSpace(initial) != "" || len(images) > 0 {
		turns = append(turns, promptTurn{Message: initial, Images: images})
	}
	for _, message := range flattenPromptMessages(remainingMessages...) {
		if strings.TrimSpace(message) == "" {
			continue
		}
		turns = append(turns, promptTurn{Message: message})
	}
	return turns
}

func flattenPromptMessages(values ...[]string) []string {
	var out []string
	for _, messages := range values {
		out = append(out, messages...)
	}
	return out
}

func runtimeAgent(runtime *AgentSessionRuntime) (*AgentSession, error) {
	if runtime == nil || runtime.Session() == nil {
		return nil, fmt.Errorf("runtime has no active session")
	}
	return runtime.Session(), nil
}

func bindRuntimeExtensionUI(runtime *AgentSessionRuntime, handler coreext.UIRequestHandler) {
	if runtime == nil {
		return
	}
	runtime.SetBeforeSessionInvalidate(func() {
		if agent := runtime.Session(); agent != nil {
			agent.SetExtensionUIHandler(nil)
		}
	})
	runtime.SetRebindSession(func(agent *AgentSession) error {
		if agent != nil {
			agent.SetExtensionUIHandler(handler)
		}
		return nil
	})
	if agent := runtime.Session(); agent != nil {
		agent.SetExtensionUIHandler(handler)
	}
}

func clearRuntimeExtensionUI(runtime *AgentSessionRuntime) {
	if runtime == nil {
		return
	}
	if agent := runtime.Session(); agent != nil {
		agent.SetExtensionUIHandler(nil)
	}
}

type extensionUIChoice struct {
	Raw   json.RawMessage
	Label string
}

type extensionUIPrompt struct {
	Message      string
	Detail       string
	Level        string
	Placeholder  string
	DefaultValue string
	Choices      []extensionUIChoice
}

type extensionUIResult struct {
	Result json.RawMessage
	Err    error
}

func parseExtensionUIPrompt(method string, params json.RawMessage) extensionUIPrompt {
	var payload struct {
		Message string            `json:"message"`
		Detail  string            `json:"detail"`
		Level   string            `json:"level"`
		Choices []json.RawMessage `json:"choices"`
		Options json.RawMessage   `json:"options"`
	}
	_ = json.Unmarshal(params, &payload)
	prompt := extensionUIPrompt{
		Message: strings.TrimSpace(payload.Message),
		Detail:  strings.TrimSpace(payload.Detail),
		Level:   strings.TrimSpace(payload.Level),
	}
	if prompt.Message == "" {
		prompt.Message = "Extension request"
	}
	for _, raw := range payload.Choices {
		if len(raw) == 0 {
			continue
		}
		prompt.Choices = append(prompt.Choices, extensionUIChoice{
			Raw:   append(json.RawMessage(nil), raw...),
			Label: extensionUIChoiceLabel(raw),
		})
	}
	if method == "input" && len(payload.Options) > 0 {
		var placeholder string
		if json.Unmarshal(payload.Options, &placeholder) == nil {
			prompt.Placeholder = placeholder
		} else {
			var options struct {
				Placeholder  string `json:"placeholder"`
				DefaultValue string `json:"defaultValue"`
				Value        string `json:"value"`
			}
			if json.Unmarshal(payload.Options, &options) == nil {
				prompt.Placeholder = options.Placeholder
				prompt.DefaultValue = firstNonEmpty(options.DefaultValue, options.Value)
			}
		}
	}
	return prompt
}

func extensionUIChoiceLabel(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) == nil {
		for _, key := range []string{"label", "title", "name", "value", "id"} {
			if value, ok := object[key]; ok {
				label := strings.TrimSpace(fmt.Sprint(value))
				if label != "" && label != "<nil>" {
					return label
				}
			}
		}
	}
	return strings.TrimSpace(string(raw))
}

func extensionUIJSON(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func newLineExtensionUIHandler(scanner *bufio.Scanner, stdout io.Writer) coreext.UIRequestHandler {
	return func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		prompt := parseExtensionUIPrompt(method, params)
		switch method {
		case "notify":
			if stdout != nil {
				level := firstNonEmpty(prompt.Level, "info")
				fmt.Fprintf(stdout, "\n[%s] %s\n", level, prompt.Message)
			}
			return json.RawMessage("null"), nil
		case "confirm":
			if stdout != nil {
				fmt.Fprintf(stdout, "\n%s", prompt.Message)
				if prompt.Detail != "" {
					fmt.Fprintf(stdout, "\n%s", prompt.Detail)
				}
				fmt.Fprint(stdout, "\nConfirm? [y/N]: ")
			}
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return nil, err
				}
				return json.RawMessage("false"), nil
			}
			answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
			return extensionUIJSON(answer == "y" || answer == "yes")
		case "select":
			if len(prompt.Choices) == 0 {
				return nil, fmt.Errorf("pi.ui.select requires at least one choice")
			}
			if stdout != nil {
				fmt.Fprintf(stdout, "\n%s\n", prompt.Message)
				for i, choice := range prompt.Choices {
					fmt.Fprintf(stdout, "  %d. %s\n", i+1, choice.Label)
				}
				fmt.Fprintf(stdout, "Select [1-%d]: ", len(prompt.Choices))
			}
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return nil, err
				}
				return json.RawMessage("null"), nil
			}
			answer := strings.TrimSpace(scanner.Text())
			if answer == "" {
				return prompt.Choices[0].Raw, nil
			}
			index, err := strconv.Atoi(answer)
			if err != nil || index < 1 || index > len(prompt.Choices) {
				return nil, fmt.Errorf("invalid selection: %s", answer)
			}
			return prompt.Choices[index-1].Raw, nil
		case "input":
			if stdout != nil {
				fmt.Fprintf(stdout, "\n%s", prompt.Message)
				switch {
				case prompt.DefaultValue != "":
					fmt.Fprintf(stdout, " [%s]", prompt.DefaultValue)
				case prompt.Placeholder != "":
					fmt.Fprintf(stdout, " [%s]", prompt.Placeholder)
				}
				fmt.Fprint(stdout, ": ")
			}
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return nil, err
				}
				return json.RawMessage("null"), nil
			}
			value := scanner.Text()
			if strings.TrimSpace(value) == "" && prompt.DefaultValue != "" {
				value = prompt.DefaultValue
			}
			return extensionUIJSON(value)
		default:
			return nil, fmt.Errorf("pi.ui.%s is not supported in this host", method)
		}
	}
}

func interactivePrompt(ctx context.Context, agent *AgentSession, message string, images []ai.ContentBlock, stdout, stderr io.Writer) error {
	streamingText := false
	sink := func(event ai.Event) {
		switch event["type"] {
		case "tool_execution_start":
			if streamingText {
				fmt.Fprintln(stdout)
				streamingText = false
			}
			fmt.Fprintf(stdout, "\n[%s]\n", event["toolName"])
		case "tool_execution_end":
			if result, ok := event["result"].(ai.ToolResult); ok {
				text := ai.MessageText(ai.ToolResultMessage{Content: result.Content})
				if text != "" {
					fmt.Fprintln(stdout, text)
				}
			}
		case "message_update":
			if assistantEvent, ok := event["assistantMessageEvent"].(ai.AssistantMessageEvent); ok && assistantEvent.Type == "text_delta" && assistantEvent.Delta != "" {
				fmt.Fprint(stdout, assistantEvent.Delta)
				streamingText = true
			}
		case "message_end":
			if msg, ok := event["message"].(ai.Message); ok && ai.MessageRole(msg) == "assistant" {
				if streamingText {
					fmt.Fprintln(stdout)
					streamingText = false
				} else {
					text := ai.MessageText(msg)
					if text != "" {
						fmt.Fprintln(stdout, text)
					}
				}
			}
		}
	}
	return agent.Prompt(ctx, message, images, sink)
}

func handleSlash(ctx context.Context, target any, line string, stdout, stderr io.Writer) (bool, error) {
	return handleSlashWithPrompt(ctx, target, line, nil, stdout, stderr)
}

type slashPrompter func(ai.OAuthPrompt) (string, error)

func handleSlashWithPrompt(ctx context.Context, target any, line string, prompter slashPrompter, stdout, stderr io.Writer) (bool, error) {
	runtime, agent, err := slashTarget(target)
	if err != nil {
		return false, err
	}
	fields := strings.Fields(line)
	cmd := strings.TrimPrefix(fields[0], "/")
	arg := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	switch cmd {
	case "quit", "exit", "q":
		return true, nil
	case "help", "hotkeys":
		fmt.Fprintln(stdout, slashHelp())
	case "session":
		raw, _ := json.MarshalIndent(agent.Session.Stats(), "", "  ")
		fmt.Fprintln(stdout, string(raw))
	case "model":
		if arg == "" {
			for _, model := range agent.Registry.List("") {
				auth := ""
				if agent.Registry.HasAuth(model) {
					auth = " *"
				}
				fmt.Fprintf(stdout, "%s/%s%s\n", model.Provider, model.ID, auth)
			}
		} else {
			provider := agent.CurrentModel().Provider
			modelID := arg
			if strings.Contains(arg, "/") {
				parts := strings.SplitN(arg, "/", 2)
				provider, modelID = parts[0], parts[1]
			}
			model, err := agent.SetModel(provider, modelID)
			if err != nil {
				if m, ok, _ := agent.Registry.Match("", arg); ok {
					model, err = agent.SetModel(m.Provider, m.ID)
				}
			}
			if err != nil {
				return false, err
			}
			fmt.Fprintf(stdout, "Model: %s/%s\n", model.Provider, model.ID)
		}
	case "thinking":
		// /thinking [level] sets the thinking level when given a valid level, else
		// cycles to the next level. This is the non-Kitty fallback for the Shift+Tab
		// cycle; success is reported via the printed level (interactive callers also
		// surface the emitted ThinkingLevelChangedEvent).
		if arg == "" {
			level, ok := agent.CycleThinkingLevel()
			if !ok {
				fmt.Fprintln(stdout, "No other thinking levels available")
			} else {
				fmt.Fprintf(stdout, "Thinking: %s\n", level)
			}
		} else {
			if !ai.IsValidThinkingLevel(arg) {
				return false, fmt.Errorf("invalid thinking level %q. Valid values: off, minimal, low, medium, high, xhigh", arg)
			}
			if err := agent.SetThinkingLevel(ai.ThinkingLevel(arg)); err != nil {
				return false, err
			}
			fmt.Fprintf(stdout, "Thinking: %s\n", agent.CurrentThinkingLevel())
		}
	case "scoped-models":
		for _, model := range agent.Registry.AvailableConfigured() {
			fmt.Fprintf(stdout, "%s/%s\n", model.Provider, model.ID)
		}
	case "settings":
		raw, _ := json.MarshalIndent(agent.State(), "", "  ")
		fmt.Fprintln(stdout, string(raw))
	case "debug":
		// Mirror TS interactive-mode handleDebugCommand: dump diagnostics + the
		// session messages (JSONL) to <agentDir>/pi-debug.log and report the path.
		// The rendered-line dump (this.ui.render) is a rich-TUI feature and is
		// intentionally omitted in line/rpc mode, so we dump only the JSONL state.
		if err := writeDebugLog(agent); err != nil {
			return false, err
		}
		fmt.Fprintf(stdout, "Debug log written\n%s\n", DebugLogPath())
	case "new":
		if runtime == nil {
			return false, fmt.Errorf("/new requires a session runtime")
		}
		_, err := runtime.NewSession(ctx, NewSessionOptions{})
		if err != nil {
			return false, err
		}
		current, err := runtimeAgent(runtime)
		if err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Started new session:", current.Session.SessionID())
	case "name":
		if err := agent.SetSessionName(arg); err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Session name set.")
	case "resume":
		if strings.TrimSpace(arg) != "" {
			if runtime == nil {
				return false, fmt.Errorf("/resume <session> requires a session runtime")
			}
			_, err := runtime.SwitchSession(ctx, arg, SwitchSessionOptions{})
			if err != nil {
				return false, err
			}
			current, err := runtimeAgent(runtime)
			if err != nil {
				return false, err
			}
			fmt.Fprintln(stdout, "Resumed session:", current.Session.SessionID())
			break
		}
		sessions, err := ListSessions(agent.Session.CWD(), agent.Settings.SessionDir())
		if err != nil {
			return false, err
		}
		for i, s := range sessions {
			fmt.Fprintf(stdout, "%d. %s %s %s\n", i+1, s.ID, s.UpdatedAt.Format("2006-01-02 15:04"), firstNonEmpty(s.Name, s.Preview))
		}
	case "import":
		if strings.TrimSpace(arg) == "" {
			fmt.Fprintln(stdout, "Usage: /import <session.jsonl>")
			return false, nil
		}
		if runtime == nil {
			return false, fmt.Errorf("/import requires a session runtime")
		}
		_, err := runtime.ImportFromJsonl(ctx, arg, "")
		if err != nil {
			return false, err
		}
		current, err := runtimeAgent(runtime)
		if err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Imported session:", current.Session.SessionID())
	case "compact":
		result, err := agent.CompactWithContext(ctx, arg, func(event ai.Event) {})
		if err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, result["summary"])
	case "export":
		out, err := ExportSessionToHTML(agent.Session.File(), arg)
		if err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Exported to:", out)
	case "copy":
		text := agent.GetLastAssistantText()
		if text == "" {
			return false, errorsString("No agent messages to copy yet.")
		}
		if err := CopyTextToClipboard(text, stdout); err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Copied last agent message to clipboard")
	case "share":
		result, err := ShareSessionHTML(ctx, agent.Session.File())
		if err != nil {
			return false, err
		}
		fmt.Fprintf(stdout, "Share URL: %s\nGist: %s\n", result.PreviewURL, result.GistURL)
	case "tree":
		if arg == "" {
			fmt.Fprint(stdout, FormatSessionTree(agent.Session))
		} else {
			result, err := agent.NavigateTree(ctx, arg, NavigateTreeOptions{})
			if err != nil {
				return false, err
			}
			fmt.Fprintln(stdout, "Navigated to entry:", result.NewLeafID)
		}
	case "clone":
		if runtime == nil {
			return false, fmt.Errorf("/clone requires a session runtime")
		}
		_, err := runtime.Fork(ctx, "", ForkOptions{Position: ForkPositionAt})
		if err != nil {
			return false, err
		}
		current, err := runtimeAgent(runtime)
		if err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Cloned to new session:", current.Session.SessionID())
	case "fork":
		if runtime == nil {
			return false, fmt.Errorf("/fork requires a session runtime")
		}
		if arg == "" {
			fmt.Fprint(stdout, FormatSessionTree(agent.Session))
			fmt.Fprintln(stdout, "Usage: /fork <entry-id>")
			return false, nil
		}
		_, err := runtime.Fork(ctx, arg, ForkOptions{Position: ForkPositionAt})
		if err != nil {
			return false, err
		}
		current, err := runtimeAgent(runtime)
		if err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Forked new session:", current.Session.SessionID())
	case "changelog":
		path := cautils.ChangelogPath()
		if arg != "" {
			path = ResolveInCWD(agent.Session.CWD(), arg)
		}
		fmt.Fprintln(stdout, "What's New")
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, cautils.FormatChangelogMarkdown(cautils.ParseChangelog(path)))
	case "login":
		provider, key, err := parseLoginArgs(arg)
		if err != nil {
			return false, err
		}
		if provider == "" {
			printAuthOverview(stdout, agent)
			return false, nil
		}
		if key == "" {
			if oauthProvider, ok := ai.GetOAuthProvider(ai.OAuthProviderID(provider)); ok {
				if prompter == nil {
					fmt.Fprintf(stdout, "OAuth login for %s requires interactive input.\n", provider)
					fmt.Fprintf(stdout, "Usage: /login %s <api-key>\n", provider)
					return false, nil
				}
				credentials, err := oauthProvider.Login(ai.OAuthLoginCallbacks{
					Context: ctx,
					OnAuth: func(info ai.OAuthAuthInfo) {
						if info.Instructions != "" {
							fmt.Fprintln(stdout, info.Instructions)
						}
						fmt.Fprintln(stdout, info.URL)
					},
					OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
						fmt.Fprintf(stdout, "Open %s and enter code %s\n", info.VerificationURI, info.UserCode)
					},
					OnPrompt: func(prompt ai.OAuthPrompt) (string, error) {
						return prompter(prompt)
					},
					OnProgress: func(message string) {
						fmt.Fprintln(stdout, message)
					},
					OnSelect: func(prompt ai.OAuthSelectPrompt) (string, bool, error) {
						if prompt.Message != "" {
							fmt.Fprintln(stdout, prompt.Message)
						}
						ids := make([]string, 0, len(prompt.Options))
						for _, option := range prompt.Options {
							ids = append(ids, option.ID)
							if option.Label != "" {
								fmt.Fprintf(stdout, "- %s: %s\n", option.ID, option.Label)
							} else {
								fmt.Fprintf(stdout, "- %s\n", option.ID)
							}
						}
						value, err := prompter(ai.OAuthPrompt{
							Message:     "Select login method",
							Placeholder: strings.Join(ids, "/"),
							AllowEmpty:  true,
						})
						if err != nil {
							return "", false, err
						}
						value = strings.TrimSpace(value)
						if value == "" && len(prompt.Options) > 0 {
							return prompt.Options[0].ID, true, nil
						}
						if value == "" {
							return "", false, nil
						}
						return value, true, nil
					},
				})
				if err != nil {
					return false, err
				}
				if err := agent.Registry.Auth.SaveOAuth(provider, credentials); err != nil {
					return false, err
				}
				fmt.Fprintln(stdout, "Saved OAuth credentials for", provider)
				return false, nil
			}
			if prompter != nil {
				key, err = prompter(ai.OAuthPrompt{Message: "Enter API key for " + provider})
				if err != nil {
					return false, err
				}
				key = strings.TrimSpace(key)
				if key != "" {
					if err := agent.Registry.Auth.SaveAPIKey(provider, key); err != nil {
						return false, err
					}
					fmt.Fprintln(stdout, "Saved API key for", provider)
					return false, nil
				}
			}
			fmt.Fprintf(stdout, "Usage: /login %s <api-key>\n", provider)
			fmt.Fprintln(stdout, "API keys are saved to auth.json. Use /login with no arguments to list providers.")
			return false, nil
		}
		if err := agent.Registry.Auth.SaveAPIKey(provider, key); err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Saved API key for", provider)
	case "logout":
		provider := strings.TrimSpace(arg)
		if provider == "" {
			stored := agent.Registry.Auth.List()
			if len(stored) == 0 {
				fmt.Fprintln(stdout, "No stored credentials to remove. Environment variables and models.json config are unchanged.")
				return false, nil
			}
			fmt.Fprintln(stdout, "Stored credentials:")
			for _, provider := range stored {
				status := agent.Registry.Auth.AuthStatus(provider)
				fmt.Fprintf(stdout, "%s  %s\n", provider, formatAuthStatus(status))
			}
			fmt.Fprintln(stdout, "Usage: /logout <provider>")
			return false, nil
		}
		if err := agent.Registry.Auth.Delete(provider); err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Removed stored credentials for", provider)
	case "reload":
		if err := agent.Reload(ctx); err != nil {
			return false, err
		}
		fmt.Fprintln(stdout, "Reloaded session resources.")
	default:
		if out, handled, err := executeExtensionSlashCommand(ctx, agent, cmd, arg); handled {
			if err != nil {
				return false, err
			}
			if strings.TrimSpace(out) != "" {
				fmt.Fprintln(stdout, out)
			}
			return false, nil
		}
		if expanded, ok := agent.Resources.ExpandInput(line); ok {
			return false, interactivePrompt(ctx, agent, expanded, nil, stdout, stderr)
		}
		return false, fmt.Errorf("unknown command: /%s", cmd)
	}
	return false, nil
}

func executeExtensionSlashCommand(ctx context.Context, agent *AgentSession, cmd, arg string) (string, bool, error) {
	if agent == nil || agent.extensionRuntime == nil {
		return "", false, nil
	}
	return agent.extensionRuntime.ExecuteCommand(ctx, cmd, arg)
}

// writeDebugLog dumps session diagnostics and the session messages (one JSON
// object per line) to DebugLogPath(), mirroring the JSONL portion of TS
// interactive-mode handleDebugCommand. The terminal render-tree dump is a
// rich-TUI feature and is intentionally not reproduced here.
func writeDebugLog(agent *AgentSession) error {
	if agent == nil || agent.Session == nil {
		return fmt.Errorf("/debug requires an active session")
	}
	path := DebugLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Debug output at %s\n", time.Now().UTC().Format(time.RFC3339))
	if stats, err := json.MarshalIndent(agent.Session.Stats(), "", "  "); err == nil {
		fmt.Fprintf(&b, "Session: %s\n", stats)
	}
	b.WriteString("\n=== Agent messages (JSONL) ===\n")
	for _, msg := range agent.Session.BuildContext().Messages {
		raw, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func slashTarget(target any) (*AgentSessionRuntime, *AgentSession, error) {
	switch value := target.(type) {
	case *AgentSessionRuntime:
		agent, err := runtimeAgent(value)
		return value, agent, err
	case *AgentSession:
		if value == nil {
			return nil, nil, fmt.Errorf("runtime has no active session")
		}
		return nil, value, nil
	default:
		return nil, nil, fmt.Errorf("unsupported slash target %T", target)
	}
}

func slashHelp() string {
	return `/login [provider key]  List auth status or save an API key
/logout <provider>    Remove stored provider credentials
/model [provider/id]  List or switch models
/scoped-models        List configured models
/settings             Show current runtime state
	/resume [session]     List sessions or switch to one
/new                  Start a new session
	/import <file>        Import a session JSONL into a new local session
/name <name>          Set session display name
/session              Show session statistics
/compact [prompt]     Compact current session with a local summary
/export [file]        Export session to HTML
/copy                 Copy last assistant message to clipboard
/share                Share session as a secret GitHub gist
/tree [entry-id]      Show session tree or navigate to an entry
/fork <entry-id>      Fork a new session from an entry
/clone                Clone current session branch
/changelog [file]     Show changelog entries
/reload               Reload session resources
/hotkeys, /help       Show commands
/quit                 Quit`
}

func parseLoginArgs(arg string) (provider string, key string, err error) {
	parts := strings.Fields(arg)
	if len(parts) == 0 {
		return "", "", nil
	}
	provider = parts[0]
	switch {
	case len(parts) == 2 && parts[1] == "--oauth":
		return provider, "", nil
	case len(parts) == 2 && strings.HasPrefix(parts[1], "--api-key="):
		return provider, strings.TrimPrefix(parts[1], "--api-key="), nil
	case len(parts) >= 3 && parts[1] == "--api-key":
		return provider, parts[2], nil
	case len(parts) >= 2:
		return provider, parts[1], nil
	default:
		return provider, "", nil
	}
}

func printAuthOverview(w io.Writer, agent *AgentSession) {
	providers := map[string]bool{}
	for _, model := range agent.Registry.List("") {
		providers[model.Provider] = true
	}
	for _, provider := range ai.GetOAuthProviders() {
		providers[string(provider.ID())] = true
	}
	names := make([]string, 0, len(providers))
	for provider := range providers {
		names = append(names, provider)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "Provider authentication:")
	for _, provider := range names {
		status := agent.Registry.Auth.AuthStatus(provider)
		fmt.Fprintf(w, "%s  %s\n", provider, formatAuthStatus(status))
	}
	fmt.Fprintln(w, "Usage: /login <provider> <api-key>")
}

func formatAuthStatus(status ai.AuthStatus) string {
	switch status.Source {
	case "stored":
		if status.Type != "" {
			return "stored " + status.Type
		}
		return "stored"
	case "runtime":
		return firstNonEmpty(status.Label, "runtime")
	case "environment":
		return "environment " + firstNonEmpty(status.Label, "variable")
	default:
		return "unconfigured"
	}
}

func mustJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func isInputTTY() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

// ChangelogForDisplay returns the changelog text to show once on startup after a
// version change, recording the current version so it is not shown again. It
// mirrors getChangelogForDisplay() in interactive-mode.ts:
//   - resumed/continued sessions (sessionHasMessages) never see the changelog;
//   - on a fresh install (no recorded version) it records the version and shows
//     nothing;
//   - otherwise it shows the entries newer than the recorded version (if any)
//     and records the current version.
//
// The numeric upstream version (ai.UpstreamVersion, e.g. "0.78.0") is used for
// comparison and recording because CHANGELOG.md headers are numeric semver, not
// the "-go" suffixed build string. When this returns non-empty the caller must
// persist settings (SaveGlobal) so the recorded version survives restart.
func ChangelogForDisplay(settings *SettingsManager, sessionHasMessages bool, changelogPath string) string {
	if settings == nil || sessionHasMessages {
		return ""
	}
	version := ai.UpstreamVersion
	lastVersion := settings.LastChangelogVersion()
	entries := cautils.ParseChangelog(changelogPath)
	if lastVersion == "" {
		// Fresh install: record the version, do not show the changelog.
		settings.SetLastChangelogVersion(version)
		return ""
	}
	newEntries := cautils.GetNewChangelogEntries(entries, lastVersion)
	if len(newEntries) == 0 {
		return ""
	}
	settings.SetLastChangelogVersion(version)
	return cautils.FormatChangelogMarkdown(newEntries)
}
