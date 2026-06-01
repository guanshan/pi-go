//go:build !unix && !windows

package codingagent

import "os"

func KillProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
}
