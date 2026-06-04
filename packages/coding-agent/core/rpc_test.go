package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
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

// TestRunRPCHandlesLineLargerThan10MB verifies the RPC reader no longer caps
// command lines at 10MB. A single command whose JSON payload exceeds the old
// bufio.Scanner buffer limit must still be parsed and handled, with framing
// preserved (one LF-terminated record).
func TestRunRPCHandlesLineLargerThan10MB(t *testing.T) {
	runtime := newRPCTestRuntime(t)
	// 11MB session name -> the serialized command line is >10MB.
	bigName := strings.Repeat("a", 11*1024*1024)
	in := new(bytes.Buffer)
	writeRPCCommandLine(t, in, map[string]any{"id": "1", "type": "set_session_name", "name": bigName})
	if in.Len() <= 10*1024*1024 {
		t.Fatalf("test setup: expected command line >10MB, got %d bytes", in.Len())
	}
	var out bytes.Buffer
	if err := RunRPC(context.Background(), runtime, in, &out); err != nil {
		t.Fatalf("RunRPC error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"command":"set_session_name"`)) ||
		!bytes.Contains(out.Bytes(), []byte(`"success":true`)) {
		t.Fatalf("large line was not processed successfully: %s", truncForLog(out.String()))
	}
	if got := runtime.Session().Session.BuildContext().Name; got != bigName {
		t.Fatalf("session name not set from large line (len got=%d want=%d)", len(got), len(bigName))
	}
}

func TestRunRPCForwardsExtensionUIRequest(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	pr, pw := io.Pipe()
	out := &syncBuffer{}
	rpcDone := make(chan error, 1)
	go func() {
		rpcDone <- RunRPC(context.Background(), runtime, pr, out)
	}()
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})

	handler := waitForExtensionUIHandler(t, api)

	type uiResponse struct {
		result json.RawMessage
		err    error
	}
	uiDone := make(chan uiResponse, 1)
	go func() {
		result, err := handler(context.Background(), "select", json.RawMessage(`{"message":"Pick","choices":["a","b"]}`))
		uiDone <- uiResponse{result: result, err: err}
	}()

	uiID := waitForRPCUIRequest(t, out, "select")
	// Respond with the TS host-facing shape: {type:"extension_ui_response", id,
	// value} (rpc-types.ts RpcExtensionUIResponse).
	raw, err := json.Marshal(map[string]any{"type": "extension_ui_response", "id": uiID, "value": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write extension_ui_response: %v", err)
	}

	select {
	case got := <-uiDone:
		if got.err != nil {
			t.Fatalf("ui handler error: %v", got.err)
		}
		if string(got.result) != `"b"` {
			t.Fatalf("ui handler result=%s want %q", got.result, `"b"`)
		}
	case <-time.After(time.Second):
		t.Fatal("RPC UI handler did not receive response")
	}
	_ = pw.Close()
	select {
	case err := <-rpcDone:
		if err != nil {
			t.Fatalf("RunRPC returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunRPC did not return")
	}

	// The emitted request must carry the TS-flattened select shape:
	// {type:"extension_ui_request", id, method:"select", title, options}.
	line := findRPCUIRequestLine(t, out, "select")
	var req struct {
		Type    string   `json:"type"`
		ID      string   `json:"id"`
		Method  string   `json:"method"`
		Title   string   `json:"title"`
		Options []string `json:"options"`
	}
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		t.Fatalf("unmarshal request line: %v (%s)", err, line)
	}
	if req.Type != "extension_ui_request" || req.Method != "select" {
		t.Fatalf("unexpected request envelope: %s", line)
	}
	if req.Title != "Pick" || len(req.Options) != 2 || req.Options[0] != "a" || req.Options[1] != "b" {
		t.Fatalf("select request not flattened to TS shape: %s", line)
	}
}

// TestRunRPCExtensionUIResponseConfirmRoundTrip verifies the confirm method
// round-trips: the host replies with {type:"extension_ui_response", id,
// confirmed:true} and the extension handler resolves to the boolean true,
// matching the TS createDialogPromise confirm parseResponse.
func TestRunRPCExtensionUIResponseConfirmRoundTrip(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	pr, pw := io.Pipe()
	out := &syncBuffer{}
	rpcDone := make(chan error, 1)
	go func() {
		rpcDone <- RunRPC(context.Background(), runtime, pr, out)
	}()
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})

	handler := waitForExtensionUIHandler(t, api)
	type uiResponse struct {
		result json.RawMessage
		err    error
	}
	uiDone := make(chan uiResponse, 1)
	go func() {
		result, err := handler(context.Background(), "confirm", json.RawMessage(`{"message":"Proceed?","detail":"do it"}`))
		uiDone <- uiResponse{result: result, err: err}
	}()

	uiID := waitForRPCUIRequest(t, out, "confirm")
	raw, err := json.Marshal(map[string]any{"type": "extension_ui_response", "id": uiID, "confirmed": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write extension_ui_response: %v", err)
	}

	select {
	case got := <-uiDone:
		if got.err != nil {
			t.Fatalf("confirm handler error: %v", got.err)
		}
		if string(got.result) != "true" {
			t.Fatalf("confirm result=%s want true", got.result)
		}
	case <-time.After(time.Second):
		t.Fatal("confirm handler did not receive response")
	}

	// The confirm request flattens detail -> message and message -> title.
	line := findRPCUIRequestLine(t, out, "confirm")
	var req struct {
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		t.Fatalf("unmarshal confirm request: %v", err)
	}
	if req.Title != "Proceed?" || req.Message != "do it" {
		t.Fatalf("confirm request not flattened to TS shape: %s", line)
	}

	_ = pw.Close()
	select {
	case err := <-rpcDone:
		if err != nil {
			t.Fatalf("RunRPC returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunRPC did not return")
	}
}

func TestRunRPCNotifyDoesNotRequireUIResponse(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	pr, pw := io.Pipe()
	out := &syncBuffer{}
	rpcDone := make(chan error, 1)
	go func() {
		rpcDone <- RunRPC(context.Background(), runtime, pr, out)
	}()
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})

	handler := waitForExtensionUIHandler(t, api)
	result, err := handler(context.Background(), "notify", json.RawMessage(`{"message":"Heads up","level":"info"}`))
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if string(result) != "null" {
		t.Fatalf("notify result=%s want null", result)
	}
	_ = waitForRPCUIRequest(t, out, "notify")
	_ = pw.Close()
	select {
	case err := <-rpcDone:
		if err != nil {
			t.Fatalf("RunRPC returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunRPC did not return")
	}
}

func waitForExtensionUIHandler(t *testing.T, api *coreext.API) coreext.UIRequestHandler {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if handler := api.UIHandler(); handler != nil {
			return handler
		}
		select {
		case <-deadline:
			t.Fatal("RPC did not bind extension UI handler")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// findRPCUIRequestLine returns the full JSON line for the first emitted
// extension_ui_request with the given method, for asserting flattened fields.
func findRPCUIRequestLine(t *testing.T, out *syncBuffer, method string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record struct {
			Type   string `json:"type"`
			Method string `json:"method"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Type == "extension_ui_request" && record.Method == method {
			return line
		}
	}
	t.Fatalf("no extension_ui_request with method %q in output: %s", method, out.String())
	return ""
}

func waitForRPCUIRequest(t *testing.T, out *syncBuffer, method string) string {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var record struct {
				Type   string `json:"type"`
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal([]byte(line), &record) == nil && record.Type == "extension_ui_request" && record.Method == method {
				if record.ID == "" {
					t.Fatalf("extension_ui_request missing id: %s", line)
				}
				return record.ID
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s extension_ui_request; output=%s", method, out.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func truncForLog(s string) string {
	if len(s) > 200 {
		return s[:200] + "...(truncated)"
	}
	return s
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
