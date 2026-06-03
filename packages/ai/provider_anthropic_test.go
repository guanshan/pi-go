package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicChatUsesMessagesEndpointHeadersAndPayload(t *testing.T) {
	var capturedPath string
	var capturedHeaders http.Header
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "anthropic",
			ID:       "claude-test",
			API:      "anthropic-messages",
			BaseURL:  server.URL,
		},
		SystemPrompt: "system",
		Messages:     []Message{NewUserMessage("hello", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturedPath != "/v1/messages" {
		t.Fatalf("path=%q", capturedPath)
	}
	if got := capturedHeaders.Get("x-api-key"); got != "test-key" {
		t.Fatalf("x-api-key=%q", got)
	}
	if got := capturedHeaders.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version=%q", got)
	}
	if got := capturedHeaders.Get("anthropic-beta"); got != "prompt-caching-2024-07-31" {
		t.Fatalf("anthropic-beta=%q", got)
	}
	if captured["model"] != "claude-test" {
		t.Fatalf("payload=%#v", captured)
	}
	system := captured["system"].([]any)
	systemText := system[0].(map[string]any)
	if systemText["type"] != "text" || systemText["text"] != "system" {
		t.Fatalf("system=%#v", system)
	}
	systemCache := systemText["cache_control"].(map[string]any)
	if systemCache["type"] != "ephemeral" {
		t.Fatalf("system cache_control=%#v", systemCache)
	}
	messages := captured["messages"].([]any)
	first := messages[0].(map[string]any)
	content := first["content"].([]any)
	text := content[0].(map[string]any)
	if first["role"] != "user" || text["type"] != "text" || text["text"] != "hello" {
		t.Fatalf("messages=%#v", messages)
	}
	if text["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("message cache_control=%#v", text["cache_control"])
	}
	if MessageText(response.Message) != "ok" {
		t.Fatalf("message=%#v", response.Message)
	}
}

func TestAnthropicOAuthHeadersToolChoiceAndToolResultImages(t *testing.T) {
	var capturedHeaders http.Header
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "sk-ant-oat-test")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "anthropic",
			ID:       "claude-test",
			API:      "anthropic-messages",
			BaseURL:  server.URL,
			Compat:   OpenAICompat{SupportsEagerToolInputStreaming: boolPtr(false)},
		},
		SystemPrompt: "system",
		Messages: []Message{
			NewToolResultMessage("toolu_1", "lookup", []ContentBlock{
				{Type: "text", Text: "result"},
				{Type: "image", MimeType: "image/png", Data: "toolimg"},
			}, nil, false),
		},
		Tools:      ToolSet{"lookup": cacheTestToolDef("lookup")},
		ToolChoice: map[string]any{"type": "tool", "name": "lookup"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := capturedHeaders.Get("Authorization"); got != "Bearer sk-ant-oat-test" {
		t.Fatalf("Authorization=%q", got)
	}
	if beta := capturedHeaders.Get("anthropic-beta"); !strings.Contains(beta, "oauth-2025-04-20") || !strings.Contains(beta, "claude-code-20250219") || !strings.Contains(beta, "fine-grained-tool-streaming-2025-05-14") {
		t.Fatalf("anthropic-beta=%q", beta)
	}
	if got := capturedHeaders.Get("x-app"); got != "cli" {
		t.Fatalf("x-app=%q", got)
	}
	system := captured["system"].([]any)
	identityBlock := system[0].(map[string]any)
	if !strings.Contains(identityBlock["text"].(string), "Claude Code") {
		t.Fatalf("system identity=%#v", system)
	}
	// P2-3: the OAuth identity block must carry the same cache_control breakpoint
	// as the system prompt block (anthropic.ts:909-923).
	if cc, ok := identityBlock["cache_control"].(map[string]any); !ok || cc["type"] != "ephemeral" {
		t.Fatalf("identity cache_control=%#v", identityBlock["cache_control"])
	}
	toolChoice := captured["tool_choice"].(map[string]any)
	if toolChoice["type"] != "tool" || toolChoice["name"] != "lookup" {
		t.Fatalf("tool_choice=%#v", toolChoice)
	}
	content := captured["messages"].([]any)[0].(map[string]any)["content"].([]any)
	toolResult := content[0].(map[string]any)
	if toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "toolu_1" {
		t.Fatalf("tool result=%#v", toolResult)
	}
	parts := toolResult["content"].([]any)
	if parts[0].(map[string]any)["text"] != "result" || parts[1].(map[string]any)["type"] != "image" {
		t.Fatalf("tool result content=%#v", parts)
	}
}

