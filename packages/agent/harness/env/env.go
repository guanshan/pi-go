package harnessenv

import (
	"context"
	"time"
)

type FileKind string

const (
	FileKindFile      FileKind = "file"
	FileKindDirectory FileKind = "directory"
	FileKindSymlink   FileKind = "symlink"
)

type FileInfo struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Kind    FileKind `json:"kind"`
	Size    int64    `json:"size"`
	MtimeMS int64    `json:"mtimeMs"`
}

type FileErrorCode string

const (
	FileErrAborted          FileErrorCode = "aborted"
	FileErrNotFound         FileErrorCode = "not_found"
	FileErrPermissionDenied FileErrorCode = "permission_denied"
	FileErrNotDirectory     FileErrorCode = "not_directory"
	FileErrIsDirectory      FileErrorCode = "is_directory"
	FileErrInvalid          FileErrorCode = "invalid"
	FileErrNotSupported     FileErrorCode = "not_supported"
	FileErrUnknown          FileErrorCode = "unknown"
)

type FileError struct {
	Code FileErrorCode
	Path string
	Msg  string
	Err  error
}

func (e *FileError) Error() string {
	if e == nil {
		return ""
	}
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *FileError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type ExecutionErrorCode string

const (
	ExecErrAborted          ExecutionErrorCode = "aborted"
	ExecErrTimeout          ExecutionErrorCode = "timeout"
	ExecErrShellUnavailable ExecutionErrorCode = "shell_unavailable"
	ExecErrSpawn            ExecutionErrorCode = "spawn_error"
	ExecErrCallback         ExecutionErrorCode = "callback_error"
	ExecErrUnknown          ExecutionErrorCode = "unknown"
)

type ExecutionError struct {
	Code ExecutionErrorCode
	Msg  string
	Err  error
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return ""
	}
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

type ExecOptions struct {
	Cwd      string
	Env      map[string]string
	Timeout  time.Duration
	OnStdout func(chunk string)
	OnStderr func(chunk string)
}

type FileSystem interface {
	Cwd() string
	AbsolutePath(context.Context, string) (string, error)
	JoinPath(context.Context, []string) (string, error)
	ReadTextFile(context.Context, string) (string, error)
	ReadTextLines(context.Context, string, int) ([]string, error)
	ReadBinaryFile(context.Context, string) ([]byte, error)
	WriteFile(context.Context, string, []byte) error
	AppendFile(context.Context, string, []byte) error
	FileInfo(context.Context, string) (FileInfo, error)
	ListDir(context.Context, string) ([]FileInfo, error)
	CanonicalPath(context.Context, string) (string, error)
	Exists(context.Context, string) (bool, error)
	CreateDir(context.Context, string, bool) error
	Remove(context.Context, string, bool, bool) error
	CreateTempDir(context.Context, string) (string, error)
	CreateTempFile(context.Context, string, string) (string, error)
	Cleanup() error
}

type Shell interface {
	Exec(context.Context, string, ExecOptions) (ExecResult, error)
	Cleanup() error
}

type ExecutionEnv interface {
	FileSystem
	Shell
}
