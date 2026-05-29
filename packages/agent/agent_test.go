package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestAgentPromptFaux(t *testing.T) {
	dir := t.TempDir()
	registry := ai.NewModelRegistry(dir, ai.NewAuthStorage(dir))
	model, ok := registry.Find("faux", "faux")
	if !ok {
		t.Fatal("missing faux")
	}
	a := NewAgent(AgentOptions{
		Registry: registry,
		InitialState: AgentState{
			Model:         model,
			ThinkingLevel: ai.ThinkingOff,
		},
	})
	var sawEnd bool
	a.Subscribe(func(ctx context.Context, event AgentEvent) error {
		if _, ok := event.(AgentEndEvent); ok {
			sawEnd = true
		}
		return nil
	})
	messages, err := AwaitRun(context.Background(), a, func(a *Agent) error {
		return a.PromptMessage(context.Background(), ai.NewUserMessage("hello", nil))
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawEnd {
		t.Fatal("missing agent_end")
	}
	if got := ai.MessageText(messages[len(messages)-1]); got != "faux: hello" {
		t.Fatalf("text=%q", got)
	}
}

func TestAsAgentErrorUsesStandardErrorChains(t *testing.T) {
	base := agentError(AgentErrAuth, "missing token", nil)
	wrapped := fmt.Errorf("outer: %w", errors.Join(errors.New("side error"), base))

	var got *AgentError
	if !AsAgentError(wrapped, &got) {
		t.Fatal("AsAgentError did not find joined AgentError")
	}
	if got.Code != AgentErrAuth {
		t.Fatalf("code=%s", got.Code)
	}
}

func TestAgentRunErrorsAreNotRelabeledAsHook(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	listenerErr := errors.New("listener failed")
	a := NewAgent(AgentOptions{InitialState: AgentState{Model: model}})
	a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
		if _, ok := ev.(AgentStartEvent); ok {
			return listenerErr
		}
		return nil
	})

	err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil))
	var agentErr *AgentError
	if !AsAgentError(err, &agentErr) {
		t.Fatalf("err=%v", err)
	}
	if agentErr.Code != AgentErrUnknown {
		t.Fatalf("code=%s err=%v", agentErr.Code, err)
	}
}

func TestAgentRunPreservesAgentErrorCode(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	listenerErr := agentError(AgentErrAuth, "missing key", nil)
	a := NewAgent(AgentOptions{InitialState: AgentState{
		Model:    model,
		Messages: []AgentMessage{ai.NewUserMessage("go", nil)},
	}})
	a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
		if _, ok := ev.(AgentStartEvent); ok {
			return listenerErr
		}
		return nil
	})

	err := a.Continue(context.Background())
	var agentErr *AgentError
	if !AsAgentError(err, &agentErr) {
		t.Fatalf("err=%v", err)
	}
	if agentErr.Code != AgentErrAuth {
		t.Fatalf("code=%s err=%v", agentErr.Code, err)
	}
}

func TestAgentStatePendingToolCallsUseSetSemantics(t *testing.T) {
	a := NewAgent(AgentOptions{})
	if err := a.emit(context.Background(), ToolExecutionStartEvent{ToolCallID: "call-1"}); err != nil {
		t.Fatal(err)
	}
	if err := a.emit(context.Background(), ToolExecutionStartEvent{ToolCallID: "call-1"}); err != nil {
		t.Fatal(err)
	}
	state := a.State()
	if len(state.PendingToolCalls) != 1 {
		t.Fatalf("pending=%#v", state.PendingToolCalls)
	}
	delete(state.PendingToolCalls, "call-1")
	if _, ok := a.State().PendingToolCalls["call-1"]; !ok {
		t.Fatal("State returned pending tool map by reference")
	}
	if err := a.emit(context.Background(), ToolExecutionEndEvent{ToolCallID: "call-1"}); err != nil {
		t.Fatal(err)
	}
	if len(a.State().PendingToolCalls) != 0 {
		t.Fatalf("pending after end=%#v", a.State().PendingToolCalls)
	}
}

