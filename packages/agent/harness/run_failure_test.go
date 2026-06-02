package harness

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

// failingAppendStorage wraps a MemoryStorage and fails AppendEntry for the first
// failLimit assistant MessageEntry writes (a negative failLimit fails every
// assistant append). Because the agent loop already synthesizes its own failure
// termination on a sink error (emitLoopFailure), the harness-level emitRunFailure
// path only runs once the loop's own failure emit also fails. Counting assistant
// appends lets a test drive both layers: the real assistant message_end and the
// loop's synthesized failure message_end both fail, while the harness's
// emitRunFailure message_end is allowed through.
type failingAppendStorage struct {
	*session.MemoryStorage
	err          error
	failLimit    int
	failed       int
	assistantHit int
}

func (s *failingAppendStorage) AppendEntry(ctx context.Context, entry session.Entry) error {
	if me, ok := entry.(session.MessageEntry); ok {
		if _, isAssistant := ai.AsAssistantMessage(me.Message); isAssistant {
			s.assistantHit++
			if s.failLimit < 0 || s.assistantHit <= s.failLimit {
				s.failed++
				return s.err
			}
		}
	}
	return s.MemoryStorage.AppendEntry(ctx, entry)
}

func inlineAssistantStreamFn(t *testing.T, text string) agent.StreamFn {
	t.Helper()
	return func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks(text), ai.Usage{}, "stop")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
		}()
		return stream
	}
}

