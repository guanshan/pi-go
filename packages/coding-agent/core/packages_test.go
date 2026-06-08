package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

func TestHandlePackageCommandUsesPackagesSetting(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	packageDir := filepath.Join(cwd, "local-package")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := NewSettingsManager(cwd, agentDir)

	handled, err := HandlePackageCommand([]string{"install", packageDir, "--local"}, cwd, agentDir, settings, nil)
	if err != nil || !handled {
		t.Fatalf("install handled=%v err=%v", handled, err)
	}
	// Local sources are stored relative to the project scope base (<cwd>/.pi),
	// matching normalizePackageSourceForSettings in package-manager.ts.
	wantStored, err := filepath.Rel(ProjectPiDir(cwd), packageDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Project.Packages) != 1 || settings.Project.Packages[0].Source != wantStored {
		t.Fatalf("project packages=%#v want stored source %q", settings.Project.Packages, wantStored)
	}
	if len(settings.Project.InstalledPackages) != 0 {
		t.Fatalf("legacy installed packages written=%#v", settings.Project.InstalledPackages)
	}

	handled, err = HandlePackageCommand([]string{"remove", packageDir, "--local"}, cwd, agentDir, settings, nil)
	if err != nil || !handled {
		t.Fatalf("remove handled=%v err=%v", handled, err)
	}
	if len(settings.Project.Packages) != 0 {
		t.Fatalf("project packages after remove=%#v", settings.Project.Packages)
	}
}

func TestNormalizePackageSourceForSettings(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	pkg := filepath.Join(cwd, "pkgs", "tool")

	globalWant, err := filepath.Rel(agentDir, pkg)
	if err != nil {
		t.Fatal(err)
	}
	projectWant, err := filepath.Rel(ProjectPiDir(cwd), pkg)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		source string
		local  bool
		want   string
	}{
		{name: "global local relative to agent dir", source: "./pkgs/tool", local: false, want: globalWant},
		{name: "project local relative to project .pi", source: "./pkgs/tool", local: true, want: projectWant},
		{name: "absolute global", source: pkg, local: false, want: globalWant},
		{name: "npm stored verbatim", source: "npm:@scope/pkg@1.0.0", local: false, want: "npm:@scope/pkg@1.0.0"},
		{name: "git stored verbatim", source: "git:https://example.com/p.git", local: true, want: "git:https://example.com/p.git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizePackageSourceForSettings(tt.source, cwd, agentDir, tt.local)
			if got != tt.want {
				t.Fatalf("normalize(%q)=%q want %q", tt.source, got, tt.want)
			}
			// Managed sources keep their managed install path; local sources must
			// resolve from the stored relative form back to the original dir.
			if !isManagedPackageSource(tt.source) {
				if resolved := packageInstallPath(got, tt.local, cwd, agentDir); resolved != pkg {
					t.Fatalf("install path=%q want %q", resolved, pkg)
				}
			}
		})
	}
}

func TestInstallPackageStoresRelativeSourceAndResolvesFromDifferentCwd(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	packageDir := filepath.Join(cwd, "local-package")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := NewSettingsManager(cwd, agentDir)

	if err := installPackage(packageDir, false, cwd, agentDir, settings); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Rel(agentDir, packageDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Global.Packages) != 1 || settings.Global.Packages[0].Source != want {
		t.Fatalf("global packages=%#v want stored %q", settings.Global.Packages, want)
	}

	// Listing the global package from a different cwd must resolve back to the
	// original directory rather than drifting with the new cwd.
	otherCwd := t.TempDir()
	entries := packageEntries(settings, otherCwd, agentDir, false)
	if len(entries) != 1 || entries[0].Record.Path != packageDir {
		t.Fatalf("entries from different cwd=%#v want path %q", entries, packageDir)
	}
}

func TestHandlePackageCommandDoesNotOwnConfig(t *testing.T) {
	handled, err := HandlePackageCommand([]string{"config", "--list"}, t.TempDir(), t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Fatal("core package command handler should not handle config")
	}
}

