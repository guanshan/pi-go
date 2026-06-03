package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

func TestHarnessHookUnsubscribeSkipsRemovedHandler(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{Model: ai.Model{Provider: "test", ID: "old", API: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	var removedCalled bool
	offRemoved := h.OnModelSelect(func(ctx context.Context, ev ModelSelectEvent) error {
		removedCalled = true
		return nil
	})
	var keptCalled int
	h.OnModelSelect(func(ctx context.Context, ev ModelSelectEvent) error {
		if ev.PreviousModel.ID != "old" || ev.Model.ID != "new" || ev.Source != ModelSelectSourceSet {
			t.Fatalf("event=%#v", ev)
		}
		keptCalled++
		return nil
	})
	h.OnModelSelect(nil)()
	offRemoved()
	if err := h.SetModel(ctx, ai.Model{Provider: "test", ID: "new", API: "test"}); err != nil {
		t.Fatal(err)
	}
	if removedCalled || keptCalled != 1 {
		t.Fatalf("removedCalled=%v keptCalled=%d", removedCalled, keptCalled)
	}
}

func TestHarnessContextHookReceivesCopiedMessages(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	source := []agent.AgentMessage{ai.NewUserMessage("original", nil)}
	h.OnContext(func(ctx context.Context, ev ContextEvent) (*ContextResult, error) {
		ev.Messages[0] = ai.NewUserMessage("mutated", nil)
		return &ContextResult{Messages: append(ev.Messages, ai.NewUserMessage("added", nil))}, nil
	})
	out, err := h.transformContext(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if ai.MessageText(source[0]) != "original" {
		t.Fatalf("source mutated: %#v", source)
	}
	if len(out) != 2 || ai.MessageText(out[0]) != "mutated" || ai.MessageText(out[1]) != "added" {
		t.Fatalf("out=%#v", out)
	}
}

func TestHarnessToolResultHooksChainPatches(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	h.OnToolResult(func(ctx context.Context, ev ToolResultEvent) (*ToolResultPatch, error) {
		if !ev.IsError || ai.MessageText(ai.NewToolResultMessage(ev.ToolCallID, ev.ToolName, ev.Content, ev.Details, ev.IsError)) != "raw" {
			t.Fatalf("first event=%#v", ev)
		}
		isError := false
		return &ToolResultPatch{
			Content: ai.TextBlocks("patched"),
			Details: &AnyValue{
				V: "detail",
			},
			IsError: &isError,
		}, nil
	})
	var secondSawPatch bool
	h.OnToolResult(func(ctx context.Context, ev ToolResultEvent) (*ToolResultPatch, error) {
		secondSawPatch = !ev.IsError &&
			ev.Details == "detail" &&
			ai.MessageText(ai.NewToolResultMessage(ev.ToolCallID, ev.ToolName, ev.Content, ev.Details, ev.IsError)) == "patched"
		terminate := true
		return &ToolResultPatch{Terminate: &terminate}, nil
	})
	patch, err := h.emitToolResult(ctx, ToolResultEvent{
		ToolCallID: "call-1",
		ToolName:   "lookup",
		Input:      map[string]any{"query": "pi"},
		Content:    ai.TextBlocks("raw"),
		Details:    "raw-detail",
		IsError:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if patch == nil || !secondSawPatch || patch.IsError == nil || *patch.IsError || patch.Terminate == nil || !*patch.Terminate {
		t.Fatalf("patch=%#v secondSawPatch=%v", patch, secondSawPatch)
	}
	if ai.MessageText(ai.NewToolResultMessage("call-1", "lookup", patch.Content, patch.Details.V, *patch.IsError)) != "patched" || patch.Details.V != "detail" {
		t.Fatalf("patch=%#v", patch)
	}
}

func TestHarnessEventSubscriptionUnsubscribeAndError(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var removedCalled bool
	offRemoved := h.SubscribeHarness(func(ctx context.Context, ev HarnessEvent) error {
		removedCalled = true
		return nil
	})
	h.SubscribeHarness(nil)()
	offRemoved()
	expected := errors.New("listener stopped")
	h.SubscribeHarness(func(ctx context.Context, ev HarnessEvent) error {
		return expected
	})
	err = h.emitHarness(ctx, SavePointEvent{})
	if !errors.Is(err, expected) || removedCalled {
		t.Fatalf("err=%v removedCalled=%v", err, removedCalled)
	}
}

// TestHarnessBeforeAgentStartHooksChainAndAppend locks the documented intentional
// divergence (docs/TS_COMPATIBILITY.md "Harness hook dispatch is chain/merge, not
// last-wins"): the Go harness runs every before_agent_start handler, chaining the
// accumulated SystemPrompt into each handler's event, taking the last non-empty
// SystemPrompt, and appending every handler's Messages in order. TS instead lets
// "the last non-undefined result win".
func TestHarnessBeforeAgentStartHooksChainAndAppend(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var secondSawFirstSystemPrompt bool
	h.OnBeforeAgentStart(func(ctx context.Context, ev BeforeAgentStartEvent) (*BeforeAgentStartResult, error) {
		return &BeforeAgentStartResult{
			SystemPrompt: "FIRST",
			Messages:     []agent.AgentMessage{ai.NewUserMessage("m1", nil)},
		}, nil
	})
	h.OnBeforeAgentStart(func(ctx context.Context, ev BeforeAgentStartEvent) (*BeforeAgentStartResult, error) {
		secondSawFirstSystemPrompt = ev.SystemPrompt == "FIRST"
		return &BeforeAgentStartResult{
			SystemPrompt: "SECOND",
			Messages:     []agent.AgentMessage{ai.NewUserMessage("m2", nil)},
		}, nil
	})
	out, err := h.emitBeforeAgentStart(ctx, BeforeAgentStartEvent{Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || !secondSawFirstSystemPrompt {
		t.Fatalf("out=%#v secondSawFirstSystemPrompt=%v", out, secondSawFirstSystemPrompt)
	}
	if out.SystemPrompt != "SECOND" {
		t.Fatalf("SystemPrompt = %q, want last-non-empty SECOND", out.SystemPrompt)
	}
	if len(out.Messages) != 2 || ai.MessageText(out.Messages[0]) != "m1" || ai.MessageText(out.Messages[1]) != "m2" {
		t.Fatalf("Messages = %#v, want appended [m1 m2]", out.Messages)
	}
}

// TestHarnessToolCallHookShortCircuitsOnBlock locks the documented intentional
// divergence: a blocking tool_call result short-circuits the remaining handlers
// rather than continuing to a "last result wins" merge.
func TestHarnessToolCallHookShortCircuitsOnBlock(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var secondCalled bool
	h.OnToolCall(func(ctx context.Context, ev ToolCallEvent) (*ToolCallResult, error) {
		return &ToolCallResult{Block: true, Reason: "denied"}, nil
	})
	h.OnToolCall(func(ctx context.Context, ev ToolCallEvent) (*ToolCallResult, error) {
		secondCalled = true
		return nil, nil
	})
	result, err := h.emitToolCall(ctx, ToolCallEvent{ToolCallID: "c1", ToolName: "bash"})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.Block || result.Reason != "denied" {
		t.Fatalf("result=%#v, want Block with reason denied", result)
	}
	if secondCalled {
		t.Fatal("second tool_call handler should not run after a block")
	}
}
