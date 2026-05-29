package core

import (
	"context"
	"encoding/json"
	"time"

	agentcore "github.com/guanshan/pi-go/packages/agent"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func (a *AgentSession) extensionHasHandlers(eventType string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	runtime := a.extensionRuntime
	a.mu.Unlock()
	return runtime != nil && runtime.HasHandlers(eventType)
}

func (a *AgentSession) emitExtensionEvent(eventType string, payload any) {
	if a == nil {
		return
	}
	a.mu.Lock()
	runtime := a.extensionRuntime
	a.mu.Unlock()
	if runtime == nil || !runtime.HasHandlers(eventType) {
		return
	}
	runtime.Emit(eventType, payload)
}

func (a *AgentSession) emitExtensionAgentEvent(event agentcore.AgentEvent, turnIndex *int) {
	switch ev := event.(type) {
	case agentcore.AgentStartEvent:
		a.emitExtensionEvent("agent_start", &coreext.AgentStartEvent{Type: "agent_start"})
	case agentcore.AgentEndEvent:
		a.emitExtensionEvent("agent_end", &coreext.AgentEndEvent{Type: "agent_end", Messages: ev.Messages})
	case agentcore.TurnStartEvent:
		if turnIndex != nil {
			*turnIndex++
		}
		a.emitExtensionEvent("turn_start", &coreext.TurnStartEvent{Type: "turn_start", TurnIndex: derefTurnIndex(turnIndex), Timestamp: time.Now().UnixMilli()})
	case agentcore.TurnEndEvent:
		a.emitExtensionEvent("turn_end", &coreext.TurnEndEvent{Type: "turn_end", TurnIndex: derefTurnIndex(turnIndex), Message: ev.Message, ToolResults: ev.ToolResults})
	case agentcore.MessageStartEvent:
		a.emitExtensionEvent("message_start", &coreext.MessageStartEvent{Type: "message_start", Message: ev.Message})
	case agentcore.MessageUpdateEvent:
		a.emitExtensionEvent("message_update", &coreext.MessageUpdateEvent{Type: "message_update", Message: ev.Message, AssistantMessageEvent: ev.AssistantEvent})
	case agentcore.MessageEndEvent:
		a.emitExtensionEvent("message_end", &coreext.MessageEndEvent{Type: "message_end", Message: ev.Message})
	case agentcore.ToolExecutionStartEvent:
		a.emitExtensionEvent("tool_call", &coreext.ToolCallEvent{Type: "tool_call", ToolCallID: ev.ToolCallID, ToolName: ev.ToolName, Args: decodeExtensionArgs(ev.Args)})
	case agentcore.ToolExecutionEndEvent:
		a.emitExtensionEvent("tool_result", &coreext.ToolResultEvent{Type: "tool_result", ToolCallID: ev.ToolCallID, ToolName: ev.ToolName, Args: nil, Content: ev.Result.Content, Details: ev.Result.Details, IsError: ev.IsError})
	}
}

func (a *AgentSession) shouldCancelSessionSwitch(reason coreext.SessionStartReason, targetSessionFile string) bool {
	event := &coreext.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: reason, TargetSessionFile: targetSessionFile}
	a.emitExtensionEvent("session_before_switch", event)
	return event.Cancel
}

func (a *AgentSession) shouldCancelSessionFork(entryID string, position ForkPosition) bool {
	event := &coreext.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: entryID, Position: string(position)}
	a.emitExtensionEvent("session_before_fork", event)
	return event.Cancel
}

func (a *AgentSession) emitExtensionSessionShutdown(reason coreext.SessionShutdownReason, targetSessionFile string) {
	a.emitExtensionEvent("session_shutdown", &coreext.SessionShutdownEvent{Type: "session_shutdown", Reason: reason, TargetSessionFile: targetSessionFile})
}

func (a *AgentSession) emitExtensionSessionStart(reason coreext.SessionStartReason, previousSessionFile string) {
	a.emitExtensionEvent("session_start", &coreext.SessionStartEvent{Type: "session_start", Reason: reason, PreviousSessionFile: previousSessionFile})
}

func (a *AgentSession) emitExtensionBeforeCompaction(ctx context.Context, preparation *compactionPreparation, branchEntries []SessionEntry, customInstructions string) *coreext.SessionBeforeCompactEvent {
	event := &coreext.SessionBeforeCompactEvent{
		Type:               "session_before_compact",
		Preparation:        buildCompactionPreparationPayload(preparation),
		BranchEntries:      append([]SessionEntry(nil), branchEntries...),
		CustomInstructions: customInstructions,
		Signal:             ctx,
	}
	a.emitExtensionEvent("session_before_compact", event)
	return event
}

func (a *AgentSession) emitExtensionSessionCompact(entry SessionEntry, fromExtension bool) {
	a.emitExtensionEvent("session_compact", &coreext.SessionCompactEvent{Type: "session_compact", CompactionEntry: entry, FromExtension: fromExtension})
}

func (a *AgentSession) emitExtensionBeforeTree(ctx context.Context, targetID, oldLeafID, commonAncestorID string, entriesToSummarize []SessionEntry, opts NavigateTreeOptions) *coreext.SessionBeforeTreeEvent {
	event := &coreext.SessionBeforeTreeEvent{
		Type: "session_before_tree",
		Preparation: coreext.TreePreparation{
			TargetID:            targetID,
			OldLeafID:           oldLeafID,
			CommonAncestorID:    commonAncestorID,
			EntriesToSummarize:  append([]SessionEntry(nil), entriesToSummarize...),
			UserWantsSummary:    opts.Summarize,
			CustomInstructions:  opts.CustomInstructions,
			ReplaceInstructions: opts.ReplaceInstructions,
			Label:               opts.Label,
		},
		Signal: ctx,
	}
	a.emitExtensionEvent("session_before_tree", event)
	return event
}

func (a *AgentSession) emitExtensionSessionTree(newLeafID, oldLeafID string, summaryEntry *BranchSummaryEntry, fromExtension bool) {
	a.emitExtensionEvent("session_tree", &coreext.SessionTreeEvent{Type: "session_tree", NewLeafID: newLeafID, OldLeafID: oldLeafID, SummaryEntry: summaryEntry, FromExtension: fromExtension})
}

func (a *AgentSession) shutdownExtensionRuntime(ctx context.Context) {
	if a == nil {
		return
	}
	a.mu.Lock()
	runtime := a.extensionRuntime
	a.mu.Unlock()
	if runtime == nil {
		return
	}
	_ = runtime.Shutdown(ctx)
}

func decodeExtensionArgs(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return string(raw)
	}
	return decoded
}

func derefTurnIndex(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func buildCompactionPreparationPayload(preparation *compactionPreparation) map[string]any {
	if preparation == nil {
		return nil
	}
	return map[string]any{
		"firstKeptEntryId":    preparation.FirstKeptEntryID,
		"tokensBefore":        preparation.TokensBefore,
		"isSplitTurn":         preparation.IsSplitTurn,
		"previousSummary":     preparation.PreviousSummary,
		"messagesToSummarize": len(preparation.MessagesToSummarize),
		"turnPrefixMessages":  len(preparation.TurnPrefixMessages),
		"details":             computeCompactionDetails(preparation.FileOps),
	}
}
