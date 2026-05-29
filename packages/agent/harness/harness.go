package harness

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/guanshan/pi-go/packages/agent"
	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/agent/messagequeue"
	"github.com/guanshan/pi-go/packages/ai"
)

type AgentHarness struct {
	mu                    sync.Mutex
	phase                 Phase
	runCancel             context.CancelFunc
	runDone               chan struct{}
	pendingWrites         []pendingSessionWrite
	env                   harnessenv.ExecutionEnv
	sess                  *session.Session
	registry              *ai.ModelRegistry
	streamFn              agent.StreamFn
	systemPrompt          SystemPromptSource
	getAuth               APIKeyResolver
	resources             Resources
	streamOptions         StreamOptions
	model                 ai.Model
	thinkingLevel         ai.ThinkingLevel
	tools                 map[string]agent.AgentTool
	toolOrder             []string
	activeToolNames       []string
	activeToolNamesSet    bool
	steerQueue            *messagequeue.Queue
	followUpQueue         *messagequeue.Queue
	nextTurnQueue         *messagequeue.Queue
	steeringMode          agent.QueueMode
	followUpMode          agent.QueueMode
	listeners             map[uint64]func(context.Context, agent.AgentEvent) error
	harnessListeners      map[uint64]func(context.Context, HarnessEvent) error
	nextListenerID        uint64
	nextHarnessListenerID uint64
	dispatching           bool

	beforeAgentStartHandlers      []func(context.Context, BeforeAgentStartEvent) (*BeforeAgentStartResult, error)
	contextHandlers               []func(context.Context, ContextEvent) (*ContextResult, error)
	beforeProviderRequestHandlers []func(context.Context, BeforeProviderRequestEvent) (*BeforeProviderRequestResult, error)
	beforeProviderPayloadHandlers []func(context.Context, BeforeProviderPayloadEvent) (*BeforeProviderPayloadResult, error)
	afterProviderResponseHandlers []func(context.Context, AfterProviderResponseEvent) error
	toolCallHandlers              []func(context.Context, ToolCallEvent) (*ToolCallResult, error)
	toolResultHandlers            []func(context.Context, ToolResultEvent) (*ToolResultPatch, error)
	sessionBeforeCompactHandlers  []func(context.Context, SessionBeforeCompactEvent) (*SessionBeforeCompactResult, error)
	sessionCompactHandlers        []func(context.Context, SessionCompactEvent) error
	sessionBeforeTreeHandlers     []func(context.Context, SessionBeforeTreeEvent) (*SessionBeforeTreeResult, error)
	sessionTreeHandlers           []func(context.Context, SessionTreeEvent) error
	modelSelectHandlers           []func(context.Context, ModelSelectEvent) error
	thinkingLevelSelectHandlers   []func(context.Context, ThinkingLevelSelectEvent) error
	resourcesUpdateHandlers       []func(context.Context, ResourcesUpdateEvent) error
	toolsUpdateHandlers           []func(context.Context, ToolsUpdateEvent) error
}

