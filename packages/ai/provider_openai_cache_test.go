package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIChatPromptCachePayload(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	temp := 0.4
	model := Model{
		Provider: "openai",
		ID:       "gpt-test",
		API:      "openai-completions",
		BaseURL:  server.URL + "/api.openai.com/v1/chat/completions",
		Compat:   OpenAICompat{MaxTokensField: "max_completion_tokens"},
	}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:          model,
		SystemPrompt:   "system",
		Messages:       []Message{NewUserMessage("hello", nil)},
		CacheRetention: "long",
		SessionID:      strings.Repeat("x", 67),
		MaxTokens:      123,
		Temperature:    &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := captured["prompt_cache_key"]; got != strings.Repeat("x", 64) {
		t.Fatalf("prompt_cache_key=%#v", got)
	}
	if got := captured["prompt_cache_retention"]; got != "24h" {
		t.Fatalf("prompt_cache_retention=%#v", got)
	}
	if got := captured["max_completion_tokens"]; got != float64(123) {
		t.Fatalf("max_completion_tokens=%#v", got)
	}
	if got := captured["temperature"]; got != 0.4 {
		t.Fatalf("temperature=%#v", got)
	}
	if _, ok := captured["max_tokens"]; ok {
		t.Fatalf("max_tokens should be omitted when max_completion_tokens is selected: %#v", captured)
	}
}

func TestOpenAIChatDeveloperRoleThinkingToolChoiceAndCachedUsage(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_meta",
			"model":"gpt-actual",
			"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":4}}
		}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "openai",
			ID:        "gpt-test",
			API:       "openai-completions",
			BaseURL:   server.URL + "/v1/chat/completions",
			Reasoning: true,
		},
		SystemPrompt:  "system",
		Messages:      []Message{NewUserMessage("hello", nil)},
		Tools:         ToolSet{"read": cacheTestToolDef("read")},
		ToolChoice:    map[string]any{"type": "function", "function": map[string]any{"name": "read"}},
		ThinkingLevel: ThinkingHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := captured["messages"].([]any)
	if got := messages[0].(map[string]any)["role"]; got != "developer" {
		t.Fatalf("system role=%#v", got)
	}
	if captured["store"] != false || captured["reasoning_effort"] != "high" {
		t.Fatalf("thinking/store payload=%#v", captured)
	}
	toolChoice := captured["tool_choice"].(map[string]any)
	if toolChoice["type"] != "function" || toolChoice["function"].(map[string]any)["name"] != "read" {
		t.Fatalf("tool_choice=%#v", toolChoice)
	}
	if response.Message.API != "openai-completions" || response.Message.Provider != "openai" {
		t.Fatalf("assistant identity=%#v", response.Message)
	}
	if response.Message.ResponseID != "chatcmpl_meta" || response.Message.ResponseModel != "gpt-actual" {
		t.Fatalf("response metadata=%#v", response.Message)
	}
	if response.Message.Usage.Input != 6 || response.Message.Usage.CacheRead != 4 || response.Message.Usage.Output != 3 || response.Message.Usage.TotalTokens != 13 {
		t.Fatalf("usage=%#v", response.Message.Usage)
	}
}

func TestOpenAICompatibleGeneratedDeepSeekXHighMapsToMax(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	model, ok := Find(AllKnownModels(), "deepseek", "deepseek-v4-flash")
	if !ok {
		t.Fatal("missing deepseek/deepseek-v4-flash")
	}
	model.BaseURL = server.URL

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("deepseek", "test-key")
	if _, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:         model,
		Messages:      []Message{NewUserMessage("hi", nil)},
		ThinkingLevel: ThinkingXHigh,
	}); err != nil {
		t.Fatal(err)
	}
	if captured["reasoning_effort"] != "max" {
		t.Fatalf("captured=%#v", captured)
	}
}