// TestAgentHarnessPromptEmitsRunFailureSequenceOnSinkError verifies that a sink
// write failure mid-run triggers the synthesized failure-termination sequence:
// the harness emits message_start, message_end, turn_end, and agent_end for a
// synthesized failure assistant message so subscribers always observe a clean
// run end. Mirrors TS AgentHarness.emitRunFailure/executeTurn
// (src/harness/agent-harness.ts:539-551, 585-611).
func TestAgentHarnessPromptEmitsRunFailureSequenceOnSinkError(t *testing.T) {
	ctx := context.Background()
	mem, err := session.NewMemoryStorage(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sinkErr := errors.New("disk full")
	// Fail the first two assistant appends: the real assistant message_end and
	// the agent loop's own synthesized failure message_end. RunAgentLoop then
	// returns a non-nil error, exercising the harness's emitRunFailure path,
	// whose own (third) assistant append is allowed through.
	storage := &failingAppendStorage{MemoryStorage: mem, err: sinkErr, failLimit: 2}
	sess := session.New(storage)

	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	h, err := New(Options{
		Session:      sess,
		Model:        model,
		SystemPrompt: StaticSystemPrompt("system"),
		StreamFn:     inlineAssistantStreamFn(t, "answer"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Capture the tail of the event stream so we can assert the harness's
	// synthesized terminal sequence. The synthesized failure assistant message
	// carries a single empty text block (createFailureMessage), distinguishing
	// the harness's emitRunFailure sequence from the loop's own failure emit.
	type evt struct {
		kind string
		msg  ai.AssistantMessage
	}
	var events []evt
	h.Subscribe(func(ctx context.Context, ev agent.AgentEvent) error {
		e := evt{kind: agent.AgentEventType(ev)}
		switch v := ev.(type) {
		case agent.MessageStartEvent:
			if m, ok := ai.AsAssistantMessage(v.Message); ok {
				e.msg = m
			}
		case agent.MessageEndEvent:
			if m, ok := ai.AsAssistantMessage(v.Message); ok {
				e.msg = m
			}
		case agent.TurnEndEvent:
			if m, ok := ai.AsAssistantMessage(v.Message); ok {
				e.msg = m
			}
		}
		events = append(events, e)
		return nil
	})

	final, err := h.Prompt(ctx, "hello", PromptOptions{})
	if err != nil {
		t.Fatalf("expected harness emitRunFailure path to recover, got err=%v", err)
	}

	// The returned message is the harness-synthesized failure assistant message.
	if final.Role != "assistant" || final.StopReason != "error" {
		t.Fatalf("final=%#v", final)
	}
	if final.ErrorMessage == "" || !strings.Contains(final.ErrorMessage, "disk full") {
		t.Fatalf("expected error message to carry sink error, final.ErrorMessage=%q", final.ErrorMessage)
	}
	if final.Provider != "test" || final.Model != "m" || final.API != "test" {
		t.Fatalf("failure message missing model identity: %#v", final)
	}
	if len(final.Content) != 1 || final.Content[0].Type != "text" || final.Content[0].Text != "" {
		t.Fatalf("expected single empty text block from createFailureMessage, content=%#v", final.Content)
	}

	// Subscribers must observe a clean terminal sequence ending the run:
	// the LAST four agent events are message_start/message_end/turn_end/agent_end
	// for the harness-synthesized failure message.
	kinds := make([]string, len(events))
	for i, e := range events {
		kinds[i] = e.kind
	}
	if len(kinds) < 4 {
		t.Fatalf("expected at least four events, got %v", kinds)
	}
	tail := kinds[len(kinds)-4:]
	if tail[0] != "message_start" || tail[1] != "message_end" || tail[2] != "turn_end" || tail[3] != "agent_end" {
		t.Fatalf("expected terminal sequence message_start/message_end/turn_end/agent_end, got tail=%v (all=%v)", tail, kinds)
	}
	// The synthesized message_start/message_end/turn_end all carry the harness
	// failure message (single empty text block + error stopReason).
	for _, e := range events[len(events)-4 : len(events)-1] {
		if e.msg.StopReason != "error" || e.msg.ErrorMessage == "" {
			t.Fatalf("terminal %s did not carry harness failure message: %#v", e.kind, e.msg)
		}
		if len(e.msg.Content) != 1 || e.msg.Content[0].Type != "text" || e.msg.Content[0].Text != "" {
			t.Fatalf("terminal %s content=%#v (expected single empty text block)", e.kind, e.msg.Content)
		}
	}

	// Both the real assistant append and the loop's own failure append failed,
	// driving the harness emitRunFailure path.
	if storage.failed != 2 {
		t.Fatalf("expected exactly two failed assistant appends, got %d", storage.failed)
	}

	// agent_end reset the phase via the normal termination path, so the harness
	// is reusable: a subsequent prompt must not be rejected as busy.
	if _, err := h.Prompt(ctx, "again", PromptOptions{}); err != nil {
		t.Fatalf("expected harness reusable after run failure, got %v", err)
	}
}

// TestAgentHarnessPromptAggregatesWhenFailureReportingFails verifies the
// aggregate-error path: when failure reporting itself fails (the synthesized
// failure message_end also errors), Prompt returns an unknown AgentError whose
// cause joins both the original run error and the failure-reporting error.
// Mirrors TS executeTurn's AggregateError branch
// (src/harness/agent-harness.ts:603-609).
func TestAgentHarnessPromptAggregatesWhenFailureReportingFails(t *testing.T) {
	ctx := context.Background()
	mem, err := session.NewMemoryStorage(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sinkErr := errors.New("disk full")
	// Every assistant append fails, so the harness's emitRunFailure message_end
	// also fails and Prompt must surface an aggregate error.
	storage := &failingAppendStorage{MemoryStorage: mem, err: sinkErr, failLimit: -1}
	sess := session.New(storage)

	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	h, err := New(Options{
		Session:      sess,
		Model:        model,
		SystemPrompt: StaticSystemPrompt("system"),
		StreamFn:     inlineAssistantStreamFn(t, "answer"),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.Prompt(ctx, "hello", PromptOptions{})
	if err == nil {
		t.Fatal("expected aggregate error when failure reporting fails")
	}
	var agentErr *agent.AgentError
	if !agent.AsAgentError(err, &agentErr) {
		t.Fatalf("expected *agent.AgentError, got %T: %v", err, err)
	}
	if agentErr.Code != agent.AgentErrUnknown {
		t.Fatalf("expected unknown code, got %q", agentErr.Code)
	}
	if !errors.Is(err, sinkErr) {
		t.Fatalf("expected joined cause to include original sink error, got %v", err)
	}

	// The run must still have been released (phase reset via Prompt's defer), so
	// a subsequent prompt is not rejected as busy.
	if _, err := h.Prompt(ctx, "again", PromptOptions{}); err != nil {
		var busy *agent.AgentError
		if agent.AsAgentError(err, &busy) && busy.Code == agent.AgentErrBusy {
			t.Fatalf("expected harness released after aggregate failure, got busy: %v", err)
		}
	}
}
