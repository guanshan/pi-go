package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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
	if err := replaceFile(tmpName, target); err != nil {
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
