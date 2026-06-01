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
	_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(pid)).Start()
}
