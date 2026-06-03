package codingagent

import (
	"reflect"
	"testing"

	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

func pmWithNpmCommand(t *testing.T, npmCommand []string) *DefaultPackageManager {
	t.Helper()
	dir := t.TempDir()
	settings := core.NewSettingsManager(dir, dir)
	if npmCommand != nil {
		settings.Project.NPMCommand = npmCommand
	}
	return NewDefaultPackageManager(dir, dir, settings)
}

// TestNpmInstallArgsPerManager locks the manager-aware install arguments to the
// TS getNpmInstallArgs behavior (src/core/package-manager.ts ~1707).
func TestNpmInstallArgsPerManager(t *testing.T) {
	const spec = "left-pad@1.0.0"
	const root = "/install/root"
	cases := []struct {
		name       string
		npmCommand []string
		want       []string
	}{
		{
			name:       "default npm",
			npmCommand: nil,
			want:       []string{"install", spec, "--prefix", root, "--legacy-peer-deps"},
		},
		{
			name:       "bun uses --cwd and --omit=peer",
			npmCommand: []string{"bun"},
			want:       []string{"install", spec, "--cwd", root, "--omit=peer"},
		},
		{
			name:       "pnpm uses --prefix and peer config flags",
			npmCommand: []string{"pnpm"},
			want: []string{
				"install", spec, "--prefix", root,
				"--config.auto-install-peers=false",
				"--config.strict-peer-dependencies=false",
				"--config.strict-dep-builds=false",
			},
		},
		{
			name:       "mise-wrapped pnpm detected after -- separator",
			npmCommand: []string{"mise", "exec", "--", "pnpm"},
			want: []string{
				"install", spec, "--prefix", root,
				"--config.auto-install-peers=false",
				"--config.strict-peer-dependencies=false",
				"--config.strict-dep-builds=false",
			},
		},
		{
			name:       "mise-wrapped bun detected after -- separator",
			npmCommand: []string{"mise", "exec", "--", "bun"},
			want:       []string{"install", spec, "--cwd", root, "--omit=peer"},
		},
		{
			name:       "unknown custom manager falls back to npm flags",
			npmCommand: []string{"yarn"},
			want:       []string{"install", spec, "--prefix", root, "--legacy-peer-deps"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := pmWithNpmCommand(t, tc.npmCommand)
			got := mgr.npmInstallArgs(spec, root)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("npmInstallArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPackageManagerNameDetection covers the name-derivation rules (last "--"
// separator, basename, .cmd/.exe stripping).
func TestPackageManagerNameDetection(t *testing.T) {
	cases := []struct {
		npmCommand []string
		want       string
	}{
		{nil, "npm"},
		{[]string{"pnpm"}, "pnpm"},
		{[]string{"/usr/local/bin/bun"}, "bun"},
		{[]string{"mise", "exec", "--", "pnpm"}, "pnpm"},
		{[]string{"pnpm.cmd"}, "pnpm"},
		{[]string{"bun.EXE"}, "bun"},
	}
	for _, tc := range cases {
		mgr := pmWithNpmCommand(t, tc.npmCommand)
		if got := mgr.packageManagerName(); got != tc.want {
			t.Errorf("packageManagerName(%v) = %q, want %q", tc.npmCommand, got, tc.want)
		}
	}
}

// TestGitDependencyInstallArgs verifies the git dependency install args now
// match TS getGitDependencyInstallArgs exactly: default npm gets only
// "install --omit=dev" (no --ignore-scripts/--no-audit/--no-fund, so lifecycle
// scripts still run), and a custom command gets a bare "install".
func TestGitDependencyInstallArgs(t *testing.T) {
	defaultNpm := pmWithNpmCommand(t, nil)
	if got, want := defaultNpm.gitDependencyInstallArgs(), []string{"install", "--omit=dev"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default npm git install args = %q, want %q", got, want)
	}
	for _, flag := range []string{"--ignore-scripts", "--no-audit", "--no-fund"} {
		if contains(defaultNpm.gitDependencyInstallArgs(), flag) {
			t.Fatalf("git install args must not contain %s (diverges from TS)", flag)
		}
	}

	custom := pmWithNpmCommand(t, []string{"pnpm"})
	if got, want := custom.gitDependencyInstallArgs(), []string{"install"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("custom git install args = %q, want %q", got, want)
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
