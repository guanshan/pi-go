package agent

import (
	"context"
	"fmt"

	"github.com/guanshan/pi-go/packages/ai"
)

type AgentEventSink func(ctx context.Context, ev AgentEvent) error

// RunAgentLoop emits all model/tool/hook failures as assistant failure messages.
// When the loop can deliver that failure sequence, the returned error is nil and
// callers observe the failure only through emitted events. A non-nil error means
// the event sink itself failed while events were being delivered.
func RunAgentLoop(ctx context.Context, prompts []AgentMessage, initial AgentContext, cfg AgentLoopConfig, emit AgentEventSink, streamFn StreamFn) ([]AgentMessage, error) {
	if emit == nil {
		emit = func(context.Context, AgentEvent) error { return nil }
	}
	cfg = withLoopDefaults(cfg)
	curr := copyAgentContext(initial)
	curr.Messages = append(curr.Messages, prompts...)
	newMessages := append([]AgentMessage(nil), prompts...)

	if err := emit(ctx, AgentStartEvent{}); err != nil {
		return newMessages, err
	}
	if err := emit(ctx, TurnStartEvent{}); err != nil {
		return newMessages, err
	}
	for _, prompt := range prompts {
		if err := emit(ctx, MessageStartEvent{Message: prompt}); err != nil {
			return newMessages, err
		}
		if err := emit(ctx, MessageEndEvent{Message: prompt}); err != nil {
			return newMessages, err
		}
	}

	if err := runLoop(ctx, &curr, &newMessages, &cfg, emit, streamFn); err != nil {
		if emitErr := emitLoopFailure(ctx, cfg.Model, err, &newMessages, emit); emitErr != nil {
			return newMessages, emitErr
		}
		return newMessages, nil
	}
	return newMessages, nil
}

// RunAgentLoopContinue has the same failure contract as RunAgentLoop: loop
// failures are represented in the event stream, while event-sink failures are
// returned because the stream could not be delivered.
func RunAgentLoopContinue(ctx context.Context, initial AgentContext, cfg AgentLoopConfig, emit AgentEventSink, streamFn StreamFn) ([]AgentMessage, error) {
	if len(initial.Messages) == 0 {
		return nil, fmt.Errorf("cannot continue: no messages in context")
	}
	if _, ok := ai.AsAssistantMessage(initial.Messages[len(initial.Messages)-1]); ok {
		return nil, fmt.Errorf("cannot continue from message role: assistant")
	}
	if emit == nil {
		emit = func(context.Context, AgentEvent) error { return nil }
	}
	cfg = withLoopDefaults(cfg)
	curr := copyAgentContext(initial)
	var newMessages []AgentMessage
	if err := emit(ctx, AgentStartEvent{}); err != nil {
		return newMessages, err
	}
	if err := emit(ctx, TurnStartEvent{}); err != nil {
		return newMessages, err
	}
	if err := runLoop(ctx, &curr, &newMessages, &cfg, emit, streamFn); err != nil {
		if emitErr := emitLoopFailure(ctx, cfg.Model, err, &newMessages, emit); emitErr != nil {
			return newMessages, emitErr
		}
		return newMessages, nil
	}
	return newMessages, nil
}

func AgentLoop(ctx context.Context, prompts []AgentMessage, initial AgentContext, cfg AgentLoopConfig, streamFn StreamFn) *EventStream[AgentEvent, []AgentMessage] {
	stream := NewEventStreamWithContext[AgentEvent, []AgentMessage](ctx, loopEventBuffer(cfg))
	go func() {
		messages, _ := RunAgentLoop(ctx, prompts, initial, cfg, func(ctx context.Context, ev AgentEvent) error {
			stream.Push(ev)
			return nil
		}, streamFn)
		stream.End(messages)
	}()
	return stream
}

func AgentLoopContinue(ctx context.Context, initial AgentContext, cfg AgentLoopConfig, streamFn StreamFn) *EventStream[AgentEvent, []AgentMessage] {
	stream := NewEventStreamWithContext[AgentEvent, []AgentMessage](ctx, loopEventBuffer(cfg))
	go func() {
		messages, _ := RunAgentLoopContinue(ctx, initial, cfg, func(ctx context.Context, ev AgentEvent) error {
			stream.Push(ev)
			return nil
		}, streamFn)
		stream.End(messages)
	}()
	return stream
}

