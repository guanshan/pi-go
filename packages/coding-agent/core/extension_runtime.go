package core

import (
	"bytes"
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
	}
	// tool_call / tool_result are NOT emitted from the agent-event subscription.
	// They run as part of the execution chain via the BeforeToolCall/AfterToolCall
	// hooks (see beforeExtensionToolCall / afterExtensionToolCall) so a handler's
	// return value can block, mutate, or override the call. Mirrors the TS
	// AgentSession._installAgentToolHooks (agent-session.ts:393-447), where these
	// hooks own tool_call/tool_result and the agent-event subscription only carries
	// the passive tool_execution_* notifications.
}

// beforeExtensionToolCall runs the extension runner's tool_call handlers as part
// of the tool execution chain. It emits a TS-shaped { toolCallId, toolName, input }
// payload; if a handler blocks, the framework's block mechanism short-circuits the
// call (tool_exec.go BeforeToolCall path). When a handler mutates the input in
// place, the mutated arguments are stashed so the tool adapter executes the patched
// arguments (the agent froze the raw bytes before this hook ran). Mirrors
// agent-session.ts:400-420 and runner.ts emitToolCall (runner.ts:806-827).
func (a *AgentSession) beforeExtensionToolCall(_ context.Context, tc agentcore.BeforeToolCallContext) (agentcore.BeforeToolCallResult, error) {
	if !a.extensionHasHandlers("tool_call") {
		return agentcore.BeforeToolCallResult{}, nil
	}
	// Capture the arguments the agent will execute (it froze the raw bytes before
	// this hook) so a handler's in-place input mutation can be matched back to the
	// adapter call. The adapter receives these exact bytes as its raw arguments.
	originalRaw, err := json.Marshal(tc.Args)
	if err != nil {
		originalRaw = nil
	}
	event := &coreext.ToolCallEvent{
		Type:       "tool_call",
		ToolCallID: tc.ToolCall.ID,
		ToolName:   tc.ToolCall.Name,
		Input:      tc.Args,
	}
	a.emitExtensionEvent("tool_call", event)
	if event.Block {
		return agentcore.BeforeToolCallResult{Block: true, Reason: event.Reason}, nil
	}
	// A handler may have mutated event.Input in place; stash the patched input keyed
	// by the original bytes so the adapter substitutes it at execution time.
	if originalRaw != nil {
		a.stashMutatedToolArgs(tc.ToolCall.ID, string(originalRaw), event.Input)
	}
	return agentcore.BeforeToolCallResult{}, nil
}

// afterExtensionToolCall runs the extension runner's tool_result handlers after a
// tool executes, emitting { toolCallId, toolName, input, content, details, isError }.
// Handler overrides of content/details/isError are merged back into the pipeline.
// Mirrors agent-session.ts:422-446 and runner.ts emitToolResult (runner.ts:756-805).
func (a *AgentSession) afterExtensionToolCall(_ context.Context, tc agentcore.AfterToolCallContext) (agentcore.AfterToolCallResult, error) {
	input := tc.Args
	if mutated, ok := a.consumeMutatedToolInput(tc.ToolCall.ID); ok {
		input = mutated
	}
	if !a.extensionHasHandlers("tool_result") {
		return agentcore.AfterToolCallResult{}, nil
	}
	event := &coreext.ToolResultEvent{
		Type:       "tool_result",
		ToolCallID: tc.ToolCall.ID,
		ToolName:   tc.ToolCall.Name,
		Input:      input,
		Content:    tc.Result.Content,
		Details:    tc.Result.Details,
		IsError:    tc.IsError,
	}
	a.emitExtensionEvent("tool_result", event)
	isError := event.IsError
	return agentcore.AfterToolCallResult{
		Content:    event.Content,
		HasContent: true,
		Details:    event.Details,
		HasDetails: true,
		IsError:    &isError,
	}, nil
}

// stashMutatedToolArgs records the arguments a tool_call handler mutated in place
// so the tool adapter executes the patched input. It is a no-op when the input is
// unchanged (the common case) or cannot be marshaled. The entry is keyed by the
// original execute-argument bytes, which the adapter receives verbatim. The
// tool call id is also kept so tool_result handlers see the same patched input.
func (a *AgentSession) stashMutatedToolArgs(toolCallID, originalKey string, mutated any) {
	encoded, err := json.Marshal(mutated)
	if err != nil {
		return
	}
	if bytes.Equal([]byte(originalKey), encoded) {
		return
	}
	a.mu.Lock()
	if a.mutatedToolArgs == nil {
		a.mutatedToolArgs = map[string]json.RawMessage{}
	}
	a.mutatedToolArgs[originalKey] = encoded
	if toolCallID != "" {
		if a.mutatedToolInputs == nil {
			a.mutatedToolInputs = map[string]any{}
		}
		a.mutatedToolInputs[toolCallID] = cloneExtensionInput(mutated)
	}
	a.mu.Unlock()
}

// consumeMutatedToolArgs substitutes the patched arguments a tool_call handler
// stashed for these raw bytes, falling back to raw when no substitution applies.
// The stash entry is cleared so it is applied exactly once.
func (a *AgentSession) consumeMutatedToolArgs(raw json.RawMessage) json.RawMessage {
	if a == nil || len(raw) == 0 {
		return raw
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	encoded, ok := a.mutatedToolArgs[string(raw)]
	if !ok {
		return raw
	}
	delete(a.mutatedToolArgs, string(raw))
	return encoded
}

// consumeMutatedToolInput returns the input shape seen by a tool_result handler
// after a tool_call handler rewrote it. It is keyed by call id because result
// hooks do have that id even though the execution adapter only sees raw args.
func (a *AgentSession) consumeMutatedToolInput(toolCallID string) (any, bool) {
	if a == nil || toolCallID == "" {
		return nil, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	input, ok := a.mutatedToolInputs[toolCallID]
	if !ok {
		return nil, false
	}
	delete(a.mutatedToolInputs, toolCallID)
	return input, true
}

func cloneExtensionInput(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return value
	}
	return decoded
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
