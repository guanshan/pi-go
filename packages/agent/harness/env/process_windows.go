//go:build windows

package harnessenv

import (
	"fmt"
	"os/exec"

	"golang.org/x/sys/windows"
)

func configureProcessGroup(cmd *exec.Cmd) {}

// hideWindow suppresses the console window that Windows would otherwise pop/flash
// for every spawned child process and for each taskkill invocation, mirroring
// TS's windowsHide: true on its spawn/exec sites. It allocates SysProcAttr only
// when nil and sets HideWindow without clobbering any pre-existing fields (e.g. a
// future CreationFlags), preserving stdio/process-group wiring.
func hideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	// Block until taskkill completes (matching coding-agent core tools'
	// killProcessTree, which uses .Run()) so the process tree is actually gone
	// before we return, rather than fire-and-forget with .Start(). hideWindow
	// keeps each tree-kill from flashing a console window, matching TS's
	// windowsHide on its taskkill spawn.
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(pid))
	hideWindow(cmd)
	_ = cmd.Run()
}
