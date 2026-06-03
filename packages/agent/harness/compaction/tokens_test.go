package compaction

import (
	"testing"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

func TestShouldCompactAndTokenHelpers(t *testing.T) {
	messages := []agent.AgentMessage{
		ai.NewUserMessage("small", nil),
		ai.NewAssistantMessageForModel(ai.Model{Provider: "test", ID: "m", API: "test"}, ai.TextBlocks("response"), ai.Usage{Input: 3, Output: 4, CacheRead: 5, CacheWrite: 6}, "stop"),
	}
	estimate := EstimateContextTokens(messages)
	if estimate.Tokens != 18 || estimate.UsageTokens != 18 || estimate.LastUsageIndex == nil || *estimate.LastUsageIndex != 1 {
		t.Fatalf("estimate=%#v", estimate)
	}
	if !ShouldCompact(95, 100, Settings{Enabled: true, ReserveTokens: 10}) {
		t.Fatalf("expected compaction for tiny max token setting")
	}
	if ShouldCompact(95, 100, Settings{Enabled: false, ReserveTokens: 10}) {
		t.Fatalf("disabled settings should not compact")
	}
	if got := CalculateContextTokens(ai.Usage{TotalTokens: 42, Input: 1, Output: 2}); got != 42 {
		t.Fatalf("total tokens=%d", got)
	}
	if got := CalculateContextTokens(ai.Usage{Input: 3, Output: 4, CacheRead: 5, CacheWrite: 6}); got != 18 {
		t.Fatalf("calculated tokens=%d", got)
	}
	entries := []session.Entry{
		session.MessageEntry{Message: messages[0]},
		session.MessageEntry{Message: messages[1]},
	}
	if got, ok := GetLastAssistantUsage(entries); !ok || got.Input != 3 || got.Output != 4 {
		t.Fatalf("usage=%#v", got)
	}
}

func TestUTF16Len(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"你好", 2},                         // CJK "ni hao": BMP runes -> 1 unit each
		{"\U0001F600", 2},                 // emoji grinning face -> surrogate pair
		{"a\U0001F600你", 4},               // mix: 1 + 2 + 1
		{"\U0001F468\u200d\U0001F469", 5}, // two emoji (2+2) + ZWJ (1)
	}
	for _, tc := range cases {
		if got := utf16Len(tc.in); got != tc.want {
			t.Fatalf("utf16Len(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestEstimateTokensUsesUTF16Length(t *testing.T) {
	// "ni hao" (2 BMP runes, 6 UTF-8 bytes) + grinning face emoji (2 UTF-16
	// units, 4 UTF-8 bytes) => 4 UTF-16 code units. Math.ceil(4/4) == 1.
	// Counting bytes would yield 10 => Math.ceil(10/4) == 3, so this proves the
	// estimate follows TS String.length, not Go byte length.
	text := "你好\U0001F600"
	if got := utf16Len(text); got != 4 {
		t.Fatalf("precondition utf16Len=%d want 4", got)
	}

	user := ai.NewUserMessage(text, nil)
	if got := EstimateTokens(user); got != 1 {
		t.Fatalf("user EstimateTokens=%d want 1", got)
	}

	assistant := ai.NewAssistantMessageForModel(ai.Model{}, ai.TextBlocks(text), ai.Usage{}, "stop")
	if got := EstimateTokens(assistant); got != 1 {
		t.Fatalf("assistant EstimateTokens=%d want 1", got)
	}

	summary := ai.CustomMessage{Role: "branchSummary", Summary: text}
	if got := EstimateTokens(summary); got != 1 {
		t.Fatalf("branchSummary EstimateTokens=%d want 1", got)
	}

	// toolCall name + args also counted as UTF-16: name "x" (1) + args {} (2) = 3.
	tool := ai.NewAssistantMessageForModel(ai.Model{}, []ai.ContentBlock{
		{Type: "toolCall", ID: "c", Name: "x", Arguments: []byte(`{}`)},
	}, ai.Usage{}, "toolUse")
	if got := EstimateTokens(tool); got != 1 { // ceil(3/4) == 1
		t.Fatalf("toolCall EstimateTokens=%d want 1", got)
	}
}

func TestFindCutPointKeepsTurnBoundary(t *testing.T) {
	entries := []session.Entry{
		session.MessageEntry{Message: ai.NewUserMessage("old question", nil)},
		session.MessageEntry{Message: ai.NewAssistantMessageForModel(ai.Model{}, ai.TextBlocks("old answer"), ai.Usage{}, "stop")},
		session.MessageEntry{Message: ai.NewUserMessage("new question", nil)},
		session.MessageEntry{Message: ai.NewAssistantMessageForModel(ai.Model{}, ai.TextBlocks("new answer"), ai.Usage{}, "stop")},
	}
	cut := FindCutPoint(entries, 0, len(entries), 4)
	if cut.FirstKeptEntryIndex != 2 || cut.IsSplitTurn {
		t.Fatalf("cut=%#v", cut)
	}
	if start := FindTurnStartIndex(entries, 3, 0); start != 2 {
		t.Fatalf("turn start=%d", start)
	}
}

func TestFindCutPointDoesNotKeepOrphanedToolResult(t *testing.T) {
	entries := []session.Entry{
		session.MessageEntry{Message: ai.NewUserMessage("old question", nil)},
		session.MessageEntry{Message: ai.NewAssistantMessageForModel(ai.Model{}, []ai.ContentBlock{
			{Type: "toolCall", ID: "call-1", Name: "lookup", Arguments: []byte(`{}`)},
		}, ai.Usage{}, "toolUse")},
		session.MessageEntry{Message: ai.NewToolResultMessage("call-1", "lookup", ai.TextBlocks("tool output that should not be a cut point"), nil, false)},
		session.MessageEntry{Message: ai.NewUserMessage("new question", nil)},
	}
	cut := FindCutPoint(entries, 0, len(entries), 5)
	if cut.FirstKeptEntryIndex != 3 {
		t.Fatalf("cut=%#v", cut)
	}
}
