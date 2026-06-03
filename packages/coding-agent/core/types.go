package core

import (
	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

const (
	AppName               = "pi"
	ConfigDirName         = ".pi"
	CurrentSessionVersion = 3
	// Version is the coding-agent version. It is derived from the single source of
	// truth in packages/ai (ai.Version) so the two never drift; ai does not import
	// coding-agent, so there is no import cycle.
	Version = ai.Version
)

func InitialModel(registry *ai.ModelRegistry, args cli.Args, settings *SettingsManager) (ai.Model, bool, string) {
	if registry == nil {
		return ai.Model{}, false, "No models available"
	}
	return registry.InitialModel(ai.InitialModelOptions{
		Provider:        args.Provider,
		Model:           args.Model,
		Models:          args.Models,
		DefaultProvider: settings.DefaultProvider(),
		DefaultModel:    settings.DefaultModel(),
		EnabledModels:   settings.EnabledModels(),
	})
}

func emit(sink ai.EventSink, event ai.Event) {
	if sink != nil {
		sink(event)
	}
}
