package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
	"go.uber.org/goleak"
)

func TestToolExecutionModeProviderForcesSequentialBatch(t *testing.T) {
	slow := &countingTool{delay: 30 * time.Millisecond}
	fast := &countingTool{}
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
				{Type: "toolCall", ID: "slow", Name: "slow", Arguments: json.RawMessage(`{}`)},
				{Type: "toolCall", ID: "fast", Name: "fast", Arguments: json.RawMessage(`{}`)},
			}, ai.Usage{}, "toolUse")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{
			modeTool{name: "slow", countingTool: slow, mode: ToolExecutionSequential},
			namedTool{name: "fast", countingTool: fast},
		}},
		StreamFn: streamFn,
		ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) {
			return len(c.ToolResults) > 0, nil
		},
	})
	var endOrder []string
	a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
		if end, ok := ev.(ToolExecutionEndEvent); ok {
			endOrder = append(endOrder, end.ToolCallID)
		}
		return nil
	})
	if err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(endOrder, []string{"slow", "fast"}) {
		t.Fatalf("end order=%#v", endOrder)
	}
}

func TestPrepareArgumentsProviderRewritesBeforeValidationAndExecute(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	tool := &preparingTool{}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
				{Type: "toolCall", ID: "call-1", Name: "prepare", Arguments: json.RawMessage(`{}`)},
			}, ai.Usage{}, "toolUse")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{tool}},
		StreamFn:     streamFn,
		ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) {
			return len(c.ToolResults) > 0, nil
		},
	})
	if err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	if tool.executedPath != "README.md" {
		t.Fatalf("executedPath=%q prepared=%v", tool.executedPath, tool.prepared)
	}
}

func TestTerminateToolBatchStopsWithoutNextProviderTurn(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	calls := 0
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		calls++
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
				{Type: "toolCall", ID: "one", Name: "one", Arguments: json.RawMessage(`{}`)},
				{Type: "toolCall", ID: "two", Name: "two", Arguments: json.RawMessage(`{}`)},
			}, ai.Usage{}, "toolUse")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
		}()
		return stream
	}
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("go", nil)}, AgentContext{
		Tools: []AgentTool{terminatingTool{name: "one"}, terminatingTool{name: "two"}},
	}, AgentLoopConfig{Model: model, ToolExecution: ToolExecutionParallel}, nil, streamFn)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d", calls)
	}
	var results int
	for _, msg := range messages {
		if _, ok := ai.AsToolResultMessage(msg); ok {
			results++
		}
	}
	if results != 2 {
		t.Fatalf("tool result count=%d messages=%#v", results, messages)
	}
}

func TestBeforeToolCallErrorBecomesToolResult(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	tool := &countingTool{}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
				{Type: "toolCall", ID: "call-1", Name: "count", Arguments: json.RawMessage(`{}`)},
			}, ai.Usage{}, "toolUse")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
		}()
		return stream
	}
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("go", nil)}, AgentContext{
		Tools: []AgentTool{tool},
	}, AgentLoopConfig{
		Model: model,
		BeforeToolCall: func(ctx context.Context, c BeforeToolCallContext) (BeforeToolCallResult, error) {
			return BeforeToolCallResult{}, errors.New("preflight failed")
		},
		ShouldStopAfterTurn: func(ctx context.Context, c ShouldStopAfterTurnContext) (bool, error) {
			return len(c.ToolResults) > 0, nil
		},
	}, nil, streamFn)
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls.Load() != 0 {
		t.Fatalf("tool executed %d times", tool.calls.Load())
	}
	result, ok := ai.AsToolResultMessage(messages[len(messages)-1])
	if !ok || !result.IsError || ai.MessageText(result) != "preflight failed" {
		t.Fatalf("tool result=%#v ok=%v", messages[len(messages)-1], ok)
	}
}