func New(opts Options) (*AgentHarness, error) {
	sess := opts.Session
	if sess == nil {
		memory, err := session.NewMemory(session.Metadata{}, nil)
		if err != nil {
			return nil, err
		}
		sess = memory
	}
	tools := map[string]agent.AgentTool{}
	var toolOrder []string
	for _, tool := range opts.Tools {
		if tool == nil {
			continue
		}
		name := tool.Name()
		if _, exists := tools[name]; exists {
			return nil, &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: "duplicate tool name(s): " + name}
		}
		tools[name] = tool
		toolOrder = append(toolOrder, name)
	}
	activeToolNames := append([]string(nil), opts.ActiveToolNames...)
	activeToolNamesSet := opts.ActiveToolNames != nil
	if activeToolNamesSet {
		if err := validateActiveToolNames(activeToolNames, tools); err != nil {
			return nil, err
		}
	}
	steering := opts.SteeringMode
	if steering == "" {
		steering = agent.QueueOneAtATime
	}
	followUp := opts.FollowUpMode
	if followUp == "" {
		followUp = agent.QueueOneAtATime
	}
	return &AgentHarness{
		phase:              PhaseIdle,
		env:                opts.Env,
		sess:               sess,
		registry:           opts.Registry,
		streamFn:           opts.StreamFn,
		systemPrompt:       opts.SystemPrompt,
		getAuth:            opts.GetAPIKeyAndHeaders,
		resources:          cloneResources(opts.Resources),
		streamOptions:      cloneStreamOptions(opts.StreamOptions),
		model:              opts.Model,
		thinkingLevel:      opts.ThinkingLevel,
		tools:              tools,
		toolOrder:          toolOrder,
		activeToolNames:    activeToolNames,
		activeToolNamesSet: activeToolNamesSet,
		steerQueue:         messagequeue.New(""),
		followUpQueue:      messagequeue.New(""),
		nextTurnQueue:      messagequeue.New(""),
		steeringMode:       steering,
		followUpMode:       followUp,
		listeners:          map[uint64]func(context.Context, agent.AgentEvent) error{},
		harnessListeners:   map[uint64]func(context.Context, HarnessEvent) error{},
	}, nil
}

func (h *AgentHarness) Session() *session.Session {
	return h.sess
}

func (h *AgentHarness) Subscribe(f func(context.Context, agent.AgentEvent) error) func() {
	if f == nil {
		return func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextListenerID
	h.nextListenerID++
	if h.listeners == nil {
		h.listeners = map[uint64]func(context.Context, agent.AgentEvent) error{}
	}
	h.listeners[id] = f
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.listeners, id)
	}
}

