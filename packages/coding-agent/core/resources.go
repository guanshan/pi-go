package core

import (
	"bytes"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

type PromptTemplate struct {
	Name       string
	Path       string
	Content    string
	SourceInfo ResourceSourceInfo
}

type Skill struct {
	Name        string
	Path        string
	Description string
	Content     string
	SourceInfo  ResourceSourceInfo
}

type ResourceSourceInfo struct {
	Path   string
	Source string
	Scope  string
}

type ResourceLoader struct {
	CWD             string
	AgentDir        string
	ContextFiles    []string
	SystemPrompt    string
	AppendPrompt    string
	PromptTemplates map[string]PromptTemplate
	Skills          map[string]Skill
	Themes          []string
	Extensions      []string
	Diagnostics     []cli.Diagnostic
}

func LoadResources(cwd, agentDir string, args cli.Args, settings *SettingsManager) ResourceLoader {
	loader := ResourceLoader{
		CWD:             cwd,
		AgentDir:        agentDir,
		PromptTemplates: map[string]PromptTemplate{},
		Skills:          map[string]Skill{},
	}

	var globalSettings, projectSettings Settings
	if settings != nil {
		globalSettings = settings.Global
		projectSettings = settings.Project
	}
	projectBaseDir := ProjectPiDir(cwd)

	if !args.NoContextFiles {
		loader.ContextFiles = discoverContextFiles(cwd, agentDir)
	}
	loader.SystemPrompt = loadFirstExisting(
		filepath.Join(ProjectPiDir(cwd), "SYSTEM.md"),
		filepath.Join(agentDir, "SYSTEM.md"),
	)
	loader.AppendPrompt = loadFirstExisting(
		filepath.Join(ProjectPiDir(cwd), "APPEND_SYSTEM.md"),
		filepath.Join(agentDir, "APPEND_SYSTEM.md"),
	)

	if !args.NoPromptTemplates {
		loader.loadConfiguredResourceType("prompts", agentDir, globalSettings.Prompts, userResourceSource("local"))
		loader.loadAutoResourceType("prompts", filepath.Join(agentDir, "prompts"), agentDir, globalSettings.Prompts, userResourceSource("auto"))
		loader.loadConfiguredResourceType("prompts", projectBaseDir, projectSettings.Prompts, projectResourceSource("local"))
		loader.loadAutoResourceType("prompts", filepath.Join(projectBaseDir, "prompts"), projectBaseDir, projectSettings.Prompts, projectResourceSource("auto"))
	}
	for _, path := range args.PromptTemplates {
		resolved := ResolveInCWD(cwd, path)
		loader.loadPromptTemplates(resolved, cliResourceSource(resolved))
	}

	if !args.NoSkills {
		loader.loadConfiguredResourceType("skills", agentDir, globalSettings.Skills, userResourceSource("local"))
		loader.loadAutoResourceType("skills", filepath.Join(agentDir, "skills"), agentDir, globalSettings.Skills, userResourceSource("auto"))
		loader.loadAutoResourceType("skills", filepath.Join(HomeDir(), ".agents", "skills"), filepath.Join(HomeDir(), ".agents"), globalSettings.Skills, userResourceSource("auto"))
		loader.loadConfiguredResourceType("skills", projectBaseDir, projectSettings.Skills, projectResourceSource("local"))
		for _, dir := range ancestorDirs(cwd) {
			loader.loadAutoResourceType("skills", filepath.Join(dir, ".pi", "skills"), filepath.Join(dir, ".pi"), projectSettings.Skills, projectResourceSource("auto"))
			loader.loadAutoResourceType("skills", filepath.Join(dir, ".agents", "skills"), filepath.Join(dir, ".agents"), projectSettings.Skills, projectResourceSource("auto"))
		}
	}
	for _, path := range args.Skills {
		resolved := ResolveInCWD(cwd, path)
		loader.loadSkills(resolved, cliResourceSource(resolved))
	}

	if !args.NoThemes {
		loader.loadConfiguredResourceType("themes", agentDir, globalSettings.Themes, userResourceSource("local"))
		loader.loadAutoResourceType("themes", filepath.Join(agentDir, "themes"), agentDir, globalSettings.Themes, userResourceSource("auto"))
		loader.loadConfiguredResourceType("themes", projectBaseDir, projectSettings.Themes, projectResourceSource("local"))
		loader.loadAutoResourceType("themes", filepath.Join(projectBaseDir, "themes"), projectBaseDir, projectSettings.Themes, projectResourceSource("auto"))
	}
	for _, path := range args.Themes {
		loader.loadThemes(ResolveInCWD(cwd, path))
	}

	for _, entry := range append(packageEntries(settings, cwd, agentDir, false), packageEntries(settings, cwd, agentDir, true)...) {
		loader.loadPackageEntryResources(entry, args)
	}

	if !args.NoExtensions {
		loader.loadConfiguredResourceType("extensions", agentDir, globalSettings.Extensions, userResourceSource("local"))
		loader.loadAutoResourceType("extensions", filepath.Join(agentDir, "extensions"), agentDir, globalSettings.Extensions, userResourceSource("auto"))
		loader.loadConfiguredResourceType("extensions", projectBaseDir, projectSettings.Extensions, projectResourceSource("local"))
		loader.loadAutoResourceType("extensions", filepath.Join(projectBaseDir, "extensions"), projectBaseDir, projectSettings.Extensions, projectResourceSource("auto"))
	}
	for _, ext := range args.Extensions {
		loader.Extensions = append(loader.Extensions, ResolveInCWD(cwd, ext))
	}

	// ContextFiles are intentionally NOT sorted: discoverContextFiles already
	// returns them in TS order (global agent dir, then ancestors root->cwd).
	loader.Themes = uniqueResourcePaths(loader.Themes)
	loader.Extensions = uniqueResourcePaths(loader.Extensions)
	return loader
}

func (r ResourceLoader) BuildSystemPrompt(args cli.Args, tools ToolPromptInfo) string {
	var b strings.Builder
	if args.SystemPrompt != "" {
		b.WriteString(readTextArg(r.CWD, args.SystemPrompt))
	} else if r.SystemPrompt != "" {
		b.WriteString(r.SystemPrompt)
	} else {
		// Default prompt: lead paragraph + an "Available tools:" list of one-line
		// snippets + a deduped "Guidelines:" section + a "Pi documentation:" block,
		// mirroring TS buildSystemPrompt (system-prompt.ts:130-147). A custom prompt
		// (args.SystemPrompt or r.SystemPrompt) replaces all three sections.
		b.WriteString(defaultPromptBody(tools))
	}
	if !args.NoContextFiles && len(r.ContextFiles) > 0 {
		// Project context files are injected as a <project_context> XML block with
		// one <project_instructions path="..."> element per file, mirroring TS
		// buildSystemPrompt (system-prompt.ts:58-67) rather than markdown headings.
		var blocks []string
		for _, path := range r.ContextFiles {
			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			blocks = append(blocks, fmt.Sprintf("<project_instructions path=\"%s\">\n%s\n</project_instructions>\n\n", path, bytes.TrimSpace(content)))
		}
		if len(blocks) > 0 {
			b.WriteString("\n\n<project_context>\n\nProject-specific instructions and guidelines:\n\n")
			for _, block := range blocks {
				b.WriteString(block)
			}
			b.WriteString("</project_context>\n")
		}
	}
	// Skills are listed only when the read tool is available, since loading a
	// skill's file requires reading it (mirrors TS system-prompt.ts:164). The
	// block uses the <available_skills> XML shape from TS formatSkillsForPrompt
	// (skills.ts) rather than a markdown list. Skills live in a map, so names are
	// sorted for deterministic output (TS iterates an ordered slice).
	if len(r.Skills) > 0 && tools.Has("read") {
		names := make([]string, 0, len(r.Skills))
		for name := range r.Skills {
			names = append(names, name)
		}
		sort.Strings(names)
		b.WriteString("\n\nThe following skills provide specialized instructions for specific tasks.\n")
		b.WriteString("Use the read tool to load a skill's file when the task matches its description.\n")
		b.WriteString("When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.\n")
		b.WriteString("\n<available_skills>\n")
		for _, name := range names {
			skill := r.Skills[name]
			b.WriteString("  <skill>\n")
			b.WriteString("    <name>" + escapeXML(skill.Name) + "</name>\n")
			b.WriteString("    <description>" + escapeXML(skill.Description) + "</description>\n")
			b.WriteString("    <location>" + escapeXML(skill.Path) + "</location>\n")
			b.WriteString("  </skill>\n")
		}
		b.WriteString("</available_skills>")
	}
	if r.AppendPrompt != "" {
		b.WriteString("\n\n")
		b.WriteString(r.AppendPrompt)
	}
	for _, text := range args.AppendSystemPrompt {
		if strings.TrimSpace(text) == "" {
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(readTextArg(r.CWD, text))
	}
	out := strings.TrimSpace(b.String())
	// Date and working directory are appended last, every turn (TS system-prompt.ts:168).
	out += fmt.Sprintf("\nCurrent date: %s", time.Now().Format("2006-01-02"))
	out += fmt.Sprintf("\nCurrent working directory: %s", filepath.ToSlash(r.CWD))
	return out
}

// escapeXML escapes the five XML predefined entities exactly as the TypeScript
// escapeXml helper (system-prompt.ts / skills.ts) does — note "&apos;"/"&quot;"
// rather than Go html.EscapeString's numeric entities — so the skills block is
// byte-identical to TS.
func escapeXML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func (r ResourceLoader) ExpandInput(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if strings.HasPrefix(trimmed, "/skill:") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "/skill:"))
		name := rest
		userMessage := ""
		if idx := strings.IndexAny(rest, " \t\n"); idx >= 0 {
			name = strings.TrimSpace(rest[:idx])
			userMessage = strings.TrimSpace(rest[idx:])
		}
		if skill, ok := r.Skills[name]; ok {
			var b strings.Builder
			fmt.Fprintf(&b, "<skill name=%q location=%q>\n%s\n</skill>", skill.Name, skill.Path, strings.TrimSpace(skill.Content))
			if userMessage != "" {
				b.WriteString("\n\n")
				b.WriteString(userMessage)
			}
			return b.String(), true
		}
		return input, false
	}
	if strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, " ") {
		name := strings.TrimPrefix(trimmed, "/")
		if tmpl, ok := r.PromptTemplates[name]; ok {
			return tmpl.Content, true
		}
	}
	return input, false
}

