//go:build unix

package tools

import (
	"os/exec"
	"testing"
	"time"
)

// TestKillTrackedDetachedChildrenKillsProcess proves the registry actually reaps
// a tracked detached process group on shutdown, which is the whole point of
// tracking PIDs that per-command aborts would otherwise miss.
func TestKillTrackedDetachedChildrenKillsProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	TrackDetachedChildPID(pid)
	KillTrackedDetachedChildren()

	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
		// reaped after the kill
	case <-time.After(5 * time.Second):
		t.Fatal("KillTrackedDetachedChildren did not kill the tracked process")
	}
}
