package core

import (
	"bytes"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

type PromptTemplate struct {
	Name         string
	Path         string
	Description  string
	ArgumentHint string
	Content      string
	SourceInfo   ResourceSourceInfo
}

type Skill struct {
	Name                   string
	Path                   string
	BaseDir                string
	Description            string
	Content                string
	DisableModelInvocation bool
	SourceInfo             ResourceSourceInfo
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
	// SkillOrder preserves skill discovery/load order (user dir, then project
	// dir, then explicit paths) so the <available_skills> system-prompt block is
	// emitted in TS load order rather than alphabetically (TS skills.ts uses an
	// ordered slice from Array.from(skillMap.values())). The Skills map keeps
	// O(1) lookup for /skill:name invocation and autocomplete.
	SkillOrder []string
	// skillRealPaths de-dupes skills loaded via symlinks pointing at the same
	// file (TS skills.ts realPathSet); a realpath already seen is skipped.
	skillRealPaths map[string]bool
	Themes         []string
	Extensions     []string
	Diagnostics    []cli.Diagnostic
}

func LoadResources(cwd, agentDir string, args cli.Args, settings *SettingsManager) ResourceLoader {
	loader := ResourceLoader{
		CWD:             cwd,
		AgentDir:        agentDir,
		PromptTemplates: map[string]PromptTemplate{},
		Skills:          map[string]Skill{},
		skillRealPaths:  map[string]bool{},
	}

	var globalSettings, projectSettings Settings
	if settings != nil {
		globalSettings = settings.Global
		projectSettings = settings.Project
	}
	projectBaseDir := ProjectPiDir(cwd)
	// Project-scoped resources (context files, SYSTEM.md, prompts, skills, themes,
	// extensions) load only when the project is trusted, mirroring TS (settings-
	// manager + resource-loader gate every project path behind isProjectTrusted).
	// settings==nil keeps the prior trusted-by-default behavior for callers that
	// predate the trust system. Project settings.json is already gated upstream in
	// NewSettingsManagerWithTrust (projectSettings stays empty when untrusted).
	projectTrusted := settings == nil || settings.ProjectTrusted

	if !args.NoContextFiles {
		loader.ContextFiles = discoverContextFiles(cwd, agentDir, projectTrusted)
	}
	systemCandidates := []string{}
	appendCandidates := []string{}
	if projectTrusted {
		systemCandidates = append(systemCandidates, filepath.Join(ProjectPiDir(cwd), "SYSTEM.md"))
		appendCandidates = append(appendCandidates, filepath.Join(ProjectPiDir(cwd), "APPEND_SYSTEM.md"))
	}
	systemCandidates = append(systemCandidates, filepath.Join(agentDir, "SYSTEM.md"))
	appendCandidates = append(appendCandidates, filepath.Join(agentDir, "APPEND_SYSTEM.md"))
	loader.SystemPrompt = loadFirstExisting(systemCandidates...)
	loader.AppendPrompt = loadFirstExisting(appendCandidates...)

	if !args.NoPromptTemplates {
		loader.loadConfiguredResourceType("prompts", agentDir, globalSettings.Prompts, userResourceSource("local"))
		loader.loadAutoResourceType("prompts", filepath.Join(agentDir, "prompts"), agentDir, globalSettings.Prompts, userResourceSource("auto"))
		if projectTrusted {
			loader.loadConfiguredResourceType("prompts", projectBaseDir, projectSettings.Prompts, projectResourceSource("local"))
			loader.loadAutoResourceType("prompts", filepath.Join(projectBaseDir, "prompts"), projectBaseDir, projectSettings.Prompts, projectResourceSource("auto"))
		}
	}
	for _, path := range args.PromptTemplates {
		resolved := ResolveInCWD(cwd, path)
		loader.loadPromptTemplates(resolved, cliResourceSource(resolved))
	}

	if !args.NoSkills {
		loader.loadConfiguredResourceType("skills", agentDir, globalSettings.Skills, userResourceSource("local"))
		loader.loadAutoResourceType("skills", filepath.Join(agentDir, "skills"), agentDir, globalSettings.Skills, userResourceSource("auto"))
		loader.loadAutoResourceType("skills", filepath.Join(HomeDir(), ".agents", "skills"), filepath.Join(HomeDir(), ".agents"), globalSettings.Skills, userResourceSource("auto"))
		if projectTrusted {
			loader.loadConfiguredResourceType("skills", projectBaseDir, projectSettings.Skills, projectResourceSource("local"))
			for _, dir := range ancestorDirs(cwd) {
				loader.loadAutoResourceType("skills", filepath.Join(dir, ".pi", "skills"), filepath.Join(dir, ".pi"), projectSettings.Skills, projectResourceSource("auto"))
				loader.loadAutoResourceType("skills", filepath.Join(dir, ".agents", "skills"), filepath.Join(dir, ".agents"), projectSettings.Skills, projectResourceSource("auto"))
			}
		}
	}
	for _, path := range args.Skills {
		resolved := ResolveInCWD(cwd, path)
		loader.loadSkills(resolved, cliResourceSource(resolved))
	}

	if !args.NoThemes {
		loader.loadConfiguredResourceType("themes", agentDir, globalSettings.Themes, userResourceSource("local"))
		loader.loadAutoResourceType("themes", filepath.Join(agentDir, "themes"), agentDir, globalSettings.Themes, userResourceSource("auto"))
		if projectTrusted {
			loader.loadConfiguredResourceType("themes", projectBaseDir, projectSettings.Themes, projectResourceSource("local"))
			loader.loadAutoResourceType("themes", filepath.Join(projectBaseDir, "themes"), projectBaseDir, projectSettings.Themes, projectResourceSource("auto"))
		}
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
		if projectTrusted {
			loader.loadConfiguredResourceType("extensions", projectBaseDir, projectSettings.Extensions, projectResourceSource("local"))
			loader.loadAutoResourceType("extensions", filepath.Join(projectBaseDir, "extensions"), projectBaseDir, projectSettings.Extensions, projectResourceSource("auto"))
		}
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
	// (skills.ts) rather than a markdown list. Skills are emitted in discovery/
	// load order (user dir, then project dir, then explicit paths) via SkillOrder,
	// matching TS, which iterates Array.from(skillMap.values()). Skills marked
	// disable-model-invocation are filtered out (they stay invokable via
	// /skill:name) exactly like TS formatSkillsForPrompt skills.ts:336.
	if visible := r.visibleSkills(); len(visible) > 0 && tools.Has("read") {
		b.WriteString("\n\nThe following skills provide specialized instructions for specific tasks.\n")
		b.WriteString("Use the read tool to load a skill's file when the task matches its description.\n")
		b.WriteString("When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.\n")
		b.WriteString("\n<available_skills>\n")
		for _, skill := range visible {
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

// visibleSkills returns the skills shown in the <available_skills> system-prompt
// block, in discovery/load order, excluding any skill with
// disable-model-invocation: true (those remain invokable via /skill:name only),
// mirroring TS formatSkillsForPrompt (skills.ts:335-336). When SkillOrder is
// empty (e.g. a ResourceLoader literal built directly without going through the
// loader) it falls back to sorted map iteration so callers still get a
// deterministic order.
func (r ResourceLoader) visibleSkills() []Skill {
	order := r.SkillOrder
	if len(order) == 0 && len(r.Skills) > 0 {
		order = make([]string, 0, len(r.Skills))
		for name := range r.Skills {
			order = append(order, name)
		}
		sort.Strings(order)
	}
	visible := make([]Skill, 0, len(order))
	for _, name := range order {
		skill, ok := r.Skills[name]
		if !ok || skill.DisableModelInvocation {
			continue
		}
		visible = append(visible, skill)
	}
	return visible
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
			_, body := parseFrontmatterFields(skill.Content)
			body = strings.TrimSpace(body)
			baseDir := skill.BaseDir
			if baseDir == "" {
				baseDir = filepath.Dir(skill.Path)
			}
			var b strings.Builder
			fmt.Fprintf(&b, "<skill name=%q location=%q>\nReferences are relative to %s.\n\n%s\n</skill>", skill.Name, skill.Path, baseDir, body)
			if userMessage != "" {
				b.WriteString("\n\n")
				b.WriteString(userMessage)
			}
			return b.String(), true
		}
		return input, false
	}
	if strings.HasPrefix(trimmed, "/") {
		if match := promptTemplateInvocationRe.FindStringSubmatch(trimmed); match != nil {
			name := match[1]
			argsString := match[2]
			if tmpl, ok := r.PromptTemplates[name]; ok {
				args := parseCommandArgs(argsString)
				return substituteArgs(tmpl.Content, args), true
			}
		}
	}
	return input, false
}

var promptTemplateInvocationRe = regexp.MustCompile(`^/([^\s]+)(?:\s+([\s\S]*))?$`)

// parseCommandArgs splits a bash-style argument string respecting single and
// double quotes, mirroring TS parseCommandArgs (prompt-templates.ts:24-55).
// Quotes are stripped; whitespace separates unquoted tokens.
func parseCommandArgs(argsString string) []string {
	var args []string
	var current strings.Builder
	var inQuote rune // 0 when not in a quote
	for _, char := range argsString {
		switch {
		case inQuote != 0:
			if char == inQuote {
				inQuote = 0
			} else {
				current.WriteRune(char)
			}
		case char == '"' || char == '\'':
			inQuote = char
		case isASCIISpace(char):
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(char)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// isASCIISpace reports whether r is whitespace per JS's /\s/ for the characters
// that appear in CLI input. TS parseCommandArgs uses /\s/.test(char); the common
// whitespace characters are space, tab, newline, carriage return, vertical tab,
// and form feed.
func isASCIISpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// substituteArgs substitutes argument placeholders in a prompt-template body,
// mirroring TS substituteArgs (prompt-templates.ts:68-102):
//   - $1, $2, ... positional args (1-indexed; missing -> "")
//   - ${@:N} / ${@:N:L} bash-style slices (1-indexed start)
//   - $ARGUMENTS and $@ -> all args joined by a single space
//
// Order matters: positional and slice placeholders are resolved BEFORE the
// all-args placeholders so that substituted values containing $-patterns are not
// recursively re-substituted.
func substituteArgs(content string, args []string) string {
	result := content

	// $1, $2, ... (process first so wildcard values are not re-substituted).
	result = positionalArgRe.ReplaceAllStringFunc(result, func(token string) string {
		m := positionalArgRe.FindStringSubmatch(token)
		index, err := strconv.Atoi(m[1])
		if err != nil {
			return ""
		}
		index-- // 1-indexed -> 0-indexed
		if index < 0 || index >= len(args) {
			return ""
		}
		return args[index]
	})

	// ${@:start} or ${@:start:length} (process before bare $@).
	result = sliceArgRe.ReplaceAllStringFunc(result, func(token string) string {
		m := sliceArgRe.FindStringSubmatch(token)
		start, _ := strconv.Atoi(m[1])
		start-- // 1-indexed -> 0-indexed
		if start < 0 {
			start = 0
		}
		if start > len(args) {
			start = len(args)
		}
		if m[2] != "" {
			length, _ := strconv.Atoi(m[2])
			end := start + length
			if end < start {
				end = start
			}
			if end > len(args) {
				end = len(args)
			}
			return strings.Join(args[start:end], " ")
		}
		return strings.Join(args[start:], " ")
	})

	allArgs := strings.Join(args, " ")
	result = strings.ReplaceAll(result, "$ARGUMENTS", allArgs)
	result = strings.ReplaceAll(result, "$@", allArgs)

	return result
}

var (
	positionalArgRe = regexp.MustCompile(`\$(\d+)`)
	sliceArgRe      = regexp.MustCompile(`\$\{@:(\d+)(?::(\d+))?\}`)
)

func discoverContextFiles(cwd, agentDir string, projectTrusted bool) []string {
	// Mirror resource-loader.ts:57-112: each directory contributes only the FIRST
	// matching candidate (all four casings), the global agent dir is loaded first,
	// then ancestors ordered root->cwd, de-duplicated by absolute path. The caller
	// must NOT re-sort the result (that would destroy this ordering). Project
	// (cwd-ancestor) context files — the AGENTS.md/CLAUDE.md trust inputs — are
	// only discovered when the project is trusted; the global agent-dir context
	// file is always loaded (TS gates loadProjectContextFiles on isProjectTrusted).
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
	if !projectTrusted {
		return files
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
	// Mirror TS loadTemplateFromFile (prompt-templates.ts:104-133): the name is
	// the basename minus the .md extension; description comes from frontmatter or
	// falls back to the first non-empty body line (truncated to 60 chars with an
	// ellipsis); argument-hint is carried verbatim from frontmatter when present.
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	fields, body := parseFrontmatterFields(string(data))
	description := frontmatterString(fields, "description")
	if description == "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			runes := []rune(line)
			if len(runes) > 60 {
				description = string(runes[:60]) + "..."
			} else {
				description = line
			}
			break
		}
	}
	r.PromptTemplates[name] = PromptTemplate{
		Name:         name,
		Path:         path,
		Description:  description,
		ArgumentHint: frontmatterString(fields, "argument-hint"),
		Content:      body,
		SourceInfo:   withResourceSourcePath(source, path),
	}
}

// skillIgnoreFileNames lists the ignore files honored during skill discovery,
// matching TS IGNORE_FILE_NAMES (skills.ts:16).
var skillIgnoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

// skillIgnoreRule is a single compiled gitignore pattern.
type skillIgnoreRule struct {
	negated  bool
	dirOnly  bool
	matchRe  *regexp.Regexp
	anchored bool // pattern contains a slash (other than a trailing one) -> matched against the full path
}

// skillIgnore accumulates gitignore rules from .gitignore/.ignore/.fdignore files
// encountered while walking a skill tree, mirroring TS's use of the `ignore`
// package via addIgnoreRules (skills.ts:24-65). Patterns from nested directories
// are prefixed with their path relative to the scan root so they remain anchored
// to where the ignore file lives.
type skillIgnore struct {
	rules []skillIgnoreRule
	seen  map[string]bool
}

func newSkillIgnore() *skillIgnore {
	return &skillIgnore{seen: map[string]bool{}}
}

// addRules reads the ignore files in dir and registers their (prefixed) patterns,
// mirroring TS addIgnoreRules (skills.ts:47-65).
func (s *skillIgnore) addRules(dir, root string) {
	relativeDir := skillRelPath(root, dir)
	prefix := ""
	if relativeDir != "" && relativeDir != "." {
		prefix = relativeDir + "/"
	}
	for _, filename := range skillIgnoreFileNames {
		ignorePath := filepath.Join(dir, filename)
		if s.seen[ignorePath] {
			continue
		}
		s.seen[ignorePath] = true
		data, err := os.ReadFile(ignorePath)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			pattern, ok := prefixIgnorePattern(line, prefix)
			if !ok {
				continue
			}
			if rule, ok := compileSkillIgnoreRule(pattern); ok {
				s.rules = append(s.rules, rule)
			}
		}
	}
}

// prefixIgnorePattern mirrors TS prefixIgnorePattern (skills.ts:24-45): blank and
// comment lines are dropped; negation (! / \!) and a leading slash are handled,
// then the pattern is prefixed with the directory's relative path.
func prefixIgnorePattern(line, prefix string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "\\#") {
		return "", false
	}

	pattern := line
	negated := false
	if strings.HasPrefix(pattern, "!") {
		negated = true
		pattern = pattern[1:]
	} else if strings.HasPrefix(pattern, "\\!") {
		pattern = pattern[1:]
	}
	pattern = strings.TrimPrefix(pattern, "/")

	prefixed := pattern
	if prefix != "" {
		prefixed = prefix + pattern
	}
	if negated {
		return "!" + prefixed, true
	}
	return prefixed, true
}

// compileSkillIgnoreRule compiles a (prefixed) gitignore pattern into a matcher.
func compileSkillIgnoreRule(pattern string) (skillIgnoreRule, bool) {
	rule := skillIgnoreRule{}
	if strings.HasPrefix(pattern, "!") {
		rule.negated = true
		pattern = pattern[1:]
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return rule, false
	}
	if strings.HasSuffix(pattern, "/") {
		rule.dirOnly = true
		pattern = strings.TrimSuffix(pattern, "/")
	}
	if pattern == "" {
		return rule, false
	}
	// A pattern containing a slash (not counting a trailing one) is anchored to
	// the scan root; otherwise it matches a basename at any depth.
	rule.anchored = strings.Contains(pattern, "/")
	rule.matchRe = regexp.MustCompile("^" + gitignoreGlobToRegex(pattern) + "$")
	return rule, true
}

// gitignoreGlobToRegex converts a gitignore glob (with **, *, ?) into a regex
// body. `**` matches across path separators; `*` and `?` do not.
func gitignoreGlobToRegex(pattern string) string {
	var b strings.Builder
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				b.WriteString(".*")
				i++
				// Consume a following slash so "**/" matches zero or more dirs.
				if i+1 < len(runes) && runes[i+1] == '/' {
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(runes[i])))
		}
	}
	return b.String()
}

