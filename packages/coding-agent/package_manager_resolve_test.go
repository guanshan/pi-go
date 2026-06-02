package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

// TestHandlePackageCommandRemoveDeletesInstalledDir proves the CLI remove path
// now delegates to DefaultPackageManager, which deletes installed code on disk
// rather than only editing settings (the P1-3 regression).
func TestHandlePackageCommandRemoveDeletesInstalledDir(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := core.NewSettingsManager(cwd, agentDir)
	source := "npm:demo"
	settings.AddPackageSource(source, false)
	if err := settings.SaveGlobal(); err != nil {
		t.Fatal(err)
	}
	// New TS layout: npm packages live under <agentDir>/npm/node_modules/<name>.
	installed := filepath.Join(agentDir, "npm", "node_modules", "demo")
	writeTestFile(t, filepath.Join(installed, "index.js"), "module.exports = {}")

	handled, err := core.HandlePackageCommand([]string{"remove", source}, cwd, agentDir, settings, NewCorePackageManager)
	if err != nil || !handled {
		t.Fatalf("remove handled=%v err=%v", handled, err)
	}
	if _, statErr := os.Stat(installed); !os.IsNotExist(statErr) {
		t.Fatalf("installed package dir still present after remove: %v", statErr)
	}
	if len(settings.Global.Packages) != 0 {
		t.Fatalf("source not removed from settings: %#v", settings.Global.Packages)
	}
}

// TestInstallPackageDependenciesHonorsNpmCommand verifies the package manager
// runs the configured npmCommand (e.g. a mise/pnpm/bun wrapper) instead of a
// hardcoded "npm", and drops npm-specific flags for custom commands.
func TestInstallPackageDependenciesHonorsNpmCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the fake package manager")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked.log")
	script := filepath.Join(dir, "fakepm.sh")
	writeTestFile(t, script, "#!/bin/sh\necho \"$@\" >> \""+marker+"\"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}

	settings := core.NewSettingsManager(dir, dir)
	settings.Project.NPMCommand = []string{script, "exec", "--", "pnpm"}
	mgr := NewDefaultPackageManager(dir, dir, settings)

	pkgDir := filepath.Join(dir, "pkg")
	writeTestFile(t, filepath.Join(pkgDir, "package.json"), `{"dependencies":{"left-pad":"1.0.0"}}`)

	if err := mgr.installPackageDependencies(pkgDir); err != nil {
		t.Fatal(err)
	}
	logged, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("configured npm command was not invoked: %v", err)
	}
	got := string(logged)
	if !strings.Contains(got, "exec -- pnpm install") {
		t.Fatalf("expected configured command with bare install, got %q", got)
	}
	if strings.Contains(got, "--omit=dev") {
		t.Fatalf("npm-specific flags leaked into custom command: %q", got)
	}
}

func TestDefaultPackageManagerResolveResources(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := core.NewSettingsManager(cwd, agentDir)
	settings.Project.DisabledExtensions = []string{"project.js"}

	writeTestFile(t, filepath.Join(cwd, ConfigDirName, "extensions", "project.js"), "project extension")
	writeTestFile(t, filepath.Join(agentDir, "extensions", "user.js"), "user extension")
	writeTestFile(t, filepath.Join(cwd, ".agents", "skills", "workspace", "SKILL.md"), "workspace skill")
	packageDir := filepath.Join(cwd, "package")
	writeTestFile(t, filepath.Join(packageDir, "package.json"), `{"pi":{"prompts":["prompts/pkg.md"],"themes":["themes/pkg.json"]}}`)
	writeTestFile(t, filepath.Join(packageDir, "prompts", "pkg.md"), "package prompt")
	writeTestFile(t, filepath.Join(packageDir, "themes", "pkg.json"), `{"name":"pkg"}`)
	settings.Project.InstalledPackages = []core.PackageRecord{{Source: "local-package", Path: packageDir, Local: true}}

	manager := NewDefaultPackageManager(cwd, agentDir, settings)
	resolved, err := manager.Resolve()
	if err != nil {
		t.Fatal(err)
	}

	projectExtension := findResolved(resolved.Extensions, filepath.Join(cwd, ConfigDirName, "extensions", "project.js"))
	if projectExtension == nil || projectExtension.Enabled || projectExtension.Metadata.Scope != "project" {
		t.Fatalf("project extension=%#v", projectExtension)
	}
	userExtension := findResolved(resolved.Extensions, filepath.Join(agentDir, "extensions", "user.js"))
	if userExtension == nil || !userExtension.Enabled || userExtension.Metadata.Scope != "user" {
		t.Fatalf("user extension=%#v", userExtension)
	}
	packagePrompt := findResolved(resolved.Prompts, filepath.Join(packageDir, "prompts", "pkg.md"))
	if packagePrompt == nil || packagePrompt.Metadata.Origin != "package" || packagePrompt.Metadata.Source != "local-package" {
		t.Fatalf("package prompt=%#v", packagePrompt)
	}
	workspaceSkill := findResolved(resolved.Skills, filepath.Join(cwd, ".agents", "skills", "workspace", "SKILL.md"))
	if workspaceSkill == nil || workspaceSkill.Metadata.Scope != "project" {
		t.Fatalf("workspace skill=%#v", workspaceSkill)
	}
}

