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
	// tempMu guards tempDirs because a single env can be shared across the
	// agent loop's parallel tool executions (see packages/agent/tool_exec.go).
	tempMu   sync.Mutex
	tempDirs []string
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
	// TS NodejsExecutionEnv's constructor only stores the cwd; it never stats or
	// validates it. Existence/kind validation happens at use-time (read, exec,
	// etc.), so constructing an env for a not-yet-created directory must succeed
	// to match the TS port.
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
	e.tempMu.Lock()
	e.tempDirs = append(e.tempDirs, dir)
	e.tempMu.Unlock()
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

// callbackErrorSink records the first callback panic and aborts the running
// command, mirroring the callback_error path in src/harness/env/nodejs.ts.
type callbackErrorSink struct {
	mu     sync.Mutex
	err    *ExecutionError
	cancel context.CancelFunc
}

func (s *callbackErrorSink) capture(err *ExecutionError) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *callbackErrorSink) get() *ExecutionError {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

type executionStreamWriter struct {
	mu       sync.Mutex
	buffer   *bytes.Buffer
	callback func(string)
	sink     *callbackErrorSink
	// pending holds the bytes of a trailing rune that was split across Write
	// calls. TS reads stdout/stderr with setEncoding("utf8"), whose internal
	// StringDecoder buffers an incomplete multibyte sequence until the rest of
	// the rune arrives, so a callback never observes a U+FFFD from a chunk
	// boundary. We mirror that by deferring undecodable trailing bytes.
	pending []byte
}

func (w *executionStreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := len(p)
	w.mu.Lock()
	if w.buffer != nil {
		_, _ = w.buffer.Write(p)
	}
	combined := p
	if len(w.pending) > 0 {
		joined := make([]byte, 0, len(w.pending)+len(p))
		joined = append(joined, w.pending...)
		joined = append(joined, p...)
		combined = joined
		w.pending = nil
	}
	emit, pending := splitTrailingPartialRune(combined)
	if len(pending) > 0 {
		w.pending = append([]byte(nil), pending...)
	}
	w.mu.Unlock()
	if w.callback != nil && len(emit) > 0 {
		w.invokeCallback(string(emit))
	}
	return n, nil
}

// flush emits any buffered trailing bytes once the stream is closed. Bytes that
// never completed into a valid rune are emitted as-is so no output is lost; the
// downstream sanitizer (SanitizeShellBinaryOutput) drops genuinely invalid
// bytes, matching how TS surfaces the final decoder state.
func (w *executionStreamWriter) flush() {
	w.mu.Lock()
	pending := w.pending
	w.pending = nil
	w.mu.Unlock()
	if w.callback != nil && len(pending) > 0 {
		w.invokeCallback(string(pending))
	}
}

// splitTrailingPartialRune returns the prefix of p that ends on a UTF-8 rune
// boundary plus the trailing bytes (if any) that form the start of an
// incomplete multibyte rune. A trailing byte sequence is only withheld when it
// is a valid prefix of a longer rune; genuinely invalid bytes are emitted
// immediately so they are not buffered indefinitely.
func splitTrailingPartialRune(p []byte) (emit, pending []byte) {
	if len(p) == 0 {
		return nil, nil
	}
	// Scan back over continuation bytes (10xxxxxx) to find the last lead byte.
	i := len(p) - 1
	for i >= 0 && p[i]&0xC0 == 0x80 {
		i--
	}
	if i < 0 {
		// All continuation bytes with no lead: not a valid rune start, emit all.
		return p, nil
	}
	lead := p[i]
	var runeLen int
	switch {
	case lead&0x80 == 0x00:
		runeLen = 1
	case lead&0xE0 == 0xC0:
		runeLen = 2
	case lead&0xF0 == 0xE0:
		runeLen = 3
	case lead&0xF8 == 0xF0:
		runeLen = 4
	default:
		// Invalid lead byte; nothing to defer.
		return p, nil
	}
	have := len(p) - i
	if have < runeLen {
		// The final rune is incomplete: defer it until the rest arrives.
		return p[:i], p[i:]
	}
	return p, nil
}

// invokeCallback runs the user callback, converting any panic into a stored
// callback_error and aborting the child process tree.
func (w *executionStreamWriter) invokeCallback(text string) {
	defer func() {
		if r := recover(); r != nil && w.sink != nil {
			w.sink.capture(&ExecutionError{Code: ExecErrCallback, Err: panicError(r)})
		}
	}()
	w.callback(text)
}

func panicError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
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
	// A cancellable context lets a callback panic abort the child process tree,
	// in addition to the optional timeout.
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if opts.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		execCtx, timeoutCancel = context.WithTimeout(execCtx, opts.Timeout)
		defer timeoutCancel()
	}
	cmd := exec.CommandContext(execCtx, shell, append(args, command)...)
	cmd.Dir = cwd
	cmd.Env = e.shellEnvironment(opts.Env)
	configureProcessGroup(cmd)
	// Suppress the console window Windows would otherwise flash for each spawned
	// shell child (no-op on non-Windows), mirroring TS's windowsHide: true.
	hideWindow(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessTree(cmd.Process.Pid)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	sink := &callbackErrorSink{cancel: cancel}
	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutWriter := &executionStreamWriter{buffer: &stdoutBuf, callback: opts.OnStdout, sink: sink}
	stderrWriter := &executionStreamWriter{buffer: &stderrBuf, callback: opts.OnStderr, sink: sink}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	if err := cmd.Start(); err != nil {
		return ExecResult{}, &ExecutionError{Code: ExecErrSpawn, Err: err}
	}
	waitErr := cmd.Wait()
	// Emit any trailing partial-rune bytes that were withheld at a chunk
	// boundary so no output is dropped (mirrors the StringDecoder flush TS gets
	// from setEncoding("utf8")).
	stdoutWriter.flush()
	stderrWriter.flush()
	// A callback panic takes precedence over timeout/abort, matching the TS
	// close handler order in nodejs.ts.
	if cbErr := sink.get(); cbErr != nil {
		killProcessTree(cmd.Process.Pid)
		return ExecResult{}, cbErr
	}
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
	e.tempMu.Lock()
	dirs := e.tempDirs
	e.tempDirs = nil
	e.tempMu.Unlock()
	var first error
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil && first == nil {
			first = err
		}
	}
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
		// Only build a candidate when the program-files env var is non-empty.
		// filepath.Join("", "Git", "bin", "bash.exe") yields the RELATIVE path
		// "Git/bin/bash.exe", so the old `candidate != ""` guard never tripped and
		// os.Stat would resolve it against the process CWD. Mirror TS's existence
		// guard (and shell_config.go:windowsGitBashPaths) by skipping the var
		// entirely when unset, keeping only absolute candidates.
		var candidates []string
		for _, envVar := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
			if base := os.Getenv(envVar); base != "" {
				candidates = append(candidates, filepath.Join(base, "Git", "bin", "bash.exe"))
			}
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, []string{"-c"}, nil
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
