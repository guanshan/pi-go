//go:build unix

package core

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

// TestExecuteBashAbortCancelsAndDoesNotHang starts a direct ExecuteBash command
// that backgrounds a long sleep, then aborts it via AbortBash (the same path a
// signal shutdown / runtime disposal drives). It verifies the call returns
// promptly (cmd.WaitDelay prevents a wedge on inherited pipes), reports the
// cancellation, and that the spawned shell process is gone.
func TestExecuteBashAbortCancelsAndDoesNotHang(t *testing.T) {
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	session := InMemorySession(cwd)
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")

	pidFile := filepath.Join(cwd, "shell.pid")
	// Record the shell PID, then background a sleep and wait on it so the shell
	// stays alive (holding the output pipes) until it is killed.
	command := "echo $$ > " + pidFile + "; sleep 60 & wait"

	type bashOutcome struct {
		result BashResult
		err    error
	}
	done := make(chan bashOutcome, 1)
	go func() {
		res, err := agent.ExecuteBash(context.Background(), command, BashRunOptions{ExcludeFromContext: true})
		done <- bashOutcome{result: res, err: err}
	}()

	// Wait for the shell to record its PID so we know it is running.
	shellPID := waitForPIDFile(t, pidFile)

	agent.AbortBash()

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("ExecuteBash returned error: %v", outcome.err)
		}
		if !outcome.result.Cancelled {
			t.Fatalf("expected Cancelled=true, got result=%#v", outcome.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ExecuteBash did not return after AbortBash (hung waiting on child pipes)")
	}

	// The shell itself must be terminated by the cancellation.
	if processAlive(shellPID) {
		t.Errorf("shell process %d survived AbortBash", shellPID)
	}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("shell never wrote its pid to %s", path)
	return 0
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Allow a brief grace period for the kill to take effect.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// signal 0 probes for existence without delivering a signal.
		if err := syscall.Kill(pid, 0); err != nil {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) == nil
}
