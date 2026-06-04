//go:build windows

package harnessenv

import (
	"path/filepath"
	"testing"
)

// TestShellConfigSkipsRelativeBashWhenProgramFilesUnset verifies the R*-P2
// finding: when ProgramFiles / ProgramFiles(x86) are empty, shellConfig must NOT
// construct (or os.Stat) the RELATIVE candidate "Git/bin/bash.exe" — that path
// would otherwise resolve against the process CWD and could match an unrelated
// file. With both vars empty, resolution must fall through to exec.LookPath /
// the "no bash shell found" error and never return a non-absolute bash path.
func TestShellConfigSkipsRelativeBashWhenProgramFilesUnset(t *testing.T) {
	t.Setenv("ProgramFiles", "")
	t.Setenv("ProgramFiles(x86)", "")

	env, err := NewLocalExecutionEnv(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}

	shell, _, err := env.shellConfig()
	if err != nil {
		// "no bash shell found" is an acceptable outcome on a host without bash.
		return
	}
	if shell == "" {
		return
	}
	if !filepath.IsAbs(shell) {
		t.Fatalf("shellConfig returned a relative shell path with ProgramFiles unset: %q", shell)
	}
}
