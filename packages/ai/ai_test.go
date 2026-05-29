package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

func TestCompleteSimpleFaux(t *testing.T) {
	dir := t.TempDir()
	auth := NewAuthStorage(dir)
	registry := NewModelRegistry(dir, auth)
	model, ok := registry.Find("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	msg, err := registry.CompleteSimple(context.Background(), model, Context{Messages: []Message{NewUserMessage("hello", nil)}}, SimpleStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := MessageText(msg); got != "faux: hello" {
		t.Fatalf("text=%q", got)
	}
}

func TestStreamSimple(t *testing.T) {
	dir := t.TempDir()
	registry := NewModelRegistry(dir, NewAuthStorage(dir))
	model, _, _ := registry.Match("faux", "faux")
	stream := registry.StreamSimple(context.Background(), model, Context{Messages: []Message{NewUserMessage("hi", nil)}}, SimpleStreamOptions{})
	var sawDelta bool
	for event := range stream.Events() {
		if event.Type == "text_delta" && event.Delta == "faux: hi" {
			sawDelta = true
		}
	}
	_ = stream.Result()
	if !sawDelta {
		t.Fatal("missing text delta")
	}
}

func TestStreamSimpleEventProtocol(t *testing.T) {
	dir := t.TempDir()
	registry := NewModelRegistry(dir, NewAuthStorage(dir))
	model, _, _ := registry.Match("faux", "faux")
	stream := registry.StreamSimple(context.Background(), model, Context{Messages: []Message{NewUserMessage("hi", nil)}}, SimpleStreamOptions{})

	var events []AssistantMessageEvent
	for event := range stream.Events() {
		events = append(events, event)
	}
	message := stream.Result()

	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	want := []string{"start", "text_start", "text_delta", "text_end", "done"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("events=%v want %v", types, want)
	}
	if events[2].ContentIndex != 0 || events[2].Delta != "faux: hi" {
		t.Fatalf("delta event=%#v", events[2])
	}
	if events[3].ContentIndex != 0 || events[3].Content != "faux: hi" {
		t.Fatalf("end event=%#v", events[3])
	}
	if events[4].Reason != "stop" || MessageText(events[4].Partial) != "faux: hi" {
		t.Fatalf("done event=%#v", events[4])
	}
	if MessageText(message) != "faux: hi" {
		t.Fatalf("result=%#v", message)
	}
}

func TestAssistantMessageAPIComesFromModelAPI(t *testing.T) {
	dir := t.TempDir()
	registry := NewModelRegistry(dir, NewAuthStorage(dir))
	model := Model{Provider: "faux-provider-not-api", ID: "faux", API: "faux"}
	msg, err := registry.CompleteSimple(context.Background(), model, Context{Messages: []Message{NewUserMessage("hi", nil)}}, SimpleStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if msg.API != model.API || msg.Provider != model.Provider || msg.Model != model.ID {
		t.Fatalf("assistant identity api/provider/model=%q/%q/%q", msg.API, msg.Provider, msg.Model)
	}
}

func TestAssistantMessageEventJSONTerminalShape(t *testing.T) {
	msg := NewAssistantMessage("openai-completions", "openai", "gpt-test", TextBlocks("ok"), Usage{}, "stop")
	doneRaw, err := json.Marshal(AssistantMessageEvent{Type: "done", Reason: "stop", Partial: msg, Message: msg})
	if err != nil {
		t.Fatal(err)
	}
	var done map[string]any
	if err := json.Unmarshal(doneRaw, &done); err != nil {
		t.Fatal(err)
	}
	if done["message"] == nil || done["partial"] != nil {
		t.Fatalf("done json=%s", doneRaw)
	}

	errMsg := NewAssistantMessage("openai-completions", "openai", "gpt-test", nil, Usage{}, "error")
	errMsg.ErrorMessage = "boom"
	errorRaw, err := json.Marshal(AssistantMessageEvent{Type: "error", Reason: "error", Partial: errMsg, Error: errMsg})
	if err != nil {
		t.Fatal(err)
	}
	var errorEvent map[string]any
	if err := json.Unmarshal(errorRaw, &errorEvent); err != nil {
		t.Fatal(err)
	}
	if errorEvent["error"] == nil || errorEvent["partial"] != nil {
		t.Fatalf("error json=%s", errorRaw)
	}

	partialRaw, err := json.Marshal(AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "o", Partial: msg})
	if err != nil {
		t.Fatal(err)
	}
	var partial map[string]any
	if err := json.Unmarshal(partialRaw, &partial); err != nil {
		t.Fatal(err)
	}
	if partial["partial"] == nil || partial["message"] != nil || partial["error"] != nil {
		t.Fatalf("partial json=%s", partialRaw)
	}
}

