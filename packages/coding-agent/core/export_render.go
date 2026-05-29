package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

func renderExportSidebar(b *strings.Builder, sm *SessionManager, labels map[string]string, branchIDs map[string]bool) {
	b.WriteString("    <aside id=\"sidebar\">\n")
	b.WriteString("      <div class=\"sidebar-header\">\n")
	b.WriteString("        <input type=\"text\" class=\"sidebar-search\" id=\"tree-search\" placeholder=\"Search...\">\n")
	b.WriteString("        <div class=\"sidebar-filters\">\n")
	for i, filter := range []struct {
		id    string
		label string
	}{
		{"default", "Default"},
		{"no-tools", "No-tools"},
		{"user-only", "User"},
		{"labeled-only", "Labeled"},
		{"all", "All"},
	} {
		active := ""
		if i == 0 {
			active = " active"
		}
		fmt.Fprintf(b, "          <button class=\"filter-btn%s\" data-filter=\"%s\">%s</button>\n", active, filter.id, filter.label)
	}
	b.WriteString("        </div>\n")
	b.WriteString("      </div>\n")
	b.WriteString("      <nav class=\"tree-container\" id=\"tree-container\">\n")
	for i, entry := range sm.Entries {
		id := exportEntryID(entry, i)
		role := exportEntryRole(entry)
		label := labels[entry.ID]
		classes := []string{"tree-node"}
		if branchIDs[entry.ID] {
			classes = append(classes, "in-path")
		}
		if label != "" {
			classes = append(classes, "labeled")
		}
		fmt.Fprintf(
			b,
			"        <a class=\"%s\" href=\"#%s\" data-entry-type=\"%s\" data-role=\"%s\"><span class=\"tree-role tree-role-%s\">%s</span><span class=\"tree-content\">%s</span>",
			html.EscapeString(strings.Join(classes, " ")),
			html.EscapeString(id),
			html.EscapeString(entry.Type),
			html.EscapeString(role),
			html.EscapeString(role),
			html.EscapeString(role),
			html.EscapeString(exportEntryPreview(entry)),
		)
		if label != "" {
			fmt.Fprintf(b, "<span class=\"tree-label\">%s</span>", html.EscapeString(label))
		}
		b.WriteString("</a>\n")
	}
	b.WriteString("      </nav>\n")
	b.WriteString("      <div class=\"tree-status\" id=\"tree-status\"></div>\n")
	b.WriteString("    </aside>\n")
}

func renderExportHeader(b *strings.Builder, sm *SessionManager, stats exportStats) {
	b.WriteString("<header class=\"session-header\">")
	b.WriteString("<div>")
	fmt.Fprintf(b, "<div class=\"app-name\">%s session</div>", html.EscapeString(AppName))
	fmt.Fprintf(b, "<h1>%s</h1>", html.EscapeString(firstNonEmpty(sessionName(sm.Entries), shortExportID(sm.Header.ID))))
	fmt.Fprintf(b, "<div class=\"session-meta\"><span>ID %s</span><span>%s</span>", html.EscapeString(sm.Header.ID), html.EscapeString(sm.Header.CWD))
	if sm.Header.Timestamp != "" {
		fmt.Fprintf(b, "<span>%s</span>", html.EscapeString(formatExportTimestamp(sm.Header.Timestamp, 0)))
	}
	b.WriteString("</div>")
	b.WriteString("</div>")
	b.WriteString("<div class=\"stats-grid\">")
	renderExportStat(b, "User", stats.UserMessages)
	renderExportStat(b, "Assistant", stats.AssistantMessages)
	renderExportStat(b, "Tools", stats.ToolResults)
	renderExportStat(b, "Calls", stats.ToolCalls)
	if stats.Tokens.TotalTokens > 0 || stats.Tokens.Input > 0 || stats.Tokens.Output > 0 {
		renderExportStat(b, "Tokens", firstNonZero(stats.Tokens.TotalTokens, stats.Tokens.Input+stats.Tokens.Output+stats.Tokens.CacheRead+stats.Tokens.CacheWrite))
	}
	if stats.Tokens.Cost.Total > 0 {
		fmt.Fprintf(b, "<div class=\"stat\"><span>Cost</span><strong>$%.4f</strong></div>", stats.Tokens.Cost.Total)
	}
	b.WriteString("</div>")
	if len(stats.Models) > 0 {
		b.WriteString("<div class=\"model-list\">")
		for _, model := range stats.Models {
			fmt.Fprintf(b, "<span>%s</span>", html.EscapeString(model))
		}
		b.WriteString("</div>")
	}
	b.WriteString("</header>")
}

