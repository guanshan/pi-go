package session

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"
)

// P2-03: CreateTimestamp must match JS Date.toISOString(): UTC, exactly three
// fractional digits, trailing "Z".
func TestCreateTimestampMatchesToISOString(t *testing.T) {
	ts := CreateTimestamp()
	re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	if !re.MatchString(ts) {
		t.Fatalf("timestamp %q does not match millisecond ISO-8601 UTC", ts)
	}
	// Must round-trip through the RFC3339Nano parser used elsewhere.
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Fatalf("timestamp %q not parseable: %v", ts, err)
	}
}

// P2-03: PathToRoot returns "not_found" when the leaf itself is missing but
// "invalid_session" when a parent in the chain is missing (matching TS
// getPathToRoot).
func TestPathToRootErrorCodes(t *testing.T) {
	ctx := context.Background()

	missingLeaf := "ghost"
	store, err := NewMemoryStorage(Metadata{ID: "s"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PathToRoot(ctx, &missingLeaf)
	var sessErr *SessionError
	if !errors.As(err, &sessErr) || sessErr.Code != "not_found" {
		t.Fatalf("missing leaf: want not_found, got %#v", err)
	}

	// An entry whose parent link dangles must surface invalid_session.
	child := MessageEntry{BaseEntry: BaseEntry{ID: "child", ParentID: ptr("does-not-exist"), Timestamp: "t"}}
	store2, err := NewMemoryStorage(Metadata{ID: "s2"}, []Entry{child})
	if err != nil {
		t.Fatal(err)
	}
	leaf := "child"
	_, err = store2.PathToRoot(ctx, &leaf)
	if !errors.As(err, &sessErr) || sessErr.Code != "invalid_session" {
		t.Fatalf("dangling parent: want invalid_session, got %#v", err)
	}
}

func ptr(s string) *string { return &s }
