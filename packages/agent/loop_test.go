package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestRunAgentLoopEventSequence(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
		}()
		return stream
	}
	var got []string
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("hi", nil)}, AgentContext{}, AgentLoopConfig{Model: model}, func(ctx context.Context, ev AgentEvent) error {
		got = append(got, AgentEventType(ev))
		return nil
	}, streamFn)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"agent_start",
		"turn_start",
		"message_start",
		"message_end",
		"message_start",
		"message_end",
		"turn_end",
		"agent_end",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%#v", got)
	}
	if len(messages) != 2 || ai.MessageText(messages[1]) != "ok" {
		t.Fatalf("messages=%#v", messages)
	}
}

func TestRunAgentLoopTransformErrorEmitsFailureSequence(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	expected := errors.New("context hook failed")
	var got []string
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("hi", nil)}, AgentContext{}, AgentLoopConfig{
		Model: model,
		TransformContext: func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error) {
			return nil, expected
		},
	}, func(ctx context.Context, ev AgentEvent) error {
		got = append(got, AgentEventType(ev))
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"agent_start",
		"turn_start",
		"message_start",
		"message_end",
		"message_start",
		"message_end",
		"turn_end",
		"agent_end",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%#v", got)
	}
	if len(messages) != 2 {
		t.Fatalf("messages=%#v", messages)
	}
	assistant, ok := ai.AsAssistantMessage(messages[1])
	if !ok || assistant.StopReason != "error" || assistant.ErrorMessage == "" {
		t.Fatalf("assistant=%#v ok=%v", messages[1], ok)
	}
}

func TestRunAgentLoopReturnsSinkErrorOnlyWhenFailureEventsCannotBeDelivered(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	loopErr := errors.New("prepare failed")
	sinkErr := errors.New("sink failed")
	var sawFailureAssistant bool

	messages, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("hi", nil)}, AgentContext{}, AgentLoopConfig{
		Model: model,
		PrepareNextTurn: func(ctx context.Context, tc PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
			return nil, loopErr
		},
	}, func(ctx context.Context, ev AgentEvent) error {
		if end, ok := ev.(MessageEndEvent); ok {
			if assistant, ok := ai.AsAssistantMessage(end.Message); ok && assistant.ErrorMessage == loopErr.Error() {
				sawFailureAssistant = true
				return sinkErr
			}
		}
		return nil
	}, func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
		}()
		return stream
	})
	if !errors.Is(err, sinkErr) {
		t.Fatalf("err=%v", err)
	}
	if !sawFailureAssistant {
		t.Fatal("missing failure assistant")
	}
	if len(messages) != 2 {
		t.Fatalf("messages=%#v", messages)
	}
}

func TestRunAgentLoopContinue(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	initial := AgentContext{Messages: []AgentMessage{ai.NewUserMessage("continue", nil)}}
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		if len(agentContext.Messages) != 1 || ai.MessageText(agentContext.Messages[0]) != "continue" {
			t.Errorf("agentContext=%#v", agentContext)
		}
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("continued"), ai.Usage{}, "stop")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
		}()
		return stream
	}
	var got []string
	messages, err := RunAgentLoopContinue(context.Background(), initial, AgentLoopConfig{Model: model}, func(ctx context.Context, ev AgentEvent) error {
		got = append(got, AgentEventType(ev))
		return nil
	}, streamFn)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"agent_start",
		"turn_start",
		"message_start",
		"message_end",
		"turn_end",
		"agent_end",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%#v", got)
	}
	if len(messages) != 1 || ai.MessageText(messages[0]) != "continued" {
		t.Fatalf("messages=%#v", messages)
	}
}

