package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/guanshan/pi-go/packages/ai"
)

type ExecutedToolBatch struct {
	Messages  []ai.ToolResultMessage
	Terminate bool
}

type finalizedToolCall struct {
	ToolCall ai.ToolCall
	Result   AgentToolResult
	IsError  bool
}

type preparedToolCall struct {
	ToolCall ai.ToolCall
	Tool     AgentTool
	Args     any
	RawArgs  json.RawMessage
}

func executeToolCalls(ctx context.Context, curr *AgentContext, assistant ai.AssistantMessage, cfg AgentLoopConfig, emit AgentEventSink) (ExecutedToolBatch, error) {
	calls := toolCallsFromAssistant(assistant)
	if len(calls) == 0 {
		return ExecutedToolBatch{}, nil
	}
	// One read-only snapshot is taken for the whole batch and shared with every
	// BeforeToolCall/AfterToolCall hook. curr.Messages is not mutated while the
	// batch runs (the loop appends tool results only after this returns), so a
	// single snapshot is consistent for all calls and avoids copying the entire
	// transcript once per tool — which the parallel path previously did inside
	// every worker. This mirrors the low-overhead reference passing in the TS
	// source while still handing hooks an isolated copy.
	snapshot := copyAgentContext(*curr)
	if cfg.ToolExecution == ToolExecutionSequential || batchRequiresSequential(curr.Tools, calls) {
		return executeToolCallsSequential(ctx, snapshot, assistant, calls, cfg, emit)
	}
	return executeToolCallsParallel(ctx, snapshot, assistant, calls, cfg, emit)
}

func executeToolCallsSequential(ctx context.Context, snapshot AgentContext, assistant ai.AssistantMessage, calls []ai.ToolCall, cfg AgentLoopConfig, emit AgentEventSink) (ExecutedToolBatch, error) {
	finalized := make([]finalizedToolCall, 0, len(calls))
	messages := make([]ai.ToolResultMessage, 0, len(calls))
	for _, call := range calls {
		if err := emit(ctx, ToolExecutionStartEvent{ToolCallID: call.ID, ToolName: call.Name, Args: cloneRaw(call.Arguments)}); err != nil {
			return ExecutedToolBatch{}, err
		}
		var updateErr error
		final, err := prepareAndRunTool(ctx, snapshot, assistant, call, cfg, func(partial AgentToolResult) {
			if updateErr != nil {
				return
			}
			updateErr = emit(ctx, ToolExecutionUpdateEvent{
				ToolCallID:    call.ID,
				ToolName:      call.Name,
				Args:          cloneRaw(call.Arguments),
				PartialResult: partial,
			})
		})
		if err != nil {
			return ExecutedToolBatch{}, err
		}
		if updateErr != nil {
			return ExecutedToolBatch{}, updateErr
		}
		if err := emit(ctx, ToolExecutionEndEvent{ToolCallID: call.ID, ToolName: call.Name, Result: final.Result, IsError: final.IsError}); err != nil {
			return ExecutedToolBatch{}, err
		}
		msg := createToolResultMessage(final)
		if err := emitToolResultMessage(ctx, msg, emit); err != nil {
			return ExecutedToolBatch{}, err
		}
		finalized = append(finalized, final)
		messages = append(messages, msg)
		if ctx.Err() != nil {
			break
		}
	}
	return ExecutedToolBatch{Messages: messages, Terminate: shouldTerminateToolBatch(finalized)}, nil
}

type toolSlot struct {
	final    *finalizedToolCall
	prepared *preparedToolCall
}

type parallelToolEvent struct {
	update *ToolExecutionUpdateEvent
	end    *finalizedToolCall
	idx    int
}

