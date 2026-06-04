//go:build !windows

package codingagent

import "os/exec"

// hidePMWindow is a no-op on non-Windows platforms: there is no console window to
// suppress. Defined so runPM compiles and behaves identically across platforms.
func hidePMWindow(cmd *exec.Cmd) {}