func TestLoadResourcesUsesPackagesSettingAndFilters(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	setTestHome(t, t.TempDir())
	packageDir := filepath.Join(cwd, "package")
	writeCoreTestFile(t, filepath.Join(packageDir, "prompts", "keep.md"), "keep prompt")
	writeCoreTestFile(t, filepath.Join(packageDir, "prompts", "drop.md"), "drop prompt")
	writeCoreTestFile(t, filepath.Join(packageDir, "skills", "demo", "SKILL.md"), "---\ndescription: demo skill\n---\ndemo skill")
	writeCoreTestFile(t, filepath.Join(packageDir, "extensions", "ext.js"), "extension")

	settings := NewSettingsManager(cwd, agentDir)
	settings.Global.Packages = []PackageSetting{{
		Source:     packageDir,
		Prompts:    []string{"keep.md"},
		Skills:     []string{},
		Extensions: []string{"ext.js"},
	}}
	resources := LoadResources(cwd, agentDir, cli.Args{}, settings)
	if _, ok := resources.PromptTemplates["keep"]; !ok {
		t.Fatalf("keep prompt missing: %#v", resources.PromptTemplates)
	}
	if _, ok := resources.PromptTemplates["drop"]; ok {
		t.Fatalf("drop prompt loaded despite filter: %#v", resources.PromptTemplates)
	}
	if len(resources.Skills) != 0 {
		t.Fatalf("skills loaded despite empty filter: %#v", resources.Skills)
	}
	if len(resources.Extensions) != 1 || resources.Extensions[0] != filepath.Join(packageDir, "extensions", "ext.js") {
		t.Fatalf("extensions=%#v", resources.Extensions)
	}
}

func TestLoadResourcesUsesTopLevelSettingsResourcePathsAndOverrides(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	projectBase := ProjectPiDir(cwd)
	writeCoreTestFile(t, filepath.Join(projectBase, "prompts", "keep.md"), "keep prompt")
	writeCoreTestFile(t, filepath.Join(projectBase, "prompts", "drop.md"), "drop prompt")
	writeCoreTestFile(t, filepath.Join(projectBase, "custom-prompts", "bonus.md"), "bonus prompt")
	writeCoreTestFile(t, filepath.Join(projectBase, "skills", "foo", "SKILL.md"), "---\ndescription: foo skill\n---\nfoo skill")
	writeCoreTestFile(t, filepath.Join(projectBase, "custom-skills", "bar", "SKILL.md"), "---\ndescription: bar skill\n---\nbar skill")
	writeCoreTestFile(t, filepath.Join(agentDir, "extensions", "on.js"), "export default {}")
	writeCoreTestFile(t, filepath.Join(agentDir, "extensions", "off.js"), "export default {}")
	writeCoreTestFile(t, filepath.Join(projectBase, "custom-ext.js"), "export default {}")
	writeCoreTestFile(t, filepath.Join(agentDir, "themes", "dark.json"), "{}")
	writeCoreTestFile(t, filepath.Join(agentDir, "themes", "skip.json"), "{}")

	settings := NewSettingsManager(cwd, agentDir)
	settings.Project.Prompts = []string{"custom-prompts", "-prompts/drop.md"}
	settings.Project.Skills = []string{"custom-skills/bar", "-skills/foo"}
	settings.Project.Extensions = []string{"custom-ext.js"}
	settings.Global.Extensions = []string{"-extensions/off.js"}
	settings.Global.Themes = []string{"-themes/skip.json"}

	resources := LoadResources(cwd, agentDir, cli.Args{}, settings)
	if _, ok := resources.PromptTemplates["keep"]; !ok {
		t.Fatalf("default prompt missing: %#v", resources.PromptTemplates)
	}
	if _, ok := resources.PromptTemplates["bonus"]; !ok {
		t.Fatalf("configured prompt missing: %#v", resources.PromptTemplates)
	}
	if _, ok := resources.PromptTemplates["drop"]; ok {
		t.Fatalf("disabled prompt loaded: %#v", resources.PromptTemplates)
	}
	if _, ok := resources.Skills["bar"]; !ok {
		t.Fatalf("configured skill missing: %#v", resources.Skills)
	}
	if _, ok := resources.Skills["foo"]; ok {
		t.Fatalf("disabled skill loaded: %#v", resources.Skills)
	}
	wantExtension := filepath.Join(projectBase, "custom-ext.js")
	if !containsPath(resources.Extensions, wantExtension) {
		t.Fatalf("configured extension missing: %#v", resources.Extensions)
	}
	if containsPath(resources.Extensions, filepath.Join(agentDir, "extensions", "off.js")) {
		t.Fatalf("disabled extension loaded: %#v", resources.Extensions)
	}
	if !containsPath(resources.Themes, filepath.Join(agentDir, "themes", "dark.json")) {
		t.Fatalf("default theme missing: %#v", resources.Themes)
	}
	if containsPath(resources.Themes, filepath.Join(agentDir, "themes", "skip.json")) {
		t.Fatalf("disabled theme loaded: %#v", resources.Themes)
	}
}