// Opus 4.7+ rejects non-default temperature. The generated catalog marks
// claude-opus-4-8 with compat.supportsTemperature=false; this asserts the
// Anthropic provider drops the temperature field for it while still sending it
// for a model without that compat. Temperature is also gated on thinking being
// disabled (off), matching anthropic.ts.
func TestAnthropicTemperatureSuppressedForOpus48(t *testing.T) {
	capture := func(model Model) map[string]any {
		t.Helper()
		var captured map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
		}))
		defer server.Close()
		model.BaseURL = server.URL

		registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
		registry.Auth.SetRuntime("anthropic", "test-key")
		temp := 0.7
		if _, err := registry.StreamlessChat(context.Background(), ChatRequest{
			Model:         model,
			Messages:      []Message{NewUserMessage("hi", nil)},
			Temperature:   &temp,
			ThinkingLevel: ThinkingOff,
		}); err != nil {
			t.Fatal(err)
		}
		return captured
	}

	opus48, ok := Find(AllKnownModels(), "anthropic", "claude-opus-4-8")
	if !ok {
		t.Fatal("missing anthropic/claude-opus-4-8")
	}
	if captured := capture(opus48); captured["temperature"] != nil {
		t.Fatalf("opus-4-8 temperature=%#v, want omitted", captured["temperature"])
	}

	// A sibling model without supportsTemperature=false still sends temperature.
	plain := Model{
		Provider: "anthropic",
		ID:       "claude-3-5-sonnet-latest",
		API:      "anthropic-messages",
		Input:    []string{"text"},
	}
	if captured := capture(plain); captured["temperature"] != 0.7 {
		t.Fatalf("plain temperature=%#v, want 0.7", captured["temperature"])
	}
}

func TestAnthropicEagerToolInputStreamingCompat(t *testing.T) {
	capture := func(compat OpenAICompat) (http.Header, map[string]any) {
		t.Helper()
		var capturedHeaders http.Header
		var captured map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedHeaders = r.Header.Clone()
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
		}))
		defer server.Close()

		registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
		registry.Auth.SetRuntime("anthropic", "test-key")
		_, err := registry.StreamlessChat(context.Background(), ChatRequest{
			Model: Model{
				Provider: "anthropic",
				ID:       "claude-opus-4-7",
				API:      "anthropic-messages",
				BaseURL:  server.URL,
				Input:    []string{"text"},
				Compat:   compat,
			},
			Messages:       []Message{NewUserMessage("Use the tool", nil)},
			Tools:          ToolSet{"lookup": cacheTestToolDef("lookup")},
			CacheRetention: "none",
		})
		if err != nil {
			t.Fatal(err)
		}
		return capturedHeaders, captured
	}

	headers, captured := capture(OpenAICompat{})
	tools := captured["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["eager_input_streaming"] != true {
		t.Fatalf("tool=%#v", tool)
	}
	if beta := headers.Get("anthropic-beta"); strings.Contains(beta, "fine-grained-tool-streaming-2025-05-14") {
		t.Fatalf("anthropic-beta=%q", beta)
	}

	headers, captured = capture(OpenAICompat{SupportsEagerToolInputStreaming: boolPtr(false)})
	tools = captured["tools"].([]any)
	tool = tools[0].(map[string]any)
	if _, ok := tool["eager_input_streaming"]; ok {
		t.Fatalf("tool=%#v", tool)
	}
	if beta := headers.Get("anthropic-beta"); !strings.Contains(beta, "fine-grained-tool-streaming-2025-05-14") {
		t.Fatalf("anthropic-beta=%q", beta)
	}
}

