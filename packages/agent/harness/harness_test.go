package harness

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

func TestAgentHarnessPromptPersistsMessagesAndForwardsAuth(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	var sawAPIKey, sawHeader bool
	h, err := New(Options{
		Session:      sess,
		Model:        model,
		SystemPrompt: StaticSystemPrompt("system"),
		StreamOptions: StreamOptions{
			Headers: map[string]string{"x-base": "base"},
		},
		GetAPIKeyAndHeaders: func(ctx context.Context, model ai.Model) (APIKeyAndHeaders, error) {
			return APIKeyAndHeaders{APIKey: "secret", Headers: map[string]string{"x-auth": "ok"}}, nil
		},
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			sawAPIKey = opts.APIKey == "secret"
			sawHeader = opts.Headers["x-base"] == "base" && opts.Headers["x-auth"] == "ok"
			if aiCtx.SystemPrompt != "system" || len(aiCtx.Messages) != 1 {
				t.Fatalf("context=%#v", aiCtx)
			}
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("answer"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawEnd bool
	h.Subscribe(func(ctx context.Context, ev agent.AgentEvent) error {
		if _, ok := ev.(agent.AgentEndEvent); ok {
			sawEnd = true
		}
		return nil
	})
	final, err := h.Prompt(ctx, "hello", PromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ai.MessageText(final) != "answer" || !sawAPIKey || !sawHeader || !sawEnd {
		t.Fatalf("final=%#v api=%v header=%v end=%v", final, sawAPIKey, sawHeader, sawEnd)
	}
	built, err := sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 2 || ai.MessageText(built.Messages[0]) != "hello" || ai.MessageText(built.Messages[1]) != "answer" {
		t.Fatalf("messages=%#v", built.Messages)
	}
}

func TestAgentHarnessContextAndProviderHooks(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	var sawContext, sawRequest, sawPayload, sawResponse bool
	h, err := New(Options{
		Model:        model,
		SystemPrompt: StaticSystemPrompt("system"),
		StreamOptions: StreamOptions{
			Headers:  map[string]string{"x-base": "base"},
			Metadata: map[string]any{"base": "yes"},
		},
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			if got := len(aiCtx.Messages); got != 2 {
				t.Fatalf("messages len=%d", got)
			}
			if ai.MessageText(aiCtx.Messages[1]) != "context hook" {
				t.Fatalf("messages=%#v", aiCtx.Messages)
			}
			if opts.Headers["x-base"] != "base" || opts.Headers["x-hook"] != "hooked" || opts.Metadata["hook"] != "meta" {
				t.Fatalf("opts headers=%#v metadata=%#v", opts.Headers, opts.Metadata)
			}
			payload, err := opts.OnPayload(map[string]any{"base": true}, model)
			if err != nil {
				t.Fatal(err)
			}
			if payload.(map[string]any)["hooked"] != true {
				t.Fatalf("payload=%#v", payload)
			}
			if err := opts.OnResponse(ai.ProviderResponse{Status: 202, Headers: map[string]string{"x-trace": "ok"}}, model); err != nil {
				t.Fatal(err)
			}
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("answer"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.OnContext(func(ctx context.Context, ev ContextEvent) (*ContextResult, error) {
		sawContext = true
		return &ContextResult{Messages: append(ev.Messages, ai.NewUserMessage("context hook", nil))}, nil
	})
	h.OnBeforeProviderRequest(func(ctx context.Context, ev BeforeProviderRequestEvent) (*BeforeProviderRequestResult, error) {
		sawRequest = true
		if ev.Model.ID != "m" || ev.StreamOptions.Headers["x-base"] != "base" {
			t.Fatalf("request event=%#v", ev)
		}
		header := "hooked"
		return &BeforeProviderRequestResult{StreamOptions: &StreamOptionsPatch{
			Headers:  map[string]*string{"x-hook": &header},
			Metadata: map[string]*AnyValue{"hook": {V: "meta"}},
		}}, nil
	})
	h.OnBeforeProviderPayload(func(ctx context.Context, ev BeforeProviderPayloadEvent) (*BeforeProviderPayloadResult, error) {
		sawPayload = true
		body := ev.Payload.(map[string]any)
		body["hooked"] = true
		return &BeforeProviderPayloadResult{Payload: body}, nil
	})
	h.OnAfterProviderResponse(func(ctx context.Context, ev AfterProviderResponseEvent) error {
		sawResponse = ev.Status == 202 && ev.Headers["x-trace"] == "ok"
		return nil
	})
	if _, err := h.Prompt(ctx, "hello", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	if !sawContext || !sawRequest || !sawPayload || !sawResponse {
		t.Fatalf("hooks context=%v request=%v payload=%v response=%v", sawContext, sawRequest, sawPayload, sawResponse)
	}
}

func TestAgentHarnessToolCallHookCanBlock(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	calls := 0
	var sawBlock bool
	h, err := New(Options{
		Model:        model,
		SystemPrompt: StaticSystemPrompt("system"),
		Tools: []agent.AgentTool{harnessTestTool{execute: func(context.Context, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
			t.Fatal("tool should have been blocked")
			return agent.AgentToolResult{}, nil
		}}},
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			calls++
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				if calls == 1 {
					msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{{
						Type:      "toolCall",
						ID:        "call-1",
						Name:      "lookup",
						Arguments: json.RawMessage(`{"query":"pi"}`),
					}}, ai.Usage{}, "toolUse")
					stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
					stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
					return
				}
				toolResult, ok := ai.AsToolResultMessage(aiCtx.Messages[len(aiCtx.Messages)-1])
				if !ok || !toolResult.IsError || ai.MessageText(toolResult) != "blocked by hook" {
					t.Errorf("tool result=%#v ok=%v", aiCtx.Messages[len(aiCtx.Messages)-1], ok)
				}
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("done"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.OnToolCall(func(ctx context.Context, ev ToolCallEvent) (*ToolCallResult, error) {
		sawBlock = ev.ToolCallID == "call-1" && ev.ToolName == "lookup" && ev.Input["query"] == "pi"
		return &ToolCallResult{Block: true, Reason: "blocked by hook"}, nil
	})
	final, err := h.Prompt(ctx, "hello", PromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ai.MessageText(final) != "done" || !sawBlock {
		t.Fatalf("final=%#v sawBlock=%v", final, sawBlock)
	}
}

func TestAgentHarnessToolResultHookCanPatch(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	calls := 0
	var sawResult bool
	h, err := New(Options{
		Model:        model,
		SystemPrompt: StaticSystemPrompt("system"),
		Tools: []agent.AgentTool{harnessTestTool{execute: func(context.Context, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: ai.TextBlocks("raw"), IsError: true}, nil
		}}},
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			calls++
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				if calls == 1 {
					msg := ai.NewAssistantMessageForModel(model, []ai.ContentBlock{{
						Type:      "toolCall",
						ID:        "call-1",
						Name:      "lookup",
						Arguments: json.RawMessage(`{"query":"pi"}`),
					}}, ai.Usage{}, "toolUse")
					stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
					stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
					return
				}
				toolResult, ok := ai.AsToolResultMessage(aiCtx.Messages[len(aiCtx.Messages)-1])
				if !ok || toolResult.IsError || ai.MessageText(toolResult) != "patched" {
					t.Errorf("tool result=%#v ok=%v", aiCtx.Messages[len(aiCtx.Messages)-1], ok)
				}
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("done"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.OnToolResult(func(ctx context.Context, ev ToolResultEvent) (*ToolResultPatch, error) {
		sawResult = ev.ToolCallID == "call-1" && ev.Input["query"] == "pi" && ev.IsError && ai.MessageText(ai.NewToolResultMessage(ev.ToolCallID, ev.ToolName, ev.Content, nil, ev.IsError)) == "raw"
		ok := false
		return &ToolResultPatch{Content: ai.TextBlocks("patched"), IsError: &ok}, nil
	})
	final, err := h.Prompt(ctx, "hello", PromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ai.MessageText(final) != "done" || !sawResult {
		t.Fatalf("final=%#v sawResult=%v", final, sawResult)
	}
}

func TestAgentHarnessSelectionHooks(t *testing.T) {
	ctx := context.Background()
	initial := ai.Model{Provider: "test", ID: "old", API: "test"}
	h, err := New(Options{Model: initial, ThinkingLevel: ai.ThinkingLow})
	if err != nil {
		t.Fatal(err)
	}
	var sawModel, sawThinking, sawResources bool
	h.OnModelSelect(func(ctx context.Context, ev ModelSelectEvent) error {
		sawModel = ev.PreviousModel.ID == "old" && ev.Model.ID == "new" && ev.Source == ModelSelectSourceSet
		return nil
	})
	h.OnThinkingLevelSelect(func(ctx context.Context, ev ThinkingLevelSelectEvent) error {
		sawThinking = ev.PreviousLevel == ai.ThinkingLow && ev.Level == ai.ThinkingHigh
		return nil
	})
	h.OnResourcesUpdate(func(ctx context.Context, ev ResourcesUpdateEvent) error {
		sawResources = len(ev.PreviousResources.Skills) == 0 && len(ev.Resources.Skills) == 1 && ev.Resources.Skills[0].Name == "skill"
		return nil
	})
	if err := h.SetModel(ctx, ai.Model{Provider: "test", ID: "new", API: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := h.SetThinkingLevel(ctx, ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	if err := h.SetResources(ctx, Resources{Skills: []Skill{{Name: "skill"}}}); err != nil {
		t.Fatal(err)
	}
	if !sawModel || !sawThinking || !sawResources {
		t.Fatalf("hooks model=%v thinking=%v resources=%v", sawModel, sawThinking, sawResources)
	}
}

func TestAgentHarnessDefersSessionWritesDuringTurn(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldModel := ai.Model{Provider: "test", ID: "old", API: "test"}
	newModel := ai.Model{Provider: "test", ID: "new", API: "test"}
	var h *AgentHarness
	h, err = New(Options{
		Session:      sess,
		Model:        oldModel,
		SystemPrompt: StaticSystemPrompt("system"),
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			if model.ID != "old" {
				t.Fatalf("current turn model=%#v", model)
			}
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("answer"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.OnContext(func(ctx context.Context, ev ContextEvent) (*ContextResult, error) {
		if err := h.SetModel(ctx, newModel); err != nil {
			return nil, err
		}
		built, err := sess.BuildContext(ctx)
		if err != nil {
			return nil, err
		}
		if built.Model != nil {
			t.Fatalf("model change leaked into current turn context: %#v", built.Model)
		}
		return nil, nil
	})
	if _, err := h.Prompt(ctx, "hello", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	built, err := sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if built.Model == nil || built.Model.ModelID != "new" {
		t.Fatalf("model change was not flushed: %#v", built.Model)
	}
}

func TestAgentHarnessHarnessEvents(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	calls := 0
	h, err := New(Options{
		Model:        model,
		SystemPrompt: StaticSystemPrompt("system"),
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			calls++
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				if calls == 1 {
					texts := []string{}
					for _, msg := range aiCtx.Messages {
						texts = append(texts, ai.MessageText(msg))
					}
					want := []string{"next", "main"}
					if len(texts) != len(want) {
						t.Errorf("texts=%#v", texts)
					} else {
						for i := range want {
							if texts[i] != want[i] {
								t.Errorf("texts=%#v", texts)
								break
							}
						}
					}
					msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("first"), ai.Usage{}, "stop")
					stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
					stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
					return
				}
				t.Errorf("unexpected extra call %d", calls)
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var queueEvents []QueueUpdateEvent
	var savePoints []SavePointEvent
	var settled []SettledEvent
	h.SubscribeHarness(func(ctx context.Context, ev HarnessEvent) error {
		switch e := ev.(type) {
		case QueueUpdateEvent:
			queueEvents = append(queueEvents, e)
		case SavePointEvent:
			savePoints = append(savePoints, e)
		case SettledEvent:
			settled = append(settled, e)
		}
		return nil
	})
	if err := h.NextTurn(ctx, "next", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	final, err := h.Prompt(ctx, "main", PromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ai.MessageText(final) != "first" || calls != 1 {
		t.Fatalf("final=%#v calls=%d", final, calls)
	}
	if len(queueEvents) < 2 {
		t.Fatalf("queueEvents=%#v", queueEvents)
	}
	if len(queueEvents[0].NextTurn) != 1 || ai.MessageText(queueEvents[0].NextTurn[0]) != "next" {
		t.Fatalf("first queue event=%#v", queueEvents[0])
	}
	lastQueue := queueEvents[len(queueEvents)-1]
	if len(lastQueue.Steer) != 0 || len(lastQueue.FollowUp) != 0 || len(lastQueue.NextTurn) != 0 {
		t.Fatalf("last queue event=%#v", lastQueue)
	}
	if len(savePoints) != 1 || savePoints[0].HadPendingMutations {
		t.Fatalf("savePoints=%#v", savePoints)
	}
	if len(settled) != 1 || settled[0].NextTurnCount != 0 {
		t.Fatalf("settled=%#v", settled)
	}
}

func TestAgentHarnessSteerAndFollowUpRequireActiveRun(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{Model: ai.Model{Provider: "test", ID: "m", API: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	var agentErr *agent.AgentError
	if err := h.Steer(ctx, "steer", PromptOptions{}); !errors.As(err, &agentErr) || agentErr.Code != agent.AgentErrInvalidState {
		t.Fatalf("steer err=%#v", err)
	}
	agentErr = nil
	if err := h.FollowUp(ctx, "follow", PromptOptions{}); !errors.As(err, &agentErr) || agentErr.Code != agent.AgentErrInvalidState {
		t.Fatalf("follow up err=%#v", err)
	}
	if err := h.NextTurn(ctx, "next", PromptOptions{}); err != nil {
		t.Fatalf("next turn err=%v", err)
	}
}

func TestAgentHarnessAbortClearsQueuedMessages(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	started := make(chan struct{})
	calls := 0
	h, err := New(Options{
		Model: model,
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			calls++
			if calls == 1 && len(aiCtx.Messages) != 1 {
				t.Fatalf("messages=%#v", aiCtx.Messages)
			}
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				if calls == 1 {
					close(started)
					<-ctx.Done()
					msg := ai.NewAssistantMessageForModel(model, nil, ai.Usage{}, "aborted")
					stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: msg})
					return
				}
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("answer"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var aborts []AbortEvent
	h.SubscribeHarness(func(ctx context.Context, ev HarnessEvent) error {
		if abort, ok := ev.(AbortEvent); ok {
			aborts = append(aborts, abort)
		}
		return nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := h.Prompt(ctx, "main", PromptOptions{})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start")
	}
	if err := h.Steer(ctx, "steer", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := h.FollowUp(ctx, "follow", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	result, err := h.Abort(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Aborted || len(aborts) != 1 || len(aborts[0].ClearedSteer) != 1 || len(aborts[0].ClearedFollowUp) != 1 {
		t.Fatalf("result=%#v aborts=%#v", result, aborts)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt did not finish")
	}
	if _, err := h.Prompt(ctx, "after abort", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentHarnessAbortEmitsAfterRunIsIdle(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	started := make(chan struct{})
	h, err := New(Options{
		Model: model,
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			stream := ai.NewAssistantMessageEventStream(4)
			close(started)
			go func() {
				<-ctx.Done()
				msg := ai.NewAssistantMessageForModel(model, nil, ai.Usage{}, "aborted")
				stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	order := make(chan string, 2)
	h.Subscribe(func(ctx context.Context, ev agent.AgentEvent) error {
		if _, ok := ev.(agent.AgentEndEvent); ok {
			order <- "agent_end"
		}
		return nil
	})
	h.SubscribeHarness(func(ctx context.Context, ev HarnessEvent) error {
		if _, ok := ev.(AbortEvent); ok {
			order <- "abort"
		}
		return nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := h.Prompt(ctx, "main", PromptOptions{})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start")
	}
	if _, err := h.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	first := <-order
	second := <-order
	if first != "agent_end" || second != "abort" {
		t.Fatalf("order=%s,%s", first, second)
	}
}

func TestAgentHarnessAbortCollectsQueueAndAbortErrors(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	started := make(chan struct{})
	h, err := New(Options{
		Model: model,
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				close(started)
				<-ctx.Done()
				msg := ai.NewAssistantMessageForModel(model, nil, ai.Usage{}, "aborted")
				stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	queueErr := errors.New("queue listener failed")
	abortErr := errors.New("abort listener failed")
	h.SubscribeHarness(func(ctx context.Context, ev HarnessEvent) error {
		switch event := ev.(type) {
		case QueueUpdateEvent:
			if len(event.Steer) == 0 {
				return queueErr
			}
		case AbortEvent:
			return abortErr
		}
		return nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := h.Prompt(ctx, "main", PromptOptions{})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start")
	}
	if err := h.Steer(ctx, "queued", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err = h.Abort(ctx)
	if !errors.Is(err, queueErr) || !errors.Is(err, abortErr) {
		t.Fatalf("err=%v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt did not finish")
	}
}

func TestAgentHarnessWaitForIdleBlocksDuringPrompt(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	stream := ai.NewAssistantMessageEventStream(4)
	started := make(chan struct{})
	h, err := New(Options{
		Model: model,
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			close(started)
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.Prompt(ctx, "main", PromptOptions{})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := h.WaitForIdle(waitCtx); err == nil {
		t.Fatal("WaitForIdle returned before prompt completed")
	}
	msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("answer"), ai.Usage{}, "stop")
	stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
	stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt did not finish")
	}
	if err := h.WaitForIdle(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAgentHarnessPrepareNextTurnReturnsNilWhenStateUnchanged(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	h, err := New(Options{Model: model, SystemPrompt: StaticSystemPrompt("system")})
	if err != nil {
		t.Fatal(err)
	}
	state, err := h.createTurnState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current := state
	cfg := h.loopConfig(
		func() turnState { return current },
		func(next turnState) { current = next },
	)
	update, err := cfg.PrepareNextTurn(ctx, agent.PrepareNextTurnContext{})
	if err != nil {
		t.Fatal(err)
	}
	if update != nil {
		t.Fatalf("update=%#v", update)
	}
}

// TestAgentHarnessDoesNotRestoreActiveToolsFromSessionContext locks the TS
// behavior (agent-harness.ts:331-363): createTurnState uses the in-memory
// activeToolNames, NOT the session BuildContext's activeToolNames. A session that
// recorded an active_tools_change does not retroactively restrict the turn's
// tools — only construction-time options or SetTools/SetActiveTools change them.
// (Item 1: the previous Go writeback from BuildContext polluted live state.)
func TestAgentHarnessDoesNotRestoreActiveToolsFromSessionContext(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendActiveToolsChange(ctx, []string{"beta"}); err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{
		Session: sess,
		Model:   model,
		// No ActiveToolNames option => the harness default-activates all tools.
		Tools: []agent.AgentTool{
			namedHarnessTool{name: "alpha"},
			namedHarnessTool{name: "beta"},
		},
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			// The session's active_tools_change(beta) must NOT restrict the turn:
			// both tools remain active because the in-memory set is the default.
			if len(aiCtx.Tools) != 2 || aiCtx.Tools[0].Name != "alpha" || aiCtx.Tools[1].Name != "beta" {
				t.Errorf("tools=%#v, want both alpha and beta (session context must not restrict)", aiCtx.Tools)
			}
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("answer"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Prompt(ctx, "main", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	// The harness in-memory active tools are unchanged by the turn.
	if active := h.GetActiveTools(); len(active) != 2 {
		t.Fatalf("active tools after turn = %d, want 2 (session context must not mutate harness)", len(active))
	}
}

func TestAgentHarnessSetToolsValidatesPersistsAndEmits(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{
		Session: sess,
		Model:   ai.Model{Provider: "test", ID: "m", API: "test"},
		Tools: []agent.AgentTool{
			namedHarnessTool{name: "alpha"},
			namedHarnessTool{name: "beta"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var hookEvents []ToolsUpdateEvent
	var harnessEvents []ToolsUpdateEvent
	h.OnToolsUpdate(func(ctx context.Context, ev ToolsUpdateEvent) error {
		hookEvents = append(hookEvents, ev)
		return nil
	})
	h.SubscribeHarness(func(ctx context.Context, ev HarnessEvent) error {
		if tools, ok := ev.(ToolsUpdateEvent); ok {
			harnessEvents = append(harnessEvents, tools)
		}
		return nil
	})
	if err := h.SetActiveTools(ctx, []string{"beta"}); err != nil {
		t.Fatal(err)
	}
	built, err := sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if built.ActiveToolNames == nil || len(*built.ActiveToolNames) != 1 || (*built.ActiveToolNames)[0] != "beta" {
		t.Fatalf("active tools=%#v", built.ActiveToolNames)
	}
	if len(hookEvents) != 1 || len(harnessEvents) != 1 || hookEvents[0].ActiveToolNames[0] != "beta" || harnessEvents[0].ActiveToolNames[0] != "beta" {
		t.Fatalf("hook=%#v harness=%#v", hookEvents, harnessEvents)
	}
	var agentErr *agent.AgentError
	if err := h.SetActiveTools(ctx, []string{"beta", "beta"}); !errors.As(err, &agentErr) || agentErr.Code != agent.AgentErrInvalidArgument {
		t.Fatalf("duplicate active tool err=%#v", err)
	}
	agentErr = nil
	if err := h.SetActiveTools(ctx, []string{"missing"}); !errors.As(err, &agentErr) || agentErr.Code != agent.AgentErrInvalidArgument {
		t.Fatalf("unknown active tool err=%#v", err)
	}
	agentErr = nil
	if err := h.SetTools(ctx, []agent.AgentTool{namedHarnessTool{name: "dup"}, namedHarnessTool{name: "dup"}}, nil); !errors.As(err, &agentErr) || agentErr.Code != agent.AgentErrInvalidArgument {
		t.Fatalf("duplicate tool err=%#v", err)
	}
	if err := h.SetTools(ctx, []agent.AgentTool{namedHarnessTool{name: "gamma"}}, []string{"gamma"}); err != nil {
		t.Fatal(err)
	}
	built, err = sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if built.ActiveToolNames == nil || len(*built.ActiveToolNames) != 1 || (*built.ActiveToolNames)[0] != "gamma" {
		t.Fatalf("active tools after SetTools=%#v", built.ActiveToolNames)
	}
	if len(hookEvents) != 2 || hookEvents[1].ToolNames[0] != "gamma" || hookEvents[1].PreviousToolNames[0] != "alpha" {
		t.Fatalf("hook events=%#v", hookEvents)
	}
}

func TestAgentHarnessQueuesAndBeforeStart(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	calls := 0
	h, err := New(Options{
		Model:        model,
		SystemPrompt: StaticSystemPrompt("system"),
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			calls++
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				if calls == 1 {
					texts := []string{}
					for _, msg := range aiCtx.Messages {
						texts = append(texts, ai.MessageText(msg))
					}
					want := []string{"next", "main", "from before"}
					if len(texts) != len(want) {
						t.Errorf("texts=%#v", texts)
					} else {
						for i := range want {
							if texts[i] != want[i] {
								t.Errorf("texts=%#v", texts)
								break
							}
						}
					}
					if aiCtx.SystemPrompt != "overridden" {
						t.Errorf("system prompt=%q", aiCtx.SystemPrompt)
					}
					msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("first"), ai.Usage{}, "stop")
					stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
					stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
					return
				}
				t.Errorf("unexpected extra call %d", calls)
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.OnBeforeAgentStart(func(ctx context.Context, ev BeforeAgentStartEvent) (*BeforeAgentStartResult, error) {
		if ev.Prompt != "main" || ev.SystemPrompt != "system" {
			t.Fatalf("before start=%#v", ev)
		}
		return &BeforeAgentStartResult{
			SystemPrompt: stringPtr("overridden"),
			Messages:     []agent.AgentMessage{ai.NewUserMessage("from before", nil)},
		}, nil
	})
	if err := h.NextTurn(ctx, "next", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	final, err := h.Prompt(ctx, "main", PromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ai.MessageText(final) != "first" || calls != 1 {
		t.Fatalf("final=%#v calls=%d", final, calls)
	}
}

// TestAgentHarnessQueueUpdatePrecedesBeforeAgentStart locks the TS ordering
// (agent-harness.ts:560-577): the nextTurn queue is drained and queue_update is
// emitted BEFORE before_agent_start, so the hook observes an already-drained
// queue and queue_update precedes before_agent_start in the event stream.
func TestAgentHarnessQueueUpdatePrecedesBeforeAgentStart(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	h, err := New(Options{
		Model:        model,
		SystemPrompt: StaticSystemPrompt("system"),
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var queueUpdateFired bool
	var order []string
	h.SubscribeHarness(func(ctx context.Context, ev HarnessEvent) error {
		if _, ok := ev.(QueueUpdateEvent); ok {
			queueUpdateFired = true
			order = append(order, "queue_update")
		}
		return nil
	})
	var beforeSawDrainedQueue bool
	h.OnBeforeAgentStart(func(ctx context.Context, ev BeforeAgentStartEvent) (*BeforeAgentStartResult, error) {
		order = append(order, "before_agent_start")
		// The nextTurn queue must already be drained by the time the hook fires.
		beforeSawDrainedQueue = !h.nextTurnQueue.HasItems()
		return nil, nil
	})
	if err := h.NextTurn(ctx, "queued", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	// NextTurn itself emits a queue_update; reset tracking for the prompt run.
	queueUpdateFired = false
	order = nil
	if _, err := h.Prompt(ctx, "main", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	if !queueUpdateFired {
		t.Fatal("queue_update should fire when draining a non-empty nextTurn queue")
	}
	if !beforeSawDrainedQueue {
		t.Fatal("before_agent_start must observe an already-drained nextTurn queue")
	}
	if len(order) < 2 || order[0] != "queue_update" || order[1] != "before_agent_start" {
		t.Fatalf("order=%v, want queue_update before before_agent_start", order)
	}
}

func TestAgentHarnessSkillAndPromptTemplate(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	var prompts []string
	h, err := New(Options{
		Model: model,
		Resources: Resources{
			Skills: []Skill{{Name: "review", Content: "review this", FilePath: "/skills/review/SKILL.md"}},
			PromptTemplates: []PromptTemplate{{
				Name:    "hello",
				Content: "hello $1 / $2 / $ARGUMENTS",
			}},
		},
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			prompts = append(prompts, ai.MessageText(aiCtx.Messages[len(aiCtx.Messages)-1]))
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.PromptFromTemplate(ctx, "hello", []string{"world", "again"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Skill(ctx, "review", "extra"); err != nil {
		t.Fatal(err)
	}
	wantSkillPrompt := "<skill name=\"review\" location=\"/skills/review/SKILL.md\">\nReferences are relative to /skills/review.\n\nreview this\n</skill>\n\nextra"
	if len(prompts) != 2 || prompts[0] != "hello world / again / world again" || prompts[1] != wantSkillPrompt {
		t.Fatalf("prompts=%#v", prompts)
	}
}

func TestAgentHarnessCompactWritesCompactionEntry(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var firstID string
	for i := 0; i < 14; i++ {
		id, err := sess.AppendMessage(ctx, ai.NewUserMessage("msg "+string(rune('a'+i)), nil))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstID = id
		}
	}
	h, err := New(Options{Session: sess})
	if err != nil {
		t.Fatal(err)
	}
	h.OnSessionBeforeCompact(func(ctx context.Context, ev SessionBeforeCompactEvent) (*SessionBeforeCompactResult, error) {
		return &SessionBeforeCompactResult{Compaction: &CompactionResult{
			Summary:          "from hook",
			FirstKeptEntryID: firstID,
			TokensBefore:     7,
			Details: map[string]any{
				"readFiles":     []string{"main.go"},
				"modifiedFiles": []string{"agent.go"},
			},
		}}, nil
	})
	result, err := h.Compact(ctx, "custom note")
	if err != nil {
		t.Fatal(err)
	}
	if result.FirstKeptEntryID != firstID || result.Summary != "from hook" {
		t.Fatalf("result=%#v", result)
	}
	built, err := sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 15 || ai.MessageRole(built.Messages[0]) != "compactionSummary" || ai.MessageText(built.Messages[1]) != "msg a" {
		t.Fatalf("messages=%#v", built.Messages)
	}
	summary, ok := built.Messages[0].(session.CompactionSummaryMessage)
	if !ok || summary.Summary != result.Summary || summary.TokensBefore != 7 {
		t.Fatalf("summary=%#v ok=%v", built.Messages[0], ok)
	}
}

func TestAgentHarnessCompactHooks(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("old", nil)); err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{Session: sess})
	if err != nil {
		t.Fatal(err)
	}
	var sawBefore, sawAfter bool
	h.OnSessionBeforeCompact(func(ctx context.Context, ev SessionBeforeCompactEvent) (*SessionBeforeCompactResult, error) {
		sawBefore = len(ev.BranchEntries) == 1 && ev.CustomInstructions == "note"
		return &SessionBeforeCompactResult{Compaction: &CompactionResult{Summary: "from hook", TokensBefore: 7, MessagesBefore: 1}}, nil
	})
	h.OnSessionCompact(func(ctx context.Context, ev SessionCompactEvent) error {
		sawAfter = ev.FromHook && ev.CompactionEntry.Summary == "from hook" && ev.CompactionEntry.FromHook
		return nil
	})
	result, err := h.Compact(ctx, "note")
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "from hook" || !sawBefore || !sawAfter {
		t.Fatalf("result=%#v before=%v after=%v", result, sawBefore, sawAfter)
	}
}

func TestAgentHarnessNavigateTreeHooksAndSummary(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rootID, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil))
	if err != nil {
		t.Fatal(err)
	}
	leftID, err := sess.AppendMessage(ctx, ai.NewUserMessage("left", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.MoveTo(ctx, &rootID); err != nil {
		t.Fatal(err)
	}
	rightID, err := sess.AppendMessage(ctx, ai.NewUserMessage("right", nil))
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{Session: sess})
	if err != nil {
		t.Fatal(err)
	}
	var sawBefore, sawTree bool
	h.OnSessionBeforeTree(func(ctx context.Context, ev SessionBeforeTreeEvent) (*SessionBeforeTreeResult, error) {
		sawBefore = ev.Preparation.TargetID == leftID && ev.Preparation.OldLeafID != nil && *ev.Preparation.OldLeafID == rightID
		label := "left-label"
		return &SessionBeforeTreeResult{Summary: &BranchSummary{Summary: "right branch summary"}, Label: &label}, nil
	})
	h.OnSessionTree(func(ctx context.Context, ev SessionTreeEvent) error {
		sawTree = ev.OldLeafID == rightID && ev.NewLeafID != "" && ev.SummaryEntry != nil && ev.SummaryEntry.Summary == "right branch summary"
		return nil
	})
	result, err := h.NavigateTree(ctx, leftID, NavigateTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !sawBefore || !sawTree || result.SummaryEntry == nil || result.EditorText != "left" {
		t.Fatalf("result=%#v before=%v tree=%v", result, sawBefore, sawTree)
	}
	built, err := sess.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 2 || ai.MessageText(built.Messages[0]) != "root" || ai.MessageRole(built.Messages[1]) != "branchSummary" {
		t.Fatalf("messages=%#v", built.Messages)
	}
	if label, ok := sess.Label(ctx, leftID); ok || label != "" {
		t.Fatalf("label=%q ok=%v", label, ok)
	}
}

func TestAgentHarnessNavigateTreeUsesPrecomputedSummary(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rootID, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil))
	if err != nil {
		t.Fatal(err)
	}
	leftID, err := sess.AppendMessage(ctx, ai.NewUserMessage("left", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.MoveTo(ctx, &rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("right", nil)); err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{Session: sess})
	if err != nil {
		t.Fatal(err)
	}
	details := map[string]any{"source": "caller"}
	result, err := h.NavigateTree(ctx, leftID, NavigateTreeOptions{
		Summary:          "precomputed summary",
		Details:          details,
		UserWantsSummary: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SummaryEntry == nil {
		t.Fatal("expected precomputed summary entry")
	}
	if result.SummaryEntry.Summary != "precomputed summary" {
		t.Fatalf("summary=%q", result.SummaryEntry.Summary)
	}
	gotDetails, ok := result.SummaryEntry.Details.(map[string]any)
	if !ok || gotDetails["source"] != "caller" {
		t.Fatalf("details=%#v", result.SummaryEntry.Details)
	}
}

func TestAgentHarnessNavigateTreeCancel(t *testing.T) {
	ctx := context.Background()
	sess, err := session.NewMemory(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rootID, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("leaf", nil)); err != nil {
		t.Fatal(err)
	}
	oldLeaf, err := sess.LeafID(ctx)
	if err != nil || oldLeaf == nil {
		t.Fatalf("old leaf=%v err=%v", oldLeaf, err)
	}
	h, err := New(Options{Session: sess})
	if err != nil {
		t.Fatal(err)
	}
	h.OnSessionBeforeTree(func(ctx context.Context, ev SessionBeforeTreeEvent) (*SessionBeforeTreeResult, error) {
		return &SessionBeforeTreeResult{Cancel: true}, nil
	})
	result, err := h.NavigateTree(ctx, rootID, NavigateTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := sess.LeafID(ctx)
	if err != nil || leaf == nil || *leaf != *oldLeaf || !result.Canceled {
		t.Fatalf("leaf=%v old=%v result=%#v err=%v", leaf, oldLeaf, result, err)
	}
}

type harnessTestTool struct {
	execute func(context.Context, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error)
}

type namedHarnessTool struct {
	name string
}

func (t namedHarnessTool) Name() string {
	return t.name
}

func (t namedHarnessTool) Label() string {
	return t.name
}

func (t namedHarnessTool) Description() string {
	return t.name
}

func (t namedHarnessTool) Schema() map[string]any {
	return map[string]any{"type": "object"}
}

func (t namedHarnessTool) Execute(ctx context.Context, args json.RawMessage, update agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{Content: ai.TextBlocks(t.name)}, nil
}

func (harnessTestTool) Name() string {
	return "lookup"
}

func (harnessTestTool) Label() string {
	return "Lookup"
}

func (harnessTestTool) Description() string {
	return "Lookup test data."
}

func (harnessTestTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []any{"query"},
	}
}

func (t harnessTestTool) Execute(ctx context.Context, args json.RawMessage, update agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return t.execute(ctx, args, update)
}
