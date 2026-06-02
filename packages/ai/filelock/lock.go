package filelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Tuning knobs. These mirror the retry/stale behaviour the TypeScript baseline
// gets from proper-lockfile so concurrent pi processes serialize access instead
// of clobbering each other. They are package-level vars (not consts) solely so
// tests can shrink the durations for deterministic, fast stale/heartbeat/retry
// coverage; production code never mutates them. The safety invariant
// heartbeat < staleAge must always hold (see TestHeartbeatBelowStaleAge): a live
// holder must be able to refresh its lock before it is judged abandoned, or
// live processes would steal each other's locks.
var (
	staleAge   = 30 * time.Second
	maxRetries = 100
	retryDelay = 20 * time.Millisecond
	// heartbeat must stay well below staleAge so a live holder keeps the lock
	// fresh; it mirrors proper-lockfile's update interval and stops a slow
	// operation under the lock (notably a network OAuth refresh) from being
	// mistaken for an abandoned lock and stolen mid-flight.
	heartbeat = 10 * time.Second

	mkdirLockDir = os.Mkdir
	runtimeGOOS  = runtime.GOOS
)

// WithLock serializes access to path across processes using an atomically
// created lock directory (path + ".lock"). Directory creation is atomic on all
// supported platforms, which makes it a portable advisory lock; every writer
// must funnel through here. A lock older than staleAge with no heartbeat is
// treated as abandoned and stolen so a crashed process cannot wedge the file
// forever.
func WithLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lockPath := path + ".lock"
	for attempt := 0; ; attempt++ {
		err := mkdirLockDir(lockPath, 0o700)
		if err == nil {
			return withHeartbeat(lockPath, fn)
		}
		if !isLockContentionError(err, lockPath) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > staleAge {
			// Lock looks abandoned by a crashed process; steal it and retry.
			_ = os.Remove(lockPath)
			continue
		}
		if attempt >= maxRetries {
			return fmt.Errorf("failed to acquire lock %s after %d attempts", lockPath, attempt)
		}
		time.Sleep(retryDelay)
	}
}

func isLockContentionError(err error, lockPath string) bool {
	if errors.Is(err, os.ErrExist) {
		return true
	}
	if runtimeGOOS != "windows" || !os.IsPermission(err) {
		return false
	}
	if _, statErr := os.Stat(lockPath); statErr == nil || os.IsPermission(statErr) {
		return true
	}
	if probe, probeErr := os.MkdirTemp(filepath.Dir(lockPath), ".lock-probe-*"); probeErr == nil {
		_ = os.Remove(probe)
		return true
	}
	return false
}

// withHeartbeat runs fn while a background goroutine periodically refreshes the
// lock directory's mtime, so other processes see a recently-touched lock and do
// not steal it as stale while fn is still running. The heartbeat is always
// stopped and the lock removed before returning.
func withHeartbeat(lockPath string, fn func() error) error {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				now := time.Now()
				_ = os.Chtimes(lockPath, now, now)
			}
		}
	}()
	err := fn()
	close(stop)
	<-done
	_ = os.Remove(lockPath)
	return err
}
