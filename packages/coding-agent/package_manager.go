package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

type PackageSource struct {
	Raw    string
	Kind   string
	Name   string
	Ref    string
	Local  bool
	Pinned bool
}

type ProgressEvent struct {
	Type    string
	Message string
	Path    string
}

type ProgressCallback func(ProgressEvent)

type PathMetadata struct {
	Source  string `json:"source"`
	Scope   string `json:"scope"`
	Origin  string `json:"origin"`
	BaseDir string `json:"baseDir,omitempty"`
}

type ResolvedResource struct {
	Path     string       `json:"path"`
	Enabled  bool         `json:"enabled"`
	Metadata PathMetadata `json:"metadata"`
}

type ResolvedPaths struct {
	Extensions []ResolvedResource `json:"extensions"`
	Skills     []ResolvedResource `json:"skills"`
	Prompts    []ResolvedResource `json:"prompts"`
	Themes     []ResolvedResource `json:"themes"`
}

type MissingSourceAction string

const (
	MissingSourceInstall MissingSourceAction = "install"
	MissingSourceSkip    MissingSourceAction = "skip"
	MissingSourceError   MissingSourceAction = "error"
)

type MissingSourceHandler func(source string) (MissingSourceAction, error)

type ResolveExtensionSourcesOptions struct {
	Local     bool
	Temporary bool
}

type PackageManagerOperationOptions struct {
	Local bool
}

type ConfiguredPackage struct {
	Source        string `json:"source"`
	Scope         string `json:"scope"`
	Filtered      bool   `json:"filtered"`
	InstalledPath string `json:"installedPath,omitempty"`
}

type PackageManager interface {
	Resolve(onMissing ...MissingSourceHandler) (ResolvedPaths, error)
	ResolveExtensionSources(sources []string, options ...ResolveExtensionSourcesOptions) (ResolvedPaths, error)
	Install(source string, local bool, progress ProgressCallback) (core.PackageRecord, error)
	InstallAndPersist(source string, options ...PackageManagerOperationOptions) error
	Remove(source string, local bool) error
	RemoveAndPersist(source string, options ...PackageManagerOperationOptions) (bool, error)
	Update(source string, progress ProgressCallback) error
	List(local bool) []core.PackageRecord
	ListConfiguredPackages() []ConfiguredPackage
	AddSourceToSettings(source string, local bool) bool
	RemoveSourceFromSettings(source string, local bool) bool
	SetProgressCallback(callback ProgressCallback)
	GetInstalledPath(source, scope string) string
}

type DefaultPackageManager struct {
	CWD      string
	AgentDir string
	Settings *core.SettingsManager
	Progress ProgressCallback
}

type configuredPackageEntry struct {
	record   core.PackageRecord
	setting  core.PackageSetting
	filtered bool
}

// corePackageManagerAdapter adapts *DefaultPackageManager to the
// core.PackageManager interface used by the CLI package-command dispatcher,
// bridging the variadic-options signatures to the simpler local-bool form core
// expects.
type corePackageManagerAdapter struct{ m *DefaultPackageManager }

func (a corePackageManagerAdapter) InstallAndPersist(source string, local bool) error {
	return a.m.InstallAndPersist(source, PackageManagerOperationOptions{Local: local})
}

func (a corePackageManagerAdapter) RemoveAndPersist(source string, local bool) (bool, error) {
	return a.m.RemoveAndPersist(source, PackageManagerOperationOptions{Local: local})
}

func (a corePackageManagerAdapter) Update(source string) error {
	return a.m.Update(source, nil)
}

// NewCorePackageManager is a core.PackageManagerFactory that routes CLI package
// commands through the full DefaultPackageManager.
func NewCorePackageManager(cwd, agentDir string, settings *core.SettingsManager) core.PackageManager {
	return corePackageManagerAdapter{m: NewDefaultPackageManager(cwd, agentDir, settings)}
}