func (h *AgentHarness) Prompt(ctx context.Context, text string, opts PromptOptions) (final ai.AssistantMessage, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := h.beginRun(ctx, PhaseTurn)
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	defer func() {
		if flushErr := h.flushPendingSessionWrites(ctx); flushErr != nil && err == nil {
			err = flushErr
		}
		release()
	}()

	state, err := h.createTurnState(ctx)
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	startResult, err := h.emitBeforeAgentStart(ctx, BeforeAgentStartEvent{
		Prompt:       text,
		Images:       append([]ai.ContentBlock(nil), opts.Images...),
		SystemPrompt: state.systemPrompt,
		Resources:    cloneResources(state.resources),
	})
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	if startResult != nil {
		if startResult.SystemPrompt != "" {
			state.systemPrompt = startResult.SystemPrompt
		}
	}
	promptMessages := h.nextTurnQueue.DrainAll()
	if len(promptMessages) > 0 {
		if err := h.emitHarness(ctx, h.queueUpdateEvent()); err != nil {
			return ai.AssistantMessage{}, err
		}
	}
	promptMessages = append(promptMessages, ai.NewUserMessage(text, opts.Images))
	if startResult != nil && startResult.Messages != nil {
		promptMessages = append(promptMessages, startResult.Messages...)
	}
	currentState := state
	getState := func() turnState {
		return currentState
	}
	setState := func(next turnState) {
		currentState = next
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	h.mu.Lock()
	h.runCancel = cancel
	h.mu.Unlock()

	loopCfg := h.loopConfig(getState, setState)
	_, err = agent.RunAgentLoop(runCtx, promptMessages, agent.AgentContext{
		SystemPrompt: state.systemPrompt,
		Messages:     state.messages,
		Tools:        state.tools,
	}, loopCfg, func(ctx context.Context, ev agent.AgentEvent) error {
		if end, ok := ev.(agent.AgentEndEvent); ok {
			for i := len(end.Messages) - 1; i >= 0; i-- {
				if assistant, ok := ai.AsAssistantMessage(end.Messages[i]); ok {
					final = assistant
					break
				}
			}
		}
		return h.handleAgentEvent(ctx, ev)
	}, h.streamForTurn(getState))
	if err != nil {
		return final, err
	}
	if final.Role == "" {
		return final, fmt.Errorf("agent did not produce an assistant message")
	}
	return final, nil
}

func (h *AgentHarness) AppendMessage(ctx context.Context, msg agent.AgentMessage) error {
	_, err := h.applyOrQueueSessionWrite(ctx, pendingMessageWrite{Message: msg})
	return err
}

func (h *AgentHarness) AppendCustom(ctx context.Context, customType string, data any) error {
	_, err := h.applyOrQueueSessionWrite(ctx, pendingCustomWrite{CustomType: customType, Data: data})
	return err
}

func (h *AgentHarness) AppendCustomMessage(ctx context.Context, customType string, content any, display bool, details any) error {
	_, err := h.applyOrQueueSessionWrite(ctx, pendingCustomMessageWrite{CustomType: customType, Content: content, Display: display, Details: details})
	return err
}

func (h *AgentHarness) AppendLabel(ctx context.Context, targetID string, label string) error {
	_, err := h.applyOrQueueSessionWrite(ctx, pendingLabelWrite{TargetID: targetID, Label: label})
	return err
}

func (h *AgentHarness) SetSessionName(ctx context.Context, name string) error {
	_, err := h.applyOrQueueSessionWrite(ctx, pendingSessionInfoWrite{Name: name})
	return err
}

func (h *AgentHarness) SetLeaf(ctx context.Context, targetID *string) error {
	_, err := h.applyOrQueueSessionWrite(ctx, pendingLeafWrite{TargetID: cloneStringPtr(targetID)})
	return err
}

func (h *AgentHarness) Steer(ctx context.Context, text string, opts PromptOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	idle := h.phase == PhaseIdle
	h.mu.Unlock()
	if idle {
		return &agent.AgentError{Code: agent.AgentErrInvalidState, Msg: "cannot steer while idle"}
	}
	msg := ai.NewUserMessage(text, opts.Images)
	h.steerQueue.Enqueue(msg)
	return h.emitHarness(ctx, h.queueUpdateEvent())
}

func (h *AgentHarness) FollowUp(ctx context.Context, text string, opts PromptOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	idle := h.phase == PhaseIdle
	h.mu.Unlock()
	if idle {
		return &agent.AgentError{Code: agent.AgentErrInvalidState, Msg: "cannot follow up while idle"}
	}
	msg := ai.NewUserMessage(text, opts.Images)
	h.followUpQueue.Enqueue(msg)
	return h.emitHarness(ctx, h.queueUpdateEvent())
}

func (h *AgentHarness) NextTurn(ctx context.Context, text string, opts PromptOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.nextTurnQueue.Enqueue(ai.NewUserMessage(text, opts.Images))
	return h.emitHarness(ctx, h.queueUpdateEvent())
}

func (h *AgentHarness) GetModel() ai.Model {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.model
}

func (h *AgentHarness) SetModel(ctx context.Context, model ai.Model) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	previous := h.model
	h.model = model
	h.mu.Unlock()
	if previous.Provider == model.Provider && previous.ID == model.ID {
		return nil
	}
	if err := h.emitModelSelect(ctx, ModelSelectEvent{Model: model, PreviousModel: previous, Source: ModelSelectSourceSet}); err != nil {
		return err
	}
	_, err := h.applyOrQueueSessionWrite(ctx, pendingModelChangeWrite{Provider: model.Provider, ModelID: model.ID})
	return err
}

func (h *AgentHarness) GetThinkingLevel() ai.ThinkingLevel {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.thinkingLevel
}

func (h *AgentHarness) SetThinkingLevel(ctx context.Context, level ai.ThinkingLevel) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	previous := h.thinkingLevel
	h.thinkingLevel = level
	h.mu.Unlock()
	if previous == level {
		return nil
	}
	if err := h.emitThinkingLevelSelect(ctx, ThinkingLevelSelectEvent{Level: level, PreviousLevel: previous}); err != nil {
		return err
	}
	_, err := h.applyOrQueueSessionWrite(ctx, pendingThinkingLevelChangeWrite{ThinkingLevel: string(level)})
	return err
}

func (h *AgentHarness) GetResources() Resources {
	h.mu.Lock()
	defer h.mu.Unlock()
	return cloneResources(h.resources)
}