func renderExportStat(b *strings.Builder, label string, value int) {
	fmt.Fprintf(b, "<div class=\"stat\"><span>%s</span><strong>%d</strong></div>", html.EscapeString(label), value)
}

func renderExportEntry(b *strings.Builder, entry SessionEntry, index int, labels map[string]string, toolResults map[string]exportToolResult) {
	id := exportEntryID(entry, index)
	label := labels[entry.ID]
	if entry.Type == "message" && entry.Message != nil {
		msg := entry.Message
		role := ai.MessageRole(msg)
		if role == "toolResult" && toolResultRenderedWithCall(entry, toolResults) {
			return
		}
		switch role {
		case "user":
			renderUserMessage(b, id, entry, msg, label)
		case "assistant":
			renderAssistantMessage(b, id, entry, msg, label, toolResults)
		case "bashExecution":
			renderBashExecution(b, id, entry, msg, label)
		case "toolResult":
			renderStandaloneToolResult(b, id, entry, msg, label)
		default:
			renderGenericMessage(b, id, entry, msg, label)
		}
		return
	}

	switch entry.Type {
	case "model_change":
		renderInfoEntry(b, id, entry, "model-change", "model", fmt.Sprintf("%s/%s", entry.Provider, entry.ModelID), label)
	case "thinking_level_change":
		renderInfoEntry(b, id, entry, "model-change", "thinking", string(entry.ThinkingLevel), label)
	case "session_info":
		renderInfoEntry(b, id, entry, "model-change", "session", entry.Name, label)
	case "compaction":
		renderCompactionEntry(b, id, entry, label)
	case "branch_summary":
		renderMarkdownInfoEntry(b, id, entry, "branch-summary", "Branch Summary", entry.Summary, label)
	case "custom_message":
		if entry.Display {
			renderMarkdownInfoEntry(b, id, entry, "hook-message", "["+entry.CustomType+"]", exportAnyString(entry.Content), label)
		}
	case "label":
		renderInfoEntry(b, id, entry, "model-change", "label", fmt.Sprintf("%s: %s", entry.TargetID, entry.Label), label)
	default:
		renderInfoEntry(b, id, entry, "model-change", entry.Type, exportAnyString(entry), label)
	}
}

func renderUserMessage(b *strings.Builder, id string, entry SessionEntry, msg ai.Message, label string) {
	fmt.Fprintf(b, "        <div class=\"user-message\" id=\"%s\">", html.EscapeString(id))
	renderEntryChrome(b, entry, label)
	renderMessageImages(b, ai.MessageBlocks(msg), "message")
	text := ai.MessageText(msg)
	if strings.TrimSpace(text) != "" {
		fmt.Fprintf(b, "<div class=\"markdown-content\">%s</div>", renderExportMarkdown(text))
	}
	b.WriteString("</div>\n")
}

