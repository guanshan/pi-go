package core

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

func TestValidateRuntimeArgsRejectsRPCFiles(t *testing.T) {
	err := validateRuntimeArgs(cli.Args{Mode: cli.ModeRPC, FileArgs: []string{"prompt.md"}})
	if err == nil || !strings.Contains(err.Error(), "@file arguments are not supported in RPC mode") {
		t.Fatalf("error=%v", err)
	}
}

func TestCreateSessionResumeIncludesGlobalSessions(t *testing.T) {
	sessionDir := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	created, err := NewSessionManager(cwdA, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := created.AppendMessage(ai.NewUserMessage("from A", nil)); err != nil {
		t.Fatal(err)
	}

	resumed, err := createSession(cli.Args{Resume: true}, cwdB, sessionDir, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.CWD() != cwdA {
		t.Fatalf("resumed cwd=%q want %q", resumed.CWD(), cwdA)
	}
}

func TestValidateSessionCWDSkipsInMemorySession(t *testing.T) {
	// In-memory sessions have no session file, so a missing cwd is not an error,
	// mirroring TS getMissingSessionCwdIssue's `!sessionFile` early return.
	session := InMemorySession("/path/that/does/not/exist")
	if err := validateSessionCWD(session, t.TempDir(), true, nil, nil); err != nil {
		t.Fatalf("error=%v", err)
	}
}

func TestValidateSessionCWDRejectsMissingDirectory(t *testing.T) {
	sessionDir := t.TempDir()
	cwd := t.TempDir()
	created, err := NewSessionManager(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := created.AppendMessage(ai.NewUserMessage("hi", nil)); err != nil {
		t.Fatal(err)
	}
	// Point the persisted session at a cwd that no longer exists.
	created.Header.CWD = "/path/that/does/not/exist"
	fallback := t.TempDir()
	err = validateSessionCWD(created, fallback, true, nil, nil)
	if err == nil {
		t.Fatal("expected missing cwd error")
	}
	for _, want := range []string{
		"Stored session working directory does not exist: /path/that/does/not/exist",
		"Current working directory: " + fallback,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestResumeSessionsMissingRootReturnsNoSessions(t *testing.T) {
	missingSessionDir := filepath.Join(t.TempDir(), "missing-sessions")
	sessions, err := resumeSessions(t.TempDir(), missingSessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions=%#v", sessions)
	}
	if _, err := createSession(cli.Args{Resume: true}, t.TempDir(), missingSessionDir, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "no sessions found") {
		t.Fatalf("error=%v", err)
	}
}

func TestValidateRuntimeArgsRejectsForkConflicts(t *testing.T) {
	err := validateRuntimeArgs(cli.Args{
		Fork:      "session-id",
		Session:   "other-session",
		Continue:  true,
		Resume:    true,
		NoSession: true,
	})
	if err == nil {
		t.Fatal("expected fork conflict")
	}
	for _, flag := range []string{"--session", "--continue", "--resume", "--no-session"} {
		if !strings.Contains(err.Error(), flag) {
			t.Fatalf("error %q missing %s", err.Error(), flag)
		}
	}
}

func TestIsNonInteractiveMode(t *testing.T) {
	cases := []struct {
		name string
		args cli.Args
		want bool
	}{
		{"print", cli.Args{Print: true}, true},
		{"json", cli.Args{Mode: cli.ModeJSON}, true},
		{"rpc", cli.Args{Mode: cli.ModeRPC}, true},
		{"interactive", cli.Args{}, false},
		{"text", cli.Args{Mode: cli.ModeText}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNonInteractiveMode(tc.args); got != tc.want {
				t.Fatalf("isNonInteractiveMode=%v want %v", got, tc.want)
			}
		})
	}
}

func TestFatalDiagnostic(t *testing.T) {
	if err := fatalDiagnostic([]Diagnostic{{Type: DiagWarning, Message: "w"}, {Type: DiagInfo}}); err != nil {
		t.Fatalf("non-error diagnostics should not be fatal: %v", err)
	}
	if err := fatalDiagnostic([]Diagnostic{{Type: DiagWarning}, {Type: DiagError, Message: "boom"}}); err == nil {
		t.Fatal("error diagnostic must be fatal")
	}
}

func TestResolveSessionClassifiesMatches(t *testing.T) {
	sessionDir := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	local, err := NewSessionManager(cwdB, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.AppendMessage(ai.NewUserMessage("local", nil)); err != nil {
		t.Fatal(err)
	}
	other, err := NewSessionManager(cwdA, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.AppendMessage(ai.NewUserMessage("other", nil)); err != nil {
		t.Fatal(err)
	}

	localRes := ResolveSession(local.SessionID(), cwdB, sessionDir)
	if localRes.Type != ResolvedSessionLocal || localRes.Path != local.File() {
		t.Fatalf("local resolution=%#v", localRes)
	}
	globalRes := ResolveSession(other.SessionID(), cwdB, sessionDir)
	if globalRes.Type != ResolvedSessionGlobal || globalRes.CWD != cwdA {
		t.Fatalf("global resolution=%#v", globalRes)
	}
	if missing := ResolveSession("does-not-exist", cwdB, sessionDir); missing.Type != ResolvedSessionNotFound {
		t.Fatalf("missing resolution=%#v", missing)
	}
	if pathRes := ResolveSession("/abs/path/session.jsonl", cwdB, sessionDir); pathRes.Type != ResolvedSessionPathType {
		t.Fatalf("path resolution=%#v", pathRes)
	}
}

func TestCreateSessionCrossProjectErrorsInNonInteractiveMode(t *testing.T) {
	sessionDir := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	other, err := NewSessionManager(cwdA, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.AppendMessage(ai.NewUserMessage("from A", nil)); err != nil {
		t.Fatal(err)
	}

	// From cwdB, a --session matching the cwdA session must NOT silently open the
	// other project in print/json mode.
	_, err = createSession(cli.Args{Session: other.SessionID(), Print: true}, cwdB, sessionDir, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "different project") {
		t.Fatalf("error=%v", err)
	}
	if !strings.Contains(err.Error(), cwdA) {
		t.Fatalf("error should reference origin cwd %q: %v", cwdA, err)
	}
}

func TestCreateSessionCrossProjectInteractiveForkConfirm(t *testing.T) {
	sessionDir := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	other, err := NewSessionManager(cwdA, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.AppendMessage(ai.NewUserMessage("from A", nil)); err != nil {
		t.Fatal(err)
	}

	t.Run("confirm forks into current project", func(t *testing.T) {
		var out bytes.Buffer
		forked, err := createSession(cli.Args{Session: other.SessionID()}, cwdB, sessionDir, nil, strings.NewReader("y\n"), &out)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if forked.CWD() != cwdB {
			t.Fatalf("forked cwd=%q want %q", forked.CWD(), cwdB)
		}
		if forked.SessionID() == other.SessionID() {
			t.Fatal("fork should receive a fresh session id")
		}
		if !strings.Contains(out.String(), "different project") {
			t.Fatalf("prompt missing origin notice: %q", out.String())
		}
	})

	t.Run("decline aborts cleanly", func(t *testing.T) {
		var out bytes.Buffer
		_, err := createSession(cli.Args{Session: other.SessionID()}, cwdB, sessionDir, nil, strings.NewReader("n\n"), &out)
		if !errors.Is(err, cli.ErrSessionSelectionCancelled) {
			t.Fatalf("err=%v, want ErrSessionSelectionCancelled", err)
		}
		if !strings.Contains(out.String(), "Aborted.") {
			t.Fatalf("decline should print Aborted: %q", out.String())
		}
	})
}

func TestValidateSessionCWDInteractiveContinue(t *testing.T) {
	sessionDir := t.TempDir()
	cwd := t.TempDir()
	fallback := t.TempDir()
	newMissingCwdSession := func() *SessionManager {
		s, err := NewSessionManager(cwd, sessionDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AppendMessage(ai.NewUserMessage("hi", nil)); err != nil {
			t.Fatal(err)
		}
		s.Header.CWD = "/path/that/does/not/exist"
		return s
	}

	t.Run("continue overrides runtime cwd without rewriting the file", func(t *testing.T) {
		session := newMissingCwdSession()
		var out bytes.Buffer
		if err := validateSessionCWD(session, fallback, false, strings.NewReader("y\n"), &out); err != nil {
			t.Fatalf("err=%v", err)
		}
		if session.CWD() != fallback {
			t.Fatalf("runtime cwd=%q want override %q", session.CWD(), fallback)
		}
		if session.Header.CWD != "/path/that/does/not/exist" {
			t.Fatalf("stored cwd should be untouched, got %q", session.Header.CWD)
		}
	})

	t.Run("decline aborts cleanly", func(t *testing.T) {
		session := newMissingCwdSession()
		var out bytes.Buffer
		err := validateSessionCWD(session, fallback, false, strings.NewReader("\n"), &out)
		if !errors.Is(err, cli.ErrSessionSelectionCancelled) {
			t.Fatalf("err=%v, want ErrSessionSelectionCancelled", err)
		}
	})
}

func TestResolveScopedModelsParsesThinkingSuffix(t *testing.T) {
	registry := ai.NewModelRegistry(t.TempDir(), ai.NewAuthStorage(t.TempDir()))
	resolved, warnings := resolveScopedModels(registry, []string{"faux:low", "faux", "missing"})
	if len(resolved) != 2 {
		t.Fatalf("resolved=%#v", resolved)
	}
	if resolved[0].Model.Provider != "faux" || resolved[0].ThinkingLevel != ai.ThinkingLow {
		t.Fatalf("first scoped model=%#v", resolved[0])
	}
	if resolved[1].Model.Provider != "faux" || resolved[1].ThinkingLevel != "" {
		t.Fatalf("second scoped model=%#v", resolved[1])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "missing") {
		t.Fatalf("warnings=%#v", warnings)
	}
}