func TestAgentContinueAfterAssistantWithoutQueuedInputIsBusy(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	a := NewAgent(AgentOptions{InitialState: AgentState{
		Model: model,
		Messages: []AgentMessage{
			ai.NewAssistantMessageForModel(model, ai.TextBlocks("done"), ai.Usage{}, "stop"),
		},
	}})
	err := a.Continue(context.Background())
	var agentErr *AgentError
	if !AsAgentError(err, &agentErr) {
		t.Fatalf("err=%v", err)
	}
	if agentErr.Code != AgentErrBusy {
		t.Fatalf("code=%s err=%v", agentErr.Code, err)
	}
	if got := err.Error(); got == "" || got == "invalid_state: cannot continue from message role: assistant" {
		t.Fatalf("err=%q", got)
	}
}

type countingTool struct {
	calls atomic.Int32
	delay time.Duration
}

func (t *countingTool) Name() string           { return "count" }
func (t *countingTool) Label() string          { return "Count" }
func (t *countingTool) Description() string    { return "count tool" }
func (t *countingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *countingTool) Execute(ctx context.Context, raw json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
	t.calls.Add(1)
	if onUpdate != nil {
		onUpdate(AgentToolResult{Content: ai.TextBlocks("working")})
	}
	if t.delay > 0 {
		select {
		case <-time.After(t.delay):
		case <-ctx.Done():
			return AgentToolResult{}, ctx.Err()
		}
	}
	return AgentToolResult{Content: ai.TextBlocks("ok")}, nil
}

func TestAgentContinueAndToolExecution(t *testing.T) {
	tool := &countingTool{}
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			var msg ai.AssistantMessage
			if len(agentContext.Messages) > 0 && ai.MessageRole(agentContext.Messages[len(agentContext.Messages)-1]) == "toolResult" {
				msg = ai.NewAssistantMessage("faux", "faux", "faux", ai.TextBlocks("done"), ai.Usage{}, "stop")
			} else {
				msg = ai.NewAssistantMessage("faux", "faux", "faux", []ai.ContentBlock{{Type: "toolCall", ID: "1", Name: "count", Arguments: json.RawMessage(`{}`)}}, ai.Usage{}, "toolUse")
			}
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: msg.StopReason, Message: msg})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{tool}, Messages: []AgentMessage{ai.NewUserMessage("go", nil)}},
		StreamFn:     streamFn,
	})
	messages, err := AwaitRun(context.Background(), a, func(a *Agent) error {
		return a.Continue(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls.Load() != 1 {
		t.Fatalf("tool calls=%d", tool.calls.Load())
	}
	if got := ai.MessageText(messages[len(messages)-1]); got != "done" {
		t.Fatalf("last=%q", got)
	}
}

func TestAgentPromptForwardsAssistantStreamEvents(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	final := ai.NewAssistantMessage("faux", "faux", "faux", ai.TextBlocks("hello"), ai.Usage{}, "stop")
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(8)
		go func() {
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: ai.NewAssistantMessage("faux", "faux", "faux", nil, ai.Usage{}, "")})
			stream.Push(ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "he", Partial: ai.NewAssistantMessage("faux", "faux", "faux", ai.TextBlocks("he"), ai.Usage{}, "")})
			stream.Push(ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "llo", Partial: final})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: final})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model},
		StreamFn:     streamFn,
	})
	var deltas []string
	var starts, ends int
	a.Subscribe(func(ctx context.Context, event AgentEvent) error {
		switch ev := event.(type) {
		case MessageStartEvent:
			if ai.MessageRole(ev.Message) == "assistant" {
				starts++
			}
		case MessageUpdateEvent:
			if ev.AssistantEvent.Type == "text_delta" {
				deltas = append(deltas, ev.AssistantEvent.Delta)
			}
		case MessageEndEvent:
			if ai.MessageRole(ev.Message) == "assistant" {
				ends++
			}
		}
		return nil
	})
	if err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("starts=%d ends=%d", starts, ends)
	}
	if len(deltas) != 2 || deltas[0] != "he" || deltas[1] != "llo" {
		t.Fatalf("deltas=%#v", deltas)
	}
}

