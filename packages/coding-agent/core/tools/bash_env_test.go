//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBashToolPrependsBinDirToPath asserts the bash tool executes with the
// agent bin directory prepended to PATH, mirroring getShellEnv() in
// src/core/tools/bash.ts so migrated/installed tools (fd, rg, package CLIs)
// resolve. We do this end-to-end by echoing $PATH from inside the command.
func TestBashToolPrependsBinDirToPath(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "agent-bin")
	tool := BashTool{CWD: t.TempDir(), BinDir: binDir}

	result := tool.Execute(context.Background(), raw(map[string]any{
		"command": `printf '%s' "$PATH"`,
	}), nil)
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(result.Content))
	}

	gotPath := strings.TrimSpace(toolText(result.Content))
	entries := filepath.SplitList(gotPath)
	if len(entries) == 0 || entries[0] != binDir {
		t.Fatalf("bin dir not prepended to PATH: first entry=%q full=%q", firstOrEmpty(entries), gotPath)
	}
}

// TestBashToolResolvesCommandFromBinDir confirms a command living only in the
// agent bin directory is actually found at runtime, proving PATH injection is
// functional and not merely cosmetic.
func TestBashToolResolvesCommandFromBinDir(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(binDir, "pi-fake-tool")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho fake-tool-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	tool := BashTool{CWD: t.TempDir(), BinDir: binDir}
	result := tool.Execute(context.Background(), raw(map[string]any{
		"command": "pi-fake-tool",
	}), nil)
	if result.IsError {
		t.Fatalf("command in bin dir not found: %s", toolText(result.Content))
	}
	if got := strings.TrimSpace(toolText(result.Content)); got != "fake-tool-ran" {
		t.Fatalf("output=%q", got)
	}
}

// TestShellEnvPrependsBinDir covers the helper directly, including the
// already-on-PATH no-op and the empty-binDir passthrough.
func TestShellEnvPrependsBinDir(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	binDir := "/tmp/agent/bin"

	env := ShellEnv(binDir)
	pathVal, ok := envValue(env, "PATH")
	if !ok {
		t.Fatal("PATH missing from env")
	}
	entries := filepath.SplitList(pathVal)
	if len(entries) == 0 || entries[0] != binDir {
		t.Fatalf("bin dir not prepended: %q", pathVal)
	}
	if want := binDir + string(os.PathListSeparator) + "/usr/bin:/bin"; pathVal != want {
		t.Fatalf("PATH=%q want %q", pathVal, want)
	}

	// Empty binDir leaves the environment untouched.
	if passthrough := ShellEnv(""); !sameStringSlice(passthrough, os.Environ()) {
		t.Fatal("empty binDir should return os.Environ() unchanged")
	}
}

func TestShellEnvNoDuplicateWhenAlreadyOnPath(t *testing.T) {
	binDir := "/tmp/agent/bin"
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/usr/bin")

	env := ShellEnv(binDir)
	pathVal, _ := envValue(env, "PATH")
	if count := strings.Count(pathVal, binDir); count != 1 {
		t.Fatalf("bin dir should appear once, got %d in %q", count, pathVal)
	}
	if pathVal != binDir+string(os.PathListSeparator)+"/usr/bin" {
		t.Fatalf("PATH should be unchanged, got %q", pathVal)
	}
}

func firstOrEmpty(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[0]
}

func envValue(env []string, key string) (string, bool) {
	for _, pair := range env {
		k, v, ok := strings.Cut(pair, "=")
		if ok && k == key {
			return v, true
		}
	}
	return "", false
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
