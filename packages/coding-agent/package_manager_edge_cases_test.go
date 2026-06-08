package codingagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

// TestEnsureNpmProjectSeedsGitIgnoreAndPackageJSON proves ensureNpmProject
// mirrors package-manager.ts: it creates the install root, writes a
// ".gitignore" of "*\n!.gitignore\n", and seeds package.json with
// {"name":"pi-extensions","private":true} at 2-space indent.
func TestEnsureNpmProjectSeedsGitIgnoreAndPackageJSON(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	root := filepath.Join(agentDir, "npm")

	if err := manager.ensureNpmProject(root); err != nil {
		t.Fatalf("ensureNpmProject: %v", err)
	}

	gitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(gitignore) != "*\n!.gitignore\n" {
		t.Fatalf("gitignore=%q want %q", string(gitignore), "*\n!.gitignore\n")
	}

	pkg, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatalf("read package.json: %v", err)
	}
	want := "{\n  \"name\": \"pi-extensions\",\n  \"private\": true\n}"
	if string(pkg) != want {
		t.Fatalf("package.json=%q want %q", string(pkg), want)
	}
}

// TestEnsureNpmProjectIsIdempotentAndPreservesExisting proves a second call does
// not clobber an existing package.json or .gitignore, mirroring the
// existsSync guards in ensureNpmProject/ensureGitIgnore.
func TestEnsureNpmProjectIsIdempotentAndPreservesExisting(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	root := filepath.Join(agentDir, "npm")

	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"custom","private":false}`)
	writeTestFile(t, filepath.Join(root, ".gitignore"), "custom-ignore\n")

	if err := manager.ensureNpmProject(root); err != nil {
		t.Fatalf("ensureNpmProject: %v", err)
	}

	pkg, _ := os.ReadFile(filepath.Join(root, "package.json"))
	if string(pkg) != `{"name":"custom","private":false}` {
		t.Fatalf("existing package.json was overwritten: %q", string(pkg))
	}
	ignore, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if string(ignore) != "custom-ignore\n" {
		t.Fatalf("existing .gitignore was overwritten: %q", string(ignore))
	}
}

// TestNpmInstallSeedsNpmProjectFiles proves the npm Install branch routes
// through ensureNpmProject, so the .gitignore and package.json land in the npm
// root, mirroring installNpm -> ensureNpmProject in package-manager.ts.
func TestNpmInstallSeedsNpmProjectFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake npm is POSIX-only")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	installFakeNPM(t, `#!/bin/sh
set -eu
if [ "$1" = "install" ]; then
	prefix=""
	while [ "$#" -gt 0 ]; do
		if [ "$1" = "--prefix" ]; then
			prefix="$2"
		fi
		shift
	done
	mkdir -p "$prefix/node_modules/fixture"
	printf installed > "$prefix/node_modules/fixture/.installed"
	exit 0
fi
exit 2
`)

	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	if _, err := manager.Install("npm:fixture@1.0.0", false, nil); err != nil {
		t.Fatal(err)
	}
	npmRoot := filepath.Join(agentDir, "npm")
	if got, err := os.ReadFile(filepath.Join(npmRoot, ".gitignore")); err != nil || string(got) != "*\n!.gitignore\n" {
		t.Fatalf("npm root .gitignore=%q err=%v", string(got), err)
	}
	if !fileExistsLocal(filepath.Join(npmRoot, "package.json")) {
		t.Fatalf("npm root package.json missing")
	}
}

// TestNpmInstallPinnedVersionCacheHit proves a present npm package whose
// installed version matches the pinned ref is a cache hit: npm is never
// invoked. Mirrors resolvePackageSources' needsInstall gate
// (parsed.pinned && installedNpmMatchesPinnedVersion) in package-manager.ts.
func TestNpmInstallPinnedVersionCacheHit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake npm is POSIX-only")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	argLog := filepath.Join(agentDir, "npm-args.log")
	installFakeNPM(t, `#!/bin/sh
echo "$@" >> "`+argLog+`"
exit 0
`)
	// Pre-seed an installed package whose version equals the pinned ref.
	installed := filepath.Join(agentDir, "npm", "node_modules", "fixture")
	writeTestFile(t, filepath.Join(installed, "package.json"), `{"name":"fixture","version":"1.0.0"}`)

	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	record, err := manager.Install("npm:fixture@1.0.0", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if record.Path != installed {
		t.Fatalf("install path=%q want %q", record.Path, installed)
	}
	if fileExistsLocal(argLog) {
		logged, _ := os.ReadFile(argLog)
		t.Fatalf("npm should not run on a pinned-version cache hit, got %q", string(logged))
	}
	npmRoot := filepath.Join(agentDir, "npm")
	if got, err := os.ReadFile(filepath.Join(npmRoot, ".gitignore")); err != nil || string(got) != "*\n!.gitignore\n" {
		t.Fatalf("npm root .gitignore=%q err=%v", string(got), err)
	}
	if !fileExistsLocal(filepath.Join(npmRoot, "package.json")) {
		t.Fatalf("npm root package.json missing on pinned-version cache hit")
	}
}

// TestNpmInstallPinnedVersionMismatchReinstalls proves a present package whose
// version differs from the pinned ref is NOT a cache hit: npm reinstalls.
func TestNpmInstallPinnedVersionMismatchReinstalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake npm is POSIX-only")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	argLog := filepath.Join(agentDir, "npm-args.log")
	installFakeNPM(t, `#!/bin/sh