func TestDefaultPackageManagerResolveTopLevelResourceSettings(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	projectBase := filepath.Join(cwd, ConfigDirName)
	writeTestFile(t, filepath.Join(projectBase, "prompts", "keep.md"), "keep")
	writeTestFile(t, filepath.Join(projectBase, "prompts", "drop.md"), "drop")
	writeTestFile(t, filepath.Join(projectBase, "custom-prompts", "bonus.md"), "bonus")
	writeTestFile(t, filepath.Join(projectBase, "skills", "skip", "SKILL.md"), "skip")
	writeTestFile(t, filepath.Join(projectBase, "custom-skills", "demo", "SKILL.md"), "demo")
	writeTestFile(t, filepath.Join(agentDir, "extensions", "on.js"), "on")
	writeTestFile(t, filepath.Join(agentDir, "extensions", "off.js"), "off")
	writeTestFile(t, filepath.Join(projectBase, "custom-ext.js"), "custom")

	settings := core.NewSettingsManager(cwd, agentDir)
	settings.Project.Prompts = []string{"custom-prompts", "-prompts/drop.md"}
	settings.Project.Skills = []string{"custom-skills/demo", "-skills/skip"}
	settings.Project.Extensions = []string{"custom-ext.js"}
	settings.Global.Extensions = []string{"-extensions/off.js"}

	manager := NewDefaultPackageManager(cwd, agentDir, settings)
	resolved, err := manager.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if prompt := findResolved(resolved.Prompts, filepath.Join(projectBase, "custom-prompts", "bonus.md")); prompt == nil || !prompt.Enabled || prompt.Metadata.Source != "local" {
		t.Fatalf("configured prompt=%#v", prompt)
	}
	if prompt := findResolved(resolved.Prompts, filepath.Join(projectBase, "prompts", "drop.md")); prompt == nil || prompt.Enabled {
		t.Fatalf("disabled prompt=%#v", prompt)
	}
	if skill := findResolved(resolved.Skills, filepath.Join(projectBase, "custom-skills", "demo", "SKILL.md")); skill == nil || !skill.Enabled || skill.Metadata.Source != "local" {
		t.Fatalf("configured skill=%#v", skill)
	}
	if skill := findResolved(resolved.Skills, filepath.Join(projectBase, "skills", "skip", "SKILL.md")); skill == nil || skill.Enabled {
		t.Fatalf("disabled skill=%#v", skill)
	}
	if extension := findResolved(resolved.Extensions, filepath.Join(projectBase, "custom-ext.js")); extension == nil || !extension.Enabled || extension.Metadata.Source != "local" {
		t.Fatalf("configured extension=%#v", extension)
	}
	if extension := findResolved(resolved.Extensions, filepath.Join(agentDir, "extensions", "off.js")); extension == nil || extension.Enabled {
		t.Fatalf("disabled extension=%#v", extension)
	}
	if extension := findResolved(resolved.Extensions, filepath.Join(agentDir, "extensions", "on.js")); extension == nil || !extension.Enabled {
		t.Fatalf("default extension=%#v", extension)
	}
}

