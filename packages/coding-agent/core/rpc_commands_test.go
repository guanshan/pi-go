package core

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// runRPCCommands feeds the given commands through RunRPC and returns the
// captured output. Input is a finite buffer so RunRPC returns after draining.
func runRPCCommands(t *testing.T, runtime *AgentSessionRuntime, commands ...map[string]any) string {
	t.Helper()
	in := new(bytes.Buffer)
	for _, c := range commands {
		writeRPCCommandLine(t, in, c)
	}
	var out bytes.Buffer
	if err := RunRPC(context.Background(), runtime, in, &out); err != nil {
		t.Fatalf("RunRPC error: %v", err)
	}
	return out.String()
}

func newRPCTestRuntime(t *testing.T) *AgentSessionRuntime {
	t.Helper()
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sessionDir := filepath.Join(agentDir, "sessions")
	initial, err := NewSessionManager(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(context.Background(), testRuntimeFactory(t), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: initial,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestRunRPCBashCommand(t *testing.T) {
	runtime := newRPCTestRuntime(t)
	out := runRPCCommands(t, runtime, map[string]any{"id": "1", "type": "bash", "command": "echo rpc-bash-ok"})
	if !strings.Contains(out, `"command":"bash"`) || !strings.Contains(out, `"success":true`) {
		t.Fatalf("bash response missing: %s", out)
	}
	if !strings.Contains(out, "rpc-bash-ok") {
		t.Fatalf("bash output missing: %s", out)
	}
}

// TestRunRPCSetModelPersistsDefault locks P1-1 for the RPC surface: the
// documented `set_model` persistence side-effect must update settings.json
// (mirroring agent-session.ts setModel -> setDefaultModelAndProvider), not just
// append a session entry.
func TestRunRPCSetModelPersistsDefault(t *testing.T) {
	runtime := newRPCTestRuntime(t)
	out := runRPCCommands(t, runtime, map[string]any{"id": "1", "type": "set_model", "provider": "faux", "modelId": "faux"})
	if !strings.Contains(out, `"command":"set_model"`) || !strings.Contains(out, `"success":true`) {
		t.Fatalf("set_model response missing: %s", out)
	}
	settings := runtime.Session().Settings
	// Re-read from disk to prove the side-effect was persisted, not just held in memory.
	reloaded := NewSettingsManager(settings.CWD, settings.AgentDir)
	if reloaded.DefaultProvider() != "faux" || reloaded.DefaultModel() != "faux" {
		t.Fatalf("RPC set_model did not persist default: provider=%q model=%q",
			reloaded.DefaultProvider(), reloaded.DefaultModel())
	}
}

// TestRunRPCOutputDoesNotHTMLEscape locks 7.md P0-1 (RPC part): RPC JSONL output
// must not HTML-escape `<`, `>`, `&`, mirroring TS serializeJsonLine
// (`${JSON.stringify(value)}\n`). Go's default json.Marshal escapes them, which
// would diverge the wire bytes for common code payloads.
func TestRunRPCOutputDoesNotHTMLEscape(t *testing.T) {
	runtime := newRPCTestRuntime(t)
	out := runRPCCommands(t, runtime, map[string]any{"id": "1", "type": "bash", "command": "printf '%s' '<a> && b </a>'"})
	if !strings.Contains(out, `"command":"bash"`) || !strings.Contains(out, `"success":true`) {
		t.Fatalf("bash response missing: %s", out)
	}
	// The raw angle brackets and ampersands must appear verbatim in the JSON line.
	if !strings.Contains(out, "<a> && b </a>") {
		t.Fatalf("expected raw <>& in RPC output, got: %s", out)
	}
	// The HTML-escaped forms (Go's json.Marshal default) must NOT appear: the
	// literal 6-char escape sequences "<", ">", "&".
	for _, escaped := range []string{"\\u003c", "\\u003e", "\\u0026"} {
		if strings.Contains(out, escaped) {
			t.Fatalf("RPC output should not HTML-escape (found %s): %s", escaped, out)
		}
	}
}

func TestRunRPCBashRequiresCommand(t *testing.T) {
	runtime := newRPCTestRuntime(t)
	out := runRPCCommands(t, runtime, map[string]any{"id": "1", "type": "bash"})
	if !strings.Contains(out, `"success":false`) || !strings.Contains(out, "command is required") {
		t.Fatalf("expected failure for missing command: %s", out)
	}
}

func TestRunRPCSessionStatsAndExport(t *testing.T) {
	runtime := newRPCTestRuntime(t)
	if err := runtime.Session().Session.AppendMessage(ai.NewUserMessage("hi there", nil)); err != nil {
		t.Fatal(err)
	}
	out := runRPCCommands(t, runtime,
		map[string]any{"id": "s", "type": "get_session_stats"},
		map[string]any{"id": "e", "type": "export_html", "outputPath": filepath.Join(t.TempDir(), "out.html")},
		map[string]any{"id": "f", "type": "get_fork_messages"},
		map[string]any{"id": "l", "type": "get_last_assistant_text"},
		map[string]any{"id": "c", "type": "get_commands"},
		map[string]any{"id": "ab", "type": "abort_bash"},
	)
	for _, want := range []string{
		`"command":"get_session_stats"`,
		`"command":"export_html"`,
		`"command":"get_fork_messages"`,
		`"command":"get_last_assistant_text"`,
		`"command":"get_commands"`,
		`"command":"abort_bash"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in output: %s", want, out)
		}
	}
	if strings.Contains(out, `"success":false`) {
		t.Fatalf("unexpected failure response: %s", out)
	}
	// get_fork_messages should surface the user message we appended.
	if !strings.Contains(out, "hi there") {
		t.Fatalf("fork messages missing user text: %s", out)
	}
}

func TestRunRPCCloneRequiresLeaf(t *testing.T) {
	runtime := newRPCTestRuntime(t)
	// Fresh session has no current entry -> clone should fail clearly.
	out := runRPCCommands(t, runtime, map[string]any{"id": "1", "type": "clone"})
	if !strings.Contains(out, `"command":"clone"`) || !strings.Contains(out, `"success":false`) {
		t.Fatalf("expected clone failure on empty session: %s", out)
	}
}
