package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

// Ports openai-responses-message-id.test.ts: multiple assistant text blocks in
// one turn must receive unique fallback ids.
func TestResponsesAssistantItemsUniqueFallbackMessageIDs(t *testing.T) {
	options := OpenAIResponsesRequestOptions{ModelID: "gpt-5.5", Provider: "openai-codex", API: "openai-codex-responses"}
	msg := OpenAIResponsesMessage{
		Role: "assistant",
		Blocks: []OpenAIResponsesMessageBlock{
			{Type: "text", Text: "first answer"},
			{Type: "text", Text: "second answer"},
			{Type: "text", Text: "third answer"},
		},
	}
	items := ResponsesAssistantItems(options, msg, 1)
	ids := messageItemIDs(items)
	want := []string{"msg_pi_1", "msg_pi_1_1", "msg_pi_1_2"}
	if len(ids) != len(want) {
		t.Fatalf("ids=%#v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids[%d]=%q want %q (all=%#v)", i, ids[i], want[i], ids)
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %q in %#v", id, ids)
		}
		seen[id] = true
	}
}

// Signature ids longer than 64 chars must be hashed (never truncated) so two
// distinct long ids cannot collide.
func TestResponsesTextMessageIDLongSignatureHashesWithoutCollision(t *testing.T) {
	prefix := strings.Repeat("a", 70)
	sigA := mustTextSignature(t, prefix+"AAAA")
	sigB := mustTextSignature(t, prefix+"BBBB")

	idA := ResponsesTextMessageID(sigA, 0, 0)
	idB := ResponsesTextMessageID(sigB, 0, 0)

	if len([]rune(idA)) > 64 || len([]rune(idB)) > 64 {
		t.Fatalf("expected <=64 chars, got %q (%d) and %q (%d)", idA, len([]rune(idA)), idB, len([]rune(idB)))
	}
	if !strings.HasPrefix(idA, "msg_") || !strings.HasPrefix(idB, "msg_") {
		t.Fatalf("expected msg_ prefix, got %q and %q", idA, idB)
	}
	if idA == idB {
		t.Fatalf("distinct long ids collided: %q", idA)
	}
}

func TestResponsesTextMessageIDShortSignatureUsedAsIs(t *testing.T) {
	sig := mustTextSignature(t, "msg_short")
	if got := ResponsesTextMessageID(sig, 3, 2); got != "msg_short" {
		t.Fatalf("id=%q want msg_short", got)
	}
}

func TestResponsesTextMessageIDFallbackByTextBlockIndex(t *testing.T) {
	if got := ResponsesTextMessageID("", 2, 0); got != "msg_pi_2" {
		t.Fatalf("first fallback=%q", got)
	}
	if got := ResponsesTextMessageID("", 2, 1); got != "msg_pi_2_1" {
		t.Fatalf("second fallback=%q", got)
	}
}

func messageItemIDs(items []map[string]any) []string {
	var ids []string
	for _, item := range items {
		if item["type"] != "message" {
			continue
		}
		if id, ok := item["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func mustTextSignature(t *testing.T, id string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"v": 1, "id": id})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
