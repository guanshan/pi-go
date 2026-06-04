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
	cmd := exec.Command(config.Shell, shellCommandArgs(config, command)...)
	// Suppress the Windows console window for every shell command, mirroring TS's
	// windowsHide: true. Centralized here so bash.go and session_api.go inherit it.
	hideWindow(cmd)
	return cmd
}

// ShellEnv returns the process environment with the agent bin directory
// prepended to PATH, so commands migrated/installed there (fd, rg, package
// CLIs) resolve. It mirrors getShellEnv() in src/utils/shell.ts:112-124 and
// returns an os/exec-style ["KEY=value", ...] slice ready for cmd.Env. When
// binDir is empty (agent dir unknown) the unmodified environment is returned.
func ShellEnv(binDir string) []string {
	environ := os.Environ()
	if binDir == "" {
		return environ
	}
	// Find the existing PATH key, preserving its original casing (matters on
	// Windows where it may be "Path"), defaulting to "PATH".
	pathKey := "PATH"
	currentPath := ""
	for _, pair := range environ {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(key, "PATH") {
			pathKey = key
			currentPath = value
			break
		}
	}
	// If the bin dir is already on PATH, leave the environment unchanged.
	for _, entry := range filepath.SplitList(currentPath) {
		if entry == binDir {
			return environ
		}
	}
	updatedPath := binDir
	if currentPath != "" {
		updatedPath = binDir + string(os.PathListSeparator) + currentPath
	}
	out := make([]string, 0, len(environ)+1)
	replaced := false
	for _, pair := range environ {
		key, _, ok := strings.Cut(pair, "=")
		if ok && strings.EqualFold(key, "PATH") {
			out = append(out, pathKey+"="+updatedPath)
			replaced = true
			continue
		}
		out = append(out, pair)
	}
	if !replaced {
		out = append(out, pathKey+"="+updatedPath)
	}
	return out
}

func ShellCommandContext(ctx context.Context, config ShellConfig, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, config.Shell, shellCommandArgs(config, command)...)
	hideWindow(cmd)
	return cmd
}

// ConfigureTreeKill makes cmd run in its own process group (where the platform
// supports it) and kill the entire process tree when its context is canceled,
// so an abort/timeout or signal shutdown does not leave grandchildren running.
// This mirrors the wiring the bash tool uses; it is exported for the direct
// shell path in AgentSession.ExecuteBash, which lives in another package.
func ConfigureTreeKill(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	configureProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}
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
