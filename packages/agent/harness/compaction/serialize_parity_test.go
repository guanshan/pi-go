package compaction

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/guanshan/pi-go/packages/ai"
)

// P1-07c: tool-call args must serialize in JSON key insertion order (matching
// TS Object.entries), not sorted, and values must use JSON.stringify semantics
// (no HTML escaping).
func TestSerializeConversationToolCallArgsPreserveOrder(t *testing.T) {
	assistant := ai.AssistantMessage{
		Role: "assistant",
		Content: []ai.ContentBlock{
			{Type: "toolCall", Name: "edit", Arguments: json.RawMessage(`{"zeta":1,"alpha":"a<b>","mid":true}`)},
		},
	}
	got := SerializeConversation([]ai.Message{assistant})
	want := `[Assistant tool calls]: edit(zeta=1, alpha="a<b>", mid=true)`
	if got != want {
		t.Fatalf("serialized=%q want %q", got, want)
	}
}

func TestSerializeConversationToolCallArgsCanonicalizeNumbers(t *testing.T) {
	assistant := ai.AssistantMessage{
		Role: "assistant",
		Content: []ai.ContentBlock{
			{Type: "toolCall", Name: "calc", Arguments: json.RawMessage(`{"exp":1e10,"fixed":1.0,"nested":[2.50]}`)},
		},
	}
	got := SerializeConversation([]ai.Message{assistant})
	want := `[Assistant tool calls]: calc(exp=10000000000, fixed=1, nested=[2.5])`
	if got != want {
		t.Fatalf("serialized=%q want %q", got, want)
	}
}

// P1-07c: a nested object value must keep its JSON key insertion order too
// (JSON.stringify preserves it); decoding into a Go map would re-sort the keys.
func TestSerializeConversationToolCallArgsPreserveNestedOrder(t *testing.T) {
	assistant := ai.AssistantMessage{
		Role: "assistant",
		Content: []ai.ContentBlock{
			{Type: "toolCall", Name: "cfg", Arguments: json.RawMessage(`{"outer":{"zeta":1,"alpha":2,"deep":{"y":1.0,"x":3}}}`)},
		},
	}
	got := SerializeConversation([]ai.Message{assistant})
	want := `[Assistant tool calls]: cfg(outer={"zeta":1,"alpha":2,"deep":{"y":1,"x":3}})`
	if got != want {
		t.Fatalf("serialized=%q want %q", got, want)
	}
}

func TestSerializeConversationToolCallArgsUseJSPropertyOrder(t *testing.T) {
	assistant := ai.AssistantMessage{
		Role: "assistant",
		Content: []ai.ContentBlock{
			{Type: "toolCall", Name: "order", Arguments: json.RawMessage(`{"10":"ten","2":"two","alpha":{"10":"ten","2":"two","z":0},"01":"kept","z":1}`)},
		},
	}
	got := SerializeConversation([]ai.Message{assistant})
	want := `[Assistant tool calls]: order(2="two", 10="ten", alpha={"2":"two","10":"ten","z":0}, 01="kept", z=1)`
	if got != want {
		t.Fatalf("serialized=%q want %q", got, want)
	}
}

// P1-07c: user (and tool result) text blocks join with "" (not "\n").
func TestSerializeConversationUserTextBlocksJoinEmpty(t *testing.T) {
	user := ai.UserMessage{
		Role:    "user",
		Content: []ai.ContentBlock{{Type: "text", Text: "foo"}, {Type: "text", Text: "bar"}},
	}
	got := SerializeConversation([]ai.Message{user})
	if got != "[User]: foobar" {
		t.Fatalf("user serialized=%q want %q", got, "[User]: foobar")
	}

	tool := ai.ToolResultMessage{
		Role:    "toolResult",
		Content: []ai.ContentBlock{{Type: "text", Text: "x"}, {Type: "text", Text: "y"}},
	}
	gotTool := SerializeConversation([]ai.Message{tool})
	if gotTool != "[Tool result]: xy" {
		t.Fatalf("tool serialized=%q want %q", gotTool, "[Tool result]: xy")
	}
}

