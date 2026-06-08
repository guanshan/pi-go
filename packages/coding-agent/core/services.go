package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

type DiagnosticType string

const (
	DiagInfo    DiagnosticType = "info"
	DiagWarning DiagnosticType = "warning"
	DiagError   DiagnosticType = "error"
)

type Diagnostic struct {
	Type    DiagnosticType
	Message string
}

type DefaultResourceLoaderOptions struct {
	AdditionalExtensionPaths      []string
	AdditionalSkillPaths          []string
	AdditionalPromptTemplatePaths []string
	AdditionalThemePaths          []string
	ExtensionFactories            []coreext.Factory
	NoExtensions                  bool
	NoSkills                      bool
	NoPromptTemplates             bool
	NoThemes                      bool
	NoContextFiles                bool
	SystemPrompt                  string
	AppendSystemPrompt            []string
}

type AgentSessionServices struct {
	Cwd                   string
	AgentDir              string
	AuthStorage           *ai.AuthStorage
	SettingsManager       *SettingsManager
	ModelRegistry         *ai.ModelRegistry
	ResourceLoader        ResourceLoader
	Theme                 ResolvedTheme
	Keybindings           *KeybindingsManager
	ExtensionRuntime      *coreext.Runner
	ResourceLoaderOptions DefaultResourceLoaderOptions
	Diagnostics           []Diagnostic
}

type CreateAgentSessionServicesOptions struct {
	Cwd                   string
	AgentDir              string
	AuthStorage           *ai.AuthStorage
	SettingsManager       *SettingsManager
	ModelRegistry         *ai.ModelRegistry
	ExtensionFlagValues   map[string]any
	ResourceLoaderOptions DefaultResourceLoaderOptions
}

type CreateAgentSessionFromServicesOptions struct {
	Services       *AgentSessionServices
	SessionManager *SessionManager
	Model          ai.Model
	ThinkingLevel  ai.ThinkingLevel
	ScopedModels   []ScopedModel
	Tools          []string
	ExcludeTools   []string
	NoTools        NoToolsMode
	// CustomTools are caller-supplied tools merged on top of the builtin and
	// extension tool sets, mirroring the TS createAgentSession `customTools`
	// option. They share the Tools allowlist / ExcludeTools / NoTools handling.
	CustomTools ToolSet
}

func CreateAgentSessionServices(ctx context.Context, options CreateAgentSessionServicesOptions) (*AgentSessionServices, error) {
	_ = ctx
	cwd, agentDir, err := resolveServicePaths(options.Cwd, options.AgentDir)
	if err != nil {
		return nil, err
	}
	settings := options.SettingsManager
	if settings == nil {
		settings = NewSettingsManager(cwd, agentDir)
	}
	auth := options.AuthStorage
	if auth == nil {
		auth = ai.NewAuthStorage(agentDir)
	}
	registry := options.ModelRegistry
	if registry == nil {
		registry = ai.NewModelRegistry(agentDir, auth)
	}
	resources := LoadResources(cwd, agentDir, resourceLoaderArgs(options.ResourceLoaderOptions), settings)
	applyResourceLoaderOverrides(&resources, cwd, options.ResourceLoaderOptions)
	diagnostics := append(settingsDiagnostics(settings), resourceDiagnostics(resources)...)
	theme, themeDiagnostics := ResolveTheme(settings, resources)
	diagnostics = append(diagnostics, themeDiagnostics...)
	keybindings := NewKeybindingsManager(agentDir)
	diagnostics = append(diagnostics, keybindings.Diagnostics()...)
	extensionRuntime, extensionDiagnostics := loadExtensionRuntime(ctx, resources.Extensions, options.ResourceLoaderOptions.ExtensionFactories, options.ExtensionFlagValues)
	diagnostics = append(diagnostics, extensionDiagnostics...)
	for _, provider := range extensionRuntime.RegisteredProviders() {
		if _, err := applyProviderModelConfigToRegistry(registry, provider, true); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Type: DiagError, Message: err.Error()})
		}
	}
	_, shortcutDiagnostics := resolveExtensionShortcuts(extensionRuntime, keybindings)
	diagnostics = append(diagnostics, shortcutDiagnostics...)
	return &AgentSessionServices{
		Cwd:                   cwd,
		AgentDir:              agentDir,
		AuthStorage:           auth,
		SettingsManager:       settings,
		ModelRegistry:         registry,
		ResourceLoader:        resources,
		Theme:                 theme,
		Keybindings:           keybindings,
		ExtensionRuntime:      extensionRuntime,
		ResourceLoaderOptions: options.ResourceLoaderOptions,
		Diagnostics:           diagnostics,
	}, nil
}

