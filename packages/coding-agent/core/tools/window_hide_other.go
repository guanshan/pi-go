//go:build !windows

package tools

import "os/exec"

// hideWindow is a no-op on non-Windows platforms: there is no console window to
// suppress. Defined so the cross-platform call sites (shell_config.go,
// package_manager.go, taskkill helpers) compile and behave identically.
func hideWindow(cmd *exec.Cmd) {}
