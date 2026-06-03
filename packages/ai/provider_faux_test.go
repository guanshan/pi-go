package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func fauxTestModel(t *testing.T) (*ModelRegistry, Model) {
	t.Helper()
	dir := t.TempDir()
	registry := NewModelRegistry(dir, NewAuthStorage(dir))
	model, ok := registry.Find("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	return registry, model
}

// TestFauxScriptedToolLoop proves a scripted 2-step tool-loop: response 1 emits
// a tool call (stopReason toolUse, inferred), response 2 emits final text.
func TestFauxScriptedToolLoop(t *testing.T) {
	ResetFauxResponses()
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	SetFauxResponses([]FauxResponse{
		{Content: []ContentBlock{FauxToolCall("call-1", "echo", map[string]any{"text": "hi"})}},
		NewFauxText("all done"),
	})

	ctx := Context{Messages: []Message{NewUserMessage("run echo", nil)}}

	first, err := registry.Complete(context.Background(), model, ctx, StreamOptions{})
	if err != nil {
		t.Fatalf("first complete: %v", err)
	}
	if first.StopReason != "toolUse" {
		t.Fatalf("first stopReason=%q want toolUse", first.StopReason)
	}
	firstCalls := toolCallsFromMessage(first)
	if len(firstCalls) != 1 || firstCalls[0].Name != "echo" || firstCalls[0].ID != "call-1" {
		t.Fatalf("first tool calls=%#v", firstCalls)
	}
	var args map[string]any
	if err := json.Unmarshal(firstCalls[0].Arguments, &args); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	if args["text"] != "hi" {
		t.Fatalf("args=%#v", args)
	}

	second, err := registry.Complete(context.Background(), model, ctx, StreamOptions{})
	if err != nil {
		t.Fatalf("second complete: %v", err)
	}
	if second.StopReason != "stop" {
		t.Fatalf("second stopReason=%q want stop", second.StopReason)
	}
	if got := MessageText(second); got != "all done" {
		t.Fatalf("second text=%q", got)
	}
	if calls := toolCallsFromMessage(second); len(calls) != 0 {
		t.Fatalf("second tool calls=%#v", calls)
	}

	if FauxCallCount() != 2 {
		t.Fatalf("callCount=%d want 2", FauxCallCount())
	}
	if PendingFauxResponseCount() != 0 {
		t.Fatalf("pending=%d want 0", PendingFauxResponseCount())
	}
}

// TestFauxUsageFlowsThrough proves explicit usage values are surfaced verbatim.
func TestFauxUsageFlowsThrough(t *testing.T) {
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	want := Usage{Input: 111, Output: 22, CacheRead: 3, CacheWrite: 4, TotalTokens: 140}
	SetFauxResponses([]FauxResponse{
		{Content: []ContentBlock{FauxText("answer")}, Usage: want},
	})

	msg, err := registry.Complete(context.Background(), model, Context{Messages: []Message{NewUserMessage("q", nil)}}, StreamOptions{})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if msg.Usage != want {
		t.Fatalf("usage=%#v want %#v", msg.Usage, want)
	}
}

// TestFauxEstimatedUsageNonZero proves a scripted response without explicit
// usage still flows non-zero estimated token counts.
func TestFauxEstimatedUsageNonZero(t *testing.T) {
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	SetFauxResponses([]FauxResponse{NewFauxText("a non-trivial answer")})

	msg, err := registry.Complete(context.Background(), model, Context{SystemPrompt: "be terse", Messages: []Message{NewUserMessage("hello there", nil)}}, StreamOptions{})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	u := msg.Usage
	if u.Input <= 0 || u.Output <= 0 {
		t.Fatalf("estimated usage not positive: %#v", u)
	}
	if u.TotalTokens != u.Input+u.Output {
		t.Fatalf("totalTokens=%d want %d", u.TotalTokens, u.Input+u.Output)
	}
}

// TestFauxScriptedError proves a scripted error surfaces as stopReason==error
// with the error text, both via Complete and as a terminal Stream error event.
func TestFauxScriptedError(t *testing.T) {
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	SetFauxResponses([]FauxResponse{
		{StopReason: "error", ErrorMessage: "upstream failed"},
	})

	_, err := registry.Complete(context.Background(), model, Context{Messages: []Message{NewUserMessage("hi", nil)}}, StreamOptions{})
	if err == nil || err.Error() != "upstream failed" {
		t.Fatalf("complete err=%v want upstream failed", err)
	}

	// Stream path: re-script (the queue was consumed) and assert the terminal
	// event is an error carrying the message.
	SetFauxResponses([]FauxResponse{
		{StopReason: "error", ErrorMessage: "upstream failed"},
	})
	stream := registry.Stream(context.Background(), model, Context{Messages: []Message{NewUserMessage("hi", nil)}}, StreamOptions{})
	var types []string
	var terminal AssistantMessageEvent
	for event := range stream.Events() {
		types = append(types, event.Type)
		terminal = event
	}
	result := stream.Result()
	if terminal.Type != "error" {
		t.Fatalf("terminal event=%q events=%v", terminal.Type, types)
	}
	if terminal.Reason != "error" || terminal.Error.StopReason != "error" || terminal.Error.ErrorMessage != "upstream failed" {
		t.Fatalf("terminal=%#v", terminal)
	}
	if result.StopReason != "error" || result.ErrorMessage != "upstream failed" {
		t.Fatalf("result=%#v", result)
	}
}

// TestFauxErrorMessageImpliesError proves an ErrorMessage without an explicit
// stopReason is promoted to stopReason==error.
func TestFauxErrorMessageImpliesError(t *testing.T) {
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	SetFauxResponses([]FauxResponse{{ErrorMessage: "boom"}})
	_, err := registry.Complete(context.Background(), model, Context{Messages: []Message{NewUserMessage("hi", nil)}}, StreamOptions{})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err=%v want boom", err)
	}
}

