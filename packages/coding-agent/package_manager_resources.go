package codingagent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

func (m *DefaultPackageManager) Resolve(onMissing ...MissingSourceHandler) (ResolvedPaths, error) {
	accumulator := newResourceAccumulator()
	if err := m.resolvePackageEntries(m.configuredPackageEntries(true), "project", &accumulator, firstMissingHandler(onMissing)); err != nil {
		return ResolvedPaths{}, err
	}
	if err := m.resolvePackageEntries(m.configuredPackageEntries(false), "user", &accumulator, firstMissingHandler(onMissing)); err != nil {
		return ResolvedPaths{}, err
	}
	m.resolveLocalResourceEntries(&accumulator)
	m.addAutoDiscoveredResources(&accumulator)
	return accumulator.resolved(), nil
}

type configResourceItem struct {
	ResourceType string
	Resource     ResolvedResource
}

func handleConfigCommand(args []string, in io.Reader, out io.Writer) (bool, error) {
	if len(args) == 0 || args[0] != "config" {
		return false, nil
	}
	listOnly := false
	for _, arg := range args[1:] {
		switch arg {
		case "-h", "--help":
			fmt.Fprintln(out, "Usage: pi config [--list]")
			fmt.Fprintln(out, "List resources and, in a terminal, toggle them by number.")
			return true, nil
		case "--list":
			listOnly = true
		default:
			return true, fmt.Errorf("unknown option %s for config", arg)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return true, err
	}
	cwd, _ = core.AbsPath(cwd)
	agentDir := core.AgentDir()
	settings := core.NewSettingsManager(cwd, agentDir)
	resolved, err := NewDefaultPackageManager(cwd, agentDir, settings).Resolve()
	if err != nil {
		return true, err
	}
	items := flattenConfigResources(resolved)
	printConfigResources(out, items)
	if listOnly || !readerIsTerminal(in) || len(items) == 0 {
		return true, nil
	}
	indexes, err := cli.PromptConfigSelection(in, out, len(items))
	if err != nil {
		return true, err
	}
	var saveGlobal, saveProject bool
	for _, index := range indexes {
		scope, err := toggleConfigResource(settings, items[index-1], cwd, agentDir)
		if err != nil {
			return true, err
		}
		saveGlobal = saveGlobal || scope == "user"
		saveProject = saveProject || scope == "project"
	}
	if saveGlobal {
		if err := settings.SaveGlobal(); err != nil {
			return true, err
		}
	}
	if saveProject {
		if err := settings.SaveProject(); err != nil {
			return true, err
		}
	}
	if len(indexes) > 0 {
		fmt.Fprintf(out, "Updated %d resource setting(s).\n", len(indexes))
	}
	return true, nil
}

func flattenConfigResources(resolved ResolvedPaths) []configResourceItem {
	var items []configResourceItem
	add := func(resourceType string, resources []ResolvedResource) {
		for _, resource := range resources {
			items = append(items, configResourceItem{ResourceType: resourceType, Resource: resource})
		}
	}
	add("extensions", resolved.Extensions)
	add("skills", resolved.Skills)
	add("prompts", resolved.Prompts)
	add("themes", resolved.Themes)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.Resource.Metadata.Origin != right.Resource.Metadata.Origin {
			return left.Resource.Metadata.Origin < right.Resource.Metadata.Origin
		}
		if left.Resource.Metadata.Scope != right.Resource.Metadata.Scope {
			return left.Resource.Metadata.Scope < right.Resource.Metadata.Scope
		}
		if left.Resource.Metadata.Source != right.Resource.Metadata.Source {
			return left.Resource.Metadata.Source < right.Resource.Metadata.Source
		}
		if left.ResourceType != right.ResourceType {
			return left.ResourceType < right.ResourceType
		}
		return left.Resource.Path < right.Resource.Path
	})
	return items
}

