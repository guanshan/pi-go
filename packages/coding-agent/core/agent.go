package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	agentcore "github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

type AgentSession struct {
	Session               *SessionManager
	Settings              *SettingsManager
	Registry              *ai.ModelRegistry
	Resources             ResourceLoader
	ResourceLoaderOptions DefaultResourceLoaderOptions
	Model                 ai.Model
	ThinkingLevel         ai.ThinkingLevel
	Tools                 ToolSet
	SystemPrompt          string

	mu                             sync.Mutex
	streaming                      bool
	compacting                     bool
	autoCompactionEnabled          bool
	autoRetryEnabled               bool
	steeringMode                   string
	followUpMode                   string
	steeringQueue                  []queuedPrompt
	followUpQueue                  []queuedPrompt
	activeAgent                    *agentcore.Agent
	activeBashCancel               context.CancelFunc
	compactionCancel               context.CancelFunc
	branchSummaryCancel            context.CancelFunc
	retryCancel                    context.CancelFunc
	extensionRuntime               *coreext.Runner
	mutatedToolArgs                map[string]json.RawMessage
	mutatedToolInputs              map[string]any
	disposed                       bool
	scopedModels                   []ScopedModel
	nextSessionListenerID          uint64
	sessionListeners               map[uint64]SessionEventListener
	extensionUIContext             any
	extensionCommandContextActions any
	extensionAbortHandler          func()
	extensionShutdownHandler       ShutdownHandler
	extensionErrorListener         ExtensionErrorListener
	extensionErrorStop             func()
}

type queuedPrompt struct {
	Message string
	Images  []ai.ContentBlock
}

func NewAgentSession(session *SessionManager, settings *SettingsManager, registry *ai.ModelRegistry, resources ResourceLoader, model ai.Model, thinking ai.ThinkingLevel, tools ToolSet, systemPrompt string) *AgentSession {
	if thinking == "" {
		thinking = settings.DefaultThinkingLevel()
	}
	return &AgentSession{
		Session:               session,
		Settings:              settings,
		Registry:              registry,
		Resources:             resources,
		Model:                 model,
		ThinkingLevel:         thinking,
		Tools:                 tools,
		SystemPrompt:          systemPrompt,
		autoCompactionEnabled: settings.AutoCompactionEnabled(),
		autoRetryEnabled:      settings.AutoRetryEnabled(),
		steeringMode:          settings.SteeringMode(),
		followUpMode:          settings.FollowUpMode(),
		sessionListeners:      map[uint64]SessionEventListener{},
	}
}

type agentModelSnapshot struct {
	Model         ai.Model
	ThinkingLevel ai.ThinkingLevel
}

type agentLoopSnapshot struct {
	Model         ai.Model
	ThinkingLevel ai.ThinkingLevel
	SteeringMode  string
	FollowUpMode  string
}

