//go:build windows

package tools

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// hideWindow suppresses the console window that Windows would otherwise pop or
// flash for every spawned child process (shell commands) and for each taskkill
// invocation, mirroring TS's windowsHide: true on its spawn/exec sites
// (src/utils/shell.ts and the package-manager spawn). It allocates SysProcAttr
// only when nil and sets HideWindow without clobbering any pre-existing fields,
// so stdio and any future process-group/creation flags are preserved.
func hideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