func printConfigResources(out io.Writer, items []configResourceItem) {
	fmt.Fprintln(out, "Resource Configuration")
	if len(items) == 0 {
		fmt.Fprintln(out, "  No resources found.")
		return
	}
	for i, item := range items {
		state := " "
		if item.Resource.Enabled {
			state = "x"
		}
		meta := item.Resource.Metadata
		fmt.Fprintf(out, "%3d. [%s] %-10s %-7s %-9s %s\n", i+1, state, item.ResourceType, meta.Scope, meta.Origin, configDisplayPath(item.Resource))
	}
}

func configDisplayPath(resource ResolvedResource) string {
	base := resource.Metadata.BaseDir
	if base != "" {
		if rel, err := filepath.Rel(base, resource.Path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(resource.Path)
}

func toggleConfigResource(settings *core.SettingsManager, item configResourceItem, cwd, agentDir string) (string, error) {
	meta := item.Resource.Metadata
	if meta.Scope != "user" && meta.Scope != "project" {
		return "", fmt.Errorf("cannot persist %s resource %s", meta.Scope, item.Resource.Path)
	}
	target := &settings.Global
	if meta.Scope == "project" {
		target = &settings.Project
	}
	pattern := configResourcePattern(item.Resource, cwd, agentDir)
	enabled := !item.Resource.Enabled
	if meta.Origin == "package" {
		index := packageSettingIndex(target.Packages, meta.Source)
		if index < 0 {
			target.Packages = append(target.Packages, core.PackageSetting{Source: meta.Source})
			index = len(target.Packages) - 1
		}
		setPackageSettingPatterns(&target.Packages[index], item.ResourceType, withResourceOverride(packageSettingPatterns(target.Packages[index], item.ResourceType), pattern, enabled))
	} else {
		setTopLevelResourcePatterns(target, item.ResourceType, withResourceOverride(topLevelResourcePatterns(target, item.ResourceType), pattern, enabled))
	}
	return meta.Scope, nil
}

func configResourcePattern(resource ResolvedResource, cwd, agentDir string) string {
	base := resource.Metadata.BaseDir
	if base == "" {
		if resource.Metadata.Scope == "project" {
			base = core.ProjectPiDir(cwd)
		} else {
			base = agentDir
		}
	}
	if rel, err := filepath.Rel(base, resource.Path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(resource.Path)
}

func withResourceOverride(entries []string, pattern string, enabled bool) []string {
	normalized := normalizeExactPattern(pattern)
	out := entries[:0]
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		stripped := strings.TrimLeft(trimmed, "!+-")
		if normalizeExactPattern(stripped) == normalized {
			continue
		}
		out = append(out, trimmed)
	}
	prefix := "-"
	if enabled {
		prefix = "+"
	}
	return append(out, prefix+pattern)
}

func packageSettingIndex(packages []core.PackageSetting, source string) int {
	for i, pkg := range packages {
		if pkg.Source == source {
			return i
		}
	}
	return -1
}

func topLevelResourcePatterns(settings *core.Settings, resourceType string) []string {
	switch resourceType {
	case "extensions":
		return settings.Extensions
	case "skills":
		return settings.Skills
	case "prompts":
		return settings.Prompts
	case "themes":
		return settings.Themes
	default:
		return nil
	}
}

func setTopLevelResourcePatterns(settings *core.Settings, resourceType string, patterns []string) {
	switch resourceType {
	case "extensions":
		settings.Extensions = patterns
	case "skills":
		settings.Skills = patterns
	case "prompts":
		settings.Prompts = patterns
	case "themes":
		settings.Themes = patterns
	}
}

func packageSettingPatterns(setting core.PackageSetting, resourceType string) []string {
	switch resourceType {
	case "extensions":
		return setting.Extensions
	case "skills":
		return setting.Skills
	case "prompts":
		return setting.Prompts
	case "themes":
		return setting.Themes
	default:
		return nil
	}
}

func setPackageSettingPatterns(setting *core.PackageSetting, resourceType string, patterns []string) {
	switch resourceType {
	case "extensions":
		setting.Extensions = patterns
	case "skills":
		setting.Skills = patterns
	case "prompts":
		setting.Prompts = patterns
	case "themes":
		setting.Themes = patterns
	}
}

func readerIsTerminal(in io.Reader) bool {
	file, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func (m *DefaultPackageManager) ResolveExtensionSources(sources []string, options ...ResolveExtensionSourcesOptions) (ResolvedPaths, error) {
	scope := "user"
	if len(options) > 0 {
		if options[0].Temporary {
			scope = "temporary"
		} else if options[0].Local {
			scope = "project"
		}
	}
	accumulator := newResourceAccumulator()
	for _, source := range sources {
		if strings.TrimSpace(source) == "" {
			continue
		}
		if !IsLocalPath(source) {
			record, ok := m.findInstalledRecord(source, scope)
			if !ok {
				continue
			}
			metadata := PathMetadata{Source: source, Scope: scope, Origin: "package", BaseDir: record.Path}
			collectPackageResources(record.Path, &accumulator, metadata)
			continue
		}
		baseDir := m.AgentDir
		switch scope {
		case "project":
			baseDir = core.ProjectPiDir(m.CWD)
		case "temporary":
			baseDir = m.CWD
		}
		path := ResolveInputPath(source, baseDir, PathInputOptions{Trim: true, NormalizeUnicodeSpaces: true})
		if !fileExistsLocal(path) {
			continue
		}
		metadata := PathMetadata{Source: source, Scope: scope, Origin: "package", BaseDir: resourceBaseDir(path)}
		if collectPackageResources(path, &accumulator, metadata) {
			continue
		}
		accumulator.add("extensions", path, metadata, true)
	}
	return accumulator.resolved(), nil
}

func (m *DefaultPackageManager) resolvePackageEntries(entries []configuredPackageEntry, scope string, accumulator *resourceAccumulator, onMissing MissingSourceHandler) error {
	for _, entry := range entries {
		record := entry.record
		if record.Path == "" {
			continue
		}
		path := ResolveInputPath(record.Path, m.CWD, PathInputOptions{Trim: true, NormalizeUnicodeSpaces: true})
		if !fileExistsLocal(path) {
			if onMissing == nil {
				continue
			}
			action, err := onMissing(record.Source)
			if err != nil {
				return err
			}
			switch action {
			case MissingSourceError:
				return fmt.Errorf("missing source: %s", record.Source)
			case MissingSourceInstall:
				// Offline mode never reaches out to install a missing managed
				// source; skip it instead, mirroring resolvePackageSources'
				// installMissing() guard (`if (isOfflineModeEnabled()) return false`)
				// in package-manager.ts (~1225).
				if offlineModeEnabled() {
					continue
				}
				installed, err := m.Install(record.Source, scope == "project", nil)
				if err != nil {
					return err
				}
				path = installed.Path
			default:
				continue
			}
		}
		metadata := PathMetadata{Source: record.Source, Scope: scope, Origin: "package", BaseDir: resourceBaseDir(path)}
		if entry.filtered {
			collectPackageResourcesWithFilter(path, accumulator, metadata, entry.setting)
		} else {
			collectPackageResources(path, accumulator, metadata)
		}
	}
	return nil
}

func (m *DefaultPackageManager) findInstalledRecord(source, scope string) (core.PackageRecord, bool) {
	candidates := append([]core.PackageRecord(nil), m.configuredPackageRecords(true)...)
	candidates = append(candidates, m.configuredPackageRecords(false)...)
	for _, record := range candidates {
		if record.Source != source {
			continue
		}
		if scope == "project" && !record.Local {
			continue
		}
		if scope == "user" && record.Local {
			continue
		}
		return record, true
	}
	return core.PackageRecord{}, false
}

func (m *DefaultPackageManager) addAutoDiscoveredResources(accumulator *resourceAccumulator) {
	projectBaseDir := core.ProjectPiDir(m.CWD)
	userBaseDir := m.AgentDir
	projectMetadata := PathMetadata{Source: "auto", Scope: "project", Origin: "top-level", BaseDir: projectBaseDir}
	userMetadata := PathMetadata{Source: "auto", Scope: "user", Origin: "top-level", BaseDir: userBaseDir}

	projectExtensions := resourceOverrideEntries(m.Settings.Project.Extensions, m.Settings.Project.DisabledExtensions)
	projectPrompts := resourceOverrideEntries(m.Settings.Project.Prompts, m.Settings.Project.DisabledPromptTemplates)
	projectThemes := resourceOverrideEntries(m.Settings.Project.Themes, m.Settings.Project.DisabledThemes)
	projectSkills := resourceOverrideEntries(m.Settings.Project.Skills, m.Settings.Project.DisabledSkills)
	userExtensions := resourceOverrideEntries(m.Settings.Global.Extensions, m.Settings.Global.DisabledExtensions)
	userPrompts := resourceOverrideEntries(m.Settings.Global.Prompts, m.Settings.Global.DisabledPromptTemplates)
	userThemes := resourceOverrideEntries(m.Settings.Global.Themes, m.Settings.Global.DisabledThemes)
	userSkills := resourceOverrideEntries(m.Settings.Global.Skills, m.Settings.Global.DisabledSkills)

	m.addAutoResources(accumulator, "extensions", collectExtensionFiles(filepath.Join(projectBaseDir, "extensions")), projectMetadata, projectExtensions, projectBaseDir)
	m.addAutoResources(accumulator, "prompts", collectResourceFiles(filepath.Join(projectBaseDir, "prompts"), "prompts"), projectMetadata, projectPrompts, projectBaseDir)
	m.addAutoResources(accumulator, "themes", collectResourceFiles(filepath.Join(projectBaseDir, "themes"), "themes"), projectMetadata, projectThemes, projectBaseDir)
	m.addAutoResources(accumulator, "skills", collectSkillFiles(filepath.Join(projectBaseDir, "skills")), projectMetadata, projectSkills, projectBaseDir)

	for _, dir := range contextAncestorDirs(m.CWD) {
		piSkillsDir := filepath.Join(dir, ConfigDirName, "skills")
		piBaseDir := filepath.Join(dir, ConfigDirName)
		piMetadata := PathMetadata{Source: "auto", Scope: "project", Origin: "top-level", BaseDir: piBaseDir}
		m.addAutoResources(accumulator, "skills", collectSkillFiles(piSkillsDir), piMetadata, projectSkills, piBaseDir)

		agentsSkillsDir := filepath.Join(dir, ".agents", "skills")
		agentsBaseDir := filepath.Join(dir, ".agents")
		agentsMetadata := PathMetadata{Source: "auto", Scope: "project", Origin: "top-level", BaseDir: agentsBaseDir}
		m.addAutoResources(accumulator, "skills", collectSkillFiles(agentsSkillsDir), agentsMetadata, projectSkills, agentsBaseDir)
	}

	m.addAutoResources(accumulator, "extensions", collectExtensionFiles(filepath.Join(userBaseDir, "extensions")), userMetadata, userExtensions, userBaseDir)
	m.addAutoResources(accumulator, "prompts", collectResourceFiles(filepath.Join(userBaseDir, "prompts"), "prompts"), userMetadata, userPrompts, userBaseDir)
	m.addAutoResources(accumulator, "themes", collectResourceFiles(filepath.Join(userBaseDir, "themes"), "themes"), userMetadata, userThemes, userBaseDir)
	m.addAutoResources(accumulator, "skills", collectSkillFiles(filepath.Join(userBaseDir, "skills")), userMetadata, userSkills, userBaseDir)

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		agentsBaseDir := filepath.Join(home, ".agents")
		agentsMetadata := PathMetadata{Source: "auto", Scope: "user", Origin: "top-level", BaseDir: agentsBaseDir}
		m.addAutoResources(accumulator, "skills", collectSkillFiles(filepath.Join(agentsBaseDir, "skills")), agentsMetadata, userSkills, agentsBaseDir)
	}
}

func (m *DefaultPackageManager) addAutoResources(accumulator *resourceAccumulator, resourceType string, paths []string, metadata PathMetadata, overrides []string, baseDir string) {
	patterns := overridePatterns(overrides)
	for _, path := range paths {
		accumulator.add(resourceType, path, metadata, applyPatterns([]string{path}, patterns, baseDir)[path])
	}
}

func (m *DefaultPackageManager) resolveLocalResourceEntries(accumulator *resourceAccumulator) {
	projectBaseDir := core.ProjectPiDir(m.CWD)
	m.resolveLocalResourceType(accumulator, "extensions", m.Settings.Project.Extensions, PathMetadata{Source: "local", Scope: "project", Origin: "top-level", BaseDir: projectBaseDir}, projectBaseDir)
	m.resolveLocalResourceType(accumulator, "skills", m.Settings.Project.Skills, PathMetadata{Source: "local", Scope: "project", Origin: "top-level", BaseDir: projectBaseDir}, projectBaseDir)
	m.resolveLocalResourceType(accumulator, "prompts", m.Settings.Project.Prompts, PathMetadata{Source: "local", Scope: "project", Origin: "top-level", BaseDir: projectBaseDir}, projectBaseDir)
	m.resolveLocalResourceType(accumulator, "themes", m.Settings.Project.Themes, PathMetadata{Source: "local", Scope: "project", Origin: "top-level", BaseDir: projectBaseDir}, projectBaseDir)

	m.resolveLocalResourceType(accumulator, "extensions", m.Settings.Global.Extensions, PathMetadata{Source: "local", Scope: "user", Origin: "top-level", BaseDir: m.AgentDir}, m.AgentDir)
	m.resolveLocalResourceType(accumulator, "skills", m.Settings.Global.Skills, PathMetadata{Source: "local", Scope: "user", Origin: "top-level", BaseDir: m.AgentDir}, m.AgentDir)
	m.resolveLocalResourceType(accumulator, "prompts", m.Settings.Global.Prompts, PathMetadata{Source: "local", Scope: "user", Origin: "top-level", BaseDir: m.AgentDir}, m.AgentDir)
	m.resolveLocalResourceType(accumulator, "themes", m.Settings.Global.Themes, PathMetadata{Source: "local", Scope: "user", Origin: "top-level", BaseDir: m.AgentDir}, m.AgentDir)
}

func (m *DefaultPackageManager) resolveLocalResourceType(accumulator *resourceAccumulator, resourceType string, entries []string, metadata PathMetadata, baseDir string) {
	plain, patterns := splitLocalResourceEntries(entries)
	if len(plain) == 0 {
		return
	}
	var resolved []string
	for _, entry := range plain {
		resolved = append(resolved, ResolveInputPath(entry, baseDir, PathInputOptions{Trim: true, NormalizeUnicodeSpaces: true}))
	}
	files := collectFilesFromPaths(resolved, resourceType)
	enabled := applyPatterns(files, patterns, baseDir)
	for _, path := range files {
		accumulator.add(resourceType, path, metadata, enabled[path])
	}
}

func splitLocalResourceEntries(entries []string) (plain []string, patterns []string) {
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if isOverridePattern(entry) {
			patterns = append(patterns, entry)
		} else {
			plain = append(plain, entry)
		}
	}
	return plain, patterns
}

func resourceOverrideEntries(entries, disabled []string) []string {
	out := append([]string(nil), entries...)
	for _, pattern := range disabled {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if isOverridePattern(pattern) {
			out = append(out, pattern)
		} else {
			out = append(out, "-"+pattern)
		}
	}
	return out
}

type resourceAccumulator struct {
	extensions map[string]resolvedResourceState
	skills     map[string]resolvedResourceState
	prompts    map[string]resolvedResourceState
	themes     map[string]resolvedResourceState
}

type resolvedResourceState struct {
	metadata PathMetadata
	enabled  bool
}

type piManifest struct {
	Extensions []string `json:"extensions"`
	Skills     []string `json:"skills"`
	Prompts    []string `json:"prompts"`
	Themes     []string `json:"themes"`
}

type piPackageJSON struct {
	Pi *piManifest `json:"pi"`
}

func newResourceAccumulator() resourceAccumulator {
	return resourceAccumulator{
		extensions: map[string]resolvedResourceState{},
		skills:     map[string]resolvedResourceState{},
		prompts:    map[string]resolvedResourceState{},
		themes:     map[string]resolvedResourceState{},
	}
}

func (a *resourceAccumulator) add(resourceType, path string, metadata PathMetadata, enabled bool) {
	if strings.TrimSpace(path) == "" {
		return
	}
	path = filepath.Clean(path)
	target := a.target(resourceType)
	if target == nil {
		return
	}
	if _, exists := target[path]; exists {
		return
	}
	target[path] = resolvedResourceState{metadata: metadata, enabled: enabled}
}

func (a *resourceAccumulator) target(resourceType string) map[string]resolvedResourceState {
	switch resourceType {
	case "extensions":
		return a.extensions
	case "skills":
		return a.skills
	case "prompts":
		return a.prompts
	case "themes":
		return a.themes
	default:
		return nil
	}
}

func (a *resourceAccumulator) resolved() ResolvedPaths {
	return ResolvedPaths{
		Extensions: mapResolvedResources(a.extensions),
		Skills:     mapResolvedResources(a.skills),
		Prompts:    mapResolvedResources(a.prompts),
		Themes:     mapResolvedResources(a.themes),
	}
}

func mapResolvedResources(input map[string]resolvedResourceState) []ResolvedResource {
	out := make([]ResolvedResource, 0, len(input))
	for path, state := range input {
		out = append(out, ResolvedResource{Path: path, Enabled: state.enabled, Metadata: state.metadata})
	}
	sort.Slice(out, func(i, j int) bool {
		leftRank := resourcePrecedenceRank(out[i].Metadata)
		rightRank := resourcePrecedenceRank(out[j].Metadata)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return out[i].Path < out[j].Path
	})
	seen := map[string]bool{}
	filtered := out[:0]
	for _, resource := range out {
		canonical := CanonicalizePath(resource.Path)
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		filtered = append(filtered, resource)
	}
	return filtered
}

func resourcePrecedenceRank(metadata PathMetadata) int {
	if metadata.Origin == "package" {
		return 4
	}
	base := 2
	if metadata.Scope == "project" {
		base = 0
	}
	if metadata.Source == "local" {
		return base
	}
	return base + 1
}

func collectPackageResources(root string, accumulator *resourceAccumulator, metadata PathMetadata) bool {
	info, err := os.Stat(root)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		if matchesResourceType(root, "extensions") {
			accumulator.add("extensions", root, metadata, true)
			return true
		}
		return false
	}
	if manifest, ok := readPiManifest(root); ok {
		addManifestEntries(manifest.Extensions, root, "extensions", accumulator, metadata)
		addManifestEntries(manifest.Skills, root, "skills", accumulator, metadata)
		addManifestEntries(manifest.Prompts, root, "prompts", accumulator, metadata)
		addManifestEntries(manifest.Themes, root, "themes", accumulator, metadata)
		return true
	}
	hasAnyDir := false
	for _, resourceType := range []string{"extensions", "skills", "prompts", "themes"} {
		dir := filepath.Join(root, resourceType)
		if !fileExistsLocal(dir) {
			continue
		}
		hasAnyDir = true
		for _, path := range collectResourceFiles(dir, resourceType) {
			accumulator.add(resourceType, path, metadata, true)
		}
	}
	return hasAnyDir
}

