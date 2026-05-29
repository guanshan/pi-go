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
