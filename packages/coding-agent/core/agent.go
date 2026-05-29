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

func (a *AgentSession) newLoopAgent(sink ai.EventSink, maxLoopHit *bool, persistErr *error) *agentcore.Agent {
	turnCount := 0
	extensionTurnIndex := 0
	sessionCtx := a.Session.BuildContext()
	opts := agentcore.AgentOptions{
		InitialState: agentcore.AgentState{
			SystemPrompt:  a.SystemPrompt,
			Model:         a.Model,
			ThinkingLevel: a.ThinkingLevel,
			Tools:         agentTools(a.Tools),
			Messages:      sessionCtx.Messages,
		},
		Registry:      a.Registry,
		SteeringMode:  queueMode(a.steeringMode),
		FollowUpMode:  queueMode(a.followUpMode),
		ToolExecution: agentcore.ToolExecutionSequential,
		ShouldStopAfterTurn: func(ctx context.Context, turn agentcore.ShouldStopAfterTurnContext) (bool, error) {
			turnCount++
			if turnCount >= DefaultAgentMaxLoop && len(turn.ToolResults) > 0 {
				*maxLoopHit = true
				return true, nil
			}
			return false, nil
		},
	}
	if a.Settings != nil && a.Settings.BlockImages() {
		streamFn := agentcore.DefaultStreamFn(a.Registry)
		opts.StreamFn = func(ctx context.Context, model ai.Model, agentContext ai.Context, options ai.StreamOptions) agentcore.AssistantStream {
			agentContext.Messages = filterImageBlocks(agentContext.Messages)
			return streamFn(ctx, model, agentContext, options)
		}
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

func agentTools(tools ToolSet) []agentcore.AgentTool {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]agentcore.AgentTool, 0, len(names))
	for _, name := range names {
		out = append(out, agentToolAdapter{tool: tools[name]})
	}
	return out
}

type agentToolAdapter struct {
	tool catools.RuntimeTool
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

func (a *AgentSession) SetModel(provider, modelID string) (ai.Model, error) {
	model, ok := a.Registry.Find(provider, modelID)
	if !ok {
		return ai.Model{}, fmt.Errorf("model not found: %s/%s", provider, modelID)
	}
	a.Model = model
	_ = a.Session.AppendModelChange(provider, modelID)
	a.emitSessionEvent(ModelChangedEvent{Model: model, ThinkingLevel: a.ThinkingLevel})
	return model, nil
}

func (a *AgentSession) CycleModel() (map[string]any, bool) {
	models := a.availableModels()
	if len(models) <= 1 {
		return nil, false
	}
	idx := 0
	for i, m := range models {
		if m.Provider == a.Model.Provider && m.ID == a.Model.ID {
			idx = i
			break
		}
	}
	next := models[(idx+1)%len(models)]
	a.Model = next
	_ = a.Session.AppendModelChange(next.Provider, next.ID)
	a.emitSessionEvent(ModelChangedEvent{Model: next, ThinkingLevel: a.ThinkingLevel})
	return map[string]any{"model": next, "thinkingLevel": a.ThinkingLevel, "isScoped": len(a.scopedModels) > 0}, true
}

func (a *AgentSession) SetThinkingLevel(level ai.ThinkingLevel) error {
	if !ai.IsValidThinkingLevel(string(level)) {
		return fmt.Errorf("invalid thinking level: %s", level)
	}
	a.ThinkingLevel = ClampThinking(a.Model, level)
	if err := a.Session.AppendThinkingChange(a.ThinkingLevel); err != nil {
		return err
	}
	a.emitSessionEvent(ThinkingLevelChangedEvent{Level: a.ThinkingLevel})
	return nil
}

func (a *AgentSession) CycleThinkingLevel() (ai.ThinkingLevel, bool) {
	levels := a.Model.ThinkingLevels
	if len(levels) == 0 {
		levels = []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingMinimal, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh}
	}
	if len(levels) <= 1 {
		return a.ThinkingLevel, false
	}
	idx := 0
	for i, l := range levels {
		if l == a.ThinkingLevel {
			idx = i
			break
		}
	}
	next := levels[(idx+1)%len(levels)]
	_ = a.SetThinkingLevel(next)
	return a.ThinkingLevel, true
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

func (a *AgentSession) Compact(customInstructions string, sink ai.EventSink) (map[string]any, error) {
	return a.compact(context.Background(), CompactionManual, customInstructions, sink)
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