func discoverContextFiles(cwd, agentDir string) []string {
	// Mirror resource-loader.ts:57-112: each directory contributes only the FIRST
	// matching candidate (all four casings), the global agent dir is loaded first,
	// then ancestors ordered root->cwd, de-duplicated by absolute path. The caller
	// must NOT re-sort the result (that would destroy this ordering).
	candidates := []string{"AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}
	firstInDir := func(dir string) string {
		for _, name := range candidates {
			if path := filepath.Join(dir, name); fileExists(path) {
				return path
			}
		}
		return ""
	}
	var files []string
	seen := map[string]bool{}
	if path := firstInDir(agentDir); path != "" {
		files = append(files, path)
		seen[path] = true
	}
	// ancestorDirs already yields root->cwd order, so append each hit to preserve
	// it (matching TS, which walks cwd->root and unshifts to the same result).
	for _, dir := range ancestorDirs(cwd) {
		if path := firstInDir(dir); path != "" && !seen[path] {
			files = append(files, path)
			seen[path] = true
		}
	}
	return files
}

func ancestorDirs(cwd string) []string {
	cwd = filepath.Clean(cwd)
	var dirs []string
	for {
		dirs = append(dirs, cwd)
		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}
		cwd = parent
	}
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	return dirs
}

func userResourceSource(source string) ResourceSourceInfo {
	return ResourceSourceInfo{Source: source, Scope: "user"}
}

