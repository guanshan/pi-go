package providers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestProviderMessagesPreserveCustomRolesAsUser(t *testing.T) {
	const text = "summary carried through direct provider call"

	anthropicOut := AnthropicMessages([]AnthropicMessage{{Role: "branchSummary", Text: text}}, anthropic.CacheControlEphemeralParam{}, false, false, false)
	if len(anthropicOut) != 1 || string(anthropicOut[0].Role) != "user" || !jsonContains(t, anthropicOut[0], text) {
		t.Fatalf("anthropic messages=%#v", anthropicOut)
	}

	bedrockOut := BedrockMessages([]BedrockMessage{{Role: "compactionSummary", Text: text}}, "", "", "none")
	if len(bedrockOut) != 1 || string(bedrockOut[0].Role) != "user" {
		t.Fatalf("bedrock messages=%#v", bedrockOut)
	}
	bedrockText, ok := bedrockOut[0].Content[0].(*bedrocktypes.ContentBlockMemberText)
	if !ok || bedrockText.Value != text {
		t.Fatalf("bedrock content=%#v", bedrockOut[0].Content)
	}

	mistralOut := MistralMessages("", []MistralMessage{{Role: "custom", Text: text}}, false)
	if len(mistralOut) != 1 || mistralOut[0]["role"] != "user" || mistralOut[0]["content"] != text {
		t.Fatalf("mistral messages=%#v", mistralOut)
	}

	chatOut := OpenAIChatMessages(OpenAIChatRequestOptions{Messages: []OpenAIChatMessage{{Role: "branchSummary", Text: text}}})
	if len(chatOut) != 1 || chatOut[0]["role"] != "user" || chatOut[0]["content"] != text {
		t.Fatalf("openai chat messages=%#v", chatOut)
	}

	responsesOut := ResponsesMessages(OpenAIResponsesRequestOptions{Messages: []OpenAIResponsesMessage{{Role: "branchSummary", Text: text}}}, false)
	if len(responsesOut) != 1 || responsesOut[0]["role"] != "user" || !jsonContains(t, responsesOut[0], text) {
		t.Fatalf("openai responses messages=%#v", responsesOut)
	}
}

// TestAnthropicToolResultBlockShape locks the TS convertContentBlocks wire shape:
// text-only tool results emit content as a concatenated string, image-bearing
// results emit a block array (with a "(see attached image)" placeholder prepended
// when there is no text).
func TestAnthropicToolResultBlockShape(t *testing.T) {
	t.Run("text-only emits a concatenated string", func(t *testing.T) {
		block := AnthropicToolResultBlock(AnthropicMessage{
			Role:       "toolResult",
			ToolCallID: "toolu_1",
			Blocks:     []AnthropicBlock{{Type: "text", Text: "line one"}, {Type: "text", Text: "line two"}},
		})
		var decoded map[string]any
		raw, err := json.Marshal(block)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		content, ok := decoded["content"].(string)
		if !ok {
			t.Fatalf("expected string content, got %T: %s", decoded["content"], raw)
		}
		if content != "line one\nline two" {
			t.Fatalf("content=%q", content)
		}
	})

	t.Run("image-only prepends the placeholder text block", func(t *testing.T) {
		block := AnthropicToolResultBlock(AnthropicMessage{
			Role:       "toolResult",
			ToolCallID: "toolu_1",
			Blocks:     []AnthropicBlock{{Type: "image", MimeType: "image/png", Data: "AAAA"}},
		})
		raw, err := json.Marshal(block)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		arr, ok := decoded["content"].([]any)
		if !ok || len(arr) != 2 {
			t.Fatalf("expected 2-element content array, got %s", raw)
		}
		first, _ := arr[0].(map[string]any)
		if first["type"] != "text" || first["text"] != "(see attached image)" {
			t.Fatalf("expected placeholder first block, got %s", raw)
		}
		second, _ := arr[1].(map[string]any)
		if second["type"] != "image" {
			t.Fatalf("expected image second block, got %s", raw)
		}
	})

	t.Run("text plus image emits an array without the placeholder", func(t *testing.T) {
		block := AnthropicToolResultBlock(AnthropicMessage{
			Role:       "toolResult",
			ToolCallID: "toolu_1",
			Blocks:     []AnthropicBlock{{Type: "text", Text: "caption"}, {Type: "image", MimeType: "image/png", Data: "AAAA"}},
		})
		raw, err := json.Marshal(block)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		arr, ok := decoded["content"].([]any)
		if !ok || len(arr) != 2 {
			t.Fatalf("expected 2-element content array, got %s", raw)
		}
		first, _ := arr[0].(map[string]any)
		if first["type"] != "text" || first["text"] != "caption" {
			t.Fatalf("expected text first block, got %s", raw)
		}
	})
}

// TestAnthropicMessagesSkipsEmptyContent mirrors TS convertMessages: a user
// message whose text is empty/whitespace after filtering is omitted entirely, and
// no empty placeholder text block is synthesized.
func TestAnthropicMessagesSkipsEmptyContent(t *testing.T) {
	out := AnthropicMessages([]AnthropicMessage{
		{Role: "user", Blocks: []AnthropicBlock{{Type: "text", Text: "   "}}},
		{Role: "user", Blocks: []AnthropicBlock{{Type: "text", Text: "keep me"}}},
	}, anthropic.CacheControlEphemeralParam{}, false, false, false)
	if len(out) != 1 {
		t.Fatalf("expected whitespace-only user message to be skipped, got %d messages", len(out))
	}
	if !jsonContains(t, out[0], "keep me") {
		t.Fatalf("expected surviving message to carry text, got %#v", out[0])
	}
}

func jsonContains(t *testing.T, value any, needle string) bool {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %#v: %v", value, err)
	}
	return strings.Contains(string(raw), needle)
}
