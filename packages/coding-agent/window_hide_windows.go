//go:build windows

package codingagent

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// hidePMWindow suppresses the console window Windows would otherwise pop or flash
// for each package-manager / git spawn in runPM, mirroring TS's windowsHide:
// true on its package-install spawn sites. It allocates SysProcAttr only when nil
// and sets HideWindow without clobbering pre-existing fields, leaving stdio
// wiring untouched.
func hidePMWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