// TestFauxFallbackEcho proves the legacy echo behaviour is preserved when no
// responses are scripted.
func TestFauxFallbackEcho(t *testing.T) {
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	// No SetFauxResponses call: queue is empty -> echo.
	msg, err := registry.Complete(context.Background(), model, Context{Messages: []Message{NewUserMessage("hello", nil)}}, StreamOptions{})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got := MessageText(msg); got != "faux: hello" {
		t.Fatalf("text=%q want %q", got, "faux: hello")
	}
}

// TestFauxResetRestoresEcho proves clearing the queue mid-flight restores echo.
func TestFauxResetRestoresEcho(t *testing.T) {
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	SetFauxResponses([]FauxResponse{NewFauxText("scripted")})
	msg, err := registry.Complete(context.Background(), model, Context{Messages: []Message{NewUserMessage("hi", nil)}}, StreamOptions{})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got := MessageText(msg); got != "scripted" {
		t.Fatalf("text=%q", got)
	}

	ResetFauxResponses()
	msg, err = registry.Complete(context.Background(), model, Context{Messages: []Message{NewUserMessage("again", nil)}}, StreamOptions{})
	if err != nil {
		t.Fatalf("complete after reset: %v", err)
	}
	if got := MessageText(msg); got != "faux: again" {
		t.Fatalf("text=%q want echo", got)
	}
}

// TestFauxAppendResponses proves append extends the existing queue.
func TestFauxAppendResponses(t *testing.T) {
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	SetFauxResponses([]FauxResponse{NewFauxText("first")})
	AppendFauxResponses([]FauxResponse{NewFauxText("second")})
	if PendingFauxResponseCount() != 2 {
		t.Fatalf("pending=%d want 2", PendingFauxResponseCount())
	}

	ctx := Context{Messages: []Message{NewUserMessage("hi", nil)}}
	for _, want := range []string{"first", "second"} {
		msg, err := registry.Complete(context.Background(), model, ctx, StreamOptions{})
		if err != nil {
			t.Fatalf("complete: %v", err)
		}
		if got := MessageText(msg); got != want {
			t.Fatalf("text=%q want %q", got, want)
		}
	}
}

