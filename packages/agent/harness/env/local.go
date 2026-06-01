package harnessenv

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type LocalExecutionEnv struct {
	cwd       string
	shellPath string
	shellEnv  map[string]string
	tempDirs  []string
}

func NewLocalExecutionEnv(cwd string, shellPath string, shellEnv map[string]string) (*LocalExecutionEnv, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, &FileError{Code: FileErrUnknown, Err: err}
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, &FileError{Code: FileErrInvalid, Path: cwd, Err: err}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, toFileError(err, abs)
	}
	if !info.IsDir() {
		return nil, &FileError{Code: FileErrNotDirectory, Path: abs, Err: fmt.Errorf("%s is not a directory", abs)}
	}
	return &LocalExecutionEnv{cwd: abs, shellPath: shellPath, shellEnv: cloneStringMap(shellEnv)}, nil
}

func (e *LocalExecutionEnv) Cwd() string {
	return e.cwd
}

func (e *LocalExecutionEnv) AbsolutePath(ctx context.Context, path string) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	return e.resolve(path), nil
}

func (e *LocalExecutionEnv) JoinPath(ctx context.Context, parts []string) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	return filepath.Join(parts...), nil
}

func (e *LocalExecutionEnv) ReadTextFile(ctx context.Context, path string) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	resolved := e.resolve(path)
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return "", toFileError(err, resolved)
	}
	return string(raw), nil
}

func (e *LocalExecutionEnv) ReadTextLines(ctx context.Context, path string, maxLines int) ([]string, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	resolved := e.resolve(path)
	file, err := os.Open(resolved)
	if err != nil {
		return nil, toFileError(err, resolved)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if maxLines > 0 && len(lines) >= maxLines {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, toFileError(err, resolved)
	}
	return lines, nil
}

func (e *LocalExecutionEnv) ReadBinaryFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	resolved := e.resolve(path)
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return nil, toFileError(err, resolved)
	}
	return raw, nil
}

func (e *LocalExecutionEnv) WriteFile(ctx context.Context, path string, content []byte) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	resolved := e.resolve(path)
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return toFileError(err, resolved)
	}
	if err := os.WriteFile(resolved, content, 0o600); err != nil {
		return toFileError(err, resolved)
	}
	return nil
}

func (e *LocalExecutionEnv) AppendFile(ctx context.Context, path string, content []byte) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	resolved := e.resolve(path)
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return toFileError(err, resolved)
	}
	file, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return toFileError(err, resolved)
	}
	defer file.Close()
	if _, err := file.Write(content); err != nil {
		return toFileError(err, resolved)
	}
	return nil
}

func (e *LocalExecutionEnv) FileInfo(ctx context.Context, path string) (FileInfo, error) {
	if err := ctxErr(ctx); err != nil {
		return FileInfo{}, err
	}
	resolved := e.resolve(path)
	info, err := os.Lstat(resolved)
	if err != nil {
		return FileInfo{}, toFileError(err, resolved)
	}
	return fileInfoFromOS(resolved, info)
}

func (e *LocalExecutionEnv) ListDir(ctx context.Context, path string) ([]FileInfo, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	resolved := e.resolve(path)
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, toFileError(err, resolved)
	}
	infos := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		entryPath := filepath.Join(resolved, entry.Name())
		info, err := os.Lstat(entryPath)
		if err != nil {
			return nil, toFileError(err, entryPath)
		}
		fileInfo, err := fileInfoFromOS(entryPath, info)
		if err != nil {
			return nil, err
		}
		infos = append(infos, fileInfo)
	}
	return infos, nil
}

func (e *LocalExecutionEnv) CanonicalPath(ctx context.Context, path string) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	resolved := e.resolve(path)
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", toFileError(err, resolved)
	}
	return canonical, nil
}

func (e *LocalExecutionEnv) Exists(ctx context.Context, path string) (bool, error) {
	_, err := e.FileInfo(ctx, path)
	if err == nil {
		return true, nil
	}
	var fileErr *FileError
	if errors.As(err, &fileErr) && fileErr.Code == FileErrNotFound {
		return false, nil
	}
	return false, err
}

func (e *LocalExecutionEnv) CreateDir(ctx context.Context, path string, recursive bool) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	resolved := e.resolve(path)
	var err error
	if recursive {
		err = os.MkdirAll(resolved, 0o755)
	} else {
		err = os.Mkdir(resolved, 0o755)
	}
	if err != nil {
		return toFileError(err, resolved)
	}
	return nil
}

func (e *LocalExecutionEnv) Remove(ctx context.Context, path string, recursive bool, force bool) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	resolved := e.resolve(path)
	var err error
	if recursive {
		err = os.RemoveAll(resolved)
	} else {
		err = os.Remove(resolved)
	}
	if err != nil {
		if force && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return toFileError(err, resolved)
	}
	return nil
}

func (e *LocalExecutionEnv) CreateTempDir(ctx context.Context, prefix string) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	if prefix == "" {
		prefix = "tmp-"
	}
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", toFileError(err, "")
	}
	e.tempDirs = append(e.tempDirs, dir)
	return dir, nil
}

func (e *LocalExecutionEnv) CreateTempFile(ctx context.Context, prefix string, suffix string) (string, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	dir, err := e.CreateTempDir(ctx, "tmp-")
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, prefix+"*"+suffix)
	if err != nil {
		return "", toFileError(err, dir)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", toFileError(err, path)
	}
	return path, nil
}