func renderAssistantMessage(b *strings.Builder, id string, entry SessionEntry, msg ai.Message, label string, toolResults map[string]exportToolResult) {
	fmt.Fprintf(b, "        <div class=\"assistant-message\" id=\"%s\">", html.EscapeString(id))
	renderEntryChrome(b, entry, label)
	blocks := ai.MessageBlocks(msg)
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				fmt.Fprintf(b, "<div class=\"assistant-text markdown-content\">%s</div>", renderExportMarkdown(block.Text))
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				fmt.Fprintf(b, "<details class=\"thinking-block\"><summary>Thinking ...</summary><div class=\"thinking-text\">%s</div></details>", html.EscapeString(block.Thinking))
			}
		}
	}
	for _, block := range blocks {
		if block.Type == "toolCall" {
			renderToolCall(b, block, toolResults)
		}
	}
	assistant, _ := ai.AsAssistantMessage(msg)
	switch assistant.StopReason {
	case "aborted":
		b.WriteString("<div class=\"error-text\">Aborted</div>")
	case "error":
		fmt.Fprintf(b, "<div class=\"error-text\">Error: %s</div>", html.EscapeString(firstNonEmpty(assistant.ErrorMessage, "Unknown error")))
	}
	if assistant.Provider != "" || assistant.Model != "" || assistant.StopReason != "" {
		fmt.Fprintf(b, "<div class=\"message-footnote\">%s %s %s</div>", html.EscapeString(assistant.Provider), html.EscapeString(assistant.Model), html.EscapeString(assistant.StopReason))
	}
	b.WriteString("</div>\n")
}

func renderBashExecution(b *strings.Builder, id string, entry SessionEntry, msg ai.Message, label string) {
	custom, _ := ai.AsCustomMessage(msg)
	status := "success"
	if custom.Cancelled || (custom.ExitCode != nil && *custom.ExitCode != 0) {
		status = "error"
	}
	fmt.Fprintf(b, "        <div class=\"tool-execution %s\" id=\"%s\">", status, html.EscapeString(id))
	renderEntryChrome(b, entry, label)
	fmt.Fprintf(b, "<div class=\"tool-command\">$ %s</div>", html.EscapeString(custom.Command))
	if custom.Output != "" {
		fmt.Fprintf(b, "<pre class=\"tool-output\">%s</pre>", html.EscapeString(custom.Output))
	}
	if custom.Cancelled {
		b.WriteString("<div class=\"error-text\">cancelled</div>")
	} else if custom.ExitCode != nil && *custom.ExitCode != 0 {
		fmt.Fprintf(b, "<div class=\"error-text\">exit %d</div>", *custom.ExitCode)
	}
	if custom.Truncated {
		b.WriteString("<div class=\"message-footnote\">output truncated</div>")
	}
	b.WriteString("</div>\n")
}

func renderStandaloneToolResult(b *strings.Builder, id string, entry SessionEntry, msg ai.Message, label string) {
	toolResult, _ := ai.AsToolResultMessage(msg)
	status := "success"
	if toolResult.IsError {
		status = "error"
	}
	fmt.Fprintf(b, "        <div class=\"tool-execution %s\" id=\"%s\">", status, html.EscapeString(id))
	renderEntryChrome(b, entry, label)
	fmt.Fprintf(b, "<div class=\"tool-header\"><span class=\"tool-name\">%s</span> <span class=\"tool-path\">%s</span></div>", html.EscapeString(firstNonEmpty(toolResult.ToolName, "tool")), html.EscapeString(toolResult.ToolCallID))
	renderMessageImages(b, ai.MessageBlocks(msg), "tool")
	text := ai.MessageText(msg)
	if strings.TrimSpace(text) != "" {
		fmt.Fprintf(b, "<pre class=\"tool-output\">%s</pre>", html.EscapeString(text))
	}
	renderDetails(b, toolResult.Details)
	b.WriteString("</div>\n")
}

func renderGenericMessage(b *strings.Builder, id string, entry SessionEntry, msg ai.Message, label string) {
	fmt.Fprintf(b, "        <div class=\"assistant-message generic-message\" id=\"%s\">", html.EscapeString(id))
	renderEntryChrome(b, entry, label)
	fmt.Fprintf(b, "<div class=\"message-role\">%s</div>", html.EscapeString(ai.MessageRole(msg)))
	text := ai.MessageText(msg)
	if strings.TrimSpace(text) != "" {
		fmt.Fprintf(b, "<div class=\"markdown-content\">%s</div>", renderExportMarkdown(text))
	} else {
		if custom, ok := ai.AsCustomMessage(msg); ok {
			fmt.Fprintf(b, "<pre>%s</pre>", html.EscapeString(exportAnyString(custom.Content)))
		}
	}
	b.WriteString("</div>\n")
}

