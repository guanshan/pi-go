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

// killProcessTreeByPID SIGKILLs the process group led by pid (negative pid),
// falling back to the single process. Used to reap tracked detached children on
// shutdown when only the pid is known. Mirrors killProcessTree in shell.ts.
func killProcessTreeByPID(pid int) {
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func processGroupStillAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || err == syscall.EPERM
}
