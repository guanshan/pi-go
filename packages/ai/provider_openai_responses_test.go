package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

func TestOpenAIResponsesPayloadAndParse(t *testing.T) {
	var captured map[string]any
	var headers http.Header
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			http.Error(w, "websocket unavailable", http.StatusBadGateway)
			return
		}
		headers = r.Header.Clone()
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_1",
			"status":"completed",
			"output":[
				{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking"}]},
				{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hello"}],"phase":"final_answer"},
				{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"ok\":true}"}
			],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":3}}
		}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	temp := 0.3
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "openai",
			ID:        "gpt-test",
			API:       "openai-responses",
			BaseURL:   server.URL + "/v1",
			Input:     []string{"text", "image"},
			Reasoning: true,
			MaxOutput: 77,
		},
		SystemPrompt: "system",
		Messages: []Message{
			NewUserMessage("see", []ContentBlock{{Type: "image", MimeType: "image/png", Data: "abc"}}),
			NewAssistantMessage("openai-responses", "openai", "gpt-test", []ContentBlock{{Type: "toolCall", ID: "call_1|fc_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)}}, Usage{}, "toolUse"),
			NewToolResultMessage("call_1|fc_1", "lookup", []ContentBlock{{Type: "text", Text: "result"}}, nil, false),
		},
		Tools:          ToolSet{"read": cacheTestToolDef("read")},
		ThinkingLevel:  ThinkingHigh,
		CacheRetention: "long",
		SessionID:      "session-1",
		MaxTokens:      55,
		Temperature:    &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/responses" {
		t.Fatalf("path=%q", path)
	}
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := headers.Get("session_id"); got != "session-1" {
		t.Fatalf("session_id=%q", got)
	}
	if got := headers.Get("x-client-request-id"); got != "session-1" {
		t.Fatalf("x-client-request-id=%q", got)
	}
	if captured["store"] != false || captured["stream"] != false {
		t.Fatalf("store/stream=%#v", captured)
	}
	if got := captured["prompt_cache_key"]; got != "session-1" {
		t.Fatalf("prompt_cache_key=%#v", got)
	}
	if got := captured["prompt_cache_retention"]; got != "24h" {
		t.Fatalf("prompt_cache_retention=%#v", got)
	}
	if got := captured["max_output_tokens"]; got != float64(55) {
		t.Fatalf("max_output_tokens=%#v", got)
	}
	if got := captured["temperature"]; got != 0.3 {
		t.Fatalf("temperature=%#v", got)
	}
	reasoning := captured["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning=%#v", reasoning)
	}
	input := captured["input"].([]any)
	if got := input[0].(map[string]any)["role"]; got != "developer" {
		t.Fatalf("system role=%#v", got)
	}
	userContent := input[1].(map[string]any)["content"].([]any)
	if got := userContent[1].(map[string]any)["type"]; got != "input_image" {
		t.Fatalf("user image part=%#v", userContent[1])
	}
	assistantCall := input[2].(map[string]any)
	if assistantCall["type"] != "function_call" || assistantCall["id"] != "fc_1" || assistantCall["call_id"] != "call_1" {
		t.Fatalf("assistant function_call=%#v", assistantCall)
	}
	toolOutput := input[3].(map[string]any)
	if toolOutput["type"] != "function_call_output" || toolOutput["call_id"] != "call_1" {
		t.Fatalf("tool output=%#v", toolOutput)
	}
	tools := captured["tools"].([]any)
	if tools[0].(map[string]any)["strict"] != false {
		t.Fatalf("tool strict=%#v", tools[0])
	}

	blocks := MessageBlocks(response.Message)
	if len(blocks) != 3 || blocks[0].Thinking != "thinking" || blocks[1].Text != "hello" || blocks[2].Name != "lookup" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if !strings.Contains(blocks[1].TextSignature, `"id":"msg_1"`) {
		t.Fatalf("text signature=%q", blocks[1].TextSignature)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].ID != "call_1|fc_1" || string(response.ToolCalls[0].Arguments) != `{"ok":true}` {
		t.Fatalf("tool calls=%#v", response.ToolCalls)
	}
	if response.Message.StopReason != "toolUse" {
		t.Fatalf("stopReason=%q", response.Message.StopReason)
	}
	if response.Message.Usage.Input != 7 || response.Message.Usage.CacheRead != 3 || response.Message.Usage.TotalTokens != 15 {
		t.Fatalf("usage=%#v", response.Message.Usage)
	}
}