func TestParsePackageUpdateTargets(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		kind   string
		source string
	}{
		{name: "default all", args: []string{"update"}, kind: "all"},
		{name: "self flag", args: []string{"update", "--self"}, kind: "self"},
		{name: "extensions flag", args: []string{"update", "--extensions"}, kind: "extensions"},
		{name: "extension value", args: []string{"update", "--extension", "npm:@scope/pkg"}, kind: "extensions", source: "npm:@scope/pkg"},
		{name: "positional package", args: []string{"update", "git:https://example.com/plugin.git"}, kind: "extensions", source: "git:https://example.com/plugin.git"},
		{name: "pi alias", args: []string{"update", "pi"}, kind: "self"},
		{name: "self plus extensions", args: []string{"update", "--self", "--extensions"}, kind: "all"},
		{name: "pi plus extensions", args: []string{"update", "pi", "--extensions"}, kind: "all"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, ok := parsePackageCommand(tt.args)
			if !ok {
				t.Fatalf("parsePackageCommand(%v) did not handle command", tt.args)
			}
			if err := packageCommandValidationError(options); err != nil {
				t.Fatalf("validation error: %v", err)
			}
			if options.UpdateTarget.Kind != tt.kind || options.UpdateTarget.Source != tt.source {
				t.Fatalf("target=%#v want kind=%q source=%q", options.UpdateTarget, tt.kind, tt.source)
			}
		})
	}
}

func TestParsePackageCommandValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing install source", args: []string{"install"}},
		{name: "unknown install option", args: []string{"install", "--self", "pkg"}},
		{name: "missing extension value", args: []string{"update", "--extension"}},
		{name: "conflicting extension and self", args: []string{"update", "--extension", "pkg", "--self"}},
		{name: "extra argument", args: []string{"remove", "one", "two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, ok := parsePackageCommand(tt.args)
			if !ok {
				t.Fatalf("parsePackageCommand(%v) did not handle command", tt.args)
			}
			if err := packageCommandValidationError(options); err == nil {
				t.Fatalf("expected validation error for %#v", options)
			}
		})
	}
}

func TestPackageCommandMatchesNPMIdentity(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := NewSettingsManager(cwd, agentDir)
	settings.Global.Packages = []PackageSetting{{Source: "npm:@scope/pkg@1.0.0"}}
	settings.Global.InstalledPackages = []PackageRecord{{Source: "npm:legacy@1.0.0", Path: filepath.Join(agentDir, "packages", "legacy")}}

	if err := removePackage("npm:@scope/pkg", false, settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.Global.Packages) != 0 {
		t.Fatalf("global packages=%#v", settings.Global.Packages)
	}
	if err := removePackage("npm:legacy", false, settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.Global.InstalledPackages) != 0 {
		t.Fatalf("legacy packages=%#v", settings.Global.InstalledPackages)
	}
	if err := updatePackages(packageUpdateTarget{Kind: "extensions", Source: "npm:missing"}, settings, nil, settings.CWD, settings.AgentDir); err == nil {
		t.Fatal("expected missing package update error")
	}
}

func TestSelfUpdateCommandSupportsOverride(t *testing.T) {
	t.Setenv("PI_SELF_UPDATE_COMMAND", "go version")
	display, name, args, err := selfUpdateCommand()
	if err != nil {
		t.Fatal(err)
	}
	if display != "go version" || name != "go" || len(args) != 1 || args[0] != "version" {
		t.Fatalf("command display=%q name=%q args=%#v", display, name, args)
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func writeCoreTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
