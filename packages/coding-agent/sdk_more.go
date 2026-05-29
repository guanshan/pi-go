package codingagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	core "github.com/guanshan/pi-go/packages/coding-agent/core"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

const (
	AppName             = "pi"
	ConfigDirName       = ".pi"
	EnvAgentDir         = core.EnvAgentDir
	EnvSessionDir       = core.EnvSessionDir
	LegacyEnvAgentDir   = core.EnvLegacyAgentDir
	LegacyEnvSessionDir = core.EnvLegacySessionDir
)

func NewEventBus() *coreext.EventBus {
	return coreext.NewEventBus()
}

func ResolveCliModel(registry *ai.ModelRegistry, provider, modelPattern string) (ai.Model, ai.ThinkingLevel, error) {
	return core.ResolveCliModel(registry, provider, modelPattern)
}

func ResolveModelScope(registry *ai.ModelRegistry, patterns []string) []core.ScopedModel {
	return core.ResolveModelScope(registry, patterns)
}

func FormatNoModelsAvailableMessage() string {
	return "No configured models are available. Set an API key such as ANTHROPIC_API_KEY, OPENAI_API_KEY, or GEMINI_API_KEY, or run with --model faux/faux for local smoke tests."
}

func FormatNoAPIKeyFoundMessage(provider string) string {
	return fmt.Sprintf("No API key found for provider %s. Set one of: %s", provider, strings.Join(ai.ProviderEnvKeys(provider), ", "))
}

func FormatNoModelSelectedMessage() string {
	return "No model selected. Use --model <provider/model> or configure defaultProvider/defaultModel in settings.json."
}

type SessionCwdIssue struct {
	SessionFile string
	SessionCWD  string
	CurrentCWD  string
	FallbackCWD string
}

func GetMissingSessionCwdIssue(session *core.SessionManager, currentCWD string) *SessionCwdIssue {
	if session == nil || session.CWD() == "" || filepath.Clean(session.CWD()) == filepath.Clean(currentCWD) {
		return nil
	}
	return &SessionCwdIssue{SessionFile: session.File(), SessionCWD: session.CWD(), CurrentCWD: currentCWD, FallbackCWD: currentCWD}
}

func FormatMissingSessionCwdPrompt(issue *SessionCwdIssue) string {
	if issue == nil {
		return ""
	}
	return fmt.Sprintf("Session was created in %s but current directory is %s.", issue.SessionCWD, issue.CurrentCWD)
}

type SourceInfo struct {
	Type    string `json:"type,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Source  string `json:"source,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Origin  string `json:"origin,omitempty"`
	BaseDir string `json:"baseDir,omitempty"`
}

func CreateSourceInfo(path string, metadata PathMetadata) SourceInfo {
	return SourceInfo{
		Path:    path,
		Source:  metadata.Source,
		Scope:   metadata.Scope,
		Origin:  metadata.Origin,
		BaseDir: metadata.BaseDir,
	}
}

func CreateSyntheticSourceInfo(name string) SourceInfo {
	return SourceInfo{
		Type:   "synthetic",
		Path:   "<synthetic:" + name + ">",
		Name:   name,
		Source: name,
		Scope:  "temporary",
		Origin: "top-level",
	}
}

func BuiltinSlashCommands() []core.SlashCommandInfo {
	return core.BuiltinSlashCommands()
}

func DefineTool(name, description string, parameters map[string]any, execute func(context.Context, []byte) (ai.ToolResult, error)) coreext.ToolDefinition {
	return coreext.DefineTool(name, description, parameters, execute)
}

type CreateAgentSessionOptions struct {
	CWD            string
	AgentDir       string
	SessionManager *core.SessionManager
	Settings       *core.SettingsManager
	Registry       *ai.ModelRegistry
	Model          ai.Model
	ThinkingLevel  ai.ThinkingLevel
	Tools          core.ToolSet
	NoTools        bool
}

type CreateAgentSessionResult struct {
	Session *core.AgentSession
}

func CreateAgentSession(options CreateAgentSessionOptions) (*CreateAgentSessionResult, error) {
	if options.Tools == nil {
		result, err := core.CreateAgentSession(context.Background(), core.CreateAgentSessionOptions{
			Cwd:             options.CWD,
			AgentDir:        options.AgentDir,
			SessionManager:  options.SessionManager,
			SettingsManager: options.Settings,
			ModelRegistry:   options.Registry,
			Model:           options.Model,
			ThinkingLevel:   options.ThinkingLevel,
			NoTools: func() core.NoToolsMode {
				if options.NoTools {
					return core.NoToolsAll
				}
				return core.NoToolsNone
			}(),
			ResourceLoaderOptions: core.DefaultResourceLoaderOptions{
				NoContextFiles:    true,
				NoExtensions:      true,
				NoSkills:          true,
				NoPromptTemplates: true,
				NoThemes:          true,
			},
		})
		if err != nil {
			return nil, err
		}
		return &CreateAgentSessionResult{Session: result.Session}, nil
	}
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
		settings = core.NewSettingsManager(cwd, agentDir)
	}
	session := options.SessionManager
	if session == nil {
		session = core.InMemorySession(cwd)
	}
	registry := options.Registry
	if registry == nil {
		registry = ai.NewModelRegistry(agentDir, ai.NewAuthStorage(agentDir))
	}
	model := options.Model
	if model.Provider == "" {
		model, _, _ = registry.Match("faux", "faux")
	}
	tools := options.Tools
	if tools == nil && !options.NoTools {
		tools = core.FilterTools(core.BuiltinTools(cwd, settings), cli.Args{})
	}
	resources := core.LoadResources(cwd, agentDir, cli.Args{NoContextFiles: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoExtensions: true}, settings)
	systemPrompt := resources.BuildSystemPrompt(cli.Args{}, core.AllToolDescriptions(tools))
	agent := core.NewAgentSession(session, settings, registry, resources, model, options.ThinkingLevel, tools, systemPrompt)
	return &CreateAgentSessionResult{Session: agent}, nil
}
