package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// TestActiveToolsChangeEmptyMarshalsArray ensures an active_tools_change entry
// with an empty tool list serializes activeToolNames as [] rather than omitting
// it. TS always writes activeToolNames: [...names] (session.ts:169) and the
// reader does [...entry.activeToolNames] (session.ts:36), which throws on an
// undefined field. omitempty would otherwise drop the empty slice and break
// Go-writes / TS-reads compatibility.
func TestActiveToolsChangeEmptyMarshalsArray(t *testing.T) {
	// Empty list (e.g. SetActiveTools(ctx, nil)) must still emit "activeToolNames":[].
	emptyLine, err := marshalJSONLine(marshalEntry(ActiveToolsChangeEntry{BaseEntry: BaseEntry{ID: "a", Timestamp: "t"}, ActiveToolNames: nil}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(emptyLine), `"activeToolNames":[]`) {
		t.Fatalf("empty active_tools_change must serialize activeToolNames:[], got %s", emptyLine)
	}
	// The field must be present (not missing) and an empty array when parsed loosely.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(emptyLine, &fields); err != nil {
		t.Fatal(err)
	}
	raw, ok := fields["activeToolNames"]
	if !ok {
		t.Fatalf("activeToolNames must be present in %s", emptyLine)
	}
	if string(raw) != "[]" {
		t.Fatalf("activeToolNames must be an empty array, got %s", raw)
	}
	// Round-trip: empty list parses back to an empty (non-iterable-crashing) slice.
	back, err := unmarshalEntry(emptyLine)
	if err != nil {
		t.Fatalf("re-parsing empty active_tools_change failed: %v", err)
	}
	emptyEntry, ok := back.(ActiveToolsChangeEntry)
	if !ok {
		t.Fatalf("expected ActiveToolsChangeEntry, got %T", back)
	}
	if len(emptyEntry.ActiveToolNames) != 0 {
		t.Fatalf("round-tripped empty active tools should be empty, got %#v", emptyEntry.ActiveToolNames)
	}

	// A non-empty list still serializes the names and round-trips intact.
	names := []string{"read", "write"}
	fullLine, err := marshalJSONLine(marshalEntry(ActiveToolsChangeEntry{BaseEntry: BaseEntry{ID: "b", Timestamp: "t"}, ActiveToolNames: names}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fullLine), `"activeToolNames":["read","write"]`) {
		t.Fatalf("non-empty active_tools_change must serialize its names, got %s", fullLine)
	}
	back, err = unmarshalEntry(fullLine)
	if err != nil {
		t.Fatalf("re-parsing non-empty active_tools_change failed: %v", err)
	}
	fullEntry, ok := back.(ActiveToolsChangeEntry)
	if !ok {
		t.Fatalf("expected ActiveToolsChangeEntry, got %T", back)
	}
	if len(fullEntry.ActiveToolNames) != 2 || fullEntry.ActiveToolNames[0] != "read" || fullEntry.ActiveToolNames[1] != "write" {
		t.Fatalf("round-tripped names mismatch, got %#v", fullEntry.ActiveToolNames)
	}

	// Unrelated entry types (e.g. model_change) must NOT gain an activeToolNames field.
	modelLine, err := marshalJSONLine(marshalEntry(ModelChangeEntry{BaseEntry: BaseEntry{ID: "m", Timestamp: "t"}, Provider: "p", ModelID: "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(modelLine), "activeToolNames") {
		t.Fatalf("model_change must not carry activeToolNames, got %s", modelLine)
	}
}

// TestLeafEntryMarshalsTargetIDNull ensures a root leaf (nil target) marshals
// targetId as null rather than omitting it, matching the TS parser which
// requires targetId to be present as null|string (jsonl-storage.ts:103-105).
func TestLeafEntryMarshalsTargetIDNull(t *testing.T) {
	// The disk format goes through marshalEntry -> entryRecord -> marshalJSONLine,
	// not LeafEntry directly, so assert the on-disk line carries targetId:null and
	// round-trips back to a nil-target leaf.
	line, err := marshalJSONLine(marshalEntry(LeafEntry{BaseEntry: BaseEntry{ID: "x", Timestamp: "t"}, TargetID: nil}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), `"targetId":null`) {
		t.Fatalf("root leaf must serialize targetId:null on disk, got %s", line)
	}

	back, err := unmarshalEntry(line)
	if err != nil {
		t.Fatalf("re-parsing the leaf line failed: %v", err)
	}
	leaf, ok := back.(LeafEntry)
	if !ok {
		t.Fatalf("expected LeafEntry, got %T", back)
	}
	if leaf.TargetID != nil {
		t.Fatalf("round-tripped root leaf target should be nil, got %q", *leaf.TargetID)
	}

	// A non-leaf entry must NOT gain a targetId field.
	msgLine, err := marshalJSONLine(marshalEntry(SessionInfoEntry{BaseEntry: BaseEntry{ID: "s", Timestamp: "t"}, Name: "demo"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(msgLine), "targetId") {
		t.Fatalf("non-leaf entry must not carry targetId, got %s", msgLine)
	}

	// A leaf with a real target keeps its string targetId.
	target := "abc"
	leafLine, err := marshalJSONLine(marshalEntry(LeafEntry{BaseEntry: BaseEntry{ID: "y", Timestamp: "t"}, TargetID: &target}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(leafLine), `"targetId":"abc"`) {
		t.Fatalf("leaf with target must serialize its string targetId, got %s", leafLine)
	}
}

// TestSessionNameUsesAllEntriesAndTrims verifies Name() returns the last
// session_info across all entries (not just the current branch) and trims it,
// matching TS getSessionName (session.ts:122-125, 236-243).
func TestSessionNameUsesAllEntriesAndTrims(t *testing.T) {
	ctx := context.Background()
	sess, err := NewMemory(Metadata{ID: "s1", CreatedAt: "now"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rootID, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil))
	if err != nil {
		t.Fatal(err)
	}
	// Name set on this branch should be trimmed when written and read back.
	if _, err := sess.AppendSessionName(ctx, "  Demo  "); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("left", nil)); err != nil {
		t.Fatal(err)
	}
	// Navigate to another branch where the session_info entry is off-branch.
	if _, err := sess.MoveTo(ctx, &rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("right", nil)); err != nil {
		t.Fatal(err)
	}
	branch, err := sess.Branch(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range branch {
		if _, ok := entry.(SessionInfoEntry); ok {
			t.Fatalf("session_info should be off the current branch: %#v", branch)
		}
	}
	name, err := sess.Name(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Demo" {
		t.Fatalf("name=%q, want %q", name, "Demo")
	}
}

// TestSessionNameWhitespaceOnly verifies a whitespace-only name yields empty,
// matching TS where name.trim() is stored and the empty string is returned.
func TestSessionNameWhitespaceOnly(t *testing.T) {
	ctx := context.Background()
	sess, err := NewMemory(Metadata{ID: "s1", CreatedAt: "now"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendSessionName(ctx, "   "); err != nil {
		t.Fatal(err)
	}
	entries, err := sess.storage.FindEntries(ctx, "session_info")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one session_info entry, got %d", len(entries))
	}
	if info, ok := entries[0].(SessionInfoEntry); !ok || info.Name != "" {
		t.Fatalf("stored name should be trimmed to empty, got %#v", entries[0])
	}
	name, err := sess.Name(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if name != "" {
		t.Fatalf("name=%q, want empty", name)
	}
}