func NewDefaultPackageManager(cwd, agentDir string, settings *core.SettingsManager) *DefaultPackageManager {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if agentDir == "" {
		agentDir = core.AgentDir()
	}
	if settings == nil {
		settings = core.NewSettingsManager(cwd, agentDir)
	}
	return &DefaultPackageManager{CWD: filepath.Clean(cwd), AgentDir: filepath.Clean(agentDir), Settings: settings}
}

func ParsePackageSource(source string) PackageSource {
	parsed := PackageSource{Raw: source}
	if strings.HasPrefix(source, "npm:") {
		parsed.Kind = "npm"
		parsed.Name = strings.TrimPrefix(source, "npm:")
	} else if gitSource, ok := ParseGitURL(source); ok {
		parsed.Kind = "git"
		parsed.Name = gitSource.Repo
		parsed.Ref = gitSource.Ref
		parsed.Pinned = gitSource.Pinned
	} else {
		parsed.Kind = "path"
		parsed.Name = source
	}
	if parsed.Kind != "git" && parsed.Ref == "" {
		parsed.Name, parsed.Ref, parsed.Pinned = parseSourceRef(parsed.Name)
	}
	return parsed
}

func parseSourceRef(name string) (base string, ref string, pinned bool) {
	if idx := strings.LastIndex(name, "@"); idx > 0 && !strings.Contains(name[idx:], "/") {
		return name[:idx], name[idx+1:], true
	}
	return name, "", false
}

func (m *DefaultPackageManager) Install(source string, local bool, progress ProgressCallback) (core.PackageRecord, error) {
	progress = m.progressCallback(progress)
	parsed := ParsePackageSource(source)
	var installPath string
	switch parsed.Kind {
	case "git":
		gitSource, ok := ParseGitURL(source)
		if !ok {
			return core.PackageRecord{}, fmt.Errorf("unsupported git source: %s", source)
		}
		dest := m.gitInstallPath(gitSource, local)
		emitProgress(progress, "start", "installing "+source, dest)
		// Clone into a sibling staging dir then atomically swap, so a failed
		// dependency install does not corrupt an existing checkout.
		gitRoot := filepath.Dir(dest)
		if err := os.MkdirAll(gitRoot, 0o755); err != nil {
			return core.PackageRecord{}, err
		}
		staged, err := os.MkdirTemp(gitRoot, ".install-*")
		if err != nil {
			return core.PackageRecord{}, err
		}
		defer os.RemoveAll(staged)
		args := []string{"clone"}
		if parsed.Ref == "" {
			args = append(args, "--depth", "1")
		}
		args = append(args, gitSource.Repo, staged)
		if err := runPM("", "git", args...); err != nil {
			return core.PackageRecord{}, err
		}
		if parsed.Ref != "" {
			if err := runPM(staged, "git", "checkout", parsed.Ref); err != nil {
				return core.PackageRecord{}, err
			}
		}
		if err := m.installPackageDependencies(staged); err != nil {
			return core.PackageRecord{}, err
		}
		if err := replaceInstalledPackage(dest, staged); err != nil {
			return core.PackageRecord{}, err
		}
		installPath = dest
	case "npm":
		// Install into the shared npm root so npm produces a full node_modules tree
		// with transitive deps. Mirrors installNpm -> getNpmInstallArgs in
		// package-manager.ts (~1730): `npm install <spec> --prefix <root>`.
		npmRoot := m.npmInstallRoot(local)
		if err := os.MkdirAll(npmRoot, 0o755); err != nil {
			return core.PackageRecord{}, err
		}
		// Keep the shared node_modules tree out of cloud-sync clients (Dropbox,
		// iCloud), mirroring ensureNpmProject -> markPathIgnoredByCloudSync in
		// package-manager.ts (~1865).
		MarkPathIgnoredByCloudSync(npmRoot)
		spec := parsed.Name
		if parsed.Ref != "" {
			spec += "@" + parsed.Ref
		}
		if err := m.runNpm("", m.npmInstallArgs(spec, npmRoot)...); err != nil {
			return core.PackageRecord{}, err
		}
		installPath = filepath.Join(npmRoot, "node_modules", filepath.FromSlash(parsed.Name))
	case "path":
		path := core.ResolveInCWD(m.CWD, parsed.Name)
		emitProgress(progress, "start", "installing "+source, path)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return core.PackageRecord{}, fmt.Errorf("package path not found: %s", parsed.Name)
		}
		installPath = path
	default:
		return core.PackageRecord{}, fmt.Errorf("unsupported package source: %s", source)
	}
	record := core.PackageRecord{Source: source, Path: installPath, Local: local, Pinned: parsed.Pinned}
	emitProgress(progress, "done", "installed "+source, installPath)
	return record, nil
}

