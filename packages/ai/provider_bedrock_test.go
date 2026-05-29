package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestBedrockChatPayloadAndParse(t *testing.T) {
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")

	var captured map[string]any
	var capturedHeaders http.Header
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		capturedPath = r.URL.EscapedPath()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{
			"output":{"message":{"role":"assistant","content":[
				{"reasoningContent":{"reasoningText":{"text":"scratch","signature":"sig-out"}}},
				{"text":"answer"},
				{"toolUse":{"toolUseId":"call-out","name":"lookup","input":{"ok":true}}}
			]}},
			"stopReason":"tool_use",
			"usage":{"inputTokens":10,"outputTokens":5,"cacheReadInputTokens":3,"cacheWriteInputTokens":2,"totalTokens":20}
		}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	temp := 0.2
	assistant := NewAssistantMessage("bedrock-converse-stream", "amazon-bedrock", "anthropic.claude-sonnet-4-5-v1:0", []ContentBlock{
		{Type: "text", Text: "using tool"},
		{Type: "thinking", Thinking: "private reasoning", Signature: "sig-in"},
		{Type: "toolCall", ID: "call with symbols!!", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
	}, Usage{}, "toolUse")

	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "amazon-bedrock",
			ID:        "anthropic.claude-sonnet-4-5-v1:0",
			API:       "bedrock-converse-stream",
			BaseURL:   server.URL,
			Input:     []string{"text", "image"},
			Reasoning: true,
			MaxOutput: 123,
			Headers:   map[string]string{"X-Model-Header": "model"},
			Name:      "Claude Sonnet 4.5",
			Cost:      ModelCost{Input: 3},
			Compat:    OpenAICompat{},
			Raw:       nil,
		},
		SystemPrompt: "system",
		Messages: []Message{
			NewUserMessage("look", []ContentBlock{{Type: "image", MimeType: "image/png", Data: "abc"}}),
			assistant,
			NewToolResultMessage("call with symbols!!", "lookup", []ContentBlock{{Type: "text", Text: "result"}}, nil, false),
			NewToolResultMessage("other tool!!", "lookup", []ContentBlock{{Type: "text", Text: "bad"}}, nil, true),
		},
		Tools:          ToolSet{"read": cacheTestToolDef("read")},
		ThinkingLevel:  ThinkingHigh,
		CacheRetention: "long",
		MaxTokens:      77,
		Temperature:    &temp,
		ToolChoice:     map[string]any{"type": "tool", "name": "read"},
		RequestMetadata: map[string]string{
			"cost-center": "ai",
			"session":     "test",
		},
		Headers: map[string]string{"X-Request-Header": "request"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(capturedPath, "/model/anthropic.claude-sonnet-4-5-v1%3A0/converse") {
		t.Fatalf("path=%q", capturedPath)
	}
	if capturedHeaders.Get("Authorization") != "" {
		t.Fatalf("authorization should be omitted with skip auth: %#v", capturedHeaders)
	}
	if capturedHeaders.Get("X-Model-Header") != "model" || capturedHeaders.Get("X-Request-Header") != "request" {
		t.Fatalf("headers=%#v", capturedHeaders)
	}

	system := captured["system"].([]any)
	if system[0].(map[string]any)["text"] != "system" {
		t.Fatalf("system=%#v", system)
	}
	systemCache := system[1].(map[string]any)["cachePoint"].(map[string]any)
	if systemCache["type"] != "default" || systemCache["ttl"] != "1h" {
		t.Fatalf("system cachePoint=%#v", systemCache)
	}
	config := captured["inferenceConfig"].(map[string]any)
	if config["maxTokens"] != float64(77) || config["temperature"] != 0.2 {
		t.Fatalf("inferenceConfig=%#v", config)
	}
	additional := captured["additionalModelRequestFields"].(map[string]any)
	thinking := additional["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(8192) || thinking["display"] != "summarized" {
		t.Fatalf("thinking=%#v", thinking)
	}
	if additional["anthropic_beta"].([]any)[0] != "interleaved-thinking-2025-05-14" {
		t.Fatalf("additional fields=%#v", additional)
	}

	messages := captured["messages"].([]any)
	userContent := messages[0].(map[string]any)["content"].([]any)
	if userContent[0].(map[string]any)["text"] != "look" {
		t.Fatalf("user content=%#v", userContent)
	}
	image := userContent[1].(map[string]any)["image"].(map[string]any)
	if image["format"] != "png" || image["source"].(map[string]any)["bytes"] != "YWJj" {
		t.Fatalf("image=%#v", image)
	}
	assistantContent := messages[1].(map[string]any)["content"].([]any)
	if assistantContent[0].(map[string]any)["text"] != "using tool" {
		t.Fatalf("assistant content=%#v", assistantContent)
	}
	reasoning := assistantContent[1].(map[string]any)["reasoningContent"].(map[string]any)["reasoningText"].(map[string]any)
	if reasoning["text"] != "private reasoning" || reasoning["signature"] != "sig-in" {
		t.Fatalf("assistant reasoning=%#v", reasoning)
	}
	toolUse := assistantContent[2].(map[string]any)["toolUse"].(map[string]any)
	if toolUse["toolUseId"] != "call_with_symbols" || toolUse["name"] != "lookup" {
		t.Fatalf("toolUse=%#v", toolUse)
	}
	toolInput := toolUse["input"].(map[string]any)
	if toolInput["q"] != "x" {
		t.Fatalf("tool input=%#v", toolInput)
	}
	toolResultContent := messages[2].(map[string]any)["content"].([]any)
	firstToolResult := toolResultContent[0].(map[string]any)["toolResult"].(map[string]any)
	if firstToolResult["toolUseId"] != "call_with_symbols" || firstToolResult["status"] != "success" {
		t.Fatalf("first tool result=%#v", firstToolResult)
	}
	secondToolResult := toolResultContent[1].(map[string]any)["toolResult"].(map[string]any)
	if secondToolResult["status"] != "error" {
		t.Fatalf("second tool result=%#v", secondToolResult)
	}
	lastCache := toolResultContent[2].(map[string]any)["cachePoint"].(map[string]any)
	if lastCache["ttl"] != "1h" {
		t.Fatalf("last cachePoint=%#v", lastCache)
	}
	tools := captured["toolConfig"].(map[string]any)["tools"].([]any)
	spec := tools[0].(map[string]any)["toolSpec"].(map[string]any)
	if spec["name"] != "read" || spec["inputSchema"].(map[string]any)["json"].(map[string]any)["type"] != "object" {
		t.Fatalf("tool spec=%#v", spec)
	}
	toolChoice := captured["toolConfig"].(map[string]any)["toolChoice"].(map[string]any)
	if toolChoice["tool"].(map[string]any)["name"] != "read" {
		t.Fatalf("toolChoice=%#v", toolChoice)
	}
	requestMetadata := captured["requestMetadata"].(map[string]any)
	if requestMetadata["cost-center"] != "ai" || requestMetadata["session"] != "test" {
		t.Fatalf("requestMetadata=%#v", requestMetadata)
	}

	blocks := MessageBlocks(response.Message)
	if len(blocks) != 3 || blocks[0].Thinking != "scratch" || blocks[1].Text != "answer" || blocks[2].Name != "lookup" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if blocks[0].Signature != "sig-out" {
		t.Fatalf("thinking signature=%#v", blocks[0])
	}
	if response.Message.StopReason != "toolUse" || len(response.ToolCalls) != 1 {
		t.Fatalf("response=%#v", response)
	}
	if string(response.ToolCalls[0].Arguments) != `{"ok":true}` {
		t.Fatalf("tool call args=%s", response.ToolCalls[0].Arguments)
	}
	if response.Message.Usage.Input != 10 || response.Message.Usage.Output != 5 || response.Message.Usage.CacheRead != 3 || response.Message.Usage.CacheWrite != 2 || response.Message.Usage.TotalTokens != 20 {
		t.Fatalf("usage=%#v", response.Message.Usage)
	}
}

func TestBedrockBearerAuthAndRegion(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-west-1")

	var authorization string
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		path = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"output":{"message":{"content":[{"text":"ok"}]}},"stopReason":"end_turn"}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("amazon-bedrock", "bedrock-token")
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "amazon-bedrock",
			ID:       "amazon.nova-2-lite-v1:0",
			API:      "bedrock-converse-stream",
			BaseURL:  server.URL + "/{region}",
		},
		Messages: []Message{NewUserMessage("hello", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer bedrock-token" {
		t.Fatalf("Authorization=%q", authorization)
	}
	if !strings.Contains(path, "/eu-west-1/model/amazon.nova-2-lite-v1%3A0/converse") {
		t.Fatalf("path=%q", path)
	}
	if MessageText(response.Message) != "ok" {
		t.Fatalf("message=%#v", response.Message)
	}
}

