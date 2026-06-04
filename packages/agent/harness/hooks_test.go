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

// TestHarnessContextHooksLastWins locks TS lastResult semantics
// (agent-harness.ts:249-266, 430-433): every context handler sees the same
// ORIGINAL messages (handler 2 does NOT see handler 1's output), and the last
// handler returning non-nil messages wins outright.
func TestHarnessContextHooksLastWins(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	source := []agent.AgentMessage{ai.NewUserMessage("original", nil)}
	h.OnContext(func(ctx context.Context, ev ContextEvent) (*ContextResult, error) {
		return &ContextResult{Messages: []agent.AgentMessage{ai.NewUserMessage("from-first", nil)}}, nil
	})
	var secondSawOriginal bool
	h.OnContext(func(ctx context.Context, ev ContextEvent) (*ContextResult, error) {
		secondSawOriginal = len(ev.Messages) == 1 && ai.MessageText(ev.Messages[0]) == "original"
		return &ContextResult{Messages: []agent.AgentMessage{ai.NewUserMessage("from-second", nil)}}, nil
	})
	out, err := h.transformContext(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if !secondSawOriginal {
		t.Fatal("handler 2 must see the original messages, not handler 1's output")
	}
	if len(out) != 1 || ai.MessageText(out[0]) != "from-second" {
		t.Fatalf("out=%#v, want last-wins [from-second]", out)
	}
}

// TestHarnessToolResultHooksLastWins locks TS lastResult semantics
// (agent-harness.ts:249-266, 443-455): every tool_result handler sees the same
// ORIGINAL event (handler 2 does NOT see handler 1's patch), and the last handler
// returning a non-nil patch wins outright — patches are not merged across
// handlers.
func TestHarnessToolResultHooksLastWins(t *testing.T) {
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
			Details: &AnyValue{V: "detail"},
			IsError: &isError,
		}, nil
	})
	var secondSawOriginal bool
	h.OnToolResult(func(ctx context.Context, ev ToolResultEvent) (*ToolResultPatch, error) {
		// Handler 2 must observe the ORIGINAL event, NOT handler 1's patch.
		secondSawOriginal = ev.IsError &&
			ev.Details == "raw-detail" &&
			ai.MessageText(ai.NewToolResultMessage(ev.ToolCallID, ev.ToolName, ev.Content, ev.Details, ev.IsError)) == "raw"
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
	if !secondSawOriginal {
		t.Fatal("handler 2 must see the original (unpatched) event, not handler 1's patch")
	}
	// Last-wins: only handler 2's patch survives (just Terminate); handler 1's
	// Content/Details/IsError are NOT merged in.
	if patch == nil || patch.Terminate == nil || !*patch.Terminate {
		t.Fatalf("patch=%#v, want last patch (Terminate=true)", patch)
	}
	if patch.Content != nil || patch.Details != nil || patch.IsError != nil {
		t.Fatalf("patch=%#v, last-wins must not merge handler 1's fields", patch)
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

func stringPtr(s string) *string { return &s }

// TestHarnessBeforeAgentStartHooksLastWins locks the TS lastResult semantics
// (agent-harness.ts:249-266): every before_agent_start handler sees the same
// ORIGINAL event (handler 2 does NOT see handler 1's SystemPrompt), and the last
// handler returning a non-nil result wins outright — its Messages and
// SystemPrompt fully replace earlier handlers' (no chaining/appending).
func TestHarnessBeforeAgentStartHooksLastWins(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	var secondSawOriginalSystemPrompt bool
	h.OnBeforeAgentStart(func(ctx context.Context, ev BeforeAgentStartEvent) (*BeforeAgentStartResult, error) {
		return &BeforeAgentStartResult{
			SystemPrompt: stringPtr("FIRST"),
			Messages:     []agent.AgentMessage{ai.NewUserMessage("m1", nil)},
		}, nil
	})
	h.OnBeforeAgentStart(func(ctx context.Context, ev BeforeAgentStartEvent) (*BeforeAgentStartResult, error) {
		secondSawOriginalSystemPrompt = ev.SystemPrompt == "ORIG"
		return &BeforeAgentStartResult{
			SystemPrompt: stringPtr("SECOND"),
			Messages:     []agent.AgentMessage{ai.NewUserMessage("m2", nil)},
		}, nil
	})
	out, err := h.emitBeforeAgentStart(ctx, BeforeAgentStartEvent{Prompt: "go", SystemPrompt: "ORIG"})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || !secondSawOriginalSystemPrompt {
		t.Fatalf("out=%#v secondSawOriginalSystemPrompt=%v (handler 2 must see the original event, not handler 1's output)", out, secondSawOriginalSystemPrompt)
	}
	if out.SystemPrompt == nil || *out.SystemPrompt != "SECOND" {
		t.Fatalf("SystemPrompt = %v, want last-wins SECOND", out.SystemPrompt)
	}
	if len(out.Messages) != 1 || ai.MessageText(out.Messages[0]) != "m2" {
		t.Fatalf("Messages = %#v, want last-wins [m2] (not appended)", out.Messages)
	}
}

// TestHarnessBeforeAgentStartEmptySystemPromptClears verifies a handler returning
// SystemPrompt: "" (non-nil pointer to empty) is treated as an override (TS `??`
// adopts the empty string), distinct from nil = preserve.
func TestHarnessBeforeAgentStartEmptySystemPromptClears(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	h.OnBeforeAgentStart(func(ctx context.Context, ev BeforeAgentStartEvent) (*BeforeAgentStartResult, error) {
		return &BeforeAgentStartResult{SystemPrompt: stringPtr("")}, nil
	})
	out, err := h.emitBeforeAgentStart(ctx, BeforeAgentStartEvent{Prompt: "go", SystemPrompt: "ORIG"})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || out.SystemPrompt == nil || *out.SystemPrompt != "" {
		t.Fatalf("out=%#v, want a non-nil SystemPrompt pointer to \"\" (clear)", out)
	}
}

// TestHarnessToolCallHookLastWins locks TS lastResult semantics: every tool_call
// handler runs (no short-circuit on a block result) and the last non-nil result
// wins. Handler 1 blocks, handler 2 returns nil, so the block survives because it
// is the last (only) non-nil result; but handler 2 must still have been invoked.
func TestHarnessToolCallHookLastWins(t *testing.T) {
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
	if !secondCalled {
		t.Fatal("second tool_call handler must run even after a block (no short-circuit)")
	}
	if result == nil || !result.Block || result.Reason != "denied" {
		t.Fatalf("result=%#v, want last non-nil result (block denied) to win", result)
	}
}

// TestHarnessToolCallHookLastResultReplaces verifies that when a later handler
// returns a non-nil result, it replaces an earlier handler's block.
func TestHarnessToolCallHookLastResultReplaces(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	h.OnToolCall(func(ctx context.Context, ev ToolCallEvent) (*ToolCallResult, error) {
		return &ToolCallResult{Block: true, Reason: "denied"}, nil
	})
	h.OnToolCall(func(ctx context.Context, ev ToolCallEvent) (*ToolCallResult, error) {
		return &ToolCallResult{Block: false}, nil
	})
	result, err := h.emitToolCall(ctx, ToolCallEvent{ToolCallID: "c1", ToolName: "bash"})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Block {
		t.Fatalf("result=%#v, want last handler's non-blocking result to win", result)
	}
}