// npmInstallArgs builds the managed install invocation, mirroring
// getNpmInstallArgs in package-manager.ts (~1707). Extension packages run inside
// pi and resolve pi APIs through loader aliases/virtual modules, so peer
// dependency resolution is disabled per manager so stale auto-installed
// @earendil-works/pi-* peers cannot block updates:
//   - bun:  install <spec> --cwd <root> --omit=peer
//   - pnpm: install <spec> --prefix <root> --config.auto-install-peers=false
//     --config.strict-peer-dependencies=false --config.strict-dep-builds=false
//   - npm (default): install <spec> --prefix <root> --legacy-peer-deps
func (m *DefaultPackageManager) npmInstallArgs(spec, installRoot string) []string {
	switch m.packageManagerName() {
	case "bun":
		return []string{"install", spec, "--cwd", installRoot, "--omit=peer"}
	case "pnpm":
		return []string{
			"install", spec, "--prefix", installRoot,
			"--config.auto-install-peers=false",
			"--config.strict-peer-dependencies=false",
			"--config.strict-dep-builds=false",
		}
	default:
		return []string{"install", spec, "--prefix", installRoot, "--legacy-peer-deps"}
	}
}

func (m *DefaultPackageManager) InstallAndPersist(source string, options ...PackageManagerOperationOptions) error {
	local := false
	if len(options) > 0 {
		local = options[0].Local
	}
	if _, err := m.Install(source, local, nil); err != nil {
		return err
	}
	// AddSourceToSettings stores local sources relative to the scope base dir,
	// matching installAndPersist -> addSourceToSettings in package-manager.ts.
	m.AddSourceToSettings(source, local)
	if local {
		return m.Settings.SaveProject()
	}
	return m.Settings.SaveGlobal()
}

func (m *DefaultPackageManager) Remove(source string, local bool) error {
	parsed := ParsePackageSource(source)
	if parsed.Kind == "path" {
		return nil
	}
	path := m.predictedInstalledPath(source, local)
	if !m.isWithinManagedRoot(path, local) {
		return fmt.Errorf("refusing to remove package outside package root: %s", path)
	}
	return os.RemoveAll(path)
}

// isWithinManagedRoot guards Remove against deleting outside a managed install
// root. The path may live under the npm root, the git root, or the legacy
// packages root depending on how it was installed/resolved.
func (m *DefaultPackageManager) isWithinManagedRoot(path string, local bool) bool {
	base := m.AgentDir
	if local {
		base = core.ProjectPiDir(m.CWD)
	}
	roots := []string{
		filepath.Join(base, "npm", "node_modules"),
		filepath.Join(base, "git"),
		m.legacyPackageRoot(local),
	}
	for _, root := range roots {
		if isWithinDir(root, path) {
			return true
		}
	}
	return false
}

func (m *DefaultPackageManager) RemoveAndPersist(source string, options ...PackageManagerOperationOptions) (bool, error) {
	local := false
	if len(options) > 0 {
		local = options[0].Local
	}
	if err := m.Remove(source, local); err != nil {
		return false, err
	}
	removed := m.RemoveSourceFromSettings(source, local)
	if !removed {
		return false, nil
	}
	if local {
		return true, m.Settings.SaveProject()
	}
	return true, m.Settings.SaveGlobal()
}