func (h *AgentHarness) SetResources(ctx context.Context, resources Resources) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	previous := cloneResources(h.resources)
	h.resources = cloneResources(resources)
	current := cloneResources(h.resources)
	h.mu.Unlock()
	return h.emitResourcesUpdate(ctx, ResourcesUpdateEvent{Resources: current, PreviousResources: previous})
}

func (h *AgentHarness) GetStreamOptions() StreamOptions {
	h.mu.Lock()
	defer h.mu.Unlock()
	return cloneStreamOptions(h.streamOptions)
}

func (h *AgentHarness) SetStreamOptions(opts StreamOptions) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.streamOptions = cloneStreamOptions(opts)
}

func (h *AgentHarness) GetTools() []agent.AgentTool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.toolsByOrderLocked()
}

func (h *AgentHarness) GetActiveTools() []agent.AgentTool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.activeToolsLocked()
}

func (h *AgentHarness) SetTools(ctx context.Context, tools []agent.AgentTool, activeToolNames []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	nextTools, nextOrder, err := buildToolMap(tools)
	if err != nil {
		return err
	}
	nextActive := append([]string(nil), activeToolNames...)
	if activeToolNames == nil {
		h.mu.Lock()
		nextActive = append([]string(nil), h.activeToolNames...)
		if !h.activeToolNamesSet {
			nextActive = append([]string(nil), nextOrder...)
		}
		h.mu.Unlock()
	}
	if err := validateActiveToolNames(nextActive, nextTools); err != nil {
		return err
	}
	h.mu.Lock()
	previousToolNames := append([]string(nil), h.toolOrder...)
	previousActiveToolNames := h.activeToolNamesSnapshotLocked()
	h.tools = nextTools
	h.toolOrder = nextOrder
	h.activeToolNames = append([]string(nil), nextActive...)
	h.activeToolNamesSet = true
	h.mu.Unlock()
	if _, err := h.applyOrQueueSessionWrite(ctx, pendingActiveToolsChangeWrite{ActiveToolNames: nextActive}); err != nil {
		return err
	}
	return h.emitToolsUpdate(ctx, ToolsUpdateEvent{
		ToolNames:               nextOrder,
		PreviousToolNames:       previousToolNames,
		ActiveToolNames:         nextActive,
		PreviousActiveToolNames: previousActiveToolNames,
		Source:                  "set",
	})
}

func (h *AgentHarness) SetActiveTools(ctx context.Context, names []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	if err := validateActiveToolNames(names, h.tools); err != nil {
		h.mu.Unlock()
		return err
	}
	previousToolNames := append([]string(nil), h.toolOrder...)
	previousActiveToolNames := h.activeToolNamesSnapshotLocked()
	nextActive := append([]string(nil), names...)
	h.activeToolNames = nextActive
	h.activeToolNamesSet = true
	toolNames := append([]string(nil), h.toolOrder...)
	h.mu.Unlock()
	if _, err := h.applyOrQueueSessionWrite(ctx, pendingActiveToolsChangeWrite{ActiveToolNames: nextActive}); err != nil {
		return err
	}
	return h.emitToolsUpdate(ctx, ToolsUpdateEvent{
		ToolNames:               toolNames,
		PreviousToolNames:       previousToolNames,
		ActiveToolNames:         nextActive,
		PreviousActiveToolNames: previousActiveToolNames,
		Source:                  "set",
	})
}

func (h *AgentHarness) GetSteeringMode() agent.QueueMode {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.steeringMode
}

