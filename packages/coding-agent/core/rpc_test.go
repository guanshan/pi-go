package core

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestRunRPCSwitchSession(t *testing.T) {
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
	switched, err := NewSessionManager(filepath.Join(cwd, "other"), sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := switched.AppendMessage(ai.NewUserMessage("switched", nil)); err != nil {
		t.Fatal(err)
	}
	in := new(bytes.Buffer)
	writeRPCCommandLine(t, in, map[string]any{"id": "1", "type": "switch_session", "sessionPath": switched.File(), "cwd": cwd})
	var out bytes.Buffer
	if err := RunRPC(context.Background(), runtime, in, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"command":"switch_session"`)) {
		t.Fatalf("unexpected rpc output: %s", out.String())
	}
	if got := ai.MessageText(runtime.Session().Session.BuildContext().Messages[0]); got != "switched" {
		t.Fatalf("switched text=%q", got)
	}
	if runtime.Cwd() != cwd {
		t.Fatalf("runtime cwd=%q", runtime.Cwd())
	}
}

func TestRunRPCSetSessionNameTrimsAndRejectsEmpty(t *testing.T) {
	runtime := newRPCTestRuntime(t)
	out := runRPCCommands(t, runtime,
		map[string]any{"id": "1", "type": "set_session_name", "name": "  named  "},
		map[string]any{"id": "2", "type": "set_session_name", "name": "   "},
	)
	if !bytes.Contains([]byte(out), []byte(`"command":"set_session_name"`)) || !bytes.Contains([]byte(out), []byte(`"success":true`)) {
		t.Fatalf("missing successful set name: %s", out)
	}
	if runtime.Session().Session.BuildContext().Name != "named" {
		t.Fatalf("name=%q", runtime.Session().Session.BuildContext().Name)
	}
	if !bytes.Contains([]byte(out), []byte(`"success":false`)) || !bytes.Contains([]byte(out), []byte("Session name cannot be empty")) && !bytes.Contains([]byte(out), []byte("session name cannot be empty")) {
		t.Fatalf("missing empty-name failure: %s", out)
	}
}

func TestRunRPCImportSession(t *testing.T) {
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
	importSource, err := NewSessionManager(filepath.Join(cwd, "import"), sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := importSource.AppendMessage(ai.NewUserMessage("imported", nil)); err != nil {
		t.Fatal(err)
	}
	in := new(bytes.Buffer)
	writeRPCCommandLine(t, in, map[string]any{"id": "1", "type": "import_session", "path": importSource.File(), "cwd": cwd})
	var out bytes.Buffer
	if err := RunRPC(context.Background(), runtime, in, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"command":"import_session"`)) {
		t.Fatalf("unexpected rpc output: %s", out.String())
	}
	if got := ai.MessageText(runtime.Session().Session.BuildContext().Messages[0]); got != "imported" {
		t.Fatalf("imported text=%q", got)
	}
	if runtime.Session().Session.File() == importSource.File() {
		t.Fatalf("expected imported session to be copied, got same file %q", runtime.Session().Session.File())
	}
}

func TestRunRPCImportSessionMissing(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	runtime, err := CreateAgentSessionRuntime(context.Background(), testRuntimeFactory(t), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: InMemorySession(cwd),
	})
	if err != nil {
		t.Fatal(err)
	}
	in := new(bytes.Buffer)
	writeRPCCommandLine(t, in, map[string]any{"id": "1", "type": "import_session", "path": "missing.jsonl"})
	var out bytes.Buffer
	if err := RunRPC(context.Background(), runtime, in, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"command":"import_session"`)) {
		t.Fatalf("unexpected rpc output: %s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`"success":false`)) {
		t.Fatalf("expected failed rpc response, got %s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("session import file not found")) {
		t.Fatalf("expected missing import error, got %s", out.String())
	}
}

func writeRPCCommandLine(t *testing.T, buffer *bytes.Buffer, value map[string]any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	buffer.Write(data)
	buffer.WriteByte('\n')
}