set -eu
echo "$@" >> "`+argLog+`"
exit 0
`)
	installed := filepath.Join(agentDir, "npm", "node_modules", "fixture")
	writeTestFile(t, filepath.Join(installed, "package.json"), `{"name":"fixture","version":"0.9.0"}`)

	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	if _, err := manager.Install("npm:fixture@1.0.0", false, nil); err != nil {
		t.Fatal(err)
	}
	logged, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("npm should reinstall on version mismatch: %v", err)
	}
	if !strings.Contains(string(logged), "install fixture@1.0.0") {
		t.Fatalf("expected reinstall of fixture@1.0.0, got %q", string(logged))
	}
}

// TestInstalledNpmMatchesPinnedVersion is a table for the version-gate helper.
func TestInstalledNpmMatchesPinnedVersion(t *testing.T) {
	cases := []struct {
		name        string
		packageJSON string // empty means no package.json
		ref         string
		want        bool
	}{
		{name: "missing package.json", packageJSON: "", ref: "1.0.0", want: false},
		{name: "no version field", packageJSON: `{"name":"x"}`, ref: "1.0.0", want: false},
		{name: "empty ref matches present install", packageJSON: `{"version":"1.2.3"}`, ref: "", want: true},
		{name: "concrete pin equal", packageJSON: `{"version":"1.2.3"}`, ref: "1.2.3", want: true},
		{name: "concrete pin different", packageJSON: `{"version":"1.2.3"}`, ref: "1.0.0", want: false},
		{name: "invalid json", packageJSON: `{not json`, ref: "1.0.0", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.packageJSON != "" {
				writeTestFile(t, filepath.Join(dir, "package.json"), tc.packageJSON)
			}
			if got := installedNpmMatchesPinnedVersion(dir, tc.ref); got != tc.want {
				t.Fatalf("installedNpmMatchesPinnedVersion(%q)=%v want %v", tc.ref, got, tc.want)
			}
		})
	}
}

// TestOfflineModeEnabledParsing locks the PI_OFFLINE parsing table, mirroring
// isOfflineModeEnabled in package-manager.ts.
func TestOfflineModeEnabledParsing(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "1", want: true},
		{value: "true", want: true},
		{value: "TRUE", want: true},
		{value: "True", want: true},
		{value: "yes", want: true},
		{value: "YES", want: true},
		{value: " yes ", want: true},
		{value: "0", want: false},
		{value: "false", want: false},
		{value: "no", want: false},
		{value: "off", want: false},
	}
	for _, tc := range cases {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv(core.EnvOffline, tc.value)
			if got := offlineModeEnabled(); got != tc.want {
				t.Fatalf("offlineModeEnabled() with %q=%v want %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestResolveSkipsMissingSourceInstallWhenOffline proves the resolve
// missing-source-install path is skipped under PI_OFFLINE rather than invoking
// Install, mirroring resolvePackageSources' installMissing() offline guard.
func TestResolveSkipsMissingSourceInstallWhenOffline(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := core.NewSettingsManager(cwd, agentDir)
	settings.Global.Packages = []core.PackageSetting{{Source: "npm:missing-pkg"}}
	manager := NewDefaultPackageManager(cwd, agentDir, settings)

	t.Setenv(core.EnvOffline, "1")
	called := false
	resolved, err := manager.Resolve(func(string) (MissingSourceAction, error) {
		called = true
		return MissingSourceInstall, nil
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !called {
		t.Fatal("missing-source handler should still be consulted")
	}
	// Nothing was installed, so no package-origin resources surface (the user's
	// ambient auto-discovered top-level skills, if any, are unrelated).
	for _, group := range [][]ResolvedResource{resolved.Extensions, resolved.Skills, resolved.Prompts, resolved.Themes} {
		for _, resource := range group {
			if resource.Metadata.Origin == "package" {
				t.Fatalf("offline resolve installed a missing package resource: %#v", resource)
			}
		}
	}
}

// TestGitInstallSeedsGitRootGitIgnore proves the git Install branch seeds the
// git root with a ".gitignore" of "*\n!.gitignore\n", mirroring installGit ->
// ensureGitIgnore(gitRoot) in package-manager.ts.
func TestGitInstallSeedsGitRootGitIgnore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fakes for git/npm")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	bin := t.TempDir()
	gitScript := `#!/bin/sh
set -eu
if [ "$1" = "clone" ]; then
	for target in "$@"; do :; done
	mkdir -p "$target"
	printf 'extension' > "$target/index.ts"
	exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	if _, err := manager.Install("git:github:acme/tool", false, nil); err != nil {
		t.Fatal(err)
	}
	// gitRoot is <dest>'s parent: <agentDir>/git/github.com/acme.
	gitRoot := filepath.Join(agentDir, "git", "github.com", "acme")
	got, err := os.ReadFile(filepath.Join(gitRoot, ".gitignore"))
	if err != nil {
		t.Fatalf("git root .gitignore missing: %v", err)
	}
	if string(got) != "*\n!.gitignore\n" {
		t.Fatalf("git root .gitignore=%q want %q", string(got), "*\n!.gitignore\n")
	}
}

