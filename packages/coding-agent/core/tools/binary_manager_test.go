package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedToolUsesCachedBinaryBeforeSystemPath(t *testing.T) {
	binDir := t.TempDir()
	cached := filepath.Join(binDir, managedToolBinaryFileName(managedTools[managedToolFD]))
	if err := os.WriteFile(cached, []byte("cached"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := replaceManagedToolHooks(t)
	managedToolLookPath = func(string) (string, error) { return filepath.Join(t.TempDir(), "system-fd"), nil }
	defer restore()

	result := ensureManagedTool(context.Background(), managedToolFD, binDir)
	if result.Path != cached {
		t.Fatalf("cached binary should win, got %q want %q", result.Path, cached)
	}
}

func TestManagedToolUsesSystemPathWhenCacheAbsent(t *testing.T) {
	systemPath := filepath.Join(t.TempDir(), managedToolBinaryFileName(managedTools[managedToolFD]))
	restore := replaceManagedToolHooks(t)
	managedToolLookPath = func(name string) (string, error) {
		if strings.HasPrefix(name, "fd") {
			return systemPath, nil
		}
		return "", exec.ErrNotFound
	}
	defer restore()

	result := ensureManagedTool(context.Background(), managedToolFD, t.TempDir())
	if result.Path != systemPath {
		t.Fatalf("system binary should be used when cache is absent, got %q want %q", result.Path, systemPath)
	}
}

func TestManagedToolDownloadsIntoCache(t *testing.T) {
	binDir := t.TempDir()
	restore := replaceManagedToolHooks(t)
	managedToolLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	managedToolDownloader = func(_ context.Context, config managedToolConfig, binDir string) (string, error) {
		path := filepath.Join(binDir, managedToolBinaryFileName(config))
		if err := os.WriteFile(path, []byte("downloaded"), 0o755); err != nil {
			return "", err
		}
		return path, nil
	}
	defer restore()

	result := ensureManagedTool(context.Background(), managedToolFD, binDir)
	if result.Path == "" || !result.Downloaded {
		t.Fatalf("download result not reported: %#v", result)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("downloaded binary missing: %v", err)
	}
}

func TestManagedToolConcurrentDownloadUsesSingleInstaller(t *testing.T) {
	binDir := t.TempDir()
	restore := replaceManagedToolHooks(t)
	managedToolLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	var calls atomic.Int32
	managedToolDownloader = func(_ context.Context, config managedToolConfig, binDir string) (string, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		path := filepath.Join(binDir, managedToolBinaryFileName(config))
		if err := os.WriteFile(path, []byte("downloaded"), 0o755); err != nil {
			return "", err
		}
		return path, nil
	}
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if result := ensureManagedTool(ctx, managedToolRG, binDir); result.Path == "" {
				errs <- errors.New(result.Diagnostic)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("installer called %d times, want 1", got)
	}
}

func TestFindDownloadFailureFallsBackWithDiagnostic(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	writeFile(t, root, "needle.txt", "x\n")
	restore := replaceManagedToolHooks(t)
	managedToolLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	var calls atomic.Int32
	managedToolDownloader = func(context.Context, managedToolConfig, string) (string, error) {
		calls.Add(1)
		return "", errors.New("network unavailable")
	}
	defer restore()

	result := FindTool{CWD: root, BinDir: binDir}.Execute(context.Background(), raw(map[string]any{"pattern": "*.txt"}), nil)
	if result.IsError {
		t.Fatalf("find should fall back instead of erroring: %s", toolText(result.Content))
	}
	text := toolText(result.Content)
	for _, want := range []string{"needle.txt", "fd not found; background download started", "built-in filesystem walk"} {
		if !strings.Contains(text, want) {
			t.Fatalf("find fallback output missing %q:\n%s", want, text)
		}
	}
	if result.Details == nil {
		t.Fatalf("find fallback should record details")
	}
	waitManagedToolInstallsForTest(t)

	result = FindTool{CWD: root, BinDir: binDir}.Execute(context.Background(), raw(map[string]any{"pattern": "*.txt"}), nil)
	text = toolText(result.Content)
	if !strings.Contains(text, "Failed to download fd: network unavailable") {
		t.Fatalf("find should report cached download failure:\n%s", text)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("downloader calls=%d, want 1", got)
	}
}

func TestGrepDownloadFailureFallsBackWithDiagnostic(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	writeFile(t, root, "a.txt", "NEEDLE\n")
	restore := replaceManagedToolHooks(t)
	managedToolLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	var calls atomic.Int32
	managedToolDownloader = func(context.Context, managedToolConfig, string) (string, error) {
		calls.Add(1)
		return "", errors.New("network unavailable")
	}
	defer restore()

	result := GrepTool{CWD: root, BinDir: binDir}.Execute(context.Background(), raw(map[string]any{"pattern": "NEEDLE"}), nil)
	if result.IsError {
		t.Fatalf("grep should fall back instead of erroring: %s", toolText(result.Content))
	}
	text := toolText(result.Content)
	for _, want := range []string{"a.txt:1: NEEDLE", "ripgrep not found; background download started", "built-in RE2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("grep fallback output missing %q:\n%s", want, text)
		}
	}
	if result.Details == nil {
		t.Fatalf("grep fallback should record details")
	}
	waitManagedToolInstallsForTest(t)

	result = GrepTool{CWD: root, BinDir: binDir}.Execute(context.Background(), raw(map[string]any{"pattern": "NEEDLE"}), nil)
	text = toolText(result.Content)
	if !strings.Contains(text, "Failed to download ripgrep: network unavailable") {
		t.Fatalf("grep should report cached download failure:\n%s", text)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("downloader calls=%d, want 1", got)
	}
}

func TestVerifyManagedToolArchiveDigest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asset.tar.gz")
	if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	// sha256("hello world")
	const sum = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	if err := verifyManagedToolArchiveDigest(path, "sha256:"+sum); err != nil {
		t.Fatalf("matching digest should pass: %v", err)
	}
	if err := verifyManagedToolArchiveDigest(path, "SHA256:"+strings.ToUpper(sum)); err != nil {
		t.Fatalf("digest comparison should be case-insensitive: %v", err)
	}
	if err := verifyManagedToolArchiveDigest(path, "sha256:0000"); err == nil {
		t.Fatal("mismatched digest must fail closed")
	}
	if err := verifyManagedToolArchiveDigest(path, "md5:whatever"); err != nil {
		t.Fatalf("unknown algorithm should be skipped, not rejected: %v", err)
	}
	if err := verifyManagedToolArchiveDigest(path, ""); err != nil {
		t.Fatalf("empty digest should be skipped: %v", err)
	}
}

func TestReleaseManagedToolLockSkipsForeignLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".rg.download.lock")
	lock, err := acquireManagedToolLock(context.Background(), lockPath)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate another installer reclaiming the stale lock: the file on disk now
	// belongs to a different token. Releasing our overrun lock must NOT delete it.
	if err := os.WriteFile(lockPath, []byte("pid=999\ntoken=someone-else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	releaseManagedToolLock(lock)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("foreign lock was deleted by the previous owner: %v", err)
	}

	// A lock we still own is removed normally.
	lock2, err := acquireManagedToolLock(context.Background(), filepath.Join(dir, ".fd.download.lock"))
	if err != nil {
		t.Fatal(err)
	}
	releaseManagedToolLock(lock2)
	if _, err := os.Stat(lock2.path); !os.IsNotExist(err) {
		t.Fatalf("owned lock should be removed on release, stat err=%v", err)
	}
}

func replaceManagedToolHooks(t *testing.T) func() {
	t.Helper()
	oldLookPath := managedToolLookPath
	oldDownloader := managedToolDownloader
	return func() {
		waitManagedToolInstallsForTest(t)
		managedToolInstallState = newManagedToolInstallState()
		managedToolLookPath = oldLookPath
		managedToolDownloader = oldDownloader
	}
}

func waitManagedToolInstallsForTest(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		managedToolInstallState.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for managed tool background install")
	}
}