func (a *AgentSession) modelSnapshot() agentModelSnapshot {
	if a == nil {
		return agentModelSnapshot{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return agentModelSnapshot{Model: a.Model, ThinkingLevel: a.ThinkingLevel}
}

func (a *AgentSession) loopSnapshot() agentLoopSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return agentLoopSnapshot{
		Model:         a.Model,
		ThinkingLevel: a.ThinkingLevel,
		SteeringMode:  a.steeringMode,
		FollowUpMode:  a.followUpMode,
	}
}

func (a *AgentSession) CurrentModel() ai.Model {
	return a.modelSnapshot().Model
}

func (a *AgentSession) CurrentThinkingLevel() ai.ThinkingLevel {
	return a.modelSnapshot().ThinkingLevel
}

func (a *AgentSession) promptActiveLocked() bool {
	return a.streaming || a.activeAgent != nil
}

func (a *AgentSession) Prompt(ctx context.Context, text string, images []ai.ContentBlock, sink ai.EventSink) error {
	return a.promptWithRetry(ctx, text, images, "", nil, sink)
}

// Send mirrors the TS AgentSession.prompt entry point: it dispatches a prompt,
// steering or queuing it as a follow-up when the agent is already streaming
// (per behavior), and reports the preflight outcome via the preflight callback
// before the (potentially long-running) agent loop executes. Callers that run
// Send on a background goroutine can rely on preflight firing synchronously
// relative to the streaming-state decision.
func (a *AgentSession) Send(ctx context.Context, text string, images []ai.ContentBlock, behavior StreamingBehavior, preflight func(bool), sink ai.EventSink) error {
	return a.promptWithRetry(ctx, text, images, behavior, preflight, sink)
}

func (a *AgentSession) QueueSteer(message string, images []ai.ContentBlock) {
	if expanded, ok := a.Resources.ExpandInput(message); ok {
		message = expanded
	}
	userMsg := ai.NewUserMessage(message, images)
	a.mu.Lock()
	active := a.activeAgent
	if active != nil {
		a.mu.Unlock()
		active.Steer(userMsg)
		a.emitQueueUpdate()
		return
	}
	defer a.mu.Unlock()
	a.steeringQueue = append(a.steeringQueue, queuedPrompt{Message: message, Images: images})
	go a.emitQueueUpdate()
}

func (a *AgentSession) QueueFollowUp(message string, images []ai.ContentBlock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpQueue = append(a.followUpQueue, queuedPrompt{Message: message, Images: images})
	go a.emitQueueUpdate()
}

// chatRequestFromStreamOptions builds an ai.ChatRequest from the model, context,
// and stream options handed to a StreamFn. It mirrors ai's internal
// chatRequestFromOptions field-for-field so callers can attach request-level
// settings, then route via Registry.StreamChat.
func chatRequestFromStreamOptions(model ai.Model, llmContext ai.Context, options ai.StreamOptions) ai.ChatRequest {
	return ai.ChatRequest{
		Model:           model,
		SystemPrompt:    llmContext.SystemPrompt,
		Messages:        llmContext.Messages,
		Tools:           ai.ToolsByName(llmContext.Tools),
		ThinkingLevel:   options.Reasoning,
		CacheRetention:  options.CacheRetention,
		SessionID:       options.SessionID,
		MaxTokens:       options.MaxTokens,
		Temperature:     options.Temperature,
		Headers:         options.Headers,
		Transport:       options.Transport,
		OnPayload:       options.OnPayload,
		OnResponse:      options.OnResponse,
		TimeoutMs:       options.TimeoutMs,
		IdleTimeoutMs:   options.IdleTimeoutMs,
		MaxRetries:      options.MaxRetries,
		MaxRetryDelayMs: options.MaxRetryDelayMs,
		ToolChoice:      options.ToolChoice,
		RequestMetadata: options.RequestMetadata,
		Metadata:        options.Metadata,
		ThinkingBudgets: options.ThinkingBudgets,
	}
}

func (a *AgentSession) newLoopAgent(sink ai.EventSink, maxLoopHit *bool, persistErr *error) *agentcore.Agent {
	turnCount := 0
	extensionTurnIndex := 0
	sessionCtx := a.Session.BuildContext()
	snapshot := a.loopSnapshot()
	opts := agentcore.AgentOptions{
		InitialState: agentcore.AgentState{
			SystemPrompt:  a.SystemPrompt,
			Model:         snapshot.Model,
			ThinkingLevel: snapshot.ThinkingLevel,
			Tools:         agentTools(a, a.Tools),
			Messages:      sessionCtx.Messages,
		},
		Registry:      a.Registry,
		SteeringMode:  queueMode(snapshot.SteeringMode),
		FollowUpMode:  queueMode(snapshot.FollowUpMode),
		ToolExecution: agentcore.ToolExecutionSequential,
		// Run the extension runner's tool_call/tool_result handlers as part of the
		// execution chain so security extensions (permission-gate, protected-paths)
		// can block, mutate, or override tool calls instead of merely observing them.
		BeforeToolCall: a.beforeExtensionToolCall,
		AfterToolCall:  a.afterExtensionToolCall,
		ShouldStopAfterTurn: func(ctx context.Context, turn agentcore.ShouldStopAfterTurnContext) (bool, error) {
			turnCount++
			if turnCount >= DefaultAgentMaxLoop && len(turn.ToolResults) > 0 {
				*maxLoopHit = true
				return true, nil
			}
			return false, nil
		},
	}
	// Mirror sdk.ts:383,391-393: wire the session id, transport, thinking
	// budgets, provider timeouts, and retry controls into the agent so the
	// prompt-cache key, session-affinity headers, transport selection, custom
	// thinking budget_tokens, and provider retry behavior actually take effect.
	// SessionID is read here (per newLoopAgent construction, i.e. each run) so it
	// reflects the current session after /new, /fork, or session switches.
	if a.Session != nil {
		opts.SessionID = a.Session.SessionID()
	}
	if a.Settings != nil {
		opts.Transport = a.Settings.Transport()
		opts.ThinkingBudgets = a.Settings.ThinkingBudgets()
		opts.TimeoutMs = a.Settings.ProviderRetryTimeoutMS()
		opts.IdleTimeoutMs = a.Settings.HTTPIdleTimeoutMS()
		opts.MaxRetries = a.Settings.ProviderRetryMaxRetries()
		opts.MaxRetryDelayMs = a.Settings.ProviderRetryMaxDelayMS()
	}
	// Always wrap the stream function so image filtering and the ChatRequest
	// conversion stay in one place. ai.StreamOptions now carries IdleTimeoutMs,
	// but this wrapper keeps the coding-agent request path explicit.
	blockImages := false
	if a.Settings != nil {
		blockImages = a.Settings.BlockImages()
	}
	registry := a.Registry
	opts.StreamFn = func(ctx context.Context, model ai.Model, agentContext ai.Context, options ai.StreamOptions) agentcore.AssistantStream {
		if blockImages {
			agentContext.Messages = filterImageBlocks(agentContext.Messages)
		}
		if registry == nil {
			// No registry to route a ChatRequest through; fall back to the default
			// stream path (idle timeout cannot be applied without a provider).
			return ai.Stream(ctx, model, agentContext, options)
		}
		req := chatRequestFromStreamOptions(model, agentContext, options)
		return registry.StreamChat(ctx, req)
	}
	loopAgent := agentcore.New(opts)
	loopAgent.Subscribe(func(ctx context.Context, event agentcore.AgentEvent) error {
		converted := agentEvent(event)
		emit(sink, converted)
		a.emitSessionEvent(AgentEventWrapper{Event: converted})
		a.emitExtensionAgentEvent(event, &extensionTurnIndex)
		// Persist each message as it completes (crash resistance): if the
		// process dies mid-turn, already-finalized user/assistant/toolResult
		// messages survive. Mirrors the per-message appendMessage in the
		// message_end handler of src/core/agent-session.ts. The batch append at
		// the end of the run has been removed to avoid double persistence.
		if end, ok := event.(agentcore.MessageEndEvent); ok && a.Session != nil {
			switch ai.MessageRole(end.Message) {
			case "user", "assistant", "toolResult":
				if err := a.Session.AppendMessage(end.Message); err != nil && persistErr != nil && *persistErr == nil {
					*persistErr = err
				}
			}
		}
		return nil
	})
	return loopAgent
}

func (a *AgentSession) finishPrompt(active *agentcore.Agent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streaming = false
	if active == nil || a.activeAgent == active {
		a.activeAgent = nil
	}
}

func (a *AgentSession) drainSteeringInto(loopAgent *agentcore.Agent) {
	for {
		next, ok := a.popSteering()
		if !ok {
			return
		}
		if expanded, ok := a.Resources.ExpandInput(next.Message); ok {
			next.Message = expanded
		}
		loopAgent.Steer(ai.NewUserMessage(next.Message, next.Images))
	}
}

func agentEvent(event agentcore.AgentEvent) ai.Event {
	out := ai.Event{"type": agentcore.AgentEventType(event)}
	switch ev := event.(type) {
	case agentcore.AgentEndEvent:
		out["messages"] = ev.Messages
		out["willRetry"] = false
	case agentcore.TurnEndEvent:
		out["message"] = ev.Message
		out["toolResults"] = ev.ToolResults
	case agentcore.MessageStartEvent:
		out["message"] = ev.Message
	case agentcore.MessageUpdateEvent:
		out["message"] = ev.Message
		out["assistantMessageEvent"] = ev.AssistantEvent
	case agentcore.MessageEndEvent:
		out["message"] = ev.Message
	case agentcore.ToolExecutionStartEvent:
		out["toolCallId"] = ev.ToolCallID
		out["toolName"] = ev.ToolName
		out["args"] = ev.Args
	case agentcore.ToolExecutionUpdateEvent:
		out["toolCallId"] = ev.ToolCallID
		out["toolName"] = ev.ToolName
		out["args"] = ev.Args
		out["partialResult"] = ev.PartialResult
	case agentcore.ToolExecutionEndEvent:
		out["toolCallId"] = ev.ToolCallID
		out["toolName"] = ev.ToolName
		out["result"] = ai.ToolResult{Content: ev.Result.Content, Details: ev.Result.Details, IsError: ev.IsError}
		out["isError"] = ev.IsError
	}
	return out
}

func agentTools(session *AgentSession, tools ToolSet) []agentcore.AgentTool {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]agentcore.AgentTool, 0, len(names))
	for _, name := range names {
		out = append(out, agentToolAdapter{session: session, tool: tools[name]})
	}
	return out
}