// ignores reports whether relPath (POSIX-slashed, dirs suffixed with "/") is
// ignored by the accumulated rules. Later rules win, so a negation can re-include
// a previously-ignored path (gitignore semantics).
func (s *skillIgnore) ignores(relPath string) bool {
	isDir := strings.HasSuffix(relPath, "/")
	clean := strings.TrimSuffix(relPath, "/")
	base := clean
	if idx := strings.LastIndex(clean, "/"); idx >= 0 {
		base = clean[idx+1:]
	}
	ignored := false
	for _, rule := range s.rules {
		if rule.dirOnly && !isDir {
			continue
		}
		target := base
		if rule.anchored {
			target = clean
		}
		if rule.matchRe.MatchString(target) {
			ignored = !rule.negated
		}
	}
	return ignored
}

// loadSkills loads skills from a path. A SKILL.md / .md file path loads that one
// file; a directory is scanned with TS discovery rules (loadSkillsFromDir,
// skills.ts:168-275): if a directory contains SKILL.md it is a skill root and is
// not recursed into; otherwise its loose .md children are loaded at the scan
// root and subdirectories are recursed to arbitrary depth, honoring
// .gitignore/.ignore/.fdignore and skipping dotfiles and node_modules.
func (r *ResourceLoader) loadSkills(path string, source ResourceSourceInfo) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if !info.IsDir() {
		if strings.HasSuffix(strings.ToLower(path), ".md") {
			r.addSkill(filepath.Dir(path), path, withResourceSourcePath(source, path))
		}
		return
	}
	r.loadSkillsFromDir(path, path, newSkillIgnore(), true, source)
}

