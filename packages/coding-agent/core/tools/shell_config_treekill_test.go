package tools

import (
	"os/exec"
	"runtime"
	"testing"
)

// TestConfigureTreeKill verifies the direct-shell path gets a cancel hook that
// kills the process tree, and on POSIX platforms is placed in its own process
// group so the kill reaches detached descendants.
func TestConfigureTreeKill(t *testing.T) {
	cmd := exec.Command("true")
	ConfigureTreeKill(cmd)

	if cmd.Cancel == nil {
		t.Fatal("ConfigureTreeKill must set cmd.Cancel so a canceled context kills the tree")
	}
	if runtime.GOOS != "windows" {
		if cmd.SysProcAttr == nil {
			t.Fatal("ConfigureTreeKill must configure a process group on POSIX platforms")
		}
	}

	// A nil command must be a safe no-op.
	ConfigureTreeKill(nil)
}