func TestToolHooksAndAgentLoopStream(t *testing.T) {
	tool := &countingTool{}
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessage("faux", "faux", "faux", []ai.ContentBlock{{Type: "toolCall", ID: "1", Name: "count", Arguments: json.RawMessage(`{}`)}}, ai.Usage{}, "toolUse")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
		}()
		return stream
	}
	blocked := false
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{tool}},
		StreamFn:     streamFn,
		BeforeToolCall: func(ctx context.Context, c BeforeToolCallContext) (BeforeToolCallResult, error) {
			blocked = c.ToolCall.Name == "count"
			return BeforeToolCallResult{Block: true, Reason: "nope"}, nil
		},
		AfterToolCall: func(ctx context.Context, c AfterToolCallContext) (AfterToolCallResult, error) {
			t.Fatal("after hook should not run for blocked preflight")
			return AfterToolCallResult{}, nil
		},
		ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) { return true, nil },
	})
	messages, err := AwaitRun(context.Background(), a, func(a *Agent) error {
		return a.PromptMessage(context.Background(), ai.NewUserMessage("hello", nil))
	})
	if err != nil {
		t.Fatal(err)
	}
	if !blocked || tool.calls.Load() != 0 {
		t.Fatalf("hook did not block tool; blocked=%v calls=%d", blocked, tool.calls.Load())
	}
	if ai.MessageRole(messages[len(messages)-1]) != "toolResult" {
		t.Fatalf("expected tool result, got %s", ai.MessageRole(messages[len(messages)-1]))
	}

	stream := AgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("x", nil)}, AgentContext{}, AgentLoopConfig{Model: model}, func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		out := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessage("faux", "faux", "faux", ai.TextBlocks("ok"), ai.Usage{}, "stop")
			out.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			out.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
		}()
		return out
	})
	var sawEnd bool
	for event := range stream.Events() {
		if _, ok := event.(AgentEndEvent); ok {
			sawEnd = true
		}
	}
	if !sawEnd || len(stream.Result()) == 0 {
		t.Fatal("bad event stream")
	}
}

func TestParallelToolEndsByCompletionOrderAndMessagesBySourceOrder(t *testing.T) {
	slow := &countingTool{delay: 30 * time.Millisecond}
	fast := &countingTool{}
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessage("faux", "faux", "faux", []ai.ContentBlock{
				{Type: "toolCall", ID: "slow", Name: "slow", Arguments: json.RawMessage(`{}`)},
				{Type: "toolCall", ID: "fast", Name: "fast", Arguments: json.RawMessage(`{}`)},
			}, ai.Usage{}, "toolUse")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{namedTool{"slow", slow}, namedTool{"fast", fast}}},
		StreamFn:     streamFn,
		ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) {
			return len(c.ToolResults) > 0, nil
		},
	})
	var endOrder, messageOrder []string
	a.Subscribe(func(ctx context.Context, event AgentEvent) error {
		switch ev := event.(type) {
		case ToolExecutionEndEvent:
			endOrder = append(endOrder, ev.ToolCallID)
		case MessageEndEvent:
			if result, ok := ai.AsToolResultMessage(ev.Message); ok {
				messageOrder = append(messageOrder, result.ToolCallID)
			}
		}
		return nil
	})
	if err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(endOrder, []string{"fast", "slow"}) {
		t.Fatalf("end order=%#v", endOrder)
	}
	if !reflect.DeepEqual(messageOrder, []string{"slow", "fast"}) {
		t.Fatalf("message order=%#v", messageOrder)
	}
}

type namedTool struct {
	name string
	*countingTool
}

func (t namedTool) Name() string  { return t.name }
func (t namedTool) Label() string { return t.name }
