package codingagent

import (
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

func TestParseAndMigrateSessionEntries(t *testing.T) {
	content := `{"type":"session","id":"s1","timestamp":"2026-05-27T00:00:00Z","cwd":"/tmp"}` + "\n" +
		`{"type":"message","message":{"role":"hookMessage","content":"legacy"}}` + "\n" +
		`not json` + "\n" +
		`{"type":"compaction","summary":"old","firstKeptEntryIndex":1,"tokensBefore":10}`
	entries := ParseSessionEntries(content)
	if len(entries) != 3 {
		t.Fatalf("entries=%#v", entries)
	}
	MigrateSessionEntries(entries)
	if entries[0].Version != CurrentSessionVersion {
		t.Fatalf("version=%d", entries[0].Version)
	}
	if entries[1].ID == "" || entries[2].ParentID == nil || *entries[2].ParentID != entries[1].ID {
		t.Fatalf("ids: %#v %#v", entries[1], entries[2])
	}
	if entries[1].Message == nil || ai.MessageRole(entries[1].Message) != "custom" {
		t.Fatalf("message=%#v", entries[1].Message)
	}
	if entries[2].FirstKeptID != entries[1].ID || entries[2].FirstKeptEntryIndex != nil {
		t.Fatalf("compaction=%#v", entries[2])
	}
}

func TestBuildSessionContextHelper(t *testing.T) {
	parent := "u1"
	parent2 := "a1"
	parent3 := "c1"
	entries := []core.SessionEntry{
		{Type: "message", ID: "u1", Message: ai.NewUserMessage("hello", nil)},
		{Type: "message", ID: "a1", ParentID: &parent, Message: ai.NewAssistantMessage("faux", "faux", "faux", ai.TextBlocks("world"), ai.Usage{}, "stop")},
		{Type: "compaction", ID: "c1", ParentID: &parent2, Summary: "summary", FirstKeptID: "a1", TokensBefore: 42},
		{Type: "custom_message", ID: "cm1", ParentID: &parent3, CustomType: "note", Content: "custom", Display: true},
	}
	ctx := BuildSessionContext(entries)
	if ctx.ThinkingLevel != ai.ThinkingOff || ctx.ModelProvider != "faux" || ctx.ModelID != "faux" {
		t.Fatalf("ctx=%#v", ctx)
	}
	if len(ctx.Messages) != 3 {
		t.Fatalf("messages=%#v", ctx.Messages)
	}
	if ai.MessageRole(ctx.Messages[0]) != "compactionSummary" || ai.MessageRole(ctx.Messages[1]) != "assistant" || ai.MessageRole(ctx.Messages[2]) != "custom" {
		t.Fatalf("messages=%#v", ctx.Messages)
	}
	empty := BuildSessionContext(entries, nil)
	if len(empty.Messages) != 0 {
		t.Fatalf("empty=%#v", empty)
	}
}

func TestGetLatestCompactionEntry(t *testing.T) {
	entries := []core.SessionEntry{
		{Type: "compaction", ID: "c1", Summary: "old"},
		{Type: "message", ID: "m1", Message: ai.NewUserMessage("x", nil)},
		{Type: "compaction", ID: "c2", Summary: "new"},
	}
	got := GetLatestCompactionEntry(entries)
	if got == nil || got.ID != "c2" || !strings.Contains(got.Summary, "new") {
		t.Fatalf("got=%#v", got)
	}
}