// Mirrors ../pi/packages/coding-agent/test/compaction-serialization.test.ts
// ("serializeConversation"): long tool results are truncated with the exact
// footer "[... N more characters truncated]" (N computed to match), while short
// tool results and assistant/user messages pass through untouched.
func TestSerializeConversationTruncatesLongToolResults(t *testing.T) {
	t.Run("truncates long tool results", func(t *testing.T) {
		longContent := strings.Repeat("x", 5000)
		messages := []ai.Message{
			ai.ToolResultMessage{
				Role:       "toolResult",
				ToolCallID: "tc1",
				ToolName:   "read",
				Content:    []ai.ContentBlock{{Type: "text", Text: longContent}},
			},
		}

		result := SerializeConversation(messages)

		if !strings.Contains(result, "[Tool result]:") {
			t.Fatalf("missing tool-result prefix: %q", result)
		}
		// N = total UTF-16 units (5000) - kept prefix (2000) = 3000.
		if !strings.Contains(result, "[... 3000 more characters truncated]") {
			t.Fatalf("missing exact truncation footer: %q", result)
		}
		// The truncated tail (chars 2000..5000) must not survive.
		if strings.Contains(result, strings.Repeat("x", 3000)) {
			t.Fatalf("result contains untruncated 3000-char run: %q", result)
		}
		// The first 2000 chars must be present.
		if !strings.Contains(result, strings.Repeat("x", 2000)) {
			t.Fatalf("result missing kept 2000-char prefix: %q", result)
		}
	})

	t.Run("does not truncate short tool results", func(t *testing.T) {
		shortContent := strings.Repeat("x", 1500)
		messages := []ai.Message{
			ai.ToolResultMessage{
				Role:       "toolResult",
				ToolCallID: "tc1",
				ToolName:   "read",
				Content:    []ai.ContentBlock{{Type: "text", Text: shortContent}},
			},
		}

		result := SerializeConversation(messages)

		if want := "[Tool result]: " + shortContent; result != want {
			t.Fatalf("short tool result serialized=%q want %q", result, want)
		}
		if strings.Contains(result, "truncated") {
			t.Fatalf("short tool result was truncated: %q", result)
		}
	})

	t.Run("does not truncate assistant or user messages", func(t *testing.T) {
		longText := strings.Repeat("y", 5000)
		messages := []ai.Message{
			ai.UserMessage{
				Role:    "user",
				Content: []ai.ContentBlock{{Type: "text", Text: longText}},
			},
			ai.AssistantMessage{
				Role:    "assistant",
				Content: []ai.ContentBlock{{Type: "text", Text: longText}},
			},
		}

		result := SerializeConversation(messages)

		if strings.Contains(result, "truncated") {
			t.Fatalf("assistant/user message was truncated: %q", result)
		}
		if !strings.Contains(result, longText) {
			t.Fatalf("result missing untruncated long text: %q", result)
		}
	})
}

// P1-07c: truncateForSummary measures and slices in UTF-16 code units and never
// splits a multibyte rune.
func TestTruncateForSummaryUTF16(t *testing.T) {
	if got := truncateForSummary("hello", 100); got != "hello" {
		t.Fatalf("short text mutated: %q", got)
	}

	// 5 emoji, each 2 UTF-16 units => total 10 units. Truncate at 4 units => 2
	// emoji kept, message reports 6 units truncated, and no rune is split.
	text := strings.Repeat("\U0001F600", 5)
	got := truncateForSummary(text, 4)
	if !strings.HasPrefix(got, "\U0001F600\U0001F600\n\n[... 6 more characters truncated]") {
		t.Fatalf("utf16 truncate=%q", got)
	}
	// The kept prefix must be exactly two whole emoji (8 bytes), never a partial.
	head := strings.SplitN(got, "\n\n", 2)[0]
	if head != "\U0001F600\U0001F600" {
		t.Fatalf("prefix split a rune or wrong length: %q", head)
	}

	// An odd UTF-16 budget that would land mid-surrogate must not split: with a
	// budget of 3 units only one whole emoji (2 units) fits.
	got3 := truncateForSummary(text, 3)
	head3 := strings.SplitN(got3, "\n\n", 2)[0]
	if head3 != "\U0001F600" {
		t.Fatalf("odd budget split a rune: %q", head3)
	}
}

// P1-07d: the Go-only SummaryMaxChars safety net truncates on a rune boundary
// and never emits an invalid (split) UTF-8 sequence, for every byte budget.
func TestTruncateBytesOnRuneBoundaryNeverSplitsRune(t *testing.T) {
	// "你好世界😀" -> three-byte CJK runes plus a four-byte emoji.
	s := "你好世界\U0001F600"
	for limit := 0; limit <= len(s)+2; limit++ {
		got := truncateBytesOnRuneBoundary(s, limit)
		if len(got) > limit && limit <= len(s) {
			t.Fatalf("limit=%d produced %d bytes", limit, len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("limit=%d produced invalid UTF-8: %q", limit, got)
		}
		if !strings.HasPrefix(s, got) {
			t.Fatalf("limit=%d result is not a prefix: %q", limit, got)
		}
	}
}
