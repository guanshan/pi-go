package harnessenv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLocalExecutionEnvFileOperations(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	env, err := NewLocalExecutionEnv(cwd, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := env.AbsolutePath(ctx, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if abs != filepath.Join(cwd, "a", "b.txt") {
		t.Fatalf("abs=%q", abs)
	}
	joined, err := env.JoinPath(ctx, []string{cwd, "x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if joined != filepath.Join(cwd, "x", "y") {
		t.Fatalf("joined=%q", joined)
	}
	if err := env.WriteFile(ctx, "a/b.txt", []byte("one\ntwo\nthree\n")); err != nil {
		t.Fatal(err)
	}
	text, err := env.ReadTextFile(ctx, "a/b.txt")
	if err != nil || text != "one\ntwo\nthree\n" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	lines, err := env.ReadTextLines(ctx, "a/b.txt", 2)
	if err != nil || len(lines) != 2 || lines[1] != "two" {
		t.Fatalf("lines=%#v err=%v", lines, err)
	}
	binary, err := env.ReadBinaryFile(ctx, "a/b.txt")
	if err != nil || string(binary) != text {
		t.Fatalf("binary=%q err=%v", string(binary), err)
	}
	if err := env.AppendFile(ctx, "a/b.txt", []byte("four\n")); err != nil {
		t.Fatal(err)
	}
	info, err := env.FileInfo(ctx, "a/b.txt")
	if err != nil || info.Kind != FileKindFile || info.Size == 0 {
		t.Fatalf("info=%#v err=%v", info, err)
	}
	infos, err := env.ListDir(ctx, "a")
	if err != nil || len(infos) != 1 || infos[0].Name != "b.txt" {
		t.Fatalf("infos=%#v err=%v", infos, err)
	}
	exists, err := env.Exists(ctx, "a/b.txt")
	if err != nil || !exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
	missing, err := env.Exists(ctx, "missing")
	if err != nil || missing {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
	canonical, err := env.CanonicalPath(ctx, "a/b.txt")
	if err != nil || canonical == "" {
		t.Fatalf("canonical=%q err=%v", canonical, err)
	}
	tmpDir, err := env.CreateTempDir(ctx, "pi-agent-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmpDir); err != nil {
		t.Fatal(err)
	}
	tmpFile, err := env.CreateTempFile(ctx, "out-", ".log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(tmpFile, ".log") {
		t.Fatalf("tmpFile=%q", tmpFile)
	}
	if err := env.Remove(ctx, "a/b.txt", false, false); err != nil {
		t.Fatal(err)
	}
	exists, _ = env.Exists(ctx, "a/b.txt")
	if exists {
		t.Fatal("expected removed file")
	}
	if err := env.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmpDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp dir still exists: %v", err)
	}
}

func TestLocalExecutionEnvExecAndErrors(t *testing.T) {
	ctx := context.Background()
	env, err := NewLocalExecutionEnv(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := env.Exec(ctx, `printf "out"; printf "err" >&2`, ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "out\n" && result.Stdout != "out" {
		t.Fatalf("stdout=%q", result.Stdout)
	}
	if result.Stderr != "err\n" && result.Stderr != "err" {
		t.Fatalf("stderr=%q", result.Stderr)
	}
	_, err = env.ReadTextFile(ctx, "missing")
	var fileErr *FileError
	if !errors.As(err, &fileErr) || fileErr.Code != FileErrNotFound {
		t.Fatalf("file err=%#v", err)
	}
	badShell, err := NewLocalExecutionEnv(t.TempDir(), filepath.Join(t.TempDir(), "nope"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = badShell.Exec(ctx, "echo nope", ExecOptions{})
	var execErr *ExecutionError
	if !errors.As(err, &execErr) || execErr.Code != ExecErrShellUnavailable {
		t.Fatalf("exec err=%#v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = env.Exec(cancelled, "echo nope", ExecOptions{})
	if !errors.As(err, &execErr) || execErr.Code != ExecErrAborted {
		t.Fatalf("aborted err=%#v", err)
	}
	_, err = env.Exec(ctx, "sleep 1", ExecOptions{Timeout: 10 * time.Millisecond})
	if !errors.As(err, &execErr) || execErr.Code != ExecErrTimeout {
		t.Fatalf("timeout err=%#v", err)
	}
}

// TestLocalExecutionEnvExecCallbackError mirrors the TS callback_error test: a
// panicking output callback must surface as ExecErrCallback and terminate the
// child process tree so its delayed side effects never run.
func TestLocalExecutionEnvExecCallbackError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell for the delayed-write child")
	}
	ctx := context.Background()
	dir := t.TempDir()
	env, err := NewLocalExecutionEnv(dir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "after-kill")
	for _, tc := range []struct {
		name string
		opts ExecOptions
	}{
		{"stdout", ExecOptions{OnStdout: func(string) { panic("boom") }}},
		{"stderr", ExecOptions{OnStderr: func(string) { panic("boom") }}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(marker)
			stream := ">&2"
			if tc.name == "stdout" {
				stream = ""
			}
			cmd := `printf "x" ` + stream + `; sleep 5; touch "` + marker + `"`
			_, err := env.Exec(ctx, cmd, tc.opts)
			var execErr *ExecutionError
			if !errors.As(err, &execErr) || execErr.Code != ExecErrCallback {
				t.Fatalf("expected callback_error, got %#v", err)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Fatal("child kept running after callback error: marker file was created")
			}
		})
	}
}

// TestLocalExecutionEnvExecCallbackErrorNoLeak ensures a panicking callback on a
// long-running child kills the process and leaves no exec copy goroutines behind:
// recovering at the writer boundary must not leak the cmd's internal pumps.
func TestLocalExecutionEnvExecCallbackErrorNoLeak(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell for the long-running child")
	}
	ctx := context.Background()
	env, err := NewLocalExecutionEnv(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	before := runtime.NumGoroutine()
	// Emit output (triggering the panicking callback) then linger so the child is
	// only gone if Exec actually killed its process tree.
	_, err = env.Exec(ctx, `printf "x"; sleep 5`, ExecOptions{
		OnStdout: func(string) { panic("boom") },
	})
	var execErr *ExecutionError
	if !errors.As(err, &execErr) || execErr.Code != ExecErrCallback {
		t.Fatalf("expected callback_error, got %#v", err)
	}
	// The copy goroutines should unwind once Wait returns; allow a brief grace
	// period before asserting we did not strand any.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	if leaked := runtime.NumGoroutine() - before; leaked > 2 {
		t.Fatalf("leaked %d goroutines after callback error", leaked)
	}
}

// TestLocalExecutionEnvConcurrentTempDirs guards the tempDirs slice against the
// agent loop's parallel tool execution: multiple goroutines create temp
// dirs/files on a shared env while another calls Cleanup. Run with -race.
func TestLocalExecutionEnvConcurrentTempDirs(t *testing.T) {
	ctx := context.Background()
	env, err := NewLocalExecutionEnv(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := env.CreateTempDir(ctx, "pi-"); err != nil {
				t.Errorf("CreateTempDir: %v", err)
			}
			if _, err := env.CreateTempFile(ctx, "pi-", ".tmp"); err != nil {
				t.Errorf("CreateTempFile: %v", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := env.Cleanup(); err != nil {
			t.Errorf("Cleanup: %v", err)
		}
	}()
	wg.Wait()
	// A final Cleanup must remove everything that survived the concurrent run
	// without leaking entries or double-freeing the slice.
	if err := env.Cleanup(); err != nil {
		t.Fatalf("final Cleanup: %v", err)
	}
}
