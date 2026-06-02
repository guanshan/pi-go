package harness

import (
	"context"

	"github.com/guanshan/pi-go/packages/agent"
	harnesscompaction "github.com/guanshan/pi-go/packages/agent/harness/compaction"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

type CompactionSettings = harnesscompaction.Settings
type CompactionResult = harnesscompaction.Result

func (h *AgentHarness) Compact(ctx context.Context, customInstructions string) (CompactionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := h.beginRun(ctx, PhaseCompaction)
	if err != nil {
		return CompactionResult{}, err
	}
	defer release()

	branch, err := h.sess.Branch(ctx, nil)
	if err != nil {
		return CompactionResult{}, err
	}
	prep, err := harnesscompaction.PrepareCompaction(branch, harnesscompaction.DefaultSettings)
	if err != nil {
		return CompactionResult{}, err
	}
	if prep == nil {
		return CompactionResult{}, &agent.AgentError{Code: agent.AgentErrCompaction, Msg: "Nothing to compact"}
	}
	before, err := h.emitSessionBeforeCompact(ctx, SessionBeforeCompactEvent{
		Preparation:        prep,
		BranchEntries:      append([]session.Entry(nil), branch...),
		CustomInstructions: customInstructions,
	})
	if err != nil {
		return CompactionResult{}, err
	}
	if before != nil && before.Cancel {
		return CompactionResult{}, &agent.AgentError{Code: agent.AgentErrCompaction, Msg: "Compaction cancelled"}
	}
	built, err := h.sess.BuildContext(ctx)
	if err != nil {
		return CompactionResult{}, err
	}
	fromHook := false
	var result CompactionResult
	if before != nil && before.Compaction != nil {
		result = *before.Compaction
		fromHook = true
	} else {
		model, thinking, apiKey, headers, err := h.compactionAuth(ctx)
		if err != nil {
			return CompactionResult{}, err
		}
		result, err = harnesscompaction.Compact(ctx, prep, model, apiKey, headers, customInstructions, h.registry, thinking)
		if err != nil {
			return CompactionResult{}, err
		}
	}
	if result.TokensBefore == 0 {
		result.TokensBefore = harnesscompaction.EstimateContextTokens(built.Messages).Tokens
	}
	if result.MessagesBefore == 0 {
		result.MessagesBefore = len(built.Messages)
	}
	if result.TokensAfter == 0 {
		if len(result.KeptMessages) > 0 {
			result.TokensAfter = harnesscompaction.EstimateContextTokens(result.KeptMessages).Tokens
		} else {
			result.TokensAfter = harnesscompaction.EstimateContextTokens([]agent.AgentMessage{CreateCompactionSummaryMessage(result.Summary, result.TokensBefore, "")}).Tokens
		}
	}
	firstKeptEntryID := result.FirstKeptEntryID
	if firstKeptEntryID == "" {
		firstKeptEntryID = firstKeptMessageEntryID(branch, len(result.KeptMessages)-1)
	}
	entryID, err := h.sess.AppendCompaction(ctx, result.Summary, firstKeptEntryID, result.TokensBefore, result.Details, fromHook)
	if err != nil {
		return CompactionResult{}, err
	}
	if entry, err := h.sess.Entry(ctx, entryID); err == nil {
		if compaction, ok := entry.(session.CompactionEntry); ok {
			if err := h.emitSessionCompact(ctx, SessionCompactEvent{CompactionEntry: compaction, FromHook: fromHook}); err != nil {
				return CompactionResult{}, err
			}
		}
	}
	return result, nil
}

func (h *AgentHarness) compactionAuth(ctx context.Context) (ai.Model, ai.ThinkingLevel, string, map[string]string, error) {
	h.mu.Lock()
	model := h.model
	thinking := h.thinkingLevel
	getAuth := h.getAuth
	h.mu.Unlock()
	if getAuth == nil {
		return model, thinking, "", nil, &agent.AgentError{Code: agent.AgentErrAuth, Msg: "no auth available for compaction"}
	}
	auth, err := getAuth(ctx, model)
	if err != nil {
		return model, thinking, "", nil, err
	}
	if auth.APIKey == "" {
		return model, thinking, "", nil, &agent.AgentError{Code: agent.AgentErrAuth, Msg: "no auth available for compaction"}
	}
	return model, thinking, auth.APIKey, auth.Headers, nil
}

func firstKeptMessageEntryID(entries []session.Entry, keptMessages int) string {
	if keptMessages <= 0 {
		return ""
	}
	messageIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.EntryType() == "message" && entry.EntryID() != "" {
			messageIDs = append(messageIDs, entry.EntryID())
		}
	}
	if keptMessages > len(messageIDs) {
		keptMessages = len(messageIDs)
	}
	if keptMessages == 0 {
		return ""
	}
	return messageIDs[len(messageIDs)-keptMessages]
}
