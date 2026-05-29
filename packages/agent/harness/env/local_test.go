package harnessenv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
