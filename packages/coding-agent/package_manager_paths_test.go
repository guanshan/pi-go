package codingagent

import (
	"path/filepath"
	"testing"

	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

// TestAddSourceToSettingsStoresRelativeToScopeBase ports the TS "settings
// source normalization" cases (package-manager.test.ts:1141,1156): local
// sources persist relative to the scope base directory (user -> agentDir,
// project -> <cwd>/.pi) so the same install can be located from a different cwd.
func TestAddSourceToSettingsStoresRelativeToScopeBase(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()

	tests := []struct {
		name  string
		local bool
		base  func() string
	}{
		{name: "global relative to agent dir", local: false, base: func() string { return agentDir }},
		{name: "project relative to project .pi", local: true, base: func() string { return core.ProjectPiDir(cwd) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := core.NewSettingsManager(cwd, agentDir)
			manager := NewDefaultPackageManager(cwd, agentDir, settings)
			pkgDir := filepath.Join(cwd, "packages", "local-pkg")
			writeTestFile(t, filepath.Join(pkgDir, "extensions", "index.js"), "export default {}")

			// CLI input is a cwd-relative path; storage must be relative to base.
			if !manager.AddSourceToSettings("./packages/local-pkg", tt.local) {
				t.Fatal("expected source to be added")
			}
			want, err := filepath.Rel(tt.base(), pkgDir)
			if err != nil {
				t.Fatal(err)
			}
			stored := manager.Settings.PackageSources(tt.local)
			if len(stored) != 1 || stored[0].Source != want {
				t.Fatalf("stored=%#v want %q", stored, want)
			}
			// The stored relative path must resolve back to the original dir.
			scope := "user"
			if tt.local {
				scope = "project"
			}
			if got := manager.GetInstalledPath(stored[0].Source, scope); got != pkgDir {
				t.Fatalf("installed path drifted: %q want %q", got, pkgDir)
			}
		})
	}
}

// TestPredictedInstalledPathGitLayout proves git sources map to the TS layout
// <base>/git/<host>/<owner>/<repo> for representative URL forms, where base is
// <agentDir> (user) or <cwd>/.pi (project). Mirrors getGitInstallPath in
// package-manager.ts (~1951).
func TestPredictedInstalledPathGitLayout(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	manager := NewDefaultPackageManager(cwd, agentDir, core.NewSettingsManager(cwd, agentDir))

	cases := []struct {
		source      string
		host, owner string
		repo        string
	}{
		{"https://github.com/owner/repo.git", "github.com", "owner", "repo"},
		{"git:git@gitlab.example.com:team/repo.git", "gitlab.example.com", "team", "repo"},
		{"git:github:acme/tool", "github.com", "acme", "tool"},
		{"git:git.example.com/team/repo", "git.example.com", "team", "repo"},
	}
	for _, tt := range cases {
		t.Run(tt.source, func(t *testing.T) {
			wantUser := filepath.Join(agentDir, "git", tt.host, tt.owner, tt.repo)
			if got := manager.predictedInstalledPath(tt.source, false); got != wantUser {
				t.Fatalf("user git path=%q want %q", got, wantUser)
			}
			wantProject := filepath.Join(core.ProjectPiDir(cwd), "git", tt.host, tt.owner, tt.repo)
			if got := manager.predictedInstalledPath(tt.source, true); got != wantProject {
				t.Fatalf("project git path=%q want %q", got, wantProject)
			}
		})
	}
}

// TestPredictedInstalledPathNpmLayout proves npm sources map to the TS layout
// <base>/npm/node_modules/<name>, with scoped names keeping their @scope/
// segment. Mirrors getManagedNpmInstallPath in package-manager.ts (~1924).
func TestPredictedInstalledPathNpmLayout(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	manager := NewDefaultPackageManager(cwd, agentDir, core.NewSettingsManager(cwd, agentDir))

	cases := []struct {
		source string
		rel    []string
	}{
		{"npm:left-pad", []string{"left-pad"}},
		{"npm:left-pad@1.0.0", []string{"left-pad"}},
		{"npm:@scope/tool", []string{"@scope", "tool"}},
		{"npm:@scope/tool@2.0.0", []string{"@scope", "tool"}},
	}
	for _, tt := range cases {
		t.Run(tt.source, func(t *testing.T) {
			wantUser := filepath.Join(append([]string{agentDir, "npm", "node_modules"}, tt.rel...)...)
			if got := manager.predictedInstalledPath(tt.source, false); got != wantUser {
				t.Fatalf("user npm path=%q want %q", got, wantUser)
			}
			wantProject := filepath.Join(append([]string{core.ProjectPiDir(cwd), "npm", "node_modules"}, tt.rel...)...)
			if got := manager.predictedInstalledPath(tt.source, true); got != wantProject {
				t.Fatalf("project npm path=%q want %q", got, wantProject)
			}
		})
	}
}

// TestPredictedInstalledPathLegacyFallback proves a user-scope package installed
// under the old Go layout (<agentDir>/packages/<sanitized>) is still discovered
// when the new-layout path is absent.
func TestPredictedInstalledPathLegacyFallback(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	manager := NewDefaultPackageManager(cwd, agentDir, core.NewSettingsManager(cwd, agentDir))

	source := "npm:legacy-pkg"
	legacy := filepath.Join(agentDir, "packages", sanitizePackageSource(source))
	writeTestFile(t, filepath.Join(legacy, "index.js"), "module.exports = {}")
	if got := manager.predictedInstalledPath(source, false); got != legacy {
		t.Fatalf("legacy fallback path=%q want %q", got, legacy)
	}

	// Once the new-layout path exists it takes precedence.
	newPath := filepath.Join(agentDir, "npm", "node_modules", "legacy-pkg")
	writeTestFile(t, filepath.Join(newPath, "index.js"), "module.exports = {}")
	if got := manager.predictedInstalledPath(source, false); got != newPath {
		t.Fatalf("new-layout path=%q want %q", got, newPath)
	}
}

// TestRemoveSourceUsingEquivalentPathForm ports package-command-paths.test.ts:79
// and package-manager.test.ts:1170: an installed local source is removable using
// an equivalent path form (here, a trailing slash), because input and stored
// sources both resolve to the same absolute directory.
func TestRemoveSourceUsingEquivalentPathForm(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := core.NewSettingsManager(cwd, agentDir)
	manager := NewDefaultPackageManager(cwd, agentDir, settings)
	pkgDir := filepath.Join(cwd, "remove-local-pkg")
	writeTestFile(t, filepath.Join(pkgDir, "extensions", "index.js"), "export default {}")

	if !manager.AddSourceToSettings("./remove-local-pkg", false) {
		t.Fatal("expected source to be added")
	}
	if !manager.RemoveSourceFromSettings(pkgDir+string(filepath.Separator), false) {
		t.Fatal("expected trailing-slash form to match the stored relative source")
	}
	if len(manager.Settings.Global.Packages) != 0 {
		t.Fatalf("global packages after remove=%#v", manager.Settings.Global.Packages)
	}
}

// TestListUpdateGlobalLocalPackageNoDriftFromDifferentCwd proves an installed
// global local package, stored relative to agentDir, still resolves to its real
// directory when listed/updated from a different working directory.
func TestListUpdateGlobalLocalPackageNoDriftFromDifferentCwd(t *testing.T) {
	installCwd := t.TempDir()
	agentDir := t.TempDir()
	pkgDir := filepath.Join(agentDir, "local-pkgs", "tool")
	writeTestFile(t, filepath.Join(pkgDir, "extensions", "index.js"), "export default {}")

	settings := core.NewSettingsManager(installCwd, agentDir)
	installer := NewDefaultPackageManager(installCwd, agentDir, settings)
	if err := installer.InstallAndPersist(pkgDir, PackageManagerOperationOptions{Local: false}); err != nil {
		t.Fatal(err)
	}
	stored := settings.Global.Packages
	if len(stored) != 1 {
		t.Fatalf("expected one stored package, got %#v", stored)
	}
	// Stored as a path relative to agentDir, independent of the install cwd.
	wantStored, err := filepath.Rel(agentDir, pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	if stored[0].Source != wantStored {
		t.Fatalf("stored source=%q want %q", stored[0].Source, wantStored)
	}

	// Re-open the settings from a completely different cwd.
	otherCwd := t.TempDir()
	reread := core.NewSettingsManager(otherCwd, agentDir)
	reread.Global = settings.Global
	manager := NewDefaultPackageManager(otherCwd, agentDir, reread)

	records := manager.List(false)
	if len(records) != 1 || records[0].Path != pkgDir {
		t.Fatalf("list drifted from different cwd: %#v want path %q", records, pkgDir)
	}
	if got := manager.GetInstalledPath(stored[0].Source, "user"); got != pkgDir {
		t.Fatalf("get installed path drifted: %q want %q", got, pkgDir)
	}
	// Update matches the package by source from the new cwd (no "no matching
	// package" error); the path has no .git dir, so it is a no-op clone-skip.
	if err := manager.Update(pkgDir, nil); err != nil {
		t.Fatalf("update from different cwd: %v", err)
	}
}
