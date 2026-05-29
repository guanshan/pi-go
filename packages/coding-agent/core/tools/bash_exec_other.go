//go:build !unix

package tools

import "os/exec"

// configureProcessGroup is a no-op on platforms without POSIX process groups.
func configureProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the direct child process. Process-tree termination is
// not implemented on this platform.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
