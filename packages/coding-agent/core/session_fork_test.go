package core

import (
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

// TestCreateSessionSessionIDOpensExisting verifies that a plain --session-id
// that matches an existing local session opens it (rather than erroring), aligning
// with the TypeScript findLocalSessionByExactId behaviour.
func TestCreateSessionSessionIDOpensExisting(t *testing.T) {
	cwd := t.TempDir()
	dir := t.TempDir()

	first, err := createSession(cli.Args{SessionID: "myid.1"}, cwd, dir, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := first.AppendMessage(ai.NewUserMessage("hello", nil)); err != nil {
		t.Fatal(err)
	}
	firstPath := first.File()

	second, err := createSession(cli.Args{SessionID: "myid.1"}, cwd, dir, nil, nil, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if second.File() != firstPath {
		t.Fatalf("expected to reopen %q, got %q", firstPath, second.File())
	}
	if second.SessionID() != "myid.1" {
		t.Fatalf("sessionID=%q", second.SessionID())
	}
	if len(second.Entries) != 1 {
		t.Fatalf("expected existing entry to load, got %d entries", len(second.Entries))
	}
}

// TestCreateSessionSessionIDCreatesNew verifies a --session-id with no existing
// match creates a fresh session carrying that id.
func TestCreateSessionSessionIDCreatesNew(t *testing.T) {
	cwd := t.TempDir()
	dir := t.TempDir()
	sm, err := createSession(cli.Args{SessionID: "brand-new.2"}, cwd, dir, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sm.SessionID() != "brand-new.2" {
		t.Fatalf("sessionID=%q", sm.SessionID())
	}
	if len(sm.Entries) != 0 {
		t.Fatalf("expected fresh session, got %d entries", len(sm.Entries))
	}
}

// TestCreateSessionForkWithSessionID verifies --fork --session-id forks into a
// new session that uses the explicit id, and rejects an id that already exists.
func TestCreateSessionForkWithSessionID(t *testing.T) {
	cwd := t.TempDir()
	dir := t.TempDir()

	source, err := createSession(cli.Args{}, cwd, dir, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.AppendMessage(ai.NewUserMessage("origin", nil)); err != nil {
		t.Fatal(err)
	}

	forked, err := createSession(cli.Args{Fork: source.File(), SessionID: "forked.3"}, cwd, dir, nil, nil, nil)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if forked.SessionID() != "forked.3" {
		t.Fatalf("forked sessionID=%q", forked.SessionID())
	}
	if forked.File() == source.File() {
		t.Fatalf("fork reused source path %q", source.File())
	}
	if len(forked.Entries) != 1 {
		t.Fatalf("expected forked branch to carry 1 entry, got %d", len(forked.Entries))
	}

	// Forking again onto the same explicit id must be rejected.
	if _, err := createSession(cli.Args{Fork: source.File(), SessionID: "forked.3"}, cwd, dir, nil, nil, nil); err == nil {
		t.Fatal("expected conflict error for existing fork target id")
	}
}

// TestValidateRuntimeArgsAllowsForkWithSessionID guards that --fork and
// --session-id are no longer rejected together (TS allows the combination).
func TestValidateRuntimeArgsAllowsForkWithSessionID(t *testing.T) {
	if err := validateRuntimeArgs(cli.Args{Fork: "abc", SessionID: "myid.1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// --session-id still conflicts with --session.
	if err := validateRuntimeArgs(cli.Args{SessionID: "myid.1", Session: "other"}); err == nil {
		t.Fatal("expected --session-id/--session conflict")
	}
}
