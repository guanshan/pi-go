package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

func bptr(b bool) *bool { return &b }

func blocksText(blocks []ai.ContentBlock) string {
	var out string
	for _, b := range blocks {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out
}

// singleToolStream emits one tool call on the first turn and then stops once a
// tool result is present, so a single-tool exchange terminates naturally.
func singleToolStream(toolName string) StreamFn {
	return func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			var msg ai.AssistantMessage
			msgs := agentContext.Messages
			if len(msgs) > 0 && ai.MessageRole(msgs[len(msgs)-1]) == "toolResult" {
				msg = ai.NewAssistantMessageForModel(model, ai.TextBlocks("done"), ai.Usage{}, "stop")
			} else {
				msg = ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
					{Type: "toolCall", ID: "call-1", Name: toolName, Arguments: json.RawMessage(`{}`)},
				}, ai.Usage{}, "toolUse")
			}
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: msg.StopReason, Message: msg})
		}()
		return stream
	}
}

func runSingleTool(t *testing.T, tool AgentTool, cfg AgentLoopConfig) ([]AgentMessage, ToolExecutionEndEvent) {
	t.Helper()
	cfg.Model = ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	var endEv ToolExecutionEndEvent
	sawEnd := false
	emit := func(ctx context.Context, ev AgentEvent) error {
		if e, ok := ev.(ToolExecutionEndEvent); ok {
			endEv = e
			sawEnd = true
		}
		return nil
	}
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("go", nil)}, AgentContext{
		Tools: []AgentTool{tool},
	}, cfg, emit, singleToolStream(tool.Name()))
	if err != nil {
		t.Fatalf("RunAgentLoop error: %v", err)
	}
	if !sawEnd {
		t.Fatal("no tool_execution_end emitted")
	}
	return messages, endEv
}

func lastToolResult(t *testing.T, messages []AgentMessage) ai.ToolResultMessage {
	t.Helper()
	for i := len(messages) - 1; i >= 0; i-- {
		if r, ok := ai.AsToolResultMessage(messages[i]); ok {
			return r
		}
	}
	t.Fatal("no tool result message found")
	return ai.ToolResultMessage{}
}

// resultTool returns a fixed AgentToolResult (and optional error) to exercise
// the soft-error vs throw paths and the AfterToolCall overrides.
type resultTool struct {
	name   string
	result AgentToolResult
	err    error
}

func (t resultTool) Name() string           { return t.name }
func (t resultTool) Label() string          { return t.name }
func (t resultTool) Description() string    { return t.name }
func (t resultTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t resultTool) Execute(context.Context, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
	return t.result, t.err
}

// Item 1: a soft error (IsError=true, nil error) keeps the tool's Details and
// Terminate, while a thrown error discards them, and both surface isError=true
// identically to AfterToolCall, the tool_execution_end event and the message.
func TestSoftErrorPreservesDetailsAndTerminate(t *testing.T) {
	softDetails := map[string]any{"code": "soft"}
	var afterSawError bool
	soft := resultTool{
		name:   "soft",
		result: AgentToolResult{Content: ai.TextBlocks("partial"), Details: softDetails, IsError: true, Terminate: true},
	}
	messages, end := runSingleTool(t, soft, AgentLoopConfig{
		AfterToolCall: func(ctx context.Context, c AfterToolCallContext) (AfterToolCallResult, error) {
			afterSawError = c.IsError
			return AfterToolCallResult{}, nil
		},
	})
	if !end.IsError {
		t.Fatal("soft error: tool_execution_end isError should be true")
	}
	if !afterSawError {
		t.Fatal("soft error: AfterToolCall should observe isError=true")
	}
	if !end.Result.Terminate {
		t.Fatal("soft error: Terminate should be preserved on the result")
	}
	res := lastToolResult(t, messages)
	if !res.IsError {
		t.Fatal("soft error: tool result message isError should be true")
	}
	gotDetails, _ := res.Details.(map[string]any)
	if gotDetails["code"] != "soft" {
		t.Fatalf("soft error: details not preserved: %#v", res.Details)
	}
	// Terminate=true on the only call ends the batch, so the provider runs once
	// and no second assistant turn is appended.
	assistantTurns := 0
	for _, msg := range messages {
		if _, ok := ai.AsAssistantMessage(msg); ok {
			assistantTurns++
		}
	}
	if assistantTurns != 1 {
		t.Fatalf("soft-error terminate should stop after one turn, got %d assistant turns", assistantTurns)
	}
}