func (m *DefaultPackageManager) Update(source string, progress ProgressCallback) error {
	progress = m.progressCallback(progress)
	records := append(m.configuredPackageRecords(false), m.configuredPackageRecords(true)...)
	matched := false
	for _, record := range records {
		if source != "" && !m.packageSourcesMatch(record.Source, source, record.Local) {
			continue
		}
		matched = true
		if record.Pinned {
			emitProgress(progress, "skip", "skipping pinned "+record.Source, record.Path)
			continue
		}
		if _, err := os.Stat(filepath.Join(record.Path, ".git")); err == nil {
			emitProgress(progress, "start", "updating "+record.Source, record.Path)
			if err := runPM(record.Path, "git", "pull", "--ff-only"); err != nil {
				return err
			}
			if err := m.installPackageDependencies(record.Path); err != nil {
				return err
			}
			emitProgress(progress, "done", "updated "+record.Source, record.Path)
			continue
		}
		// npm packages: reinstall <name>@latest into the shared npm root so the
		// installed tree advances to the newest published version. Mirrors
		// updateConfiguredSources -> updateNpmBatch -> installNpmBatch in
		// package-manager.ts (which installs `${name}@latest`). Pinned npm
		// versions are fixed and were already skipped above.
		if parsed := ParsePackageSource(record.Source); parsed.Kind == "npm" {
			npmRoot := m.npmInstallRoot(record.Local)
			if err := os.MkdirAll(npmRoot, 0o755); err != nil {
				return err
			}
			MarkPathIgnoredByCloudSync(npmRoot)
			emitProgress(progress, "start", "updating "+record.Source, record.Path)
			if err := m.runNpm("", m.npmInstallArgs(parsed.Name+"@latest", npmRoot)...); err != nil {
				return err
			}
			emitProgress(progress, "done", "updated "+record.Source, record.Path)
		}
	}
	if source != "" && !matched {
		return fmt.Errorf("no matching package found for %s", source)
	}
	return nil
}

func (m *DefaultPackageManager) List(local bool) []core.PackageRecord {
	return m.configuredPackageRecords(local)
}

func (m *DefaultPackageManager) ListConfiguredPackages() []ConfiguredPackage {
	var packages []ConfiguredPackage
	for _, entry := range m.configuredPackageEntries(true) {
		packages = append(packages, ConfiguredPackage{Source: entry.record.Source, Scope: "project", Filtered: entry.filtered, InstalledPath: entry.record.Path})
	}
	for _, entry := range m.configuredPackageEntries(false) {
		packages = append(packages, ConfiguredPackage{Source: entry.record.Source, Scope: "user", Filtered: entry.filtered, InstalledPath: entry.record.Path})
	}
	return packages
}

func (m *DefaultPackageManager) AddSourceToSettings(source string, local bool) bool {
	source = strings.TrimSpace(source)
	if source == "" {
		return false
	}
	// Local sources are persisted relative to the scope base dir so list/update
	// from a different cwd resolve back to the same directory; managed sources are
	// stored verbatim. Mirrors normalizePackageSourceForSettings in
	// package-manager.ts.
	normalized := m.normalizeSourceForSettings(source, local)
	target := &m.Settings.Global.Packages
	if local {
		target = &m.Settings.Project.Packages
	}
	for i, record := range *target {
		if m.packageSourcesMatch(record.Source, source, local) {
			if record.Source == normalized {
				return false
			}
			(*target)[i].Source = normalized
			return true
		}
	}
	*target = append(*target, core.PackageSetting{Source: normalized})
	return true
}

func (m *DefaultPackageManager) normalizeSourceForSettings(source string, local bool) string {
	parsed := ParsePackageSource(source)
	if parsed.Kind != "path" {
		return source
	}
	resolved := core.ResolveInCWD(m.CWD, parsed.Name)
	rel, err := filepath.Rel(m.scopeBaseDir(local), resolved)
	if err != nil {
		return source
	}
	if rel == "" {
		return "."
	}
	return rel
}