// loadSkillsFromDir mirrors TS loadSkillsFromDirInternal (skills.ts:173-275).
func (r *ResourceLoader) loadSkillsFromDir(dir, root string, ig *skillIgnore, includeRootFiles bool, source ResourceSourceInfo) {
	ig.addRules(dir, root)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// If a SKILL.md exists in this directory, treat it as a skill root and stop.
	for _, entry := range entries {
		if entry.Name() != "SKILL.md" {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		isFile, _ := skillEntryKind(entry, fullPath)
		if !isFile {
			continue
		}
		if ig.ignores(skillRelPath(root, fullPath)) {
			return
		}
		r.addSkill(dir, fullPath, withResourceSourcePath(source, fullPath))
		return
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isFile, isDir := skillEntryKind(entry, fullPath)
		if !isFile && !isDir {
			continue // broken symlink
		}

		relPath := skillRelPath(root, fullPath)
		ignorePath := relPath
		if isDir {
			ignorePath = relPath + "/"
		}
		if ig.ignores(ignorePath) {
			continue
		}

		if isDir {
			r.loadSkillsFromDir(fullPath, root, ig, false, source)
			continue
		}

		if !includeRootFiles || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		r.addSkill(dir, fullPath, withResourceSourcePath(source, fullPath))
	}
}

// skillEntryKind resolves whether a directory entry is a file or directory,
// following symlinks (TS uses statSync on symlinks). Returns (isFile, isDir).
func skillEntryKind(entry os.DirEntry, fullPath string) (bool, bool) {
	if entry.Type()&os.ModeSymlink != 0 {
		info, err := os.Stat(fullPath)
		if err != nil {
			return false, false
		}
		return info.Mode().IsRegular(), info.IsDir()
	}
	return entry.Type().IsRegular(), entry.IsDir()
}

// skillRelPath returns the POSIX-slashed path of target relative to root, used
// for ignore matching (TS toPosixPath(relative(root, fullPath))).
func skillRelPath(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	return filepath.ToSlash(rel)
}

func (r *ResourceLoader) addSkill(dir, skillPath string, source ResourceSourceInfo) {
	data, err := os.ReadFile(skillPath)
	if err != nil {
		r.Diagnostics = append(r.Diagnostics, cli.Diagnostic{Type: "warning", Message: err.Error()})
		return
	}
	content := string(data)
	fields, _ := parseFrontmatterFields(content)

	description := frontmatterString(fields, "description")
	parentDirName := filepath.Base(dir)

	for _, msg := range validateSkillDescription(description) {
		r.Diagnostics = append(r.Diagnostics, cli.Diagnostic{Type: "warning", Message: msg})
	}

	name := frontmatterString(fields, "name")
	if name == "" {
		name = parentDirName
	}

	for _, msg := range validateSkillName(name) {
		r.Diagnostics = append(r.Diagnostics, cli.Diagnostic{Type: "warning", Message: msg})
	}

	if strings.TrimSpace(description) == "" {
		return
	}

	disableModelInvocation := false
	if v, ok := fields["disable-model-invocation"].(bool); ok {
		disableModelInvocation = v
	}

	realPath := canonicalizeSkillPath(skillPath)
	if r.skillRealPaths == nil {
		r.skillRealPaths = map[string]bool{}
	}
	if r.skillRealPaths[realPath] {
		return
	}
	if existing, ok := r.Skills[name]; ok {
		r.Diagnostics = append(r.Diagnostics, cli.Diagnostic{
			Type:    "collision",
			Message: fmt.Sprintf("name %q collision: keeping %s, ignoring %s", name, existing.Path, skillPath),
		})
		return
	}

	r.Skills[name] = Skill{
		Name:                   name,
		Path:                   skillPath,
		BaseDir:                dir,
		Description:            description,
		Content:                content,
		DisableModelInvocation: disableModelInvocation,
		SourceInfo:             withResourceSourcePath(source, skillPath),
	}
	r.SkillOrder = append(r.SkillOrder, name)
	r.skillRealPaths[realPath] = true
}

// canonicalizeSkillPath resolves symlinks for realpath dedup, matching TS
// canonicalizePath (utils/paths.ts:28-34): on failure it returns the input.
func canonicalizeSkillPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

const (
	maxSkillNameLength        = 64
	maxSkillDescriptionLength = 1024
)

var skillNameCharsetRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// validateSkillName mirrors TS validateName (skills.ts:92-112).
func validateSkillName(name string) []string {
	var errs []string
	if len(name) > maxSkillNameLength {
		errs = append(errs, fmt.Sprintf("name exceeds %d characters (%d)", maxSkillNameLength, len(name)))
	}
	if !skillNameCharsetRe.MatchString(name) {
		errs = append(errs, "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		errs = append(errs, "name must not start or end with a hyphen")
	}
	if strings.Contains(name, "--") {
		errs = append(errs, "name must not contain consecutive hyphens")
	}
	return errs
}

// validateSkillDescription mirrors TS validateDescription (skills.ts:117-127).
func validateSkillDescription(description string) []string {
	var errs []string
	if strings.TrimSpace(description) == "" {
		errs = append(errs, "description is required")
	} else if len(description) > maxSkillDescriptionLength {
		errs = append(errs, fmt.Sprintf("description exceeds %d characters (%d)", maxSkillDescriptionLength, len(description)))
	}
	return errs
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
	collectSkillDir(root, root, newSkillIgnore(), true, &paths)
	return uniqueResourcePaths(paths)
}

// collectSkillDir mirrors loadSkillsFromDir's TS-faithful walk (skills.ts:173-275)
// but collects SKILL.md/loose-.md file paths instead of loading skills, so the
// auto and package skill-discovery paths gain ignore-file handling
// (.gitignore/.ignore/.fdignore) and the stop-at-SKILL.md rule that the CLI
// --skills path already had (P2-21 auto path). Loose .md children are collected
// only at the scan root (includeRootFiles); dotfiles and node_modules are skipped.
func collectSkillDir(dir, root string, ig *skillIgnore, includeRootFiles bool, out *[]string) {
	ig.addRules(dir, root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() != "SKILL.md" {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		if isFile, _ := skillEntryKind(entry, fullPath); !isFile {
			continue
		}
		if ig.ignores(skillRelPath(root, fullPath)) {
			return
		}
		*out = append(*out, filepath.Clean(fullPath))
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		isFile, isDir := skillEntryKind(entry, fullPath)
		if !isFile && !isDir {
			continue
		}
		ignorePath := skillRelPath(root, fullPath)
		if isDir {
			ignorePath += "/"
		}
		if ig.ignores(ignorePath) {
			continue
		}
		if isDir {
			collectSkillDir(fullPath, root, ig, false, out)
		} else if includeRootFiles && strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			*out = append(*out, filepath.Clean(fullPath))
		}
	}
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

// extractFrontmatter splits YAML frontmatter from the body, mirroring TS
// extractFrontmatter (utils/frontmatter.ts:10-26): newlines are normalized; a
// document beginning with "---" with a later "\n---" delimiter yields the YAML
// between them and a trimmed body, otherwise the whole (normalized) content is
// the body with no frontmatter.
func extractFrontmatter(content string) (yamlString string, body string) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if !strings.HasPrefix(normalized, "---") {
		return "", normalized
	}
	endIndex := strings.Index(normalized[3:], "\n---")
	if endIndex == -1 {
		return "", normalized
	}
	endIndex += 3
	return normalized[4:endIndex], strings.TrimSpace(normalized[endIndex+4:])
}

// parseFrontmatterFields parses the YAML frontmatter into a string/bool keyed
// map plus the body. It mirrors TS parseFrontmatter (utils/frontmatter.ts) for
// the simple "key: value" scalar lines that prompt-template and SKILL.md
// frontmatter use (the same simple-YAML subset utils.go's parseSimpleYAML
// supports): quoted strings are unquoted, true/false become booleans, and other
// scalars stay as raw strings.
func parseFrontmatterFields(content string) (fields map[string]any, body string) {
	yamlString, body := extractFrontmatter(content)
	fields = map[string]any{}
	if yamlString == "" {
		return fields, body
	}
	for _, line := range strings.Split(yamlString, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = parseFrontmatterScalar(strings.TrimSpace(value))
	}
	return fields, body
}

// parseFrontmatterScalar parses a single frontmatter scalar value, matching the
// subset of YAML used by utils.go parseYAMLScalar that prompt-template / SKILL.md
// frontmatter rely on: quoted strings, booleans, and bare strings.
func parseFrontmatterScalar(value string) any {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	switch strings.ToLower(value) {
	case "true":
		return true
	case "false":
		return false
	}
	return value
}

// frontmatterString returns the trimmed string value for key, or "" when absent
// or not a string.
func frontmatterString(fields map[string]any, key string) string {
	if v, ok := fields[key].(string); ok {
		return v
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
