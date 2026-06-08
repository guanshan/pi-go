package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
)

type exportSessionData struct {
	Header  SessionHeader  `json:"header"`
	Entries []SessionEntry `json:"entries"`
	LeafID  *string        `json:"leafId"`
	Stats   exportStats    `json:"stats"`
	Version string         `json:"version"`
}

type exportStats struct {
	UserMessages      int      `json:"userMessages"`
	AssistantMessages int      `json:"assistantMessages"`
	ToolResults       int      `json:"toolResults"`
	ToolCalls         int      `json:"toolCalls"`
	CustomMessages    int      `json:"customMessages"`
	Compactions       int      `json:"compactions"`
	BranchSummaries   int      `json:"branchSummaries"`
	Tokens            ai.Usage `json:"tokens"`
	Models            []string `json:"models"`
}

type exportToolResult struct {
	Entry SessionEntry
	Msg   ai.Message
}

func ExportSessionToHTML(inputPath, outputPath string) (string, error) {
	// Export is read-only: load + migrate in memory without rewriting the file.
	sm, err := openSessionNoRewrite(inputPath, "")
	if err != nil {
		return "", err
	}
	if outputPath == "" {
		outputPath = defaultExportPath(inputPath)
	}
	body, err := generateSessionHTML(sm)
	if err != nil {
		return "", err
	}
	if dir := filepath.Dir(outputPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(outputPath, []byte(body), 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
}

func defaultExportPath(inputPath string) string {
	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	if base == "" {
		base = "session"
	}
	return fmt.Sprintf("%s-session-%s.html", AppName, base)
}

func generateSessionHTML(sm *SessionManager) (string, error) {
	header, entries, leaf := sm.Snapshot()
	snapshot := &SessionManager{Header: header, Entries: entries, CurrentID: leaf}
	data := exportSessionData{
		Header:  header,
		Entries: entries,
		LeafID:  leaf,
		Stats:   computeExportStats(entries),
		Version: Version,
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(raw)

	labelByTarget := collectExportLabels(entries)
	toolResults := collectExportToolResults(entries)
	branchIDs := map[string]bool{}
	if leaf != nil {
		if branch, err := branchFromResolvedEntries(entries, *leaf); err == nil {
			for _, entry := range branch {
				if entry.ID != "" {
					branchIDs[entry.ID] = true
				}
			}
		}
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("  <meta charset=\"UTF-8\">\n")
	b.WriteString("  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n")
	fmt.Fprintf(&b, "  <meta name=\"pi-session-id\" content=\"%s\">\n", html.EscapeString(header.ID))
	b.WriteString("  <meta name=\"pi-share-base-url\" content=\"\">\n")
	fmt.Fprintf(&b, "  <title>Pi Session %s</title>\n", html.EscapeString(shortExportID(header.ID)))
	b.WriteString("  <style>\n")
	b.WriteString(exportHTMLCSS())
	b.WriteString("  </style>\n</head>\n<body>\n")
	b.WriteString("  <div id=\"app\">\n")
	renderExportSidebar(&b, snapshot, labelByTarget, branchIDs)
	b.WriteString("    <main id=\"content\">\n")
	b.WriteString("      <div id=\"header-container\">")
	renderExportHeader(&b, snapshot, data.Stats)
	b.WriteString("</div>\n")
	b.WriteString("      <div id=\"messages\">\n")
	for i, entry := range entries {
		renderExportEntry(&b, entry, i, labelByTarget, toolResults)
	}
	b.WriteString("      </div>\n")
	b.WriteString("    </main>\n")
	b.WriteString("    <div id=\"image-modal\" class=\"image-modal\"><img id=\"modal-image\" src=\"\" alt=\"\"></div>\n")
	b.WriteString("  </div>\n")
	fmt.Fprintf(&b, "  <script id=\"session-data\" type=\"application/json\">%s</script>\n", encoded)
	b.WriteString("  <script>\n")
	b.WriteString(exportHTMLJS())
	b.WriteString("  </script>\n")
	b.WriteString("</body>\n</html>\n")
	return b.String(), nil
}

func computeExportStats(entries []SessionEntry) exportStats {
	var stats exportStats
	models := map[string]bool{}
	for _, entry := range entries {
		switch entry.Type {
		case "message":
			if entry.Message == nil {
				continue
			}
			msg := entry.Message
			switch ai.MessageRole(msg) {
			case "user":
				stats.UserMessages++
			case "assistant":
				stats.AssistantMessages++
				assistant, _ := ai.AsAssistantMessage(msg)
				stats.Tokens.Input += assistant.Usage.Input
				stats.Tokens.Output += assistant.Usage.Output
				stats.Tokens.CacheRead += assistant.Usage.CacheRead
				stats.Tokens.CacheWrite += assistant.Usage.CacheWrite
				stats.Tokens.TotalTokens += assistant.Usage.TotalTokens
				stats.Tokens.Cost.Input += assistant.Usage.Cost.Input
				stats.Tokens.Cost.Output += assistant.Usage.Cost.Output
				stats.Tokens.Cost.CacheRead += assistant.Usage.Cost.CacheRead
				stats.Tokens.Cost.CacheWrite += assistant.Usage.Cost.CacheWrite
				stats.Tokens.Cost.Total += assistant.Usage.Cost.Total
				if assistant.Provider != "" || assistant.Model != "" {
					models[firstNonEmpty(assistant.Provider, "?")+"/"+firstNonEmpty(assistant.Model, "?")] = true
				}
				for _, block := range ai.MessageBlocks(msg) {
					if block.Type == "toolCall" {
						stats.ToolCalls++
					}
				}
			case "toolResult":
				stats.ToolResults++
			}
		case "custom_message":
			stats.CustomMessages++
		case "compaction":
			stats.Compactions++
		case "branch_summary":
			stats.BranchSummaries++
		}
	}
	stats.Models = make([]string, 0, len(models))
	for model := range models {
		stats.Models = append(stats.Models, model)
	}
	sort.Strings(stats.Models)
	return stats
}

func collectExportLabels(entries []SessionEntry) map[string]string {
	labels := map[string]string{}
	for _, entry := range entries {
		if entry.Type != "label" || entry.TargetID == "" {
			continue
		}
		if entry.Label == "" {
			delete(labels, entry.TargetID)
		} else {
			labels[entry.TargetID] = entry.Label
		}
	}
	return labels
}

func collectExportToolResults(entries []SessionEntry) map[string]exportToolResult {
	results := map[string]exportToolResult{}
	for _, entry := range entries {
		if entry.Type == "message" && entry.Message != nil && ai.MessageRole(entry.Message) == "toolResult" && ai.MessageToolCallID(entry.Message) != "" {
			results[ai.MessageToolCallID(entry.Message)] = exportToolResult{Entry: entry, Msg: entry.Message}
		}
	}
	return results
}
