package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func (a *AgentSession) NavigateTree(ctx context.Context, targetID string, opts NavigateTreeOptions) (NavigateTreeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil || a.Session == nil {
		return NavigateTreeResult{}, fmt.Errorf("session is nil")
	}
	oldLeaf := a.Session.CurrentLeafID()
	resolvedTargetID := targetID
	if targetID != "" {
		var err error
		resolvedTargetID, err = a.Session.ResolveEntryID(targetID)
		if err != nil {
			return NavigateTreeResult{}, err
		}
	}
	entriesToSummarize := []SessionEntry(nil)
	commonAncestorID := ""
	if opts.Summarize && oldLeaf != "" && resolvedTargetID != "" && oldLeaf != resolvedTargetID {
		var err error
		entriesToSummarize, commonAncestorID, err = collectEntriesForBranchSummary(a.Session, oldLeaf, resolvedTargetID)
		if err != nil {
			return NavigateTreeResult{}, err
		}
	}
	treeCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.branchSummaryCancel = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.branchSummaryCancel = nil
		a.mu.Unlock()
		cancel()
	}()
	var beforeTree *coreext.SessionBeforeTreeEvent
	fromExtension := false
	if a.extensionHasHandlers("session_before_tree") {
		beforeTree = a.emitExtensionBeforeTree(treeCtx, resolvedTargetID, oldLeaf, commonAncestorID, entriesToSummarize, opts)
		if beforeTree.Cancel {
			return NavigateTreeResult{OldLeafID: oldLeaf, Cancelled: true}, nil
		}
		if beforeTree.CustomInstructions != nil {
			opts.CustomInstructions = *beforeTree.CustomInstructions
		}
		if beforeTree.ReplaceInstructions != nil {
			opts.ReplaceInstructions = *beforeTree.ReplaceInstructions
		}
		if beforeTree.Label != nil {
			opts.Label = *beforeTree.Label
		}
		if beforeTree.Summary != nil && opts.Summarize {
			fromExtension = true
		}
	}
	if err := ctxErr(treeCtx); err != nil {
		return NavigateTreeResult{OldLeafID: oldLeaf, Cancelled: true}, nil
	}
	if err := a.Session.SetLeaf(resolvedTargetID); err != nil {
		return NavigateTreeResult{}, err
	}
	result := NavigateTreeResult{OldLeafID: oldLeaf, NewLeafID: a.Session.CurrentLeafID()}
	if opts.Label != "" && result.NewLeafID != "" {
		if err := a.Session.Append(SessionEntry{Type: "label", TargetID: result.NewLeafID, Label: opts.Label}); err != nil {
			return NavigateTreeResult{}, err
		}
	}
	if opts.Summarize && oldLeaf != "" && result.NewLeafID != "" && oldLeaf != result.NewLeafID {
		summary := ""
		var details any
		if beforeTree != nil && beforeTree.Summary != nil {
			summary = beforeTree.Summary.Summary
			details = beforeTree.Summary.Details
		}
		if summary == "" {
			snapshot := a.modelSnapshot()
			generatedSummary, generatedDetails, err := mustBuildBranchSummary(treeCtx, a.Session, a.Registry, snapshot.Model, snapshot.ThinkingLevel, a.Settings.BranchSummaryReserveTokens(), oldLeaf, resolvedTargetID, opts.CustomInstructions, opts.ReplaceInstructions)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					result.Cancelled = true
					return result, nil
				}
				return NavigateTreeResult{}, err
			}
			summary = generatedSummary
			details = generatedDetails
		}
		if strings.TrimSpace(summary) == "" {
			a.emitExtensionSessionTree(result.NewLeafID, oldLeaf, nil, fromExtension)
			return result, nil
		}
		entry := SessionEntry{Type: "branch_summary", FromID: oldLeaf, Summary: summary, Details: details, FromHook: fromExtension}
		if err := a.Session.Append(entry); err != nil {
			return NavigateTreeResult{}, err
		}
		result.SummaryEntry = &BranchSummaryEntry{Type: "branch_summary", FromID: oldLeaf, Summary: summary, Details: details, FromHook: fromExtension}
	}
	a.emitExtensionSessionTree(result.NewLeafID, oldLeaf, result.SummaryEntry, fromExtension)
	return result, nil
}