func TestPrepareNextTurnUpdatesModelBeforeToolContinuation(t *testing.T) {
	firstModel := ai.Model{Provider: "faux", ID: "first", API: "faux"}
	secondModel := ai.Model{Provider: "faux", ID: "second", API: "faux"}
	tool := namedTool{name: "lookup", countingTool: &countingTool{}}
	calls := 0
	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		calls++
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			if calls == 1 {
				if model.ID != "first" {
					t.Errorf("first model=%s", model.ID)
				}
				msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{
					{Type: "toolCall", ID: "lookup", Name: "lookup", Arguments: json.RawMessage(`{}`)},
				}, ai.Usage{}, "toolUse")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
				return
			}
			if model.ID != "second" {
				t.Errorf("second model=%s", model.ID)
			}
			msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("done"), ai.Usage{}, "stop")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
		}()
		return stream
	}
	prepareCalls := 0
	stopCalls := 0
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("go", nil)}, AgentContext{
		Tools: []AgentTool{tool},
	}, AgentLoopConfig{
		Model: firstModel,
		PrepareNextTurn: func(ctx context.Context, tc PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
			prepareCalls++
			if prepareCalls == 1 {
				return &AgentLoopTurnUpdate{Model: &secondModel}, nil
			}
			return nil, nil
		},
		ShouldStopAfterTurn: func(ctx context.Context, tc ShouldStopAfterTurnContext) (bool, error) {
			stopCalls++
			return stopCalls == 2, nil
		},
	}, nil, streamFn)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || prepareCalls != 2 || stopCalls != 2 {
		t.Fatalf("calls=%d prepare=%d stop=%d messages=%#v", calls, prepareCalls, stopCalls, messages)
	}
	if ai.MessageText(messages[len(messages)-1]) != "done" {
		t.Fatalf("messages=%#v", messages)
	}
}

func TestRunAgentLoopFailureAfterPrepareNextTurnUsesLatestModel(t *testing.T) {
	firstModel := ai.Model{Provider: "faux", ID: "first", API: "faux"}
	secondModel := ai.Model{Provider: "faux", ID: "second", API: "faux"}
	transformCalls := 0
	steeringCalls := 0

	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("first"), ai.Usage{}, "stop")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
		}()
		return stream
	}

	messages, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("go", nil)}, AgentContext{}, AgentLoopConfig{
		Model: firstModel,
		TransformContext: func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error) {
			transformCalls++
			if transformCalls == 2 {
				return nil, errors.New("second turn failed")
			}
			return messages, nil
		},
		PrepareNextTurn: func(ctx context.Context, tc PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
			return &AgentLoopTurnUpdate{Model: &secondModel}, nil
		},
		GetSteeringMessages: func(ctx context.Context) ([]AgentMessage, error) {
			steeringCalls++
			if steeringCalls == 2 {
				return []AgentMessage{ai.NewUserMessage("again", nil)}, nil
			}
			return nil, nil
		},
	}, nil, streamFn)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 {
		t.Fatalf("messages=%#v", messages)
	}
	assistant, ok := ai.AsAssistantMessage(messages[len(messages)-1])
	if !ok {
		t.Fatalf("last=%#v", messages[len(messages)-1])
	}
	if assistant.Model != "second" || assistant.Provider != "faux" || assistant.API != "faux" {
		t.Fatalf("assistant identity=%s/%s/%s", assistant.API, assistant.Provider, assistant.Model)
	}
	if assistant.ErrorMessage != "second turn failed" {
		t.Fatalf("error=%q", assistant.ErrorMessage)
	}
}

func TestStreamAssistantResponseDoesNotCopyMessagesWithoutTransform(t *testing.T) {
	model := ai.Model{Provider: "faux", ID: "faux", API: "faux"}
	var prepareText string

	streamFn := func(ctx context.Context, model ai.Model, agentContext ai.Context, opts ai.StreamOptions) AssistantStream {
		stream := ai.NewAssistantMessageEventStream(4)
		go func() {
			msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
		}()
		return stream
	}

	_, err := RunAgentLoop(context.Background(), []AgentMessage{ai.NewUserMessage("go", nil)}, AgentContext{}, AgentLoopConfig{
		Model: model,
		ConvertToLLM: func(messages []AgentMessage) ([]ai.Message, error) {
			messages[0] = ai.NewUserMessage("mutated", nil)
			return messages, nil
		},
		PrepareNextTurn: func(ctx context.Context, tc PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
			prepareText = ai.MessageText(tc.Context.Messages[0])
			return nil, nil
		},
		ShouldStopAfterTurn: func(ctx context.Context, tc ShouldStopAfterTurnContext) (bool, error) {
			return true, nil
		},
	}, nil, streamFn)
	if err != nil {
		t.Fatal(err)
	}
	if prepareText != "mutated" {
		t.Fatalf("prepareText=%q", prepareText)
	}
}
