package harnessutils

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

type ShellExecOptions struct {
	OnStdout func(string)
	OnStderr func(string)
}

type ShellExecResult struct {
	ExitCode int
}

type ShellCaptureOptions struct {
	OnChunk func(string)
}

type ShellCaptureResult struct {
	Output         string `json:"output"`
	ExitCode       *int   `json:"exitCode,omitempty"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

type ShellExecutionEnv interface {
	Exec(context.Context, string, ShellExecOptions) (ShellExecResult, error)
	CreateTempFile(context.Context, string, string) (string, error)
	AppendFile(context.Context, string, string) error
}

type AbortedError struct {
	Message string
}

func (e AbortedError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "aborted"
}

// utf16Len returns the number of UTF-16 code units in s, matching JavaScript's
// String.length. TS shell-output.ts tracks the in-memory buffer size using
// text.length (UTF-16 code units), not byte length, so the tail-trimming budget
// must be measured the same way to stay byte-for-byte identical.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// SanitizeShellBinaryOutput strips control bytes and genuinely invalid UTF-8
// from shell output, mirroring TS sanitizeBinaryOutput. TS iterates over code
// points (Array.from + codePointAt), so a real U+FFFD that was encoded in the
// input (the valid bytes EF BF BD) is preserved; only truly invalid byte
// sequences -- which Go decodes as utf8.RuneError with a width of 1 -- are
// dropped.
func SanitizeShellBinaryOutput(str string) string {
	var builder strings.Builder
	for i := 0; i < len(str); {
		r, size := utf8.DecodeRuneInString(str[i:])
		i += size
		if r == utf8.RuneError && size == 1 {
			// Genuinely invalid byte: TS's codePointAt never yields this, so drop it.
			continue
		}
		if r == '\t' || r == '\n' || r == '\r' {
			builder.WriteRune(r)
			continue
		}
		if r <= 0x1f || (r >= 0xfff9 && r <= 0xfffb) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func ExecuteShellWithCapture(ctx context.Context, env ShellExecutionEnv, command string, opts ShellCaptureOptions) (ShellCaptureResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var outputChunks []string
	outputBytes := 0
	maxOutputBytes := DefaultMaxBytes * 2
	totalBytes := 0
	fullOutputPath := ""

	appendFullOutput := func(text string) error {
		if fullOutputPath == "" {
			return nil
		}
		return env.AppendFile(ctx, fullOutputPath, text)
	}
	ensureFullOutputFile := func(initialContent string) error {
		if fullOutputPath != "" {
			return nil
		}
		path, err := env.CreateTempFile(ctx, "bash-", ".log")
		if err != nil {
			return err
		}
		fullOutputPath = path
		return env.AppendFile(ctx, path, initialContent)
	}
	onChunk := func(chunk string) error {
		totalBytes += len([]byte(chunk))
		text := strings.ReplaceAll(SanitizeShellBinaryOutput(chunk), "\r", "")
		if totalBytes > DefaultMaxBytes && fullOutputPath == "" {
			if err := ensureFullOutputFile(strings.Join(outputChunks, "") + text); err != nil {
				return err
			}
		} else if err := appendFullOutput(text); err != nil {
			return err
		}
		outputChunks = append(outputChunks, text)
		// TS measures outputBytes with text.length (UTF-16 code units), not the
		// encoded byte length, so the tail buffer is trimmed identically.
		outputBytes += utf16Len(text)
		for outputBytes > maxOutputBytes && len(outputChunks) > 1 {
			removed := outputChunks[0]
			outputChunks = outputChunks[1:]
			outputBytes -= utf16Len(removed)
		}
		if opts.OnChunk != nil {
			opts.OnChunk(text)
		}
		return nil
	}
	var captureErr error
	result, err := env.Exec(ctx, command, ShellExecOptions{
		OnStdout: func(chunk string) {
			if captureErr == nil {
				captureErr = onChunk(chunk)
			}
		},
		OnStderr: func(chunk string) {
			if captureErr == nil {
				captureErr = onChunk(chunk)
			}
		},
	})
	if captureErr != nil {
		return ShellCaptureResult{}, captureErr
	}
	tailOutput := strings.Join(outputChunks, "")
	truncation := TruncateTail(tailOutput, TruncationOptions{})
	if truncation.Truncated && fullOutputPath == "" {
		if fileErr := ensureFullOutputFile(tailOutput); fileErr != nil {
			return ShellCaptureResult{}, fileErr
		}
	}
	output := tailOutput
	if truncation.Truncated {
		output = truncation.Content
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isAbortedError(err) || ctx.Err() != nil {
			return ShellCaptureResult{
				Output:         output,
				Cancelled:      true,
				Truncated:      truncation.Truncated,
				FullOutputPath: fullOutputPath,
			}, nil
		}
		return ShellCaptureResult{}, err
	}
	exitCode := result.ExitCode
	if ctx.Err() != nil {
		return ShellCaptureResult{Output: output, Cancelled: true, Truncated: truncation.Truncated, FullOutputPath: fullOutputPath}, nil
	}
	return ShellCaptureResult{
		Output:         output,
		ExitCode:       &exitCode,
		Cancelled:      false,
		Truncated:      truncation.Truncated,
		FullOutputPath: fullOutputPath,
	}, nil
}

func isAbortedError(err error) bool {
	var aborted AbortedError
	return errors.As(err, &aborted)
}