func TestThrownErrorDiscardsDetailsAndTerminate(t *testing.T) {
	throw := resultTool{
		name:   "throw",
		result: AgentToolResult{Content: ai.TextBlocks("ignored"), Details: map[string]any{"code": "soft"}, Terminate: true},
		err:    errors.New("boom"),
	}
	messages, end := runSingleTool(t, throw, AgentLoopConfig{
		ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) {
			return len(c.ToolResults) > 0, nil
		},
	})
	if !end.IsError {
		t.Fatal("thrown error: isError should be true")
	}
	if end.Result.Terminate {
		t.Fatal("thrown error: Terminate must be reset to false (createErrorToolResult)")
	}
	res := lastToolResult(t, messages)
	if ai.MessageText(res) != "boom" {
		t.Fatalf("thrown error: content should be the error message, got %q", ai.MessageText(res))
	}
	if m, ok := res.Details.(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("thrown error: details should be empty, got %#v", res.Details)
	}
}

// Item 3: HasContent / HasDetails gate explicit replacement, the IsError and
// Terminate pointers override when non-nil.
func TestAfterToolCallOverrideSemantics(t *testing.T) {
	base := func() resultTool {
		return resultTool{name: "tool", result: AgentToolResult{Content: ai.TextBlocks("orig"), Details: map[string]any{"d": 1}}}
	}

	t.Run("explicit empty content clears", func(t *testing.T) {
		_, end := runSingleTool(t, base(), AgentLoopConfig{
			AfterToolCall: func(ctx context.Context, c AfterToolCallContext) (AfterToolCallResult, error) {
				return AfterToolCallResult{HasContent: true, Content: nil}, nil
			},
			ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) { return true, nil },
		})
		if len(end.Result.Content) != 0 {
			t.Fatalf("HasContent override should clear content, got %#v", end.Result.Content)
		}
	})

	t.Run("content not provided keeps original", func(t *testing.T) {
		_, end := runSingleTool(t, base(), AgentLoopConfig{
			AfterToolCall: func(ctx context.Context, c AfterToolCallContext) (AfterToolCallResult, error) {
				// Content set but HasContent false: must be ignored.
				return AfterToolCallResult{Content: ai.TextBlocks("ignored")}, nil
			},
			ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) { return true, nil },
		})
		if blocksText(end.Result.Content) != "orig" {
			t.Fatalf("content should be untouched, got %q", blocksText(end.Result.Content))
		}
	})

	t.Run("isError pointer overrides", func(t *testing.T) {
		_, end := runSingleTool(t, base(), AgentLoopConfig{
			AfterToolCall: func(ctx context.Context, c AfterToolCallContext) (AfterToolCallResult, error) {
				return AfterToolCallResult{IsError: bptr(true)}, nil
			},
			ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) { return true, nil },
		})
		if !end.IsError {
			t.Fatal("isError pointer should override to true")
		}
	})

	t.Run("terminate pointer overrides", func(t *testing.T) {
		messages, end := runSingleTool(t, base(), AgentLoopConfig{
			AfterToolCall: func(ctx context.Context, c AfterToolCallContext) (AfterToolCallResult, error) {
				return AfterToolCallResult{Terminate: bptr(true)}, nil
			},
		})
		if !end.Result.Terminate {
			t.Fatal("terminate pointer should override to true")
		}
		assistantTurns := 0
		for _, msg := range messages {
			if _, ok := ai.AsAssistantMessage(msg); ok {
				assistantTurns++
			}
		}
		if assistantTurns != 1 {
			t.Fatalf("terminate override should stop after one turn, got %d", assistantTurns)
		}
	})

	t.Run("details replacement gated by HasDetails", func(t *testing.T) {
		_, end := runSingleTool(t, base(), AgentLoopConfig{
			AfterToolCall: func(ctx context.Context, c AfterToolCallContext) (AfterToolCallResult, error) {
				return AfterToolCallResult{HasDetails: true, Details: "replaced"}, nil
			},
			ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) { return true, nil },
		})
		if end.Result.Details != "replaced" {
			t.Fatalf("HasDetails override should replace details, got %#v", end.Result.Details)
		}
	})

	t.Run("details not provided keeps original", func(t *testing.T) {
		_, end := runSingleTool(t, base(), AgentLoopConfig{
			AfterToolCall: func(ctx context.Context, c AfterToolCallContext) (AfterToolCallResult, error) {
				return AfterToolCallResult{Details: "ignored"}, nil
			},
			ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) { return true, nil },
		})
		m, ok := end.Result.Details.(map[string]any)
		if !ok || m["d"] != 1 {
			t.Fatalf("details should be untouched, got %#v", end.Result.Details)
		}
	})
}

