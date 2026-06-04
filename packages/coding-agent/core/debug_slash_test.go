package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// TestSlashDebugWritesLog verifies the /debug command is recognized and writes a
// debug log to DebugLogPath() containing the JSONL messages section, mirroring
// the JSONL portion of TS interactive-mode handleDebugCommand. The rich-TUI
// rendered-line dump is intentionally not reproduced in line mode.
func TestSlashDebugWritesLog(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(EnvLegacyAgentDir, agentDir)
	t.Setenv(EnvAgentDir, "")

	agent := newAuthSlashTestAgent(t)
	if err := agent.Session.AppendMessage(ai.NewUserMessage("hello debug", nil)); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	done, err := handleSlash(context.Background(), agent, "/debug", &stdout, &stderr)
	if err != nil {
		t.Fatalf("/debug returned error: %v", err)
	}
	if done {
		t.Fatal("/debug should not terminate the session")
	}
	logPath := DebugLogPath()
	if filepath.Dir(logPath) != filepath.Clean(agentDir) {
		t.Fatalf("debug log should live in the agent dir, got %q (agentDir=%q)", logPath, agentDir)
	}
	if !strings.Contains(stdout.String(), logPath) {
		t.Fatalf("/debug should print the log path, got %q", stdout.String())
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("debug log not written: %v", err)
	}
	contents := string(raw)
	if !strings.Contains(contents, "Agent messages (JSONL)") {
		t.Fatalf("debug log missing JSONL section: %s", contents)
	}
	if !strings.Contains(contents, "hello debug") {
		t.Fatalf("debug log missing session message: %s", contents)
	}
}

// TestSlashDebugIsRegisteredInteractive asserts /debug is listed among the
// interactive slash commands so the TUI recognizes it.
func TestSlashDebugIsRegisteredInteractive(t *testing.T) {
	found := false
	for _, cmd := range interactiveSlashCommands {
		if cmd == "debug" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("debug not registered in interactiveSlashCommands")
	}
}