func TestThinkingLevelsMatchTSClampRules(t *testing.T) {
	ptr := func(value string) *string { return &value }
	model := Model{
		Reasoning: true,
		ThinkingLevelMap: map[string]*string{
			"minimal": nil,
			"low":     nil,
			"medium":  nil,
			"high":    ptr("high"),
			"xhigh":   nil,
		},
	}
	levels := GetSupportedThinkingLevels(model)
	levelNames := make([]string, 0, len(levels))
	for _, level := range levels {
		levelNames = append(levelNames, string(level))
	}
	if got := strings.Join(levelNames, ","); got != "off,high" {
		t.Fatalf("supported levels=%q", got)
	}
	if got := ClampThinking(model, ThinkingLow); got != ThinkingHigh {
		t.Fatalf("clamp low=%q", got)
	}
	if got := ClampThinking(model, ThinkingXHigh); got != ThinkingHigh {
		t.Fatalf("clamp xhigh=%q", got)
	}
	if got := ClampThinking(Model{}, ThinkingHigh); got != ThinkingOff {
		t.Fatalf("non-reasoning clamp=%q", got)
	}
}

func TestStreamChatResponseCancellationIsAborted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := streamChatResponse(ctx, func(ctx context.Context) (ChatResponse, error) {
		return ChatResponse{}, ctx.Err()
	})
	var event AssistantMessageEvent
	for e := range stream.Events() {
		event = e
	}
	message := stream.Result()
	if event.Type != "error" || event.Reason != "aborted" {
		t.Fatalf("event=%#v", event)
	}
	if message.StopReason != "aborted" {
		t.Fatalf("message=%#v", message)
	}
}

