//go:build !unix && !windows

package tools

import (
	"os"
	"os/exec"
)

// configureProcessGroup is a no-op on platforms without POSIX process groups.
func configureProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the direct child process. Process-tree termination is
// not available on this platform.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// killProcessTreeByPID kills the single process; process-tree termination is not
// available on this platform.
func killProcessTreeByPID(pid int) {
	if pid <= 0 {
		return
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
}

func processGroupStillAlive(int) bool {
	return false
}