func renderToolCall(b *strings.Builder, block ai.ContentBlock, toolResults map[string]exportToolResult) {
	result, ok := toolResults[block.ID]
	status := "pending"
	if ok {
		status = "success"
		if ai.MessageIsError(result.Msg) {
			status = "error"
		}
	}
	fmt.Fprintf(b, "<div class=\"tool-execution %s\" id=\"tool-call-%s\">", status, html.EscapeString(block.ID))
	fmt.Fprintf(b, "<div class=\"tool-header\"><span class=\"tool-name\">%s</span>", html.EscapeString(firstNonEmpty(block.Name, "tool")))
	args := formatToolArguments(block.Arguments)
	if args != "" {
		fmt.Fprintf(b, "<span class=\"tool-path\">%s</span>", html.EscapeString(args))
	}
	b.WriteString("</div>")
	if ok {
		renderMessageImages(b, ai.MessageBlocks(result.Msg), "tool")
		text := ai.MessageText(result.Msg)
		if strings.TrimSpace(text) != "" {
			fmt.Fprintf(b, "<pre class=\"tool-output\">%s</pre>", html.EscapeString(text))
		}
		if toolResult, ok := ai.AsToolResultMessage(result.Msg); ok {
			renderDetails(b, toolResult.Details)
		}
		if ai.MessageIsError(result.Msg) {
			b.WriteString("<div class=\"error-text\">tool returned an error</div>")
		}
	} else {
		b.WriteString("<div class=\"message-footnote\">pending</div>")
	}
	b.WriteString("</div>")
}

func renderEntryChrome(b *strings.Builder, entry SessionEntry, label string) {
	if entry.ID != "" {
		fmt.Fprintf(b, `<button class="copy-link-btn" data-entry-id="%s" title="Copy link to this message">`, html.EscapeString(entry.ID))
		b.WriteString(`<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>`)
		b.WriteString("</button>")
	}
	if ts := formatExportTimestamp(entry.Timestamp, 0); ts != "" {
		fmt.Fprintf(b, "<div class=\"message-timestamp\">%s</div>", html.EscapeString(ts))
	}
	if label != "" {
		fmt.Fprintf(b, "<div class=\"entry-label\">%s</div>", html.EscapeString(label))
	}
}

func renderMessageImages(b *strings.Builder, blocks []ai.ContentBlock, classPrefix string) {
	var images []ai.ContentBlock
	for _, block := range blocks {
		if block.Type == "image" && block.Data != "" {
			images = append(images, block)
		}
	}
	if len(images) == 0 {
		return
	}
	if classPrefix == "tool" {
		b.WriteString("<div class=\"tool-images\">")
	} else {
		b.WriteString("<div class=\"message-images\">")
	}
	for _, img := range images {
		mimeType := firstNonEmpty(img.MimeType, "image/png")
		className := "message-image"
		if classPrefix == "tool" {
			className = "tool-image"
		}
		fmt.Fprintf(b, `<img src="data:%s;base64,%s" class="%s" alt="">`, html.EscapeString(mimeType), html.EscapeString(img.Data), className)
	}
	b.WriteString("</div>")
}

func renderInfoEntry(b *strings.Builder, id string, entry SessionEntry, className, label, value string, entryLabel string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "        <div class=\"%s\" id=\"%s\">", html.EscapeString(className), html.EscapeString(id))
	renderEntryChrome(b, entry, entryLabel)
	fmt.Fprintf(b, "<span class=\"info-label\">%s</span> %s", html.EscapeString(label), html.EscapeString(value))
	b.WriteString("</div>\n")
}

func renderMarkdownInfoEntry(b *strings.Builder, id string, entry SessionEntry, className, title, content string, entryLabel string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	fmt.Fprintf(b, "        <div class=\"%s\" id=\"%s\">", html.EscapeString(className), html.EscapeString(id))
	renderEntryChrome(b, entry, entryLabel)
	fmt.Fprintf(b, "<div class=\"branch-summary-header\">%s</div><div class=\"markdown-content\">%s</div>", html.EscapeString(title), renderExportMarkdown(content))
	b.WriteString("</div>\n")
}