// scopeBaseDir returns the directory local package source paths are stored
// relative to: project scope -> <cwd>/.pi, user scope -> agentDir. Mirrors
// getBaseDirForScope() in package-manager.ts.
func (m *DefaultPackageManager) scopeBaseDir(local bool) string {
	if local {
		return core.ProjectPiDir(m.CWD)
	}
	return m.AgentDir
}

func (m *DefaultPackageManager) RemoveSourceFromSettings(source string, local bool) bool {
	removedPackages := m.removePackageSourceFromSettings(source, local)
	removedLegacy := m.removeInstalledPackage(source, local)
	return removedPackages || removedLegacy
}

func (m *DefaultPackageManager) SetProgressCallback(callback ProgressCallback) {
	m.Progress = callback
}

func (m *DefaultPackageManager) GetInstalledPath(source, scope string) string {
	local := scope == "project"
	for _, record := range m.configuredPackageRecords(local) {
		if record.Source == source {
			return record.Path
		}
	}
	path := m.predictedInstalledPath(source, local)
	if fileExistsLocal(path) {
		return path
	}
	return ""
}

func (m *DefaultPackageManager) progressCallback(progress ProgressCallback) ProgressCallback {
	if progress != nil {
		return progress
	}
	return m.Progress
}

func (m *DefaultPackageManager) predictedInstalledPath(source string, local bool) string {
	parsed := ParsePackageSource(source)
	switch parsed.Kind {
	case "path":
		// Stored local sources are relative to the scope base dir, so resolve from
		// there to avoid drift when listing/updating from a different cwd. Mirrors
		// resolvePathFromBase(parsed.path, getBaseDirForScope(scope)).
		return core.ResolveInCWD(m.scopeBaseDir(local), parsed.Name)
	case "npm":
		// npm packages live in a shared node_modules tree, keyed by package name
		// (scoped names keep their @scope/ segment). Mirrors getManagedNpmInstallPath
		// in package-manager.ts (~1924).
		path := filepath.Join(m.npmInstallRoot(local), "node_modules", filepath.FromSlash(parsed.Name))
		if !local && !fileExistsLocal(path) {
			if legacy := m.legacyPackagePath(source, local); legacy != "" {
				return legacy
			}
		}
		return path
	case "git":
		// git packages are stored under git/<host>/<owner>/<repo>. Mirrors
		// getGitInstallPath in package-manager.ts (~1951).
		if gitSource, ok := ParseGitURL(source); ok {
			path := m.gitInstallPath(gitSource, local)
			if !fileExistsLocal(path) {
				if legacy := m.legacyPackagePath(source, local); legacy != "" {
					return legacy
				}
			}
			return path
		}
	}
	// Unparseable git/other: fall back to the legacy sanitized layout.
	return filepath.Join(m.legacyPackageRoot(local), sanitizePackageSource(source))
}

// npmInstallRoot returns the shared npm root: <ProjectPiDir>/npm (project) or
// <agentDir>/npm (user). Mirrors getNpmInstallRoot in package-manager.ts (~1884).
func (m *DefaultPackageManager) npmInstallRoot(local bool) string {
	if local {
		return filepath.Join(core.ProjectPiDir(m.CWD), "npm")
	}
	return filepath.Join(m.AgentDir, "npm")
}

// gitInstallPath returns <base>/git/<host>/<owner>/<repo>; base is <ProjectPiDir>
// (project) or <agentDir> (user). Mirrors getGitInstallPath in
// package-manager.ts (~1951). gitSource.Path is the "owner/repo" segment.
func (m *DefaultPackageManager) gitInstallPath(gitSource GitSource, local bool) string {
	base := m.AgentDir
	if local {
		base = core.ProjectPiDir(m.CWD)
	}
	return filepath.Join(base, "git", gitSource.Host, filepath.FromSlash(gitSource.Path))
}

// legacyPackageRoot is the pre-TS-layout Go install root (<base>/packages).
func (m *DefaultPackageManager) legacyPackageRoot(local bool) string {
	if local {
		return filepath.Join(core.ProjectPiDir(m.CWD), "packages")
	}
	return filepath.Join(m.AgentDir, "packages")
}