func CreateAgentSessionFromServices(ctx context.Context, options CreateAgentSessionFromServicesOptions) (CreateAgentSessionResult, error) {
	_ = ctx
	services := options.Services
	if services == nil {
		return CreateAgentSessionResult{}, fmt.Errorf("services are required")
	}
	session := options.SessionManager
	if session == nil {
		session = InMemorySession(services.Cwd)
	}
	sessionCtx := session.BuildContext()
	model := options.Model
	thinking := options.ThinkingLevel
	modelFallbackMessage := ""
	if model.Provider == "" && len(options.ScopedModels) > 0 {
		model = options.ScopedModels[0].Model
		if thinking == "" {
			thinking = options.ScopedModels[0].ThinkingLevel
		}
	}
	if model.Provider == "" {
		selected, ok, warning := InitialModel(services.ModelRegistry, cli.Args{}, services.SettingsManager)
		if ok {
			model = selected
		}
		modelFallbackMessage = warning
	}
	if model.Provider == "" {
		model, _, _ = services.ModelRegistry.Match("faux", "faux")
	}
	if thinking == "" {
		if len(sessionCtx.Messages) > 0 && sessionBranchHasThinkingChange(session) && sessionCtx.ThinkingLevel != "" {
			thinking = sessionCtx.ThinkingLevel
		} else {
			thinking = services.SettingsManager.DefaultThinkingLevel()
		}
	}
	tools := toolSetFromServiceOptions(services.Cwd, services.SettingsManager, options.NoTools, options.Tools, options.ExcludeTools, services.ExtensionRuntime, options.CustomTools, model)
	systemPrompt := services.ResourceLoader.BuildSystemPrompt(cli.Args{}, ToolPromptInfoFor(tools))
	agentSession := NewAgentSession(session, services.SettingsManager, services.ModelRegistry, services.ResourceLoader, model, thinking, tools, systemPrompt)
	if services.Theme.Name != "" {
		agentSession.Theme = services.Theme
	}
	if services.Keybindings != nil {
		agentSession.Keybindings = services.Keybindings
	}
	agentSession.extensionRuntime = services.ExtensionRuntime
	agentSession.ResourceLoaderOptions = services.ResourceLoaderOptions
	agentSession.installExtensionContextBridge()
	return CreateAgentSessionResult{
		Session:              agentSession,
		ModelFallbackMessage: modelFallbackMessage,
	}, nil
}

// formatNoModelsAvailableMessage is the guidance shown when no model can be
// resolved (no API keys configured, no models.json, etc.). It mirrors TS
// formatNoModelsAvailableMessage / getProviderLoginHelp in
// core/auth-guidance.ts, adapted to the Go port (which has no docs path).
func formatNoModelsAvailableMessage() string {
	return "No models available. Use /login to log into a provider via OAuth or API key, or configure API keys or models.json and try again."
}

func resolveServicePaths(cwd, agentDir string) (string, string, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	resolvedCWD, err := AbsPath(cwd)
	if err != nil {
		return "", "", err
	}
	if agentDir == "" {
		agentDir = AgentDir()
	}
	resolvedAgentDir, err := AbsPath(agentDir)
	if err != nil {
		return "", "", err
	}
	return resolvedCWD, resolvedAgentDir, nil
}

func resourceLoaderArgs(options DefaultResourceLoaderOptions) cli.Args {
	return cli.Args{
		Extensions:         append([]string(nil), options.AdditionalExtensionPaths...),
		Skills:             append([]string(nil), options.AdditionalSkillPaths...),
		PromptTemplates:    append([]string(nil), options.AdditionalPromptTemplatePaths...),
		Themes:             append([]string(nil), options.AdditionalThemePaths...),
		NoExtensions:       options.NoExtensions,
		NoSkills:           options.NoSkills,
		NoPromptTemplates:  options.NoPromptTemplates,
		NoThemes:           options.NoThemes,
		NoContextFiles:     options.NoContextFiles,
		SystemPrompt:       options.SystemPrompt,
		AppendSystemPrompt: append([]string(nil), options.AppendSystemPrompt...),
	}
}

func applyResourceLoaderOverrides(loader *ResourceLoader, cwd string, options DefaultResourceLoaderOptions) {
	if strings.TrimSpace(options.SystemPrompt) != "" {
		loader.SystemPrompt = readTextArg(cwd, options.SystemPrompt)
	}
	appendParts := nonEmpty([]string{loader.AppendPrompt})
	for _, text := range options.AppendSystemPrompt {
		if strings.TrimSpace(text) == "" {
			continue
		}
		appendParts = append(appendParts, readTextArg(cwd, text))
	}
	loader.AppendPrompt = strings.Join(nonEmpty(appendParts), "\n\n")
}

