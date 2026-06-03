package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// TestAnthropicRawSSEMalformedJSONRepairAndPostStop ports
// ../pi/packages/ai/test/anthropic-sse-parsing.test.ts.
//
// The TS test feeds a fake raw SSE body through iterateAnthropicEvents. The Go
// port consumes Anthropic streams through the official anthropic-sdk-go
// accumulator (provider_anthropic.go:106), which is the one piece the Go port
// does NOT own. That accumulator rejects an invalid `\H` string escape inside an
// input_json_delta before the port's own repair runs, so the malformed-tool-JSON
// case cannot be exercised faithfully end-to-end (documented in
// docs/TS_COMPATIBILITY.md). Instead the two behaviors are split:
//
//  1. The malformed input_json_delta repair is driven directly through
//     applyAnthropicDelta (provider_anthropic.go:202), which streams tool-call
//     arguments via StreamingToolArguments -> ParseStreamingJSON -> RepairJSON
//     (utils/json_parse.go). This is exactly the delta-application + RepairJSON
//     path the task names.
//  2. The "ignore unknown events after message_stop" behavior is exercised
//     end-to-end through a fake SSE body. Note the mechanism: the anthropic-sdk-go
//     ssestream reader does NOT stop at message_stop — it keeps reading until the
//     HTTP body ends, and unrecognized event types (e.g. a trailing "done" or
//     vendor "proxy.stats" event) simply fall through the SDK's type switch
//     without unmarshalling or erroring, so they never perturb the parsed result.
func TestAnthropicRawSSEMalformedJSONRepairAndPostStop(t *testing.T) {
	t.Run("repairs malformed streamed tool JSON via applyAnthropicDelta", func(t *testing.T) {
		// partial_json bytes: {"path":"A\H","text":"col1<TAB>col2"}
		// \H is an invalid JSON string escape and the tab is a raw control char;
		// both must be repaired by RepairJSON underneath StreamingToolArguments.
		malformedPartial := "{\"path\":\"A\\H\",\"text\":\"col1\tcol2\"}"

		blocks := []ContentBlock{{Type: "toolCall", ID: "toolu_test", Name: "edit", Arguments: json.RawMessage(`{}`)}}
		delta, eventType := applyAnthropicDelta(&blocks, 0, anthropic.MessageStreamEventUnionDelta{
			Type:        "input_json_delta",
			PartialJSON: malformedPartial,
		})
		if eventType != "toolcall_delta" {
			t.Fatalf("eventType=%q, want toolcall_delta", eventType)
		}
		if delta != malformedPartial {
			t.Fatalf("emitted raw delta=%q, want the unmodified partial_json", delta)
		}

		repaired := blocks[0].Arguments
		var args struct {
			Path string `json:"path"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(repaired, &args); err != nil {
			t.Fatalf("streamed tool arguments not valid JSON after repair: %s (%v)", repaired, err)
		}
		if args.Path != `A\H` {
			t.Fatalf("repaired path=%q, want %q", args.Path, `A\H`)
		}
		if args.Text != "col1\tcol2" {
			t.Fatalf("repaired text=%q, want %q", args.Text, "col1\tcol2")
		}
	})

	t.Run("ignores unknown SSE events after message_stop", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(
				"event: message_start\n" +
					`data: {"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":12,"output_tokens":0}}}` + "\n\n" +
					"event: content_block_start\n" +
					`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
					"event: content_block_delta\n" +
					`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n\n" +
					"event: content_block_stop\n" +
					`data: {"type":"content_block_stop","index":0}` + "\n\n" +
					"event: message_delta\n" +
					`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":12,"output_tokens":5}}` + "\n\n" +
					"event: message_stop\n" +
					`data: {"type":"message_stop"}` + "\n\n" +
					// Events after message_stop must be ignored: an unknown event
					// type and a non-JSON data payload.
					"event: done\n" +
					"data: [DONE]\n\n" +
					"event: proxy.stats\n" +
					"data: not json\n\n",
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
			Messages: []Message{NewUserMessage("Say hello.", nil)},
		})
		for range stream.Events() {
		}
		message := stream.Result()
		if message.StopReason != "stop" {
			t.Fatalf("stopReason=%q, want stop", message.StopReason)
		}
		if message.ErrorMessage != "" {
			t.Fatalf("unexpected errorMessage=%q", message.ErrorMessage)
		}
		blocks := MessageBlocks(message)
		if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "Hello" {
			t.Fatalf("blocks=%#v, want single text block 'Hello'", blocks)
		}
	})
}
