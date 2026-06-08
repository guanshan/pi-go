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
	mu                         sync.RWMutex
	Tools                      []ToolDefinition
	Commands                   []CommandInfo
	Shortcuts                  []ShortcutDefinition
	Autocomplete               []AutocompleteProviderDefinition
	Providers                  []ProviderDefinition
	MessageRenderers           []MessageRendererDefinition
	Flags                      []FlagDefinition
	flagValues                 map[string]any
	Events                     *EventBus
	shutdownHandlers           []func(context.Context) error
	eventHandlers              map[string]int
	uiHandler                  UIRequestHandler
	uiListeners                []func(uint64, bool)
	providerListeners          []func(ProviderDefinition, bool)
	nextAutocompleteProviderID uint64
	contextProvider            ExtensionContextProvider
	contextAction              ExtensionContextActionHandler
	// uiSeq is a monotonic sequence stamped on each SetUIHandler call (under mu)
	// so listeners can discard stale notifications and resolve a true/false race
	// to the latest state regardless of goroutine scheduling order.
	uiSeq uint64
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

func (api *API) RegisterShortcut(shortcut ShortcutDefinition) {
	if api == nil || shortcut.Execute == nil {
		return
	}
	key := strings.TrimSpace(shortcut.Key)
	if key == "" {
		return
	}
	shortcut.Key = key
	if shortcut.Source == "" {
		shortcut.Source = "extension"
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	insertAt := len(api.Shortcuts)
	for i, existing := range api.Shortcuts {
		if strings.TrimSpace(existing.Source) == strings.TrimSpace(shortcut.Source) {
			if strings.TrimSpace(existing.Key) == key {
				api.Shortcuts[i] = shortcut
				return
			}
			insertAt = i + 1
		}
	}
	if insertAt == len(api.Shortcuts) {
		api.Shortcuts = append(api.Shortcuts, shortcut)
		return
	}
	api.Shortcuts = append(api.Shortcuts, ShortcutDefinition{})
	copy(api.Shortcuts[insertAt+1:], api.Shortcuts[insertAt:])
	api.Shortcuts[insertAt] = shortcut
}

func (api *API) UnregisterShortcut(key string) {
	api.UnregisterShortcutSource(key, "")
}

func (api *API) UnregisterShortcutSource(key, source string) {
	if api == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	out := api.Shortcuts[:0]
	for _, shortcut := range api.Shortcuts {
		if strings.TrimSpace(shortcut.Key) != key || (source != "" && strings.TrimSpace(shortcut.Source) != strings.TrimSpace(source)) {
			out = append(out, shortcut)
		}
	}
	api.Shortcuts = out
}

func (api *API) RegisterAutocompleteProvider(provider AutocompleteProviderDefinition) {
	if api == nil || provider.Suggest == nil {
		return
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if provider.ID == 0 {
		api.nextAutocompleteProviderID++
		provider.ID = api.nextAutocompleteProviderID
	} else if provider.ID > api.nextAutocompleteProviderID {
		api.nextAutocompleteProviderID = provider.ID
	}
	api.Autocomplete = append(api.Autocomplete, provider)
}

func (api *API) RegisterProvider(provider ProviderDefinition) {
	if api == nil {
		return
	}
	if provider.Provider != nil {
		provider.API = strings.TrimSpace(firstNonEmpty(provider.API, provider.Provider.API()))
	} else {
		provider.API = strings.TrimSpace(provider.API)
	}
	provider.ProviderName = strings.TrimSpace(provider.ProviderName)
	if provider.API == "" && provider.ProviderName == "" {
		return
	}
	if provider.Provider == nil && !hasProviderModelConfig(provider.ModelConfig) {
		return
	}
	if provider.Source == "" {
		provider.Source = "extension"
	}
	api.mu.Lock()
	for i, existing := range api.Providers {
		if sameProviderDefinition(existing, provider) {
			api.Providers[i] = provider
			listeners := append([]func(ProviderDefinition, bool){}, api.providerListeners...)
			api.mu.Unlock()
			for _, listener := range listeners {
				if listener != nil {
					listener(provider, true)
				}
			}
			return
		}
	}
	api.Providers = append(api.Providers, provider)
	listeners := append([]func(ProviderDefinition, bool){}, api.providerListeners...)
	api.mu.Unlock()
	for _, listener := range listeners {
		if listener != nil {
			listener(provider, true)
		}
	}
}

func (api *API) UnregisterProviderSource(apiID, source string) {
	if api == nil {
		return
	}
	apiID = strings.TrimSpace(apiID)
	if apiID == "" {
		return
	}
	api.mu.Lock()
	out := api.Providers[:0]
	var removed []ProviderDefinition
	for _, provider := range api.Providers {
		if providerMatchesUnregisterKey(provider, apiID) && (source == "" || strings.TrimSpace(provider.Source) == strings.TrimSpace(source)) {
			removed = append(removed, provider)
			continue
		}
		out = append(out, provider)
	}
	api.Providers = out
	listeners := append([]func(ProviderDefinition, bool){}, api.providerListeners...)
	api.mu.Unlock()
	for _, provider := range removed {
		for _, listener := range listeners {
			if listener != nil {
				listener(provider, false)
			}
		}
	}
}

func (api *API) OnProviderChange(listener func(ProviderDefinition, bool)) func() {
	if api == nil || listener == nil {
		return func() {}
	}
	api.mu.Lock()
	api.providerListeners = append(api.providerListeners, listener)
	index := len(api.providerListeners) - 1
	api.mu.Unlock()
	return func() {
		api.mu.Lock()
		defer api.mu.Unlock()
		if index < 0 || index >= len(api.providerListeners) || api.providerListeners[index] == nil {
			return
		}
		api.providerListeners[index] = nil
	}
}

func sameProviderDefinition(a, b ProviderDefinition) bool {
	return strings.TrimSpace(a.Source) == strings.TrimSpace(b.Source) &&
		strings.TrimSpace(a.API) == strings.TrimSpace(b.API) &&
		strings.TrimSpace(a.ProviderName) == strings.TrimSpace(b.ProviderName)
}

func providerMatchesUnregisterKey(provider ProviderDefinition, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	return strings.TrimSpace(provider.API) == key || strings.TrimSpace(provider.ProviderName) == key
}

func hasProviderModelConfig(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
}

func (api *API) RegisterMessageRenderer(renderer MessageRendererDefinition) {
	if api == nil || renderer.Render == nil {
		return
	}
	renderer.CustomType = strings.TrimSpace(renderer.CustomType)
	if renderer.CustomType == "" {
		return
	}
	if renderer.Source == "" {
		renderer.Source = "extension"
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	for i, existing := range api.MessageRenderers {
		if strings.TrimSpace(existing.CustomType) == renderer.CustomType && strings.TrimSpace(existing.Source) == strings.TrimSpace(renderer.Source) {
			api.MessageRenderers[i] = renderer
			return
		}
	}
	api.MessageRenderers = append(api.MessageRenderers, renderer)
}

func (api *API) UnregisterMessageRendererSource(customType, source string) {
	if api == nil {
		return
	}
	customType = strings.TrimSpace(customType)
	if customType == "" {
		return
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	out := api.MessageRenderers[:0]
	for _, renderer := range api.MessageRenderers {
		if strings.TrimSpace(renderer.CustomType) == customType && (source == "" || strings.TrimSpace(renderer.Source) == strings.TrimSpace(source)) {
			continue
		}
		out = append(out, renderer)
	}
	api.MessageRenderers = out
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

func (api *API) SetContextProvider(provider ExtensionContextProvider) {
	if api == nil {
		return
	}
	api.mu.Lock()
	api.contextProvider = provider
	api.mu.Unlock()
}

func (api *API) ContextSnapshot() ExtensionContextSnapshot {
	if api == nil {
		return ExtensionContextSnapshot{}
	}
	api.mu.RLock()
	provider := api.contextProvider
	hasUI := api.uiHandler != nil
	api.mu.RUnlock()
	if provider == nil {
		return ExtensionContextSnapshot{Mode: "print", HasUI: hasUI, IsIdle: true}
	}
	return provider()
}

func (api *API) SetContextActionHandler(handler ExtensionContextActionHandler) {
	if api == nil {
		return
	}
	api.mu.Lock()
	api.contextAction = handler
	api.mu.Unlock()
}

func (api *API) ContextActionHandler() ExtensionContextActionHandler {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return api.contextAction
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

func (api *API) SnapshotShortcuts() []ShortcutDefinition {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return append([]ShortcutDefinition(nil), api.Shortcuts...)
}

func (api *API) SnapshotAutocompleteProviders() []AutocompleteProviderDefinition {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return append([]AutocompleteProviderDefinition(nil), api.Autocomplete...)
}

func (api *API) SnapshotProviders() []ProviderDefinition {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return append([]ProviderDefinition(nil), api.Providers...)
}

func (api *API) SnapshotMessageRenderers() []MessageRendererDefinition {
	if api == nil {
		return nil
	}
	api.mu.RLock()
	defer api.mu.RUnlock()
	return append([]MessageRendererDefinition(nil), api.MessageRenderers...)
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

func (r *Runner) RegisteredShortcuts() []ShortcutDefinition {
	if r == nil || r.API == nil {
		return nil
	}
	return r.API.SnapshotShortcuts()
}

func (r *Runner) RegisteredAutocompleteProviders() []AutocompleteProviderDefinition {
	if r == nil || r.API == nil {
		return nil
	}
	return r.API.SnapshotAutocompleteProviders()
}

func (r *Runner) RegisteredProviders() []ProviderDefinition {
	if r == nil || r.API == nil {
		return nil
	}
	return r.API.SnapshotProviders()
}

func (r *Runner) RegisteredMessageRenderers() []MessageRendererDefinition {
	if r == nil || r.API == nil {
		return nil
	}
	return r.API.SnapshotMessageRenderers()
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

func (r *Runner) SetContextProvider(provider ExtensionContextProvider) {
	if r == nil || r.API == nil {
		return
	}
	r.API.SetContextProvider(provider)
}

func (r *Runner) SetContextActionHandler(handler ExtensionContextActionHandler) {
	if r == nil || r.API == nil {
		return
	}
	r.API.SetContextActionHandler(handler)
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

func (r *Runner) ShortcutDefinition(key string) (ShortcutDefinition, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return ShortcutDefinition{}, false
	}
	for _, shortcut := range r.RegisteredShortcuts() {
		if strings.TrimSpace(shortcut.Key) == key {
			return shortcut, true
		}
	}
	return ShortcutDefinition{}, false
}

func (r *Runner) MessageRendererDefinition(customType string) (MessageRendererDefinition, bool) {
	customType = strings.TrimSpace(customType)
	if customType == "" {
		return MessageRendererDefinition{}, false
	}
	renderers := r.RegisteredMessageRenderers()
	for i := len(renderers) - 1; i >= 0; i-- {
		if strings.TrimSpace(renderers[i].CustomType) == customType {
			return renderers[i], true
		}
	}
	return MessageRendererDefinition{}, false
}

func (r *Runner) RenderMessage(ctx context.Context, request MessageRenderRequest) (MessageRenderResult, bool, error) {
	renderer, ok := r.MessageRendererDefinition(request.CustomType)
	if !ok {
		return MessageRenderResult{}, false, nil
	}
	if renderer.Render == nil {
		err := fmt.Errorf("extension message renderer %s has no handler", request.CustomType)
		r.EmitError(err)
		return MessageRenderResult{}, true, err
	}
	result, err := renderer.Render(ctx, request)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			r.EmitError(err)
		}
		return result, true, err
	}
	return result, true, nil
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

func (r *Runner) ExecuteShortcut(ctx context.Context, key string) (bool, error) {
	shortcut, ok := r.ShortcutDefinition(key)
	if !ok {
		return false, nil
	}
	if shortcut.Execute == nil {
		err := fmt.Errorf("extension shortcut %s has no handler", key)
		r.EmitError(err)
		return true, err
	}
	err := shortcut.Execute(ctx)
	if err != nil {
		r.EmitError(err)
		return true, err
	}
	return true, nil
}

func (r *Runner) Autocomplete(ctx context.Context, request AutocompleteRequest) (AutocompleteSuggestions, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var merged AutocompleteSuggestions
	seen := map[string]bool{}
	var errs []error
	sourceCounts := map[string]int{}
	for providerIndex, provider := range r.RegisteredAutocompleteProviders() {
		if provider.Suggest == nil {
			continue
		}
		sourceIndex := sourceCounts[provider.Source]
		sourceCounts[provider.Source] = sourceIndex + 1
		result, err := provider.Suggest(ctx, request)
		if err != nil {
			errs = append(errs, err)
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				r.EmitError(err)
			}
			continue
		}
		if merged.Prefix == "" {
			merged.Prefix = result.Prefix
		}
		for _, item := range result.Items {
			if item.Value == "" {
				item.Value = item.Label
			}
			if item.Value == "" {
				continue
			}
			item.Provider = providerIndex
			item.ProviderID = provider.ID
			item.Source = provider.Source
			item.SourceIndex = sourceIndex
			key := item.Value + "\x00" + item.Label
			if seen[key] {
				continue
			}
			seen[key] = true
			merged.Items = append(merged.Items, item)
		}
	}
	return merged, errors.Join(errs...)
}

func (r *Runner) ApplyAutocomplete(ctx context.Context, request AutocompleteApplyRequest) (AutocompleteApplyResult, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	providers := r.RegisteredAutocompleteProviders()
	provider, ok := autocompleteProviderForItem(providers, request.Item)
	if !ok {
		return AutocompleteApplyResult{}, false, nil
	}
	if provider.Apply == nil {
		return AutocompleteApplyResult{}, false, nil
	}
	result, err := provider.Apply(ctx, request)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			r.EmitError(err)
		}
		return result, true, err
	}
	return result, true, nil
}

func autocompleteProviderForItem(providers []AutocompleteProviderDefinition, item AutocompleteItem) (AutocompleteProviderDefinition, bool) {
	if item.ProviderID != 0 {
		for _, provider := range providers {
			if provider.ID == item.ProviderID {
				return provider, true
			}
		}
		return AutocompleteProviderDefinition{}, false
	}
	if item.Source != "" {
		sourceIndex := 0
		for _, provider := range providers {
			if provider.Source != item.Source {
				continue
			}
			if sourceIndex == item.SourceIndex {
				return provider, true
			}
			sourceIndex++
		}
	}
	providerIndex := item.Provider
	if providerIndex < 0 || providerIndex >= len(providers) {
		return AutocompleteProviderDefinition{}, false
	}
	return providers[providerIndex], true
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
