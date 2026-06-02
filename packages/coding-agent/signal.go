package codingagent

import (
	"os"
	"os/signal"

	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

// InstallSignalShutdown is a core.ShutdownInstaller: once the agent runtime
// exists, it handles termination signals (SIGTERM, plus SIGHUP on non-Windows)
// by killing tracked detached children, disposing the runtime, and exiting with
// the conventional 128+signum status — mirroring killTrackedDetachedChildren()
// + shutdown(143/129) in the TS print/rpc/interactive modes.
//
// Plain context cancellation is insufficient because RPC and interactive modes
// block on a stdin scanner that cancellation cannot interrupt; only an explicit
// process exit reliably unblocks them. A second signal forces an immediate hard
// exit in case dispose hangs.
func InstallSignalShutdown(dispose func()) (stop func()) {
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, shutdownSignals()...)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-sigCh:
			go func() {
				<-sigCh
				os.Exit(130)
			}()
			catools.KillTrackedDetachedChildren()
			if dispose != nil {
				dispose()
			}
			os.Exit(signalExitCode(sig))
		case <-done:
		}
	}()
	var stopped bool
	return func() {
		if stopped {
			return
		}
		stopped = true
		signal.Stop(sigCh)
		close(done)
	}
}