func projectResourceSource(source string) ResourceSourceInfo {
	return ResourceSourceInfo{Source: source, Scope: "project"}
}

func cliResourceSource(path string) ResourceSourceInfo {
	return ResourceSourceInfo{Path: path, Source: "cli", Scope: "temporary"}
}

func packageResourceSource(entry packageEntry) ResourceSourceInfo {
	scope := "user"
	if entry.Record.Local {
		scope = "project"
	}
	return ResourceSourceInfo{Source: entry.Record.Source, Scope: scope}
}

func withResourceSourcePath(source ResourceSourceInfo, path string) ResourceSourceInfo {
	source.Path = path
	return source
}

func (r *ResourceLoader) loadConfiguredResourceType(resourceType, baseDir string, entries []string, source ResourceSourceInfo) {
	for _, path := range configuredResourcePaths(resourceType, baseDir, entries) {
		r.loadResourcePath(resourceType, path, withResourceSourcePath(source, path))
	}
}

func (r *ResourceLoader) loadAutoResourceType(resourceType, root, baseDir string, entries []string, source ResourceSourceInfo) {
	for _, path := range autoResourcePaths(resourceType, root) {
		if resourceEnabledByOverrides(path, entries, baseDir) {
			r.loadResourcePath(resourceType, path, withResourceSourcePath(source, path))
		}
	}
}

