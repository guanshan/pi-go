package tools

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

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

// mutationQueueKeys resolves path to stable queue keys. The cleaned absolute
// path keeps creates/overwrites for the same requested file serialized even
// when the file appears between concurrent calls; the resolved path, when
// available, keeps symlink aliases on the same queue too.
func mutationQueueKeys(path string) []string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	keys := []string{normalizeMutationQueueKey(abs)}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		keys = append(keys, normalizeMutationQueueKey(resolved))
	}
	sort.Strings(keys)
	n := 0
	for _, key := range keys {
		if n == 0 || key != keys[n-1] {
			keys[n] = key
			n++
		}
	}
	return keys[:n]
}

func normalizeMutationQueueKey(path string) string {
	key := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

// withFileMutationQueue runs fn while holding the per-file lock for path.
func withFileMutationQueue(path string, fn func() ai.ToolResult) ai.ToolResult {
	keys := mutationQueueKeys(path)

	fileMutations.mu.Lock()
	locks := make([]*refLock, 0, len(keys))
	for _, key := range keys {
		lock := fileMutations.locks[key]
		if lock == nil {
			lock = &refLock{}
			fileMutations.locks[key] = lock
		}
		lock.ref++
		locks = append(locks, lock)
	}
	fileMutations.mu.Unlock()

	for _, lock := range locks {
		lock.mu.Lock()
	}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].mu.Unlock()
		}
		fileMutations.mu.Lock()
		for i, key := range keys {
			lock := locks[i]
			lock.ref--
			if lock.ref == 0 {
				delete(fileMutations.locks, key)
			}
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
		// When the parent directory is read-only/permission-denied we cannot create
		// a temp sibling, but the target file itself may still be writable. TS uses
		// a plain writeFile here, so fall back to an in-place write to preserve that
		// success path (a writable file inside a read-only dir). This trades the
		// atomic-rename crash-safety for parity in that corner case only.
		if isWriteFallbackError(err) {
			return os.WriteFile(target, data, perm)
		}
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
	if err := replaceFileWithRetry(tmpName, target); err != nil {
		return err
	}
	committed = true
	return nil
}

func replaceFileWithRetry(oldpath, newpath string) error {
	const attempts = 8
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = replaceFile(oldpath, newpath)
		if err == nil {
			return nil
		}
		if runtime.GOOS != "windows" || !isWriteFallbackError(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return err
}

func fileWriteMode(path string, fallback os.FileMode) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return fallback
}

// isWriteFallbackError reports whether a CreateTemp failure indicates the parent
// directory is not writable (permission denied, read-only filesystem, or not a
// directory) — the cases where TS's plain writeFile would still succeed on a
// writable target inside it, so atomicWriteFile falls back to an in-place write.
func isWriteFallbackError(err error) bool {
	return errors.Is(err, os.ErrPermission) ||
		errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EROFS) ||
		errors.Is(err, syscall.ENOTDIR)
}
