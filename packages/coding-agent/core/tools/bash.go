package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

func (BashTool) Name() string { return "bash" }
func (BashTool) Description() string {
	return "Execute a shell command in the current working directory. Returns stdout and stderr, truncated to the last 2000 lines or 50KB."
}
func (BashTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"command": stringSchema("Bash command to execute"),
		"timeout": numberSchema("Timeout in seconds"),
	}, []string{"command"})
}

// bashUpdateThrottle bounds how often incremental output updates are emitted,
// matching BASH_UPDATE_THROTTLE_MS in src/core/tools/bash.ts.
const bashUpdateThrottle = 100 * time.Millisecond

func (t BashTool) Execute(ctx context.Context, raw json.RawMessage, onUpdate ToolUpdate) ai.ToolResult {
	if ctx == nil {
		ctx = context.Background()
	}
	var args struct {
		Command string  `json:"command"`
		Timeout float64 `json:"timeout"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Command == "" {
		return toolError("Invalid bash input: command is required")
	}
	command := args.Command
	if t.CommandPrefix != "" {
		command = t.CommandPrefix + "\n" + command
	}
	shellConfig, err := ResolveShellConfig(t.ShellPath)
	if err != nil {
		return toolError(err.Error())
	}

	// Use-time precheck that the working directory still exists, matching
	// createLocalBashOperations in src/core/tools/bash.ts (fsAccess(cwd) before
	// spawn). The harness no longer stats cwd at construct time, so this is the
	// correct place. The message wording is byte-for-byte from bash.ts.
	if t.CWD != "" {
		if info, statErr := os.Stat(t.CWD); statErr != nil || !info.IsDir() {
			return toolError(fmt.Sprintf("Working directory does not exist: %s\nCannot execute bash commands.", t.CWD))
		}
	}

	// The command runs in its own process group so that on abort/timeout the
	// entire tree (including detached children) can be killed, not just the
	// shell. See killProcessGroup (bash_exec_*.go).
	execCtx, cancelExec := context.WithCancel(ctx)
	defer cancelExec()
	cmd := ShellCommandContext(execCtx, shellConfig, command)
	cmd.Dir = t.CWD
	// Prepend the agent bin directory to PATH so migrated/installed tools (fd,
	// rg, package CLIs) resolve, mirroring getShellEnv() in src/core/tools/bash.ts.
	cmd.Env = ShellEnv(t.BinDir)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}
	cmd.WaitDelay = 2 * time.Second

	acc := newOutputAccumulator("pi-bash")
	defer acc.cleanup()

	// Stream stdout+stderr into the accumulator, emitting throttled updates.
	var mu sync.Mutex
	var lastUpdate time.Time
	var updateTimer *time.Timer
	emitUpdate := func() {
		if onUpdate == nil {
			return
		}
		snap := acc.snapshot()
		onUpdate(ai.ToolResult{Content: ai.TextBlocks(snap.content), Details: snap.details()})
	}
	scheduleUpdate := func() {
		if onUpdate == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		delay := bashUpdateThrottle - time.Since(lastUpdate)
		if delay <= 0 {
			if updateTimer != nil {
				updateTimer.Stop()
				updateTimer = nil
			}
			lastUpdate = time.Now()
			emitUpdate()
			return
		}
		if updateTimer == nil {
			updateTimer = time.AfterFunc(delay, func() {
				mu.Lock()
				updateTimer = nil
				lastUpdate = time.Now()
				mu.Unlock()
				emitUpdate()
			})
		}
	}

	if onUpdate != nil {
		onUpdate(ai.ToolResult{Content: nil})
	}

	writer := newStreamingWriter(acc, scheduleUpdate)
	cmd.Stdout = writer
	cmd.Stderr = writer

	var timedOut, aborted atomic.Bool
	if err := cmd.Start(); err != nil {
		return toolError(fmt.Sprintf("Failed to start command: %v", err))
	}
	// Track the process group so it can be reaped on agent shutdown if a
	// normally completed shell leaves background descendants behind.
	trackedPID := 0
	if cmd.Process != nil {
		trackedPID = cmd.Process.Pid
		TrackDetachedChildPID(trackedPID)
	}

	// Watch for context cancellation (abort) and timeout; kill the whole group.
	done := make(chan struct{})
	var cancelOnce sync.Once
	cancelCommand := func() {
		cancelOnce.Do(cancelExec)
	}
	var timer *time.Timer
	if args.Timeout > 0 {
		timer = time.AfterFunc(time.Duration(args.Timeout*float64(time.Second)), func() {
			timedOut.Store(true)
			cancelCommand()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			aborted.Store(true)
			cancelCommand()
		case <-done:
		}
	}()
	runErr := cmd.Wait()
	close(done)
	if trackedPID > 0 && !processGroupStillAlive(trackedPID) {
		UntrackDetachedChildPID(trackedPID)
	}
	if timer != nil {
		timer.Stop()
	}

	mu.Lock()
	if updateTimer != nil {
		updateTimer.Stop()
		updateTimer = nil
	}
	mu.Unlock()
	acc.finish()
	snap := acc.snapshot()
	details := snap.details()

	// Aborted/timed-out commands keep whatever partial output was produced and
	// report the reason, mirroring src/core/tools/bash.ts.
	if aborted.Load() {
		return ai.ToolResult{Content: ai.TextBlocks(appendStatus(formatBashOutput(snap, ""), "Command aborted")), Details: details, IsError: true}
	}
	if timedOut.Load() {
		return ai.ToolResult{Content: ai.TextBlocks(appendStatus(formatBashOutput(snap, ""), fmt.Sprintf("Command timed out after %.0f seconds", args.Timeout))), Details: details, IsError: true}
	}
	text := formatBashOutput(snap, "(no output)")
	if runErr != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			code = exit.ExitCode()
		}
		return ai.ToolResult{Content: ai.TextBlocks(appendStatus(text, fmt.Sprintf("Command exited with code %d", code))), Details: details, IsError: true}
	}
	return ai.ToolResult{Content: ai.TextBlocks(text), Details: details}
}

// streamingWriter feeds command output into the accumulator and schedules an
// incremental update on each write.
type streamingWriter struct {
	mu       sync.Mutex
	acc      *outputAccumulator
	schedule func()
}

func newStreamingWriter(acc *outputAccumulator, schedule func()) *streamingWriter {
	return &streamingWriter{acc: acc, schedule: schedule}
}

func (w *streamingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.acc.append(p)
	w.mu.Unlock()
	w.schedule()
	return len(p), nil
}

func appendStatus(text, status string) string {
	// Mirror bash.ts:370 `${text ? `${text}\n\n` : ""}${status}`: keep the output
	// verbatim (no trailing-newline trim) and only prepend it when non-empty.
	if text == "" {
		return status
	}
	return text + "\n\n" + status
}

func detailsOrNil(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	return m
}