func TestOpenAIResponsesGeneratedThinkingLevelsSuppressOffReasoning(t *testing.T) {
	model, ok := Find(AllKnownModels(), "openai", "gpt-5.4-pro")
	if !ok {
		t.Fatal("missing openai/gpt-5.4-pro")
	}
	req := ChatRequest{
		Model:    model,
		Messages: []Message{NewUserMessage("hi", nil)},
	}
	body := aiproviders.OpenAIResponsesBody(openAIResponsesRequestOptions(req))
	if reasoning, ok := body["reasoning"]; ok {
		t.Fatalf("default reasoning=%#v, want omitted because TS marks off unsupported", reasoning)
	}

	req.ThinkingLevel = ThinkingOff
	body = aiproviders.OpenAIResponsesBody(openAIResponsesRequestOptions(req))
	if reasoning, ok := body["reasoning"]; ok {
		t.Fatalf("off reasoning=%#v, want omitted because TS marks off unsupported", reasoning)
	}
}

func TestAzureOpenAIResponsesPayload(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_VERSION", "2025-04-01-preview")
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-test=deployment-test")
	var captured map[string]any
	var query string
	var path string
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		query = r.URL.RawQuery
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","id":"msg_a","content":[{"type":"output_text","text":"azure"}]}]}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("azure-openai-responses", "azure-key")
	msg, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "azure-openai-responses",
			ID:       "gpt-test",
			API:      "azure-openai-responses",
			BaseURL:  server.URL,
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headers.Get("api-key"); got != "azure-key" {
		t.Fatalf("api-key=%q", got)
	}
	if query != "api-version=2025-04-01-preview" {
		t.Fatalf("query=%q", query)
	}
	if path != "/openai/v1/responses" {
		t.Fatalf("path=%q", path)
	}
	if captured["model"] != "deployment-test" {
		t.Fatalf("model=%#v", captured["model"])
	}
	if got := MessageText(msg.Message); got != "azure" {
		t.Fatalf("text=%q", got)
	}
}

// P2-5: Azure Responses sends prompt_cache_key whenever a sessionId is present,
// even when cacheRetention=="none" (azure-openai-responses.ts:258 has no
// cacheRetention gate, unlike openai-responses.ts:237).
func TestAzureOpenAIResponsesSendsPromptCacheKeyWithoutRetentionGate(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_VERSION", "2025-04-01-preview")
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-test=deployment-test")
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","id":"msg_a","content":[{"type":"output_text","text":"azure"}]}]}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("azure-openai-responses", "azure-key")
	if _, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "azure-openai-responses",
			ID:       "gpt-test",
			API:      "azure-openai-responses",
			BaseURL:  server.URL,
			Input:    []string{"text"},
		},
		CacheRetention: "none",
		SessionID:      "azure-session-1",
		Messages:       []Message{NewUserMessage("hi", nil)},
	}); err != nil {
		t.Fatal(err)
	}
	if got := captured["prompt_cache_key"]; got != "azure-session-1" {
		t.Fatalf("expected prompt_cache_key even with cacheRetention=none, got %#v", got)
	}
}

