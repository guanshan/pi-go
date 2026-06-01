//go:build windows

package codingagent

import (
	"os/exec"
	"strconv"
)

func KillProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Start()
}