func AgentLoopStream(ctx context.Context, prompts []AgentMessage, initial AgentContext, cfg AgentLoopConfig) *EventStream[AgentEvent, []AgentMessage] {
	return AgentLoop(ctx, prompts, initial, cfg, nil)
}

func loopEventBuffer(cfg AgentLoopConfig) int {
	if cfg.EventBuffer > 0 {
		return cfg.EventBuffer
	}
	return DefaultAgentLoopEventBuffer
}

func emitLoopFailure(ctx context.Context, model ai.Model, err error, newMessages *[]AgentMessage, emit AgentEventSink) error {
	stopReason := "error"
	if ctx.Err() != nil {
		stopReason = "aborted"
	}
	msg := ai.NewAssistantMessageForModel(model, nil, ai.Usage{}, stopReason)
	if err != nil {
		msg.ErrorMessage = err.Error()
	}
	if emitErr := emit(ctx, MessageStartEvent{Message: msg}); emitErr != nil {
		return emitErr
	}
	if emitErr := emit(ctx, MessageEndEvent{Message: msg}); emitErr != nil {
		return emitErr
	}
	*newMessages = append(*newMessages, msg)
	if emitErr := emit(ctx, TurnEndEvent{Message: msg, ToolResults: nil}); emitErr != nil {
		return emitErr
	}
	// On a thrown loop-internal failure (transform/convert/getApiKey/hook), the
	// terminal agent_end carries ONLY the synthesized failure message, matching
	// TS where the throw unwinds out of runAgentLoop and the wrapper
	// (Agent.runWithLifecycle / AgentHarness.executeTurn) catches it and emits
	// agent_end{messages:[failureMessage]} (agent.ts handleRunFailure /
	// agent-harness.ts emitRunFailure). The in-stream error/aborted path in
	// runLoop still emits the full transcript on both sides.
	return emit(ctx, AgentEndEvent{Messages: []AgentMessage{msg}})
}