// Item 2: a listener error must not swallow the run's terminal signal; other
// subscribers (and AwaitRun) still observe agent_end.
func TestRunFailureStillEmitsTerminalEvents(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	a := NewAgent(AgentOptions{InitialState: AgentState{Model: model}})
	var failed atomic.Bool
	a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
		// Fail exactly once (on the very first event), then behave.
		if failed.CompareAndSwap(false, true) {
			return errors.New("listener boom")
		}
		return nil
	})
	messages, err := a.Prompt(context.Background(), ai.NewUserMessage("go", nil))
	if err == nil {
		t.Fatal("expected the sink failure to surface as an error")
	}
	if len(messages) == 0 {
		t.Fatal("AwaitRun should still capture terminal agent_end messages")
	}
	last, ok := ai.AsAssistantMessage(messages[len(messages)-1])
	if !ok || last.StopReason != "error" {
		t.Fatalf("expected a synthetic error assistant message, got %#v", messages[len(messages)-1])
	}
}

// Item 6: a sink failure while emitting tool-result messages discards the batch
// and yields a single synthetic failure turn, never tool results plus a
// synthetic error.
func TestParallelToolResultEmitFailureIsTerminal(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
				{Type: "toolCall", ID: "a", Name: "a", Arguments: json.RawMessage(`{}`)},
				{Type: "toolCall", ID: "b", Name: "b", Arguments: json.RawMessage(`{}`)},
			}, ai.Usage{}, "toolUse")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{
			resultTool{name: "a", result: AgentToolResult{Content: ai.TextBlocks("a")}},
			resultTool{name: "b", result: AgentToolResult{Content: ai.TextBlocks("b")}},
		}},
		StreamFn: streamFn,
	})
	// Fail when the first tool-result message is emitted.
	var failed atomic.Bool
	a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
		if start, ok := ev.(MessageStartEvent); ok {
			if _, isResult := ai.AsToolResultMessage(start.Message); isResult {
				if failed.CompareAndSwap(false, true) {
					return errors.New("sink down")
				}
			}
		}
		return nil
	})
	if err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	var toolResults, errorMessages int
	for _, msg := range a.State().Messages {
		if _, ok := ai.AsToolResultMessage(msg); ok {
			toolResults++
		}
		if assistant, ok := ai.AsAssistantMessage(msg); ok && assistant.ErrorMessage != "" {
			errorMessages++
		}
	}
	if toolResults != 0 {
		t.Fatalf("partial tool results must be discarded, got %d", toolResults)
	}
	if errorMessages != 1 {
		t.Fatalf("expected exactly one synthetic error turn, got %d", errorMessages)
	}
}

// Item 5: every hook in a tool batch sees one consistent pre-batch snapshot.
func TestToolBatchSharesConsistentSnapshot(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			var msg ai.AssistantMessage
			msgs := agentContext.Messages
			if len(msgs) > 0 && ai.MessageRole(msgs[len(msgs)-1]) == "toolResult" {
				msg = ai.NewAssistantMessageForModel(model, ai.TextBlocks("done"), ai.Usage{}, "stop")
			} else {
				msg = ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
					{Type: "toolCall", ID: "a", Name: "a", Arguments: json.RawMessage(`{}`)},
					{Type: "toolCall", ID: "b", Name: "b", Arguments: json.RawMessage(`{}`)},
				}, ai.Usage{}, "toolUse")
			}
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: msg.StopReason, Message: msg})
		}()
		return stream
	}
	var mu sync.Mutex
	var lengths []int
	record := func(c AgentContext) {
		mu.Lock()
		lengths = append(lengths, len(c.Messages))
		mu.Unlock()
	}
	_, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("go", nil)}, AgentContext{
		Tools: []AgentTool{
			resultTool{name: "a", result: AgentToolResult{Content: ai.TextBlocks("a")}},
			resultTool{name: "b", result: AgentToolResult{Content: ai.TextBlocks("b")}},
		},
	}, AgentLoopConfig{
		Model: model,
		BeforeToolCall: func(ctx context.Context, c BeforeToolCallContext) (BeforeToolCallResult, error) {
			record(c.Context)
			return BeforeToolCallResult{}, nil
		},
		AfterToolCall: func(ctx context.Context, c AfterToolCallContext) (AfterToolCallResult, error) {
			record(c.Context)
			return AfterToolCallResult{}, nil
		},
	}, func(ctx context.Context, ev AgentEvent) error { return nil }, streamFn)
	if err != nil {
		t.Fatal(err)
	}
	// 2 tools x (before + after) = 4 observations; all must see the same
	// pre-batch transcript length (user + assistant = 2), proving a single
	// shared snapshot rather than per-call copies that include in-flight results.
	if len(lengths) != 4 {
		t.Fatalf("expected 4 hook observations, got %d", len(lengths))
	}
	for _, l := range lengths {
		if l != 2 {
			t.Fatalf("hook saw inconsistent snapshot length %d (want 2): %#v", l, lengths)
		}
	}
}

