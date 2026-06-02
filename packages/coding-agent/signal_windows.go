//go:build windows

package codingagent

import (
	"os"
	"syscall"
)

// shutdownSignals are the termination signals handled for graceful cleanup.
// Windows does not support SIGHUP, so only SIGTERM is registered.
func shutdownSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM}
}

// signalExitCode maps a signal to the conventional 128+signum exit status.
// SIGTERM is treated as 143 to match the TS shutdown code.
func signalExitCode(os.Signal) int {
	return 143
}