func TestToggleConfigResourcePersistsTopLevelAndPackageOverrides(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := core.NewSettingsManager(cwd, agentDir)

	topLevelPath := filepath.Join(core.ProjectPiDir(cwd), "extensions", "demo.js")
	topLevel := configResourceItem{
		ResourceType: "extensions",
		Resource: ResolvedResource{
			Path:    topLevelPath,
			Enabled: true,
			Metadata: PathMetadata{
				Source:  "auto",
				Scope:   "project",
				Origin:  "top-level",
				BaseDir: core.ProjectPiDir(cwd),
			},
		},
	}
	scope, err := toggleConfigResource(settings, topLevel, cwd, agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if scope != "project" || len(settings.Project.Extensions) != 1 || settings.Project.Extensions[0] != "-extensions/demo.js" {
		t.Fatalf("project extensions=%#v scope=%q", settings.Project.Extensions, scope)
	}

	packageDir := filepath.Join(cwd, "pkg")
	settings.Project.Packages = []core.PackageSetting{{Source: packageDir}}
	packageItem := configResourceItem{
		ResourceType: "prompts",
		Resource: ResolvedResource{
			Path:    filepath.Join(packageDir, "prompts", "guide.md"),
			Enabled: false,
			Metadata: PathMetadata{
				Source:  packageDir,
				Scope:   "project",
				Origin:  "package",
				BaseDir: packageDir,
			},
		},
	}
	scope, err = toggleConfigResource(settings, packageItem, cwd, agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if scope != "project" || len(settings.Project.Packages) != 1 || len(settings.Project.Packages[0].Prompts) != 1 || settings.Project.Packages[0].Prompts[0] != "+prompts/guide.md" {
		t.Fatalf("project packages=%#v scope=%q", settings.Project.Packages, scope)
	}
}

func TestPackageSettingsJSONCompatibility(t *testing.T) {
	var settings core.Settings
	if err := json.Unmarshal([]byte(`{"packages":["npm:simple",{"source":"./pkg","skills":["skills/demo/SKILL.md"]}]}`), &settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.Packages) != 2 {
		t.Fatalf("packages=%#v", settings.Packages)
	}
	if settings.Packages[0].Source != "npm:simple" {
		t.Fatalf("string package source=%#v", settings.Packages[0])
	}
	if settings.Packages[1].Source != "./pkg" || len(settings.Packages[1].Skills) != 1 {
		t.Fatalf("object package source=%#v", settings.Packages[1])
	}

	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, `"npm:simple"`) || !strings.Contains(got, `"source":"./pkg"`) {
		t.Fatalf("marshaled packages=%s", got)
	}
}

func TestDefaultPackageManagerResolvePackagesSetting(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	packageDir := filepath.Join(cwd, "package")
	writeTestFile(t, filepath.Join(packageDir, "package.json"), `{"pi":{"prompts":["prompts/pkg.md"]}}`)
	writeTestFile(t, filepath.Join(packageDir, "prompts", "pkg.md"), "package prompt")

	settings := core.NewSettingsManager(cwd, agentDir)
	settings.Project.Packages = []core.PackageSetting{{Source: packageDir, Prompts: []string{"prompts/pkg.md"}}}
	manager := NewDefaultPackageManager(cwd, agentDir, settings)
	resolved, err := manager.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	packagePrompt := findResolved(resolved.Prompts, filepath.Join(packageDir, "prompts", "pkg.md"))
	if packagePrompt == nil || packagePrompt.Metadata.Source != packageDir {
		t.Fatalf("package prompt=%#v", packagePrompt)
	}
	configured := manager.ListConfiguredPackages()
	if len(configured) != 1 || !configured[0].Filtered || configured[0].Source != packageDir {
		t.Fatalf("configured=%#v", configured)
	}
}

func TestDefaultPackageManagerAppliesPackageFilters(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	packageDir := filepath.Join(cwd, "package")
	writeTestFile(t, filepath.Join(packageDir, "prompts", "keep.md"), "keep")
	writeTestFile(t, filepath.Join(packageDir, "prompts", "drop.md"), "drop")
	writeTestFile(t, filepath.Join(packageDir, "skills", "demo", "SKILL.md"), "skill")

	settings := core.NewSettingsManager(cwd, agentDir)
	settings.Project.Packages = []core.PackageSetting{{
		Source:  packageDir,
		Prompts: []string{"keep.md"},
		Skills:  []string{},
	}}
	manager := NewDefaultPackageManager(cwd, agentDir, settings)
	resolved, err := manager.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	keep := findResolved(resolved.Prompts, filepath.Join(packageDir, "prompts", "keep.md"))
	drop := findResolved(resolved.Prompts, filepath.Join(packageDir, "prompts", "drop.md"))
	if keep == nil || !keep.Enabled {
		t.Fatalf("keep prompt=%#v", keep)
	}
	if drop == nil || drop.Enabled {
		t.Fatalf("drop prompt=%#v", drop)
	}
	skill := findResolved(resolved.Skills, filepath.Join(packageDir, "skills", "demo", "SKILL.md"))
	if skill == nil || skill.Enabled {
		t.Fatalf("skill=%#v", skill)
	}
}

func TestDefaultPackageManagerResolveExtensionSources(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sourceDir := filepath.Join(cwd, "inline-package")
	writeTestFile(t, filepath.Join(sourceDir, "package.json"), `{"pi":{"extensions":["ext.js"],"skills":["skills/demo/SKILL.md"]}}`)
	writeTestFile(t, filepath.Join(sourceDir, "ext.js"), "extension")
	writeTestFile(t, filepath.Join(sourceDir, "skills", "demo", "SKILL.md"), "demo skill")

	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	resolved, err := manager.ResolveExtensionSources([]string{sourceDir}, ResolveExtensionSourcesOptions{Temporary: true})
	if err != nil {
		t.Fatal(err)
	}
	extension := findResolved(resolved.Extensions, filepath.Join(sourceDir, "ext.js"))
	if extension == nil || extension.Metadata.Scope != "temporary" || extension.Metadata.Origin != "package" {
		t.Fatalf("extension=%#v", extension)
	}
	skill := findResolved(resolved.Skills, filepath.Join(sourceDir, "skills", "demo", "SKILL.md"))
	if skill == nil || skill.Metadata.Source != sourceDir {
		t.Fatalf("skill=%#v", skill)
	}
}

// TestDefaultPackageManagerNpmInstallUsesRealInstall proves the npm branch runs
// a real `install <spec> --prefix <npmRoot>` (so transitive deps land in a
// proper node_modules tree) instead of the old `pack`+`tar` flow, and that the
// resulting install path follows the TS layout <npmRoot>/node_modules/<name>.
func TestDefaultPackageManagerNpmInstallUsesRealInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake npm is POSIX-only")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	// The fake npm records its argv, rejects `pack`, and emulates a real install:
	// it materializes <prefix>/node_modules/<name> with a transitive dep marker.
	argLog := filepath.Join(agentDir, "npm-args.log")
	installFakeNPM(t, `#!/bin/sh
set -eu
echo "$@" >> "`+argLog+`"
if [ "$1" = "pack" ]; then
	echo "pack must not be used" >&2
	exit 3
fi
if [ "$1" = "install" ]; then
	spec="$2"
	prefix=""
	while [ "$#" -gt 0 ]; do
		if [ "$1" = "--prefix" ]; then
			prefix="$2"
		fi
		shift
	done
	name=$(printf '%s' "$spec" | sed 's/@[^@/]*$//')
	mkdir -p "$prefix/node_modules/$name/node_modules/dep"
	printf transitive > "$prefix/node_modules/$name/node_modules/dep/.installed"
	exit 0
fi
echo "unexpected npm $*" >&2
exit 2
`)

	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	record, err := manager.Install("npm:fixture@1.0.0", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(agentDir, "npm", "node_modules", "fixture")
	if record.Path != wantPath {
		t.Fatalf("install path=%q want %q", record.Path, wantPath)
	}
	// Transitive dependency installed by the real `npm install` tree.
	if _, err := os.Stat(filepath.Join(record.Path, "node_modules", "dep", ".installed")); err != nil {
		t.Fatalf("transitive dependency marker missing: %v", err)
	}
	logged, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("npm was not invoked: %v", err)
	}
	got := string(logged)
	if strings.Contains(got, "pack") {
		t.Fatalf("npm pack was used instead of install: %q", got)
	}
	wantPrefix := filepath.Join(agentDir, "npm")
	if !strings.Contains(got, "install fixture@1.0.0 --prefix "+wantPrefix) {
		t.Fatalf("expected install-style invocation with --prefix, got %q", got)
	}
	if !strings.Contains(got, "--legacy-peer-deps") {
		t.Fatalf("default npm path should pass --legacy-peer-deps, got %q", got)
	}
}

