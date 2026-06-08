package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newOpenAIResponsesGoldenServer(t *testing.T, captured *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_golden","status":"completed","output":[{"type":"message","id":"msg_g","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`))
	}))
}

// TestOpenAIResponsesImageContentPayloadGolden locks the OpenAI Responses
// request wire-shape for a user message that carries an image content block. The
// image branch of providers/openai_responses.go (ResponsesUserMessage, ~349-352)
// emits {type:"input_image", detail:"auto", image_url:"data:..."}, mirroring the
// TS upstream (../pi/packages/ai/src/providers/openai-responses-shared.ts
// :143-155). Run with UPDATE_GOLDEN=1 to regenerate the Go snapshot.
func TestOpenAIResponsesImageContentPayloadGolden(t *testing.T) {
	var captured map[string]any
	server := newOpenAIResponsesGoldenServer(t, &captured)
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	model := Model{
		Provider: "openai",
		ID:       "gpt-image-golden",
		API:      "openai-responses",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text", "image"},
	}
	user := UserMessage{Role: "user", Content: []ContentBlock{
		{Type: "text", Text: "describe this"},
		{Type: "image", MimeType: "image/png", Data: "aGVsbG8="},
	}}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:          model,
		SystemPrompt:   "system prompt",
		Messages:       []Message{user},
		CacheRetention: "none",
		MaxTokens:      64,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGolden(t, captured, "openai_responses_image_content_payload.golden.json")
}

// TestOpenAIResponsesToolResultImagesPayloadGolden locks the OpenAI Responses
// request wire-shape for tool results that carry images. A text+image tool
// result becomes a function_call_output whose output is an [input_text,
// input_image] content array; an image-only tool result emits the
// "(see attached image)" string fallback. providers/openai_responses.go
// (ResponsesToolResultMessage, ~428-458) ports the TS converter in
// ../pi/packages/ai/src/providers/openai-responses-shared.ts:221-260. Run with
// UPDATE_GOLDEN=1 to regenerate the Go snapshot.
func TestOpenAIResponsesToolResultImagesPayloadGolden(t *testing.T) {
	var captured map[string]any
	server := newOpenAIResponsesGoldenServer(t, &captured)
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	model := Model{
		Provider: "openai",
		ID:       "gpt-image-golden",
		API:      "openai-responses",
		BaseURL:  server.URL + "/v1",
		Input:    []string{"text", "image"},
	}
	assistant := NewAssistantMessageForModel(model, []ContentBlock{
		{Type: "toolCall", ID: "call_a", Name: "read", Arguments: json.RawMessage(`{"path":"a.png"}`)},
		{Type: "toolCall", ID: "call_b", Name: "read", Arguments: json.RawMessage(`{"path":"b.png"}`)},
	}, Usage{}, "toolUse")
	textImageResult := NewToolResultMessage("call_a", "read", []ContentBlock{
		{Type: "text", Text: "a red circle"},
		{Type: "image", MimeType: "image/png", Data: "aGVsbG8="},
	}, nil, false)
	imageOnlyResult := NewToolResultMessage("call_b", "read", []ContentBlock{
		{Type: "image", MimeType: "image/png", Data: "d29ybGQ="},
	}, nil, false)
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:        model,
		SystemPrompt: "system prompt",
		Messages: []Message{
			NewUserMessage("read the images", nil),
			assistant,
			textImageResult,
			imageOnlyResult,
		},
		CacheRetention: "none",
		MaxTokens:      64,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGolden(t, captured, "openai_responses_tool_result_images_payload.golden.json")
}

// TestOpenAIResponsesReasoningReplayPayloadGolden locks the OpenAI Responses
// request wire-shape when a same-model assistant turn replays a thinking block
// whose signature is an encrypted reasoning item plus a following tool call whose
// id encodes the "call_id|item_id" pair. providers/openai_responses.go
// (ResponsesAssistantItems, ~366-406) replays the reasoning item verbatim and
// splits the tool-call id into call_id + item-id, mirroring the TS upstream
// (../pi/packages/ai/src/providers/openai-responses-shared.ts:171-217). Run with
// UPDATE_GOLDEN=1 to regenerate the Go snapshot.
func TestOpenAIResponsesReasoningReplayPayloadGolden(t *testing.T) {
	var captured map[string]any
	server := newOpenAIResponsesGoldenServer(t, &captured)
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("openai", "test-key")
	model := Model{
		Provider:  "openai",
		ID:        "gpt-reasoning-golden",
		API:       "openai-responses",
		BaseURL:   server.URL + "/v1",
		Reasoning: true,
	}
	// The thinking block's signature is the JSON encoding of an OpenAI reasoning
	// item; it is replayed verbatim as a `reasoning` input item.
	reasoningItem := `{"type":"reasoning","id":"rs_abc","summary":[],"encrypted_content":"ENC"}`
	assistant := NewAssistantMessageForModel(model, []ContentBlock{
		{Type: "thinking", Thinking: "thinking...", Signature: reasoningItem},
		{Type: "toolCall", ID: "call_x|fc_x", Name: "lookup", Arguments: json.RawMessage(`{"query":"pi"}`)},
	}, Usage{}, "toolUse")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model:        model,
		SystemPrompt: "system prompt",
		Messages: []Message{
			NewUserMessage("please reason", nil),
			assistant,
			NewToolResultMessage("call_x|fc_x", "lookup", TextBlocks("result text"), nil, false),
			NewUserMessage("continue", nil),
		},
		Tools: ToolSet{
			"lookup": {
				Name:        "lookup",
				Description: "Look up docs",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
					"required": []string{"query"},
				},
			},
		},
		ThinkingLevel:  ThinkingHigh,
		CacheRetention: "none",
		MaxTokens:      64,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGolden(t, captured, "openai_responses_reasoning_replay_payload.golden.json")
}
