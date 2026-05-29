package harnessutils

import (
	"context"
	"strings"
	"testing"
)

func TestTruncationHelpers(t *testing.T) {
	head := TruncateHead("a\nb\nc", TruncationOptions{MaxLines: 2, MaxBytes: 100})
	if !head.Truncated || head.Content != "a\nb" || head.TruncatedBy != TruncatedByLines {
		t.Fatalf("head=%#v", head)
	}
	tail := TruncateTail("hello 世界", TruncationOptions{MaxLines: 10, MaxBytes: 6})
	if !tail.Truncated || !tail.LastLinePartial || tail.OutputBytes > 6 {
		t.Fatalf("tail=%#v", tail)
	}
	line, truncated := TruncateLine("abcdef", 3)
	if !truncated || line != "abc... [truncated]" {
		t.Fatalf("line=%q truncated=%v", line, truncated)
	}
	if got := FormatSize(1536); got != "1.5KB" {
		t.Fatalf("size=%q", got)
	}
}

type fakeShellEnv struct {
	outputs  []string
	tempPath string
	files    map[string]string
	err      error
}

func (f *fakeShellEnv) Exec(ctx context.Context, command string, opts ShellExecOptions) (ShellExecResult, error) {
	for _, output := range f.outputs {
		opts.OnStdout(output)
	}
	if f.err != nil {
		return ShellExecResult{}, f.err
	}
	return ShellExecResult{ExitCode: 0}, nil
}

func (f *fakeShellEnv) CreateTempFile(context.Context, string, string) (string, error) {
	if f.tempPath == "" {
		f.tempPath = "full.log"
	}
	return f.tempPath, nil
}

func (f *fakeShellEnv) AppendFile(ctx context.Context, path, text string) error {
	if f.files == nil {
		f.files = map[string]string{}
	}
	f.files[path] += text
	return nil
}

func TestExecuteShellWithCapture(t *testing.T) {
	env := &fakeShellEnv{outputs: []string{"ok\x00", strings.Repeat("x", DefaultMaxBytes+1)}}
	result, err := ExecuteShellWithCapture(context.Background(), env, "cmd", ShellCaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exit=%v", result.ExitCode)
	}
	if !result.Truncated || result.FullOutputPath == "" {
		t.Fatalf("capture=%#v", result)
	}
	if strings.Contains(result.Output, "\x00") {
		t.Fatalf("unsanitized output=%q", result.Output)
	}
}
