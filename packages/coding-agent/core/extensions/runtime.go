package extensions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type busListener struct {
	id uint64
	fn func(any)
}

type EventBus struct {
	mu        sync.RWMutex
	nextID    uint64
	listeners map[string][]busListener
}

func NewEventBus() *EventBus {
	return &EventBus{listeners: map[string][]busListener{}}
}

// On registers a listener and returns an unsubscribe func. Listeners are keyed
// by a monotonically increasing id (not a captured slice index), so unsubscribe
// is robust to concurrent registration and actually removes the entry rather
// than leaving a nil hole that accumulates over a long-lived session.
// Registration order is preserved for Emit.
func (b *EventBus) On(event string, listener func(any)) func() {
	if listener == nil {
		return func() {}
	}
	normalized := normalizeEventKey(event)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.listeners[normalized] = append(b.listeners[normalized], busListener{id: id, fn: listener})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		entries := b.listeners[normalized]
		for i, entry := range entries {
			if entry.id == id {
				b.listeners[normalized] = append(entries[:i:i], entries[i+1:]...)
				break
			}
		}
		if len(b.listeners[normalized]) == 0 {
			delete(b.listeners, normalized)
		}
	}
}

func (b *EventBus) Emit(event string, payload any) {
	if b == nil {
		return
	}
	normalized := normalizeEventKey(event)
	b.mu.RLock()
	entries := append([]busListener(nil), b.listeners[normalized]...)
	b.mu.RUnlock()
	for _, entry := range entries {
		if entry.fn != nil {
			entry.fn(payload)
		}
	}
}

type Factory func(*API) error

type ErrorListener func(error)

type API struct {
	mu               sync.RWMutex
	Tools            []ToolDefinition
	Commands         []CommandInfo
	Flags            []FlagDefinition
	flagValues       map[string]any
	Events           *EventBus
	shutdownHandlers []func(context.Context) error
	eventHandlers    map[string]int
}

func NewAPI() *API {
	return NewAPIWithBus(NewEventBus())
}

func NewAPIWithBus(events *EventBus) *API {
	if events == nil {
		events = NewEventBus()
	}
	return &API{Events: events, eventHandlers: map[string]int{}, flagValues: map[string]any{}}
}

