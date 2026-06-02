package codingagent

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestInstallSignalShutdownExitsAndDisposes reproduces the review's manual RPC
// repro at the unit level: a process that installs the signal handler and then
// blocks must, on SIGTERM, run the dispose callback and exit 143 rather than
// hang. It uses the helper-process pattern because the handler calls os.Exit.
func TestInstallSignalShutdownExitsAndDisposes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM delivery semantics differ on Windows")
	}
	if os.Getenv("PI_SIGNAL_CHILD") == "1" {
		marker := os.Getenv("PI_SIGNAL_MARKER")
		InstallSignalShutdown(func() {
			_ = os.WriteFile(marker, []byte("disposed"), 0o600)
		})
		// Signal readiness, then block on a never-closed channel so only the
		// signal handler can terminate the process (mirrors a blocked stdin
		// scanner in RPC/interactive mode).
		_, _ = os.Stdout.WriteString("ready\n")
		select {}
	}

	marker := filepath.Join(t.TempDir(), "disposed")
	cmd := exec.Command(os.Args[0], "-test.run=TestInstallSignalShutdownExitsAndDisposes", "-test.v")
	cmd.Env = append(os.Environ(), "PI_SIGNAL_CHILD=1", "PI_SIGNAL_MARKER="+marker)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	ready := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "ready" {
				close(ready)
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child never became ready")
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("expected non-zero exit, got %v", err)
		}
		if code := exitErr.ExitCode(); code != 143 {
			t.Fatalf("exit code=%d, want 143", code)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child did not exit after SIGTERM (process hung)")
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("dispose callback did not run: %v", err)
	}
}