func (h *AgentHarness) SetSteeringMode(mode agent.QueueMode) {
	if mode == "" {
		mode = agent.QueueOneAtATime
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steeringMode = mode
}

func (h *AgentHarness) GetFollowUpMode() agent.QueueMode {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.followUpMode
}

func (h *AgentHarness) SetFollowUpMode(mode agent.QueueMode) {
	if mode == "" {
		mode = agent.QueueOneAtATime
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.followUpMode = mode
}

func (h *AgentHarness) Abort(ctx context.Context) (AbortResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	cancel := h.runCancel
	h.mu.Unlock()
	clearedSteer := h.steerQueue.Clear()
	clearedFollowUp := h.followUpQueue.Clear()
	if cancel == nil && len(clearedSteer) == 0 && len(clearedFollowUp) == 0 {
		return AbortResult{}, nil
	}
	if cancel != nil {
		cancel()
	}
	var errs []error
	if err := h.emitHarness(ctx, h.queueUpdateEvent()); err != nil {
		errs = append(errs, err)
	}
	if err := h.WaitForIdle(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := h.emitHarness(ctx, AbortEvent{ClearedSteer: clearedSteer, ClearedFollowUp: clearedFollowUp}); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return AbortResult{Aborted: true}, &agent.AgentError{
			Code: agent.AgentErrHook,
			Msg:  "abort completed with errors",
			Err:  errors.Join(errs...),
		}
	}
	return AbortResult{Aborted: true}, nil
}

func (h *AgentHarness) WaitForIdle(ctx context.Context) error {
	h.mu.Lock()
	done := h.runDone
	h.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type turnState struct {
	messages      []agent.AgentMessage
	resources     Resources
	streamOptions StreamOptions
	sessionID     string
	systemPrompt  string
	model         ai.Model
	thinkingLevel ai.ThinkingLevel
	tools         []agent.AgentTool
}

func (h *AgentHarness) beginRun(ctx context.Context, phase Phase) (func(), error) {
	h.mu.Lock()
	if h.phase != PhaseIdle {
		h.mu.Unlock()
		return nil, &agent.AgentError{Code: agent.AgentErrBusy, Msg: "harness is busy"}
	}
	done := make(chan struct{})
	h.phase = phase
	h.runDone = done
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		h.phase = PhaseIdle
		h.runCancel = nil
		if h.runDone == done {
			h.runDone = nil
		}
		h.mu.Unlock()
		close(done)
	}, nil
}

func (h *AgentHarness) createTurnState(ctx context.Context) (turnState, error) {
	h.mu.Lock()
	model := h.model
	thinking := h.thinkingLevel
	resources := cloneResources(h.resources)
	streamOptions := cloneStreamOptions(h.streamOptions)
	h.mu.Unlock()
	metadata, err := h.sess.Metadata(ctx)
	if err != nil {
		return turnState{}, err
	}
	built, err := h.sess.BuildContext(ctx)
	if err != nil {
		return turnState{}, err
	}
	var tools []agent.AgentTool
	h.mu.Lock()
	if built.ActiveToolNames != nil {
		h.activeToolNames = append([]string(nil), (*built.ActiveToolNames)...)
		h.activeToolNamesSet = true
	}
	if built.ThinkingLevel != "" {
		thinking = ai.ThinkingLevel(built.ThinkingLevel)
		h.thinkingLevel = thinking
	}
	if built.Model != nil {
		model.Provider = built.Model.Provider
		model.ID = built.Model.ModelID
		h.model = model
	}
	tools = h.activeToolsLocked()
	h.mu.Unlock()
	if thinking == "" {
		thinking = ai.ThinkingOff
	}
	systemPrompt, err := h.systemPrompt.Build(ctx, SystemPromptContext{
		Env:           h.env,
		Session:       h.sess,
		Model:         model,
		ThinkingLevel: thinking,
		ActiveTools:   tools,
		Resources:     resources,
	})
	if err != nil {
		return turnState{}, err
	}
	return turnState{
		messages:      built.Messages,
		resources:     resources,
		streamOptions: streamOptions,
		sessionID:     metadata.ID,
		systemPrompt:  systemPrompt,
		model:         model,
		thinkingLevel: thinking,
		tools:         tools,
	}, nil
}

func (h *AgentHarness) loopConfig(getState func() turnState, setState func(turnState)) agent.AgentLoopConfig {
	state := getState()
	reasoning := state.thinkingLevel
	if reasoning == ai.ThinkingOff {
		reasoning = ""
	}
	return agent.AgentLoopConfig{
		Model:            state.model,
		Reasoning:        reasoning,
		SessionID:        state.sessionID,
		ConvertToLLM:     ConvertToLLM,
		TransformContext: h.transformContext,
		BeforeToolCall:   h.beforeToolCall,
		AfterToolCall:    h.afterToolCall,
		PrepareNextTurn: func(ctx context.Context, _ agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
			current := getState()
			if err := h.flushPendingSessionWrites(ctx); err != nil {
				return nil, err
			}
			next, err := h.createTurnState(ctx)
			if err != nil {
				return nil, err
			}
			setState(next)
			if sameLoopTurnState(current, next) {
				return nil, nil
			}
			thinking := next.thinkingLevel
			model := next.model
			return &agent.AgentLoopTurnUpdate{
				Context: &agent.AgentContext{
					SystemPrompt: next.systemPrompt,
					Messages:     next.messages,
					Tools:        next.tools,
				},
				Model:         &model,
				ThinkingLevel: &thinking,
			}, nil
		},
		GetSteeringMessages: func(ctx context.Context) ([]agent.AgentMessage, error) {
			return h.drainQueuedMessages(ctx, h.steerQueue, h.steeringMode)
		},
		GetFollowUpMessages: func(ctx context.Context) ([]agent.AgentMessage, error) {
			return h.drainQueuedMessages(ctx, h.followUpQueue, h.followUpMode)
		},
		ToolExecution: agent.ToolExecutionParallel,
	}
}

func sameLoopTurnState(a, b turnState) bool {
	return a.systemPrompt == b.systemPrompt &&
		a.thinkingLevel == b.thinkingLevel &&
		reflect.DeepEqual(a.model, b.model) &&
		sameAgentMessages(a.messages, b.messages) &&
		sameAgentTools(a.tools, b.tools)
}

func sameAgentMessages(a, b []agent.AgentMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameAgentTools(a, b []agent.AgentTool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		var aName, bName string
		if a[i] != nil {
			aName = a[i].Name()
		}
		if b[i] != nil {
			bName = b[i].Name()
		}
		if aName != bName {
			return false
		}
	}
	return true
}

func (h *AgentHarness) drainQueuedMessages(ctx context.Context, queue *messagequeue.Queue, mode agent.QueueMode) ([]agent.AgentMessage, error) {
	messages := queue.DrainMode(messagequeue.Mode(mode))
	if len(messages) == 0 {
		return nil, nil
	}
	if err := h.emitHarness(ctx, h.queueUpdateEvent()); err != nil {
		queue.Prepend(messages)
		return nil, err
	}
	return messages, nil
}

func (h *AgentHarness) streamForTurn(getState func() turnState) agent.StreamFn {
	base := h.streamFn
	if base == nil {
		base = agent.DefaultStreamFn(h.registry)
	}
	return func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
		state := getState()
		opts.Transport = firstString(opts.Transport, state.streamOptions.Transport)
		opts.TimeoutMs = firstInt(opts.TimeoutMs, state.streamOptions.TimeoutMs)
		opts.MaxRetries = firstInt(opts.MaxRetries, state.streamOptions.MaxRetries)
		opts.MaxRetryDelayMs = firstInt(opts.MaxRetryDelayMs, state.streamOptions.MaxRetryDelayMs)
		opts.CacheRetention = firstString(opts.CacheRetention, state.streamOptions.CacheRetention)
		opts.Headers = mergeStringMaps(state.streamOptions.Headers, opts.Headers)
		opts.Metadata = mergeAnyMaps(state.streamOptions.Metadata, opts.Metadata)
		if h.getAuth != nil {
			auth, err := h.getAuth(ctx, model)
			if err != nil {
				return streamError(model, err)
			}
			if auth.APIKey != "" {
				opts.APIKey = auth.APIKey
			}
			opts.Headers = mergeStringMaps(auth.Headers, opts.Headers)
		}
		requestOptions, err := h.emitBeforeProviderRequest(ctx, BeforeProviderRequestEvent{
			Model:         model,
			SessionID:     opts.SessionID,
			StreamOptions: streamOptionsFromAI(opts),
		})
		if err != nil {
			return streamError(model, err)
		}
		applyStreamOptionsToAI(&opts, requestOptions)
		opts = h.wrapProviderHooks(ctx, opts)
		return base(ctx, model, aiCtx, opts)
	}
}

func (h *AgentHarness) appendMessageImmediate(ctx context.Context, msg agent.AgentMessage) error {
	_, err := h.sess.AppendMessage(ctx, msg)
	return err
}

func (h *AgentHarness) handleAgentEvent(ctx context.Context, ev agent.AgentEvent) error {
	if end, ok := ev.(agent.MessageEndEvent); ok {
		if err := h.appendMessageImmediate(ctx, end.Message); err != nil {
			return err
		}
		return h.emit(ctx, ev)
	}
	switch ev.(type) {
	case agent.TurnEndEvent:
		eventErr := h.emit(ctx, ev)
		hadPending := h.pendingSessionWriteCount() > 0
		flushErr := h.flushPendingSessionWrites(ctx)
		if eventErr != nil {
			return eventErr
		}
		if flushErr != nil {
			return flushErr
		}
		return h.emitHarness(ctx, SavePointEvent{HadPendingMutations: hadPending})
	case agent.AgentEndEvent:
		if err := h.flushPendingSessionWrites(ctx); err != nil {
			return err
		}
		h.mu.Lock()
		h.phase = PhaseIdle
		h.mu.Unlock()
		if err := h.emit(ctx, ev); err != nil {
			return err
		}
		return h.emitHarness(ctx, SettledEvent{NextTurnCount: h.nextTurnQueue.Len()})
	default:
		return h.emit(ctx, ev)
	}
}

func (h *AgentHarness) applyOrQueueSessionWrite(ctx context.Context, write pendingSessionWrite) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	if h.phase != PhaseIdle {
		h.pendingWrites = append(h.pendingWrites, write)
		h.mu.Unlock()
		return "", nil
	}
	h.mu.Unlock()
	return write.apply(ctx, h.sess)
}