// TestDefaultPackageManagerNpmScopedInstallPath proves a scoped npm spec
// (@scope/name) installs to <npmRoot>/node_modules/@scope/name, matching
// getManagedNpmInstallPath in package-manager.ts.
func TestDefaultPackageManagerNpmScopedInstallPath(t *testing.T) {
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
	mkdir -p "$prefix/node_modules/@scope/tool"
	printf installed > "$prefix/node_modules/@scope/tool/.installed"
	exit 0
fi
echo "unexpected npm $*" >&2
exit 2
`)

	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	record, err := manager.Install("npm:@scope/tool@2.0.0", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(agentDir, "npm", "node_modules", "@scope", "tool")
	if record.Path != wantPath {
		t.Fatalf("scoped install path=%q want %q", record.Path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(record.Path, ".installed")); err != nil {
		t.Fatalf("scoped package marker missing: %v", err)
	}
}

// TestDefaultPackageManagerGitInstallRollbackKeepsExistingPackage proves the git
// branch still stages into a sibling temp dir and atomically swaps, so a failed
// dependency install leaves a prior checkout intact. The git dest follows the TS
// layout <agentDir>/git/<host>/<owner>/<repo>.
func TestDefaultPackageManagerGitInstallRollbackKeepsExistingPackage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fakes for git/npm")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	source := "git:github:acme/tool"
	dest := filepath.Join(agentDir, "git", "github.com", "acme", "tool")
	writeTestFile(t, filepath.Join(dest, "README.md"), "existing")

	// Fake git clone produces a package.json with deps (forcing a dependency
	// install); fake npm install then fails, triggering rollback.
	bin := t.TempDir()
	gitScript := `#!/bin/sh
set -eu
if [ "$1" = "clone" ]; then
	for target in "$@"; do :; done
	mkdir -p "$target"
	cat > "$target/package.json" <<'JSON'
{"name":"tool","version":"1.0.0","dependencies":{"dep":"1.0.0"}}
JSON
	exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatal(err)
	}
	npmScript := `#!/bin/sh
echo "install failed" >&2
exit 9
`
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(npmScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	manager := NewDefaultPackageManager(cwd, agentDir, nil)
	if _, err := manager.Install(source, false, nil); err == nil {
		t.Fatal("expected install error")
	}
	data, err := os.ReadFile(filepath.Join(dest, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Fatalf("existing package was not restored: %q", data)
	}
}

func TestDefaultPackageManagerConfiguredPackageHelpers(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	localPackage := filepath.Join(cwd, "local-package")
	writeTestFile(t, filepath.Join(localPackage, "README.md"), "local")
	settings := core.NewSettingsManager(cwd, agentDir)
	manager := NewDefaultPackageManager(cwd, agentDir, settings)

	if !manager.AddSourceToSettings(localPackage, true) {
		t.Fatal("expected source to be added")
	}
	if manager.AddSourceToSettings(localPackage, true) {
		t.Fatal("duplicate source was added")
	}
	// Local sources persist relative to the project scope base (<cwd>/.pi) yet
	// must still resolve back to the original directory.
	wantStored, err := filepath.Rel(core.ProjectPiDir(cwd), localPackage)
	if err != nil {
		t.Fatal(err)
	}
	if got := manager.GetInstalledPath(localPackage, "project"); got != localPackage {
		t.Fatalf("installed path=%q", got)
	}
	configured := manager.ListConfiguredPackages()
	if len(configured) != 1 || configured[0].Source != wantStored || configured[0].Scope != "project" {
		t.Fatalf("configured=%#v want stored source %q", configured, wantStored)
	}
	if configured[0].InstalledPath != localPackage {
		t.Fatalf("installed path drifted: %q want %q", configured[0].InstalledPath, localPackage)
	}
	if !manager.RemoveSourceFromSettings(localPackage, true) {
		t.Fatal("expected source to be removed")
	}
	if len(manager.ListConfiguredPackages()) != 0 {
		t.Fatalf("configured after remove=%#v", manager.ListConfiguredPackages())
	}

	var events []ProgressEvent
	manager.SetProgressCallback(func(event ProgressEvent) { events = append(events, event) })
	if err := manager.InstallAndPersist(localPackage, PackageManagerOperationOptions{Local: true}); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("default progress callback was not used")
	}
	removed, err := manager.RemoveAndPersist(localPackage, PackageManagerOperationOptions{Local: true})
	if err != nil || !removed {
		t.Fatalf("remove and persist removed=%v err=%v", removed, err)
	}
}

