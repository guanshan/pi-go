package core

import (
	"context"
	"os"

	"github.com/guanshan/pi-go/packages/ai"
)

type NoToolsMode string

const (
	NoToolsNone    NoToolsMode = ""
	NoToolsAll     NoToolsMode = "all"
	NoToolsBuiltin NoToolsMode = "builtin"
)

type ScopedModel struct {
	Model         ai.Model
	ThinkingLevel ai.ThinkingLevel
}

type CreateAgentSessionOptions struct {
	Cwd                   string
	AgentDir              string
	AuthStorage           *ai.AuthStorage
	ModelRegistry         *ai.ModelRegistry
	Model                 ai.Model
	ThinkingLevel         ai.ThinkingLevel
	ScopedModels          []ScopedModel
	NoTools               NoToolsMode
	Tools                 []string
	ExcludeTools          []string
	CustomTools           ToolSet
	ResourceLoader        *ResourceLoader
	ResourceLoaderOptions DefaultResourceLoaderOptions
	SessionManager        *SessionManager
	SettingsManager       *SettingsManager
}

type CreateAgentSessionResult struct {
	Session              *AgentSession
	ModelFallbackMessage string
}

func CreateAgentSession(ctx context.Context, options CreateAgentSessionOptions) (CreateAgentSessionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cwd := options.Cwd
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return CreateAgentSessionResult{}, err
		}
	}
	services, err := CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
		Cwd:                   cwd,
		AgentDir:              options.AgentDir,
		AuthStorage:           options.AuthStorage,
		SettingsManager:       options.SettingsManager,
		ModelRegistry:         options.ModelRegistry,
		ResourceLoaderOptions: options.ResourceLoaderOptions,
	})
	if err != nil {
		return CreateAgentSessionResult{}, err
	}
	if options.ResourceLoader != nil {
		services.ResourceLoader = *options.ResourceLoader
	}
	return CreateAgentSessionFromServices(ctx, CreateAgentSessionFromServicesOptions{
		Services:       services,
		SessionManager: options.SessionManager,
		Model:          options.Model,
		ThinkingLevel:  options.ThinkingLevel,
		ScopedModels:   options.ScopedModels,
		Tools:          options.Tools,
		ExcludeTools:   options.ExcludeTools,
		CustomTools:    options.CustomTools,
		NoTools:        options.NoTools,
	})
}