func (r *ResourceLoader) loadResourcePath(resourceType, path string, source ResourceSourceInfo) {
	switch resourceType {
	case "prompts":
		r.loadPromptTemplates(path, withResourceSourcePath(source, path))
	case "skills":
		r.loadSkills(path, withResourceSourcePath(source, path))
	case "themes":
		r.loadThemes(path)
	case "extensions":
		if isExtensionPath(path) {
			r.Extensions = append(r.Extensions, path)
		} else {
			r.loadExtensionDir(path)
		}
	}
}

func configuredResourcePaths(resourceType, baseDir string, entries []string) []string {
	var plain, overrides []string
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if isResourceOverridePattern(entry) {
			overrides = append(overrides, entry)
			continue
		}
		plain = append(plain, ResolveInCWD(baseDir, entry))
	}
	files := expandResourcePaths(resourceType, plain)
	return enabledResourcePaths(files, overrides, baseDir)
}

func autoResourcePaths(resourceType, root string) []string {
	switch resourceType {
	case "prompts":
		return collectMarkdownFiles(root, false)
	case "skills":
		return collectSkillResourcePaths(root)
	case "themes":
		return collectJSONFiles(root, false)
	case "extensions":
		return collectExtensionResourcePaths(root)
	default:
		return nil
	}
}

func enabledResourcePaths(paths, patterns []string, baseDir string) []string {
	var out []string
	for _, path := range paths {
		if resourceEnabledByOverrides(path, patterns, baseDir) {
			out = append(out, path)
		}
	}
	return uniqueResourcePaths(out)
}

func resourceEnabledByOverrides(path string, entries []string, baseDir string) bool {
	var excludes, forceIncludes, forceExcludes []string
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		switch {
		case strings.HasPrefix(entry, "!"):
			excludes = append(excludes, entry[1:])
		case strings.HasPrefix(entry, "+"):
			forceIncludes = append(forceIncludes, entry[1:])
		case strings.HasPrefix(entry, "-"):
			forceExcludes = append(forceExcludes, entry[1:])
		}
	}
	enabled := !packagePathMatchesAny(path, excludes, baseDir)
	if resourcePathMatchesAnyExact(path, forceIncludes, baseDir) {
		enabled = true
	}
	if resourcePathMatchesAnyExact(path, forceExcludes, baseDir) {
		enabled = false
	}
	return enabled
}