// TestGitInstallPropagatesGitIgnoreError proves git Install fails before cloning
// when the managed git root cannot be seeded, matching package-manager.ts where
// writeFileSync errors from ensureGitIgnore propagate.
func TestGitInstallPropagatesGitIgnoreError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX symlinks and shell fake git")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	gitRoot := filepath.Join(agentDir, "git", "github.com", "acme")
	if err := os.MkdirAll(gitRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(agentDir, "missing", "gitignore"), filepath.Join(gitRoot, ".gitignore")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	bin := t.TempDir()
	argLog := filepath.Join(agentDir, "git-args.log")
	gitScript := `#!/bin/sh
set -eu
echo "$@" >> "` + argLog + `"
if [ "$1" = "clone" ]; then
	for target in "$@"; do :; done
	mkdir -p "$target"
	printf 'extension' > "$target/index.ts"
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	if _, err := manager.Install("git:github:acme/tool", false, nil); err == nil {
		t.Fatal("expected git install to fail when .gitignore cannot be written")
	} else if !strings.Contains(err.Error(), ".gitignore") {
		t.Fatalf("install error=%v, want .gitignore write failure", err)
	}
	if fileExistsLocal(argLog) {
		logged, _ := os.ReadFile(argLog)
		t.Fatalf("git should not run after .gitignore seed failure, got %q", string(logged))
	}
}

// TestUpdateGitRefFetchesAndResets proves Update's git branch, when the record
// carries a ref, fetches that ref and hard-resets the checkout to it (advancing
// HEAD to the upstream commit), mirroring updateGit -> ensureGitRef in
// package-manager.ts. It uses real git against a local upstream repo.
func TestUpdateGitRefFetchesAndResets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on a POSIX git environment")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()

	// Build a bare-ish upstream repo with two commits on a branch named "main".
	upstream := t.TempDir()
	gitRun(t, upstream, "init", "-q", "-b", "main")
	gitRun(t, upstream, "config", "user.email", "t@example.com")
	gitRun(t, upstream, "config", "user.name", "tester")
	writeTestFile(t, filepath.Join(upstream, "index.ts"), "v1")
	gitRun(t, upstream, "add", ".")
	gitRun(t, upstream, "commit", "-q", "-m", "v1")

	// Clone into the managed git install path for the source git:<host>/<owner>/<repo>.
	source := "git:https://github.com/acme/tool.git#main"
	gitSource, ok := ParseGitURL(source)
	if !ok {
		t.Fatalf("ParseGitURL(%q) failed", source)
	}
	dest := filepath.Join(agentDir, "git", gitSource.Host, filepath.FromSlash(gitSource.Path))
	gitRun(t, "", "clone", "-q", upstream, dest)
	gitRun(t, dest, "config", "user.email", "t@example.com")
	gitRun(t, dest, "config", "user.name", "tester")

	// Advance upstream with a second commit the clone has not seen.
	writeTestFile(t, filepath.Join(upstream, "index.ts"), "v2")
	gitRun(t, upstream, "commit", "-q", "-am", "v2")
	wantHead := strings.TrimSpace(gitOut(t, upstream, "rev-parse", "HEAD"))

	settings := core.NewSettingsManager(cwd, agentDir)
	// A source carrying an explicit ref parses as pinned, which Update's pinned
	// guard would skip. Record it as an installed (unpinned) package so the
	// ref-bearing git-update path (fetch + reset to FETCH_HEAD) runs.
	settings.Global.InstalledPackages = []core.PackageRecord{{Source: source, Path: dest, Local: false, Pinned: false}}
	manager := NewDefaultPackageManager(cwd, agentDir, settings)

	if err := manager.Update(source, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	gotHead := strings.TrimSpace(gitOut(t, dest, "rev-parse", "HEAD"))
	if gotHead != wantHead {
		t.Fatalf("checkout HEAD=%q want upstream HEAD %q", gotHead, wantHead)
	}
	content, _ := os.ReadFile(filepath.Join(dest, "index.ts"))
	if string(content) != "v2" {
		t.Fatalf("working tree not reset to upstream: index.ts=%q", string(content))
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}
