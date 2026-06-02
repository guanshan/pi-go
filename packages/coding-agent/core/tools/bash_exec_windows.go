//go:build windows

package tools

import (
	"fmt"
	"os/exec"
)

// configureProcessGroup is a no-op on Windows; process-tree termination is
// handled by taskkill in killProcessGroup rather than POSIX process groups.
func configureProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup terminates the entire process tree rooted at the spawned
// shell using `taskkill /F /T /PID`, mirroring the Windows branch of
// killProcessTree in src/utils/shell.ts. Without /T, grandchildren spawned by
// the shell can survive an abort/timeout and keep mutating the workspace.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(pid)).Run()
	// Fall back to killing the direct child in case taskkill is unavailable.
	_ = cmd.Process.Kill()
}

// killProcessTreeByPID terminates the process tree rooted at pid via taskkill.
// Used to reap tracked detached children on shutdown when only the pid is known.
func killProcessTreeByPID(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(pid)).Run()
}

func processGroupStillAlive(int) bool {
	return false
}