func resourcePathMatchesAnyExact(path string, patterns []string, baseDir string) bool {
	if len(patterns) == 0 {
		return false
	}
	path = filepath.Clean(path)
	rel, _ := filepath.Rel(baseDir, path)
	candidates := []string{
		filepath.ToSlash(path),
		filepath.ToSlash(filepath.Clean(rel)),
		filepath.Base(path),
	}
	if filepath.Base(path) == "SKILL.md" {
		parent := filepath.Dir(path)
		parentRel, _ := filepath.Rel(baseDir, parent)
		candidates = append(candidates, filepath.ToSlash(parent), filepath.ToSlash(filepath.Clean(parentRel)), filepath.Base(parent))
	}
	for _, pattern := range patterns {
		pattern = normalizeExactResourcePattern(pattern)
		for _, candidate := range candidates {
			if candidate == pattern {
				return true
			}
		}
	}
	return false
}

func normalizeExactResourcePattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	pattern = strings.TrimPrefix(pattern, "./")
	pattern = strings.TrimPrefix(pattern, `.\\`)
	return filepath.ToSlash(filepath.Clean(pattern))
}

func isResourceOverridePattern(pattern string) bool {
	return strings.HasPrefix(pattern, "!") || strings.HasPrefix(pattern, "+") || strings.HasPrefix(pattern, "-")
}

func (r *ResourceLoader) loadPromptTemplates(path string, source ResourceSourceInfo) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if !info.IsDir() {
		r.addPromptTemplate(path, withResourceSourcePath(source, path))
		return
	}
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		r.addPromptTemplate(p, withResourceSourcePath(source, p))
		return nil
	})
}

func (r *ResourceLoader) addPromptTemplate(path string, source ResourceSourceInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		r.Diagnostics = append(r.Diagnostics, cli.Diagnostic{Type: "warning", Message: err.Error()})
		return
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	r.PromptTemplates[name] = PromptTemplate{Name: name, Path: path, Content: stripFrontmatter(string(data)), SourceInfo: withResourceSourcePath(source, path)}
}

func (r *ResourceLoader) loadSkills(path string, source ResourceSourceInfo) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if !info.IsDir() {
		if filepath.Base(path) == "SKILL.md" {
			r.addSkill(filepath.Dir(path), path, withResourceSourcePath(source, path))
		}
		return
	}
	if fileExists(filepath.Join(path, "SKILL.md")) {
		skillPath := filepath.Join(path, "SKILL.md")
		r.addSkill(path, skillPath, withResourceSourcePath(source, skillPath))
		return
	}
	entries, _ := os.ReadDir(path)
	for _, entry := range entries {
		if entry.IsDir() && fileExists(filepath.Join(path, entry.Name(), "SKILL.md")) {
			skillPath := filepath.Join(path, entry.Name(), "SKILL.md")
			r.addSkill(filepath.Join(path, entry.Name()), skillPath, withResourceSourcePath(source, skillPath))
		}
	}
}

func (r *ResourceLoader) addSkill(dir, skillPath string, source ResourceSourceInfo) {
	data, err := os.ReadFile(skillPath)
	if err != nil {
		r.Diagnostics = append(r.Diagnostics, cli.Diagnostic{Type: "warning", Message: err.Error()})
		return
	}
	name := filepath.Base(dir)
	content := string(data)
	description := extractSkillDescription(content)
	r.Skills[name] = Skill{Name: name, Path: skillPath, Description: description, Content: content, SourceInfo: withResourceSourcePath(source, skillPath)}
}

func (r *ResourceLoader) loadThemes(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if !info.IsDir() {
		r.Themes = append(r.Themes, path)
		return
	}
	entries, _ := os.ReadDir(path)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			r.Themes = append(r.Themes, filepath.Join(path, entry.Name()))
		}
	}
}

