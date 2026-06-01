//go:build !unix && !windows

package harnessenv

import (
	"os"
	"os/exec"
)

func configureProcessGroup(cmd *exec.Cmd) {}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
}
