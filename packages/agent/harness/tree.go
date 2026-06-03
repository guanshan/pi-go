package harness

import (
	"context"
	"strings"

	"github.com/guanshan/pi-go/packages/agent"
	harnesscompaction "github.com/guanshan/pi-go/packages/agent/harness/compaction"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

func (h *AgentHarness) NavigateTree(ctx context.Context, targetID string, opts NavigateTreeOptions) (NavigateTreeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if targetID == "" {
		return NavigateTreeResult{}, &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: "target entry id is required"}
	}
	release, err := h.beginRun(ctx, PhaseBranchSummary)
	if err != nil {
		return NavigateTreeResult{}, err
	}
	defer release()

	oldLeaf, err := h.sess.LeafID(ctx)
	if err != nil {
		return NavigateTreeResult{}, err
	}
	oldLeafID := ""
	if oldLeaf != nil {
		oldLeafID = *oldLeaf
	}
	if oldLeafID == targetID {
		return NavigateTreeResult{Canceled: false}, nil
	}
	target, err := h.sess.Entry(ctx, targetID)
	if err != nil {
		return NavigateTreeResult{}, err
	}
	if target == nil {
		return NavigateTreeResult{}, &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: "target entry not found"}
	}
	collected, err := harnesscompaction.CollectEntriesForBranchSummary(ctx, h.sess, oldLeaf, targetID)
	if err != nil {
		return NavigateTreeResult{}, err
	}
	prep := TreePreparation{
		TargetID:            targetID,
		OldLeafID:           cloneStringPtr(oldLeaf),
		CommonAncestorID:    collected.CommonAncestorID,
		EntriesToSummarize:  collected.Entries,
		UserWantsSummary:    opts.UserWantsSummary,
		CustomInstructions:  opts.CustomInstructions,
		ReplaceInstructions: opts.ReplaceInstructions,
		Label:               opts.Label,
	}
	before, err := h.emitSessionBeforeTree(ctx, SessionBeforeTreeEvent{Preparation: prep})
	if err != nil {
		return NavigateTreeResult{}, err
	}
	if before != nil && before.Cancel {
		return NavigateTreeResult{OldLeafID: oldLeafID, NewLeafID: oldLeafID, Canceled: true}, nil
	}

	// Mirror TS agent-harness.ts navigateTree: the hook's summary object (if any)
	// becomes the summary; its customInstructions/replaceInstructions are NOT a
	// literal summary, they override the inputs fed to generateBranchSummary. So
	// a hook that returns only customInstructions must still trigger generation
	// when summarize is set, using the overridden instructions.
	var summary string
	var summaryDetails any
	fromHook := opts.GeneratedFromHook
	if before != nil && before.Summary != nil {
		summary = before.Summary.Summary
		if len(before.Summary.ReadFiles) > 0 || len(before.Summary.ModifiedFiles) > 0 {
			summaryDetails = branchSummaryDetails(before.Summary.ReadFiles, before.Summary.ModifiedFiles)
		}
		// fromHook is "hookResult?.summary !== undefined" in TS.
		fromHook = true
	}
	customInstructions := prep.CustomInstructions
	replaceInstructions := prep.ReplaceInstructions
	if before != nil {
		if before.CustomInstructions != "" {
			customInstructions = before.CustomInstructions
		}
		if before.ReplaceInstructions != nil {
			replaceInstructions = *before.ReplaceInstructions
		}
		if before.Label != nil {
			opts.Label = *before.Label
		}
	}
	// TS gate: !summaryText && options.summarize && entries.length > 0. SkipSummary
	// is a Go-only opt-out that defaults to false, so the default path matches TS.
	if !opts.SkipSummary && summary == "" && prep.UserWantsSummary && len(prep.EntriesToSummarize) > 0 {
		model, _, apiKey, headers, err := h.compactionAuth(ctx)
		if err != nil {
			return NavigateTreeResult{}, err
		}
		generated, err := harnesscompaction.GenerateBranchSummary(ctx, prep.EntriesToSummarize, harnesscompaction.BranchSummaryOptions{
			Model:               model,
			APIKey:              apiKey,
			Headers:             headers,
			CustomInstructions:  customInstructions,
			ReplaceInstructions: replaceInstructions,
			Registry:            h.registry,
		})
		if err != nil {
			return NavigateTreeResult{}, err
		}
		summary = generated.Summary
		summaryDetails = branchSummaryDetails(generated.ReadFiles, generated.ModifiedFiles)
	}

	var editorText string
	newLeaf := &targetID
	switch entry := target.(type) {
	case session.MessageEntry:
		if aiMessageRole(entry.Message) == "user" {
			newLeaf = entry.EntryParentID()
			editorText = aiMessageText(entry.Message)
		}
	case session.CustomMessageEntry:
		newLeaf = entry.EntryParentID()
		editorText = customMessageText(entry.Content)
	}
	var move *session.BranchMove
	if strings.TrimSpace(summary) != "" {
		move = &session.BranchMove{Summary: strings.TrimSpace(summary), Details: summaryDetails, FromHook: fromHook}
	}
	newLeafID, err := h.sess.MoveTo(ctx, newLeaf, move)
	if err != nil {
		return NavigateTreeResult{}, err
	}
	var summaryEntry *session.BranchSummaryEntry
	if move != nil {
		if entry, err := h.sess.Entry(ctx, newLeafID); err == nil {
			if branch, ok := entry.(session.BranchSummaryEntry); ok {
				copy := branch
				summaryEntry = &copy
			}
		}
	}
	result := NavigateTreeResult{
		OldLeafID:    oldLeafID,
		NewLeafID:    newLeafID,
		EditorText:   editorText,
		SummaryEntry: summaryEntry,
	}
	if err := h.emitSessionTree(ctx, SessionTreeEvent{
		NewLeafID:    result.NewLeafID,
		OldLeafID:    result.OldLeafID,
		SummaryEntry: result.SummaryEntry,
		FromHook:     fromHook,
	}); err != nil {
		return NavigateTreeResult{}, err
	}
	return result, nil
}

func aiMessageRole(message agent.AgentMessage) string {
	if message == nil {
		return ""
	}
	return message.MessageRole()
}

func aiMessageText(message agent.AgentMessage) string {
	return ai.MessageText(message)
}

func customMessageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []ai.ContentBlock:
		var b strings.Builder
		for _, block := range value {
			if block.Type == "text" {
				b.WriteString(block.Text)
			}
		}
		return b.String()
	default:
		return ""
	}
}

func branchSummaryDetails(readFiles, modifiedFiles []string) map[string]any {
	return map[string]any{
		"readFiles":     readFiles,
		"modifiedFiles": modifiedFiles,
	}
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