func collectPackageResourcesWithFilter(root string, accumulator *resourceAccumulator, metadata PathMetadata, filter core.PackageSetting) bool {
	info, err := os.Stat(root)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		if patterns, ok := packageFilterPatterns(filter, "extensions"); ok && matchesResourceType(root, "extensions") {
			enabled := len(patterns) > 0 && applyPatterns([]string{root}, patterns, filepath.Dir(root))[root]
			accumulator.add("extensions", root, metadata, enabled)
			return true
		}
		return collectPackageResources(root, accumulator, metadata)
	}
	for _, resourceType := range []string{"extensions", "skills", "prompts", "themes"} {
		if patterns, ok := packageFilterPatterns(filter, resourceType); ok {
			applyPackageFilter(root, patterns, resourceType, accumulator, metadata)
		} else {
			collectDefaultPackageResources(root, resourceType, accumulator, metadata)
		}
	}
	return true
}

func collectDefaultPackageResources(root, resourceType string, accumulator *resourceAccumulator, metadata PathMetadata) {
	if manifest, ok := readPiManifest(root); ok {
		switch resourceType {
		case "extensions":
			addManifestEntries(manifest.Extensions, root, resourceType, accumulator, metadata)
		case "skills":
			addManifestEntries(manifest.Skills, root, resourceType, accumulator, metadata)
		case "prompts":
			addManifestEntries(manifest.Prompts, root, resourceType, accumulator, metadata)
		case "themes":
			addManifestEntries(manifest.Themes, root, resourceType, accumulator, metadata)
		}
		return
	}
	dir := filepath.Join(root, resourceType)
	if !fileExistsLocal(dir) {
		return
	}
	for _, path := range collectResourceFiles(dir, resourceType) {
		accumulator.add(resourceType, path, metadata, true)
	}
}