// legacyPackagePath probes the older Go install location so packages installed
// by a previous build still resolve when the new-layout path is absent.
func (m *DefaultPackageManager) legacyPackagePath(source string, local bool) string {
	legacy := filepath.Join(m.legacyPackageRoot(local), sanitizePackageSource(source))
	if fileExistsLocal(legacy) {
		return legacy
	}
	return ""
}

func replaceInstalledPackage(dest, staged string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	var backup string
	if _, err := os.Stat(dest); err == nil {
		tempBackup, err := os.MkdirTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".rollback-*")
		if err != nil {
			return err
		}
		_ = os.Remove(tempBackup)
		backup = tempBackup
		if err := os.Rename(dest, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staged, dest); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, dest); restoreErr != nil {
				return fmt.Errorf("%w (rollback restore failed: %v)", err, restoreErr)
			}
		}
		return err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// npmCommand returns the configured package-manager command and its leading
// args (e.g. ["mise", "exec", "--", "npm"] -> "mise", ["exec","--","npm"]),
// falling back to plain "npm". Mirrors getNpmCommand() in
// src/core/package-manager.ts.
func (m *DefaultPackageManager) npmCommand() (string, []string) {
	if m.Settings != nil {
		if configured := m.Settings.NPMCommand(); len(configured) > 0 && configured[0] != "" {
			return configured[0], append([]string(nil), configured[1:]...)
		}
	}
	return "npm", nil
}

func (m *DefaultPackageManager) hasCustomNpmCommand() bool {
	return m.Settings != nil && len(m.Settings.NPMCommand()) > 0 && m.Settings.NPMCommand()[0] != ""
}

// packageManagerName derives the underlying package-manager binary name from the
// configured npm command, mirroring getPackageManagerName() in
// src/core/package-manager.ts: it takes the token after the LAST "--" separator
// (so "mise exec -- pnpm" resolves to "pnpm"), else the command itself, then
// basenames it and strips a trailing .cmd/.exe (Windows shims).
func (m *DefaultPackageManager) packageManagerName() string {
	command, args := m.npmCommand()
	parts := append([]string{command}, args...)
	sep := -1
	for i, p := range parts {
		if p == "--" {
			sep = i
		}
	}
	name := command
	if sep >= 0 && sep+1 < len(parts) {
		name = parts[sep+1]
	}
	if name == "" {
		return ""
	}
	name = filepath.Base(name)
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".exe") {
		name = name[:len(name)-4]
	}
	return name
}

// runNpm runs the configured package manager with the given subcommand args,
// honoring any prefix such as "mise exec -- npm".
func (m *DefaultPackageManager) runNpm(dir string, args ...string) error {
	command, prefix := m.npmCommand()
	return runPM(dir, command, append(prefix, args...)...)
}

func (m *DefaultPackageManager) installPackageDependencies(dir string) error {
	needsInstall, err := packageNeedsInstall(dir)
	if err != nil || !needsInstall {
		return err
	}
	return m.runNpm(dir, m.gitDependencyInstallArgs()...)
}

// gitDependencyInstallArgs mirrors getGitDependencyInstallArgs in
// src/core/package-manager.ts exactly: a custom package manager
// (pnpm/bun/mise-wrapped) gets a bare "install"; the default npm path gets only
// "install --omit=dev". TS deliberately omits --ignore-scripts/--no-audit/
// --no-fund so a git dependency's postinstall and prepare scripts (e.g. building
// the package) still run; suppressing them here diverged from TS and could leave
// a git dep unbuilt.
func (m *DefaultPackageManager) gitDependencyInstallArgs() []string {
	if m.hasCustomNpmCommand() {
		return []string{"install"}
	}
	return []string{"install", "--omit=dev"}
}

func packageNeedsInstall(dir string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var pkg struct {
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
		PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return false, err
	}
	return len(pkg.Dependencies) > 0 || len(pkg.OptionalDependencies) > 0 || len(pkg.PeerDependencies) > 0, nil
}