func runLoop(ctx context.Context, curr *AgentContext, newMessages *[]AgentMessage, cfg *AgentLoopConfig, emit AgentEventSink, streamFn StreamFn) error {
	firstTurn := true
	pending, err := drainMessages(ctx, cfg.GetSteeringMessages)
	if err != nil {
		return err
	}

	for {
		hasMoreToolCalls := true
		for hasMoreToolCalls || len(pending) > 0 {
			if !firstTurn {
				if err := emit(ctx, TurnStartEvent{}); err != nil {
					return err
				}
			} else {
				firstTurn = false
			}

			for _, msg := range pending {
				if err := emit(ctx, MessageStartEvent{Message: msg}); err != nil {
					return err
				}
				if err := emit(ctx, MessageEndEvent{Message: msg}); err != nil {
					return err
				}
				curr.Messages = append(curr.Messages, msg)
				*newMessages = append(*newMessages, msg)
			}

			assistant, err := streamAssistantResponse(ctx, curr, *cfg, emit, streamFn)
			if err != nil {
				return err
			}
			*newMessages = append(*newMessages, assistant)

			if assistant.StopReason == "error" || assistant.StopReason == "aborted" {
				if err := emit(ctx, TurnEndEvent{Message: assistant, ToolResults: nil}); err != nil {
					return err
				}
				return emit(ctx, AgentEndEvent{Messages: append([]AgentMessage(nil), *newMessages...)})
			}

			var toolResults []ai.ToolResultMessage
			hasMoreToolCalls = false
			if len(toolCallsFromAssistant(assistant)) > 0 {
				batch, err := executeToolCalls(ctx, curr, assistant, *cfg, emit)
				if err != nil {
					// A sink failure during tool execution is terminal. The
					// partial batch is intentionally discarded so the transcript
					// is not left with tool results plus the synthetic failure
					// turn emitted by emitLoopFailure. This mirrors the TS source,
					// where a throw inside executeToolCalls unwinds before any
					// tool result is appended to the context.
					return err
				}
				toolResults = batch.Messages
				hasMoreToolCalls = !batch.Terminate
				for _, result := range toolResults {
					curr.Messages = append(curr.Messages, result)
					*newMessages = append(*newMessages, result)
				}
			}

			if err := emit(ctx, TurnEndEvent{Message: assistant, ToolResults: append([]ai.ToolResultMessage(nil), toolResults...)}); err != nil {
				return err
			}

			// One snapshot is shared between the PrepareNextTurn and
			// ShouldStopAfterTurn hooks. It is only recomputed when
			// PrepareNextTurn replaces the context, so the common case copies the
			// transcript once per turn instead of twice.
			turnSnapshot := copyAgentContext(*curr)
			if cfg.PrepareNextTurn != nil {
				update, err := cfg.PrepareNextTurn(ctx, ShouldStopAfterTurnContext{
					Message:     assistant,
					ToolResults: append([]ai.ToolResultMessage(nil), toolResults...),
					Context:     turnSnapshot,
					NewMessages: append([]AgentMessage(nil), *newMessages...),
				})
				if err != nil {
					return err
				}
				if update != nil {
					if update.Context != nil {
						*curr = copyAgentContext(*update.Context)
						turnSnapshot = copyAgentContext(*curr)
					}
					if update.Model != nil {
						cfg.Model = *update.Model
					}
					if update.ThinkingLevel != nil {
						if *update.ThinkingLevel == ai.ThinkingOff {
							cfg.Reasoning = ""
						} else {
							cfg.Reasoning = *update.ThinkingLevel
						}
					}
				}
			}

			if cfg.ShouldStopAfterTurn != nil {
				stop, err := cfg.ShouldStopAfterTurn(ctx, ShouldStopAfterTurnContext{
					Message:     assistant,
					ToolResults: append([]ai.ToolResultMessage(nil), toolResults...),
					Context:     turnSnapshot,
					NewMessages: append([]AgentMessage(nil), *newMessages...),
				})
				if err != nil {
					return err
				}
				if stop {
					return emit(ctx, AgentEndEvent{Messages: append([]AgentMessage(nil), *newMessages...)})
				}
			}

			pending, err = drainMessages(ctx, cfg.GetSteeringMessages)
			if err != nil {
				return err
			}
		}

		followUps, err := drainMessages(ctx, cfg.GetFollowUpMessages)
		if err != nil {
			return err
		}
		if len(followUps) > 0 {
			pending = followUps
			continue
		}
		break
	}

	return emit(ctx, AgentEndEvent{Messages: append([]AgentMessage(nil), *newMessages...)})
}