func TestOpenAIResponsesReasoningOffServiceTierResponseMetadataAndForeignIDs(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_meta",
			"model":"gpt-actual",
			"status":"completed",
			"service_tier":"priority",
			"output":[{"type":"message","id":"msg_meta","content":[{"type":"output_text","text":"meta"}]}],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":2}}
		}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	foreignAssistant := NewAssistantMessage("openai-responses", "openai", "other-model", []ContentBlock{
		{Type: "toolCall", ID: "call foreign|fc_foreign_pair", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
	}, Usage{}, "toolUse")
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "openai",
			ID:        "gpt-5.5",
			API:       "openai-responses",
			BaseURL:   server.URL + "/v1",
			Input:     []string{"text"},
			Reasoning: true,
			Cost:      ModelCost{Input: 1, Output: 1, CacheRead: 1},
		},
		Messages: []Message{
			foreignAssistant,
			NewToolResultMessage("call foreign|fc_foreign_pair", "lookup", TextBlocks("ok"), nil, false),
		},
		ThinkingLevel: ThinkingOff,
		Metadata:      map[string]any{"serviceTier": "priority"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured["service_tier"] != "priority" {
		t.Fatalf("service_tier=%#v", captured["service_tier"])
	}
	reasoning := captured["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" {
		t.Fatalf("reasoning=%#v", reasoning)
	}
	if _, ok := captured["include"]; ok {
		t.Fatalf("include should be omitted for reasoning off: %#v", captured["include"])
	}
	input := captured["input"].([]any)
	toolCall := input[0].(map[string]any)
	if _, ok := toolCall["id"]; ok {
		t.Fatalf("foreign same-api different-model tool call should omit item id: %#v", toolCall)
	}
	if toolCall["call_id"] != "call_foreign" {
		t.Fatalf("tool call=%#v", toolCall)
	}
	toolResult := input[1].(map[string]any)
	if toolResult["call_id"] != "call_foreign" {
		t.Fatalf("tool result=%#v", toolResult)
	}
	if response.Message.ResponseID != "resp_meta" || response.Message.ResponseModel != "gpt-actual" {
		t.Fatalf("response metadata=%#v", response.Message)
	}
	if math.Abs(response.Message.Usage.Cost.Input-((8.0/1_000_000)*2.5)) > 1e-12 {
		t.Fatalf("service tier cost=%#v", response.Message.Usage.Cost)
	}
}

func TestOpenAIResponsesStreamEmitsDeltas(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.created","response":{"id":"resp_s","status":"in_progress"}}` + "\n\n" +
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_s","role":"assistant","status":"in_progress","content":[]}}` + "\n\n" +
				`data: {"type":"response.output_text.delta","item_id":"msg_s","output_index":0,"content_index":0,"delta":"hel"}` + "\n\n" +
				`data: {"type":"response.output_text.delta","item_id":"msg_s","output_index":0,"content_index":0,"delta":"lo"}` + "\n\n" +
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_s","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_s","status":"completed","output":[{"type":"message","id":"msg_s","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6,"input_tokens_details":{"cached_tokens":1}}}}` + "\n\n" +
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
			API:      "openai-responses",
			BaseURL:  server.URL + "/v1",
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	var deltas []string
	for event := range stream.Events() {
		if event.Type == "text_delta" {
			deltas = append(deltas, event.Delta)
		}
	}
	message := stream.Result()
	if got := strings.Join(deltas, ""); got != "hello" {
		t.Fatalf("deltas=%q", got)
	}
	if got := MessageText(message); got != "hello" {
		t.Fatalf("message=%q", got)
	}
	if message.Usage.Input != 3 || message.Usage.CacheRead != 1 || message.Usage.Output != 2 || message.Usage.TotalTokens != 6 {
		t.Fatalf("usage=%#v", message.Usage)
	}
	if captured["stream"] != true {
		t.Fatalf("stream flag=%#v", captured["stream"])
	}
}

func TestOpenAIResponsesStreamCleansPartialToolArgumentsData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_test","call_id":"call_test","name":"edit","arguments":""}}` + "\n\n" +
				`data: {"type":"response.function_call_arguments.delta","item_id":"fc_test","output_index":0,"delta":"{\"path\":\"README.md\""}` + "\n\n" +
				`data: {"type":"response.function_call_arguments.delta","item_id":"fc_test","output_index":0,"delta":",\"content\":\"updated\"}"}` + "\n\n" +
				`data: {"type":"response.function_call_arguments.done","item_id":"fc_test","output_index":0,"arguments":"{\"path\":\"README.md\",\"content\":\"updated\"}"}` + "\n\n" +
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_test","call_id":"call_test","name":"edit","arguments":"{\"path\":\"README.md\",\"content\":\"updated\"}"}}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_tool","status":"completed"}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
			API:      "openai-responses",
			BaseURL:  server.URL + "/v1",
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	for range stream.Events() {
	}
	message := stream.Result()
	blocks := MessageBlocks(message)
	if len(blocks) != 1 || blocks[0].Type != "toolCall" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if blocks[0].Data != "" {
		t.Fatalf("tool call Data should be streaming scratch only: %#v", blocks[0])
	}
	if string(blocks[0].Arguments) != `{"path":"README.md","content":"updated"}` {
		t.Fatalf("arguments=%s", blocks[0].Arguments)
	}
}

func TestOpenAIResponsesStreamErrorEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.created","response":{"id":"resp_err","status":"in_progress"}}` + "\n\n" +
				`data: {"type":"error","code":"rate_limit","message":"too fast"}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
			API:      "openai-responses",
			BaseURL:  server.URL + "/v1",
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	var sawError, sawDone bool
	var errorMessage string
	for event := range stream.Events() {
		switch event.Type {
		case "error":
			sawError = true
			errorMessage = event.Error.ErrorMessage
		case "done":
			sawDone = true
		}
	}
	message := stream.Result()
	if !sawError || sawDone {
		t.Fatalf("events sawError=%v sawDone=%v", sawError, sawDone)
	}
	if message.StopReason != "error" || !strings.Contains(message.ErrorMessage, "Error Code rate_limit: too fast") {
		t.Fatalf("message=%#v", message)
	}
	if errorMessage != message.ErrorMessage {
		t.Fatalf("error event message=%q final=%q", errorMessage, message.ErrorMessage)
	}
}

func TestOpenAIResponsesStreamUnknownStatusEmitsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_unknown","role":"assistant","status":"in_progress","content":[]}}` + "\n\n" +
				`data: {"type":"response.output_text.delta","item_id":"msg_unknown","output_index":0,"delta":"partial"}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_unknown","status":"provider_made_this_up"}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
			API:      "openai-responses",
			BaseURL:  server.URL + "/v1",
			Input:    []string{"text"},
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
	if !sawError || message.StopReason != "error" || !strings.Contains(message.ErrorMessage, "provider_made_this_up") || MessageText(message) != "partial" {
		t.Fatalf("message=%#v sawError=%v", message, sawError)
	}
}

func TestOpenAIResponsesStreamResponseFailedPreservesPartial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_failed","role":"assistant","status":"in_progress","content":[]}}` + "\n\n" +
				`data: {"type":"response.output_text.delta","item_id":"msg_failed","output_index":0,"delta":"partial"}` + "\n\n" +
				`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"bad_request","message":"nope"}}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
			API:      "openai-responses",
			BaseURL:  server.URL + "/v1",
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
	})
	var sawError bool
	for event := range stream.Events() {
		if event.Type == "error" {
			sawError = true
		}
	}
	message := stream.Result()
	if !sawError {
		t.Fatal("missing error event")
	}
	if message.StopReason != "error" || message.ErrorMessage != "bad_request: nope" || MessageText(message) != "partial" || message.ResponseID != "resp_failed" {
		t.Fatalf("message=%#v", message)
	}
}

func TestOpenAIResponsesStreamAggregatesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_s","call_id":"call_s","name":"lookup","arguments":""}}` + "\n\n" +
				`data: {"type":"response.function_call_arguments.delta","item_id":"fc_s","output_index":0,"delta":"{\"q\":"}` + "\n\n" +
				`data: {"type":"response.function_call_arguments.done","item_id":"fc_s","output_index":0,"arguments":"{\"q\":\"go\"}"}` + "\n\n" +
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_s","call_id":"call_s","name":"lookup","arguments":"{\"q\":\"go\"}"}}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_s","status":"completed","output":[{"type":"function_call","id":"fc_s","call_id":"call_s","name":"lookup","arguments":"{\"q\":\"go\"}"}]}}` + "\n\n" +
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
			API:      "openai-responses",
			BaseURL:  server.URL + "/v1",
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
		Tools:    ToolSet{"lookup": cacheTestToolDef("lookup")},
	})
	var toolDeltas []string
	for event := range stream.Events() {
		if event.Type == "toolcall_delta" {
			toolDeltas = append(toolDeltas, event.Delta)
		}
	}
	message := stream.Result()
	blocks := MessageBlocks(message)
	if len(blocks) != 1 || blocks[0].Type != "toolCall" || blocks[0].ID != "call_s|fc_s" || blocks[0].Name != "lookup" {
		t.Fatalf("blocks=%#v", blocks)
	}
	var args map[string]string
	if err := json.Unmarshal(blocks[0].Arguments, &args); err != nil || args["q"] != "go" {
		t.Fatalf("arguments=%s", blocks[0].Arguments)
	}
	if message.StopReason != "toolUse" || strings.Join(toolDeltas, "") != `{"q":"go"}` {
		t.Fatalf("stopReason=%q toolDeltas=%#v", message.StopReason, toolDeltas)
	}
}

func TestOpenAIResponsesStreamItemsWithoutOutputIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.output_item.done","item":{"type":"message","id":"msg_noidx","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}}` + "\n\n" +
				`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_noidx","call_id":"call_noidx","name":"lookup","arguments":"{\"q\":\"go\"}"}}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_noidx","status":"completed"}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai",
			ID:       "gpt-test",
			API:      "openai-responses",
			BaseURL:  server.URL + "/v1",
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
		Tools:    ToolSet{"lookup": cacheTestToolDef("lookup")},
	})
	for range stream.Events() {
	}
	message := stream.Result()
	blocks := MessageBlocks(message)
	if len(blocks) != 2 || blocks[0].Text != "hello" || blocks[1].ID != "call_noidx|fc_noidx" || blocks[1].Name != "lookup" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if message.StopReason != "toolUse" || message.ResponseID != "resp_noidx" {
		t.Fatalf("message=%#v", message)
	}
}

func TestOpenAICodexResponsesStreamEmitsDeltas(t *testing.T) {
	var captured map[string]any
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			http.Error(w, "websocket unavailable", http.StatusBadGateway)
			return
		}
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.created","response":{"id":"resp_codex_stream","status":"in_progress"}}` + "\n\n" +
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_codex_stream","role":"assistant","status":"in_progress","content":[]}}` + "\n\n" +
				`data: {"type":"response.output_text.delta","item_id":"msg_codex_stream","output_index":0,"delta":"cod"}` + "\n\n" +
				`data: {"type":"response.output_text.delta","item_id":"msg_codex_stream","output_index":0,"delta":"ex"}` + "\n\n" +
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_codex_stream","role":"assistant","status":"completed","content":[{"type":"output_text","text":"codex"}]}}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_codex_stream","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai-codex",
			ID:       "gpt-5-codex",
			API:      "openai-codex-responses",
			BaseURL:  server.URL + "/backend-api",
			Input:    []string{"text"},
		},
		Messages:  []Message{NewUserMessage("hi", nil)},
		Transport: "auto",
		SessionID: "codex-stream-session",
	})
	var deltas []string
	for event := range stream.Events() {
		if event.Type == "text_delta" {
			deltas = append(deltas, event.Delta)
		}
	}
	message := stream.Result()
	if path != "/backend-api/codex/responses" {
		t.Fatalf("path=%q", path)
	}
	if captured["stream"] != true {
		t.Fatalf("stream flag=%#v", captured["stream"])
	}
	if strings.Join(deltas, "") != "codex" || MessageText(message) != "codex" || message.ResponseID != "resp_codex_stream" {
		t.Fatalf("deltas=%#v message=%#v", deltas, message)
	}
	if len(message.Diagnostics) != 1 || message.Diagnostics[0].Type != "provider_transport_failure" || message.Diagnostics[0].Details["fallbackTransport"] != "sse" {
		t.Fatalf("diagnostics=%#v", message.Diagnostics)
	}
}

