//go:build windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

// TestBashCloseHangDetachedChildReturnsPromptly is the Windows regression for the
// classic "close hang": a shell command that exits but leaves a detached,
// long-lived child holding the inherited stdout/stderr handles. Without
// cmd.WaitDelay, cmd.Wait() would block forever waiting for those inherited
// handles to close. ExecuteBash sets WaitDelay (bash.go), so the call must return
// well within our timeout even though the grandchild outlives the shell.
//
// This mirrors the TS regression (a node -e detached child with inherited stdio).
// Windows-only: the inherited-handle hang is a Windows process-semantics issue;
// the POSIX path is covered by the unix abort test.
func TestBashCloseHangDetachedChildReturnsPromptly(t *testing.T) {
	if _, err := exec.LookPath("cmd"); err != nil {
		t.Skip("cmd.exe not available")
	}
	tool := BashTool{CWD: os.TempDir()}

	// `start /b` launches a detached child (a long ping used as a portable sleep)
	// that inherits the shell's stdio handles, then the shell exits immediately.
	command := `start /b ping -n 60 127.0.0.1 >NUL & exit 0`

	done := make(chan ai.ToolResult, 1)
	go func() {
		done <- tool.Execute(context.Background(), raw(map[string]any{"command": command}), nil)
	}()

	select {
	case <-done:
		// Returned promptly; the detached child did not wedge cmd.Wait().
	case <-time.After(15 * time.Second):
		t.Fatal("ExecuteBash hung on a detached child holding inherited stdio handles (close-hang regression)")
	}
}

// TestHideWindowSuppressesConsole verifies the windowsHide wiring: hideWindow
// sets HideWindow and ShellCommand applies it centrally so every bash spawn
// inherits it. Windows-only; the suppression is a no-op elsewhere.
func TestHideWindowSuppressesConsole(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "echo hi")
	hideWindow(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("hideWindow should set HideWindow=true")
	}
	shell := ShellCommand(ShellConfig{Shell: "cmd", Args: []string{"/c"}}, "echo hi")
	if shell.SysProcAttr == nil || !shell.SysProcAttr.HideWindow {
		t.Fatal("ShellCommand should suppress the console window")
	}
}