func settingsDiagnostics(settings *SettingsManager) []Diagnostic {
	if settings == nil || len(settings.Errors) == 0 {
		return nil
	}
	result := make([]Diagnostic, 0, len(settings.Errors))
	for _, err := range settings.Errors {
		if err == nil {
			continue
		}
		result = append(result, Diagnostic{Type: DiagWarning, Message: err.Error()})
	}
	return result
}

func resourceDiagnostics(loader ResourceLoader) []Diagnostic {
	if len(loader.Diagnostics) == 0 {
		return nil
	}
	result := make([]Diagnostic, 0, len(loader.Diagnostics))
	for _, diagnostic := range loader.Diagnostics {
		result = append(result, Diagnostic{Type: diagnosticType(diagnostic.Type), Message: diagnostic.Message})
	}
	return result
}

func diagnosticType(value string) DiagnosticType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error":
		return DiagError
	case "info":
		return DiagInfo
	default:
		return DiagWarning
	}
}

func toolSetFromServiceOptions(cwd string, settings *SettingsManager, noTools NoToolsMode, names, excludeNames []string, runtime *coreext.Runner, custom ToolSet, model ai.Model) ToolSet {
	if noTools == NoToolsAll {
		return ToolSet{}
	}
	builtins := BuiltinToolsForModel(cwd, settings, ai.SupportsInput(model, "image"))
	extensions := extensionToolSet(runtime)
	excluded := make(map[string]bool, len(excludeNames))
	for _, name := range excludeNames {
		excluded[name] = true
	}
	if len(names) > 0 {
		combined := ToolSet{}
		for name, tool := range builtins {
			combined[name] = tool
		}
		for name, tool := range extensions {
			combined[name] = tool
		}
		for name, tool := range custom {
			combined[name] = tool
		}
		selected := ToolSet{}
		for _, name := range names {
			if excluded[name] {
				continue
			}
			if tool, ok := combined[name]; ok {
				selected[name] = tool
			}
		}
		return selected
	}
	selected := ToolSet{}
	if noTools != NoToolsBuiltin {
		for _, name := range []string{"read", "bash", "edit", "write"} {
			if excluded[name] {
				continue
			}
			if tool, ok := builtins[name]; ok {
				selected[name] = tool
			}
		}
	}
	for name, tool := range extensions {
		if excluded[name] {
			continue
		}
		selected[name] = tool
	}
	for name, tool := range custom {
		if excluded[name] {
			continue
		}
		selected[name] = tool
	}
	return selected
}

func loadExtensionRuntime(ctx context.Context, paths []string, factories []coreext.Factory, flagValues map[string]any) (*coreext.Runner, []Diagnostic) {
	runtime := coreext.NewRunnerWithAPI(coreext.NewAPI())
	if len(paths) == 0 && len(factories) == 0 {
		return runtime, nil
	}
	diagnostics := make([]Diagnostic, 0, len(paths)+len(factories))
	for _, err := range coreext.LoadScriptExtensions(ctx, runtime.API, paths, flagValues) {
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Type: DiagError, Message: err.Error()})
		}
	}
	for index, factory := range factories {
		if factory == nil {
			continue
		}
		if err := factory(runtime.API); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Type: DiagError, Message: fmt.Sprintf("<inline:%d>: %v", index+1, err)})
		}
	}
	// Seed CLI flag values for in-process extensions so getFlag resolves them
	// (script extensions are seeded via their environment at spawn time).
	runtime.SetFlagValues(flagValues)
	return runtime, diagnostics
}

func extensionToolSet(runtime *coreext.Runner) ToolSet {
	if runtime == nil {
		return nil
	}
	tools := runtime.RegisteredTools()
	if len(tools) == 0 {
		return nil
	}
	result := ToolSet{}
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		result[tool.Name] = extensionRuntimeTool{definition: tool}
	}
	return result
}

type extensionRuntimeTool struct {
	definition coreext.ToolDefinition
}

func (t extensionRuntimeTool) Name() string {
	return t.definition.Name
}

func (t extensionRuntimeTool) Description() string {
	return t.definition.Description
}

func (t extensionRuntimeTool) Schema() map[string]any {
	return t.definition.Parameters
}

func (t extensionRuntimeTool) Execute(ctx context.Context, raw json.RawMessage, _ catools.ToolUpdate) ai.ToolResult {
	if t.definition.Execute == nil {
		return ai.ToolResult{Content: ai.TextBlocks("extension tool is not implemented"), IsError: true}
	}
	result, err := t.definition.Execute(ctx, raw)
	if err != nil {
		return ai.ToolResult{Content: ai.TextBlocks(err.Error()), IsError: true}
	}
	return result
}
