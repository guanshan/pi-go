package core

import (
	"context"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
)

const branchSummaryPreamble = "The user explored a different conversation branch before returning here.\nSummary of that exploration:\n\n"

const branchSummaryPrompt = "Create a structured summary of this conversation branch for context when returning later.\n\nUse this EXACT format:\n\n## Goal\n[What was the user trying to accomplish in this branch?]\n\n## Constraints & Preferences\n- [Any constraints, preferences, or requirements mentioned]\n- [Or \"(none)\" if none were mentioned]\n\n## Progress\n### Done\n- [x] [Completed tasks/changes]\n\n### In Progress\n- [ ] [Work that was started but not finished]\n\n### Blocked\n- [Issues preventing progress, if any]\n\n## Key Decisions\n- **[Decision]**: [Brief rationale]\n\n## Next Steps\n1. [What should happen next to continue this work]\n\nKeep each section concise. Preserve exact file paths, function names, and error messages."

type branchSummaryResult struct {
	Summary string
	Details CompactionDetails
	Aborted bool
}

func buildBranchSummary(ctx context.Context, session *SessionManager, registry *ai.ModelRegistry, model ai.Model, thinkingLevel ai.ThinkingLevel, reserveTokens int, oldLeafID, targetID, customInstructions string, replaceInstructions bool) (branchSummaryResult, error) {
	entries, _, err := collectEntriesForBranchSummary(session, oldLeafID, targetID)
	if err != nil {
		return branchSummaryResult{}, err
	}
	if len(entries) == 0 {
		return branchSummaryResult{}, nil
	}
	return generateBranchSummary(ctx, registry, model, thinkingLevel, entries, reserveTokens, customInstructions, replaceInstructions)
}

func generateBranchSummary(ctx context.Context, registry *ai.ModelRegistry, model ai.Model, thinkingLevel ai.ThinkingLevel, entries []SessionEntry, reserveTokens int, customInstructions string, replaceInstructions bool) (branchSummaryResult, error) {
	if reserveTokens <= 0 {
		reserveTokens = defaultCompactionReserveTokens
	}
	tokenBudget := 0
	if model.ContextWindow > reserveTokens {
		tokenBudget = model.ContextWindow - reserveTokens
	}
	messages, fileOps, _ := prepareBranchEntries(entries, tokenBudget)
	if len(messages) == 0 {
		return branchSummaryResult{}, nil
	}

	instructions := branchSummaryPrompt
	if replaceInstructions && strings.TrimSpace(customInstructions) != "" {
		instructions = strings.TrimSpace(customInstructions)
	} else if strings.TrimSpace(customInstructions) != "" {
		instructions += "\n\nAdditional focus: " + strings.TrimSpace(customInstructions)
	}
	promptText := "<conversation>\n" + serializeConversation(messages) + "\n</conversation>\n\n" + instructions
	summary, err := completeCompactionPromptWithMaxTokens(ctx, registry, model, thinkingLevel, 2048, promptText)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return branchSummaryResult{Aborted: true}, nil
		}
		return branchSummaryResult{}, err
	}
	details := computeCompactionDetails(fileOps)
	fullSummary := branchSummaryPreamble + summary + formatCompactionFileOperations(details)
	return branchSummaryResult{Summary: fullSummary, Details: details}, nil
}

func collectEntriesForBranchSummary(session *SessionManager, oldLeafID, targetID string) ([]SessionEntry, string, error) {
	if session == nil || oldLeafID == "" {
		return nil, "", nil
	}
	oldPath, err := session.BranchFrom(oldLeafID)
	if err != nil {
		return nil, "", err
	}
	targetPath, err := session.BranchFrom(targetID)
	if err != nil {
		return nil, "", err
	}
	oldIDs := make(map[string]struct{}, len(oldPath))
	for _, entry := range oldPath {
		if entry.ID != "" {
			oldIDs[entry.ID] = struct{}{}
		}
	}
	commonAncestorID := ""
	for i := len(targetPath) - 1; i >= 0; i-- {
		if _, ok := oldIDs[targetPath[i].ID]; ok {
			commonAncestorID = targetPath[i].ID
			break
		}
	}
	byID := make(map[string]SessionEntry, len(session.Entries))
	for _, entry := range session.Entries {
		if entry.ID != "" {
			byID[entry.ID] = entry
		}
	}
	entries := []SessionEntry{}
	current := oldLeafID
	for current != "" && current != commonAncestorID {
		entry, ok := byID[current]
		if !ok {
			break
		}
		entries = append(entries, entry)
		if entry.ParentID == nil {
			break
		}
		current = *entry.ParentID
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, commonAncestorID, nil
}

func prepareBranchEntries(entries []SessionEntry, tokenBudget int) ([]ai.Message, fileOperations, int) {
	messages := []ai.Message{}
	fileOps := newFileOperations()
	totalTokens := 0
	for _, entry := range entries {
		if entry.Type != "branch_summary" {
			continue
		}
		details := compactionDetailsFromAny(entry.Details)
		for _, path := range details.ReadFiles {
			fileOps.Read[path] = struct{}{}
		}
		for _, path := range details.ModifiedFiles {
			fileOps.Edited[path] = struct{}{}
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		message, ok := compactionMessageFromEntry(entries[i], true)
		if !ok {
			continue
		}
		extractFileOpsFromMessage(message, fileOps)
		tokens := estimateCompactionMessageTokens(message)
		if tokenBudget > 0 && totalTokens+tokens > tokenBudget {
			if (entries[i].Type == "compaction" || entries[i].Type == "branch_summary") && float64(totalTokens) < float64(tokenBudget)*0.9 {
				messages = append([]ai.Message{message}, messages...)
				totalTokens += tokens
			}
			break
		}
		messages = append([]ai.Message{message}, messages...)
		totalTokens += tokens
	}
	return messages, fileOps, totalTokens
}

func mustBuildBranchSummary(ctx context.Context, session *SessionManager, registry *ai.ModelRegistry, model ai.Model, thinkingLevel ai.ThinkingLevel, reserveTokens int, oldLeafID, targetID, customInstructions string, replaceInstructions bool) (string, CompactionDetails, error) {
	result, err := buildBranchSummary(ctx, session, registry, model, thinkingLevel, reserveTokens, oldLeafID, targetID, customInstructions, replaceInstructions)
	if err != nil {
		return "", CompactionDetails{}, err
	}
	if result.Aborted {
		return "", CompactionDetails{}, context.Canceled
	}
	return result.Summary, result.Details, nil
}