func TestAssistantMessageEventStreamPushAfterEndIsNoop(t *testing.T) {
	stream := NewAssistantMessageEventStream(2)
	msg := NewAssistantMessage("test-api", "test", "test", TextBlocks("done"), Usage{}, "stop")
	stream.Push(AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
	stream.Push(AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "ignored", Partial: msg})
	if got := stream.Result(); MessageText(got) != "done" {
		t.Fatalf("result=%#v", got)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events() {
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Type != "done" {
		t.Fatalf("events=%#v", events)
	}
}

func TestAssistantMessageEventStreamCancelUnblocksDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := NewAssistantMessageEventStreamWithContext(ctx, 1)
	events := stream.Events()
	partial := NewAssistantMessage("test-api", "test", "test", TextBlocks("partial"), Usage{}, "stop")
	stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
	stream.Push(AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "partial", Partial: partial})

	cancel()
	resultCh := make(chan AssistantMessage, 1)
	go func() {
		resultCh <- stream.Result()
	}()
	select {
	case result := <-resultCh:
		if result.StopReason != "aborted" {
			t.Fatalf("result=%#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("Result did not unblock after context cancellation")
	}

	drained := make(chan struct{})
	go func() {
		for range events {
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("Events did not close after context cancellation")
	}
}

func TestAssistantMessageEventStreamDoesNotDropEventsPushedBeforeConsume(t *testing.T) {
	stream := NewAssistantMessageEventStream(1)
	partial := NewAssistantMessage("test-api", "test", "test", TextBlocks("partial"), Usage{}, "stop")
	const deltas = 5000 // far beyond the old 1024 cap that used to drop events
	for i := 0; i < deltas; i++ {
		stream.Push(AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "x", Partial: partial})
	}
	final := NewAssistantMessage("test-api", "test", "test", TextBlocks("done"), Usage{}, "stop")
	stream.Push(AssistantMessageEvent{Type: "done", Reason: "stop", Message: final})

	gotDeltas := 0
	for event := range stream.Events() {
		if event.Type == "text_delta" {
			gotDeltas++
		}
	}
	if gotDeltas != deltas {
		t.Fatalf("delivered %d text_delta events, want %d (events must not be silently dropped)", gotDeltas, deltas)
	}
	if got := stream.Result(); MessageText(got) != "done" {
		t.Fatalf("result=%#v", got)
	}
}

func TestProviderStreamRecoverEndsWithError(t *testing.T) {
	model := Model{Provider: "test", ID: "panic-model", API: "test-api"}
	stream := providerStream(context.Background(), model, 1, func(*AssistantMessageEventStream) (AssistantMessage, error) {
		panic("provider panic")
	})
	var event AssistantMessageEvent
	for next := range stream.Events() {
		event = next
	}
	result := stream.Result()
	if event.Type != "error" || result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "provider panic") {
		t.Fatalf("event=%#v result=%#v", event, result)
	}
}

func TestOpenAISDKJSONUsesDefaultRetryPolicy(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After-Ms", "1")
			http.Error(w, "temporary overload", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	raw, err := aiproviders.DoOpenAISDKJSONWithClient(
		context.Background(),
		server.URL,
		"test-key",
		nil,
		map[string]any{"ping": "pong"},
		true,
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts=%d, want SDK retry to make a second request", attempts.Load())
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("raw=%s", raw)
	}
}

func TestAnthropicSDKJSONUsesDefaultRetryPolicy(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After-Ms", "1")
			http.Error(w, "temporary overload", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	raw, err := aiproviders.DoAnthropicSDKJSONWithClient(
		context.Background(),
		server.URL,
		"test-key",
		nil,
		map[string]any{"ping": "pong"},
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts=%d, want SDK retry to make a second request", attempts.Load())
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("raw=%s", raw)
	}
}

func TestOpenAIChatStreamEmitsDeltas(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":""}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"hel"},"finish_reason":""}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
			API:      "openai-completions",
			BaseURL:  server.URL + "/v1",
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	var deltas []string
	var sawStart bool
	for event := range stream.Events() {
		if event.Type == "start" {
			sawStart = true
		}
		if event.Type == "text_delta" {
			deltas = append(deltas, event.Delta)
		}
	}
	message := stream.Result()
	if !sawStart {
		t.Fatal("missing start")
	}
	if got := strings.Join(deltas, ""); got != "hello" {
		t.Fatalf("deltas=%q", got)
	}
	if got := MessageText(message); got != "hello" {
		t.Fatalf("message text=%q", got)
	}
	if message.Usage.Input != 3 || message.Usage.Output != 2 || message.Usage.TotalTokens != 5 {
		t.Fatalf("usage=%#v", message.Usage)
	}
	if captured["stream"] != true {
		t.Fatalf("stream flag=%#v", captured["stream"])
	}
	options := captured["stream_options"].(map[string]any)
	if options["include_usage"] != true {
		t.Fatalf("stream_options=%#v", options)
	}
}

func TestOpenAIChatStreamAggregatesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":""}}]},"finish_reason":""}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\""}}]},"finish_reason":""}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"x\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
			API:      "openai-completions",
			BaseURL:  server.URL + "/v1",
		},
		Messages: []Message{NewUserMessage("hi", nil)},
		Tools:    ToolSet{"lookup": cacheTestToolDef("lookup")},
	})
	for range stream.Events() {
	}
	message := stream.Result()
	blocks := MessageBlocks(message)
	if len(blocks) != 1 || blocks[0].Type != "toolCall" || blocks[0].ID != "call_1" || blocks[0].Name != "lookup" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if string(blocks[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("arguments=%s", blocks[0].Arguments)
	}
	if message.StopReason != "toolUse" {
		t.Fatalf("stopReason=%q", message.StopReason)
	}
}