func (h *AgentHarness) flushPendingSessionWrites(ctx context.Context) error {
	h.mu.Lock()
	writes := append([]pendingSessionWrite(nil), h.pendingWrites...)
	h.pendingWrites = nil
	h.mu.Unlock()
	for _, write := range writes {
		if write == nil {
			continue
		}
		if _, err := write.apply(ctx, h.sess); err != nil {
			return err
		}
	}
	return nil
}

func (h *AgentHarness) pendingSessionWriteCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pendingWrites)
}

func (h *AgentHarness) emit(ctx context.Context, ev agent.AgentEvent) error {
	h.mu.Lock()
	ids := make([]uint64, 0, len(h.listeners))
	for id := range h.listeners {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	listeners := make([]func(context.Context, agent.AgentEvent) error, 0, len(ids))
	for _, id := range ids {
		listeners = append(listeners, h.listeners[id])
	}
	h.dispatching = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.dispatching = false
		h.mu.Unlock()
	}()
	// Agent event listeners are invoked after harness-side reduction and
	// persistence work. They must not call state-mutating harness methods;
	// those methods panic while dispatching to expose re-entrant races.
	for _, listener := range listeners {
		if listener == nil {
			continue
		}
		if err := listener(ctx, ev); err != nil {
			return err
		}
	}
	return nil
}

func (h *AgentHarness) activeToolsLocked() []agent.AgentTool {
	if !h.activeToolNamesSet {
		return h.toolsByOrderLocked()
	}
	out := make([]agent.AgentTool, 0, len(h.activeToolNames))
	for _, name := range h.activeToolNames {
		if tool := h.tools[name]; tool != nil {
			out = append(out, tool)
		}
	}
	return out
}

func (h *AgentHarness) toolsByOrderLocked() []agent.AgentTool {
	out := make([]agent.AgentTool, 0, len(h.toolOrder))
	for _, name := range h.toolOrder {
		if tool := h.tools[name]; tool != nil {
			out = append(out, tool)
		}
	}
	return out
}

func (h *AgentHarness) activeToolNamesSnapshotLocked() []string {
	if h.activeToolNamesSet {
		return append([]string(nil), h.activeToolNames...)
	}
	return append([]string(nil), h.toolOrder...)
}