func TestAnthropicEmptyThinkingSignatureCompat(t *testing.T) {
	capturePayload := func(allowEmptySignature *bool, thinkingSignature string) map[string]any {
		t.Helper()
		var captured map[string]any
		model := Model{
			Provider:      "xiaomi-token-plan-ams",
			ID:            "mimo-v2.5-pro",
			API:           "anthropic-messages",
			BaseURL:       "http://127.0.0.1:9/anthropic",
			Reasoning:     true,
			Input:         []string{"text"},
			ContextWindow: 1048576,
			MaxOutput:     1024,
		}
		if allowEmptySignature != nil {
			model.Compat.AllowEmptySignature = allowEmptySignature
		}
		registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
		registry.Auth.SetRuntime(model.Provider, "test-key")
		stream := registry.StreamChat(context.Background(), ChatRequest{
			Model: model,
			Messages: []Message{
				NewUserMessage("first", nil),
				NewAssistantMessage("anthropic-messages", model.Provider, model.ID, []ContentBlock{
					{Type: "thinking", Thinking: "internal reasoning", ThinkingSignature: thinkingSignature},
				}, Usage{}, "stop"),
				NewUserMessage("second", nil),
			},
			OnPayload: func(payload any, model Model) (any, error) {
				raw, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(raw, &captured); err != nil {
					return nil, err
				}
				return nil, errors.New("payload captured")
			},
		})
		_ = stream.Result()
		if captured == nil {
			t.Fatal("payload was not captured")
		}
		return captured
	}

	payload := capturePayload(nil, "")
	messages := payload["messages"].([]any)
	assistant := messages[1].(map[string]any)
	content := assistant["content"].([]any)
	block := content[0].(map[string]any)
	if len(content) != 1 || block["type"] != "text" || block["text"] != "internal reasoning" {
		t.Fatalf("default content=%#v", content)
	}

	allow := true
	payload = capturePayload(&allow, " ")
	messages = payload["messages"].([]any)
	assistant = messages[1].(map[string]any)
	content = assistant["content"].([]any)
	block = content[0].(map[string]any)
	if len(content) != 1 || block["type"] != "thinking" || block["thinking"] != "internal reasoning" || block["signature"] != "" {
		t.Fatalf("allowEmptySignature content=%#v", content)
	}
}

func TestAnthropicInterleavedThinkingBetaHeader(t *testing.T) {
	captureBeta := func(metadata map[string]any) string {
		t.Helper()
		var capturedHeaders http.Header
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedHeaders = r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
		}))
		defer server.Close()

		registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
		registry.Auth.SetRuntime("anthropic", "test-key")
		_, err := registry.StreamlessChat(context.Background(), ChatRequest{
			Model: Model{
				Provider:  "anthropic",
				ID:        "claude-sonnet-4-5",
				API:       "anthropic-messages",
				BaseURL:   server.URL,
				Input:     []string{"text"},
				Reasoning: true,
			},
			Messages:      []Message{NewUserMessage("Think, then answer", nil)},
			ThinkingLevel: ThinkingHigh,
			Metadata:      metadata,
		})
		if err != nil {
			t.Fatal(err)
		}
		return capturedHeaders.Get("anthropic-beta")
	}

	if beta := captureBeta(nil); !strings.Contains(beta, "interleaved-thinking-2025-05-14") {
		t.Fatalf("anthropic-beta=%q", beta)
	}
	if beta := captureBeta(map[string]any{"interleavedThinking": false}); strings.Contains(beta, "interleaved-thinking-2025-05-14") {
		t.Fatalf("anthropic-beta=%q", beta)
	}
}