// TestFauxStreamEmitsAllBlockEvents proves the Stream path emits the full
// thinking/text/toolcall/done event sequence for a scripted multi-block
// response, with usage on the final message.
func TestFauxStreamEmitsAllBlockEvents(t *testing.T) {
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	SetFauxResponses([]FauxResponse{
		{
			Content: []ContentBlock{
				FauxThinking("let me think"),
				FauxText("here is the answer"),
				FauxToolCall("call-1", "echo", map[string]any{"text": "hi", "count": 12}),
			},
		},
	})

	stream := registry.Stream(context.Background(), model, Context{Messages: []Message{NewUserMessage("hi", nil)}}, StreamOptions{})
	var types []string
	var toolDelta string
	for event := range stream.Events() {
		types = append(types, event.Type)
		if event.Type == "toolcall_delta" {
			toolDelta += event.Delta
		}
	}
	result := stream.Result()

	want := []string{
		"start",
		"thinking_start", "thinking_delta", "thinking_end",
		"text_start", "text_delta", "text_end",
		"toolcall_start", "toolcall_delta", "toolcall_end",
		"done",
	}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("events=%v\nwant  %v", types, want)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(toolDelta), &args); err != nil {
		t.Fatalf("toolcall delta not valid json %q: %v", toolDelta, err)
	}
	if args["text"] != "hi" {
		t.Fatalf("toolcall args=%#v", args)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("result stopReason=%q want toolUse", result.StopReason)
	}
	if result.Usage.TotalTokens <= 0 {
		t.Fatalf("result usage not populated: %#v", result.Usage)
	}
}

// TestFauxStreamMultipleToolCalls proves multiple tool calls in one scripted
// message each produce their own start/end events.
func TestFauxStreamMultipleToolCalls(t *testing.T) {
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	SetFauxResponses([]FauxResponse{
		{
			Content: []ContentBlock{
				FauxToolCall("t1", "echo", map[string]any{"text": "one"}),
				FauxToolCall("t2", "echo", map[string]any{"text": "two"}),
			},
			StopReason: "toolUse",
		},
	})

	stream := registry.Stream(context.Background(), model, Context{Messages: []Message{NewUserMessage("hi", nil)}}, StreamOptions{})
	starts, ends := 0, 0
	for event := range stream.Events() {
		switch event.Type {
		case "toolcall_start":
			starts++
		case "toolcall_end":
			ends++
		}
	}
	_ = stream.Result()
	if starts != 2 || ends != 2 {
		t.Fatalf("toolcall starts=%d ends=%d want 2/2", starts, ends)
	}
}

// TestFauxTwoInstancesIsolated proves two RegisterFauxProvider instances script
// independent queues with no crosstalk, and that per-instance traffic does not
// touch the shared default instance behind the package-level shims.
func TestFauxTwoInstancesIsolated(t *testing.T) {
	ResetFauxResponses()
	defer ResetFauxResponses()

	dir := t.TempDir()
	registry := NewModelRegistry(dir, NewAuthStorage(dir))

	a := RegisterFauxProvider(NewFauxText("alpha answer"))
	defer a.Unregister()
	b := RegisterFauxProvider(NewFauxText("beta answer"))
	defer b.Unregister()

	if a.Model.API == b.Model.API {
		t.Fatalf("instances share API %q", a.Model.API)
	}
	if a.Model.Provider != "faux" || b.Model.Provider != "faux" {
		t.Fatalf("instance Provider not faux: %q %q", a.Model.Provider, b.Model.Provider)
	}

	ctx := Context{Messages: []Message{NewUserMessage("hi", nil)}}
	ra, err := registry.Complete(context.Background(), a.Model, ctx, StreamOptions{})
	if err != nil {
		t.Fatalf("a complete: %v", err)
	}
	rb, err := registry.Complete(context.Background(), b.Model, ctx, StreamOptions{})
	if err != nil {
		t.Fatalf("b complete: %v", err)
	}

	if got := MessageText(ra); got != "alpha answer" {
		t.Fatalf("a text=%q want %q", got, "alpha answer")
	}
	if got := MessageText(rb); got != "beta answer" {
		t.Fatalf("b text=%q want %q", got, "beta answer")
	}
	if a.Provider.CallCount() != 1 || b.Provider.CallCount() != 1 {
		t.Fatalf("callCounts a=%d b=%d want 1/1", a.Provider.CallCount(), b.Provider.CallCount())
	}
	if a.Provider.PendingResponseCount() != 0 || b.Provider.PendingResponseCount() != 0 {
		t.Fatalf("pending a=%d b=%d want 0/0", a.Provider.PendingResponseCount(), b.Provider.PendingResponseCount())
	}
	// The shared default instance must be untouched by per-instance traffic.
	if FauxCallCount() != 0 {
		t.Fatalf("default instance callCount=%d want 0 (per-instance traffic leaked into the default)", FauxCallCount())
	}
}