type agentToolAdapter struct {
	session *AgentSession
	tool    catools.RuntimeTool
}

func (t agentToolAdapter) Name() string { return t.tool.Name() }

func (t agentToolAdapter) Label() string { return t.tool.Name() }

func (t agentToolAdapter) Description() string { return t.tool.Description() }

func (t agentToolAdapter) Schema() map[string]any { return t.tool.Schema() }

func (t agentToolAdapter) Execute(ctx context.Context, raw json.RawMessage, onUpdate agentcore.ToolUpdateCallback) (result agentcore.AgentToolResult, err error) {
	// Bridge the framework's update callback to the tool runtime's so that
	// streaming tools (e.g. bash) can report incremental output, which the
	// harness surfaces as ToolExecutionUpdateEvent.
	var update catools.ToolUpdate
	if onUpdate != nil {
		update = func(partial ai.ToolResult) {
			onUpdate(agentcore.AgentToolResult{Content: partial.Content, Details: partial.Details, IsError: partial.IsError})
		}
	}
	// RuntimeTool.Execute has no error return — failures are signalled via
	// ToolResult.IsError. Recover any panic from a misbehaving (e.g. extension)
	// tool and turn it into an error result so a single bad tool cannot crash
	// the agent run. The framework also recovers at its own layer; this gives a
	// clearer, tool-scoped message.
	defer func() {
		if recovered := recover(); recovered != nil {
			result = agentcore.AgentToolResult{
				Content: ai.TextBlocks(fmt.Sprintf("tool %q panicked: %v", t.tool.Name(), recovered)),
				IsError: true,
			}
			err = nil
		}
	}()
	// Substitute any arguments a tool_call extension handler mutated in place. The
	// framework froze the raw bytes before the BeforeToolCall hook ran, so the
	// patched input is applied here at execution time.
	if t.session != nil {
		raw = t.session.consumeMutatedToolArgs(raw)
	}
	toolResult := t.tool.Execute(ctx, raw, update)
	return agentcore.AgentToolResult{Content: toolResult.Content, Details: toolResult.Details, IsError: toolResult.IsError}, nil
}