func TestAnthropicAdaptiveRedactedThinkingAndToolResultMerge(t *testing.T) {
	var capturedHeaders http.Header
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"redacted_thinking","data":"opaque-out"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	maxEffort := "max"
	model := Model{
		Provider:         "anthropic",
		ID:               "claude-adaptive",
		API:              "anthropic-messages",
		BaseURL:          server.URL,
		Reasoning:        true,
		ThinkingLevelMap: map[string]*string{"high": &maxEffort},
		Compat: OpenAICompat{
			ForceAdaptiveThinking:           boolPtr(true),
			SupportsEagerToolInputStreaming: boolPtr(false),
		},
	}
	assistant := NewAssistantMessage("anthropic-messages", "anthropic", "claude-adaptive", []ContentBlock{
		{Type: "thinking", Thinking: "[Reasoning redacted]", Signature: "opaque-in", Redacted: true},
		{Type: "thinking", Thinking: "unsig reasoning"},
	}, Usage{}, "stop")
	foreignAssistant := NewAssistantMessage("openai-completions", "openai", "gpt-test", []ContentBlock{
		{Type: "toolCall", ID: "call with symbols!!", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
	}, Usage{}, "toolUse")

	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:         model,
		Messages:      []Message{assistant, foreignAssistant, NewToolResultMessage("call with symbols!!", "lookup", TextBlocks("one"), nil, false), NewToolResultMessage("call with symbols!!", "lookup", TextBlocks("two"), nil, false)},
		Tools:         ToolSet{"lookup": cacheTestToolDef("lookup")},
		ThinkingLevel: ThinkingHigh,
		Metadata:      map[string]any{"thinkingDisplay": "omitted"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if beta := capturedHeaders.Get("anthropic-beta"); !strings.Contains(beta, "fine-grained-tool-streaming-2025-05-14") || strings.Contains(beta, "interleaved-thinking-2025-05-14") {
		t.Fatalf("anthropic-beta=%q", beta)
	}
	thinking := captured["thinking"].(map[string]any)
	if thinking["type"] != "adaptive" || thinking["display"] != "omitted" {
		t.Fatalf("thinking=%#v", thinking)
	}
	outputConfig := captured["output_config"].(map[string]any)
	if outputConfig["effort"] != "max" {
		t.Fatalf("output_config=%#v", outputConfig)
	}
	messages := captured["messages"].([]any)
	firstAssistantContent := messages[0].(map[string]any)["content"].([]any)
	if firstAssistantContent[0].(map[string]any)["type"] != "redacted_thinking" || firstAssistantContent[0].(map[string]any)["data"] != "opaque-in" {
		t.Fatalf("redacted thinking=%#v", firstAssistantContent)
	}
	if firstAssistantContent[1].(map[string]any)["type"] != "text" || firstAssistantContent[1].(map[string]any)["text"] != "unsig reasoning" {
		t.Fatalf("unsigned thinking fallback=%#v", firstAssistantContent)
	}
	toolUse := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if toolUse["id"] != "call_with_symbols__" {
		t.Fatalf("tool use=%#v", toolUse)
	}
	toolResults := messages[2].(map[string]any)["content"].([]any)
	if len(toolResults) != 2 || toolResults[0].(map[string]any)["tool_use_id"] != "call_with_symbols__" || toolResults[1].(map[string]any)["tool_use_id"] != "call_with_symbols__" {
		t.Fatalf("merged tool results=%#v", toolResults)
	}
	blocks := MessageBlocks(response.Message)
	if len(blocks) != 1 || !blocks[0].Redacted || blocks[0].Signature != "opaque-out" || blocks[0].Thinking != "[Reasoning redacted]" {
		t.Fatalf("response blocks=%#v", blocks)
	}
}

func TestAnthropicGeneratedOpus46UsesAdaptiveXHighCompat(t *testing.T) {
	var captured map[string]any
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	model, ok := Find(AllKnownModels(), "anthropic", "claude-opus-4-6")
	if !ok {
		t.Fatal("missing anthropic/claude-opus-4-6")
	}
	model.BaseURL = server.URL

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:         model,
		Messages:      []Message{NewUserMessage("hi", nil)},
		ThinkingLevel: ThinkingXHigh,
	})
	if err != nil {
		t.Fatal(err)
	}

	thinking := captured["thinking"].(map[string]any)
	if thinking["type"] != "adaptive" {
		t.Fatalf("thinking=%#v", thinking)
	}
	outputConfig := captured["output_config"].(map[string]any)
	if outputConfig["effort"] != "max" {
		t.Fatalf("output_config=%#v", outputConfig)
	}
	if beta := capturedHeaders.Get("anthropic-beta"); strings.Contains(beta, "interleaved-thinking-2025-05-14") {
		t.Fatalf("anthropic-beta=%q", beta)
	}
}