func TestOpenAICodexResponsesSSERetriesRetryAfterBeforeStart(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After-Ms", "1")
			http.Error(w, "temporary overload", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.output_text.delta","delta":"ok","output_index":0,"item_id":"msg_retry"}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_retry","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai-codex",
			ID:       "gpt-5-codex",
			API:      "openai-codex-responses",
			BaseURL:  server.URL + "/backend-api",
			Input:    []string{"text"},
		},
		Messages:   []Message{NewUserMessage("hi", nil)},
		Transport:  "sse",
		MaxRetries: 1,
	})
	for range stream.Events() {
	}
	message := stream.Result()
	if attempts.Load() != 2 {
		t.Fatalf("attempts=%d want 2", attempts.Load())
	}
	if message.StopReason != "stop" || MessageText(message) != "ok" {
		t.Fatalf("message=%#v", message)
	}
}

func TestOpenAICodexResponsesUsageLimitFriendlyErrorDoesNotRetry(t *testing.T) {
	var attempts atomic.Int32
	resetAt := time.Now().Add(2 * time.Minute).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprintf(w, `{"error":{"code":"usage_limit_reached","message":"Monthly usage limit reached","plan_type":"plus","resets_at":%d}}`, resetAt)
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai-codex",
			ID:       "gpt-5-codex",
			API:      "openai-codex-responses",
			BaseURL:  server.URL + "/backend-api",
			Input:    []string{"text"},
		},
		Messages:   []Message{NewUserMessage("hi", nil)},
		Transport:  "sse",
		MaxRetries: 3,
	})
	for range stream.Events() {
	}
	message := stream.Result()
	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d want 1", attempts.Load())
	}
	if message.StopReason != "error" || !strings.Contains(message.ErrorMessage, "You have hit your ChatGPT usage limit (plus plan).") || !strings.Contains(message.ErrorMessage, "Try again in ~") {
		t.Fatalf("message=%#v", message)
	}
}

