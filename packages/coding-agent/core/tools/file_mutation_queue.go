package tools

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/guanshan/pi-go/packages/ai"
)

// fileMutationQueue serializes mutation operations targeting the same file while
// letting operations on different files run in parallel. It mirrors the
// TypeScript withFileMutationQueue so that concurrent edit/write tool calls to
// one path cannot interleave their read-modify-write steps and corrupt the file.
type fileMutationQueue struct {
	mu    sync.Mutex
	locks map[string]*refLock
}

type refLock struct {
	mu  sync.Mutex
	ref int
}

var fileMutations = &fileMutationQueue{locks: map[string]*refLock{}}

// mutationQueueKey resolves path to a stable key. Like the TS implementation it
// uses the real (symlink-resolved) path when the file exists so two aliases of
// the same file share a queue, falling back to the cleaned absolute path.
func mutationQueueKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// withFileMutationQueue runs fn while holding the per-file lock for path.
func withFileMutationQueue(path string, fn func() ai.ToolResult) ai.ToolResult {
	key := mutationQueueKey(path)

	fileMutations.mu.Lock()
	lock := fileMutations.locks[key]
	if lock == nil {
		lock = &refLock{}
		fileMutations.locks[key] = lock
	}
	lock.ref++
	fileMutations.mu.Unlock()

	lock.mu.Lock()
	defer func() {
		lock.mu.Unlock()
		fileMutations.mu.Lock()
		lock.ref--
		if lock.ref == 0 {
			delete(fileMutations.locks, key)
		}
		fileMutations.mu.Unlock()
	}()

	return fn()
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	committed = true
	return nil
}

func fileWriteMode(path string, fallback os.FileMode) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return fallback
}