// Item 4: Push and End racing across goroutines must not panic.
func TestEventStreamConcurrentPushAndEnd(t *testing.T) {
	stream := NewEventStream[int, string](4)
	consumed := make(chan struct{})
	go func() {
		for range stream.Events() {
		}
		close(consumed)
	}()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			stream.Push(n)
		}(i)
	}
	stream.End("done")
	wg.Wait()
	if got := stream.Result(); got != "done" {
		t.Fatalf("result=%q", got)
	}
	<-consumed
}

// Item 7: a run started with StartPrompt is observable and steerable before it
// finishes; a steered message lands in the transcript.
func TestStartPromptIsNonBlockingAndSteerable(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	toolReady := make(chan struct{})
	toolGo := make(chan struct{})
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			var msg ai.AssistantMessage
			msgs := agentContext.Messages
			if len(msgs) > 0 && ai.MessageRole(msgs[len(msgs)-1]) == "toolResult" {
				msg = ai.NewAssistantMessageForModel(model, ai.TextBlocks("done"), ai.Usage{}, "stop")
			} else {
				msg = ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
					{Type: "toolCall", ID: "wait", Name: "wait", Arguments: json.RawMessage(`{}`)},
				}, ai.Usage{}, "toolUse")
			}
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: msg.StopReason, Message: msg})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{gateTool{name: "wait", ready: toolReady, release: toolGo}}},
		StreamFn:     streamFn,
	})
	if err := a.StartPrompt(context.Background(), []AgentMessage{ai.NewUserMessage("go", nil)}); err != nil {
		t.Fatal(err)
	}
	// The call returned while the tool is still running.
	<-toolReady
	if !a.State().IsStreaming {
		t.Fatal("StartPrompt should leave the run active")
	}
	// Steer before the loop reaches its post-turn drain point.
	a.Steer(ai.NewUserMessage("steered", nil))
	close(toolGo)
	if err := a.WaitForIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.State().IsStreaming {
		t.Fatal("run should be idle after WaitForIdle")
	}
	steered := false
	for _, msg := range a.State().Messages {
		if ai.MessageRole(msg) == "user" && ai.MessageText(msg) == "steered" {
			steered = true
		}
	}
	if !steered {
		t.Fatal("steered message was not injected into the transcript")
	}
}

// Item 7: a started run can be aborted; WaitForIdle then returns.
func TestStartPromptAbort(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	toolReady := make(chan struct{})
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			var msg ai.AssistantMessage
			if ctx.Err() != nil {
				msg = ai.NewAssistantMessageForModel(model, nil, ai.Usage{}, "aborted")
			} else {
				msg = ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
					{Type: "toolCall", ID: "block", Name: "block", Arguments: json.RawMessage(`{}`)},
				}, ai.Usage{}, "toolUse")
			}
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: msg.StopReason, Message: msg})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{blockUntilCancelTool{name: "block", ready: toolReady}}},
		StreamFn:     streamFn,
	})
	if err := a.StartPrompt(context.Background(), []AgentMessage{ai.NewUserMessage("go", nil)}); err != nil {
		t.Fatal(err)
	}
	<-toolReady
	a.Abort()
	done := make(chan error, 1)
	go func() { done <- a.WaitForIdle(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForIdle returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForIdle did not return after Abort")
	}
	if a.State().IsStreaming {
		t.Fatal("run should be idle after abort")
	}
}