func queueMode(value string) agentcore.QueueMode {
	if value == string(agentcore.QueueAll) {
		return agentcore.QueueAll
	}
	return agentcore.QueueOneAtATime
}

func filterImageBlocks(messages []ai.Message) []ai.Message {
	if len(messages) == 0 {
		return messages
	}
	out := make([]ai.Message, len(messages))
	for i, msg := range messages {
		out[i] = msg
		blocks := ai.MessageBlocks(msg)
		if len(blocks) == 0 {
			continue
		}
		filtered := make([]ai.ContentBlock, 0, len(blocks))
		changed := false
		for _, block := range blocks {
			if block.Type == "image" {
				changed = true
				continue
			}
			filtered = append(filtered, block)
		}
		if changed {
			switch m := msg.(type) {
			case ai.UserMessage:
				m.Content = append([]ai.ContentBlock(nil), filtered...)
				out[i] = m
			case ai.ToolResultMessage:
				m.Content = append([]ai.ContentBlock(nil), filtered...)
				out[i] = m
			case ai.AssistantMessage:
				m.Content = append([]ai.ContentBlock(nil), filtered...)
				out[i] = m
			}
		}
	}
	return out
}

func (a *AgentSession) popSteering() (queuedPrompt, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.steeringQueue) == 0 {
		return queuedPrompt{}, false
	}
	if a.steeringMode == string(agentcore.QueueAll) {
		var combined []string
		var images []ai.ContentBlock
		for _, q := range a.steeringQueue {
			combined = append(combined, q.Message)
			images = append(images, q.Images...)
		}
		a.steeringQueue = nil
		return queuedPrompt{Message: strings.Join(combined, "\n\n"), Images: images}, true
	}
	q := a.steeringQueue[0]
	a.steeringQueue = a.steeringQueue[1:]
	return q, true
}