func streamAssistantResponse(ctx context.Context, curr *AgentContext, cfg AgentLoopConfig, emit AgentEventSink, streamFn StreamFn) (ai.AssistantMessage, error) {
	messages := curr.Messages
	if cfg.TransformContext != nil {
		messages = append([]AgentMessage(nil), curr.Messages...)
		transformed, err := cfg.TransformContext(ctx, messages)
		if err != nil {
			return ai.AssistantMessage{}, err
		}
		messages = transformed
	}
	llmMessages, err := cfg.ConvertToLLM(messages)
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	apiKey := ""
	if cfg.GetAPIKey != nil {
		apiKey, err = cfg.GetAPIKey(cfg.Model.Provider)
		if err != nil {
			return ai.AssistantMessage{}, err
		}
	}
	// Fall back to the static config APIKey when GetAPIKey is unset or yields an
	// empty string, matching TS `(config.getApiKey ? ... : undefined) || config.apiKey`
	// (agent-loop.ts:302).
	if apiKey == "" {
		apiKey = cfg.APIKey
	}
	if streamFn == nil {
		streamFn = DefaultStreamFn(nil)
	}
	stream := streamFn(ctx, cfg.Model, ai.Context{
		SystemPrompt: curr.SystemPrompt,
		Messages:     llmMessages,
		Tools:        toolsToAI(curr.Tools),
	}, ai.StreamOptions{
		APIKey:          apiKey,
		Reasoning:       cfg.Reasoning,
		SessionID:       cfg.SessionID,
		Transport:       cfg.Transport,
		ThinkingBudgets: valueThinkingBudgets(cfg.ThinkingBudgets),
		TimeoutMs:       cfg.TimeoutMs,
		IdleTimeoutMs:   cfg.IdleTimeoutMs,
		MaxRetries:      cfg.MaxRetries,
		MaxRetryDelayMs: cfg.MaxRetryDelayMs,
		OnPayload:       cfg.OnPayload,
		OnResponse:      cfg.OnResponse,
	})
	// The TS StreamFn contract forbids returning null, so this branch should be
	// unreachable for well-behaved stream functions. It is kept as a defensive
	// guard because a Go StreamFn can trivially return a nil interface; without
	// it the range over stream.Events() below would panic. The failure is
	// surfaced as a normal error assistant message rather than a panic.
	if stream == nil {
		msg := ai.NewAssistantMessageForModel(cfg.Model, nil, ai.Usage{}, "error")
		msg.ErrorMessage = "stream function returned nil"
		if err := emit(ctx, MessageStartEvent{Message: msg}); err != nil {
			return msg, err
		}
		if err := emit(ctx, MessageEndEvent{Message: msg}); err != nil {
			return msg, err
		}
		curr.Messages = append(curr.Messages, msg)
		return msg, nil
	}

	var partial ai.AssistantMessage
	started := false
	for ev := range stream.Events() {
		switch ev.Type {
		case "start":
			partial = ev.Partial
			curr.Messages = append(curr.Messages, partial)
			started = true
			if err := emit(ctx, MessageStartEvent{Message: partial}); err != nil {
				return partial, err
			}
		case "text_start", "text_delta", "text_end",
			"thinking_start", "thinking_delta", "thinking_end",
			"toolcall_start", "toolcall_delta", "toolcall_end":
			if !started {
				continue
			}
			partial = ev.Partial
			curr.Messages[len(curr.Messages)-1] = partial
			if err := emit(ctx, MessageUpdateEvent{Message: partial, AssistantEvent: ev}); err != nil {
				return partial, err
			}
		case "done", "error":
			final := stream.Result()
			if started {
				curr.Messages[len(curr.Messages)-1] = final
			} else {
				curr.Messages = append(curr.Messages, final)
				if err := emit(ctx, MessageStartEvent{Message: final}); err != nil {
					return final, err
				}
			}
			if err := emit(ctx, MessageEndEvent{Message: final}); err != nil {
				return final, err
			}
			return final, nil
		}
	}

	final := stream.Result()
	if started {
		curr.Messages[len(curr.Messages)-1] = final
	} else {
		curr.Messages = append(curr.Messages, final)
		if err := emit(ctx, MessageStartEvent{Message: final}); err != nil {
			return final, err
		}
	}
	if err := emit(ctx, MessageEndEvent{Message: final}); err != nil {
		return final, err
	}
	return final, nil
}

func withLoopDefaults(cfg AgentLoopConfig) AgentLoopConfig {
	if cfg.ConvertToLLM == nil {
		cfg.ConvertToLLM = defaultConvertToLLM
	}
	if cfg.ToolExecution == "" {
		cfg.ToolExecution = ToolExecutionParallel
	}
	return cfg
}

func copyAgentContext(ctx AgentContext) AgentContext {
	return AgentContext{
		SystemPrompt: ctx.SystemPrompt,
		Messages:     append([]AgentMessage(nil), ctx.Messages...),
		Tools:        append([]AgentTool(nil), ctx.Tools...),
	}
}

func drainMessages(ctx context.Context, f func(context.Context) ([]AgentMessage, error)) ([]AgentMessage, error) {
	if f == nil {
		return nil, nil
	}
	msgs, err := f(ctx)
	if err != nil {
		return nil, err
	}
	return append([]AgentMessage(nil), msgs...), nil
}

func valueThinkingBudgets(b *ai.ThinkingBudgets) ai.ThinkingBudgets {
	if b == nil {
		return ai.ThinkingBudgets{}
	}
	return *b
}

func toolCallsFromAssistant(message ai.AssistantMessage) []ai.ToolCall {
	var calls []ai.ToolCall
	for _, block := range message.Content {
		if block.Type == "toolCall" {
			calls = append(calls, ai.ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Arguments, ThoughtSignature: block.ThoughtSignature})
		}
	}
	return calls
}
