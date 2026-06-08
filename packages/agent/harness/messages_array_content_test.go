package harness

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

// reloadedArrayContent is the shape a custom message's array content takes after
// a session is reloaded from JSONL: encoding/json decodes a JSON array into
// []interface{} whose elements are map[string]any, not []ai.ContentBlock.
func reloadedArrayContent() []interface{} {
	return []interface{}{
		map[string]interface{}{"type": "text", "text": "look at this screenshot"},
		map[string]interface{}{"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"},
	}
}

func assertTextAndImage(t *testing.T, msg ai.Message) {
	t.Helper()
	if role := msg.MessageRole(); role != "user" {
		t.Fatalf("converted message role = %q, want user", role)
	}
	blocks := ai.MessageBlocks(msg)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 content blocks (text + image), got %d: %#v", len(blocks), blocks)
	}
	if blocks[0].Type != "text" || blocks[0].Text != "look at this screenshot" {
		t.Fatalf("text block not preserved: %#v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Data != "aGVsbG8=" || blocks[1].MimeType != "image/png" {
		t.Fatalf("image block not preserved: %#v", blocks[1])
	}
}

// TestConvertToLLMPreservesArrayContentCustomMessage verifies R3-P1-1: a custom
// message whose content is an array (text + image) must convert to exactly one
// user message that keeps both blocks. Before the fix the []interface{} reload
// shape fell through customContentBlocks' default and was silently dropped,
// losing context (and images) after a session restart. TS convertToLlm always
// emits a user message for custom entries and passes the array content through
// (messages.ts:133-139).
func TestConvertToLLMPreservesArrayContentCustomMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  agent.AgentMessage
	}{
		{
			// In-memory shape (before any reload): already typed blocks.
			name: "typed blocks via ai.CustomMessage",
			msg: ai.CustomMessage{
				Role:       "custom",
				CustomType: "note",
				Content: []ai.ContentBlock{
					{Type: "text", Text: "look at this screenshot"},
					{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
				},
				TimestampMs: 5,
			},
		},
		{
			// Reload shape: []interface{} of map[string]any (the regression).
			name: "reloaded []interface{} via ai.CustomMessage",
			msg: ai.CustomMessage{
				Role:        "custom",
				CustomType:  "note",
				Content:     reloadedArrayContent(),
				TimestampMs: 5,
			},
		},
		{
			// Same regression via the harness-native CustomMessage type, which
			// flows through convertKnownHarnessMessage rather than convertCustomAIMessage.
			name: "reloaded []interface{} via harness CustomMessage",
			msg: CustomMessage{
				Role:        "custom",
				CustomType:  "note",
				Content:     reloadedArrayContent(),
				TimestampMs: 5,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm, err := ConvertToLLM([]agent.AgentMessage{tc.msg})
			if err != nil {
				t.Fatal(err)
			}
			if len(llm) != 1 {
				t.Fatalf("expected exactly one converted message, got %d (array content was dropped)", len(llm))
			}
			assertTextAndImage(t, llm[0])
		})
	}
}

func TestConvertToLLMDropsEmptyCustomContent(t *testing.T) {
	llm, err := ConvertToLLM([]agent.AgentMessage{
		CustomMessage{Role: "custom", CustomType: "empty", Content: nil, TimestampMs: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(llm) != 0 {
		t.Fatalf("empty custom content should be dropped, got %#v", llm)
	}
}

func TestConvertToLLMConvertsScalarCustomContentToText(t *testing.T) {
	llm, err := ConvertToLLM([]agent.AgentMessage{
		CustomMessage{Role: "custom", CustomType: "flag", Content: true, TimestampMs: 5},
		session.CustomSessionMessage{Role: "custom", CustomType: "count", Content: float64(42), TimestampMs: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(llm) != 2 {
		t.Fatalf("expected two scalar custom messages, got %#v", llm)
	}
	if got := ai.MessageText(llm[0]); got != "true" {
		t.Fatalf("bool custom text=%q", got)
	}
	if got := ai.MessageText(llm[1]); got != "42" {
		t.Fatalf("number custom text=%q", got)
	}
}

// TestSessionReloadPreservesArrayContentCustomMessage verifies R3-P1-1 end to
// end: a custom message with array content (text + image) written to a JSONL
// session must, after reload, still appear in the LLM context with both blocks.
// This is the common real-world trigger (the content is always an array of maps
// once round-tripped through disk).
func TestSessionReloadPreservesArrayContentCustomMessage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	storage, err := session.CreateJSONLStorage(ctx, path, session.JSONLMetadata{
		Metadata: session.Metadata{ID: "s1"},
		Cwd:      "/work",
		Path:     path,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := session.New(storage)
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("hello", nil)); err != nil {
		t.Fatal(err)
	}
	content := []ai.ContentBlock{
		{Type: "text", Text: "look at this screenshot"},
		{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
	}
	if _, err := sess.AppendCustomMessageEntry(ctx, "note", content, true, nil); err != nil {
		t.Fatal(err)
	}

	// Reopen from disk so Content decodes to the []interface{} shape.
	reopened, err := session.OpenJSONLStorage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	built, err := session.New(reopened).BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}

	llm, err := ConvertToLLM(built.Messages)
	if err != nil {
		t.Fatal(err)
	}
	// Expect the user "hello" message plus the converted custom message.
	if len(llm) != 2 {
		t.Fatalf("expected 2 LLM messages after reload, got %d: %#v", len(llm), llm)
	}
	assertTextAndImage(t, llm[1])
}
