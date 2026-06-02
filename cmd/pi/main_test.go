package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
	codingagent "github.com/guanshan/pi-go/packages/coding-agent"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestPiBinaryProcessSnapshots(t *testing.T) {
	binary := buildPiBinary(t)
	agentDir := t.TempDir()
	home := t.TempDir()
	cwd := t.TempDir()

	result := runPiProcess(t, binary, cwd, agentDir, home, "--version")
	if result.exitCode != 0 || !strings.Contains(result.stdout, "dev commit none built unknown") || result.stderr != "" {
		t.Fatalf("version result=%#v", result)
	}

	result = runPiProcess(t, binary, cwd, agentDir, home, "--session-id", "help-snapshot", "--help")
	if result.exitCode != 0 || !strings.Contains(result.stdout, "pi - AI coding assistant") || result.stderr != "" {
		t.Fatalf("help result=%#v", result)
	}
	if got := countJSONLFiles(t, agentDir); got != 0 {
		t.Fatalf("help created %d session files under %s", got, agentDir)
	}

	common := []string{
		"--model", "faux/faux",
		"--no-session",
		"--no-tools",
		"--no-context-files",
		"--no-extensions",
		"--no-skills",
		"--no-prompt-templates",
		"--no-themes",
		"--offline",
	}
	args := append([]string{"--print", "hello"}, common...)
	result = runPiProcess(t, binary, cwd, agentDir, home, args...)
	if result.exitCode != 0 || strings.TrimSpace(result.stdout) != "faux: hello" || strings.Contains(result.stderr, "Error:") {
		t.Fatalf("print result=%#v", result)
	}

	args = append([]string{"--mode", "json", "--print", "hello"}, common...)
	result = runPiProcess(t, binary, cwd, agentDir, home, args...)
	if result.exitCode != 0 || strings.Contains(result.stderr, "Error:") {
		t.Fatalf("json result=%#v", result)
	}
	lines := nonEmptyLines(result.stdout)
	if len(lines) < 2 {
		t.Fatalf("json stdout lines=%#v", lines)
	}
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("header json=%q err=%v", lines[0], err)
	}
	if header["type"] != "session" {
		t.Fatalf("header=%#v", header)
	}
	sawMessageEnd := false
	for _, line := range lines[1:] {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("event json=%q err=%v", line, err)
		}
		if event["type"] == "message_end" {
			sawMessageEnd = true
		}
	}
	if !sawMessageEnd {
		t.Fatalf("json events missing message_end: %s", result.stdout)
	}
}

// Environment switches that turn this test binary into the in-process "child"
// (re-exec target). The child sets up the scripted faux provider in-process —
// the spawned pi binary cannot see ai.SetFauxResponses, so we drive the real
// MainWithOptions flow inside a forked copy of the test process instead.
const (
	envPrintErrorChild   = "PI_PRINT_ERROR_CHILD"
	envPrintErrorMarker  = "PI_PRINT_ERROR_SHUTDOWN_MARKER"
	envPrintErrorMessage = "PI_PRINT_ERROR_MESSAGE"
)

// TestPrintModeAssistantErrorExitsNonZeroAndShutsDown mirrors print-mode.test.ts:126
// ("emits session_shutdown and returns non-zero on assistant error"). It scripts
// the faux provider to end a turn with stopReason=="error" and runs the full
// print-mode entrypoint end-to-end (in a re-executed child process so the
// child's os.Exit is observable). The contract: the process exits NON-ZERO, the
// errorMessage is written to stderr, and session_shutdown(quit) is emitted via
// the extension runner during disposal.
func TestPrintModeAssistantErrorExitsNonZeroAndShutsDown(t *testing.T) {
	if os.Getenv(envPrintErrorChild) == "1" {
		runPrintErrorChild()
		return
	}

	markerPath := filepath.Join(t.TempDir(), "shutdown.json")
	const wantErrorMessage = "provider failure"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestPrintModeAssistantErrorExitsNonZeroAndShutsDown$", "-test.v")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		envPrintErrorChild+"=1",
		envPrintErrorMarker+"="+markerPath,
		envPrintErrorMessage+"="+wantErrorMessage,
		"PI_AGENT_DIR="+t.TempDir(),
		"PI_CODING_AGENT_DIR="+t.TempDir(),
		"PI_OFFLINE=1",
		"PI_SKIP_VERSION_CHECK=1",
		"HOME="+t.TempDir(),
		"USERPROFILE="+t.TempDir(),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("print-error child timed out: %v\nstdout=%s\nstderr=%s", ctx.Err(), stdout.String(), stderr.String())
	}

	// Contract 1: the process exits non-zero on assistant error (print-mode.ts:136).
	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("child failed to start: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
		}
		exitCode = exitErr.ExitCode()
	}
	if exitCode == 0 {
		t.Fatalf("exitCode=0, want non-zero\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}

	// Contract 2: the assistant errorMessage is reported on stderr
	// (print-mode.ts:135 / errorSpy assertion in the TS test).
	if !strings.Contains(stderr.String(), wantErrorMessage) {
		t.Fatalf("stderr missing %q\nstderr=%s", wantErrorMessage, stderr.String())
	}

	// Contract 3: session_shutdown(quit) is emitted during disposal, even on the
	// error exit path (the TS test asserts extensionRunner.emit was called once
	// with { type: "session_shutdown", reason: "quit" }). The child records the
	// emitted event to the marker file from a session_shutdown handler.
	raw, readErr := os.ReadFile(markerPath)
	if readErr != nil {
		t.Fatalf("session_shutdown not emitted (no marker %s): %v\nstdout=%s\nstderr=%s", markerPath, readErr, stdout.String(), stderr.String())
	}
	var marker struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
		Count  int    `json:"count"`
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		t.Fatalf("marker json=%q err=%v", raw, err)
	}
	if marker.Type != "session_shutdown" {
		t.Fatalf("marker type=%q, want session_shutdown", marker.Type)
	}
	if marker.Reason != "quit" {
		t.Fatalf("marker reason=%q, want quit", marker.Reason)
	}
	if marker.Count != 1 {
		t.Fatalf("session_shutdown emitted %d times, want exactly 1", marker.Count)
	}
}