func isWithinDir(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}

func (m *DefaultPackageManager) configuredPackageRecords(local bool) []core.PackageRecord {
	entries := m.configuredPackageEntries(local)
	records := make([]core.PackageRecord, 0, len(entries))
	for _, entry := range entries {
		records = append(records, entry.record)
	}
	return records
}

func (m *DefaultPackageManager) configuredPackageEntries(local bool) []configuredPackageEntry {
	var entries []configuredPackageEntry
	seen := map[string]bool{}
	for _, source := range m.Settings.PackageSources(local) {
		if strings.TrimSpace(source.Source) == "" || seen[source.Source] {
			continue
		}
		seen[source.Source] = true
		entries = append(entries, configuredPackageEntry{
			record:   m.packageRecordFromSource(source.Source, local),
			setting:  source,
			filtered: packageSettingHasFilters(source),
		})
	}
	for _, record := range m.Settings.InstalledPackages(local) {
		if strings.TrimSpace(record.Source) == "" || seen[record.Source] {
			continue
		}
		seen[record.Source] = true
		if record.Path == "" {
			record.Path = m.predictedInstalledPath(record.Source, local)
		}
		entries = append(entries, configuredPackageEntry{record: record})
	}
	return entries
}

func (m *DefaultPackageManager) removePackageSourceFromSettings(source string, local bool) bool {
	target := &m.Settings.Global.Packages
	if local {
		target = &m.Settings.Project.Packages
	}
	for i, record := range *target {
		if m.packageSourcesMatch(record.Source, source, local) {
			*target = append((*target)[:i], (*target)[i+1:]...)
			return true
		}
	}
	return false
}

func (m *DefaultPackageManager) removeInstalledPackage(source string, local bool) bool {
	target := &m.Settings.Global.InstalledPackages
	if local {
		target = &m.Settings.Project.InstalledPackages
	}
	for i, record := range *target {
		if m.packageSourcesMatch(record.Source, source, local) {
			*target = append((*target)[:i], (*target)[i+1:]...)
			return true
		}
	}
	return false
}

func (m *DefaultPackageManager) packageSourcesMatch(existing, input string, local bool) bool {
	return m.packageIdentity(existing, local, true) == m.packageIdentity(input, local, false)
}

func (m *DefaultPackageManager) packageIdentity(source string, local bool, settingsSource bool) string {
	parsed := ParsePackageSource(source)
	switch parsed.Kind {
	case "npm":
		return "npm:" + parsed.Name
	case "git":
		if gitSource, ok := ParseGitURL(source); ok {
			return "git:" + gitSource.Host + "/" + gitSource.Path
		}
		return "git:" + strings.TrimSuffix(parsed.Name, ".git")
	default:
		baseDir := m.CWD
		if settingsSource {
			baseDir = m.AgentDir
			if local {
				baseDir = core.ProjectPiDir(m.CWD)
			}
		}
		return "local:" + core.ResolveInCWD(baseDir, parsed.Name)
	}
}

func (m *DefaultPackageManager) packageRecordFromSource(source string, local bool) core.PackageRecord {
	return core.PackageRecord{
		Source: source,
		Path:   m.predictedInstalledPath(source, local),
		Local:  local,
		Pinned: ParsePackageSource(source).Pinned,
	}
}

func packageSettingHasFilters(source core.PackageSetting) bool {
	return source.Extensions != nil || source.Skills != nil || source.Prompts != nil || source.Themes != nil
}

func emitProgress(cb ProgressCallback, typ, message, path string) {
	if cb != nil {
		cb(ProgressEvent{Type: typ, Message: message, Path: path})
	}
}

func runPM(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func sanitizePackageSource(source string) string {
	replacer := strings.NewReplacer("/", "-", ":", "-", "@", "-", "\\", "-")
	name := strings.Trim(replacer.Replace(source), "-")
	if name == "" {
		return "package"
	}
	return name
}