func (a *AgentSession) popFollowUp() (queuedPrompt, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.followUpQueue) == 0 {
		return queuedPrompt{}, false
	}
	if a.followUpMode == string(agentcore.QueueAll) {
		var combined []string
		var images []ai.ContentBlock
		for _, q := range a.followUpQueue {
			combined = append(combined, q.Message)
			images = append(images, q.Images...)
		}
		a.followUpQueue = nil
		return queuedPrompt{Message: strings.Join(combined, "\n\n"), Images: images}, true
	}
	q := a.followUpQueue[0]
	a.followUpQueue = a.followUpQueue[1:]
	return q, true
}

type AgentSessionState struct {
	Model                 ai.Model         `json:"model"`
	ThinkingLevel         ai.ThinkingLevel `json:"thinkingLevel"`
	IsStreaming           bool             `json:"isStreaming"`
	IsCompacting          bool             `json:"isCompacting"`
	SteeringMode          string           `json:"steeringMode"`
	FollowUpMode          string           `json:"followUpMode"`
	SessionFile           string           `json:"sessionFile"`
	SessionID             string           `json:"sessionId"`
	SessionName           string           `json:"sessionName"`
	AutoCompactionEnabled bool             `json:"autoCompactionEnabled"`
	AutoRetryEnabled      bool             `json:"autoRetryEnabled"`
	MessageCount          int              `json:"messageCount"`
	PendingMessageCount   int              `json:"pendingMessageCount"`
}

func (a *AgentSession) State() AgentSessionState {
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx := a.Session.BuildContext()
	return AgentSessionState{
		Model:                 a.Model,
		ThinkingLevel:         a.ThinkingLevel,
		IsStreaming:           a.streaming,
		IsCompacting:          a.compacting,
		SteeringMode:          a.steeringMode,
		FollowUpMode:          a.followUpMode,
		SessionFile:           a.Session.File(),
		SessionID:             a.Session.SessionID(),
		SessionName:           ctx.Name,
		AutoCompactionEnabled: a.autoCompactionEnabled,
		AutoRetryEnabled:      a.autoRetryEnabled,
		MessageCount:          len(ctx.Messages),
		PendingMessageCount:   len(a.steeringQueue) + len(a.followUpQueue),
	}
}