func TestOpenAICodexResponsesStreamlessUsageLimitFriendlyError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"usage_not_included","message":"usage not included","plan_type":"free"}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai-codex",
			ID:       "gpt-5-codex",
			API:      "openai-codex-responses",
			BaseURL:  server.URL + "/backend-api",
			Input:    []string{"text"},
		},
		Messages:  []Message{NewUserMessage("hi", nil)},
		Transport: "sse",
	})
	if err == nil || !strings.Contains(err.Error(), "You have hit your ChatGPT usage limit (free plan).") {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenAICodexResponsesSSEHeaderTimeout(t *testing.T) {
	old := openAICodexSSEHeaderTimeout
	openAICodexSSEHeaderTimeout = 10 * time.Millisecond
	t.Cleanup(func() { openAICodexSSEHeaderTimeout = old })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"late","status":"completed"}}` + "\n\n"))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai-codex",
			ID:       "gpt-5-codex",
			API:      "openai-codex-responses",
			BaseURL:  server.URL + "/backend-api",
			Input:    []string{"text"},
		},
		Messages:  []Message{NewUserMessage("hi", nil)},
		Transport: "sse",
	})
	for range stream.Events() {
	}
	message := stream.Result()
	if message.StopReason != "error" || !strings.Contains(message.ErrorMessage, "Codex SSE response headers timed out after 10ms") {
		t.Fatalf("message=%#v", message)
	}
}

func TestValidateOpenAICodexResponsesTransport(t *testing.T) {
	codex := Model{API: "openai-codex-responses"}
	for _, accepted := range []string{"", "auto", "sse", "websocket", "websocket-cached"} {
		if err := validateOpenAICodexResponsesTransport(ChatRequest{Model: codex, Transport: accepted}); err != nil {
			t.Fatalf("transport %q should be accepted: %v", accepted, err)
		}
	}
	err := validateOpenAICodexResponsesTransport(ChatRequest{Model: codex, Transport: "grpc"})
	if err == nil {
		t.Fatal("unsupported transport should be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported openai-codex-responses transport") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Transport is only validated for the codex API; other APIs ignore it.
	if err := validateOpenAICodexResponsesTransport(ChatRequest{Model: Model{API: "openai-responses"}, Transport: "grpc"}); err != nil {
		t.Fatalf("non-codex transport should be ignored: %v", err)
	}
}

func TestOpenAICodexResponsesWebSocketTransportFallsBackToSSE(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			http.Error(w, "websocket unavailable", http.StatusBadGateway)
			return
		}
		called = true
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.output_text.delta","delta":"fallback","output_index":0,"item_id":"msg_ws"}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_ws","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai-codex",
			ID:       "gpt-5-codex",
			API:      "openai-codex-responses",
			BaseURL:  server.URL + "/backend-api",
			Input:    []string{"text"},
		},
		Messages:  []Message{NewUserMessage("hi", nil)},
		Transport: "websocket-cached",
	})
	var event AssistantMessageEvent
	for next := range stream.Events() {
		event = next
	}
	message := stream.Result()
	if !called {
		t.Fatal("server should be called for SSE fallback")
	}
	if event.Type != "done" || message.StopReason != "stop" || MessageText(message) != "fallback" {
		t.Fatalf("event=%#v message=%#v", event, message)
	}
	if len(message.Diagnostics) != 1 || message.Diagnostics[0].Type != "provider_transport_failure" || message.Diagnostics[0].Details["configuredTransport"] != "websocket-cached" || message.Diagnostics[0].Details["fallbackTransport"] != "sse" {
		t.Fatalf("diagnostics=%#v", message.Diagnostics)
	}
}

func TestOpenAICodexResponsesStickySSEFallbackKeepsDiagnostic(t *testing.T) {
	sessionID := "sticky-sse-session"
	ResetOpenAICodexWebSocketDebugStats(sessionID)
	CloseOpenAICodexWebSocketSessions(sessionID)
	t.Cleanup(func() {
		CloseOpenAICodexWebSocketSessions(sessionID)
		ResetOpenAICodexWebSocketDebugStats(sessionID)
	})

	var wsAttempts atomic.Int32
	var sseRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			wsAttempts.Add(1)
			http.Error(w, "websocket unavailable", http.StatusBadGateway)
			return
		}
		n := sseRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"resp_%d","status":"completed","output":[{"type":"message","id":"msg_%d","content":[{"type":"output_text","text":"fallback %d"}]}]}`, n, n, n)
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	model := Model{
		Provider: "openai-codex",
		ID:       "gpt-5-codex",
		API:      "openai-codex-responses",
		BaseURL:  server.URL + "/backend-api",
		Input:    []string{"text"},
	}
	first, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:     model,
		Messages:  []Message{NewUserMessage("first", nil)},
		Transport: "auto",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:     model,
		Messages:  []Message{NewUserMessage("second", nil)},
		Transport: "auto",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if MessageText(first.Message) != "fallback 1" || MessageText(second.Message) != "fallback 2" {
		t.Fatalf("responses=%q/%q", MessageText(first.Message), MessageText(second.Message))
	}
	if wsAttempts.Load() != 1 {
		t.Fatalf("sticky fallback should skip second websocket attempt, got %d attempts", wsAttempts.Load())
	}
	if len(second.Message.Diagnostics) != 1 || second.Message.Diagnostics[0].Type != "provider_transport_failure" || second.Message.Diagnostics[0].Details["fallbackTransport"] != "sse" {
		t.Fatalf("sticky fallback diagnostics=%#v", second.Message.Diagnostics)
	}
}