func TestBedrockThinkingDisplayAndInterleavedOption(t *testing.T) {
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"output":{"message":{"content":[{"text":"ok"}]}},"stopReason":"end_turn"}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "amazon-bedrock",
			ID:        "anthropic.claude-sonnet-4-5-v1:0",
			Name:      "Claude Sonnet 4.5",
			API:       "bedrock-converse-stream",
			BaseURL:   server.URL,
			Reasoning: true,
		},
		Messages:      []Message{NewUserMessage("hello", nil)},
		ThinkingLevel: ThinkingHigh,
		Metadata:      map[string]any{"thinkingDisplay": "omitted", "interleavedThinking": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	additional := captured["additionalModelRequestFields"].(map[string]any)
	thinking := additional["thinking"].(map[string]any)
	if thinking["display"] != "omitted" {
		t.Fatalf("thinking=%#v", thinking)
	}
	if _, ok := additional["anthropic_beta"]; ok {
		t.Fatalf("additional fields=%#v", additional)
	}
}

func TestBedrockChatStreamHTTP(t *testing.T) {
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")
	var capturedPath string
	var captured map[string]any
	body := bedrockEventStreamBody(t, []bedrockStreamEvent{
		{typ: "messageStart", payload: `{"role":"assistant"}`},
		{typ: "contentBlockDelta", payload: `{"contentBlockIndex":0,"delta":{"text":"hel"}}`},
		{typ: "contentBlockDelta", payload: `{"contentBlockIndex":0,"delta":{"text":"lo"}}`},
		{typ: "contentBlockStop", payload: `{"contentBlockIndex":0}`},
		{typ: "messageStop", payload: `{"stopReason":"end_turn"}`},
		{typ: "metadata", payload: `{"usage":{"inputTokens":2,"outputTokens":3,"totalTokens":5}}`},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.EscapedPath()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "amazon-bedrock",
			ID:       "amazon.nova-test:0",
			API:      "bedrock-converse-stream",
			BaseURL:  server.URL,
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
	if !strings.HasSuffix(capturedPath, "/model/amazon.nova-test%3A0/converse-stream") {
		t.Fatalf("path=%q", capturedPath)
	}
	if captured["modelId"] != nil {
		t.Fatalf("modelId should be in path, payload=%#v", captured)
	}
	if got := strings.Join(deltas, ""); got != "hello" {
		t.Fatalf("deltas=%q", got)
	}
	if got := MessageText(message); got != "hello" {
		t.Fatalf("message=%q", got)
	}
	if message.Usage.Input != 2 || message.Usage.Output != 3 || message.Usage.TotalTokens != 5 {
		t.Fatalf("usage=%#v", message.Usage)
	}
}

type bedrockStreamEvent struct {
	typ     string
	payload string
}

func bedrockEventStreamBody(t *testing.T, events []bedrockStreamEvent) []byte {
	t.Helper()
	var buffer bytes.Buffer
	encoder := eventstream.NewEncoder()
	for _, event := range events {
		message := eventstream.Message{Payload: []byte(event.payload)}
		message.Headers.Set(eventstreamapi.MessageTypeHeader, eventstream.StringValue(eventstreamapi.EventMessageType))
		message.Headers.Set(eventstreamapi.EventTypeHeader, eventstream.StringValue(event.typ))
		message.Headers.Set(eventstreamapi.ContentTypeHeader, eventstream.StringValue("application/json"))
		if err := encoder.Encode(&buffer, message); err != nil {
			t.Fatal(err)
		}
	}
	return buffer.Bytes()
}

func TestBedrockHasAuthSources(t *testing.T) {
	model := Model{Provider: "amazon-bedrock", API: "bedrock-converse-stream"}

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	if registry.HasAuth(model) {
		t.Fatal("expected no Bedrock auth by default")
	}
	registry.Auth.SetRuntime("amazon-bedrock", "token")
	if !registry.HasAuth(model) {
		t.Fatal("expected runtime Bedrock bearer token to satisfy auth")
	}

	registry.Auth.RuntimeKey = map[string]string{}
	t.Setenv("AWS_ACCESS_KEY_ID", "access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	if !registry.HasAuth(model) {
		t.Fatal("expected AWS access key credentials to satisfy auth")
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")

	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "profile", env: map[string]string{"AWS_PROFILE": "dev"}},
		{name: "bearer", env: map[string]string{"AWS_BEARER_TOKEN_BEDROCK": "bedrock-token"}},
		{name: "ecs", env: map[string]string{"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI": "/creds"}},
		{name: "irsa", env: map[string]string{"AWS_WEB_IDENTITY_TOKEN_FILE": "/tmp/token"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
			if !registry.HasAuth(model) {
				t.Fatalf("expected %s environment to satisfy auth", tc.name)
			}
		})
	}
}

func TestBedrockStreamEventsEmitDeltas(t *testing.T) {
	stream := NewAssistantMessageEventStream(32)
	partial := NewAssistantMessage("bedrock-converse-stream", "amazon-bedrock", "anthropic.claude-test", nil, Usage{}, "stop")
	blocks := []ContentBlock{}
	blockByIndex := map[int32]int{}
	events := []bedrocktypes.ConverseStreamOutput{
		&bedrocktypes.ConverseStreamOutputMemberMessageStart{Value: bedrocktypes.MessageStartEvent{Role: bedrocktypes.ConversationRoleAssistant}},
		&bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{Value: bedrocktypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &bedrocktypes.ContentBlockDeltaMemberReasoningContent{Value: &bedrocktypes.ReasoningContentBlockDeltaMemberText{Value: "think"}},
		}},
		&bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{Value: bedrocktypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &bedrocktypes.ContentBlockDeltaMemberReasoningContent{Value: &bedrocktypes.ReasoningContentBlockDeltaMemberSignature{Value: "sig"}},
		}},
		&bedrocktypes.ConverseStreamOutputMemberContentBlockStop{Value: bedrocktypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
		&bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{Value: bedrocktypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(1),
			Delta:             &bedrocktypes.ContentBlockDeltaMemberText{Value: "hel"},
		}},
		&bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{Value: bedrocktypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(1),
			Delta:             &bedrocktypes.ContentBlockDeltaMemberText{Value: "lo"},
		}},
		&bedrocktypes.ConverseStreamOutputMemberContentBlockStop{Value: bedrocktypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(1)}},
		&bedrocktypes.ConverseStreamOutputMemberMessageStop{Value: bedrocktypes.MessageStopEvent{StopReason: bedrocktypes.StopReasonEndTurn}},
		&bedrocktypes.ConverseStreamOutputMemberMetadata{Value: bedrocktypes.ConverseStreamMetadataEvent{Usage: &bedrocktypes.TokenUsage{
			InputTokens:           aws.Int32(5),
			OutputTokens:          aws.Int32(3),
			CacheReadInputTokens:  aws.Int32(1),
			CacheWriteInputTokens: aws.Int32(2),
			TotalTokens:           aws.Int32(10),
		}}},
	}
	for _, event := range events {
		if err := bedrockApplyStreamOutput(event, &partial, &blocks, blockByIndex, stream); err != nil {
			t.Fatal(err)
		}
	}
	stream.End(partial)

	var textDeltas []string
	var thinkingDeltas []string
	for event := range stream.Events() {
		switch event.Type {
		case "text_delta":
			textDeltas = append(textDeltas, event.Delta)
		case "thinking_delta":
			thinkingDeltas = append(thinkingDeltas, event.Delta)
		}
	}
	if got := strings.Join(textDeltas, ""); got != "hello" {
		t.Fatalf("text deltas=%q", got)
	}
	if got := strings.Join(thinkingDeltas, ""); got != "think" {
		t.Fatalf("thinking deltas=%q", got)
	}
	if len(blocks) != 2 || blocks[0].Thinking != "think" || blocks[0].Signature != "sig" || blocks[1].Text != "hello" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if partial.StopReason != "stop" {
		t.Fatalf("stopReason=%q", partial.StopReason)
	}
	if partial.Usage.Input != 5 || partial.Usage.Output != 3 || partial.Usage.CacheRead != 1 || partial.Usage.CacheWrite != 2 || partial.Usage.TotalTokens != 10 {
		t.Fatalf("usage=%#v", partial.Usage)
	}
}