// OpenRouter-hosted DeepSeek V4 is served through OpenRouter's normalized
// reasoning protocol, not DeepSeek's native API. The generated catalog carries
// requiresReasoningContentOnAssistantMessages (compat) and a {xhigh:"xhigh"}
// thinkingLevelMap, but no thinkingFormat override, so the openai-completions
// provider falls back to the OpenRouter thinking format and maps xhigh via the
// catalog. This mirrors openai-completions.ts (isDeepSeek is provider/baseURL
// based, so an OpenRouter model is never treated as native DeepSeek).
func TestOpenRouterGeneratedDeepSeekV4UsesOpenRouterThinkingFormat(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	model, ok := Find(AllKnownModels(), "openrouter", "deepseek/deepseek-v4-flash")
	if !ok {
		t.Fatal("missing openrouter/deepseek/deepseek-v4-flash")
	}
	model.BaseURL = server.URL

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openrouter", "test-key")
	if _, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:         model,
		Messages:      []Message{NewUserMessage("hi", nil)},
		ThinkingLevel: ThinkingXHigh,
	}); err != nil {
		t.Fatal(err)
	}
	reasoning, ok := captured["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "xhigh" {
		t.Fatalf("captured=%#v, want reasoning.effort=xhigh", captured)
	}
	if _, ok := captured["thinking"]; ok {
		t.Fatalf("captured thinking=%#v, want OpenRouter reasoning object", captured["thinking"])
	}
	if _, ok := captured["reasoning_effort"]; ok {
		t.Fatalf("captured reasoning_effort=%#v, want nested reasoning object", captured["reasoning_effort"])
	}
}

