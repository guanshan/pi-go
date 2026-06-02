package filelock

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withKnobs temporarily overrides the package tuning vars for a single test and
// restores them afterward, so each test runs fast and deterministically without
// leaking shrunken durations into sibling tests.
func withKnobs(t *testing.T, stale, hb, delay time.Duration, retries int) {
	t.Helper()
	origStale, origHB, origDelay, origRetries := staleAge, heartbeat, retryDelay, maxRetries
	staleAge, heartbeat, retryDelay, maxRetries = stale, hb, delay, retries
	t.Cleanup(func() {
		staleAge, heartbeat, retryDelay, maxRetries = origStale, origHB, origDelay, origRetries
	})
}

// TestHeartbeatBelowStaleAge guards the core safety invariant: a live holder
// must be able to refresh (heartbeat) its lock before staleAge elapses,
// otherwise live processes would judge each other's healthy locks as abandoned
// and steal them mid-flight. A regression flipping heartbeat >= staleAge would
// fail here. This asserts the production defaults, independent of test knobs.
func TestHeartbeatBelowStaleAge(t *testing.T) {
	if heartbeat >= staleAge {
		t.Fatalf("safety invariant violated: heartbeat (%v) must be < staleAge (%v); a live holder could not refresh before being judged stale", heartbeat, staleAge)
	}
	// Leave generous headroom so a single slow heartbeat tick still beats the
	// stale threshold; proper-lockfile keeps a comfortable multiple.
	if heartbeat*2 > staleAge {
		t.Fatalf("heartbeat (%v) leaves too little headroom under staleAge (%v); want heartbeat*2 <= staleAge", heartbeat, staleAge)
	}
}

// TestStaleLockSteal verifies that a lock whose directory mtime is older than
// staleAge is treated as abandoned and stolen, allowing acquisition to proceed.
func TestStaleLockSteal(t *testing.T) {
	withKnobs(t, 50*time.Millisecond, 10*time.Millisecond, 5*time.Millisecond, 100)

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	lockPath := path + ".lock"

	// Simulate a crashed process that left a lock directory behind.
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("seed lock dir: %v", err)
	}
	// Backdate the mtime well past staleAge so it looks abandoned.
	old := time.Now().Add(-10 * time.Second)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatalf("backdate lock mtime: %v", err)
	}

	ran := false
	done := make(chan error, 1)
	go func() {
		done <- WithLock(path, func() error {
			ran = true
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WithLock should steal a stale lock, got error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WithLock did not steal the stale lock in time (likely treated as live)")
	}
	if !ran {
		t.Fatal("fn never ran after stale-lock steal")
	}
	// The lock must be released (removed) after fn completes.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock dir should be removed after release, stat err = %v", err)
	}
}

// TestStaleLockNotStolenWhenFresh verifies the negative case: a held lock whose
// mtime is kept fresh (younger than staleAge) is NOT stolen, so a contender
// waits and retries rather than stealing a live lock.
func TestStaleLockNotStolenWhenFresh(t *testing.T) {
	withKnobs(t, 200*time.Millisecond, 10*time.Millisecond, 5*time.Millisecond, 100)

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	lockPath := path + ".lock"

	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("seed lock dir: %v", err)
	}
	now := time.Now()
	if err := os.Chtimes(lockPath, now, now); err != nil {
		t.Fatalf("touch lock mtime: %v", err)
	}

	acquired := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithLock(path, func() error {
			close(acquired)
			return nil
		})
	}()

	// Within one staleAge window the fresh lock must not be stolen.
	select {
	case <-acquired:
		t.Fatal("WithLock stole a fresh (non-stale) lock; live-lock invariant violated")
	case <-time.After(staleAge / 2):
		// Good: still blocked because the lock is not stale yet.
	}

	// Release the lock; the waiter should now acquire promptly.
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("release seeded lock: %v", err)
	}
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not acquire after the fresh lock was released")
	}
	// Join the worker so its heartbeat goroutine is fully stopped before the
	// test (and t.Cleanup restoring the knobs) returns; otherwise the still-live
	// heartbeat would race the knob restore.
	if err := <-done; err != nil {
		t.Fatalf("waiter WithLock error: %v", err)
	}
}