func (r *ResourceLoader) loadExtensionDir(path string) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".ts") || strings.HasSuffix(entry.Name(), ".js") || strings.HasSuffix(entry.Name(), ".mjs")) {
			r.Extensions = append(r.Extensions, filepath.Join(path, entry.Name()))
		}
	}
}

func (r *ResourceLoader) loadPackageEntryResources(entry packageEntry, args cli.Args) {
	root := entry.Record.Path
	if root == "" {
		return
	}
	source := packageResourceSource(entry)
	manifest := readPackageManifest(root)
	if !args.NoPromptTemplates {
		r.loadPackageResourceType(root, "prompts", entry.Setting.Prompts, entry.Setting.Prompts != nil, manifest.Prompts, source)
	}
	if !args.NoSkills {
		r.loadPackageResourceType(root, "skills", entry.Setting.Skills, entry.Setting.Skills != nil, manifest.Skills, source)
	}
	if !args.NoThemes {
		r.loadPackageResourceType(root, "themes", entry.Setting.Themes, entry.Setting.Themes != nil, manifest.Themes, source)
	}
	if !args.NoExtensions {
		r.loadPackageResourceType(root, "extensions", entry.Setting.Extensions, entry.Setting.Extensions != nil, manifest.Extensions, source)
	}
}

func (r *ResourceLoader) loadPackageResourceType(root, resourceType string, filter []string, hasFilter bool, manifestEntries []string, source ResourceSourceInfo) {
	for _, path := range packageResourcePaths(root, resourceType, filter, hasFilter, manifestEntries) {
		source := withResourceSourcePath(source, path)
		switch resourceType {
		case "prompts":
			r.addPromptTemplate(path, source)
		case "skills":
			r.loadSkills(path, source)
		case "themes":
			r.loadThemes(path)
		case "extensions":
			if isExtensionPath(path) {
				r.Extensions = append(r.Extensions, path)
			} else {
				r.loadExtensionDir(path)
			}
		}
	}
}

func packageResourcePaths(root, resourceType string, filter []string, hasFilter bool, manifestEntries []string) []string {
	all := defaultPackageResourcePaths(root, resourceType, manifestEntries)
	if hasFilter {
		return filterPackageResourcePaths(all, filter, root)
	}
	return all
}

func defaultPackageResourcePaths(root, resourceType string, manifestEntries []string) []string {
	if len(manifestEntries) > 0 {
		return collectPackageManifestPaths(root, resourceType, manifestEntries)
	}
	switch resourceType {
	case "prompts":
		return collectMarkdownFiles(filepath.Join(root, "prompts"), false)
	case "skills":
		return collectSkillResourcePaths(filepath.Join(root, "skills"))
	case "themes":
		return collectJSONFiles(filepath.Join(root, "themes"), false)
	case "extensions":
		return collectExtensionResourcePaths(filepath.Join(root, "extensions"))
	default:
		return nil
	}
}

func collectPackageManifestPaths(root, resourceType string, entries []string) []string {
	var paths []string
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" || strings.HasPrefix(entry, "!") || strings.HasPrefix(entry, "+") || strings.HasPrefix(entry, "-") {
			continue
		}
		if strings.ContainsAny(entry, "*?[") {
			matches, _ := filepath.Glob(filepath.Join(root, entry))
			paths = append(paths, expandResourcePaths(resourceType, matches)...)
			continue
		}
		paths = append(paths, expandResourcePaths(resourceType, []string{ResolveInCWD(root, entry)})...)
	}
	return uniqueResourcePaths(paths)
}

func expandResourcePaths(resourceType string, paths []string) []string {
	var out []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			switch resourceType {
			case "prompts":
				out = append(out, collectMarkdownFiles(path, true)...)
			case "skills":
				out = append(out, collectSkillResourcePaths(path)...)
			case "themes":
				out = append(out, collectJSONFiles(path, true)...)
			case "extensions":
				out = append(out, collectExtensionResourcePaths(path)...)
			}
			continue
		}
		if resourcePathMatchesType(path, resourceType) {
			out = append(out, filepath.Clean(path))
		}
	}
	return out
}