// syncReadToolModelSupportLocked keeps the read tool's non-vision image note in
// sync with the live model after a /model switch (read.ts reads ctx.model at
// execute time; the Go tool is a value baked at build time, so re-stamp it).
// Caller must hold a.mu.
func (a *AgentSession) syncReadToolModelSupportLocked(model ai.Model) {
	if a.Tools == nil {
		return
	}
	if rt, ok := a.Tools["read"].(catools.ReadTool); ok {
		rt.ModelSupportsImages = ai.SupportsInput(model, "image")
		a.Tools["read"] = rt
	}
}

func (a *AgentSession) SetModel(provider, modelID string) (ai.Model, error) {
	model, ok := a.Registry.Find(provider, modelID)
	if !ok {
		return ai.Model{}, fmt.Errorf("model not found: %s/%s", provider, modelID)
	}
	a.mu.Lock()
	if a.promptActiveLocked() {
		a.mu.Unlock()
		return ai.Model{}, errorsString("can't switch model while a response is streaming")
	}
	a.Model = model
	a.syncReadToolModelSupportLocked(model)
	thinkingLevel := a.ThinkingLevel
	_ = a.Session.AppendModelChange(provider, modelID)
	// Persist as the global default so a fresh launch remembers the choice,
	// mirroring agent-session.ts:1448 setModel -> setDefaultModelAndProvider.
	if a.Settings != nil {
		_ = a.Settings.SetDefaultModelAndProvider(provider, modelID)
	}
	a.mu.Unlock()
	a.emitSessionEvent(ModelChangedEvent{Model: model, ThinkingLevel: thinkingLevel})
	return model, nil
}

func (a *AgentSession) CycleModel() (map[string]any, bool) {
	return a.cycleModelInDirection(1)
}

// CycleModelBackward cycles to the previous available model, mirroring TS
// session.cycleModel("backward") (interactive-mode.ts cycleModel) for the
// Shift+Ctrl+P binding.
func (a *AgentSession) CycleModelBackward() (map[string]any, bool) {
	return a.cycleModelInDirection(-1)
}

func (a *AgentSession) cycleModelInDirection(step int) (map[string]any, bool) {
	models, isScoped := a.availableModelsWithScoped()
	if len(models) <= 1 {
		return nil, false
	}
	a.mu.Lock()
	if a.promptActiveLocked() {
		a.mu.Unlock()
		// Distinguish a streaming-busy refusal from "only one model" (len<=1 above)
		// so the TUI can show the right reason; both still report ok=false.
		return map[string]any{"busy": true}, false
	}
	current := a.Model
	thinkingLevel := a.ThinkingLevel
	idx := 0
	for i, m := range models {
		if m.Provider == current.Provider && m.ID == current.ID {
			idx = i
			break
		}
	}
	// Euclidean modulo so a backward step from index 0 wraps to the last model.
	next := models[((idx+step)%len(models)+len(models))%len(models)]
	a.Model = next
	a.syncReadToolModelSupportLocked(next)
	_ = a.Session.AppendModelChange(next.Provider, next.ID)
	// Persist the cycled model as the global default, mirroring
	// agent-session.ts:1485/1513 (_cycleScopedModel/_cycleAvailableModel ->
	// setDefaultModelAndProvider).
	if a.Settings != nil {
		_ = a.Settings.SetDefaultModelAndProvider(next.Provider, next.ID)
	}
	a.mu.Unlock()
	a.emitSessionEvent(ModelChangedEvent{Model: next, ThinkingLevel: thinkingLevel})
	return map[string]any{"model": next, "thinkingLevel": thinkingLevel, "isScoped": isScoped}, true
}