// Item 8: reading state and using the queue/abort APIs from a listener is safe;
// the configuration mutators panic by design and the run recovers into an error.
func TestListenerSafeAndGuardedMethods(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}

	stopStream := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
		}()
		return stream
	}

	t.Run("safe methods do not panic", func(t *testing.T) {
		a := NewAgent(AgentOptions{InitialState: AgentState{Model: model}, StreamFn: stopStream})
		var sawState, steered atomic.Bool
		a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
			// All of these are documented as listener-safe.
			_ = a.State()
			_ = a.HasQueuedMessages()
			sawState.Store(true)
			// Steer exactly once so the run still terminates.
			if _, ok := ev.(AgentStartEvent); ok {
				a.Steer(ai.NewUserMessage("steered", nil))
				steered.Store(true)
			}
			return nil
		})
		if err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil)); err != nil {
			t.Fatal(err)
		}
		if !sawState.Load() || !steered.Load() {
			t.Fatal("listener-safe methods did not run")
		}
		found := false
		for _, m := range a.State().Messages {
			if ai.MessageRole(m) == "user" && ai.MessageText(m) == "steered" {
				found = true
			}
		}
		if !found {
			t.Fatal("Steer from listener was not safely processed")
		}
	})

	t.Run("mutators panic and surface as a run error", func(t *testing.T) {
		a := NewAgent(AgentOptions{InitialState: AgentState{Model: model}, StreamFn: stopStream})
		a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
			if _, ok := ev.(AgentStartEvent); ok {
				a.SetModel(model) // guarded: panics during dispatch
			}
			return nil
		})
		err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil))
		var agentErr *AgentError
		if !AsAgentError(err, &agentErr) {
			t.Fatalf("expected AgentError, got %v", err)
		}
		if agentErr.Code != AgentErrUnknown || !strings.Contains(err.Error(), "cannot be called from an Agent event listener") {
			t.Fatalf("expected guarded-mutator panic to be reported, got %v", err)
		}
	})
}

// Item 9: the proxy decoder accumulates tool-call JSON in a side buffer; the
// persisted ContentBlock.Data field is never written.
func TestProxyDecoderKeepsDataFieldUntouched(t *testing.T) {
	dec := newProxyDecoder()
	partial := ai.NewAssistantMessage("api", "p", "m", nil, ai.Usage{}, "stop")
	steps := []ProxyAssistantMessageEvent{
		{Type: "toolcall_start", ContentIndex: 0, ID: "tc1", ToolName: "read"},
		{Type: "toolcall_delta", ContentIndex: 0, Delta: `{"path":`},
		{Type: "toolcall_delta", ContentIndex: 0, Delta: `"README.md"}`},
		{Type: "toolcall_end", ContentIndex: 0},
	}
	for _, step := range steps {
		if _, err := dec.process(step, &partial); err != nil {
			t.Fatalf("process(%s): %v", step.Type, err)
		}
		blocks := ai.MessageBlocks(partial)
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		if blocks[0].Data != "" {
			t.Fatalf("after %s the Data field was polluted: %q", step.Type, blocks[0].Data)
		}
	}
	blocks := ai.MessageBlocks(partial)
	var args map[string]string
	if err := json.Unmarshal(blocks[0].Arguments, &args); err != nil {
		t.Fatalf("final args: %v raw=%s", err, blocks[0].Arguments)
	}
	if args["path"] != "README.md" {
		t.Fatalf("args=%#v", args)
	}
}

type gateTool struct {
	name    string
	ready   chan struct{}
	release chan struct{}
}

func (t gateTool) Name() string           { return t.name }
func (t gateTool) Label() string          { return t.name }
func (t gateTool) Description() string    { return t.name }
func (t gateTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t gateTool) Execute(ctx context.Context, raw json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
	close(t.ready)
	select {
	case <-t.release:
	case <-ctx.Done():
		return AgentToolResult{}, ctx.Err()
	}
	return AgentToolResult{Content: ai.TextBlocks("ok")}, nil
}

type blockUntilCancelTool struct {
	name  string
	ready chan struct{}
}

func (t blockUntilCancelTool) Name() string           { return t.name }
func (t blockUntilCancelTool) Label() string          { return t.name }
func (t blockUntilCancelTool) Description() string    { return t.name }
func (t blockUntilCancelTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t blockUntilCancelTool) Execute(ctx context.Context, raw json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
	close(t.ready)
	<-ctx.Done()
	return AgentToolResult{}, ctx.Err()
}