func collectMarkdownFiles(root string, recursive bool) []string {
	return collectFilesByExtension(root, recursive, ".md")
}

func collectJSONFiles(root string, recursive bool) []string {
	return collectFilesByExtension(root, recursive, ".json")
}

func collectFilesByExtension(root string, recursive bool, extensions ...string) []string {
	info, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		for _, ext := range extensions {
			if strings.EqualFold(filepath.Ext(root), ext) {
				return []string{filepath.Clean(root)}
			}
		}
		return nil
	}
	var files []string
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && !recursive {
				return filepath.SkipDir
			}
			if path != root && shouldSkipPackageResource(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		for _, ext := range extensions {
			if strings.EqualFold(filepath.Ext(entry.Name()), ext) {
				files = append(files, filepath.Clean(path))
				break
			}
		}
		return nil
	}
	_ = filepath.WalkDir(root, walk)
	return uniqueResourcePaths(files)
}

func collectSkillResourcePaths(root string) []string {
	info, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		if filepath.Base(root) == "SKILL.md" {
			return []string{filepath.Clean(root)}
		}
		return nil
	}
	var paths []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && shouldSkipPackageResource(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "SKILL.md" || (filepath.Dir(path) == root && strings.HasSuffix(strings.ToLower(entry.Name()), ".md")) {
			paths = append(paths, filepath.Clean(path))
		}
		return nil
	})
	return uniqueResourcePaths(paths)
}

func collectExtensionResourcePaths(root string) []string {
	info, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		if isExtensionPath(root) {
			return []string{filepath.Clean(root)}
		}
		return nil
	}
	var paths []string
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if shouldSkipPackageResource(entry.Name()) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			for _, name := range []string{"index.ts", "index.js", "index.mjs"} {
				indexPath := filepath.Join(path, name)
				if fileExists(indexPath) {
					paths = append(paths, filepath.Clean(indexPath))
					break
				}
			}
			continue
		}
		if isExtensionPath(path) {
			paths = append(paths, filepath.Clean(path))
		}
	}
	return uniqueResourcePaths(paths)
}

func filterPackageResourcePaths(paths []string, patterns []string, root string) []string {
	if len(patterns) == 0 {
		return nil
	}
	var includes, excludes []string
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, "!") || strings.HasPrefix(pattern, "-") {
			excludes = append(excludes, pattern[1:])
			continue
		}
		pattern = strings.TrimPrefix(pattern, "+")
		includes = append(includes, pattern)
	}
	var out []string
	for _, path := range paths {
		enabled := len(includes) == 0 || packagePathMatchesAny(path, includes, root)
		if enabled && packagePathMatchesAny(path, excludes, root) {
			enabled = false
		}
		if enabled {
			out = append(out, path)
		}
	}
	return uniqueResourcePaths(out)
}

func packagePathMatchesAny(path string, patterns []string, root string) bool {
	for _, pattern := range patterns {
		if packagePathMatches(path, pattern, root) {
			return true
		}
	}
	return false
}

func packagePathMatches(path, pattern, root string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	path = filepath.Clean(path)
	rel, _ := filepath.Rel(root, path)
	candidates := []string{filepath.ToSlash(path), filepath.ToSlash(rel), filepath.Base(path)}
	if filepath.Base(path) == "SKILL.md" {
		candidates = append(candidates, filepath.ToSlash(filepath.Dir(rel)), filepath.Base(filepath.Dir(path)))
	}
	for _, candidate := range candidates {
		if ok, _ := pathpkg.Match(pattern, candidate); ok {
			return true
		}
		if candidate == pattern || strings.HasSuffix(candidate, "/"+pattern) {
			return true
		}
	}
	return false
}

