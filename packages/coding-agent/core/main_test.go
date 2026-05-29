package core

import (
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

func TestValidateSessionCWDRejectsMissingDirectory(t *testing.T) {
	session := InMemorySession("/path/that/does/not/exist")
	if err := validateSessionCWD(session, false); err == nil || !strings.Contains(err.Error(), "session cwd no longer exists") {
		t.Fatalf("error=%v", err)
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

func TestResolveScopedModelsParsesThinkingSuffix(t *testing.T) {
	registry := ai.NewModelRegistry(t.TempDir(), ai.NewAuthStorage(t.TempDir()))
	resolved := resolveScopedModels(registry, []string{"faux:low", "faux", "missing"})
	if len(resolved) != 2 {
		t.Fatalf("resolved=%#v", resolved)
	}
	if resolved[0].Model.Provider != "faux" || resolved[0].ThinkingLevel != ai.ThinkingLow {
		t.Fatalf("first scoped model=%#v", resolved[0])
	}
	if resolved[1].Model.Provider != "faux" || resolved[1].ThinkingLevel != "" {
		t.Fatalf("second scoped model=%#v", resolved[1])
	}
}
