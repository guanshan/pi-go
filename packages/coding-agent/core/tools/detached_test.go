package tools

import "testing"

func detachedTracked(pid int) bool {
	trackedDetachedChildren.Lock()
	defer trackedDetachedChildren.Unlock()
	_, ok := trackedDetachedChildren.pids[pid]
	return ok
}

func detachedTrackedPIDs() []int {
	trackedDetachedChildren.Lock()
	defer trackedDetachedChildren.Unlock()
	pids := make([]int, 0, len(trackedDetachedChildren.pids))
	for pid := range trackedDetachedChildren.pids {
		pids = append(pids, pid)
	}
	return pids
}

// TestDetachedChildRegistryBookkeeping covers track/untrack/clear without
// touching real processes (the high PID makes the eventual kill a harmless
// no-op).
func TestDetachedChildRegistryBookkeeping(t *testing.T) {
	const pid = 0x7ffffff0
	TrackDetachedChildPID(0) // non-positive pids are ignored
	TrackDetachedChildPID(pid)
	if !detachedTracked(pid) {
		t.Fatal("TrackDetachedChildPID did not record the pid")
	}
	UntrackDetachedChildPID(pid)
	if detachedTracked(pid) {
		t.Fatal("UntrackDetachedChildPID did not remove the pid")
	}
	TrackDetachedChildPID(pid)
	KillTrackedDetachedChildren()
	if detachedTracked(pid) {
		t.Fatal("KillTrackedDetachedChildren did not clear the registry")
	}
}