// GetUserMessagesForForking returns every user message in the session that can
// serve as a fork/branch point. It mirrors TS agent-session.ts
// getUserMessagesForForking, which iterates the full entry list
// (sessionManager.getEntries()) rather than only the current branch — so user
// messages on abandoned/sibling branches are forkable too. Both the interactive
// /fork overlay and the RPC get_fork_messages command read through this.
func (a *AgentSession) GetUserMessagesForForking() []ForkableUserMessage {
	if a == nil || a.Session == nil {
		return nil
	}
	entries := a.Session.EntriesSnapshot()
	result := make([]ForkableUserMessage, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != "message" || entry.Message == nil || ai.MessageRole(entry.Message) != "user" || entry.ID == "" {
			continue
		}
		text := strings.TrimSpace(ai.MessageText(entry.Message))
		if text == "" {
			continue
		}
		result = append(result, ForkableUserMessage{EntryID: entry.ID, Text: text})
	}
	return result
}

func (a *AgentSession) GetSessionStats() SessionStats {
	if a == nil || a.Session == nil {
		return SessionStats{}
	}
	ctx := a.Session.BuildContext()
	stats := SessionStats{SessionFile: a.Session.File(), SessionID: a.Session.SessionID(), TotalMessages: len(ctx.Messages), ContextUsage: a.GetContextUsage()}
	var usage ai.Usage
	for _, msg := range ctx.Messages {
		switch ai.MessageRole(msg) {
		case "user":
			stats.UserMessages++
		case "assistant":
			stats.AssistantMessages++
			assistant, _ := ai.AsAssistantMessage(msg)
			usage.Input += assistant.Usage.Input
			usage.Output += assistant.Usage.Output
			usage.CacheRead += assistant.Usage.CacheRead
			usage.CacheWrite += assistant.Usage.CacheWrite
			usage.TotalTokens += assistant.Usage.TotalTokens
			usage.Cost.Input += assistant.Usage.Cost.Input
			usage.Cost.Output += assistant.Usage.Cost.Output
			usage.Cost.CacheRead += assistant.Usage.Cost.CacheRead
			usage.Cost.CacheWrite += assistant.Usage.Cost.CacheWrite
			usage.Cost.Total += assistant.Usage.Cost.Total
			for _, block := range ai.MessageBlocks(msg) {
				if block.Type == "toolCall" {
					stats.ToolCalls++
				}
			}
		case "toolResult":
			stats.ToolResults++
		}
	}
	stats.Tokens = TokenStats{Input: usage.Input, Output: usage.Output, CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite, Total: usage.TotalTokens}
	stats.Cost = usage.Cost.Total
	return stats
}

func (a *AgentSession) GetContextUsage() *ContextUsage {
	if a == nil || a.Session == nil {
		return nil
	}
	model := a.CurrentModel()
	if model.ContextWindow <= 0 {
		return nil
	}
	return &ContextUsage{UsedTokens: estimateMessageTokens(a.Session.BuildContext().Messages), ContextWindow: model.ContextWindow, EstimatedAt: time.Now()}
}

func estimateMessageTokens(messages []ai.Message) int {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		text := strings.TrimSpace(ai.MessageText(msg))
		if text == "" {
			continue
		}
		switch ai.MessageRole(msg) {
		case "user":
			parts = append(parts, "User: "+text)
		case "assistant":
			parts = append(parts, "Assistant: "+text)
		case "toolResult":
			parts = append(parts, "Tool "+ai.MessageToolName(msg)+": "+text)
		default:
			parts = append(parts, text)
		}
	}
	return estimateTokens(parts)
}

func (a *AgentSession) ExportToHTML(ctx context.Context, outputPath string) (string, error) {
	_ = ctx
	if a == nil || a.Session == nil {
		return "", fmt.Errorf("session is nil")
	}
	if outputPath == "" {
		if sessionFile := a.Session.File(); sessionFile != "" {
			outputPath = defaultExportPath(sessionFile)
		} else {
			outputPath = filepath.Join(a.Session.CWD(), fmt.Sprintf("%s-session-%s.html", AppName, shortExportID(a.Session.SessionID())))
		}
	}
	body, err := generateSessionHTML(a.Session)
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

func (a *AgentSession) ExportToJsonl(outputPath string) (string, error) {
	if a == nil || a.Session == nil {
		return "", fmt.Errorf("session is nil")
	}
	if outputPath == "" {
		if sessionFile := a.Session.File(); sessionFile != "" {
			base := strings.TrimSuffix(filepath.Base(sessionFile), filepath.Ext(sessionFile))
			outputPath = filepath.Join(filepath.Dir(sessionFile), base+"-export.jsonl")
		} else {
			outputPath = filepath.Join(a.Session.CWD(), fmt.Sprintf("%s-session-%s.jsonl", AppName, shortExportID(a.Session.SessionID())))
		}
	}
	if dir := filepath.Dir(outputPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	header, entries, _ := a.Session.Snapshot()
	if err := writeJSONLine(file, header); err != nil {
		return "", err
	}
	for _, entry := range entries {
		if err := writeJSONLine(file, entry); err != nil {
			return "", err
		}
	}
	return outputPath, nil
}
