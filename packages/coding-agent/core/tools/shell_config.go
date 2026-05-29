package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type ShellConfig struct {
	Shell string
	Args  []string
}

func ResolveShellConfig(customShellPath string) (ShellConfig, error) {
	customShellPath = strings.TrimSpace(customShellPath)
	if customShellPath != "" {
		if _, err := os.Stat(customShellPath); err != nil {
			return ShellConfig{}, fmt.Errorf("custom shell path not found: %s", customShellPath)
		}
		return ShellConfig{Shell: customShellPath, Args: []string{"-c"}}, nil
	}
	if runtime.GOOS == "windows" {
		paths := windowsGitBashPaths()
		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				return ShellConfig{Shell: path, Args: []string{"-c"}}, nil
			}
		}
		if path, ok := lookPathAny("bash.exe", "bash"); ok {
			return ShellConfig{Shell: path, Args: []string{"-c"}}, nil
		}
		return ShellConfig{}, fmt.Errorf("no bash shell found; install Git for Windows, add bash to PATH, or set shellPath")
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return ShellConfig{Shell: "/bin/bash", Args: []string{"-c"}}, nil
	}
	if path, ok := lookPathAny("bash"); ok {
		return ShellConfig{Shell: path, Args: []string{"-c"}}, nil
	}
	return ShellConfig{Shell: "sh", Args: []string{"-c"}}, nil
}

func ShellCommand(config ShellConfig, command string) *exec.Cmd {
	return exec.Command(config.Shell, shellCommandArgs(config, command)...)
}

func ShellCommandContext(ctx context.Context, config ShellConfig, command string) *exec.Cmd {
	return exec.CommandContext(ctx, config.Shell, shellCommandArgs(config, command)...)
}

func shellCommandArgs(config ShellConfig, command string) []string {
	args := append([]string(nil), config.Args...)
	return append(args, command)
}

func lookPathAny(names ...string) (string, bool) {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil && path != "" {
			return path, true
		}
	}
	return "", false
}

func windowsGitBashPaths() []string {
	var paths []string
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		paths = append(paths, filepath.Join(programFiles, "Git", "bin", "bash.exe"))
	}
	if programFilesX86 := os.Getenv("ProgramFiles(x86)"); programFilesX86 != "" {
		paths = append(paths, filepath.Join(programFilesX86, "Git", "bin", "bash.exe"))
	}
	return paths
}