func TestBedrockStreamEventsAggregateToolCalls(t *testing.T) {
	stream := NewAssistantMessageEventStream(32)
	partial := NewAssistantMessage("bedrock-converse-stream", "amazon-bedrock", "anthropic.claude-test", nil, Usage{}, "stop")
	blocks := []ContentBlock{}
	blockByIndex := map[int32]int{}
	events := []bedrocktypes.ConverseStreamOutput{
		&bedrocktypes.ConverseStreamOutputMemberContentBlockStart{Value: bedrocktypes.ContentBlockStartEvent{
			ContentBlockIndex: aws.Int32(0),
			Start: &bedrocktypes.ContentBlockStartMemberToolUse{Value: bedrocktypes.ToolUseBlockStart{
				ToolUseId: aws.String("call-b"),
				Name:      aws.String("lookup"),
			}},
		}},
		&bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{Value: bedrocktypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &bedrocktypes.ContentBlockDeltaMemberToolUse{Value: bedrocktypes.ToolUseBlockDelta{Input: aws.String(`{"q":`)}},
		}},
		&bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{Value: bedrocktypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &bedrocktypes.ContentBlockDeltaMemberToolUse{Value: bedrocktypes.ToolUseBlockDelta{Input: aws.String(`"go"}`)}},
		}},
		&bedrocktypes.ConverseStreamOutputMemberContentBlockStop{Value: bedrocktypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
		&bedrocktypes.ConverseStreamOutputMemberMessageStop{Value: bedrocktypes.MessageStopEvent{StopReason: bedrocktypes.StopReasonToolUse}},
	}
	for _, event := range events {
		if err := bedrockApplyStreamOutput(event, &partial, &blocks, blockByIndex, stream); err != nil {
			t.Fatal(err)
		}
	}
	stream.End(partial)

	var toolDelta bool
	for event := range stream.Events() {
		if event.Type == "toolcall_delta" {
			toolDelta = true
		}
	}
	if len(blocks) != 1 || blocks[0].Type != "toolCall" || blocks[0].ID != "call-b" || blocks[0].Name != "lookup" {
		t.Fatalf("blocks=%#v", blocks)
	}
	var args map[string]string
	if err := json.Unmarshal(blocks[0].Arguments, &args); err != nil || args["q"] != "go" {
		t.Fatalf("arguments=%s", blocks[0].Arguments)
	}
	if blocks[0].Data != "" {
		t.Fatalf("scratch data leaked: %#v", blocks[0])
	}
	if partial.StopReason != "toolUse" || !toolDelta {
		t.Fatalf("stopReason=%q toolDelta=%v", partial.StopReason, toolDelta)
	}
}