func executeToolCallsParallel(ctx context.Context, snapshot AgentContext, assistant ai.AssistantMessage, calls []ai.ToolCall, cfg AgentLoopConfig, emit AgentEventSink) (ExecutedToolBatch, error) {
	slots := make([]toolSlot, len(calls))
	for i, call := range calls {
		if err := emit(ctx, ToolExecutionStartEvent{ToolCallID: call.ID, ToolName: call.Name, Args: cloneRaw(call.Arguments)}); err != nil {
			return ExecutedToolBatch{}, err
		}
		prepared, final, err := prepareToolCall(ctx, snapshot, assistant, call, cfg)
		if err != nil {
			return ExecutedToolBatch{}, err
		}
		if final != nil {
			slots[i].final = final
			if err := emit(ctx, ToolExecutionEndEvent{ToolCallID: final.ToolCall.ID, ToolName: final.ToolCall.Name, Result: final.Result, IsError: final.IsError}); err != nil {
				return ExecutedToolBatch{}, err
			}
			if ctx.Err() != nil {
				break
			}
			continue
		}
		slots[i].prepared = prepared
		if ctx.Err() != nil {
			break
		}
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	events := make(chan parallelToolEvent, len(calls))
	var wg sync.WaitGroup
	sendEvent := func(event parallelToolEvent) bool {
		select {
		case events <- event:
			return true
		case <-workerCtx.Done():
			return false
		case <-done:
			return false
		}
	}
	for i := range slots {
		if slots[i].prepared == nil {
			continue
		}
		wg.Add(1)
		go func(idx int, prepared preparedToolCall) {
			defer wg.Done()
			final := executePreparedAndFinalize(workerCtx, snapshot, assistant, prepared, cfg, func(partial AgentToolResult) {
				sendEvent(parallelToolEvent{idx: idx, update: &ToolExecutionUpdateEvent{
					ToolCallID:    prepared.ToolCall.ID,
					ToolName:      prepared.ToolCall.Name,
					Args:          cloneRaw(prepared.ToolCall.Arguments),
					PartialResult: partial,
				}})
			})
			sendEvent(parallelToolEvent{idx: idx, end: &final})
		}(i, *slots[i].prepared)
	}
	go func() {
		wg.Wait()
		close(events)
	}()

	// Unified sink-failure policy (matches the prep phase above and the
	// sequential path): the first emit failure of any lifecycle event is fatal.
	// We stop emitting further events, cancel the in-flight workers so they wind
	// down promptly, keep draining the event channel so no worker leaks, and
	// return the error. We do NOT append the partially executed tool results to
	// the batch — runLoop discards them and the single terminal failure sequence
	// is emitted by RunAgentLoop, so the transcript never carries both real tool
	// results and a synthetic error turn.
	var firstErr error
	for event := range events {
		if firstErr != nil {
			continue
		}
		if event.update != nil {
			if err := emit(ctx, *event.update); err != nil {
				firstErr = err
				cancel()
			}
			continue
		}
		if event.end != nil {
			final := *event.end
			slots[event.idx].final = &final
			if err := emit(ctx, ToolExecutionEndEvent{ToolCallID: final.ToolCall.ID, ToolName: final.ToolCall.Name, Result: final.Result, IsError: final.IsError}); err != nil {
				firstErr = err
				cancel()
			}
		}
	}
	close(done)
	if firstErr != nil {
		return ExecutedToolBatch{}, firstErr
	}

	finalized := make([]finalizedToolCall, 0, len(slots))
	messages := make([]ai.ToolResultMessage, 0, len(slots))
	for _, slot := range slots {
		if slot.final == nil {
			continue
		}
		finalized = append(finalized, *slot.final)
		msg := createToolResultMessage(*slot.final)
		if err := emitToolResultMessage(ctx, msg, emit); err != nil {
			return ExecutedToolBatch{}, err
		}
		messages = append(messages, msg)
	}
	return ExecutedToolBatch{Messages: messages, Terminate: shouldTerminateToolBatch(finalized)}, nil
}

func prepareAndRunTool(ctx context.Context, snapshot AgentContext, assistant ai.AssistantMessage, call ai.ToolCall, cfg AgentLoopConfig, onUpdate ToolUpdateCallback) (finalizedToolCall, error) {
	prepared, final, err := prepareToolCall(ctx, snapshot, assistant, call, cfg)
	if err != nil {
		return finalizedToolCall{}, err
	}
	if final != nil {
		return *final, nil
	}
	return executePreparedAndFinalize(ctx, snapshot, assistant, *prepared, cfg, onUpdate), nil
}

// prepareToolCall validates and preflights a single call. snapshot is the shared
// read-only batch context handed to the BeforeToolCall hook; it is never mutated.
func prepareToolCall(ctx context.Context, snapshot AgentContext, assistant ai.AssistantMessage, call ai.ToolCall, cfg AgentLoopConfig) (*preparedToolCall, *finalizedToolCall, error) {
	tool := findTool(snapshot.Tools, call.Name)
	if tool == nil {
		return nil, &finalizedToolCall{ToolCall: call, Result: createErrorToolResult("Tool " + call.Name + " not found"), IsError: true}, nil
	}
	rawArgs := cloneRaw(call.Arguments)
	if provider, ok := tool.(PrepareArgumentsProvider); ok {
		prepared, err := provider.PrepareArguments(rawArgs)
		if err != nil {
			return nil, &finalizedToolCall{ToolCall: call, Result: createErrorToolResult(err.Error()), IsError: true}, nil
		}
		rawArgs = cloneRaw(prepared)
	}
	preparedCall := call
	preparedCall.Arguments = cloneRaw(rawArgs)
	args, err := ai.ValidateToolArgumentsWithSchema(ai.Tool{Name: tool.Name(), Parameters: tool.Schema()}, ai.ToolCall{ID: preparedCall.ID, Name: preparedCall.Name, Arguments: rawArgs})
	if err != nil {
		return nil, &finalizedToolCall{ToolCall: preparedCall, Result: createErrorToolResult(err.Error()), IsError: true}, nil
	}
	executeArgs, err := json.Marshal(args)
	if err != nil {
		return nil, &finalizedToolCall{ToolCall: preparedCall, Result: createErrorToolResult(err.Error()), IsError: true}, nil
	}
	if cfg.BeforeToolCall != nil {
		res, err := cfg.BeforeToolCall(ctx, BeforeToolCallContext{
			AssistantMessage: assistant,
			ToolCall:         preparedCall,
			Args:             args,
			Context:          snapshot,
		})
		if err != nil {
			return nil, &finalizedToolCall{ToolCall: preparedCall, Result: createErrorToolResult(err.Error()), IsError: true}, nil
		}
		if ctx.Err() != nil {
			return nil, &finalizedToolCall{ToolCall: preparedCall, Result: createErrorToolResult("Operation aborted"), IsError: true}, nil
		}
		if res.Block {
			reason := res.Reason
			if reason == "" {
				reason = "Tool execution was blocked"
			}
			return nil, &finalizedToolCall{ToolCall: preparedCall, Result: createErrorToolResult(reason), IsError: true}, nil
		}
	}
	if ctx.Err() != nil {
		return nil, &finalizedToolCall{ToolCall: preparedCall, Result: createErrorToolResult("Operation aborted"), IsError: true}, nil
	}
	return &preparedToolCall{ToolCall: preparedCall, Tool: tool, Args: args, RawArgs: executeArgs}, nil, nil
}

func executePreparedAndFinalize(ctx context.Context, snapshot AgentContext, assistant ai.AssistantMessage, prepared preparedToolCall, cfg AgentLoopConfig, onUpdate ToolUpdateCallback) finalizedToolCall {
	result, isError := executePreparedToolCall(ctx, prepared, onUpdate)
	if cfg.AfterToolCall != nil {
		res, err := cfg.AfterToolCall(ctx, AfterToolCallContext{
			AssistantMessage: assistant,
			ToolCall:         prepared.ToolCall,
			Args:             prepared.Args,
			Result:           result,
			IsError:          isError,
			Context:          snapshot,
		})
		if err != nil {
			return finalizedToolCall{ToolCall: prepared.ToolCall, Result: createErrorToolResult(err.Error()), IsError: true}
		}
		// Field-by-field override mirroring TS finalizeExecutedToolCall. HasContent
		// and HasDetails gate the replacement so an explicit empty value can clear
		// the field, while leaving the flag false keeps the original.
		if res.HasContent {
			result.Content = res.Content
		}
		if res.HasDetails {
			result.Details = res.Details
		}
		if res.IsError != nil {
			isError = *res.IsError
		}
		if res.Terminate != nil {
			result.Terminate = *res.Terminate
		}
	}
	return finalizedToolCall{ToolCall: prepared.ToolCall, Result: result, IsError: isError}
}

func executePreparedToolCall(ctx context.Context, prepared preparedToolCall, onUpdate ToolUpdateCallback) (result AgentToolResult, isError bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = createErrorToolResult(fmt.Sprint(recovered))
			isError = true
		}
	}()
	result, err := prepared.Tool.Execute(ctx, prepared.RawArgs, onUpdate)
	if err != nil {
		return createErrorToolResult(err.Error()), true
	}
	return result, result.IsError
}