func packageFilterPatterns(filter core.PackageSetting, resourceType string) ([]string, bool) {
	switch resourceType {
	case "extensions":
		return filter.Extensions, filter.Extensions != nil
	case "skills":
		return filter.Skills, filter.Skills != nil
	case "prompts":
		return filter.Prompts, filter.Prompts != nil
	case "themes":
		return filter.Themes, filter.Themes != nil
	default:
		return nil, false
	}
}

func applyPackageFilter(root string, patterns []string, resourceType string, accumulator *resourceAccumulator, metadata PathMetadata) {
	files := collectManifestFiles(root, resourceType)
	if len(patterns) == 0 {
		for _, path := range files {
			accumulator.add(resourceType, path, metadata, false)
		}
		return
	}
	enabled := applyPatterns(files, patterns, root)
	for _, path := range files {
		accumulator.add(resourceType, path, metadata, enabled[path])
	}
}

func collectManifestFiles(root, resourceType string) []string {
	if manifest, ok := readPiManifest(root); ok {
		var entries []string
		switch resourceType {
		case "extensions":
			entries = manifest.Extensions
		case "skills":
			entries = manifest.Skills
		case "prompts":
			entries = manifest.Prompts
		case "themes":
			entries = manifest.Themes
		}
		if len(entries) > 0 {
			files := collectFilesFromManifestEntries(entries, root, resourceType)
			overrides := overridePatterns(entries)
			if len(overrides) == 0 {
				return files
			}
			enabled := applyPatterns(files, overrides, root)
			return filterEnabled(files, enabled)
		}
	}
	dir := filepath.Join(root, resourceType)
	if !fileExistsLocal(dir) {
		return nil
	}
	return collectResourceFiles(dir, resourceType)
}