func (a *AgentSession) SetThinkingLevel(level ai.ThinkingLevel) error {
	if !ai.IsValidThinkingLevel(string(level)) {
		return fmt.Errorf("invalid thinking level: %s", level)
	}
	// Clamp to the model's capabilities, then persist to session + settings only
	// when the effective level actually changes, mirroring
	// agent-session.ts:1532 setThinkingLevel (which gates the session append,
	// settings write, and event on isChanging).
	a.mu.Lock()
	if a.promptActiveLocked() {
		a.mu.Unlock()
		return errorsString("can't switch thinking level while a response is streaming")
	}
	model := a.Model
	effectiveLevel := ClampThinking(model, level)
	previousLevel := a.ThinkingLevel
	a.ThinkingLevel = effectiveLevel
	supportsThinking := modelSupportsThinking(model)
	if effectiveLevel == previousLevel {
		a.mu.Unlock()
		return nil
	}
	if err := a.Session.AppendThinkingChange(effectiveLevel); err != nil {
		a.mu.Unlock()
		return err
	}
	// Only persist as the global default when the model supports thinking or the
	// level is non-off, mirroring the `supportsThinking() || level !== "off"`
	// guard at agent-session.ts:1544.
	if a.Settings != nil && (supportsThinking || effectiveLevel != ai.ThinkingOff) {
		_ = a.Settings.SetDefaultThinkingLevel(effectiveLevel)
	}
	a.mu.Unlock()
	a.emitSessionEvent(ThinkingLevelChangedEvent{Level: effectiveLevel})
	return nil
}

func (a *AgentSession) CycleThinkingLevel() (ai.ThinkingLevel, bool) {
	a.mu.Lock()
	if a.promptActiveLocked() {
		level := a.ThinkingLevel
		a.mu.Unlock()
		return level, false
	}
	snapshot := agentModelSnapshot{Model: a.Model, ThinkingLevel: a.ThinkingLevel}
	a.mu.Unlock()
	levels := availableThinkingLevelsForModel(snapshot.Model)
	if len(levels) <= 1 {
		return snapshot.ThinkingLevel, false
	}
	idx := 0
	for i, l := range levels {
		if l == snapshot.ThinkingLevel {
			idx = i
			break
		}
	}
	next := levels[(idx+1)%len(levels)]
	if err := a.SetThinkingLevel(next); err != nil {
		return snapshot.ThinkingLevel, false
	}
	return a.CurrentThinkingLevel(), true
}

func (a *AgentSession) SetSessionName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	if err := a.Session.AppendSessionName(name); err != nil {
		return err
	}
	a.emitSessionEvent(SessionInfoChangedEvent{Name: name})
	return nil
}

// Compact manually compacts the session context.
//
// Deprecated: prefer CompactWithContext so the caller can cancel the manual
// compaction with its own request context. Compact delegates to
// CompactWithContext(context.Background(), ...).
func (a *AgentSession) Compact(customInstructions string, sink ai.EventSink) (map[string]any, error) {
	return a.CompactWithContext(context.Background(), customInstructions, sink)
}

// CompactWithContext manually compacts the session context. It first aborts any
// active agent operation so the compaction is the sole writer to the session,
// mirroring AgentSession.compact in agent-session.ts (which calls
// _disconnectFromAgent()/abort() before starting its own abort controller).
// The provided ctx cancels the manual compaction, including the
// session_before_compact extension hook and the provider compact request.
func (a *AgentSession) CompactWithContext(ctx context.Context, customInstructions string, sink ai.EventSink) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Abort the active agent run (and wait for it to go idle) before taking
	// over the session, so a streaming prompt cannot interleave session
	// entries with the compaction.
	if err := a.Abort(ctx); err != nil {
		return nil, err
	}
	return a.compact(ctx, CompactionManual, customInstructions, sink)
}

func ClampThinking(model ai.Model, level ai.ThinkingLevel) ai.ThinkingLevel {
	return ai.ClampThinking(model, level)
}

func estimateTokens(parts []string) int {
	total := 0
	for _, p := range parts {
		total += max(1, len(p)/4)
	}
	return total
}

type errorsString string

func (e errorsString) Error() string { return string(e) }
