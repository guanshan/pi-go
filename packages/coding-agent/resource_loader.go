package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	core "github.com/guanshan/pi-go/packages/coding-agent/core"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

type ResourcePathEntry struct {
	Path     string       `json:"path"`
	Metadata PathMetadata `json:"metadata"`
}

type ResourceExtensionPaths struct {
	SkillPaths  []ResourcePathEntry `json:"skillPaths,omitempty"`
	PromptPaths []ResourcePathEntry `json:"promptPaths,omitempty"`
	ThemePaths  []ResourcePathEntry `json:"themePaths,omitempty"`
}

type LoadedExtension struct {
	Path       string                   `json:"path"`
	Tools      []coreext.ToolDefinition `json:"tools,omitempty"`
	Commands   []core.SlashCommandInfo  `json:"commands,omitempty"`
	SourceInfo SourceInfo               `json:"sourceInfo,omitempty"`
}

type LoadExtensionsResult struct {
	Extensions []LoadedExtension `json:"extensions"`
	Errors     []cli.Diagnostic  `json:"errors"`
	Runtime    *ExtensionRunner  `json:"-"`
}

type SkillsResult struct {
	Skills      []core.Skill     `json:"skills"`
	Diagnostics []cli.Diagnostic `json:"diagnostics"`
}

type PromptsResult struct {
	Prompts     []core.PromptTemplate `json:"prompts"`
	Diagnostics []cli.Diagnostic      `json:"diagnostics"`
}