func TestOpenAICodexResponsesWebSocketTransportStreams(t *testing.T) {
	ResetOpenAICodexWebSocketDebugStats("ws-session")
	CloseOpenAICodexWebSocketSessions("ws-session")
	t.Cleanup(func() {
		CloseOpenAICodexWebSocketSessions("ws-session")
		ResetOpenAICodexWebSocketDebugStats("ws-session")
	})

	var captured map[string]any
	var headers http.Header
	var path string
	var fallbackCalled bool
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			fallbackCalled = true
			http.Error(w, "unexpected fallback", http.StatusInternalServerError)
			return
		}
		headers = r.Header.Clone()
		path = r.URL.Path
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			t.Error(err)
			return
		}
		for _, event := range []string{
			`{"type":"response.created","response":{"id":"resp_ws_stream","status":"in_progress"}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_ws_stream","role":"assistant","status":"in_progress","content":[]}}`,
			`{"type":"response.output_text.delta","item_id":"msg_ws_stream","output_index":0,"delta":"web"}`,
			`{"type":"response.output_text.delta","item_id":"msg_ws_stream","output_index":0,"delta":"socket"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_ws_stream","role":"assistant","status":"completed","content":[{"type":"output_text","text":"websocket"}]}}`,
			`{"type":"response.completed","response":{"id":"resp_ws_stream","status":"completed","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}`,
		} {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(event)); err != nil {
				t.Error(err)
				return
			}
		}
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "openai-codex",
			ID:       "gpt-5-codex",
			API:      "openai-codex-responses",
			BaseURL:  server.URL + "/backend-api",
			Input:    []string{"text"},
		},
		Messages:  []Message{NewUserMessage("hi", nil)},
		Transport: "websocket",
		SessionID: "ws-session",
	})
	var deltas []string
	for event := range stream.Events() {
		if event.Type == "text_delta" {
			deltas = append(deltas, event.Delta)
		}
	}
	message := stream.Result()
	if fallbackCalled {
		t.Fatal("SSE fallback should not be called")
	}
	if path != "/backend-api/codex/responses" {
		t.Fatalf("path=%q", path)
	}
	if headers.Get("session-id") != "ws-session" || headers.Get("x-client-request-id") != "ws-session" || headers.Get("chatgpt-account-id") != "acct-123" {
		t.Fatalf("headers=%#v", headers)
	}
	if headers.Get("OpenAI-Beta") != "" || headers.Get("Content-Type") != "" {
		t.Fatalf("websocket headers should omit SSE-only headers: %#v", headers)
	}
	if captured["type"] != "response.create" || captured["stream"] != true {
		t.Fatalf("captured=%#v", captured)
	}
	if strings.Join(deltas, "") != "websocket" || MessageText(message) != "websocket" || message.ResponseID != "resp_ws_stream" {
		t.Fatalf("deltas=%#v message=%#v", deltas, message)
	}
	if len(message.Diagnostics) != 0 {
		t.Fatalf("diagnostics=%#v", message.Diagnostics)
	}
	stats, ok := GetOpenAICodexWebSocketDebugStats("ws-session")
	if !ok || stats.Requests != 1 || stats.ConnectionsCreated != 1 || stats.FullContextRequests != 1 {
		t.Fatalf("stats=%#v ok=%v", stats, ok)
	}
}

func TestOpenAICodexResponsesWebSocketCachedSendsInputDelta(t *testing.T) {
	ResetOpenAICodexWebSocketDebugStats("session-1")
	CloseOpenAICodexWebSocketSessions("session-1")
	t.Cleanup(func() {
		CloseOpenAICodexWebSocketSessions("session-1")
		ResetOpenAICodexWebSocketDebugStats("session-1")
	})

	var requests []map[string]any
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			http.Error(w, "unexpected fallback", http.StatusInternalServerError)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		responses := []struct {
			responseID string
			messageID  string
			text       string
		}{
			{responseID: "resp_1", messageID: "msg_1", text: "first answer"},
			{responseID: "resp_2", messageID: "msg_2", text: "second answer"},
		}
		for _, response := range responses {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var request map[string]any
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Error(err)
				return
			}
			requests = append(requests, request)
			done := fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"status":"completed","output":[{"type":"message","id":%q,"role":"assistant","status":"completed","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}}`, response.responseID, response.messageID, response.text)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(done)); err != nil {
				t.Error(err)
				return
			}
		}
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	model := Model{
		Provider: "openai-codex",
		ID:       "gpt-5-codex",
		API:      "openai-codex-responses",
		BaseURL:  server.URL + "/backend-api",
		Input:    []string{"text"},
	}
	first := registry.StreamChat(context.Background(), ChatRequest{
		Model:     model,
		Messages:  []Message{NewUserMessage("first", nil)},
		Transport: "websocket-cached",
		SessionID: "session-1",
	}).Result()
	second := registry.StreamChat(context.Background(), ChatRequest{
		Model:     model,
		Messages:  []Message{NewUserMessage("first", nil), first, NewUserMessage("second", nil)},
		Transport: "websocket-cached",
		SessionID: "session-1",
	}).Result()
	if MessageText(first) != "first answer" || MessageText(second) != "second answer" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%#v", requests)
	}
	if requests[0]["previous_response_id"] != nil {
		t.Fatalf("first request should be full context: %#v", requests[0])
	}
	if requests[1]["previous_response_id"] != "resp_1" {
		t.Fatalf("second request should reference previous response: %#v", requests[1])
	}
	input, ok := requests[1]["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("delta input=%#v", requests[1]["input"])
	}
	stats, ok := GetOpenAICodexWebSocketDebugStats("session-1")
	if !ok || stats.Requests != 2 || stats.ConnectionsCreated != 1 || stats.ConnectionsReused != 1 || stats.DeltaRequests != 1 || stats.LastPreviousResponseID != "resp_1" {
		t.Fatalf("stats=%#v ok=%v", stats, ok)
	}
	if stats.LastDeltaInputItems == nil || *stats.LastDeltaInputItems != 1 {
		t.Fatalf("last delta input items=%#v", stats.LastDeltaInputItems)
	}
}

