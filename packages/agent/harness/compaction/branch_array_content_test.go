package compaction

import (
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

// TestConvertCompactionCustomMessagePreservesArrayContent verifies R3-P1-1 for
// the compaction/branch-summary serialization path: a custom message whose
// content is the []interface{} shape produced by reloading a session from JSONL
// must still convert to a user message that keeps both its text and image
// blocks, rather than being dropped. TS getMessageFromEntry -> createCustomMessage
// -> convertToLlm always emits a user message and passes array content through.
func TestConvertCompactionCustomMessagePreservesArrayContent(t *testing.T) {
	reloaded := []interface{}{
		map[string]interface{}{"type": "text", "text": "branch screenshot"},
		map[string]interface{}{"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"},
	}

	cases := []struct {
		name    string
		content any
	}{
		{name: "typed blocks", content: []ai.ContentBlock{
			{Type: "text", Text: "branch screenshot"},
			{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
		}},
		{name: "reloaded []interface{}", content: reloaded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := ai.CustomMessage{Role: "custom", CustomType: "note", Content: tc.content, TimestampMs: 7}
			out, err := messagesToLLM([]agent.AgentMessage{msg})
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 {
				t.Fatalf("expected exactly one converted message, got %d (array content was dropped)", len(out))
			}
			if out[0].MessageRole() != "user" {
				t.Fatalf("converted message role = %q, want user", out[0].MessageRole())
			}
			blocks := ai.MessageBlocks(out[0])
			if len(blocks) != 2 {
				t.Fatalf("expected 2 content blocks, got %d: %#v", len(blocks), blocks)
			}
			if blocks[0].Type != "text" || blocks[0].Text != "branch screenshot" {
				t.Fatalf("text block not preserved: %#v", blocks[0])
			}
			if blocks[1].Type != "image" || blocks[1].Data != "aGVsbG8=" || blocks[1].MimeType != "image/png" {
				t.Fatalf("image block not preserved: %#v", blocks[1])
			}
		})
	}
}