func resourcePathMatchesType(path, resourceType string) bool {
	switch resourceType {
	case "prompts":
		return strings.EqualFold(filepath.Ext(path), ".md")
	case "skills":
		return filepath.Base(path) == "SKILL.md" || strings.EqualFold(filepath.Ext(path), ".md")
	case "themes":
		return strings.EqualFold(filepath.Ext(path), ".json")
	case "extensions":
		return isExtensionPath(path)
	default:
		return false
	}
}

func isExtensionPath(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".mjs")
}

func shouldSkipPackageResource(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

func uniqueResourcePaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "." || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func stripFrontmatter(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	idx := strings.Index(content[4:], "\n---")
	if idx < 0 {
		return content
	}
	return strings.TrimLeft(content[idx+8:], "\n")
}

func extractSkillDescription(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "use this skill") {
			return strings.TrimSpace(line)
		}
		if i > 20 {
			break
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

// defaultPromptBody builds the default system-prompt header — lead paragraph,
// "Available tools:" snippet list, deduped "Guidelines:" section, and the "Pi
// documentation:" block — byte-for-byte matching TS buildSystemPrompt
// (system-prompt.ts:88-147).
func defaultPromptBody(tools ToolPromptInfo) string {
	// Available tools: only tools that expose a one-line snippet, in registration
	// order (system-prompt.ts:90-93).
	var toolLines []string
	for _, name := range tools.OrderedNames {
		if snippet := tools.Snippets[name]; snippet != "" {
			toolLines = append(toolLines, "- "+name+": "+snippet)
		}
	}
	toolsList := "(none)"
	if len(toolLines) > 0 {
		toolsList = strings.Join(toolLines, "\n")
	}

	// Guidelines: the bash-only-file-ops rule first (only when bash is the sole
	// file-exploration tool), then each per-tool guideline (deduped, in tool
	// order), then the two always-on bullets last (system-prompt.ts:96-128).
	var guidelines []string
	seen := map[string]bool{}
	add := func(g string) {
		if g == "" || seen[g] {
			return
		}
		seen[g] = true
		guidelines = append(guidelines, g)
	}
	if tools.Has("bash") && !tools.Has("grep") && !tools.Has("find") && !tools.Has("ls") {
		add("Use bash for file operations like ls, rg, find")
	}
	for _, g := range tools.Guidelines {
		add(strings.TrimSpace(g))
	}
	add("Be concise in your responses")
	add("Show file paths clearly when working with files")
	var gb strings.Builder
	for i, g := range guidelines {
		if i > 0 {
			gb.WriteByte('\n')
		}
		gb.WriteString("- ")
		gb.WriteString(g)
	}

	return "You are an expert coding assistant operating inside pi, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files.\n\n" +
		"Available tools:\n" + toolsList + "\n\n" +
		"In addition to the tools above, you may have access to other custom tools depending on the project.\n\n" +
		"Guidelines:\n" + gb.String() + "\n\n" +
		"Pi documentation (read only when the user asks about pi itself, its SDK, extensions, themes, skills, or TUI):\n" +
		"- Main documentation: " + filepath.ToSlash(ReadmePath()) + "\n" +
		"- Additional docs: " + filepath.ToSlash(DocsPath()) + "\n" +
		"- Examples: " + filepath.ToSlash(ExamplesPath()) + " (extensions, custom tools, SDK)\n" +
		"- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory\n" +
		"- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pi packages (docs/packages.md)\n" +
		"- When working on pi topics, read the docs and examples, and follow .md cross-references before implementing\n" +
		"- Always read pi .md files completely and follow links to related docs (e.g., tui.md for TUI API details)"
}

func readTextArg(cwd, value string) string {
	path := ResolveInCWD(cwd, value)
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return value
}

func loadFirstExisting(paths ...string) string {
	for _, path := range paths {
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
	}
	return ""
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
