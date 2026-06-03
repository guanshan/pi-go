package core

import (
	"context"
	"fmt"
	"strings"

	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func (a *AgentSession) BindExtensions(ctx context.Context, bindings ExtensionBindings) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if a == nil {
		return fmt.Errorf("agent session is nil")
	}
	a.mu.Lock()
	if a.disposed {
		a.mu.Unlock()
		return fmt.Errorf("agent session is disposed")
	}
	stop := a.extensionErrorStop
	runtime := a.extensionRuntime
	a.extensionUIContext = bindings.UIContext
	a.extensionCommandContextActions = bindings.CommandContextActions
	a.extensionAbortHandler = bindings.AbortHandler
	a.extensionShutdownHandler = bindings.ShutdownHandler
	a.extensionErrorListener = bindings.OnError
	a.extensionErrorStop = nil
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
	if runtime != nil && bindings.OnError != nil {
		a.mu.Lock()
		a.extensionErrorStop = runtime.OnError(coreext.ErrorListener(bindings.OnError))
		a.mu.Unlock()
	}
	return nil
}

func (a *AgentSession) SetExtensionUIHandler(handler coreext.UIRequestHandler) {
	if a == nil {
		return
	}
	a.mu.Lock()
	runtime := a.extensionRuntime
	a.mu.Unlock()
	if runtime == nil || runtime.API == nil {
		return
	}
	runtime.API.SetUIHandler(handler)
}

func (a *AgentSession) HasExtensionHandlers(eventType string) bool {
	if a == nil {
		return false
	}
	normalized := normalizeExtensionEventType(eventType)
	runtimeHasHandlers := a.extensionHasHandlers(normalized)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch normalized {
	case "", "*", "any":
		return runtimeHasHandlers ||
			a.extensionUIContext != nil ||
			a.extensionCommandContextActions != nil ||
			a.extensionAbortHandler != nil ||
			a.extensionShutdownHandler != nil ||
			a.extensionErrorListener != nil
	case "ui", "ui_context", "input", "select", "confirm", "editor":
		return a.extensionUIContext != nil
	case "command", "command_context", "command_context_actions", "slash_command":
		return a.extensionCommandContextActions != nil
	case "abort", "session_abort", "user_abort":
		return a.extensionAbortHandler != nil
	case "shutdown", "session_shutdown", "dispose", "session_dispose":
		return a.extensionShutdownHandler != nil
	case "error", "extension_error":
		return a.extensionErrorListener != nil
	default:
		return runtimeHasHandlers
	}
}

func (a *AgentSession) Reload(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil || a.Session == nil {
		return fmt.Errorf("session is nil")
	}
	a.emitExtensionSessionShutdown(coreext.SessionShutdownReload, "")
	a.shutdownExtensionRuntime(ctx)
	args := resourceLoaderArgs(a.ResourceLoaderOptions)
	resources := LoadResources(a.Session.CWD(), a.Settings.AgentDir, args, a.Settings)
	applyResourceLoaderOverrides(&resources, a.Session.CWD(), a.ResourceLoaderOptions)
	a.mu.Lock()
	a.Resources = resources
	a.SystemPrompt = resources.BuildSystemPrompt(args, ToolPromptInfoFor(a.Tools))
	a.mu.Unlock()
	a.emitExtensionSessionStart(coreext.SessionStartReload, "")
	return nil
}

func (a *AgentSession) Dispose() {
	a.disposeSession(true)
}

// disposeSession aborts any in-flight work (retry, compaction, branch summary,
// bash, active agent) and tears down the extension runtime. It mirrors the
// abort sequence in agent-session.ts dispose(). When emitShutdown is true it
// emits the session_shutdown(quit) event itself; the runtime disposal path
// emits that event up front and passes false to avoid a double emit.
func (a *AgentSession) disposeSession(emitShutdown bool) {
	if a == nil {
		return
	}
	a.AbortRetry()
	a.AbortCompaction()
	a.AbortBranchSummary()
	a.AbortBash()
	_ = a.abortActiveAgent(context.Background(), false)
	if emitShutdown {
		a.emitExtensionSessionShutdown(coreext.SessionShutdownQuit, "")
	}
	a.mu.Lock()
	runtime := a.extensionRuntime
	errorStop := a.extensionErrorStop
	shutdownHandler := a.extensionShutdownHandler
	errorListener := a.extensionErrorListener
	a.disposed = true
	a.steeringQueue = nil
	a.followUpQueue = nil
	a.activeAgent = nil
	a.activeBashCancel = nil
	a.compactionCancel = nil
	a.branchSummaryCancel = nil
	a.retryCancel = nil
	a.extensionRuntime = nil
	a.sessionListeners = nil
	a.extensionUIContext = nil
	a.extensionCommandContextActions = nil
	a.extensionAbortHandler = nil
	a.extensionShutdownHandler = nil
	a.extensionErrorListener = nil
	a.extensionErrorStop = nil
	a.mu.Unlock()
	if runtime != nil {
		_ = runtime.Shutdown(context.Background())
	}
	if errorStop != nil {
		errorStop()
	}
	a.invokeShutdownHandler(context.Background(), shutdownHandler, errorListener)
}

func normalizeExtensionEventType(eventType string) string {
	normalized := strings.ToLower(strings.TrimSpace(eventType))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized
}

func (a *AgentSession) invokeAbortHandler(handler func()) {
	if handler == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.reportExtensionError(fmt.Errorf("extension abort handler panicked: %v", recovered))
		}
	}()
	handler()
}

func (a *AgentSession) invokeShutdownHandler(ctx context.Context, handler ShutdownHandler, listener ExtensionErrorListener) {
	if handler == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.reportExtensionErrorWithListener(listener, fmt.Errorf("extension shutdown handler panicked: %v", recovered))
		}
	}()
	if err := handler(ctx); err != nil {
		a.reportExtensionErrorWithListener(listener, err)
	}
}

func (a *AgentSession) reportExtensionError(err error) {
	if err == nil || a == nil {
		return
	}
	a.mu.Lock()
	listener := a.extensionErrorListener
	a.mu.Unlock()
	a.reportExtensionErrorWithListener(listener, err)
}

func (a *AgentSession) reportExtensionErrorWithListener(listener ExtensionErrorListener, err error) {
	if listener == nil || err == nil {
		return
	}
	listener(err)
}

func (a *AgentSession) CreateReplacedSessionContext() ReplacedSessionContext {
	if a == nil || a.Session == nil || a.Settings == nil {
		return ReplacedSessionContext{}
	}
	return ReplacedSessionContext{
		Session: a,
		Services: &AgentSessionServices{
			Cwd:             a.Session.CWD(),
			AgentDir:        a.Settings.AgentDir,
			SettingsManager: a.Settings,
			ModelRegistry:   a.Registry,
			ResourceLoader:  a.Resources,
		},
	}
}

func ParseSkillBlock(text string) *ParsedSkillBlock {
	matches := skillBlockPattern.FindStringSubmatch(text)
	if matches == nil {
		return nil
	}
	parsed := &ParsedSkillBlock{Name: matches[1], Location: matches[2], Content: matches[3]}
	if len(matches) > 4 {
		parsed.UserMessage = strings.TrimSpace(matches[4])
	}
	return parsed
}