// runPrintErrorChild is the re-executed child half of
// TestPrintModeAssistantErrorExitsNonZeroAndShutsDown. It scripts the faux
// provider to fail the turn, registers an extension that records the
// session_shutdown event to a marker file, and drives the real print-mode
// entrypoint. RunWithOptions calls os.Exit(1) on the assistant-error path, so
// this function does not return.
func runPrintErrorChild() {
	markerPath := os.Getenv(envPrintErrorMarker)
	errorMessage := os.Getenv(envPrintErrorMessage)

	// Script the faux provider so the single prompt turn ends with an assistant
	// error stop reason, mirroring createAssistantMessage({ stopReason: "error",
	// errorMessage }) in print-mode.test.ts.
	ai.SetFauxResponses([]ai.FauxResponse{
		{StopReason: "error", ErrorMessage: errorMessage},
	})
	defer ai.ResetFauxResponses()

	shutdownCount := 0
	factory := codingagent.ExtensionFactory(func(api *codingagent.ExtensionAPI) error {
		api.On("session_shutdown", func(payload any) {
			event, ok := payload.(*coreext.SessionShutdownEvent)
			if !ok {
				return
			}
			shutdownCount++
			data, _ := json.Marshal(struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
				Count  int    `json:"count"`
			}{Type: event.Type, Reason: string(event.Reason), Count: shutdownCount})
			_ = os.WriteFile(markerPath, data, 0o644)
		})
		return nil
	})

	// Mirror the proven-working MainWithOptions invocation in coding_agent_test.go
	// (TestMainWithOptionsLoadsExtensionFactories). --no-extensions only gates
	// disk-loaded extensions, never the injected MainOptions.ExtensionFactories,
	// so the session_shutdown handler stays wired while the rest of the resource
	// loading is suppressed.
	args := []string{
		"--print", "trigger error",
		"--model", "faux/faux",
		"--no-session",
		"--no-tools",
		"--no-context-files",
		"--no-skills",
		"--no-prompt-templates",
		"--no-themes",
		"--offline",
	}
	err := codingagent.RunWithOptions(context.Background(), codingagent.BuildInfo{}, args, codingagent.MainOptions{
		ExtensionFactories: []codingagent.ExtensionFactory{factory},
	})
	// On the assistant-error path RunWithOptions calls os.Exit(1) and never
	// returns. If it does return, surface the error and exit non-zero so the
	// parent's assertions still hold for a meaningful reason.
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
	}
	os.Exit(2)
}

type piProcessResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func buildPiBinary(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "pi-test")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = "."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build cmd/pi: %v\n%s", err, stderr.String())
	}
	return exe
}

func runPiProcess(t *testing.T, binary, cwd, agentDir, home string, args ...string) piProcessResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"PI_AGENT_DIR="+agentDir,
		"PI_CODING_AGENT_DIR="+agentDir,
		"PI_OFFLINE=1",
		"PI_SKIP_VERSION_CHECK=1",
		"HOME="+home,
		"USERPROFILE="+home,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("pi process timed out: %v args=%v stdout=%s stderr=%s", ctx.Err(), args, stdout.String(), stderr.String())
	}
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("pi process failed to start: %v", err)
		}
	}
	return piProcessResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode}
}

func countJSONLFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(d.Name(), ".jsonl") {
			count++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return count
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