func renderCompactionEntry(b *strings.Builder, id string, entry SessionEntry, label string) {
	fmt.Fprintf(b, "        <details class=\"compaction\" id=\"%s\">", html.EscapeString(id))
	renderEntryChrome(b, entry, label)
	fmt.Fprintf(b, "<summary><span class=\"compaction-label\">[compaction]</span> Compacted from %d tokens</summary>", entry.TokensBefore)
	fmt.Fprintf(b, "<pre class=\"compaction-content\">%s</pre>", html.EscapeString(entry.Summary))
	b.WriteString("</details>\n")
}

func renderDetails(b *strings.Builder, details any) {
	if details == nil {
		return
	}
	text := exportAnyString(details)
	if strings.TrimSpace(text) == "" || text == "null" {
		return
	}
	fmt.Fprintf(b, "<details class=\"tool-details\"><summary>Details</summary><pre>%s</pre></details>", html.EscapeString(text))
}

func renderExportMarkdown(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var b strings.Builder
	inCode := false
	var paragraph []string
	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(strings.Join(paragraph, "\n")))
		paragraph = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				b.WriteString("</code></pre>")
				inCode = false
			} else {
				flushParagraph()
				lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				fmt.Fprintf(&b, "<pre class=\"code-block\"><code data-lang=\"%s\">", html.EscapeString(lang))
				inCode = true
			}
			continue
		}
		if inCode {
			b.WriteString(html.EscapeString(line))
			b.WriteByte('\n')
			continue
		}
		if trimmed == "" {
			flushParagraph()
			continue
		}
		if level, ok := markdownHeading(trimmed); ok {
			flushParagraph()
			text := strings.TrimSpace(trimmed[level:])
			fmt.Fprintf(&b, "<h%d>%s</h%d>", level, html.EscapeString(text), level)
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			flushParagraph()
			fmt.Fprintf(&b, "<blockquote>%s</blockquote>", html.EscapeString(strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))))
			continue
		}
		paragraph = append(paragraph, line)
	}
	if inCode {
		b.WriteString("</code></pre>")
	}
	flushParagraph()
	return b.String()
}

func markdownHeading(line string) (int, bool) {
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	return level, level > 0 && level < len(line) && line[level] == ' '
}

func toolResultRenderedWithCall(entry SessionEntry, results map[string]exportToolResult) bool {
	if entry.Message == nil || ai.MessageToolCallID(entry.Message) == "" {
		return false
	}
	result, ok := results[ai.MessageToolCallID(entry.Message)]
	return ok && result.Entry.ID == entry.ID
}

func exportEntryID(entry SessionEntry, index int) string {
	if entry.ID != "" {
		return "entry-" + entry.ID
	}
	return fmt.Sprintf("entry-%d", index+1)
}

func exportEntryRole(entry SessionEntry) string {
	if entry.Type == "message" && entry.Message != nil {
		if role := ai.MessageRole(entry.Message); role != "" {
			return role
		}
	}
	switch entry.Type {
	case "model_change":
		return "model"
	case "thinking_level_change":
		return "thinking"
	case "session_info":
		return "session"
	case "custom_message":
		return "custom"
	default:
		return entry.Type
	}
}

func exportEntryPreview(entry SessionEntry) string {
	if entry.Type == "message" && entry.Message != nil {
		msg := entry.Message
		switch ai.MessageRole(msg) {
		case "toolResult":
			return firstNonEmpty(ai.MessageToolName(msg), ai.MessageToolCallID(msg), "tool result")
		case "bashExecution":
			custom, _ := ai.AsCustomMessage(msg)
			return oneLine(firstNonEmpty(custom.Command, custom.Output, "bash"))
		case "assistant":
			text := ai.MessageText(msg)
			if strings.TrimSpace(text) != "" {
				return oneLine(text)
			}
			for _, block := range ai.MessageBlocks(msg) {
				if block.Type == "toolCall" {
					return "tool call: " + firstNonEmpty(block.Name, block.ID)
				}
			}
			return "assistant"
		default:
			return oneLine(firstNonEmpty(ai.MessageText(msg), ai.MessageRole(msg)))
		}
	}
	switch entry.Type {
	case "model_change":
		return entry.Provider + "/" + entry.ModelID
	case "thinking_level_change":
		return string(entry.ThinkingLevel)
	case "session_info":
		return entry.Name
	case "compaction":
		return fmt.Sprintf("%d tokens", entry.TokensBefore)
	case "branch_summary":
		return oneLine(entry.Summary)
	case "custom_message":
		return oneLine(exportAnyString(entry.Content))
	case "label":
		return firstNonEmpty(entry.Label, entry.TargetID)
	default:
		return entry.Type
	}
}

