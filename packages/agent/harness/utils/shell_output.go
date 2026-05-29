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

func SanitizeShellBinaryOutput(str string) string {
	var builder strings.Builder
	for _, r := range str {
		if r == utf8.RuneError {
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
		outputBytes += len(text)
		for outputBytes > maxOutputBytes && len(outputChunks) > 1 {
			removed := outputChunks[0]
			outputChunks = outputChunks[1:]
			outputBytes -= len(removed)
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