func installFakeNPM(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, "npm")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDefaultPackageManagerMatchesPackageIdentity(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := core.NewSettingsManager(cwd, agentDir)
	settings.Global.Packages = []core.PackageSetting{{Source: "npm:@scope/pkg@1.0.0"}}
	settings.Project.Packages = []core.PackageSetting{{Source: "git:https://github.com/acme/tool.git#main"}}
	manager := NewDefaultPackageManager(cwd, agentDir, settings)

	if !manager.AddSourceToSettings("npm:@scope/pkg@2.0.0", false) {
		t.Fatal("expected npm package source to update by identity")
	}
	if len(settings.Global.Packages) != 1 || settings.Global.Packages[0].Source != "npm:@scope/pkg@2.0.0" {
		t.Fatalf("global packages=%#v", settings.Global.Packages)
	}
	if !manager.RemoveSourceFromSettings("npm:@scope/pkg", false) {
		t.Fatal("expected npm package source to remove by identity")
	}
	if len(settings.Global.Packages) != 0 {
		t.Fatalf("global packages after remove=%#v", settings.Global.Packages)
	}
	if !manager.RemoveSourceFromSettings("git:github:acme/tool", true) {
		t.Fatal("expected git package source to remove by identity")
	}
	if len(settings.Project.Packages) != 0 {
		t.Fatalf("project packages after remove=%#v", settings.Project.Packages)
	}
}