func readPiManifest(root string) (*piManifest, bool) {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil, false
	}
	var pkg piPackageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil || pkg.Pi == nil {
		return nil, false
	}
	return pkg.Pi, true
}

func addManifestEntries(entries []string, root, resourceType string, accumulator *resourceAccumulator, metadata PathMetadata) {
	files := collectFilesFromManifestEntries(entries, root, resourceType)
	overrides := overridePatterns(entries)
	if len(overrides) > 0 {
		files = filterEnabled(files, applyPatterns(files, overrides, root))
	}
	for _, path := range files {
		accumulator.add(resourceType, path, metadata, true)
	}
}

func collectFilesFromManifestEntries(entries []string, root, resourceType string) []string {
	var paths []string
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" || isOverridePattern(entry) {
			continue
		}
		if hasGlobPattern(entry) {
			matches, _ := filepath.Glob(filepath.Join(root, entry))
			paths = append(paths, matches...)
			continue
		}
		paths = append(paths, ResolveInputPath(entry, root, PathInputOptions{Trim: true, NormalizeUnicodeSpaces: true}))
	}
	return collectFilesFromPaths(paths, resourceType)
}

func collectFilesFromPaths(paths []string, resourceType string) []string {
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			files = append(files, collectResourceFiles(path, resourceType)...)
			continue
		}
		if matchesResourceType(path, resourceType) {
			files = append(files, path)
		}
	}
	return uniqueStrings(files)
}

