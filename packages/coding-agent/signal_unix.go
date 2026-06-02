//go:build !windows

package codingagent

import (
	"os"
	"syscall"
)

// shutdownSignals are the termination signals handled for graceful cleanup.
// SIGHUP is included on non-Windows platforms, matching the TS modes which
// register SIGHUP only when process.platform !== "win32".
func shutdownSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGHUP}
}

// signalExitCode maps a signal to the conventional 128+signum exit status
// (SIGTERM -> 143, SIGHUP -> 129), matching the TS shutdown codes.
func signalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 143
}