// TestFauxParallelSubtestsNoCrosstalk exercises the per-instance path from
// t.Parallel() subtests: each owns its own RegisterFauxProvider instance and
// unique API, so concurrent scripting is race-free (the global shims are never
// used here).
func TestFauxParallelSubtestsNoCrosstalk(t *testing.T) {
	cases := []struct{ name, answer string }{
		{"one", "answer-one"},
		{"two", "answer-two"},
		{"three", "answer-three"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			registry := NewModelRegistry(dir, NewAuthStorage(dir))
			reg := RegisterFauxProvider()
			t.Cleanup(reg.Unregister)
			reg.Provider.SetResponses([]FauxResponse{NewFauxText(tc.answer)})

			ctx := Context{Messages: []Message{NewUserMessage("hi "+tc.name, nil)}}
			resp, err := registry.Complete(context.Background(), reg.Model, ctx, StreamOptions{})
			if err != nil {
				t.Fatalf("complete: %v", err)
			}
			if got := MessageText(resp); got != tc.answer {
				t.Fatalf("text=%q want %q", got, tc.answer)
			}
			if reg.Provider.CallCount() != 1 {
				t.Fatalf("callCount=%d want 1", reg.Provider.CallCount())
			}
		})
	}
}

// TestFauxDefaultInstanceShimsStillWork is a regression guard that the
// package-level Set/Append/Reset/Pending/FauxCallCount shims still drive the
// shared default instance behind the builtin "faux" model exactly as before.
func TestFauxDefaultInstanceShimsStillWork(t *testing.T) {
	ResetFauxResponses()
	defer ResetFauxResponses()
	registry, model := fauxTestModel(t)

	SetFauxResponses([]FauxResponse{NewFauxText("first")})
	AppendFauxResponses([]FauxResponse{NewFauxText("second")})
	if PendingFauxResponseCount() != 2 {
		t.Fatalf("pending=%d want 2", PendingFauxResponseCount())
	}

	ctx := Context{Messages: []Message{NewUserMessage("hi", nil)}}
	r1, err := registry.Complete(context.Background(), model, ctx, StreamOptions{})
	if err != nil {
		t.Fatalf("complete 1: %v", err)
	}
	if got := MessageText(r1); got != "first" {
		t.Fatalf("r1=%q want first", got)
	}
	r2, err := registry.Complete(context.Background(), model, ctx, StreamOptions{})
	if err != nil {
		t.Fatalf("complete 2: %v", err)
	}
	if got := MessageText(r2); got != "second" {
		t.Fatalf("r2=%q want second", got)
	}
	if FauxCallCount() != 2 {
		t.Fatalf("callCount=%d want 2", FauxCallCount())
	}
	if PendingFauxResponseCount() != 0 {
		t.Fatalf("pending=%d want 0", PendingFauxResponseCount())
	}
}