func collectResourceFiles(dir, resourceType string) []string {
	switch resourceType {
	case "extensions":
		return collectExtensionFiles(dir)
	case "skills":
		return collectSkillFiles(dir)
	default:
		return walkResourceFiles(dir, resourceType)
	}
}

func collectExtensionFiles(dir string) []string {
	if entries := resolveExtensionEntries(dir); len(entries) > 0 {
		return uniqueStrings(entries)
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range dirEntries {
		if shouldSkipResourceEntry(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			files = append(files, resolveExtensionEntries(path)...)
			continue
		}
		if matchesResourceType(path, "extensions") {
			files = append(files, path)
		}
	}
	return uniqueStrings(files)
}

func resolveExtensionEntries(dir string) []string {
	if manifest, ok := readPiManifest(dir); ok && len(manifest.Extensions) > 0 {
		return collectFilesFromManifestEntries(manifest.Extensions, dir, "extensions")
	}
	var entries []string
	for _, name := range []string{"index.ts", "index.js", "index.mjs"} {
		path := filepath.Join(dir, name)
		if fileExistsLocal(path) {
			entries = append(entries, path)
			break
		}
	}
	return entries
}

func collectSkillFiles(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != dir && shouldSkipResourceEntry(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "SKILL.md" || (filepath.Dir(path) == dir && strings.HasSuffix(strings.ToLower(entry.Name()), ".md")) {
			files = append(files, path)
		}
		return nil
	})
	return uniqueStrings(files)
}