type executionStreamWriter struct {
	mu       sync.Mutex
	buffer   *bytes.Buffer
	callback func(string)
}

func (w *executionStreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	text := string(p)
	w.mu.Lock()
	if w.buffer != nil {
		_, _ = w.buffer.Write(p)
	}
	w.mu.Unlock()
	if w.callback != nil {
		w.callback(text)
	}
	return len(p), nil
}

func (e *LocalExecutionEnv) Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return ExecResult{}, &ExecutionError{Code: ExecErrAborted, Err: ctx.Err()}
	}
	shell, args, err := e.shellConfig()
	if err != nil {
		return ExecResult{}, err
	}
	cwd := e.cwd
	if opts.Cwd != "" {
		cwd = e.resolve(opts.Cwd)
	}
	execCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(execCtx, shell, append(args, command)...)
	cmd.Dir = cwd
	cmd.Env = e.shellEnvironment(opts.Env)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessTree(cmd.Process.Pid)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &executionStreamWriter{buffer: &stdoutBuf, callback: opts.OnStdout}
	cmd.Stderr = &executionStreamWriter{buffer: &stderrBuf, callback: opts.OnStderr}
	if err := cmd.Start(); err != nil {
		return ExecResult{}, &ExecutionError{Code: ExecErrSpawn, Err: err}
	}
	waitErr := cmd.Wait()
	if execCtx.Err() != nil {
		killProcessTree(cmd.Process.Pid)
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return ExecResult{}, &ExecutionError{Code: ExecErrTimeout, Err: execCtx.Err()}
		}
		return ExecResult{}, &ExecutionError{Code: ExecErrAborted, Err: execCtx.Err()}
	}
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return ExecResult{}, &ExecutionError{Code: ExecErrSpawn, Err: waitErr}
		}
	}
	return ExecResult{Stdout: stdoutBuf.String(), Stderr: stderrBuf.String(), ExitCode: exitCode}, nil
}

func (e *LocalExecutionEnv) Cleanup() error {
	var first error
	for _, dir := range e.tempDirs {
		if err := os.RemoveAll(dir); err != nil && first == nil {
			first = err
		}
	}
	e.tempDirs = nil
	if first != nil {
		return toFileError(first, "")
	}
	return nil
}

func (e *LocalExecutionEnv) resolve(path string) string {
	if path == "" {
		return filepath.Clean(e.cwd)
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(e.cwd, path))
}

func (e *LocalExecutionEnv) shellConfig() (string, []string, error) {
	if e.shellPath != "" {
		if _, err := os.Stat(e.shellPath); err == nil {
			return e.shellPath, []string{"-c"}, nil
		}
		return "", nil, &ExecutionError{Code: ExecErrShellUnavailable, Err: fmt.Errorf("custom shell path not found: %s", e.shellPath)}
	}
	if runtime.GOOS == "windows" {
		for _, candidate := range []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Git", "bin", "bash.exe"),
		} {
			if candidate != "" {
				if _, err := os.Stat(candidate); err == nil {
					return candidate, []string{"-c"}, nil
				}
			}
		}
		if bash, err := exec.LookPath("bash.exe"); err == nil {
			return bash, []string{"-c"}, nil
		}
		return "", nil, &ExecutionError{Code: ExecErrShellUnavailable, Err: errors.New("no bash shell found")}
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash", []string{"-c"}, nil
	}
	if bash, err := exec.LookPath("bash"); err == nil {
		return bash, []string{"-c"}, nil
	}
	return "sh", []string{"-c"}, nil
}

func (e *LocalExecutionEnv) shellEnvironment(extra map[string]string) []string {
	env := map[string]string{}
	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			env[key] = value
		}
	}
	for key, value := range e.shellEnv {
		env[key] = value
	}
	for key, value := range extra {
		env[key] = value
	}
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func fileInfoFromOS(path string, info os.FileInfo) (FileInfo, error) {
	kind := FileKind("")
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		kind = FileKindSymlink
	case info.IsDir():
		kind = FileKindDirectory
	case info.Mode().IsRegular():
		kind = FileKindFile
	default:
		return FileInfo{}, &FileError{Code: FileErrInvalid, Path: path, Err: errors.New("unsupported file type")}
	}
	return FileInfo{Name: filepath.Base(path), Path: path, Kind: kind, Size: info.Size(), MtimeMS: info.ModTime().UnixMilli()}, nil
}

func ctxErr(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return &FileError{Code: FileErrAborted, Err: ctx.Err()}
	}
	return nil
}

func toFileError(err error, path string) error {
	if err == nil {
		return nil
	}
	code := FileErrUnknown
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = FileErrAborted
	case errors.Is(err, os.ErrNotExist):
		code = FileErrNotFound
	case errors.Is(err, os.ErrPermission):
		code = FileErrPermissionDenied
	default:
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			switch pathErr.Err {
			case syscall.ENOTDIR:
				code = FileErrNotDirectory
			case syscall.EISDIR:
				code = FileErrIsDirectory
			case syscall.EINVAL:
				code = FileErrInvalid
			}
		}
	}
	return &FileError{Code: code, Path: path, Err: err}
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

var _ ExecutionEnv = (*LocalExecutionEnv)(nil)