func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 96 {
		return text[:93] + "..."
	}
	return text
}

func sessionName(entries []SessionEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "session_info" && entries[i].Name != "" {
			return entries[i].Name
		}
	}
	return ""
}

func formatToolArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err == nil {
		for _, key := range []string{"command", "file_path", "path"} {
			if value, ok := args[key].(string); ok && value != "" {
				if key == "command" {
					return "$ " + value
				}
				return value
			}
		}
	}
	text := exportAnyString(raw)
	if len(text) > 120 {
		return text[:117] + "..."
	}
	return text
}

func exportAnyString(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case json.RawMessage:
		if len(v) == 0 {
			return ""
		}
		var pretty bytes.Buffer
		if json.Indent(&pretty, v, "", "  ") == nil {
			return pretty.String()
		}
		return string(v)
	default:
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(raw)
	}
}

func formatExportTimestamp(text string, unixMilli int64) string {
	if text != "" {
		if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return t.Local().Format("2006-01-02 15:04:05")
		}
		return text
	}
	if unixMilli > 0 {
		return time.UnixMilli(unixMilli).Local().Format("2006-01-02 15:04:05")
	}
	return ""
}

func shortExportID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func exportHTMLCSS() string {
	return `
:root {
  --body-bg: #17191f;
  --container-bg: #20232b;
  --panel-bg: #252a33;
  --text: #eceff4;
  --muted: #a5adbb;
  --dim: #3a404d;
  --accent: #8bd5ca;
  --success: #a6da95;
  --warning: #eed49f;
  --error: #ed8796;
  --user-bg: #243447;
  --assistant-bg: #20252e;
  --code-bg: #111319;
}
* { box-sizing: border-box; }
html { scroll-behavior: smooth; }
body {
  margin: 0;
  background: var(--body-bg);
  color: var(--text);
  font: 13px/1.55 ui-monospace, "Cascadia Code", "Source Code Pro", Menlo, Consolas, monospace;
}
a { color: inherit; }
#app { display: flex; min-height: 100vh; }
#sidebar {
  position: sticky;
  top: 0;
  width: 360px;
  min-width: 260px;
  max-width: 42vw;
  height: 100vh;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  background: var(--container-bg);
  border-right: 1px solid var(--dim);
}
.sidebar-header { padding: 14px; border-bottom: 1px solid var(--dim); }
.sidebar-search {
  width: 100%;
  padding: 7px 9px;
  color: var(--text);
  background: var(--body-bg);
  border: 1px solid var(--dim);
  border-radius: 4px;
  font: inherit;
}
.sidebar-search:focus { outline: 1px solid var(--accent); }
.sidebar-filters { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
.filter-btn {
  color: var(--muted);
  background: transparent;
  border: 1px solid var(--dim);
  border-radius: 4px;
  padding: 3px 7px;
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}
.filter-btn.active { color: var(--body-bg); background: var(--accent); border-color: var(--accent); }
.tree-container { flex: 1; overflow: auto; padding: 8px 0; }
.tree-node {
  display: grid;
  grid-template-columns: 86px 1fr auto;
  gap: 8px;
  align-items: baseline;
  padding: 4px 14px;
  text-decoration: none;
  color: var(--muted);
  border-left: 3px solid transparent;
}
.tree-node:hover, .tree-node.active { background: rgba(139, 213, 202, .08); color: var(--text); }
.tree-node.in-path { border-left-color: var(--accent); }
.tree-role { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tree-role-user { color: var(--accent); }
.tree-role-assistant { color: var(--success); }
.tree-role-toolResult, .tree-role-bashExecution { color: var(--warning); }
.tree-content { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tree-label, .entry-label {
  color: var(--body-bg);
  background: var(--warning);
  border-radius: 3px;
  padding: 0 5px;
  font-size: 11px;
}
.tree-status { padding: 8px 14px; color: var(--muted); border-top: 1px solid var(--dim); }
#content { width: min(100%, 1040px); padding: 24px; margin: 0 auto; }
.session-header {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 18px;
  padding: 18px;
  margin-bottom: 18px;
  background: var(--container-bg);
  border: 1px solid var(--dim);
  border-radius: 8px;
}
.app-name { color: var(--accent); text-transform: uppercase; letter-spacing: .08em; font-size: 11px; }
h1 { margin: 2px 0 8px; font-size: 24px; line-height: 1.2; }
.session-meta { display: flex; flex-wrap: wrap; gap: 10px; color: var(--muted); }
.stats-grid { display: grid; grid-template-columns: repeat(4, minmax(76px, 1fr)); gap: 8px; }
.stat { padding: 7px 9px; background: var(--panel-bg); border: 1px solid var(--dim); border-radius: 6px; }
.stat span { display: block; color: var(--muted); font-size: 11px; }
.stat strong { font-size: 16px; }
.model-list { grid-column: 1 / -1; display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
.model-list span { border: 1px solid var(--dim); border-radius: 4px; padding: 2px 6px; color: var(--muted); }
#messages { display: flex; flex-direction: column; gap: 14px; }
.user-message, .assistant-message, .tool-execution, .branch-summary, .hook-message, .model-change, .compaction {
  position: relative;
  padding: 16px;
  border: 1px solid var(--dim);
  border-radius: 8px;
  background: var(--assistant-bg);
}
.user-message { background: var(--user-bg); margin-left: min(12vw, 120px); }
.assistant-message { margin-right: min(8vw, 80px); }
.tool-execution { background: #1c2028; border-left: 3px solid var(--warning); }
.tool-execution.success { border-left-color: var(--success); }
.tool-execution.error { border-left-color: var(--error); }
.model-change { color: var(--muted); padding: 8px 12px; background: transparent; }
.copy-link-btn {
  position: absolute;
  top: 9px;
  right: 9px;
  display: inline-grid;
  place-items: center;
  width: 26px;
  height: 26px;
  color: var(--muted);
  background: transparent;
  border: 1px solid transparent;
  border-radius: 4px;
  cursor: pointer;
}
.copy-link-btn:hover, .copy-link-btn.copied { color: var(--accent); border-color: var(--dim); background: var(--panel-bg); }
.message-timestamp, .message-footnote { color: var(--muted); font-size: 11px; margin-bottom: 8px; }
.message-role, .info-label, .tool-name, .branch-summary-header { color: var(--accent); font-weight: 700; }
.tool-header { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 8px; }
.tool-path { color: var(--muted); }
.tool-command { color: var(--warning); margin-bottom: 8px; white-space: pre-wrap; }
.markdown-content { white-space: normal; overflow-wrap: anywhere; }
.markdown-content p { margin: 0 0 10px; white-space: pre-wrap; }
.markdown-content p:last-child { margin-bottom: 0; }
.markdown-content h1, .markdown-content h2, .markdown-content h3, .markdown-content h4, .markdown-content h5, .markdown-content h6 { margin: 10px 0 6px; }
blockquote { margin: 8px 0; padding-left: 10px; color: var(--muted); border-left: 3px solid var(--dim); }
pre, .tool-output, .code-block {
  margin: 8px 0 0;
  padding: 10px;
  overflow: auto;
  white-space: pre-wrap;
  color: var(--text);
  background: var(--code-bg);
  border: 1px solid var(--dim);
  border-radius: 6px;
}
.thinking-block { margin: 8px 0; color: var(--muted); }
.thinking-block summary { cursor: pointer; color: var(--warning); }
.thinking-text { margin-top: 8px; white-space: pre-wrap; }
.message-images, .tool-images { display: flex; flex-wrap: wrap; gap: 10px; margin: 8px 0; }
.message-image, .tool-image {
  max-width: min(100%, 420px);
  max-height: 420px;
  border-radius: 6px;
  border: 1px solid var(--dim);
  cursor: zoom-in;
}
.error-text { color: var(--error); margin-top: 8px; }
.tool-details summary { color: var(--muted); cursor: pointer; margin-top: 8px; }
.image-modal {
  position: fixed;
  inset: 0;
  display: none;
  align-items: center;
  justify-content: center;
  padding: 28px;
  background: rgba(0, 0, 0, .78);
}
.image-modal.open { display: flex; }
.image-modal img { max-width: 96vw; max-height: 92vh; border-radius: 8px; }
@media (max-width: 820px) {
  #app { display: block; }
  #sidebar { position: relative; width: 100%; max-width: none; height: auto; max-height: 42vh; }
  #content { padding: 14px; }
  .session-header { grid-template-columns: 1fr; }
  .stats-grid { grid-template-columns: repeat(2, 1fr); }
  .user-message, .assistant-message { margin-left: 0; margin-right: 0; }
}
`
}

