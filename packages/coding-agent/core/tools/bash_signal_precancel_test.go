//go:build !windows

package tools

import (
	"context"
	"strings"
	"testing"
)

// TestBashPrecanceledContextReportsAborted verifies that when the context is
// already cancelled before the command can start, the bash tool reports
// "Command aborted" (mirroring bash.ts) instead of the raw
// "Failed to start command: context canceled".
func TestBashPrecanceledContextReportsAborted(t *testing.T) {
	tool := BashTool{CWD: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := tool.Execute(ctx, raw(map[string]any{"command": "echo hi"}), nil)
	if !res.IsError {
		t.Fatalf("precanceled ctx should be an error result")
	}
	out := toolText(res.Content)
	if !strings.Contains(out, "Command aborted") {
		t.Fatalf("expected 'Command aborted', got %q", out)
	}
	if strings.Contains(out, "Failed to start command") {
		t.Fatalf("should not surface raw start error, got %q", out)
	}
}

// TestBashSignalKillIsSuccess verifies that a command terminated by a signal
// (exit code -1 in Go / null in Node) is treated as success, matching bash.ts:397
// `exitCode !== 0 && exitCode !== null`. The command kills itself with SIGTERM.
func TestBashSignalKillIsSuccess(t *testing.T) {
	tool := BashTool{CWD: t.TempDir()}
	// Print output, then self-terminate via SIGKILL so the shell's exit status is
	// signal-based (not a numeric non-zero code).
	res := tool.Execute(context.Background(), raw(map[string]any{"command": "echo before; kill -KILL $$"}), nil)
	out := toolText(res.Content)
	if res.IsError {
		t.Fatalf("signal-killed command should be success, got error: %q", out)
	}
	if strings.Contains(out, "Command exited with code") {
		t.Fatalf("signal kill should not append exit-code status, got %q", out)
	}
	if !strings.Contains(out, "before") {
		t.Fatalf("expected partial output retained, got %q", out)
	}
}
