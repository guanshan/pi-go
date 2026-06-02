package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

// Unit coverage for the canonical-casing helpers. Mirrors anthropic.ts:97-105
// and the core cases of anthropic-tool-name-normalization.test.ts.
func TestAnthropicToClaudeCodeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"todowrite", "TodoWrite"},
		{"TodoWrite", "TodoWrite"},
		{"read", "Read"},
		{"find", "find"}, // not a CC tool name
		{"my_custom_tool", "my_custom_tool"},
	}
	for _, c := range cases {
		if got := aiproviders.ToClaudeCodeName(c.in); got != c.want {
			t.Fatalf("ToClaudeCodeName(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestAnthropicFromClaudeCodeName(t *testing.T) {
	tools := []map[string]any{
		{"name": "todowrite"},
		{"name": "read"},
		{"name": "find"},
	}
	cases := []struct {
		in   string
		want string
	}{
		{"TodoWrite", "todowrite"}, // back to original casing
		{"Read", "read"},
		{"Glob", "Glob"}, // no tool named glob -> unchanged
		{"find", "find"},
	}
	for _, c := range cases {
		if got := aiproviders.FromClaudeCodeName(c.in, tools); got != c.want {
			t.Fatalf("FromClaudeCodeName(%q)=%q want %q", c.in, got, c.want)
		}
	}
	// Without a tool list nothing is mapped.
	if got := aiproviders.FromClaudeCodeName("TodoWrite", nil); got != "TodoWrite" {
		t.Fatalf("FromClaudeCodeName without tools=%q", got)
	}
}

// Outbound tool schema names use CC canonical casing for OAuth tokens; raw
// names otherwise. Mirrors anthropic.ts:1192.
func TestAnthropicOutboundToolNameNormalization(t *testing.T) {
	captureTools := func(key string) []any {
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

		registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
		registry.Auth.SetRuntime("anthropic", key)
		_, err := registry.StreamlessChat(context.Background(), ChatRequest{
			Model:    Model{Provider: "anthropic", ID: "claude-test", API: "anthropic-messages", BaseURL: server.URL},
			Messages: []Message{NewUserMessage("hi", nil)},
			Tools: ToolSet{
				"todowrite":      cacheTestToolDef("todowrite"),
				"find":           cacheTestToolDef("find"),
				"my_custom_tool": cacheTestToolDef("my_custom_tool"),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return captured["tools"].([]any)
	}

	names := func(tools []any) map[string]bool {
		set := map[string]bool{}
		for _, tool := range tools {
			set[tool.(map[string]any)["name"].(string)] = true
		}
		return set
	}

	oauth := names(captureTools("sk-ant-oat-test"))
	if !oauth["TodoWrite"] || !oauth["find"] || !oauth["my_custom_tool"] {
		t.Fatalf("oauth tool names=%#v", oauth)
	}
	if oauth["todowrite"] {
		t.Fatalf("oauth todowrite should be normalized to TodoWrite: %#v", oauth)
	}

	raw := names(captureTools("test-key"))
	if !raw["todowrite"] || !raw["find"] || !raw["my_custom_tool"] || raw["TodoWrite"] {
		t.Fatalf("non-oauth tool names=%#v", raw)
	}
}

// Outbound assistant-history tool_use names also use CC casing for OAuth.
// Mirrors anthropic.ts:1103.
func TestAnthropicOutboundAssistantToolUseNormalization(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("anthropic", "sk-ant-oat-test")
	assistant := NewAssistantMessage("anthropic-messages", "anthropic", "claude-test", []ContentBlock{
		{Type: "toolCall", ID: "toolu_1", Name: "todowrite", Arguments: json.RawMessage(`{"task":"x"}`)},
	}, Usage{}, "toolUse")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{Provider: "anthropic", ID: "claude-test", API: "anthropic-messages", BaseURL: server.URL},
		Messages: []Message{
			NewUserMessage("hi", nil),
			assistant,
			NewToolResultMessage("toolu_1", "todowrite", TextBlocks("done"), nil, false),
		},
		Tools: ToolSet{"todowrite": cacheTestToolDef("todowrite")},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := captured["messages"].([]any)
	assistantContent := messages[1].(map[string]any)["content"].([]any)
	toolUse := assistantContent[0].(map[string]any)
	if toolUse["name"] != "TodoWrite" {
		t.Fatalf("assistant tool_use name=%#v", toolUse)
	}
}

// Inbound tool_use names map back to the original tool names for OAuth on the
// non-streaming path. Mirrors anthropic.ts:569-576 (applied to ParseAnthropicMessage).
func TestAnthropicInboundToolNameNormalizationNonStream(t *testing.T) {
	parseName := func(key, returnedName string) string {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			body := `{"content":[{"type":"tool_use","id":"toolu_1","name":"` + returnedName + `","input":{}}],"stop_reason":"tool_use"}`
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()

		registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
		registry.Auth.SetRuntime("anthropic", key)
		response, err := registry.StreamlessChat(context.Background(), ChatRequest{
			Model:    Model{Provider: "anthropic", ID: "claude-test", API: "anthropic-messages", BaseURL: server.URL},
			Messages: []Message{NewUserMessage("hi", nil)},
			Tools: ToolSet{
				"todowrite": cacheTestToolDef("todowrite"),
				"find":      cacheTestToolDef("find"),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		blocks := MessageBlocks(response.Message)
		if len(blocks) != 1 || blocks[0].Type != "toolCall" {
			t.Fatalf("blocks=%#v", blocks)
		}
		return blocks[0].Name
	}

	// OAuth: CC casing maps back to the original tool name.
	if got := parseName("sk-ant-oat-test", "TodoWrite"); got != "todowrite" {
		t.Fatalf("oauth TodoWrite inbound=%q", got)
	}
	// "find" is sent and returned unchanged (not a CC tool).
	if got := parseName("sk-ant-oat-test", "find"); got != "find" {
		t.Fatalf("oauth find inbound=%q", got)
	}
	// "Glob" has no matching tool, so it stays "Glob".
	if got := parseName("sk-ant-oat-test", "Glob"); got != "Glob" {
		t.Fatalf("oauth Glob inbound=%q", got)
	}
	// Non-OAuth: names pass through unchanged.
	if got := parseName("test-key", "TodoWrite"); got != "TodoWrite" {
		t.Fatalf("non-oauth TodoWrite inbound=%q", got)
	}
}

// Inbound streaming tool_use names map back for OAuth. Mirrors anthropic.ts:569-576.
func TestAnthropicInboundToolNameNormalizationStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"msg_stream_1","usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
				"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"TodoWrite","input":{}}}` + "\n\n" +
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
	registry.Auth.SetRuntime("anthropic", "sk-ant-oat-test")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model:    Model{Provider: "anthropic", ID: "claude-test", API: "anthropic-messages", BaseURL: server.URL},
		Messages: []Message{NewUserMessage("hi", nil)},
		Tools:    ToolSet{"todowrite": cacheTestToolDef("todowrite")},
	})
	var startName string
	for event := range stream.Events() {
		if event.Type == "toolcall_start" && event.ContentIndex < len(event.Partial.Content) {
			startName = event.Partial.Content[event.ContentIndex].Name
		}
	}
	message := stream.Result()
	blocks := MessageBlocks(message)
	if len(blocks) != 1 || blocks[0].Name != "todowrite" {
		t.Fatalf("stream inbound blocks=%#v", blocks)
	}
	if startName != "todowrite" {
		t.Fatalf("toolcall_start name=%q", startName)
	}
}
