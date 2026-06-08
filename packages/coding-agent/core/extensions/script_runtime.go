package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

type scriptToolMetadata struct {
	Name        string         `json:"name"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type scriptCommandMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type scriptShortcutMetadata struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

type scriptFlagMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Default     any    `json:"default"`
}

type scriptReadyMessage struct {
	Type                  string                          `json:"type"`
	Tools                 []scriptToolMetadata            `json:"tools"`
	Commands              []scriptCommandMetadata         `json:"commands"`
	Shortcuts             []scriptShortcutMetadata        `json:"shortcuts"`
	AutocompleteProviders int                             `json:"autocompleteProviders"`
	Providers             []scriptProviderMetadata        `json:"providers"`
	MessageRenderers      []scriptMessageRendererMetadata `json:"messageRenderers"`
	Flags                 []scriptFlagMetadata            `json:"flags"`
	Events                []string                        `json:"events"`
	Error                 string                          `json:"error"`
}

type scriptShortcutUpdateMessage struct {
	Type     string                 `json:"type"`
	Shortcut scriptShortcutMetadata `json:"shortcut"`
	Key      string                 `json:"key"`
}

type scriptProviderUpdateMessage struct {
	Type         string                 `json:"type"`
	Provider     scriptProviderMetadata `json:"provider"`
	API          string                 `json:"api"`
	ProviderName string                 `json:"providerName"`
}

type scriptMessageRendererUpdateMessage struct {
	Type       string                        `json:"type"`
	Renderer   scriptMessageRendererMetadata `json:"renderer"`
	CustomType string                        `json:"customType"`
}

type scriptResponseMessage struct {
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

type scriptRuntime struct {
	path   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *syncBuffer
	nextID int64

	// writeMu serializes concurrent stdin writes (each request emits one JSON
	// line). It is held only for the duration of the write, never across the
	// blocking wait for a response, so a slow/blocked extension never serializes
	// unrelated requests or blocks cancellation.
	writeMu sync.Mutex

	// A single background reader goroutine (readLoop) owns the scanner and
	// dispatches each response to the per-request channel registered in pending,
	// keyed by request id. request() selects on {response, ctx.Done(), readDone}
	// so a cancelled context unblocks it without depending on the extension.
	pendingMu sync.Mutex
	pending   map[int64]chan scriptResponseMessage
	// providerChunks routes out-of-band provider_chunk events (token-level
	// streaming) to the ProviderStream consumer for that call id. Guarded by
	// pendingMu. readLoop sends non-blocking (drops under backpressure) since the
	// final integer-id reply remains the authoritative source of the message.
	providerChunks map[int64]chan scriptProviderChunkEvent
	readDone       chan struct{}
	readErr        error // protected by pendingMu; set once before readDone closes

	// ctx is the session-scoped context; cancel tears down the runtime so the
	// event callback and any in-flight requests stop after cancellation.
	ctx    context.Context
	cancel context.CancelFunc

	// uiHandler resolves the host's server-initiated UI request handler at request
	// time (so a handler bound after load — e.g. by the TUI — is still seen). nil
	// when the host wired none.
	uiHandler func() UIRequestHandler

	// contextProvider/actionHandler mirror the host-backed ExtensionContext. They
	// are resolved dynamically from API so a script runtime loaded before the
	// AgentSession exists still sees the live session once it is bound.
	contextProvider func() ExtensionContextSnapshot
	actionHandler   func() ExtensionContextActionHandler

	shortcutRegister          func(scriptShortcutMetadata)
	shortcutUnregister        func(string)
	providerRegister          func(scriptProviderMetadata)
	providerUnregister        func(string, string)
	messageRendererRegister   func(scriptMessageRendererMetadata)
	messageRendererUnregister func(string)

	// hasUI* carry the latest ctx.hasUI capability to hasUIWriteLoop without
	// blocking the caller: sendSetHasUI records the seq-stamped state under
	// hasUIMu and wakes the loop via the buffered(1) hasUIWake; see ui_bridge.go.
	hasUIMu      sync.Mutex
	hasUISeq     uint64
	hasUIPending bool
	hasUIWake    chan struct{}
}

func LoadScriptExtensions(ctx context.Context, api *API, paths []string, flagValues map[string]any) []error {
	if api == nil || len(paths) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var errs []error
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := loadScriptExtension(ctx, api, path, flagValues); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func loadScriptExtension(ctx context.Context, api *API, path string, flagValues map[string]any) error {
	// ctx.hasUI mirrors whether a UI handler is bound (TS model). It may be bound
	// before load (reflected in the spawn env) or after (pushed via set_has_ui).
	runtime, ready, err := startScriptRuntime(
		ctx,
		path,
		flagValues,
		api.UIHandler() != nil,
		api.UIHandler,
		api.ContextSnapshot,
		api.ContextActionHandler,
		func(runtime *scriptRuntime, shortcut scriptShortcutMetadata) {
			registerScriptShortcut(api, runtime, path, shortcut)
		},
		func(_ *scriptRuntime, key string) {
			api.UnregisterShortcutSource(key, path)
		},
		func(runtime *scriptRuntime, provider scriptProviderMetadata) {
			registerScriptProvider(api, runtime, path, provider)
		},
		func(_ *scriptRuntime, providerName, apiID string) {
			unregisterScriptProvider(api, path, providerName, apiID)
		},
		func(runtime *scriptRuntime, renderer scriptMessageRendererMetadata) {
			registerScriptMessageRenderer(api, runtime, path, renderer)
		},
		func(_ *scriptRuntime, customType string) {
			unregisterScriptMessageRenderer(api, path, customType)
		},
	)
	if err != nil {
		return err
	}
	// Forward later handler binds/unbinds so the extension's ctx.hasUI stays live.
	api.registerUIListener(runtime.sendSetHasUI)
	for _, flag := range ready.Flags {
		if flag.Name == "" {
			continue
		}
		// Declared so the host can surface it in --help; the flag's value is
		// resolved inside the script runtime (seeded from flagValues at spawn).
		// scriptFlagMetadata is layout-identical to FlagDefinition by design.
		api.RegisterFlag(FlagDefinition(flag))
	}
	for _, tool := range ready.Tools {
		tool := tool
		if tool.Name == "" {
			continue
		}
		api.RegisterTool(ToolDefinition{
			Name:        tool.Name,
			Label:       firstNonEmpty(tool.Label, tool.Name),
			Description: tool.Description,
			Parameters:  tool.Parameters,
			Execute: func(ctx context.Context, raw []byte) (ai.ToolResult, error) {
				return runtime.ExecuteTool(ctx, tool.Name, raw)
			},
		})
	}
	for _, command := range ready.Commands {
		command := command
		if command.Name != "" {
			api.RegisterCommandHandler(command.Name, command.Description, func(ctx context.Context, args string) (string, error) {
				return runtime.ExecuteCommand(ctx, command.Name, args)
			})
		}
	}
	for _, shortcut := range ready.Shortcuts {
		registerScriptShortcut(api, runtime, path, shortcut)
	}
	if ready.AutocompleteProviders > 0 {
		api.RegisterAutocompleteProvider(AutocompleteProviderDefinition{
			Source: path,
			Suggest: func(ctx context.Context, request AutocompleteRequest) (AutocompleteSuggestions, error) {
				return runtime.Autocomplete(ctx, request)
			},
			Apply: func(ctx context.Context, request AutocompleteApplyRequest) (AutocompleteApplyResult, error) {
				return runtime.ApplyAutocomplete(ctx, request)
			},
		})
	}
	for _, provider := range ready.Providers {
		registerScriptProvider(api, runtime, path, provider)
	}
	for _, renderer := range ready.MessageRenderers {
		registerScriptMessageRenderer(api, runtime, path, renderer)
	}
	for _, event := range ready.Events {
		event := event
		if strings.TrimSpace(event) == "" {
			continue
		}
		api.On(event, func(payload any) {
			// Use the session-scoped context (not context.Background) so that once
			// the runtime is cancelled/shut down, the event callback declines fast
			// instead of dispatching to a torn-down extension process.
			if runtime.ctx.Err() != nil {
				return
			}
			result, err := runtime.Emit(runtime.ctx, event, payload)
			if err == nil {
				applyScriptEventResult(event, payload, result)
			}
		})
	}
	api.OnShutdown(func(context.Context) error {
		ai.UnregisterProviders(path)
		ai.RegisterBuiltinProviders()
		return nil
	})
	api.OnShutdown(runtime.Shutdown)
	return nil
}

func registerScriptShortcut(api *API, runtime *scriptRuntime, path string, shortcut scriptShortcutMetadata) {
	if api == nil || runtime == nil || strings.TrimSpace(shortcut.Key) == "" {
		return
	}
	shortcutCopy := shortcut
	api.RegisterShortcut(ShortcutDefinition{
		Key:         shortcutCopy.Key,
		Description: shortcutCopy.Description,
		Source:      path,
		Execute: func(ctx context.Context) error {
			return runtime.ExecuteShortcut(ctx, shortcutCopy.Key)
		},
	})
}

func registerScriptProvider(api *API, runtime *scriptRuntime, path string, provider scriptProviderMetadata) {
	if api == nil || runtime == nil {
		return
	}
	apiID := strings.TrimSpace(provider.API)
	providerName := strings.TrimSpace(provider.ProviderName)
	hasModelConfig := hasProviderModelConfig(provider.ModelConfig)
	if apiID == "" && providerName == "" {
		return
	}
	var adapter ai.Provider
	if provider.HasHandler && apiID != "" {
		scriptAdapter := &scriptAIProvider{api: apiID, runtime: runtime}
		adapter = scriptAdapter
		ai.RegisterProvider(scriptAdapter, path)
	}
	if adapter == nil && !hasModelConfig {
		return
	}
	api.RegisterProvider(ProviderDefinition{
		API:          apiID,
		ProviderName: providerName,
		Source:       path,
		Provider:     adapter,
		ModelConfig:  provider.ModelConfig,
	})
}

func unregisterScriptProvider(api *API, path, providerName, apiID string) {
	apiID = strings.TrimSpace(apiID)
	providerName = strings.TrimSpace(providerName)
	if apiID == "" && providerName == "" {
		return
	}
	if apiID != "" {
		ai.UnregisterProvider(apiID, path)
		ai.RegisterBuiltinProviders()
	}
	if api != nil {
		api.UnregisterProviderSource(firstNonEmpty(providerName, apiID), path)
	}
}

func registerScriptMessageRenderer(api *API, runtime *scriptRuntime, path string, renderer scriptMessageRendererMetadata) {
	if api == nil || runtime == nil {
		return
	}
	customType := strings.TrimSpace(renderer.CustomType)
	if customType == "" {
		return
	}
	api.RegisterMessageRenderer(MessageRendererDefinition{
		CustomType: customType,
		Source:     path,
		Render: func(ctx context.Context, request MessageRenderRequest) (MessageRenderResult, error) {
			request.CustomType = customType
			return runtime.RenderMessage(ctx, request)
		},
	})
}

func unregisterScriptMessageRenderer(api *API, path, customType string) {
	if api == nil {
		return
	}
	api.UnregisterMessageRendererSource(customType, path)
}

func startScriptRuntime(
	ctx context.Context,
	path string,
	flagValues map[string]any,
	hasUI bool,
	uiHandler func() UIRequestHandler,
	contextProvider func() ExtensionContextSnapshot,
	actionHandler func() ExtensionContextActionHandler,
	shortcutRegister func(*scriptRuntime, scriptShortcutMetadata),
	shortcutUnregister func(*scriptRuntime, string),
	providerRegister func(*scriptRuntime, scriptProviderMetadata),
	providerUnregister func(*scriptRuntime, string, string),
	messageRendererRegister func(*scriptRuntime, scriptMessageRendererMetadata),
	messageRendererUnregister func(*scriptRuntime, string),
) (*scriptRuntime, scriptReadyMessage, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: node executable is required to load script extensions", path)
	}
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "--eval", scriptRuntimeBridge, "--", path)
	cmd.Dir = filepath.Dir(path)
	env := os.Environ()
	// Seed extension CLI flag values before the factory runs so getFlag resolves
	// command-line values (the host does not yet know which flags the extension
	// declares, so it forwards all unknown flags; the bridge gates by name).
	if len(flagValues) > 0 {
		if encoded, err := json.Marshal(flagValues); err == nil {
			env = append(env, "PI_EXTENSION_FLAG_VALUES="+string(encoded))
		}
	}
	// Tell the bridge whether the host can answer ctx.ui requests so ctx.hasUI is
	// accurate (UI-gated extensions check it before prompting).
	if hasUI {
		env = append(env, "PI_EXTENSION_HAS_UI=1")
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: %w", path, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: %w", path, err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: %w", path, err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	if !scanner.Scan() {
		_ = cmd.Wait()
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: extension loader exited before ready%s", path, scriptStderrSuffix(stderr))
	}
	var ready scriptReadyMessage
	if err := json.Unmarshal(scanner.Bytes(), &ready); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: invalid extension loader response: %w", path, err)
	}
	if ready.Type == "error" || ready.Error != "" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: %s%s", path, firstNonEmpty(ready.Error, "extension failed to load"), scriptStderrSuffix(stderr))
	}
	// Derive a session-scoped context so cancelling the parent (or Shutdown)
	// propagates to in-flight requests and the event callback.
	runtimeCtx, cancel := context.WithCancel(ctx)
	r := &scriptRuntime{
		path:            path,
		cmd:             cmd,
		stdin:           stdin,
		stderr:          stderr,
		pending:         make(map[int64]chan scriptResponseMessage),
		providerChunks:  make(map[int64]chan scriptProviderChunkEvent),
		readDone:        make(chan struct{}),
		ctx:             runtimeCtx,
		cancel:          cancel,
		uiHandler:       uiHandler,
		contextProvider: contextProvider,
		actionHandler:   actionHandler,
		hasUIWake:       make(chan struct{}, 1),
	}
	if shortcutRegister != nil {
		r.shortcutRegister = func(shortcut scriptShortcutMetadata) {
			shortcutRegister(r, shortcut)
		}
	}
	if shortcutUnregister != nil {
		r.shortcutUnregister = func(key string) {
			shortcutUnregister(r, key)
		}
	}
	if providerRegister != nil {
		r.providerRegister = func(provider scriptProviderMetadata) {
			providerRegister(r, provider)
		}
	}
	if providerUnregister != nil {
		r.providerUnregister = func(providerName, apiID string) {
			providerUnregister(r, providerName, apiID)
		}
	}
	if messageRendererRegister != nil {
		r.messageRendererRegister = func(renderer scriptMessageRendererMetadata) {
			messageRendererRegister(r, renderer)
		}
	}
	if messageRendererUnregister != nil {
		r.messageRendererUnregister = func(customType string) {
			messageRendererUnregister(r, customType)
		}
	}
	// uiHandler is set above, before the reader goroutine starts, so the
	// readLoop-spawned handleUIRequest never races the assignment.
	// The ready message was already consumed above; the reader goroutine takes
	// over the scanner for all subsequent response lines.
	go r.readLoop(scanner)
	// Dedicated writer for set_has_ui frames so late handler binds never block the
	// host; exits when runtimeCtx is cancelled (Shutdown).
	go r.hasUIWriteLoop()
	return r, ready, nil
}

func (r *scriptRuntime) ExecuteTool(ctx context.Context, toolName string, raw []byte) (ai.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var params json.RawMessage
	if len(raw) > 0 {
		params = append(json.RawMessage(nil), raw...)
	} else {
		params = json.RawMessage(`{}`)
	}
	response, err := r.request(ctx, map[string]any{
		"type":     "execute_tool",
		"toolName": toolName,
		"params":   params,
	})
	if err != nil {
		return ai.ToolResult{}, err
	}
	var result ai.ToolResult
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return result, nil
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return ai.ToolResult{}, fmt.Errorf("%s: invalid tool result for %s: %w", r.path, toolName, err)
	}
	return result, nil
}

func (r *scriptRuntime) ExecuteCommand(ctx context.Context, commandName, args string) (string, error) {
	response, err := r.request(ctx, map[string]any{
		"type":        "execute_command",
		"commandName": commandName,
		"args":        args,
	})
	if err != nil {
		return "", err
	}
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(response.Result, &text); err == nil {
		return text, nil
	}
	return strings.TrimSpace(string(response.Result)), nil
}

func (r *scriptRuntime) ExecuteShortcut(ctx context.Context, key string) error {
	_, err := r.request(ctx, map[string]any{
		"type": "execute_shortcut",
		"key":  key,
	})
	return err
}

func (r *scriptRuntime) Autocomplete(ctx context.Context, request AutocompleteRequest) (AutocompleteSuggestions, error) {
	response, err := r.request(ctx, map[string]any{
		"type":    "autocomplete",
		"request": request,
	})
	if err != nil {
		return AutocompleteSuggestions{}, err
	}
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return AutocompleteSuggestions{}, nil
	}
	var result AutocompleteSuggestions
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return AutocompleteSuggestions{}, fmt.Errorf("%s: invalid autocomplete result: %w", r.path, err)
	}
	return result, nil
}

func (r *scriptRuntime) ApplyAutocomplete(ctx context.Context, request AutocompleteApplyRequest) (AutocompleteApplyResult, error) {
	response, err := r.request(ctx, map[string]any{
		"type":    "autocomplete_apply",
		"request": request,
	})
	if err != nil {
		return AutocompleteApplyResult{}, err
	}
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return AutocompleteApplyResult{}, nil
	}
	var result AutocompleteApplyResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return AutocompleteApplyResult{}, fmt.Errorf("%s: invalid autocomplete apply result: %w", r.path, err)
	}
	return result, nil
}

func (r *scriptRuntime) Emit(ctx context.Context, event string, payload any) (json.RawMessage, error) {
	response, err := r.request(ctx, map[string]any{
		"type":    "emit",
		"event":   event,
		"payload": payload,
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func applyScriptEventResult(event string, payload any, result json.RawMessage) {
	if payload == nil || len(result) == 0 || string(result) == "null" {
		return
	}
	switch normalizeEventKey(event) {
	// session_before_* hooks carry their decision (cancel/result) back in the
	// payload; tool_call/tool_result carry the handler's block/mutation/override so
	// the BeforeToolCall/AfterToolCall hooks can apply them to the execution chain.
	case "session_before_switch", "session_before_fork", "session_before_compact", "session_before_tree",
		"tool_call", "tool_result":
	default:
		return
	}
	_ = json.Unmarshal(result, payload)
}

// scriptShutdownTimeout bounds the shutdown handshake so a hung extension
// onShutdown handler can't block teardown forever before the process is killed.
const scriptShutdownTimeout = 5 * time.Second

func (r *scriptRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// If the caller set no deadline (the dispose path passes context.Background()),
	// bound the handshake ourselves; otherwise a script whose onShutdown never
	// resolves would block request() — and thus the host's dispose — forever, since
	// the Kill below is sequenced after this request.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, scriptShutdownTimeout)
		defer cancel()
	}
	_, err := r.request(ctx, map[string]any{"type": "shutdown"})
	// Cancel the session context so the event callback and any in-flight requests
	// stop after shutdown, then tear down the process and reader goroutine.
	if r.cancel != nil {
		r.cancel()
	}
	_ = r.stdin.Close()
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	if r.cmd != nil {
		_ = r.cmd.Wait()
	}
	return err
}
