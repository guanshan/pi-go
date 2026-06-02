package tools

import "sync"

// trackedDetachedChildren records the PIDs of shell processes started in their
// own process group (Setpgid, see configureProcessGroup). Per-command aborts and
// timeouts already kill the group via killProcessGroup, but a command that
// completes normally can leave detached descendants (e.g. `foo &`) running in
// that group. This registry lets the agent reap those on its own shutdown
// (SIGTERM/SIGHUP) via KillTrackedDetachedChildren. Mirrors the
// trackDetachedChildPid/killTrackedDetachedChildren set in src/utils/shell.ts.
//
// The registry lives here, the lowest package that both shell spawn sites (this
// package's BashTool and core's AgentSession.ExecuteBash) and the codingagent
// signal handler can reach without an import cycle.
var trackedDetachedChildren = struct {
	sync.Mutex
	pids map[int]struct{}
}{pids: map[int]struct{}{}}

// TrackDetachedChildPID records a spawned shell PID so it can be force-killed on
// shutdown. No-op for non-positive pids.
func TrackDetachedChildPID(pid int) {
	if pid <= 0 {
		return
	}
	trackedDetachedChildren.Lock()
	trackedDetachedChildren.pids[pid] = struct{}{}
	trackedDetachedChildren.Unlock()
}

// UntrackDetachedChildPID drops a PID once its command has finished and been
// reaped normally.
func UntrackDetachedChildPID(pid int) {
	if pid <= 0 {
		return
	}
	trackedDetachedChildren.Lock()
	delete(trackedDetachedChildren.pids, pid)
	trackedDetachedChildren.Unlock()
}

// KillTrackedDetachedChildren SIGKILLs the process tree of every still-tracked
// child and clears the registry. Called from the agent's shutdown signal handler.
func KillTrackedDetachedChildren() {
	trackedDetachedChildren.Lock()
	pids := make([]int, 0, len(trackedDetachedChildren.pids))
	for pid := range trackedDetachedChildren.pids {
		pids = append(pids, pid)
	}
	trackedDetachedChildren.pids = map[int]struct{}{}
	trackedDetachedChildren.Unlock()
	for _, pid := range pids {
		killProcessTreeByPID(pid)
	}
}
