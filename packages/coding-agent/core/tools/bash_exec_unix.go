//go:build unix

package tools

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup makes the command the leader of a new process group
// (setpgid) so the whole tree — including detached descendants — can be killed
// together. Mirrors the detached spawn + killProcessTree behavior in
// src/utils/shell.ts.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs the entire process group, falling back to killing
// just the direct child if the group kill fails.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	// Negative pid targets the whole process group.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
