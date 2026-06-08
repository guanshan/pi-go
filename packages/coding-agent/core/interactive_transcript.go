package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	agentcore "github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
	"github.com/guanshan/pi-go/packages/tui"
)

func (m *interactiveModel) updateToolResult(event ai.Event, key string) {
	result, ok := interactiveToolResult(event[key])
	if !ok {
		return
	}
	text := strings.TrimSpace(ai.MessageText(ai.ToolResultMessage{Content: result.Content}))
	id := strings.TrimSpace(fmt.Sprint(event["toolCallId"]))
	name := strings.TrimSpace(fmt.Sprint(event["toolName"]))
	if name == "" || name == "<nil>" {
		name = "tool"
	}
	isPartial := key == "partialResult"
	isError := result.IsError
	if eventError, ok := event["isError"].(bool); ok {
		isError = eventError
	}
	if slot, ok := m.toolSlots[id]; ok && slot >= 0 && slot < len(m.messages) {
		msg := &m.messages[slot]
		msg.Role = interactiveRoleTool
		msg.Kind = interactiveMessageTool
		msg.ToolName = name
		msg.ToolCallID = id
		if args := interactiveRawMessage(event["args"]); len(args) > 0 {
			msg.ToolArgs = args
		}
		msg.ToolIsError = isError
		msg.ToolPartial = isPartial
		msg.Text = text
		return
	}
	if text == "" && !isError {
		// No existing slot to update and nothing to show: appending here would add
		// a spurious empty "[name]" entry. This happens when a tool has an
		// empty/unset toolCallId (its start placeholder was never registered as a
		// slot, so the lookup above misses). Updating an existing slot to empty is
		// still allowed above — that intentionally clears a finalized empty tool.
		return
	}
	m.appendMessageEntry(interactiveMessage{
		Role:        interactiveRoleTool,
		Text:        text,
		Kind:        interactiveMessageTool,
		ToolName:    name,
		ToolCallID:  id,
		ToolArgs:    interactiveRawMessage(event["args"]),
		ToolIsError: isError,
		ToolPartial: isPartial,
	})
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

func (m *interactiveModel) appendMessage(role interactiveRole, text string) int {
	return m.appendMessageEntry(interactiveMessage{Role: role, Text: text})
}

func (m *interactiveModel) appendBashMessage(result interactiveBashResult) int {
	return m.appendMessageEntry(interactiveMessage{
		Role:               interactiveRoleTool,
		Text:               result.Output,
		Kind:               interactiveMessageBash,
		BashCommand:        result.Command,
		BashExitCode:       result.ExitCode,
		BashCancelled:      result.Cancelled,
		BashTruncated:      result.Truncated,
		BashFullOutputPath: result.FullOutputPath,
		BashExclude:        result.Exclude,
		ToolIsError:        result.IsError,
	})
}

func (m *interactiveModel) appendMessageEntry(msg interactiveMessage) int {
	msg.Text = strings.TrimRight(msg.Text, "\n")
	if msg.Text == "" && msg.Kind == interactiveMessagePlain {
		return -1
	}
	m.messages = append(m.messages, msg)
	return len(m.messages) - 1
}

func (m *interactiveModel) renderTranscript(width int) string {
	var rendered []string
	for _, msg := range m.messages {
		prefix, style := m.interactiveMessagePrefix(msg.Role)
		text := strings.TrimRight(msg.Text, "\n")
		if text == "" && msg.Kind == interactiveMessagePlain {
			continue
		}
		lines := m.renderInteractiveMessageLines(msg, max(1, width-tui.VisibleWidth(prefix)))
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

func (m *interactiveModel) renderInteractiveMessageLines(msg interactiveMessage, width int) []string {
	text := strings.TrimRight(msg.Text, "\n")
	switch msg.Kind {
	case interactiveMessageTool:
		return m.renderToolMessageLines(msg, width)
	case interactiveMessageBash:
		return m.renderBashMessageLines(msg, width)
	case interactiveMessageCustom:
		return m.renderCustomMessageLines(msg, width)
	}
	switch msg.Role {
	case interactiveRoleUser, interactiveRoleAssistant:
		lines := tui.NewMarkdown(text, 0, 0, m.styles.Markdown).Render(width)
		if len(lines) > 0 {
			return lines
		}
	}
	return strings.Split(text, "\n")
}

func (m *interactiveModel) renderToolMessageLines(msg interactiveMessage, width int) []string {
	name := firstNonEmpty(strings.TrimSpace(msg.ToolName), "tool")
	status := "done"
	if msg.ToolPartial {
		status = "running"
	} else if msg.ToolIsError {
		status = "error"
	}
	lines := []string{m.styles.Tool.Render("["+name+"]") + " " + m.styles.ToolDiffContext.Render(status)}
	if m.toolsExpanded && len(msg.ToolArgs) > 0 {
		if args := prettyJSON(msg.ToolArgs); args != "" {
			lines = append(lines, m.styles.ToolDiffContext.Render("args:"))
			lines = append(lines, styleLines(strings.Split(args, "\n"), m.styles.ToolOutput)...)
		}
	}
	output := normalizeTranscriptOutput(msg.Text)
	body, hidden := m.previewTranscriptLines(output, m.toolsExpanded)
	if hidden > 0 && !m.toolsExpanded {
		lines = append(lines, m.styles.ToolDiffContext.Render(fmt.Sprintf("... %d more lines %s", hidden, m.expandHint("to expand"))))
	}
	for _, line := range body {
		lines = append(lines, m.styleToolOutputLine(line))
	}
	if hidden > 0 && m.toolsExpanded {
		lines = append(lines, m.styles.ToolDiffContext.Render(m.expandHint("to collapse")))
	}
	if len(lines) == 1 && msg.ToolPartial {
		lines = append(lines, m.styles.ToolDiffContext.Render("waiting for output..."))
	}
	return lines
}

func (m *interactiveModel) renderBashMessageLines(msg interactiveMessage, width int) []string {
	command := strings.TrimSpace(msg.BashCommand)
	if command == "" {
		command = "bash"
	}
	title := "$ " + command
	if msg.BashExclude {
		title = "!! " + command
	}
	lines := []string{m.styles.Tool.Render(title)}
	output := normalizeTranscriptOutput(msg.Text)
	body, hidden := m.previewTranscriptLines(output, m.toolsExpanded)
	if hidden > 0 && !m.toolsExpanded {
		lines = append(lines, m.styles.ToolDiffContext.Render(fmt.Sprintf("... %d more lines %s", hidden, m.expandHint("to expand"))))
	}
	for _, line := range body {
		lines = append(lines, m.styles.ToolOutput.Render(line))
	}
	var status []string
	if hidden > 0 && m.toolsExpanded {
		status = append(status, m.expandHint("to collapse"))
	}
	if msg.BashCancelled {
		status = append(status, "cancelled")
	} else if msg.BashExitCode != nil && *msg.BashExitCode != 0 {
		status = append(status, fmt.Sprintf("exit %d", *msg.BashExitCode))
	} else if msg.ToolIsError {
		status = append(status, "error")
	}
	if msg.BashTruncated && msg.BashFullOutputPath != "" {
		status = append(status, "Output truncated. Full output: "+msg.BashFullOutputPath)
	}
	if len(status) > 0 {
		lines = append(lines, m.styles.ToolDiffContext.Render("("+strings.Join(status, "; ")+")"))
	}
	return lines
}

// renderCustomMessageLines renders an extension pi.sendMessage custom entry. When
// a renderer produced lines (rendered once on receipt, ANSI preserved) they are
// shown with the shared expand/collapse preview; otherwise it falls back to the
// TS default: a bold [customType] label plus markdown of the content text.
func (m *interactiveModel) renderCustomMessageLines(msg interactiveMessage, width int) []string {
	if len(msg.CustomLines) > 0 {
		body, hidden := m.previewTranscriptLines(strings.Join(msg.CustomLines, "\n"), m.toolsExpanded)
		lines := append([]string(nil), body...)
		if hidden > 0 && !m.toolsExpanded {
			lines = append(lines, m.styles.ToolDiffContext.Render(fmt.Sprintf("... %d more lines %s", hidden, m.expandHint("to expand"))))
		} else if hidden > 0 && m.toolsExpanded {
			lines = append(lines, m.styles.ToolDiffContext.Render(m.expandHint("to collapse")))
		}
		return lines
	}
	label := firstNonEmpty(strings.TrimSpace(msg.CustomType), "custom")
	lines := []string{m.styles.Tool.Render("[" + label + "]")}
	if text := strings.TrimRight(msg.CustomText, "\n"); text != "" {
		lines = append(lines, tui.NewMarkdown(text, 0, 0, m.styles.Markdown).Render(width)...)
	}
	return lines
}

func (m *interactiveModel) previewTranscriptLines(text string, expanded bool) ([]string, int) {
	if text == "" {
		return nil, 0
	}
	lines := strings.Split(text, "\n")
	hidden := len(lines) - interactiveTranscriptPreviewLines
	if hidden < 0 {
		hidden = 0
	}
	if expanded || hidden == 0 {
		return lines, hidden
	}
	return lines[hidden:], hidden
}

func (m *interactiveModel) styleToolOutputLine(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	switch {
	case strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "+++"):
		return m.styles.ToolDiffAdded.Render(line)
	case strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "---"):
		return m.styles.ToolDiffRemoved.Render(line)
	case strings.HasPrefix(trimmed, "@@"):
		return m.styles.ToolDiffContext.Render(line)
	default:
		return m.styles.ToolOutput.Render(line)
	}
}

func (m *interactiveModel) expandHint(action string) string {
	key := "ctrl+o"
	if m.keybindings != nil {
		if display := strings.TrimSpace(m.keybindings.KeyDisplay(AppToolsExpand)); display != "" {
			key = display
		}
	}
	return "(" + key + " " + action + ")"
}

func styleLines(lines []string, style lipgloss.Style) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, style.Render(line))
	}
	return out
}

func normalizeTranscriptOutput(text string) string {
	return strings.TrimRight(catools.SanitizeBashOutput(text), "\n")
}

func prettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.TrimSpace(string(raw))
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(data)
}

func interactiveRawMessage(value any) json.RawMessage {
	switch raw := value.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return append(json.RawMessage(nil), raw...)
	case []byte:
		return append(json.RawMessage(nil), raw...)
	case string:
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		return json.RawMessage(raw)
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		return data
	}
}

func toolDetailBool(details any, key string) bool {
	switch values := details.(type) {
	case map[string]any:
		if value, ok := values[key].(bool); ok {
			return value
		}
	case map[string]string:
		return strings.EqualFold(values[key], "true")
	}
	return false
}

func toolDetailString(details any, key string) string {
	switch values := details.(type) {
	case map[string]any:
		if value, ok := values[key].(string); ok {
			return value
		}
	case map[string]string:
		return values[key]
	}
	return ""
}