func TestDefaultPackageManagerRemoveDoesNotPersistSettings(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := core.NewSettingsManager(cwd, agentDir)
	settings.Global.Packages = []core.PackageSetting{{Source: "npm:test-package"}}
	manager := NewDefaultPackageManager(cwd, agentDir, settings)
	installedPath := manager.predictedInstalledPath("npm:test-package", false)
	writeTestFile(t, filepath.Join(installedPath, "README.md"), "installed")

	if err := manager.Remove("npm:test-package", false); err != nil {
		t.Fatal(err)
	}
	if fileExistsLocal(installedPath) {
		t.Fatalf("installed path still exists: %s", installedPath)
	}
	if len(settings.Global.Packages) != 1 {
		t.Fatalf("remove should not persist settings: %#v", settings.Global.Packages)
	}

	writeTestFile(t, filepath.Join(installedPath, "README.md"), "installed")
	removed, err := manager.RemoveAndPersist("npm:test-package")
	if err != nil || !removed {
		t.Fatalf("remove and persist removed=%v err=%v", removed, err)
	}
	if len(settings.Global.Packages) != 0 {
		t.Fatalf("source was not removed from settings: %#v", settings.Global.Packages)
	}
}

func findResolved(resources []ResolvedResource, path string) *ResolvedResource {
	for i := range resources {
		if resources[i].Path == path {
			return &resources[i]
		}
	}
	return nil
}