func TestOpenAICodexWebSocketConcurrentSameSessionAcquireDoesNotLeak(t *testing.T) {
	sessionID := "ws-race-session"
	ResetOpenAICodexWebSocketDebugStats(sessionID)
	CloseOpenAICodexWebSocketSessions(sessionID)
	t.Cleanup(func() {
		CloseOpenAICodexWebSocketSessions(sessionID)
		ResetOpenAICodexWebSocketDebugStats(sessionID)
	})

	var entered atomic.Int32
	var enteredOnce sync.Once
	bothEntered := make(chan struct{})
	releaseUpgrades := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseUpgrades) })
	closed := make(chan struct{}, 2)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			http.Error(w, "expected websocket", http.StatusBadRequest)
			return
		}
		if entered.Add(1) == 2 {
			enteredOnce.Do(func() { close(bothEntered) })
		}
		<-releaseUpgrades
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
		closed <- struct{}{}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	acquired := make(chan openAICodexWebSocketAcquire, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			acq, err := acquireOpenAICodexWebSocket(ctx, wsURL, nil, sessionID)
			if err != nil {
				errs <- err
				return
			}
			acquired <- acq
		}()
	}
	select {
	case <-bothEntered:
	case <-time.After(time.Second):
		t.Fatal("websocket dials did not overlap")
	}
	releaseOnce.Do(func() { close(releaseUpgrades) })
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("websocket acquires did not finish")
	}
	close(acquired)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("acquire failed: %v", err)
		}
	}

	count := 0
	for acq := range acquired {
		count++
		acq.release(true)
	}
	if count != 2 {
		t.Fatalf("acquired %d connections, want 2", count)
	}
	CloseOpenAICodexWebSocketSessions(sessionID)
	for i := 0; i < 2; i++ {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatalf("only observed %d closed websocket(s), want 2", i)
		}
	}
}

func TestOpenAICodexResponsesSSE(t *testing.T) {
	var captured map[string]any
	var headers http.Header
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"type":"response.output_item.done","item":{"type":"message","id":"msg_c","content":[{"type":"output_text","text":"codex"}]}}` + "\n\n" +
				`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_c","call_id":"call_c","name":"edit","arguments":"{\"path\":\"a\"}"}}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_c","status":"completed","usage":{"input_tokens":4,"output_tokens":6,"total_tokens":10,"input_tokens_details":{"cached_tokens":1}}}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai-codex", codexTestJWT("acct-123"))
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "openai-codex",
			ID:        "gpt-5-codex",
			API:       "openai-codex-responses",
			BaseURL:   server.URL + "/backend-api",
			Input:     []string{"text"},
			Reasoning: true,
		},
		SystemPrompt:   "codex system",
		Messages:       []Message{NewUserMessage("hi", nil)},
		Tools:          ToolSet{"read": cacheTestToolDef("read")},
		ThinkingLevel:  ThinkingMedium,
		CacheRetention: "none",
		SessionID:      "codex-session",
		Transport:      "sse",
		Metadata:       map[string]any{"textVerbosity": "medium"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/backend-api/codex/responses" {
		t.Fatalf("path=%q", path)
	}
	if headers.Get("chatgpt-account-id") != "acct-123" {
		t.Fatalf("account header=%q", headers.Get("chatgpt-account-id"))
	}
	if headers.Get("OpenAI-Beta") != "responses=experimental" || headers.Get("session-id") != "codex-session" {
		t.Fatalf("headers=%#v", headers)
	}
	if captured["instructions"] != "codex system" || captured["stream"] != true || captured["tool_choice"] != "auto" {
		t.Fatalf("codex body=%#v", captured)
	}
	if captured["prompt_cache_key"] != "codex-session" {
		t.Fatalf("prompt_cache_key=%#v", captured["prompt_cache_key"])
	}
	text := captured["text"].(map[string]any)
	if text["verbosity"] != "medium" {
		t.Fatalf("text=%#v", text)
	}
	reasoning := captured["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" {
		t.Fatalf("reasoning=%#v", reasoning)
	}
	tools := captured["tools"].([]any)
	if _, ok := tools[0].(map[string]any)["strict"]; !ok || tools[0].(map[string]any)["strict"] != nil {
		t.Fatalf("codex tool strict=%#v", tools[0])
	}
	blocks := MessageBlocks(response.Message)
	if len(blocks) != 2 || blocks[0].Text != "codex" || blocks[1].Name != "edit" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if response.Message.Usage.Input != 3 || response.Message.Usage.CacheRead != 1 {
		t.Fatalf("usage=%#v", response.Message.Usage)
	}
}

func codexTestJWT(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`))
	return header + "." + payload + ".sig"
}