func exportHTMLJS() string {
	return `
(function () {
  'use strict';
  const dataEl = document.getElementById('session-data');
  if (dataEl) {
    try {
      const binary = atob(dataEl.textContent.trim());
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
      window.__PI_SESSION_DATA__ = JSON.parse(new TextDecoder('utf-8').decode(bytes));
    } catch (err) {
      console.error('Failed to decode session data', err);
    }
  }

  const nodes = Array.from(document.querySelectorAll('.tree-node'));
  const status = document.getElementById('tree-status');
  let activeFilter = 'default';

  function matchesFilter(node) {
    const role = node.dataset.role || '';
    const type = node.dataset.entryType || '';
    if (activeFilter === 'all') return true;
    if (activeFilter === 'user-only') return role === 'user';
    if (activeFilter === 'labeled-only') return node.classList.contains('labeled');
    if (activeFilter === 'no-tools') return role !== 'toolResult' && role !== 'bashExecution';
    return type !== 'label';
  }

  function applyTreeFilter() {
    const q = (document.getElementById('tree-search')?.value || '').toLowerCase();
    let visible = 0;
    for (const node of nodes) {
      const ok = matchesFilter(node) && node.textContent.toLowerCase().includes(q);
      node.style.display = ok ? '' : 'none';
      if (ok) visible++;
    }
    if (status) status.textContent = visible + ' entries';
  }

  document.getElementById('tree-search')?.addEventListener('input', applyTreeFilter);
  document.querySelectorAll('.filter-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      activeFilter = btn.dataset.filter || 'default';
      document.querySelectorAll('.filter-btn').forEach((b) => b.classList.toggle('active', b === btn));
      applyTreeFilter();
    });
  });

  function copyText(text, button) {
    const done = () => {
      if (!button) return;
      button.classList.add('copied');
      setTimeout(() => button.classList.remove('copied'), 1200);
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done).catch(() => {});
      return;
    }
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    try { document.execCommand('copy'); done(); } finally { textarea.remove(); }
  }

  document.querySelectorAll('.copy-link-btn').forEach((btn) => {
    btn.addEventListener('click', (event) => {
      event.preventDefault();
      event.stopPropagation();
      const id = btn.dataset.entryId;
      const url = new URL(window.location.href);
      url.hash = 'entry-' + id;
      copyText(url.toString(), btn);
    });
  });

  const modal = document.getElementById('image-modal');
  const modalImage = document.getElementById('modal-image');
  document.querySelectorAll('.message-image,.tool-image').forEach((img) => {
    img.addEventListener('click', () => {
      if (!modal || !modalImage) return;
      modalImage.src = img.src;
      modal.classList.add('open');
    });
  });
  modal?.addEventListener('click', () => modal.classList.remove('open'));

  const byId = new Map(nodes.map((node) => [node.getAttribute('href'), node]));
  function markActive() {
    nodes.forEach((node) => node.classList.toggle('active', node.getAttribute('href') === window.location.hash));
  }
  window.addEventListener('hashchange', markActive);
  if (window.location.hash && byId.has(window.location.hash)) markActive();
  applyTreeFilter();
})();
`
}