func TestAnthropicCopilotDynamicHeaders(t *testing.T) {
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("github-copilot", "copilot-token")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{Provider: "github-copilot", ID: "claude-test", API: "anthropic-messages", BaseURL: server.URL, Input: []string{"text", "image"}},
		Messages: []Message{
			NewUserMessage("look", []ContentBlock{{Type: "image", MimeType: "image/png", Data: "img"}}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturedHeaders.Get("Authorization") != "Bearer copilot-token" {
		t.Fatalf("Authorization=%q", capturedHeaders.Get("Authorization"))
	}
	if capturedHeaders.Get("X-Initiator") != "user" || capturedHeaders.Get("Openai-Intent") != "conversation-edits" || capturedHeaders.Get("Copilot-Vision-Request") != "true" {
		t.Fatalf("copilot headers=%#v", capturedHeaders)
	}
}

func TestAnthropicChatStreamEmitsDeltas(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
				"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hel"}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}` + "\n\n" +
				"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}` + "\n\n" +
				"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":2}}` + "\n\n" +
				"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "anthropic",
			ID:       "claude-test",
			API:      "anthropic-messages",
			BaseURL:  server.URL,
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
	if got := strings.Join(deltas, ""); got != "hello" {
		t.Fatalf("deltas=%q", got)
	}
	if got := MessageText(message); got != "hello" {
		t.Fatalf("message=%q", got)
	}
	if message.Usage.Input != 1 || message.Usage.Output != 2 || message.Usage.TotalTokens != 3 {
		t.Fatalf("usage=%#v", message.Usage)
	}
	if captured["stream"] != true {
		t.Fatalf("stream flag=%#v", captured["stream"])
	}
}

func TestAnthropicChatStreamRedactedThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
				"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"opaque-stream"}}` + "\n\n" +
				"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}` + "\n\n" +
				"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}` + "\n\n" +
				"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "anthropic",
			ID:       "claude-test",
			API:      "anthropic-messages",
			BaseURL:  server.URL,
		},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	sawThinkingStart := false
	for event := range stream.Events() {
		if event.Type == "thinking_start" {
			sawThinkingStart = true
		}
	}
	blocks := MessageBlocks(stream.Result())
	if !sawThinkingStart || len(blocks) != 1 || !blocks[0].Redacted || blocks[0].Signature != "opaque-stream" {
		t.Fatalf("stream blocks=%#v sawThinkingStart=%v", blocks, sawThinkingStart)
	}
}

func TestAnthropicChatStreamAggregatesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message_start\n" +
				`data: {"type":"message_start","message":{}}` + "\n\n" +
				"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":" \"go\"}"}}` + "\n\n" +
				"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}` + "\n\n" +
				"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":1,"output_tokens":2}}` + "\n\n" +
				"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "anthropic",
			ID:       "claude-test",
			API:      "anthropic-messages",
			BaseURL:  server.URL,
		},
		Messages: []Message{NewUserMessage("hello", nil)},
		Tools:    ToolSet{"lookup": cacheTestToolDef("lookup")},
	})
	var toolDelta bool
	for event := range stream.Events() {
		if event.Type == "toolcall_delta" {
			toolDelta = true
		}
	}
	message := stream.Result()
	blocks := MessageBlocks(message)
	if len(blocks) != 1 || blocks[0].Type != "toolCall" || blocks[0].ID != "toolu_1" || blocks[0].Name != "lookup" {
		t.Fatalf("blocks=%#v", blocks)
	}
	var args map[string]string
	if err := json.Unmarshal(blocks[0].Arguments, &args); err != nil || args["q"] != "go" {
		t.Fatalf("arguments=%s", blocks[0].Arguments)
	}
	if message.StopReason != "toolUse" || !toolDelta {
		t.Fatalf("stopReason=%q toolDelta=%v", message.StopReason, toolDelta)
	}
}

func TestAnthropicChatStreamSensitiveStopReasonEmitsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
				"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}` + "\n\n" +
				"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}` + "\n\n" +
				"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"sensitive"},"usage":{"input_tokens":1,"output_tokens":1}}` + "\n\n" +
				"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "anthropic",
			ID:       "claude-test",
			API:      "anthropic-messages",
			BaseURL:  server.URL,
		},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	sawError := false
	for event := range stream.Events() {
		if event.Type == "error" {
			sawError = true
		}
	}
	message := stream.Result()
	// Parity with TS mapStopReason: a "sensitive" stop_reason maps to "error" with
	// no reason-specific text; the stream surfaces the generic provider error
	// message rather than echoing the raw stop_reason word.
	if !sawError || message.StopReason != "error" || message.ErrorMessage == "" {
		t.Fatalf("message=%#v sawError=%v", message, sawError)
	}
}