// TestHeartbeatRefreshesMtime verifies that while fn runs, the background
// heartbeat periodically refreshes the lock directory's mtime, so other
// processes keep seeing a recently-touched (non-stale) lock.
func TestHeartbeatRefreshesMtime(t *testing.T) {
	// heartbeat well below staleAge, fn duration spanning several heartbeats.
	withKnobs(t, 500*time.Millisecond, 20*time.Millisecond, 5*time.Millisecond, 100)

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	lockPath := path + ".lock"

	var initialMtime time.Time
	gotInitial := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- WithLock(path, func() error {
			info, err := os.Stat(lockPath)
			if err != nil {
				return err
			}
			initialMtime = info.ModTime()
			close(gotInitial)
			<-release // hold the lock so the heartbeat can tick repeatedly
			return nil
		})
	}()

	<-gotInitial
	// Let several heartbeat ticks fire.
	time.Sleep(200 * time.Millisecond)

	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("stat lock while held: %v", err)
	}
	if !info.ModTime().After(initialMtime) {
		t.Fatalf("heartbeat did not refresh lock mtime: initial=%v current=%v", initialMtime, info.ModTime())
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("WithLock returned error: %v", err)
	}
}

// TestRetryExhaustion verifies that when the lock stays continuously held by
// another holder whose lock never goes stale, a contender exhausts maxRetries
// and returns the documented "failed to acquire lock ... after N attempts"
// error rather than stealing or blocking forever.
func TestRetryExhaustion(t *testing.T) {
	// staleAge large so the held lock never looks stale; small retry budget so
	// the contender exhausts quickly.
	withKnobs(t, 10*time.Second, 1*time.Second, 2*time.Millisecond, 5)

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	lockPath := path + ".lock"

	// Hold the lock directly (fresh mtime) for the whole contention window.
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("seed held lock: %v", err)
	}
	now := time.Now()
	if err := os.Chtimes(lockPath, now, now); err != nil {
		t.Fatalf("touch held lock: %v", err)
	}
	defer os.Remove(lockPath)

	ran := false
	err := WithLock(path, func() error {
		ran = true
		return nil
	})
	if err == nil {
		t.Fatal("expected retry-exhaustion error, got nil (lock should not have been acquired)")
	}
	if ran {
		t.Fatal("fn ran despite the lock being continuously held")
	}
	if !strings.Contains(err.Error(), "failed to acquire lock") ||
		!strings.Contains(err.Error(), lockPath) ||
		!strings.Contains(err.Error(), "attempts") {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestWindowsPermissionDuringLockMkdirRetries(t *testing.T) {
	withKnobs(t, time.Second, 100*time.Millisecond, time.Millisecond, 10)

	origMkdir, origGOOS := mkdirLockDir, runtimeGOOS
	var mkdirCalls int32
	mkdirLockDir = func(path string, perm os.FileMode) error {
		if strings.HasSuffix(path, ".lock") && atomic.AddInt32(&mkdirCalls, 1) == 1 {
			return &fs.PathError{Op: "mkdir", Path: path, Err: fs.ErrPermission}
		}
		return origMkdir(path, perm)
	}
	runtimeGOOS = "windows"
	t.Cleanup(func() {
		mkdirLockDir = origMkdir
		runtimeGOOS = origGOOS
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	ran := false
	if err := WithLock(path, func() error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("WithLock should retry transient Windows mkdir permission error, got: %v", err)
	}
	if !ran {
		t.Fatal("fn never ran after retrying transient Windows mkdir permission error")
	}
	if got := atomic.LoadInt32(&mkdirCalls); got < 2 {
		t.Fatalf("mkdir calls=%d, want at least 2", got)
	}
}

// TestMutualExclusionSerializes verifies that concurrent WithLock callers are
// serialized: at no point do two holders run fn at the same time. This is the
// cross-process auth.json safety guarantee, exercised here with goroutines.
func TestMutualExclusionSerializes(t *testing.T) {
	withKnobs(t, 5*time.Second, 1*time.Second, 1*time.Millisecond, 1000)

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	const workers = 8
	var inside int32
	var maxConcurrent int32
	var calls int32
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := WithLock(path, func() error {
				atomic.AddInt32(&calls, 1)
				n := atomic.AddInt32(&inside, 1)
				if n > atomic.LoadInt32(&maxConcurrent) {
					atomic.StoreInt32(&maxConcurrent, n)
				}
				time.Sleep(2 * time.Millisecond) // widen the critical-section window
				atomic.AddInt32(&inside, -1)
				return nil
			})
			if err != nil {
				t.Errorf("worker WithLock error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != workers {
		t.Fatalf("expected all %d workers to acquire, got %d", workers, got)
	}
	if got := atomic.LoadInt32(&maxConcurrent); got != 1 {
		t.Fatalf("lock did not serialize access: max concurrent holders = %d, want 1", got)
	}
}