func emitToolResultMessage(ctx context.Context, msg ai.ToolResultMessage, emit AgentEventSink) error {
	if err := emit(ctx, MessageStartEvent{Message: msg}); err != nil {
		return err
	}
	return emit(ctx, MessageEndEvent{Message: msg})
}

func createToolResultMessage(final finalizedToolCall) ai.ToolResultMessage {
	return ai.NewToolResultMessage(final.ToolCall.ID, final.ToolCall.Name, final.Result.Content, final.Result.Details, final.IsError)
}

func createErrorToolResult(message string) AgentToolResult {
	return AgentToolResult{Content: ai.TextBlocks(message), Details: map[string]any{}}
}

func shouldTerminateToolBatch(finalized []finalizedToolCall) bool {
	if len(finalized) == 0 {
		return false
	}
	for _, final := range finalized {
		if !final.Result.Terminate {
			return false
		}
	}
	return true
}

func batchRequiresSequential(tools []AgentTool, calls []ai.ToolCall) bool {
	byName := map[string]AgentTool{}
	for _, tool := range tools {
		if tool != nil {
			byName[tool.Name()] = tool
		}
	}
	for _, call := range calls {
		if provider, ok := byName[call.Name].(ToolExecutionModeProvider); ok && provider.ToolExecutionMode() == ToolExecutionSequential {
			return true
		}
	}
	return false
}

func findTool(tools []AgentTool, name string) AgentTool {
	for _, tool := range tools {
		if tool != nil && tool.Name() == name {
			return tool
		}
	}
	return nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}
