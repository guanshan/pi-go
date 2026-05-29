package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestSessionRoundTripAndContext(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	sm, err := NewSessionManager(cwd, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sm.AppendMessage(ai.NewUserMessage("hello", nil)); err != nil {
		t.Fatal(err)
	}
	if err := sm.AppendModelChange("faux", "faux"); err != nil {
		t.Fatal(err)
	}
	if err := sm.AppendThinkingChange(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenSession(sm.File(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	ctx := opened.BuildContext()
	if got := len(ctx.Messages); got != 1 {
		t.Fatalf("messages=%d", got)
	}
	if ctx.ModelProvider != "faux" || ctx.ModelID != "faux" {
		t.Fatalf("model not restored: %#v", ctx)
	}
	if ctx.ThinkingLevel != ai.ThinkingHigh {
		t.Fatalf("thinking not restored: %s", ctx.ThinkingLevel)
	}
	if _, err := os.Stat(filepath.Dir(sm.File())); err != nil {
		t.Fatal(err)
	}
}

func TestSessionBuildContextKeepsMessagesAfterCompaction(t *testing.T) {
	session := InMemorySession(t.TempDir())
	oldID := appendSessionMessage(t, session, ai.NewUserMessage("old", nil))
	keptID := appendSessionMessage(t, session, ai.NewUserMessage("kept", nil))
	if err := session.Append(SessionEntry{Type: "compaction", Summary: "summary", FirstKeptID: keptID, TokensBefore: 10}); err != nil {
		t.Fatal(err)
	}
	appendSessionMessage(t, session, ai.NewUserMessage("after", nil))

	ctx := session.BuildContext()
	if ctx.ThinkingLevel != ai.ThinkingOff {
		t.Fatalf("thinking default=%s", ctx.ThinkingLevel)
	}
	if len(ctx.Messages) != 3 {
		t.Fatalf("messages=%#v oldID=%s", ctx.Messages, oldID)
	}
	if ai.MessageRole(ctx.Messages[0]) != "compactionSummary" {
		t.Fatalf("first message=%#v", ctx.Messages[0])
	}
	if got := ai.MessageText(ctx.Messages[1]); got != "kept" {
		t.Fatalf("kept message=%q", got)
	}
	if got := ai.MessageText(ctx.Messages[2]); got != "after" {
		t.Fatalf("post-compaction message=%q", got)
	}
}
