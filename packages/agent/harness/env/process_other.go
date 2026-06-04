//go:build !unix && !windows

package harnessenv

import (
	"os"
	"os/exec"
)

func configureProcessGroup(cmd *exec.Cmd) {}

// hideWindow is a no-op on non-Windows platforms (there is no console window to
// suppress); the Windows build provides the real implementation.
func hideWindow(cmd *exec.Cmd) {}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
}