func TestParallelToolUpdatesDoNotLeakWhenEmitFails(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("go.opencensus.io/stats/view.(*worker).start"))
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	leftDone := make(chan struct{})
	rightDone := make(chan struct{})
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
				{Type: "toolCall", ID: "left", Name: "left", Arguments: json.RawMessage(`{}`)},
				{Type: "toolCall", ID: "right", Name: "right", Arguments: json.RawMessage(`{}`)},
			}, ai.Usage{}, "toolUse")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{
			updateSpamTool{name: "left", done: leftDone},
			updateSpamTool{name: "right", done: rightDone},
		}},
		StreamFn: streamFn,
	})
	updateErr := errors.New("listener stopped")
	a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
		if _, ok := ev.(ToolExecutionUpdateEvent); ok {
			return updateErr
		}
		return nil
	})
	if err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	// Unified fatal sink-failure policy (item 6): an emit failure during tool
	// execution discards the partial batch instead of appending tool results and
	// then also emitting a synthetic failure turn. The transcript must therefore
	// carry no tool results and exactly one synthetic assistant error message.
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
		t.Fatalf("expected partial tool results discarded, got count=%d", toolResults)
	}
	if errorMessages != 1 {
		t.Fatalf("expected exactly one synthetic error message, got %d", errorMessages)
	}
	for name, done := range map[string]chan struct{}{"left": leftDone, "right": rightDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s tool did not exit", name)
		}
	}
}

func TestSingleToolParallelBatchUsesParallelArtifactPath(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	done := make(chan struct{})
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
				{Type: "toolCall", ID: "single", Name: "single", Arguments: json.RawMessage(`{}`)},
			}, ai.Usage{}, "toolUse")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
		}()
		return stream
	}
	a := NewAgent(AgentOptions{
		InitialState: AgentState{Model: model, Tools: []AgentTool{
			updateSpamTool{name: "single", done: done},
		}},
		StreamFn: streamFn,
	})
	updateErr := errors.New("listener stopped")
	a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
		if _, ok := ev.(ToolExecutionUpdateEvent); ok {
			return updateErr
		}
		return nil
	})
	if err := a.PromptMessage(context.Background(), ai.NewUserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("single tool did not exit")
	}
	// A single-tool batch still goes through the parallel artifact path, so the
	// same unified fatal sink-failure policy applies: the partial result is
	// discarded rather than appended alongside the synthetic failure turn.
	var toolResults int
	for _, msg := range a.State().Messages {
		if _, ok := ai.AsToolResultMessage(msg); ok {
			toolResults++
		}
	}
	if toolResults != 0 {
		t.Fatalf("expected partial tool result discarded, got count=%d", toolResults)
	}
}

type modeTool struct {
	name string
	*countingTool
	mode ToolExecutionMode
}

func (t modeTool) Name() string                         { return t.name }
func (t modeTool) Label() string                        { return t.name }
func (t modeTool) ToolExecutionMode() ToolExecutionMode { return t.mode }

type preparingTool struct {
	prepared     bool
	executedPath string
}

func (t *preparingTool) Name() string        { return "prepare" }
func (t *preparingTool) Label() string       { return "Prepare" }
func (t *preparingTool) Description() string { return "prepare arguments" }
func (t *preparingTool) Schema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"path"},
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
	}
}
func (t *preparingTool) PrepareArguments(json.RawMessage) (json.RawMessage, error) {
	t.prepared = true
	return json.RawMessage(`{"path":"README.md"}`), nil
}
func (t *preparingTool) Execute(ctx context.Context, raw json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return AgentToolResult{}, err
	}
	t.executedPath = args.Path
	return AgentToolResult{Content: ai.TextBlocks("ok")}, nil
}

type terminatingTool struct {
	name string
}

func (t terminatingTool) Name() string           { return t.name }
func (t terminatingTool) Label() string          { return t.name }
func (t terminatingTool) Description() string    { return t.name }
func (t terminatingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t terminatingTool) Execute(context.Context, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
	return AgentToolResult{Content: ai.TextBlocks(t.name), Terminate: true}, nil
}

type updateSpamTool struct {
	name string
	done chan struct{}
}

func (t updateSpamTool) Name() string           { return t.name }
func (t updateSpamTool) Label() string          { return t.name }
func (t updateSpamTool) Description() string    { return t.name }
func (t updateSpamTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t updateSpamTool) Execute(ctx context.Context, raw json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
	defer close(t.done)
	for i := 0; i < 100; i++ {
		if onUpdate != nil {
			onUpdate(AgentToolResult{Content: ai.TextBlocks(t.name)})
		}
		select {
		case <-ctx.Done():
			return AgentToolResult{}, ctx.Err()
		default:
		}
	}
	return AgentToolResult{Content: ai.TextBlocks("ok")}, nil
}