type Theme struct {
	Name       string         `json:"name"`
	Path       string         `json:"path"`
	SourcePath string         `json:"sourcePath,omitempty"`
	Raw        string         `json:"raw,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
	SourceInfo SourceInfo     `json:"sourceInfo,omitempty"`
}

type ThemesResult struct {
	Themes      []Theme          `json:"themes"`
	Diagnostics []cli.Diagnostic `json:"diagnostics"`
}

type AgentContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type AgentsFilesResult struct {
	AgentsFiles []AgentContextFile `json:"agentsFiles"`
}

type ProjectContextFilesOptions struct {
	CWD      string
	AgentDir string
}

type DefaultResourceLoaderOptions struct {
	CWD                           string
	AgentDir                      string
	Settings                      *core.SettingsManager
	SettingsManager               *core.SettingsManager
	EventBus                      *coreext.EventBus
	AdditionalExtensionPaths      []string
	AdditionalSkillPaths          []string
	AdditionalPromptTemplatePaths []string
	AdditionalThemePaths          []string
	ExtensionFactories            []ExtensionFactory
	NoExtensions                  bool
	NoSkills                      bool
	NoPromptTemplates             bool
	NoThemes                      bool
	NoContextFiles                bool
	SystemPrompt                  string
	AppendSystemPrompt            []string
	ExtensionsOverride            func(LoadExtensionsResult) LoadExtensionsResult
	SkillsOverride                func(SkillsResult) SkillsResult
	PromptsOverride               func(PromptsResult) PromptsResult
	ThemesOverride                func(ThemesResult) ThemesResult
	AgentsFilesOverride           func(AgentsFilesResult) AgentsFilesResult
	SystemPromptOverride          func(*string) *string
	AppendSystemPromptOverride    func([]string) []string
}

type DefaultResourceLoader struct {
	mu                            sync.RWMutex
	cwd                           string
	agentDir                      string
	settings                      *core.SettingsManager
	eventBus                      *coreext.EventBus
	additionalExtensionPaths      []string
	additionalSkillPaths          []string
	additionalPromptTemplatePaths []string
	additionalThemePaths          []string
	extensionFactories            []ExtensionFactory
	noExtensions                  bool
	noSkills                      bool
	noPromptTemplates             bool
	noThemes                      bool
	noContextFiles                bool
	systemPromptSource            string
	appendSystemPromptSource      []string
	extensionsOverride            func(LoadExtensionsResult) LoadExtensionsResult
	skillsOverride                func(SkillsResult) SkillsResult
	promptsOverride               func(PromptsResult) PromptsResult
	themesOverride                func(ThemesResult) ThemesResult
	agentsFilesOverride           func(AgentsFilesResult) AgentsFilesResult
	systemPromptOverride          func(*string) *string
	appendSystemPromptOverride    func([]string) []string
	base                          core.ResourceLoader
	extensions                    LoadExtensionsResult
	skills                        SkillsResult
	prompts                       PromptsResult
	themes                        ThemesResult
	agentsFiles                   AgentsFilesResult
	systemPrompt                  *string
	appendSystemPrompt            []string
}

func NewDefaultResourceLoader(options DefaultResourceLoaderOptions) (*DefaultResourceLoader, error) {
	cwd := options.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	agentDir := options.AgentDir
	if agentDir == "" {
		agentDir = core.AgentDir()
	}
	settings := options.Settings
	if settings == nil {
		settings = options.SettingsManager
	}
	if settings == nil {
		settings = core.NewSettingsManager(cwd, agentDir)
	}
	eventBus := options.EventBus
	if eventBus == nil {
		eventBus = NewEventBus()
	}
	loader := &DefaultResourceLoader{
		cwd:                           filepath.Clean(cwd),
		agentDir:                      filepath.Clean(agentDir),
		settings:                      settings,
		eventBus:                      eventBus,
		additionalExtensionPaths:      append([]string(nil), options.AdditionalExtensionPaths...),
		additionalSkillPaths:          append([]string(nil), options.AdditionalSkillPaths...),
		additionalPromptTemplatePaths: append([]string(nil), options.AdditionalPromptTemplatePaths...),
		additionalThemePaths:          append([]string(nil), options.AdditionalThemePaths...),
		extensionFactories:            append([]ExtensionFactory(nil), options.ExtensionFactories...),
		noExtensions:                  options.NoExtensions,
		noSkills:                      options.NoSkills,
		noPromptTemplates:             options.NoPromptTemplates,
		noThemes:                      options.NoThemes,
		noContextFiles:                options.NoContextFiles,
		systemPromptSource:            options.SystemPrompt,
		appendSystemPromptSource:      append([]string(nil), options.AppendSystemPrompt...),
		extensionsOverride:            options.ExtensionsOverride,
		skillsOverride:                options.SkillsOverride,
		promptsOverride:               options.PromptsOverride,
		themesOverride:                options.ThemesOverride,
		agentsFilesOverride:           options.AgentsFilesOverride,
		systemPromptOverride:          options.SystemPromptOverride,
		appendSystemPromptOverride:    options.AppendSystemPromptOverride,
	}
	loader.Reload()
	return loader, nil
}

func (l *DefaultResourceLoader) GetExtensions() LoadExtensionsResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneLoadExtensionsResult(l.extensions)
}

func (l *DefaultResourceLoader) GetSkills() SkillsResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneSkillsResult(l.skills)
}

func (l *DefaultResourceLoader) GetPrompts() PromptsResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return clonePromptsResult(l.prompts)
}

func (l *DefaultResourceLoader) GetThemes() ThemesResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneThemesResult(l.themes)
}

func (l *DefaultResourceLoader) GetAgentsFiles() AgentsFilesResult {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneAgentsFilesResult(l.agentsFiles)
}

func (l *DefaultResourceLoader) GetSystemPrompt() *string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.systemPrompt == nil {
		return nil
	}
	value := *l.systemPrompt
	return &value
}

func (l *DefaultResourceLoader) GetAppendSystemPrompt() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string(nil), l.appendSystemPrompt...)
}

func (l *DefaultResourceLoader) ExtendResources(paths ResourceExtensionPaths) {
	l.mu.Lock()
	for _, entry := range paths.SkillPaths {
		l.additionalSkillPaths = append(l.additionalSkillPaths, l.resolveResourcePathWithMetadata(entry))
	}
	for _, entry := range paths.PromptPaths {
		l.additionalPromptTemplatePaths = append(l.additionalPromptTemplatePaths, l.resolveResourcePathWithMetadata(entry))
	}
	for _, entry := range paths.ThemePaths {
		l.additionalThemePaths = append(l.additionalThemePaths, l.resolveResourcePathWithMetadata(entry))
	}
	l.mu.Unlock()
	l.Reload()
}

func (l *DefaultResourceLoader) Reload() {
	working := l.snapshotForReload()
	packageManager := NewDefaultPackageManager(working.cwd, working.agentDir, working.settings)
	resolved, _ := packageManager.Resolve()
	extensionSources, _ := packageManager.ResolveExtensionSources(working.additionalExtensionPaths, ResolveExtensionSourcesOptions{Temporary: true})
	extensionPaths := enabledResourcePaths(extensionSources.Extensions)
	if !working.noExtensions {
		extensionPaths = append(reverseStrings(enabledResourcePaths(resolved.Extensions)), extensionPaths...)
	}
	skillPaths := append([]string{}, enabledResourcePaths(extensionSources.Skills)...)
	if !working.noSkills {
		skillPaths = append(reverseStrings(enabledResourcePaths(resolved.Skills)), skillPaths...)
	}
	skillPaths = append(skillPaths, working.resolvePathsInOrder(working.additionalSkillPaths)...)
	promptPaths := append([]string{}, enabledResourcePaths(extensionSources.Prompts)...)
	if !working.noPromptTemplates {
		promptPaths = append(reverseStrings(enabledResourcePaths(resolved.Prompts)), promptPaths...)
	}
	promptPaths = append(promptPaths, working.resolvePathsInOrder(working.additionalPromptTemplatePaths)...)
	themePaths := append([]string{}, enabledResourcePaths(extensionSources.Themes)...)
	if !working.noThemes {
		themePaths = append(reverseStrings(enabledResourcePaths(resolved.Themes)), themePaths...)
	}
	themePaths = append(themePaths, working.resolvePathsInOrder(working.additionalThemePaths)...)

	args := cli.Args{
		NoExtensions:      true,
		NoSkills:          true,
		NoPromptTemplates: true,
		NoThemes:          true,
		NoContextFiles:    working.noContextFiles,
		Extensions:        uniqueStringsPreserveOrder(extensionPaths),
		Skills:            uniqueStringsPreserveOrder(skillPaths),
		PromptTemplates:   uniqueStringsPreserveOrder(promptPaths),
		Themes:            uniqueStringsPreserveOrder(themePaths),
	}
	base := core.LoadResources(working.cwd, working.agentDir, args, working.settings)
	working.base = base

	extensions := LoadExtensionsResult{
		Extensions: make([]LoadedExtension, 0, len(base.Extensions)+len(working.extensionFactories)),
		Errors:     working.missingPathDiagnostics(working.additionalExtensionPaths, "Extension"),
		Runtime:    newExtensionRunnerWithAPI(newExtensionAPIWithBus(working.eventBus)),
	}
	for _, path := range base.Extensions {
		extensions.Extensions = append(extensions.Extensions, LoadedExtension{Path: path, SourceInfo: working.defaultSourceInfoForPath(path)})
	}
	inlineExtensions, inlineErrors := working.loadExtensionFactories(extensions.Runtime)
	extensions.Extensions = append(extensions.Extensions, inlineExtensions...)
	extensions.Errors = append(extensions.Errors, inlineErrors...)
	if working.extensionsOverride != nil {
		extensions = working.extensionsOverride(extensions)
	}
	working.extensions = extensions

	skills := SkillsResult{
		Skills:      mapSkills(base.Skills),
		Diagnostics: append(append([]cli.Diagnostic(nil), base.Diagnostics...), working.missingPathDiagnostics(working.additionalSkillPaths, "Skill")...),
	}
	if working.skillsOverride != nil {
		skills = working.skillsOverride(skills)
	}
	working.skills = skills

	prompts := PromptsResult{
		Prompts:     mapPrompts(base.PromptTemplates),
		Diagnostics: append(append([]cli.Diagnostic(nil), base.Diagnostics...), working.missingPathDiagnostics(working.additionalPromptTemplatePaths, "Prompt template")...),
	}
	if working.promptsOverride != nil {
		prompts = working.promptsOverride(prompts)
	}
	working.prompts = prompts

	themes := ThemesResult{
		Themes:      loadThemes(base.Themes),
		Diagnostics: append(append([]cli.Diagnostic(nil), base.Diagnostics...), working.missingPathDiagnostics(working.additionalThemePaths, "Theme")...),
	}
	if working.themesOverride != nil {
		themes = working.themesOverride(themes)
	}
	working.themes = themes

	files := AgentsFilesResult{}
	if !working.noContextFiles {
		files.AgentsFiles = LoadProjectContextFiles(ProjectContextFilesOptions{CWD: working.cwd, AgentDir: working.agentDir})
	}
	if working.agentsFilesOverride != nil {
		files = working.agentsFilesOverride(files)
	}
	working.agentsFiles = files

	working.systemPrompt = working.resolveSystemPrompt(base)
	appendPrompts := working.resolveAppendSystemPrompt(base)
	if working.appendSystemPromptOverride != nil {
		appendPrompts = working.appendSystemPromptOverride(appendPrompts)
	}
	working.appendSystemPrompt = append([]string(nil), appendPrompts...)

	l.mu.Lock()
	l.base = cloneCoreResourceLoader(working.base)
	l.extensions = cloneLoadExtensionsResult(working.extensions)
	l.skills = cloneSkillsResult(working.skills)
	l.prompts = clonePromptsResult(working.prompts)
	l.themes = cloneThemesResult(working.themes)
	l.agentsFiles = cloneAgentsFilesResult(working.agentsFiles)
	if working.systemPrompt != nil {
		value := *working.systemPrompt
		l.systemPrompt = &value
	} else {
		l.systemPrompt = nil
	}
	l.appendSystemPrompt = append([]string(nil), working.appendSystemPrompt...)
	l.mu.Unlock()
}

func (l *DefaultResourceLoader) Base() core.ResourceLoader {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneCoreResourceLoader(l.base)
}

func (l *DefaultResourceLoader) resolveSystemPrompt(base core.ResourceLoader) *string {
	var prompt *string
	source := l.systemPromptSource
	if source == "" {
		source = l.discoverSystemPromptFile()
	}
	if source != "" {
		value := resolvePromptInput(l.cwd, source)
		prompt = &value
	} else if strings.TrimSpace(base.SystemPrompt) != "" {
		value := base.SystemPrompt
		prompt = &value
	}
	if l.systemPromptOverride != nil {
		return l.systemPromptOverride(prompt)
	}
	return prompt
}

func (l *DefaultResourceLoader) resolveAppendSystemPrompt(base core.ResourceLoader) []string {
	var out []string
	sources := append([]string(nil), l.appendSystemPromptSource...)
	if len(sources) == 0 {
		if source := l.discoverAppendSystemPromptFile(); source != "" {
			sources = append(sources, source)
		}
	}
	for _, source := range sources {
		if strings.TrimSpace(source) == "" {
			continue
		}
		out = append(out, resolvePromptInput(l.cwd, source))
	}
	if len(out) == 0 && len(sources) == 0 && strings.TrimSpace(base.AppendPrompt) != "" {
		out = append(out, base.AppendPrompt)
	}
	return out
}

func (l *DefaultResourceLoader) resolvePathsInOrder(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, l.resolveResourcePath(path))
	}
	return uniqueStringsPreserveOrder(out)
}

func (l *DefaultResourceLoader) resolveResourcePath(path string) string {
	return ResolveInputPath(path, l.cwd, PathInputOptions{Trim: true, NormalizeUnicodeSpaces: true})
}

func (l *DefaultResourceLoader) resolveResourcePathWithMetadata(entry ResourcePathEntry) string {
	baseDir := entry.Metadata.BaseDir
	if baseDir != "" && !filepath.IsAbs(entry.Path) {
		return ResolveInputPath(entry.Path, l.resolveResourcePath(baseDir), PathInputOptions{Trim: true, NormalizeUnicodeSpaces: true})
	}
	return l.resolveResourcePath(entry.Path)
}

func (l *DefaultResourceLoader) discoverSystemPromptFile() string {
	for _, path := range []string{
		filepath.Join(l.cwd, ConfigDirName, "SYSTEM.md"),
		filepath.Join(l.agentDir, "SYSTEM.md"),
	} {
		if fileExistsLocal(path) {
			return path
		}
	}
	return ""
}

func (l *DefaultResourceLoader) discoverAppendSystemPromptFile() string {
	for _, path := range []string{
		filepath.Join(l.cwd, ConfigDirName, "APPEND_SYSTEM.md"),
		filepath.Join(l.agentDir, "APPEND_SYSTEM.md"),
	} {
		if fileExistsLocal(path) {
			return path
		}
	}
	return ""
}

func (l *DefaultResourceLoader) loadExtensionFactories(runtime *ExtensionRunner) ([]LoadedExtension, []cli.Diagnostic) {
	var extensions []LoadedExtension
	var diagnostics []cli.Diagnostic
	for index, factory := range l.extensionFactories {
		path := fmt.Sprintf("<inline:%d>", index+1)
		api := newExtensionAPIWithBus(l.eventBus)
		if err := factory(api); err != nil {
			diagnostics = append(diagnostics, cli.Diagnostic{Type: "error", Message: fmt.Sprintf("%s: %v", path, err)})
			continue
		}
		extensions = append(extensions, LoadedExtension{
			Path:       path,
			Tools:      api.snapshotTools(),
			Commands:   api.snapshotCommands(),
			SourceInfo: CreateSyntheticSourceInfo(path),
		})
		if runtime != nil && runtime.API != nil {
			for _, handler := range api.snapshotShutdownHandlers() {
				runtime.API.OnShutdown(handler)
			}
		}
	}
	return extensions, diagnostics
}

func (l *DefaultResourceLoader) snapshotForReload() *DefaultResourceLoader {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return &DefaultResourceLoader{
		cwd:                           l.cwd,
		agentDir:                      l.agentDir,
		settings:                      l.settings,
		eventBus:                      l.eventBus,
		additionalExtensionPaths:      append([]string(nil), l.additionalExtensionPaths...),
		additionalSkillPaths:          append([]string(nil), l.additionalSkillPaths...),
		additionalPromptTemplatePaths: append([]string(nil), l.additionalPromptTemplatePaths...),
		additionalThemePaths:          append([]string(nil), l.additionalThemePaths...),
		extensionFactories:            append([]ExtensionFactory(nil), l.extensionFactories...),
		noExtensions:                  l.noExtensions,
		noSkills:                      l.noSkills,
		noPromptTemplates:             l.noPromptTemplates,
		noThemes:                      l.noThemes,
		noContextFiles:                l.noContextFiles,
		systemPromptSource:            l.systemPromptSource,
		appendSystemPromptSource:      append([]string(nil), l.appendSystemPromptSource...),
		extensionsOverride:            l.extensionsOverride,
		skillsOverride:                l.skillsOverride,
		promptsOverride:               l.promptsOverride,
		themesOverride:                l.themesOverride,
		agentsFilesOverride:           l.agentsFilesOverride,
		systemPromptOverride:          l.systemPromptOverride,
		appendSystemPromptOverride:    l.appendSystemPromptOverride,
	}
}

func cloneLoadExtensionsResult(input LoadExtensionsResult) LoadExtensionsResult {
	result := LoadExtensionsResult{Errors: append([]cli.Diagnostic(nil), input.Errors...), Runtime: input.Runtime}
	if len(input.Extensions) == 0 {
		return result
	}
	result.Extensions = make([]LoadedExtension, len(input.Extensions))
	for i, extension := range input.Extensions {
		result.Extensions[i] = extension
		result.Extensions[i].Tools = append([]coreext.ToolDefinition(nil), extension.Tools...)
		result.Extensions[i].Commands = append([]core.SlashCommandInfo(nil), extension.Commands...)
	}
	return result
}

func cloneSkillsResult(input SkillsResult) SkillsResult {
	return SkillsResult{Skills: append([]core.Skill(nil), input.Skills...), Diagnostics: append([]cli.Diagnostic(nil), input.Diagnostics...)}
}

func clonePromptsResult(input PromptsResult) PromptsResult {
	return PromptsResult{Prompts: append([]core.PromptTemplate(nil), input.Prompts...), Diagnostics: append([]cli.Diagnostic(nil), input.Diagnostics...)}
}

func cloneThemesResult(input ThemesResult) ThemesResult {
	return ThemesResult{Themes: append([]Theme(nil), input.Themes...), Diagnostics: append([]cli.Diagnostic(nil), input.Diagnostics...)}
}

func cloneAgentsFilesResult(input AgentsFilesResult) AgentsFilesResult {
	return AgentsFilesResult{AgentsFiles: append([]AgentContextFile(nil), input.AgentsFiles...)}
}

func cloneCoreResourceLoader(input core.ResourceLoader) core.ResourceLoader {
	cloned := input
	cloned.ContextFiles = append([]string(nil), input.ContextFiles...)
	cloned.Themes = append([]string(nil), input.Themes...)
	cloned.Extensions = append([]string(nil), input.Extensions...)
	cloned.Diagnostics = append([]cli.Diagnostic(nil), input.Diagnostics...)
	if input.PromptTemplates != nil {
		cloned.PromptTemplates = make(map[string]core.PromptTemplate, len(input.PromptTemplates))
		for key, value := range input.PromptTemplates {
			cloned.PromptTemplates[key] = value
		}
	}
	if input.Skills != nil {
		cloned.Skills = make(map[string]core.Skill, len(input.Skills))
		for key, value := range input.Skills {
			cloned.Skills[key] = value
		}
	}
	return cloned
}

func (l *DefaultResourceLoader) missingPathDiagnostics(paths []string, resourceType string) []cli.Diagnostic {
	seen := map[string]bool{}
	var diagnostics []cli.Diagnostic
	for _, path := range paths {
		if !shouldCheckLocalPath(path) {
			continue
		}
		resolved := l.resolveResourcePath(path)
		if resolved == "" || seen[resolved] || fileExistsLocal(resolved) {
			continue
		}
		seen[resolved] = true
		diagnostics = append(diagnostics, cli.Diagnostic{
			Type:    "error",
			Message: fmt.Sprintf("%s path does not exist: %s", resourceType, resolved),
		})
	}
	return diagnostics
}

func (l *DefaultResourceLoader) defaultSourceInfoForPath(path string) SourceInfo {
	baseDir := filepath.Dir(path)
	return SourceInfo{
		Path:    path,
		Source:  "local",
		Scope:   l.scopeForPath(path),
		Origin:  "top-level",
		BaseDir: baseDir,
	}
}

func (l *DefaultResourceLoader) scopeForPath(path string) string {
	resolved := filepath.Clean(path)
	if isUnderPath(resolved, filepath.Join(l.agentDir, "extensions")) ||
		isUnderPath(resolved, filepath.Join(l.agentDir, "skills")) ||
		isUnderPath(resolved, filepath.Join(l.agentDir, "prompts")) ||
		isUnderPath(resolved, filepath.Join(l.agentDir, "themes")) {
		return "user"
	}
	if isUnderPath(resolved, filepath.Join(l.cwd, ConfigDirName)) {
		return "project"
	}
	return "temporary"
}

func mapSkills(input map[string]core.Skill) []core.Skill {
	out := make([]core.Skill, 0, len(input))
	for _, skill := range input {
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func mapPrompts(input map[string]core.PromptTemplate) []core.PromptTemplate {
	out := make([]core.PromptTemplate, 0, len(input))
	for _, prompt := range input {
		out = append(out, prompt)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func loadThemes(paths []string) []Theme {
	out := make([]Theme, 0, len(paths))
	for _, path := range uniqueStrings(paths) {
		theme := Theme{Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Path: path, SourcePath: path}
		if raw, err := os.ReadFile(path); err == nil {
			theme.Raw = string(raw)
			var config map[string]any
			if json.Unmarshal(raw, &config) == nil {
				theme.Config = config
				if name, ok := config["name"].(string); ok && strings.TrimSpace(name) != "" {
					theme.Name = strings.TrimSpace(name)
				}
			}
		}
		out = append(out, theme)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func resolvePromptInput(cwd, value string) string {
	path := ResolveInputPath(value, cwd, PathInputOptions{Trim: true, NormalizeUnicodeSpaces: true})
	if raw, err := os.ReadFile(path); err == nil {
		return string(raw)
	}
	return value
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func uniqueStringsPreserveOrder(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func enabledResourcePaths(resources []ResolvedResource) []string {
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		if resource.Enabled {
			out = append(out, resource.Path)
		}
	}
	return out
}

func reverseStrings(values []string) []string {
	out := append([]string(nil), values...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func LoadProjectContextFiles(options ProjectContextFilesOptions) []AgentContextFile {
	cwdInput := options.CWD
	if cwdInput == "" {
		cwdInput, _ = os.Getwd()
	}
	agentDirInput := options.AgentDir
	if agentDirInput == "" {
		agentDirInput = core.AgentDir()
	}
	cwd := ResolveInputPath(cwdInput, "", PathInputOptions{})
	agentDir := ResolveInputPath(agentDirInput, "", PathInputOptions{})
	var files []AgentContextFile
	seen := map[string]bool{}
	if file, ok := loadContextFileFromDir(agentDir); ok {
		files = append(files, file)
		seen[file.Path] = true
	}
	var ancestorFiles []AgentContextFile
	for _, dir := range contextAncestorDirs(cwd) {
		file, ok := loadContextFileFromDir(dir)
		if !ok || seen[file.Path] {
			continue
		}
		ancestorFiles = append(ancestorFiles, file)
		seen[file.Path] = true
	}
	files = append(files, ancestorFiles...)
	return files
}

func loadContextFileFromDir(dir string) (AgentContextFile, bool) {
	for _, name := range []string{"AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"} {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return AgentContextFile{Path: path, Content: string(raw)}, true
	}
	return AgentContextFile{}, false
}

func contextAncestorDirs(cwd string) []string {
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

func shouldCheckLocalPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if strings.Contains(path, "://") {
		return false
	}
	if strings.HasPrefix(path, "npm:") || strings.HasPrefix(path, "github:") {
		return false
	}
	return true
}

func fileExistsLocal(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isUnderPath(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != "" && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}
