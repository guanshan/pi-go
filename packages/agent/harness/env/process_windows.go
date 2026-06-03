//go:build windows

package harnessenv

import (
	"fmt"
	"os/exec"
)

func configureProcessGroup(cmd *exec.Cmd) {}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	// Block until taskkill completes (matching coding-agent core tools'
	// killProcessTree, which uses .Run()) so the process tree is actually gone
	// before we return, rather than fire-and-forget with .Start().
	_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(pid)).Run()
}
