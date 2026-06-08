package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssistantMessageJSONUsageShape(t *testing.T) {
	emptyUsage := NewAssistantMessage("faux", "faux", "faux", TextBlocks("ok"), Usage{}, "stop")
	raw, err := json.Marshal(emptyUsage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"usage"`) || !strings.Contains(string(raw), `"totalTokens":0`) {
		t.Fatalf("zero usage should be present: %s", raw)
	}

	withUsage := NewAssistantMessage("faux", "faux", "faux", TextBlocks("ok"), Usage{Input: 2, Output: 3, TotalTokens: 5}, "stop")
	raw, err = json.Marshal(withUsage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"usage"`) || !strings.Contains(string(raw), `"totalTokens":5`) {
		t.Fatalf("assistant usage should be preserved: %s", raw)
	}
}

func TestModelJSONUsesMaxTokensShape(t *testing.T) {
	model := Model{Provider: "local", ID: "coder", API: "openai-completions", MaxOutput: 2048}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"maxTokens":2048`) || strings.Contains(string(raw), `"maxOutput"`) {
		t.Fatalf("model json should use maxTokens: %s", raw)
	}

	var fromMaxTokens Model
	if err := json.Unmarshal([]byte(`{"provider":"local","id":"coder","api":"openai-completions","maxTokens":4096}`), &fromMaxTokens); err != nil {
		t.Fatal(err)
	}
	if fromMaxTokens.MaxOutput != 4096 {
		t.Fatalf("MaxOutput from maxTokens=%d", fromMaxTokens.MaxOutput)
	}

	var fromMaxOutput Model
	if err := json.Unmarshal([]byte(`{"provider":"local","id":"coder","api":"openai-completions","maxOutput":1024}`), &fromMaxOutput); err != nil {
		t.Fatal(err)
	}
	if fromMaxOutput.MaxOutput != 1024 {
		t.Fatalf("MaxOutput fallback=%d", fromMaxOutput.MaxOutput)
	}
}

func TestContextUnmarshalRestoresMessageUnion(t *testing.T) {
	raw := []byte(`{
		"systemPrompt":"system",
		"messages":[
			{"role":"user","content":"hello","timestamp":1},
			{"role":"assistant","content":[{"type":"text","text":"hi"}],"api":"faux","provider":"faux","model":"faux","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2},
			{"role":"toolResult","toolCallId":"call_1","toolName":"lookup","content":[{"type":"text","text":"result"}],"isError":false,"timestamp":3}
		],
		"tools":[{"name":"lookup","parameters":{"type":"object"}}]
	}`)
	var ctx Context
	if err := json.Unmarshal(raw, &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.SystemPrompt != "system" || len(ctx.Messages) != 3 || len(ctx.Tools) != 1 {
		t.Fatalf("context=%#v", ctx)
	}
	if _, ok := ctx.Messages[0].(UserMessage); !ok {
		t.Fatalf("message[0] type=%T", ctx.Messages[0])
	}
	if _, ok := ctx.Messages[1].(AssistantMessage); !ok {
		t.Fatalf("message[1] type=%T", ctx.Messages[1])
	}
	if _, ok := ctx.Messages[2].(ToolResultMessage); !ok {
		t.Fatalf("message[2] type=%T", ctx.Messages[2])
	}
	if MessageText(ctx.Messages[0]) != "hello" || MessageText(ctx.Messages[1]) != "hi" || MessageToolCallID(ctx.Messages[2]) != "call_1" {
		t.Fatalf("messages lost fields: %#v", ctx.Messages)
	}
}

func TestMessageHelpers(t *testing.T) {
	msg := NewUserMessage("describe", []ContentBlock{{Type: "image", Data: "abc", MimeType: "image/png"}})
	if got := MessageText(msg); got != "describe" {
		t.Fatalf("text=%q", got)
	}

	blocks := MessageBlocks(UserMessage{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello"}}})
	if len(blocks) != 1 || blocks[0].Text != "hello" {
		t.Fatalf("blocks=%#v", blocks)
	}
}

func TestCustomContentBlocksFallsBackToJSONForInvalidBlockArrays(t *testing.T) {
	blocks, ok := CustomContentBlocks([]any{
		map[string]any{"foo": "bar"},
	})
	if !ok {
		t.Fatal("custom content should be representable")
	}
	if len(blocks) != 1 || blocks[0].Type != "text" || !strings.Contains(blocks[0].Text, `"foo":"bar"`) {
		t.Fatalf("blocks=%#v", blocks)
	}
	for _, block := range blocks {
		if block.Type == "" {
			t.Fatalf("empty content block leaked: %#v", blocks)
		}
	}
}

func TestMessageDiscriminatedUnionHelpers(t *testing.T) {
	user := Message(NewUserMessage("hello", nil))
	assistant := Message(NewAssistantMessage("openai-completions", "openai", "gpt-test", []ContentBlock{
		{Type: "thinking", Thinking: "hidden"},
		{Type: "text", Text: "visible"},
	}, Usage{}, "stop"))
	tool := Message(NewToolResultMessage("call-1", "read", TextBlocks("result"), nil, false))

	if MessageRole(user) != "user" || MessageRole(assistant) != "assistant" || MessageRole(tool) != "toolResult" {
		t.Fatalf("roles: %q %q %q", MessageRole(user), MessageRole(assistant), MessageRole(tool))
	}
	if got := MessageText(assistant); got != "visible" {
		t.Fatalf("assistant text should exclude thinking, got %q", got)
	}
	if got := AssistantThinkingText(assistant); got != "hidden" {
		t.Fatalf("thinking text=%q", got)
	}
	if MessageToolCallID(tool) != "call-1" || MessageToolName(tool) != "read" {
		t.Fatalf("tool fields lost: %#v", tool)
	}
}

func TestCompatAccessorsDetectProviderDefaults(t *testing.T) {
	openRouter := GetOpenAICompletionsCompat(Model{
		Provider: "openrouter",
		ID:       "anthropic/claude-sonnet-4.5",
		BaseURL:  "https://openrouter.ai/api/v1",
	})
	if openRouter.CacheControlFormat != "anthropic" || openRouter.ThinkingFormat != "openrouter" {
		t.Fatalf("openrouter compat=%#v", openRouter)
	}

	anthropic := GetAnthropicMessagesCompat(Model{
		Provider: "fireworks",
		BaseURL:  "https://api.fireworks.ai/inference/v1",
	})
	if anthropic.SupportsLongCacheRetention || anthropic.SupportsCacheControlOnTools || !anthropic.SendSessionAffinityHeaders {
		t.Fatalf("anthropic compat=%#v", anthropic)
	}

	sendSessionID := false
	responses := GetOpenAIResponsesCompat(Model{Compat: OpenAICompat{SendSessionIDHeader: &sendSessionID}})
	if responses.SendSessionIDHeader || !responses.SupportsLongCacheRetention {
		t.Fatalf("responses compat=%#v", responses)
	}
}