func walkResourceFiles(dir, resourceType string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != dir && shouldSkipResourceEntry(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if matchesResourceType(path, resourceType) {
			files = append(files, path)
		}
		return nil
	})
	return uniqueStrings(files)
}

func matchesResourceType(path, resourceType string) bool {
	name := strings.ToLower(filepath.Base(path))
	switch resourceType {
	case "extensions":
		return strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".mjs")
	case "skills":
		return strings.HasSuffix(name, ".md")
	case "prompts":
		return strings.HasSuffix(name, ".md")
	case "themes":
		return strings.HasSuffix(name, ".json")
	default:
		return false
	}
}

func shouldSkipResourceEntry(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

func isOverridePattern(pattern string) bool {
	return strings.HasPrefix(pattern, "!") || strings.HasPrefix(pattern, "+") || strings.HasPrefix(pattern, "-")
}

func hasGlobPattern(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func overridePatterns(entries []string) []string {
	var out []string
	for _, entry := range entries {
		if isOverridePattern(strings.TrimSpace(entry)) {
			out = append(out, strings.TrimSpace(entry))
		}
	}
	return out
}

func filterEnabled(files []string, enabled map[string]bool) []string {
	var out []string
	for _, file := range files {
		if enabled[file] {
			out = append(out, file)
		}
	}
	return out
}