func TestOpenAIChatStreamInterleavedToolCallDeltasKeepContentIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"first","arguments":"{\"a\""}}]},"finish_reason":""}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"second","arguments":"{\"b\""}}]},"finish_reason":""}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":1"}}]},"finish_reason":""}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":":2"}}]},"finish_reason":""}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":""}]}` + "\n\n" +
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
			API:      "openai-completions",
			BaseURL:  server.URL + "/v1",
		},
		Messages: []Message{NewUserMessage("hi", nil)},
		Tools: ToolSet{
			"first":  cacheTestToolDef("first"),
			"second": cacheTestToolDef("second"),
		},
	})
	deltasByIndex := map[int]string{}
	var deltaIndexes []int
	for event := range stream.Events() {
		if event.Type == "toolcall_delta" {
			deltaIndexes = append(deltaIndexes, event.ContentIndex)
			deltasByIndex[event.ContentIndex] += event.Delta
		}
	}
	message := stream.Result()
	if want := []int{0, 1, 0, 1, 0, 1}; !reflect.DeepEqual(deltaIndexes, want) {
		t.Fatalf("toolcall_delta content indexes=%v", deltaIndexes)
	}
	if deltasByIndex[0] != `{"a":1}` || deltasByIndex[1] != `{"b":2}` {
		t.Fatalf("toolcall deltas=%#v", deltasByIndex)
	}
	blocks := MessageBlocks(message)
	if len(blocks) != 2 || blocks[0].ID != "call_a" || blocks[0].Name != "first" || string(blocks[0].Arguments) != `{"a":1}` {
		t.Fatalf("first block=%#v", blocks)
	}
	if blocks[1].ID != "call_b" || blocks[1].Name != "second" || string(blocks[1].Arguments) != `{"b":2}` {
		t.Fatalf("second block=%#v", blocks)
	}
	if message.StopReason != "toolUse" {
		t.Fatalf("stopReason=%q", message.StopReason)
	}
}

func TestOpenAIChatStreamErrorFinishReasonEmitsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":0,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"network_error"}]}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
			API:      "openai-completions",
			BaseURL:  server.URL + "/v1",
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	sawError := false
	for event := range stream.Events() {
		if event.Type == "error" {
			sawError = true
		}
	}
	message := stream.Result()
	if !sawError || message.StopReason != "error" || !strings.Contains(message.ErrorMessage, "network_error") {
		t.Fatalf("message=%#v sawError=%v", message, sawError)
	}
}

func TestAssistantMessageEventStreamResultDoesNotRequireDrainingEvents(t *testing.T) {
	stream := NewAssistantMessageEventStream(2)
	final := NewAssistantMessage("faux", "faux", "faux", TextBlocks("done"), Usage{}, "stop")
	go func() {
		partial := NewAssistantMessage("faux", "faux", "faux", nil, Usage{}, "stop")
		stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
		for i := 0; i < 32; i++ {
			stream.Push(AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "x", Partial: partial})
		}
		stream.Push(AssistantMessageEvent{Type: "done", Reason: "stop", Partial: final, Message: final})
	}()

	result := make(chan AssistantMessage, 1)
	go func() {
		result <- stream.Result()
	}()

	select {
	case message := <-result:
		if MessageText(message) != "done" {
			t.Fatalf("message=%#v", message)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Result blocked while Events was not drained")
	}
}

func TestAssistantMessageEventStreamResultDoesNotRequireContinuingToReadEvents(t *testing.T) {
	stream := NewAssistantMessageEventStream(1)
	events := stream.Events()
	final := NewAssistantMessage("faux", "faux", "faux", TextBlocks("done"), Usage{}, "stop")
	go func() {
		partial := NewAssistantMessage("faux", "faux", "faux", nil, Usage{}, "stop")
		for i := 0; i < 2048; i++ {
			stream.Push(AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "x", Partial: partial})
		}
		stream.Push(AssistantMessageEvent{Type: "done", Reason: "stop", Partial: final, Message: final})
	}()

	select {
	case <-events:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("initial event was not delivered")
	}

	result := make(chan AssistantMessage, 1)
	go func() {
		result <- stream.Result()
	}()

	select {
	case message := <-result:
		if MessageText(message) != "done" {
			t.Fatalf("message=%#v", message)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Result blocked after event consumer stopped reading")
	}
}

func TestCompleteSimplePropagatesTools(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	registry := NewModelRegistry(dir, NewAuthStorage(dir))
	registry.Auth.SetRuntime("openai", "test-key")
	_, err := registry.CompleteSimple(context.Background(), Model{
		Provider: "openai",
		ID:       "gpt-test",
		API:      "openai-completions",
		BaseURL:  server.URL,
	}, Context{
		Messages: []Message{NewUserMessage("hi", nil)},
		Tools: []Tool{{
			Name:        "lookup",
			Description: "Lookup data",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
		}},
	}, SimpleStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tools := captured["tools"].([]any)
	function := tools[0].(map[string]any)["function"].(map[string]any)
	if function["name"] != "lookup" || function["description"] != "Lookup data" {
		t.Fatalf("tool function=%#v", function)
	}
	if captured["tool_choice"] != "auto" {
		t.Fatalf("tool_choice=%#v", captured["tool_choice"])
	}
}

func TestModelRegistryUsesLiteralAPIKeyFromModelsJSON(t *testing.T) {
	dir := t.TempDir()
	raw := `{"providers":{"literalai":{"api":"openai-completions","baseUrl":"https://example.test/chat","apiKey":"literal-key","models":[{"id":"model"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir, NewAuthStorage(dir))
	model, ok := registry.Find("literalai", "model")
	if !ok {
		t.Fatal("missing literal model")
	}
	key, err := registry.APIKey(context.Background(), model)
	if err != nil {
		t.Fatal(err)
	}
	if key != "literal-key" {
		t.Fatalf("key=%q", key)
	}
}

func TestUtilities(t *testing.T) {
	repaired := RepairJSON("{\"x\":\"a\nb\"}")
	parsed, err := ParseJSONWithRepair[map[string]string](repaired)
	if err != nil {
		t.Fatal(err)
	}
	if parsed["x"] != "a\nb" {
		t.Fatalf("bad repaired json: %#v", parsed)
	}
	msg := NewAssistantMessage("openai-completions", "openai", "m", nil, Usage{}, "error")
	msg.ErrorMessage = "Your input exceeds the context window of this model"
	if !IsContextOverflow(msg, 0) {
		t.Fatal("expected overflow")
	}
	if ShortHash("hello") == ShortHash("world") {
		t.Fatal("hash collision in smoke test")
	}
	called := false
	unregister := RegisterSessionResourceCleanup(func(sessionID string) error {
		called = sessionID == "s1"
		return nil
	})
	defer unregister()
	if err := CleanupSessionResources("s1"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("cleanup not called")
	}
}

func TestCalculateCostUsesModelRates(t *testing.T) {
	cost := CalculateCost(Model{
		Cost: ModelCost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	}, Usage{Input: 1_000_000, Output: 2_000_000, CacheRead: 500_000, CacheWrite: 100_000})
	if cost.Total != 33.525 {
		t.Fatalf("cost=%#v", cost)
	}
}
