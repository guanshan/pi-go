package core

import (
	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

const (
	AppName               = "pi"
	ConfigDirName         = ".pi"
	CurrentSessionVersion = 3
	Version               = "0.75.5-go"
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
