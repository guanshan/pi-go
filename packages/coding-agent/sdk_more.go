package codingagent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
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
	// Context cancels resource/extension loading during session construction.
	// When nil, context.Background() is used.
	Context        context.Context
	CWD            string
	AgentDir       string
	SessionManager *core.SessionManager
	Settings       *core.SettingsManager
	Registry       *ai.ModelRegistry
	AuthStorage    *ai.AuthStorage
	Model          ai.Model
	ThinkingLevel  ai.ThinkingLevel
	ScopedModels   []core.ScopedModel
	// CustomTools are caller-supplied tools merged on top of the builtin and
	// extension tool sets, mirroring the TS createAgentSession `customTools`
	// option.
	CustomTools  core.ToolSet
	Tools        []string
	ExcludeTools []string
	NoTools      bool
	// ResourceLoaderOptions controls context files, extensions, skills, prompt
	// templates and themes. The zero value loads resources just like the TS
	// DefaultResourceLoader; set the No* fields to opt out.
	ResourceLoaderOptions core.DefaultResourceLoaderOptions
	ResourceLoader        *core.ResourceLoader
}

type CreateAgentSessionResult struct {
	Session              *core.AgentSession
	ModelFallbackMessage string
	Diagnostics          []core.Diagnostic
}

// CreateAgentSession is a thin facade over the core SDK. It loads resources and
// extensions (unless opted out via ResourceLoaderOptions), merges any
// CustomTools through the same tool-assembly path as builtin/extension tools,
// and surfaces the model fallback message and diagnostics produced by the core
// session services, matching the shape of the TS createAgentSession().
func CreateAgentSession(options CreateAgentSessionOptions) (*CreateAgentSessionResult, error) {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	services, err := core.CreateAgentSessionServices(ctx, core.CreateAgentSessionServicesOptions{
		Cwd:                   options.CWD,
		AgentDir:              options.AgentDir,
		AuthStorage:           options.AuthStorage,
		SettingsManager:       options.Settings,
		ModelRegistry:         options.Registry,
		ResourceLoaderOptions: options.ResourceLoaderOptions,
	})
	if err != nil {
		return nil, err
	}
	if options.ResourceLoader != nil {
		services.ResourceLoader = *options.ResourceLoader
	}
	noTools := core.NoToolsNone
	if options.NoTools {
		noTools = core.NoToolsAll
	}
	result, err := core.CreateAgentSessionFromServices(ctx, core.CreateAgentSessionFromServicesOptions{
		Services:       services,
		SessionManager: options.SessionManager,
		Model:          options.Model,
		ThinkingLevel:  options.ThinkingLevel,
		ScopedModels:   options.ScopedModels,
		Tools:          options.Tools,
		ExcludeTools:   options.ExcludeTools,
		CustomTools:    options.CustomTools,
		NoTools:        noTools,
	})
	if err != nil {
		return nil, err
	}
	return &CreateAgentSessionResult{
		Session:              result.Session,
		ModelFallbackMessage: result.ModelFallbackMessage,
		Diagnostics:          services.Diagnostics,
	}, nil
}