func (api *API) RegisterTool(tool ToolDefinition) {
	if api == nil {
		return
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	api.Tools = append(api.Tools, tool)
}

func (api *API) RegisterCommand(name, description string) {
	api.RegisterCommandHandler(name, description, nil)
}

func (api *API) RegisterCommandHandler(name, description string, execute func(context.Context, string) (string, error)) {
	if api == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	api.Commands = append(api.Commands, CommandInfo{Name: name, Description: description, Source: "extension", Execute: execute})
}

// RegisterFlag declares a CLI flag. Its default is seeded into the flag values
// so Flag returns it until the host supplies a parsed command-line value.
func (api *API) RegisterFlag(flag FlagDefinition) {
	if api == nil || flag.Name == "" {
		return
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	api.Flags = append(api.Flags, flag)
	if api.flagValues == nil {
		api.flagValues = map[string]any{}
	}
	if flag.Default != nil {
		if _, ok := api.flagValues[flag.Name]; !ok {
			api.flagValues[flag.Name] = flag.Default
		}
	}
}

// SetFlagValues merges host-parsed flag values (e.g. unknown CLI flags) over any
// registered defaults.
func (api *API) SetFlagValues(values map[string]any) {
	if api == nil || len(values) == 0 {
		return
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.flagValues == nil {
		api.flagValues = map[string]any{}
	}
	for name, value := range values {
		api.flagValues[name] = value
	}
}

// Flag returns the current value of a registered flag (nil if unregistered),
// mirroring the upstream getFlag which only resolves flags the extension declared.
func (api *API) Flag(name string) any {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	registered := false
	for _, flag := range api.Flags {
		if flag.Name == name {
			registered = true
			break
		}
	}
	if !registered {
		return nil
	}
	return api.flagValues[name]
}

func (api *API) SnapshotFlags() []FlagDefinition {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return append([]FlagDefinition(nil), api.Flags...)
}

func (api *API) OnShutdown(handler func(context.Context) error) {
	if api == nil || handler == nil {
		return
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	api.shutdownHandlers = append(api.shutdownHandlers, handler)
}

func (api *API) On(event string, listener func(any)) func() {
	if api == nil || listener == nil {
		return func() {}
	}
	normalized := normalizeEventKey(event)
	api.mu.Lock()
	api.eventHandlers[normalized]++
	api.mu.Unlock()
	unsubscribe := api.Events.On(normalized, listener)
	return func() {
		api.mu.Lock()
		if api.eventHandlers[normalized] > 1 {
			api.eventHandlers[normalized]--
		} else {
			delete(api.eventHandlers, normalized)
		}
		api.mu.Unlock()
		unsubscribe()
	}
}

func (api *API) Emit(event string, payload any) {
	if api == nil || api.Events == nil {
		return
	}
	api.Events.Emit(event, payload)
}

func (api *API) SnapshotTools() []ToolDefinition {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return append([]ToolDefinition(nil), api.Tools...)
}

func (api *API) SnapshotCommands() []CommandInfo {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return append([]CommandInfo(nil), api.Commands...)
}

func (api *API) SnapshotShutdownHandlers() []func(context.Context) error {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return append([]func(context.Context) error(nil), api.shutdownHandlers...)
}

func (api *API) HasHandlers(eventType string) bool {
	if api == nil {
		return false
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return api.eventHandlers[normalizeEventKey(eventType)] > 0
}

type Runner struct {
	API                 *API
	mu                  sync.RWMutex
	nextErrorListenerID uint64
	errorListeners      map[uint64]ErrorListener
}

func NewRunner(factories ...Factory) (*Runner, error) {
	api := NewAPI()
	for _, factory := range factories {
		if factory == nil {
			continue
		}
		if err := factory(api); err != nil {
			return nil, err
		}
	}
	return NewRunnerWithAPI(api), nil
}

func NewRunnerWithAPI(api *API) *Runner {
	if api == nil {
		api = NewAPI()
	}
	return &Runner{API: api, errorListeners: map[uint64]ErrorListener{}}
}

func (r *Runner) RegisteredTools() []ToolDefinition {
	if r == nil || r.API == nil {
		return nil
	}
	return r.API.SnapshotTools()
}

func (r *Runner) RegisteredCommands() []CommandInfo {
	if r == nil || r.API == nil {
		return nil
	}
	return r.API.SnapshotCommands()
}

func (r *Runner) RegisteredFlags() []FlagDefinition {
	if r == nil || r.API == nil {
		return nil
	}
	return r.API.SnapshotFlags()
}

// SetFlagValues forwards host-parsed flag values to the shared API so extension
// getFlag calls resolve them.
func (r *Runner) SetFlagValues(values map[string]any) {
	if r == nil || r.API == nil {
		return
	}
	r.API.SetFlagValues(values)
}

func (r *Runner) FlagValue(name string) any {
	if r == nil || r.API == nil {
		return nil
	}
	return r.API.Flag(name)
}

func (r *Runner) ToolDefinition(name string) (ToolDefinition, bool) {
	for _, tool := range r.RegisteredTools() {
		if tool.Name == name {
			return tool, true
		}
	}
	return ToolDefinition{}, false
}

func (r *Runner) CommandDefinition(name string) (CommandInfo, bool) {
	for _, command := range r.RegisteredCommands() {
		if command.Name == name {
			return command, true
		}
	}
	return CommandInfo{}, false
}

func (r *Runner) ExecuteCommand(ctx context.Context, name, args string) (string, bool, error) {
	command, ok := r.CommandDefinition(name)
	if !ok {
		return "", false, nil
	}
	if command.Execute == nil {
		err := fmt.Errorf("extension command /%s has no handler", name)
		r.EmitError(err)
		return "", true, err
	}
	out, err := command.Execute(ctx, args)
	if err != nil {
		r.EmitError(err)
		return out, true, err
	}
	return out, true, nil
}

func (r *Runner) HasHandlers(eventType string) bool {
	if r == nil || r.API == nil {
		return false
	}
	normalized := normalizeEventKey(eventType)
	if normalized == "shutdown" || normalized == "session_shutdown" {
		return len(r.API.SnapshotShutdownHandlers()) > 0 || r.API.HasHandlers(normalized)
	}
	return r.API.HasHandlers(normalized)
}

func (r *Runner) Emit(event string, payload any) {
	if r == nil || r.API == nil {
		return
	}
	r.API.Emit(event, payload)
}

func (r *Runner) OnError(listener ErrorListener) func() {
	if r == nil || listener == nil {
		return func() {}
	}
	r.mu.Lock()
	r.nextErrorListenerID++
	id := r.nextErrorListenerID
	if r.errorListeners == nil {
		r.errorListeners = map[uint64]ErrorListener{}
	}
	r.errorListeners[id] = listener
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.errorListeners, id)
		r.mu.Unlock()
	}
}

func (r *Runner) EmitError(err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.RLock()
	listeners := make([]ErrorListener, 0, len(r.errorListeners))
	for _, listener := range r.errorListeners {
		listeners = append(listeners, listener)
	}
	r.mu.RUnlock()
	for _, listener := range listeners {
		listener(err)
	}
}

func (r *Runner) Shutdown(ctx context.Context) error {
	if r == nil || r.API == nil {
		return nil
	}
	handlers := r.API.SnapshotShutdownHandlers()
	var errs []error
	for i := len(handlers) - 1; i >= 0; i-- {
		if handlers[i] == nil {
			continue
		}
		if err := handlers[i](ctx); err != nil {
			errs = append(errs, err)
			r.EmitError(err)
		}
	}
	return errors.Join(errs...)
}

func normalizeEventKey(eventType string) string {
	normalized := strings.ToLower(strings.TrimSpace(eventType))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized
}
