package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// These tests lock the on-the-wire JSON shape of the core types to the upstream
// TypeScript field names (@earendil-works/pi-ai). They guard against silent
// drift such as renamed fields or keys dropped when empty, which would break
// cross-language interop and persisted-session compatibility.

func keysOf(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map (value=%s): %v", data, err)
	}
	return m
}

func assertKeys(t *testing.T, v any, want []string) {
	t.Helper()
	got := keysOf(t, v)
	if len(got) != len(want) {
		t.Fatalf("key set mismatch for %#v: got %v, want %v", v, mapKeys(got), want)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing key %q for %#v: got %v, want %v", k, v, mapKeys(got), want)
		}
	}
}

func mapKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestAssistantMessageEventWireShape(t *testing.T) {
	msg := NewAssistantMessage("anthropic", "claude", "model", TextBlocks("hi"), Usage{}, "stop")
	tc := &ToolCall{ID: "1", Name: "bash", Arguments: json.RawMessage(`{}`)}

	cases := []struct {
		name  string
		event AssistantMessageEvent
		want  []string
	}{
		{"start", AssistantMessageEvent{Type: "start", Partial: msg}, []string{"type", "partial"}},
		{"text_start", AssistantMessageEvent{Type: "text_start", ContentIndex: 0, Partial: msg}, []string{"type", "contentIndex", "partial"}},
		{"text_delta", AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "x", Partial: msg}, []string{"type", "contentIndex", "delta", "partial"}},
		// content must be present even when empty (upstream: content: string).
		{"text_end_empty", AssistantMessageEvent{Type: "text_end", ContentIndex: 0, Content: "", Partial: msg}, []string{"type", "contentIndex", "content", "partial"}},
		{"thinking_end_empty", AssistantMessageEvent{Type: "thinking_end", ContentIndex: 1, Content: "", Partial: msg}, []string{"type", "contentIndex", "content", "partial"}},
		// delta must be present even when empty (upstream: delta: string).
		{"toolcall_delta_empty", AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: "", Partial: msg}, []string{"type", "contentIndex", "delta", "partial"}},
		{"toolcall_end", AssistantMessageEvent{Type: "toolcall_end", ContentIndex: 0, ToolCall: tc, Partial: msg}, []string{"type", "contentIndex", "toolCall", "partial"}},
		// done/error carry message/error and must NOT carry partial.
		{"done", AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg}, []string{"type", "reason", "message"}},
		{"error", AssistantMessageEvent{Type: "error", Reason: "aborted", Error: msg}, []string{"type", "reason", "error"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertKeys(t, tc.event, tc.want)
		})
	}
}

func TestModelMaxTokensWireName(t *testing.T) {
	m := Model{Provider: "anthropic", ID: "claude", API: "anthropic", BaseURL: "https://x", MaxOutput: 8192}
	keys := keysOf(t, m)
	if _, ok := keys["maxTokens"]; !ok {
		t.Fatalf("Model must marshal MaxOutput as upstream field %q, got keys %v", "maxTokens", mapKeys(keys))
	}
	if _, ok := keys["maxOutput"]; ok {
		t.Fatalf("Model must not emit legacy %q field, got keys %v", "maxOutput", mapKeys(keys))
	}
	var got int
	if err := json.Unmarshal(keys["maxTokens"], &got); err != nil || got != 8192 {
		t.Fatalf("maxTokens=%v err=%v, want 8192", got, err)
	}
}

func TestModelJSONRoundTrip(t *testing.T) {
	// Accepts both the canonical maxTokens and the legacy maxOutput on input.
	for _, raw := range []string{
		`{"provider":"anthropic","id":"claude","api":"anthropic","baseUrl":"https://x","maxTokens":8192}`,
		`{"provider":"anthropic","id":"claude","api":"anthropic","baseUrl":"https://x","maxOutput":8192}`,
	} {
		var m Model
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if m.MaxOutput != 8192 {
			t.Fatalf("MaxOutput=%d from %s, want 8192", m.MaxOutput, raw)
		}
		// Re-marshalling must be stable and use the canonical name.
		again, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m2 Model
		if err := json.Unmarshal(again, &m2); err != nil {
			t.Fatalf("re-unmarshal %s: %v", again, err)
		}
		if !reflect.DeepEqual(m.MaxOutput, m2.MaxOutput) || m.Provider != m2.Provider || m.ID != m2.ID {
			t.Fatalf("round-trip mismatch: %+v vs %+v", m, m2)
		}
	}
}
