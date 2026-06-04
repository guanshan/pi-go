package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMistralChatPayloadAndToolResponse(t *testing.T) {
	var captured map[string]any
	var capturedHeaders http.Header
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		capturedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"id":"resp_mistral","model":"mistral-actual","choices":[{"message":{"content":[{"type":"text","text":"answer"},{"type":"thinking","thinking":[{"type":"text","text":"scratch"}]}],"tool_calls":[{"id":"abc123XYZ","type":"function","function":{"name":"lookup","arguments":"{\"ok\":true}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("mistral", "test-key")
	temp := 0.2
	assistant := NewAssistantMessage("mistral-conversations", "mistral", "mistral-small-latest", []ContentBlock{
		{Type: "text", Text: "using tool"},
		{Type: "toolCall", ID: "call-with-symbols!!", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
	}, Usage{}, "toolUse")
	toolResult := NewToolResultMessage("call-with-symbols!!", "lookup", []ContentBlock{
		{Type: "text", Text: "result"},
		{Type: "image", MimeType: "image/png", Data: "toolimg"},
	}, nil, false)

	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:         "mistral",
			ID:               "mistral-small-latest",
			API:              "mistral-conversations",
			BaseURL:          server.URL,
			Input:            []string{"text", "image"},
			Reasoning:        true,
			Headers:          map[string]string{"X-Model-Header": "model"},
			ThinkingLevelMap: map[string]*string{"high": stringPtr("medium")},
		},
		SystemPrompt: "system",
		Messages: []Message{
			NewUserMessage("look", []ContentBlock{{Type: "image", MimeType: "image/png", Data: "abc"}}),
			assistant,
			toolResult,
		},
		Tools:          ToolSet{"read": cacheTestToolDef("read")},
		ThinkingLevel:  ThinkingHigh,
		SessionID:      "session-affinity",
		MaxTokens:      55,
		Temperature:    &temp,
		Headers:        map[string]string{"X-Request-Header": "request"},
		CacheRetention: "long",
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturedPath != "/v1/chat/completions" {
		t.Fatalf("path=%q", capturedPath)
	}
	if got := capturedHeaders.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := capturedHeaders.Get("x-affinity"); got != "session-affinity" {
		t.Fatalf("x-affinity=%q", got)
	}
	if got := capturedHeaders.Get("X-Model-Header"); got != "model" {
		t.Fatalf("model header=%q", got)
	}
	if got := capturedHeaders.Get("X-Request-Header"); got != "request" {
		t.Fatalf("request header=%q", got)
	}
	if captured["model"] != "mistral-small-latest" || captured["stream"] != false {
		t.Fatalf("model/stream payload=%#v", captured)
	}
	if got := captured["max_tokens"]; got != float64(55) {
		t.Fatalf("max_tokens=%#v", got)
	}
	if got := captured["temperature"]; got != 0.2 {
		t.Fatalf("temperature=%#v", got)
	}
	if got := captured["reasoning_effort"]; got != "medium" {
		t.Fatalf("reasoning_effort=%#v", got)
	}
	if _, ok := captured["prompt_mode"]; ok {
		t.Fatalf("prompt_mode should be omitted for reasoning-effort models: %#v", captured)
	}

	messages := captured["messages"].([]any)
	if got := messages[0].(map[string]any)["role"]; got != "system" {
		t.Fatalf("first message role=%#v", got)
	}
	userContent := messages[1].(map[string]any)["content"].([]any)
	if got := userContent[1].(map[string]any)["image_url"]; got != "data:image/png;base64,abc" {
		t.Fatalf("user image_url=%#v", got)
	}
	assistantMessage := messages[2].(map[string]any)
	requestToolCall := assistantMessage["tool_calls"].([]any)[0].(map[string]any)
	requestToolID := requestToolCall["id"].(string)
	if requestToolID == "call-with-symbols!!" || len(requestToolID) != 9 || !isAlphaNum(requestToolID) {
		t.Fatalf("normalized tool id=%q", requestToolID)
	}
	requestFunction := requestToolCall["function"].(map[string]any)
	if requestFunction["arguments"] != `{"q":"x"}` {
		t.Fatalf("request tool arguments=%#v", requestFunction["arguments"])
	}
	toolMessage := messages[3].(map[string]any)
	if got := toolMessage["tool_call_id"]; got != requestToolID {
		t.Fatalf("tool result id=%#v, want %q", got, requestToolID)
	}
	toolContent := toolMessage["content"].([]any)
	if got := toolContent[1].(map[string]any)["image_url"]; got != "data:image/png;base64,toolimg" {
		t.Fatalf("tool image_url=%#v", got)
	}
	tools := captured["tools"].([]any)
	function := tools[0].(map[string]any)["function"].(map[string]any)
	if function["strict"] != false {
		t.Fatalf("tool strict=%#v", function["strict"])
	}
	if function["parameters"].(map[string]any)["type"] != "object" {
		t.Fatalf("tool parameters=%#v", function["parameters"])
	}

	if response.Message.StopReason != "toolUse" {
		t.Fatalf("stopReason=%q", response.Message.StopReason)
	}
	if response.Message.ResponseID != "resp_mistral" || response.Message.ResponseModel != "mistral-actual" {
		t.Fatalf("response metadata=%#v", response.Message)
	}
	blocks := MessageBlocks(response.Message)
	if len(blocks) != 3 || blocks[0].Text != "answer" || blocks[1].Thinking != "scratch" || blocks[2].Name != "lookup" {
		t.Fatalf("response blocks=%#v", blocks)
	}
	if len(response.ToolCalls) != 1 || string(response.ToolCalls[0].Arguments) != `{"ok":true}` {
		t.Fatalf("tool calls=%#v", response.ToolCalls)
	}
	if response.Message.Usage.Input != 3 || response.Message.Usage.Output != 5 || response.Message.Usage.TotalTokens != 8 {
		t.Fatalf("usage=%#v", response.Message.Usage)
	}
}

func TestMistralChatPromptModeImageOmissionAndAffinityOverride(t *testing.T) {
	var captured map[string]any
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("mistral", "test-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "mistral",
			ID:        "magistral-small",
			API:       "mistral-conversations",
			BaseURL:   server.URL + "/v1/chat/completions",
			Input:     []string{"text"},
			Reasoning: true,
		},
		Messages: []Message{
			UserMessage{Role: "user", Content: []ContentBlock{{Type: "image", MimeType: "image/png", Data: "abc"}}},
			NewToolResultMessage("tool-call-with-symbols!!", "vision", []ContentBlock{{Type: "image", MimeType: "image/png", Data: "def"}}, nil, true),
		},
		ThinkingLevel: ThinkingHigh,
		SessionID:     "session-affinity",
		Headers:       map[string]string{"x-affinity": "explicit-affinity"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := capturedHeaders.Get("x-affinity"); got != "explicit-affinity" {
		t.Fatalf("x-affinity=%q", got)
	}
	if got := captured["prompt_mode"]; got != "reasoning" {
		t.Fatalf("prompt_mode=%#v", got)
	}
	if _, ok := captured["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be omitted: %#v", captured)
	}
	messages := captured["messages"].([]any)
	userContent := messages[0].(map[string]any)["content"].([]any)
	if got := userContent[0].(map[string]any)["text"]; got != "(image omitted: model does not support images)" {
		t.Fatalf("user omitted image content=%#v", got)
	}
	toolContent := messages[1].(map[string]any)["content"].([]any)
	if got := toolContent[0].(map[string]any)["text"]; got != "[tool error] (tool image omitted: model does not support images)" {
		t.Fatalf("tool omitted image content=%#v", got)
	}
}

// TestMistralChatNonReasoningModelOmitsReasoning locks the parity fix: a
// non-reasoning Mistral model must NOT receive prompt_mode or reasoning_effort
// even when a thinking level is requested. Mirrors mistral.ts shouldUseReasoning
// = model.reasoning && reasoning !== "off".
func TestMistralChatNonReasoningModelOmitsReasoning(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("mistral", "test-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "mistral",
			ID:        "mistral-small-latest",
			API:       "mistral-conversations",
			BaseURL:   server.URL + "/v1/chat/completions",
			Input:     []string{"text"},
			Reasoning: false,
		},
		Messages:      []Message{NewUserMessage("hi", nil)},
		ThinkingLevel: ThinkingHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := captured["prompt_mode"]; ok {
		t.Fatalf("prompt_mode should be omitted for non-reasoning model: %#v", captured)
	}
	if _, ok := captured["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be omitted for non-reasoning model: %#v", captured)
	}
}

func TestMistralChatStreamingDeltas(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"id":"resp_mistral_stream","model":"mistral-stream-actual","choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"scratch"}]}]}}]}` + "\n\n" +
				`data: {"id":"resp_mistral_stream","model":"mistral-stream-actual","choices":[{"delta":{"content":"hel"}}]}` + "\n\n" +
				`data: {"id":"resp_mistral_stream","model":"mistral-stream-actual","choices":[{"delta":{"content":[{"type":"text","text":"lo"}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("mistral", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "mistral",
			ID:       "mistral-small-latest",
			API:      "mistral-conversations",
			BaseURL:  server.URL,
		},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	var deltas []string
	var thinkingDeltas []string
	for event := range stream.Events() {
		switch event.Type {
		case "text_delta":
			deltas = append(deltas, event.Delta)
		case "thinking_delta":
			thinkingDeltas = append(thinkingDeltas, event.Delta)
		}
	}
	message := stream.Result()
	if captured["stream"] != true {
		t.Fatalf("stream payload=%#v", captured)
	}
	if got := strings.Join(deltas, ""); got != "hello" {
		t.Fatalf("deltas=%q", got)
	}
	if got := strings.Join(thinkingDeltas, ""); got != "scratch" {
		t.Fatalf("thinking deltas=%q", got)
	}
	if message.API != "mistral-conversations" || message.Provider != "mistral" {
		t.Fatalf("assistant identity=%#v", message)
	}
	blocks := MessageBlocks(message)
	if len(blocks) != 2 || blocks[0].Thinking != "scratch" || blocks[1].Text != "hello" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if message.ResponseID != "resp_mistral_stream" || message.ResponseModel != "mistral-stream-actual" {
		t.Fatalf("response metadata=%#v", message)
	}
	if message.Usage.Input != 2 || message.Usage.Output != 3 || message.Usage.TotalTokens != 5 {
		t.Fatalf("usage=%#v", message.Usage)
	}
}

func TestStreamOptionsPayloadAndResponseHooks(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("X-Trace", "hooked")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	model := Model{
		Provider: "mistral",
		ID:       "mistral-small-latest",
		API:      "mistral-conversations",
		BaseURL:  server.URL,
	}
	var sawPayload, sawResponse bool
	msg, err := CompleteSimple(context.Background(), model, Context{
		Messages: []Message{NewUserMessage("hello", nil)},
	}, SimpleStreamOptions{
		APIKey: "test-key",
		OnPayload: func(payload any, model Model) (any, error) {
			sawPayload = true
			if model.ID != "mistral-small-latest" {
				t.Fatalf("payload model=%#v", model)
			}
			body := payload.(map[string]any)
			body["extra"] = "yes"
			return body, nil
		},
		OnResponse: func(resp ProviderResponse, model Model) error {
			sawResponse = true
			if model.Provider != "mistral" || resp.Status != http.StatusOK || resp.Headers["X-Trace"] != "hooked" {
				t.Fatalf("response hook resp=%#v model=%#v", resp, model)
			}
			return nil
		},
		TimeoutMs:       5000,
		MaxRetries:      0,
		MaxRetryDelayMs: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawPayload || !sawResponse {
		t.Fatalf("hooks payload=%v response=%v", sawPayload, sawResponse)
	}
	if captured["extra"] != "yes" {
		t.Fatalf("captured=%#v", captured)
	}
	if MessageText(msg) != "ok" {
		t.Fatalf("message=%#v", msg)
	}
}

func TestBuiltinMistralUsesNativeAPI(t *testing.T) {
	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	model, ok := registry.Find("mistral", "mistral-large-latest")
	if !ok {
		t.Fatal("missing mistral-large-latest")
	}
	if model.API != "mistral-conversations" {
		t.Fatalf("api=%q", model.API)
	}
	if model.BaseURL != "https://api.mistral.ai" {
		t.Fatalf("baseURL=%q", model.BaseURL)
	}
}

func isAlphaNum(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func stringPtr(value string) *string {
	return &value
}
