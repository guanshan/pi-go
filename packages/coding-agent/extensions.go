package codingagent

import (
	"context"

	core "github.com/guanshan/pi-go/packages/coding-agent/core"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

type ExtensionFactory func(*ExtensionAPI) error

type ExtensionAPI struct {
	inner *coreext.API
}

func NewExtensionAPI() *ExtensionAPI {
	return newExtensionAPIWithBus(NewEventBus())
}

func newExtensionAPIWithBus(events *coreext.EventBus) *ExtensionAPI {
	return &ExtensionAPI{inner: coreext.NewAPIWithBus(events)}
}

func (api *ExtensionAPI) RegisterTool(tool coreext.ToolDefinition) {
	if api == nil || api.inner == nil {
		return
	}
	api.inner.RegisterTool(tool)
}

func (api *ExtensionAPI) RegisterCommand(name, description string) {
	if api == nil || api.inner == nil {
		return
	}
	api.inner.RegisterCommand(name, description)
}

func (api *ExtensionAPI) RegisterCommandHandler(name, description string, handler func(context.Context, string) (string, error)) {
	if api == nil || api.inner == nil {
		return
	}
	api.inner.RegisterCommandHandler(name, description, handler)
}

func (api *ExtensionAPI) OnShutdown(handler func(context.Context) error) {
	if api == nil || api.inner == nil {
		return
	}
	api.inner.OnShutdown(handler)
}

func (api *ExtensionAPI) On(event string, listener func(any)) func() {
	if api == nil || api.inner == nil {
		return func() {}
	}
	return api.inner.On(event, listener)
}

func (api *ExtensionAPI) Emit(event string, payload any) {
	if api == nil || api.inner == nil {
		return
	}
	api.inner.Emit(event, payload)
}

func (api *ExtensionAPI) snapshotTools() []coreext.ToolDefinition {
	if api == nil || api.inner == nil {
		return nil
	}
	return api.inner.SnapshotTools()
}

func (api *ExtensionAPI) snapshotCommands() []core.SlashCommandInfo {
	if api == nil || api.inner == nil {
		return nil
	}
	commands := api.inner.SnapshotCommands()
	result := make([]core.SlashCommandInfo, 0, len(commands))
	for _, command := range commands {
		result = append(result, core.SlashCommandInfo{Name: command.Name, Description: command.Description, Source: command.Source})
	}
	return result
}

func (api *ExtensionAPI) snapshotShutdownHandlers() []func(context.Context) error {
	if api == nil || api.inner == nil {
		return nil
	}
	return api.inner.SnapshotShutdownHandlers()
}

type ExtensionRunner struct {
	inner *coreext.Runner
	API   *ExtensionAPI
}

func NewExtensionRunner(factories ...ExtensionFactory) (*ExtensionRunner, error) {
	converted := make([]coreext.Factory, 0, len(factories))
	for _, factory := range factories {
		if factory == nil {
			continue
		}
		converted = append(converted, wrapExtensionFactory(factory))
	}
	inner, err := coreext.NewRunner(converted...)
	if err != nil {
		return nil, err
	}
	return newExtensionRunnerFromInner(inner), nil
}

func newExtensionRunnerWithAPI(api *ExtensionAPI) *ExtensionRunner {
	if api == nil {
		api = NewExtensionAPI()
	}
	return newExtensionRunnerFromInner(coreext.NewRunnerWithAPI(api.inner))
}

func (r *ExtensionRunner) GetAllRegisteredTools() []coreext.ToolDefinition {
	if r == nil || r.inner == nil {
		return nil
	}
	return r.inner.RegisteredTools()
}

func (r *ExtensionRunner) GetRegisteredCommands() []core.SlashCommandInfo {
	if r == nil || r.inner == nil {
		return nil
	}
	commands := r.inner.RegisteredCommands()
	result := make([]core.SlashCommandInfo, 0, len(commands))
	for _, command := range commands {
		result = append(result, core.SlashCommandInfo{Name: command.Name, Description: command.Description, Source: command.Source})
	}
	return result
}

func (r *ExtensionRunner) GetToolDefinition(name string) (coreext.ToolDefinition, bool) {
	if r == nil || r.inner == nil {
		return coreext.ToolDefinition{}, false
	}
	tool, ok := r.inner.ToolDefinition(name)
	if !ok {
		return coreext.ToolDefinition{}, false
	}
	return tool, true
}

func (r *ExtensionRunner) HasHandlers(eventType string) bool {
	if r == nil || r.inner == nil {
		return false
	}
	return r.inner.HasHandlers(eventType)
}

func (r *ExtensionRunner) Emit(event string, payload any) {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.Emit(event, payload)
}

func (r *ExtensionRunner) OnError(listener coreext.ErrorListener) func() {
	if r == nil || r.inner == nil {
		return func() {}
	}
	return r.inner.OnError(listener)
}

func (r *ExtensionRunner) EmitError(err error) {
	if r == nil || r.inner == nil {
		return
	}
	r.inner.EmitError(err)
}

func (r *ExtensionRunner) Shutdown(ctx context.Context) error {
	if r == nil || r.inner == nil {
		return nil
	}
	return r.inner.Shutdown(ctx)
}

func newExtensionRunnerFromInner(inner *coreext.Runner) *ExtensionRunner {
	if inner == nil {
		inner = coreext.NewRunnerWithAPI(coreext.NewAPI())
	}
	return &ExtensionRunner{inner: inner, API: &ExtensionAPI{inner: inner.API}}
}

func wrapExtensionFactory(factory ExtensionFactory) coreext.Factory {
	return func(api *coreext.API) error {
		return factory(&ExtensionAPI{inner: api})
	}
}

func wrapExtensionFactories(factories []ExtensionFactory) []coreext.Factory {
	if len(factories) == 0 {
		return nil
	}
	converted := make([]coreext.Factory, 0, len(factories))
	for _, factory := range factories {
		if factory == nil {
			continue
		}
		converted = append(converted, wrapExtensionFactory(factory))
	}
	return converted
}
