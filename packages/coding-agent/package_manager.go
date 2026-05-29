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
	root := filepath.Join(m.AgentDir, "packages")
	if local {
		root = filepath.Join(core.ProjectPiDir(m.CWD), "packages")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return core.PackageRecord{}, err
	}
	dest := filepath.Join(root, sanitizePackageSource(source))
	emitProgress(progress, "start", "installing "+source, dest)
	installPath := dest
	switch parsed.Kind {
	case "git":
		staged, err := os.MkdirTemp(root, ".install-"+sanitizePackageSource(source)+"-*")
		if err != nil {
			return core.PackageRecord{}, err
		}
		defer os.RemoveAll(staged)
		args := []string{"clone"}
		if parsed.Ref == "" {
			args = append(args, "--depth", "1")
		}
		args = append(args, parsed.Name, staged)
		if err := runPM("", "git", args...); err != nil {
			return core.PackageRecord{}, err
		}
		if parsed.Ref != "" {
			if err := runPM(staged, "git", "checkout", parsed.Ref); err != nil {
				return core.PackageRecord{}, err
			}
		}
		if err := installPackageDependencies(staged); err != nil {
			return core.PackageRecord{}, err
		}
		if err := replaceInstalledPackage(dest, staged); err != nil {
			return core.PackageRecord{}, err
		}
	case "npm":
		staged, err := os.MkdirTemp(root, ".install-"+sanitizePackageSource(source)+"-*")
		if err != nil {
			return core.PackageRecord{}, err
		}
		defer os.RemoveAll(staged)
		pkg := parsed.Name
		if parsed.Ref != "" {
			pkg += "@" + parsed.Ref
		}
		if err := runPM(staged, "npm", "pack", pkg); err != nil {
			return core.PackageRecord{}, err
		}
		matches, _ := filepath.Glob(filepath.Join(staged, "*.tgz"))
		if len(matches) == 0 {
			return core.PackageRecord{}, fmt.Errorf("npm pack produced no tarball for %s", pkg)
		}
		if err := runPM(staged, "tar", "-xzf", matches[0], "--strip-components=1"); err != nil {
			return core.PackageRecord{}, err
		}
		for _, match := range matches {
			_ = os.Remove(match)
		}
		if err := installPackageDependencies(staged); err != nil {
			return core.PackageRecord{}, err
		}
		if err := replaceInstalledPackage(dest, staged); err != nil {
			return core.PackageRecord{}, err
		}
	case "path":
		path := core.ResolveInCWD(m.CWD, parsed.Name)
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

func (m *DefaultPackageManager) InstallAndPersist(source string, options ...PackageManagerOperationOptions) error {
	local := false
	if len(options) > 0 {
		local = options[0].Local
	}
	if _, err := m.Install(source, local, nil); err != nil {
		return err
	}
	m.Settings.AddPackageSource(source, local)
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
	root := m.packageRoot(local)
	if !isWithinDir(root, path) {
		return fmt.Errorf("refusing to remove package outside package root: %s", path)
	}
	return os.RemoveAll(path)
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
			if err := installPackageDependencies(record.Path); err != nil {
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
	target := &m.Settings.Global.Packages
	if local {
		target = &m.Settings.Project.Packages
	}
	for i, record := range *target {
		if m.packageSourcesMatch(record.Source, source, local) {
			if record.Source == source {
				return false
			}
			(*target)[i].Source = source
			return true
		}
	}
	*target = append(*target, core.PackageSetting{Source: source})
	return true
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
	if parsed.Kind == "path" {
		return core.ResolveInCWD(m.CWD, parsed.Name)
	}
	return filepath.Join(m.packageRoot(local), sanitizePackageSource(source))
}

func (m *DefaultPackageManager) packageRoot(local bool) string {
	if local {
		return filepath.Join(core.ProjectPiDir(m.CWD), "packages")
	}
	return filepath.Join(m.AgentDir, "packages")
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

func installPackageDependencies(dir string) error {
	needsInstall, err := packageNeedsInstall(dir)
	if err != nil || !needsInstall {
		return err
	}
	return runPM(dir, "npm", "install", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund")
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
