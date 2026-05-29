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

	target, err := h.sess.Entry(ctx, targetID)
	if err != nil {
		return NavigateTreeResult{}, err
	}
	if target == nil {
		return NavigateTreeResult{}, &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: "target entry not found"}
	}
	oldLeaf, err := h.sess.LeafID(ctx)
	if err != nil {
		return NavigateTreeResult{}, err
	}
	oldLeafID := ""
	if oldLeaf != nil {
		oldLeafID = *oldLeaf
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
		UserWantsSummary:    opts.UserWantsSummary || opts.Summary != "" || opts.CustomInstructions != "",
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

	summary := opts.Summary
	fromHook := opts.GeneratedFromHook
	if before != nil {
		if before.Summary != nil {
			summary = before.Summary.Summary
			fromHook = true
		}
		if before.CustomInstructions != "" {
			if summary == "" || before.ReplaceInstructions != nil && *before.ReplaceInstructions {
				summary = before.CustomInstructions
			} else {
				summary = strings.TrimSpace(summary) + "\n\n" + strings.TrimSpace(before.CustomInstructions)
			}
		}
		if before.Label != nil {
			opts.Label = *before.Label
		}
	}
	if !opts.SkipSummary && summary == "" && prep.UserWantsSummary {
		model, _, apiKey, headers, err := h.compactionAuth(ctx)
		if err != nil {
			return NavigateTreeResult{}, err
		}
		generated, err := harnesscompaction.GenerateBranchSummary(ctx, prep.EntriesToSummarize, harnesscompaction.BranchSummaryOptions{
			Model:               model,
			APIKey:              apiKey,
			Headers:             headers,
			CustomInstructions:  prep.CustomInstructions,
			ReplaceInstructions: prep.ReplaceInstructions,
			Registry:            h.registry,
		})
		if err != nil {
			return NavigateTreeResult{}, err
		}
		summary = generated.Summary
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
		move = &session.BranchMove{Summary: strings.TrimSpace(summary), Details: opts.Details, FromHook: fromHook}
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

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