func TestOpenAICompatibleChatUsesHTTPStreamingWhenSDKDoesNotMatch(t *testing.T) {
	var captured map[string]any
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"id":"chatcmpl_stream","model":"gpt-stream-actual","choices":[{"delta":{"content":"hel"}}]}` + "\n\n" +
				`data: {"id":"chatcmpl_stream","model":"gpt-stream-actual","choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":3}}}` + "\n\n" +
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
			BaseURL:  server.URL + "/compat-route",
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	var deltas []string
	for event := range stream.Events() {
		if event.Type == "text_delta" {
			deltas = append(deltas, event.Delta)
		}
	}
	message := stream.Result()
	if capturedPath != "/compat-route" {
		t.Fatalf("path=%q", capturedPath)
	}
	if captured["stream"] != true {
		t.Fatalf("stream payload=%#v", captured)
	}
	if captured["stream_options"].(map[string]any)["include_usage"] != true {
		t.Fatalf("stream_options=%#v", captured["stream_options"])
	}
	if got := strings.Join(deltas, ""); got != "hello" {
		t.Fatalf("deltas=%q", got)
	}
	if message.Usage.Input != 5 || message.Usage.CacheRead != 3 || message.Usage.Output != 2 || message.Usage.TotalTokens != 10 {
		t.Fatalf("usage=%#v", message.Usage)
	}
	if message.ResponseID != "chatcmpl_stream" || message.ResponseModel != "gpt-stream-actual" {
		t.Fatalf("response metadata=%#v", message)
	}
}

func TestOpenAICompatibleChatReasoningDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_reasoning",
			"model":"gpt-reason-actual",
			"choices":[{
				"message":{
					"content":"",
					"reasoning":"inspect",
					"tool_calls":[{
						"id":"call_1",
						"type":"function",
						"function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}
					}],
					"reasoning_details":[{"type":"reasoning.encrypted","id":"call_1","data":"secret"}]
				},
				"finish_reason":"tool_calls"
			}]
		}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("opencode-go", "test-key")
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "opencode-go",
			ID:        "gpt-reason",
			API:       "openai-completions",
			BaseURL:   server.URL + "/v1/chat/completions",
			Reasoning: true,
		},
		Messages: []Message{NewUserMessage("hello", nil)},
		Tools:    ToolSet{"lookup": cacheTestToolDef("lookup")},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := MessageBlocks(response.Message)
	if len(blocks) != 2 || blocks[0].Type != "thinking" || blocks[0].Thinking != "inspect" || blocks[0].Signature != "reasoning_content" {
		t.Fatalf("thinking block=%#v", blocks)
	}
	if blocks[1].Type != "toolCall" || blocks[1].Name != "lookup" || string(blocks[1].Arguments) != `{"q":"x"}` || !strings.Contains(blocks[1].ThoughtSignature, `"secret"`) {
		t.Fatalf("tool block=%#v", blocks[1])
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].ThoughtSignature != blocks[1].ThoughtSignature {
		t.Fatalf("tool calls=%#v", response.ToolCalls)
	}
	if response.Message.ResponseID != "chatcmpl_reasoning" || response.Message.ResponseModel != "gpt-reason-actual" {
		t.Fatalf("response metadata=%#v", response.Message)
	}
}

func TestOpenAIChatThinkingAsTextReplayParts(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("repro-provider", "test-key")
	assistant := NewAssistantMessage("openai-completions", "repro-provider", "repro-model", []ContentBlock{
		{Type: "thinking", Thinking: "internal reasoning"},
		{Type: "text", Text: "visible answer"},
	}, Usage{}, "stop")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "repro-provider",
			ID:        "repro-model",
			API:       "openai-completions",
			BaseURL:   server.URL + "/chat/completions",
			Reasoning: true,
			Compat:    OpenAICompat{RequiresThinkingAsText: boolPtr(true)},
		},
		Messages: []Message{
			NewUserMessage("hello", nil),
			assistant,
			NewUserMessage("continue", nil),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := captured["messages"].([]any)
	content := messages[1].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("assistant content=%#v", content)
	}
	if content[0].(map[string]any)["text"] != "internal reasoning" || content[1].(map[string]any)["text"] != "visible answer" {
		t.Fatalf("assistant content=%#v", content)
	}
}

func TestOpenAIChatReasoningReplayJoinsThinkingBlocks(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("opencode-go", "test-key")
	assistant := NewAssistantMessage("openai-completions", "opencode-go", "gpt-oss", []ContentBlock{
		{Type: "thinking", Thinking: "first", Signature: "reasoning"},
		{Type: "thinking", Thinking: "second", Signature: "reasoning"},
		{Type: "text", Text: "visible"},
	}, Usage{}, "stop")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "opencode-go",
			ID:        "gpt-oss",
			API:       "openai-completions",
			BaseURL:   server.URL + "/v1/chat/completions",
			Reasoning: true,
		},
		Messages: []Message{
			NewUserMessage("hello", nil),
			assistant,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := captured["messages"].([]any)
	assistantMessage := messages[1].(map[string]any)
	if assistantMessage["content"] != "visible" {
		t.Fatalf("assistant content=%#v", assistantMessage)
	}
	if assistantMessage["reasoning_content"] != "first\nsecond" {
		t.Fatalf("assistant reasoning=%#v", assistantMessage)
	}
}

func TestOpenAICompatibleChatStreamingReasoningAndToolSignatures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"id":"chatcmpl_stream_reason","model":"gpt-stream-reason-actual","choices":[{"delta":{"reasoning_content":"think "}}]}` + "\n\n" +
				`data: {"id":"chatcmpl_stream_reason","model":"gpt-stream-reason-actual","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}` + "\n\n" +
				`data: {"id":"chatcmpl_stream_reason","model":"gpt-stream-reason-actual","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}],"reasoning_details":[{"type":"reasoning.encrypted","id":"call_1","data":"secret"}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":3}}}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "openai",
			ID:        "gpt-stream-reason",
			API:       "openai-completions",
			BaseURL:   server.URL + "/compat-route",
			Reasoning: true,
		},
		Messages: []Message{NewUserMessage("hello", nil)},
		Tools:    ToolSet{"lookup": cacheTestToolDef("lookup")},
	})
	var thinkingDeltas []string
	for event := range stream.Events() {
		if event.Type == "thinking_delta" {
			thinkingDeltas = append(thinkingDeltas, event.Delta)
		}
	}
	message := stream.Result()
	if got := strings.Join(thinkingDeltas, ""); got != "think " {
		t.Fatalf("thinking deltas=%q", got)
	}
	blocks := MessageBlocks(message)
	if len(blocks) != 2 || blocks[0].Type != "thinking" || blocks[0].Thinking != "think " || blocks[0].Signature != "reasoning_content" {
		t.Fatalf("thinking block=%#v", blocks)
	}
	if blocks[1].Type != "toolCall" || blocks[1].Name != "lookup" || string(blocks[1].Arguments) != `{"q":"x"}` || !strings.Contains(blocks[1].ThoughtSignature, `"secret"`) {
		t.Fatalf("tool block=%#v", blocks[1])
	}
	if message.StopReason != "toolUse" {
		t.Fatalf("stopReason=%q", message.StopReason)
	}
	if message.ResponseID != "chatcmpl_stream_reason" || message.ResponseModel != "gpt-stream-reason-actual" {
		t.Fatalf("response metadata=%#v", message)
	}
	if message.Usage.Input != 5 || message.Usage.CacheRead != 3 || message.Usage.Output != 2 || message.Usage.TotalTokens != 10 {
		t.Fatalf("usage=%#v", message.Usage)
	}
}

func TestOpenRouterAnthropicCacheControlAndAffinity(t *testing.T) {
	var captured map[string]any
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openrouter", "test-key")
	model := Model{
		Provider: "openrouter",
		ID:       "anthropic/claude-sonnet-4.5",
		API:      "openai-completions",
		BaseURL:  server.URL + "/openrouter.ai/api/v1/chat/completions",
		Compat: OpenAICompat{
			SendSessionAffinityHeaders: true,
			OpenRouterRouting:          map[string]any{"only": []string{"anthropic"}, "allow_fallbacks": false},
		},
	}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:          model,
		SystemPrompt:   "system",
		Messages:       []Message{NewUserMessage("hello", nil)},
		Tools:          ToolSet{"read": cacheTestToolDef("read")},
		CacheRetention: "long",
		SessionID:      "session-affinity",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"session_id", "x-client-request-id", "x-session-affinity"} {
		if got := capturedHeaders.Get(header); got != "session-affinity" {
			t.Fatalf("%s header=%q", header, got)
		}
	}
	assertMessageCacheControl(t, captured, 0, "ephemeral", "1h")
	messages := captured["messages"].([]any)
	assertContentCacheControl(t, messages[len(messages)-1].(map[string]any), "ephemeral", "1h")
	tools := captured["tools"].([]any)
	toolCache := tools[len(tools)-1].(map[string]any)["cache_control"].(map[string]any)
	if toolCache["type"] != "ephemeral" || toolCache["ttl"] != "1h" {
		t.Fatalf("tool cache_control=%#v", toolCache)
	}
	provider := captured["provider"].(map[string]any)
	if provider["allow_fallbacks"] != false {
		t.Fatalf("provider routing=%#v", provider)
	}
}

func TestOpenAIChatCacheRetentionNoneOmitsCacheControls(t *testing.T) {
	var captured map[string]any
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openrouter", "test-key")
	model := Model{
		Provider: "openrouter",
		ID:       "anthropic/claude-sonnet-4.5",
		API:      "openai-completions",
		BaseURL:  server.URL,
		Compat:   OpenAICompat{SendSessionAffinityHeaders: true},
	}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:          model,
		SystemPrompt:   "system",
		Messages:       []Message{NewUserMessage("hello", nil)},
		CacheRetention: "none",
		SessionID:      "session-affinity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := captured["prompt_cache_key"]; ok {
		t.Fatalf("prompt_cache_key should be omitted: %#v", captured)
	}
	if capturedHeaders.Get("session_id") != "" || capturedHeaders.Get("x-session-affinity") != "" {
		t.Fatalf("affinity headers should be omitted: %#v", capturedHeaders)
	}
	messages := captured["messages"].([]any)
	first := messages[0].(map[string]any)
	if _, ok := first["content"].([]any); ok {
		t.Fatalf("system content should stay a string without cache control: %#v", first["content"])
	}
}

func cacheTestToolDef(name string) Tool {
	return Tool{
		Name:        name,
		Description: "Read a file",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []string{"path"},
		},
	}
}

func assertMessageCacheControl(t *testing.T, payload map[string]any, index int, typ, ttl string) {
	t.Helper()
	messages := payload["messages"].([]any)
	assertContentCacheControl(t, messages[index].(map[string]any), typ, ttl)
}

func assertContentCacheControl(t *testing.T, message map[string]any, typ, ttl string) {
	t.Helper()
	content := message["content"].([]any)
	part := content[0].(map[string]any)
	cacheControl := part["cache_control"].(map[string]any)
	if cacheControl["type"] != typ || cacheControl["ttl"] != ttl {
		t.Fatalf("cache_control=%#v", cacheControl)
	}
}
