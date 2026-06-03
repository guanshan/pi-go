package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/guanshan/pi-go/packages/agent/messagequeue"
	"github.com/guanshan/pi-go/packages/ai"
)

// Agent is the stateful convenience wrapper around RunAgentLoop.
//
// The layering mirrors the TypeScript implementation: RunAgentLoop is the
// low-level loop kernel, Agent owns an in-memory transcript plus steering and
// follow-up queues, and harness.AgentHarness builds long-running persisted
// sessions by calling RunAgentLoop directly instead of nesting another Agent.
//
// # Event listeners and re-entrancy
//
// This is a deliberate divergence from the single-threaded TypeScript Agent,
// where a listener may freely call any method. Here events are dispatched after
// state has been reduced and after a.mu has been released, so listeners run
// without holding the lock. To keep state reduction and explicit mutation from
// interleaving in confusing ways, the configuration-mutating methods
// (SetSystemPrompt, SetModel, SetThinkingLevel, SetTools, SetMessages,
// SetState, AppendMessage) panic if called while an event is being dispatched.
//
// What is safe to call from inside a listener:
//   - State (returns a deep copy) and HasQueuedMessages — read-only.
//   - Steer, FollowUp, ClearSteeringQueue, ClearFollowUpQueue, ClearAllQueues —
//     they operate on independently locked queues, not on a.mu.
//   - Abort — cancels the active run's context.
//
// What is not safe from inside a listener:
//   - The Set*/AppendMessage mutators above (they panic by design).
//   - WaitForIdle (it would block until the run finishes, but the run cannot
//     finish until the listener returns — a self-deadlock).
//
// Returning an error from a listener stops dispatch of the current event to
// later listeners and ends the run; subscribers still receive a terminal
// agent_end via the failure path (see drive/handleRunFailure).
type Agent struct {
	mu    sync.Mutex
	state AgentState

	listeners      map[uint64]func(context.Context, AgentEvent) error
	nextListenerID uint64
	dispatching    bool

	steerQueue    *messagequeue.Queue
	followUpQueue *messagequeue.Queue

	registry *ai.ModelRegistry
	streamFn StreamFn

	convertToLLM     ConvertToLLMFunc
	transformContext TransformContextFunc
	getAPIKey        GetAPIKeyFunc
	beforeToolCall   BeforeToolCallFunc
	afterToolCall    AfterToolCallFunc

	shouldStopAfterTurn ShouldStopAfterTurnFunc
	prepareNextTurn     PrepareNextTurnFunc

	toolExecution   ToolExecutionMode
	sessionID       string
	thinkingBudgets *ai.ThinkingBudgets
	transport       string
	timeoutMs       int
	idleTimeoutMs   int
	maxRetries      int
	maxRetryDelayMs int
	onPayload       func(payload any, model ai.Model) (any, error)
	onResponse      func(resp ai.ProviderResponse, model ai.Model) error

	activeRun *activeRun
}

type activeRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewAgent(opts AgentOptions) *Agent {
	state := cloneState(opts.InitialState)
	if state.ThinkingLevel == "" {
		state.ThinkingLevel = ai.ThinkingOff
	}
	steering := opts.SteeringMode
	if steering == "" {
		steering = QueueOneAtATime
	}
	followUp := opts.FollowUpMode
	if followUp == "" {
		followUp = QueueOneAtATime
	}
	execution := opts.ToolExecution
	if execution == "" {
		execution = ToolExecutionParallel
	}
	convert := opts.ConvertToLLM
	if convert == nil {
		convert = defaultConvertToLLM
	}
	streamFn := opts.StreamFn
	if streamFn == nil {
		streamFn = DefaultStreamFn(opts.Registry)
	}
	return &Agent{
		state:               state,
		listeners:           map[uint64]func(context.Context, AgentEvent) error{},
		registry:            opts.Registry,
		streamFn:            streamFn,
		convertToLLM:        convert,
		transformContext:    opts.TransformContext,
		getAPIKey:           opts.GetAPIKey,
		beforeToolCall:      opts.BeforeToolCall,
		afterToolCall:       opts.AfterToolCall,
		shouldStopAfterTurn: opts.ShouldStopAfterTurn,
		prepareNextTurn:     opts.PrepareNextTurn,
		steerQueue:          messagequeue.New(messagequeue.Mode(steering)),
		followUpQueue:       messagequeue.New(messagequeue.Mode(followUp)),
		toolExecution:       execution,
		sessionID:           opts.SessionID,
		thinkingBudgets:     opts.ThinkingBudgets,
		transport:           opts.Transport,
		timeoutMs:           opts.TimeoutMs,
		idleTimeoutMs:       opts.IdleTimeoutMs,
		maxRetries:          opts.MaxRetries,
		maxRetryDelayMs:     opts.MaxRetryDelayMs,
		onPayload:           opts.OnPayload,
		onResponse:          opts.OnResponse,
	}
}

func New(opts AgentOptions) *Agent {
	return NewAgent(opts)
}

func (a *Agent) State() AgentState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return cloneState(a.state)
}

func (a *Agent) SetSystemPrompt(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.panicIfDispatchingLocked("SetSystemPrompt")
	a.state.SystemPrompt = s
}

func (a *Agent) SetModel(m ai.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.panicIfDispatchingLocked("SetModel")
	a.state.Model = m
}

func (a *Agent) SetThinkingLevel(l ai.ThinkingLevel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.panicIfDispatchingLocked("SetThinkingLevel")
	a.state.ThinkingLevel = l
}

func (a *Agent) SetTools(tools []AgentTool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.panicIfDispatchingLocked("SetTools")
	a.state.Tools = append([]AgentTool(nil), tools...)
}

func (a *Agent) SetMessages(msgs []AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.panicIfDispatchingLocked("SetMessages")
	a.state.Messages = append([]AgentMessage(nil), msgs...)
}

func (a *Agent) SetState(state AgentState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.panicIfDispatchingLocked("SetState")
	a.state = cloneState(state)
}

func (a *Agent) AppendMessage(msg AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.panicIfDispatchingLocked("AppendMessage")
	a.state.Messages = append(a.state.Messages, msg)
}

func (a *Agent) SetToolExecution(mode ToolExecutionMode) {
	if mode == "" {
		mode = ToolExecutionParallel
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolExecution = mode
}

func (a *Agent) SetSteeringMode(mode QueueMode) {
	a.steerQueue.SetMode(messagequeue.Mode(mode))
}

func (a *Agent) SetFollowUpMode(mode QueueMode) {
	a.followUpQueue.SetMode(messagequeue.Mode(mode))
}

// Subscribe registers an event listener and returns an unsubscribe function.
// Listeners are invoked in subscription order, after state reduction, with
// a.mu released. See the Agent type doc for which methods are safe to call from
// within a listener — the configuration mutators panic if called during
// dispatch.
func (a *Agent) Subscribe(f func(context.Context, AgentEvent) error) func() {
	if f == nil {
		return func() {}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.nextListenerID
	a.nextListenerID++
	if a.listeners == nil {
		a.listeners = map[uint64]func(context.Context, AgentEvent) error{}
	}
	a.listeners[id] = f
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.listeners, id)
	}
}

func (a *Agent) PromptText(ctx context.Context, text string, images []ai.ContentBlock) error {
	return a.PromptMessages(ctx, []AgentMessage{ai.NewUserMessage(text, images)})
}

func (a *Agent) PromptMessage(ctx context.Context, msg AgentMessage) error {
	return a.PromptMessages(ctx, []AgentMessage{msg})
}

func (a *Agent) PromptMessages(ctx context.Context, msgs []AgentMessage) error {
	return a.runPromptMessages(ctx, msgs, false)
}

func (a *Agent) Prompt(ctx context.Context, msg AgentMessage) ([]AgentMessage, error) {
	return AwaitRun(ctx, a, func(agent *Agent) error {
		return agent.PromptMessage(ctx, msg)
	})
}

func (a *Agent) Continue(ctx context.Context) error {
	state := a.State()
	if len(state.Messages) == 0 {
		return agentError(AgentErrInvalidState, "no messages to continue from", nil)
	}
	if _, ok := ai.AsAssistantMessage(state.Messages[len(state.Messages)-1]); ok {
		if queued := a.steerQueue.Drain(); len(queued) > 0 {
			return a.runPromptMessages(ctx, queued, true)
		}
		if queued := a.followUpQueue.Drain(); len(queued) > 0 {
			return a.runPromptMessages(ctx, queued, false)
		}
		return agentError(AgentErrBusy, "no queued messages to continue after assistant response", nil)
	}

	return a.runContinuation(ctx)
}

func (a *Agent) ContinueMessages(ctx context.Context) ([]AgentMessage, error) {
	return AwaitRun(ctx, a, func(agent *Agent) error {
		return agent.Continue(ctx)
	})
}

func (a *Agent) Steer(msg AgentMessage) {
	a.steerQueue.Enqueue(msg)
}

func (a *Agent) FollowUp(msg AgentMessage) {
	a.followUpQueue.Enqueue(msg)
}

func (a *Agent) ClearSteeringQueue() {
	a.steerQueue.Clear()
}

func (a *Agent) ClearFollowUpQueue() {
	a.followUpQueue.Clear()
}

func (a *Agent) ClearAllQueues() {
	a.ClearSteeringQueue()
	a.ClearFollowUpQueue()
}

func (a *Agent) HasQueuedMessages() bool {
	return a.steerQueue.HasItems() || a.followUpQueue.HasItems()
}

func (a *Agent) Abort() {
	a.mu.Lock()
	active := a.activeRun
	a.mu.Unlock()
	if active != nil {
		active.cancel()
	}
}

func (a *Agent) WaitForIdle(ctx context.Context) error {
	a.mu.Lock()
	active := a.activeRun
	a.mu.Unlock()
	if active == nil {
		return nil
	}
	select {
	case <-active.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Agent) Reset() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeRun != nil {
		return agentError(AgentErrBusy, "agent is already processing", nil)
	}
	a.state.Messages = nil
	a.state.IsStreaming = false
	a.state.StreamingMessage = nil
	a.state.PendingToolCalls = nil
	a.state.ErrorMessage = ""
	a.steerQueue.Clear()
	a.followUpQueue.Clear()
	return nil
}

func AwaitRun(ctx context.Context, a *Agent, runFn func(*Agent) error) ([]AgentMessage, error) {
	var messages []AgentMessage
	unsubscribe := a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
		if end, ok := ev.(AgentEndEvent); ok {
			messages = append([]AgentMessage(nil), end.Messages...)
		}
		return nil
	})
	defer unsubscribe()
	err := runFn(a)
	if waitErr := a.WaitForIdle(ctx); waitErr != nil && err == nil {
		err = waitErr
	}
	return messages, err
}

func (a *Agent) runPromptMessages(ctx context.Context, msgs []AgentMessage, skipInitialSteeringPoll bool) error {
	runCtx, finish, err := a.beginRun(ctx)
	if err != nil {
		return err
	}
	return a.drive(runCtx, finish, "agent run failed", a.promptLoop(msgs, skipInitialSteeringPoll))
}

func (a *Agent) runContinuation(ctx context.Context) error {
	runCtx, finish, err := a.beginRun(ctx)
	if err != nil {
		return err
	}
	return a.drive(runCtx, finish, "agent continuation failed", a.continueLoop())
}

// StartPrompt begins a prompt run without blocking the caller. Unlike
// PromptMessages, it returns as soon as the run has been registered (so Steer,
// FollowUp, Abort and WaitForIdle all act on the in-flight run) and the loop
// proceeds on a background goroutine. Use WaitForIdle or subscribe to agent_end
// to observe completion. Returns AgentErrBusy if a run is already active.
func (a *Agent) StartPrompt(ctx context.Context, msgs []AgentMessage) error {
	return a.startPromptMessages(ctx, msgs, false)
}

// StartPromptText is the text convenience form of StartPrompt.
func (a *Agent) StartPromptText(ctx context.Context, text string, images []ai.ContentBlock) error {
	return a.startPromptMessages(ctx, []AgentMessage{ai.NewUserMessage(text, images)}, false)
}

// StartContinue is the non-blocking analog of Continue. It applies the same
// preconditions and queue-draining rules and then drives the run on a background
// goroutine.
func (a *Agent) StartContinue(ctx context.Context) error {
	state := a.State()
	if len(state.Messages) == 0 {
		return agentError(AgentErrInvalidState, "no messages to continue from", nil)
	}
	if _, ok := ai.AsAssistantMessage(state.Messages[len(state.Messages)-1]); ok {
		if queued := a.steerQueue.Drain(); len(queued) > 0 {
			return a.startPromptMessages(ctx, queued, true)
		}
		if queued := a.followUpQueue.Drain(); len(queued) > 0 {
			return a.startPromptMessages(ctx, queued, false)
		}
		return agentError(AgentErrBusy, "no queued messages to continue after assistant response", nil)
	}
	return a.startContinuation(ctx)
}

func (a *Agent) startPromptMessages(ctx context.Context, msgs []AgentMessage, skipInitialSteeringPoll bool) error {
	runCtx, finish, err := a.beginRun(ctx)
	if err != nil {
		return err
	}
	// The loop body (config + context snapshot) is captured synchronously, before
	// the goroutine starts, so callers mutating agent state right after this
	// returns cannot race the run's view of it.
	body := a.promptLoop(msgs, skipInitialSteeringPoll)
	go func() { _ = a.drive(runCtx, finish, "agent run failed", body) }()
	return nil
}

func (a *Agent) startContinuation(ctx context.Context) error {
	runCtx, finish, err := a.beginRun(ctx)
	if err != nil {
		return err
	}
	body := a.continueLoop()
	go func() { _ = a.drive(runCtx, finish, "agent continuation failed", body) }()
	return nil
}

func (a *Agent) promptLoop(msgs []AgentMessage, skipInitialSteeringPoll bool) func(context.Context) error {
	cfg := a.loopConfig(skipInitialSteeringPoll)
	snapshot := a.contextSnapshot()
	return func(runCtx context.Context) error {
		_, err := RunAgentLoop(runCtx, msgs, snapshot, cfg, a.emit, a.streamFn)
		return err
	}
}

func (a *Agent) continueLoop() func(context.Context) error {
	cfg := a.loopConfig(false)
	snapshot := a.contextSnapshot()
	return func(runCtx context.Context) error {
		_, err := RunAgentLoopContinue(runCtx, snapshot, cfg, a.emit, a.streamFn)
		return err
	}
}

// drive runs body synchronously and guarantees that the run always settles with
// a terminal signal. On a panic, or when body returns a non-nil error, it calls
// handleRunFailure so subscribers still observe message_start/message_end/
// turn_end/agent_end, mirroring TS runWithLifecycle. finish() always runs.
//
// RunAgentLoop only returns a non-nil error when the event sink itself failed
// mid-stream (so the loop could not deliver its own emitLoopFailure sequence);
// re-emitting via handleRunFailure is the last-resort delivery of that terminal
// signal. When the loop delivered its terminal events itself it returns nil and
// handleRunFailure is not invoked, so there is no duplicate agent_end.
func (a *Agent) drive(runCtx context.Context, finish func(), wrapMsg string, body func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = agentError(AgentErrUnknown, "agent panic", fmt.Errorf("%v", recovered))
			_ = a.handleRunFailure(runCtx, err)
		}
		finish()
	}()
	if runErr := body(runCtx); runErr != nil {
		_ = a.handleRunFailure(runCtx, runErr)
		err = agentError(AgentErrUnknown, wrapMsg, runErr)
	}
	return err
}

func (a *Agent) beginRun(ctx context.Context) (context.Context, func(), error) {
	a.mu.Lock()
	if a.activeRun != nil {
		a.mu.Unlock()
		return nil, nil, agentError(AgentErrBusy, "agent is already processing", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	active := &activeRun{cancel: cancel, done: make(chan struct{})}
	a.activeRun = active
	a.state.IsStreaming = true
	a.state.StreamingMessage = nil
	a.state.ErrorMessage = ""
	a.state.PendingToolCalls = nil
	a.mu.Unlock()
	finish := func() {
		a.mu.Lock()
		if a.activeRun == active {
			a.state.IsStreaming = false
			a.state.StreamingMessage = nil
			a.state.PendingToolCalls = nil
			a.activeRun = nil
		}
		a.mu.Unlock()
		cancel()
		close(active.done)
	}
	return runCtx, finish, nil
}

func (a *Agent) loopConfig(skipInitialSteeringPoll bool) AgentLoopConfig {
	a.mu.Lock()
	reasoning := a.state.ThinkingLevel
	if reasoning == ai.ThinkingOff {
		reasoning = ""
	}
	cfg := AgentLoopConfig{
		Model:               a.state.Model,
		Reasoning:           reasoning,
		SessionID:           a.sessionID,
		Transport:           a.transport,
		ThinkingBudgets:     a.thinkingBudgets,
		TimeoutMs:           a.timeoutMs,
		IdleTimeoutMs:       a.idleTimeoutMs,
		MaxRetries:          a.maxRetries,
		MaxRetryDelayMs:     a.maxRetryDelayMs,
		ConvertToLLM:        a.convertToLLM,
		TransformContext:    a.transformContext,
		GetAPIKey:           a.getAPIKey,
		BeforeToolCall:      a.beforeToolCall,
		AfterToolCall:       a.afterToolCall,
		ShouldStopAfterTurn: a.shouldStopAfterTurn,
		PrepareNextTurn:     a.prepareNextTurn,
		ToolExecution:       a.toolExecution,
		OnPayload:           a.onPayload,
		OnResponse:          a.onResponse,
	}
	a.mu.Unlock()
	cfg.GetSteeringMessages = func(context.Context) ([]AgentMessage, error) {
		if skipInitialSteeringPoll {
			skipInitialSteeringPoll = false
			return nil, nil
		}
		return a.steerQueue.Drain(), nil
	}
	cfg.GetFollowUpMessages = func(context.Context) ([]AgentMessage, error) {
		return a.followUpQueue.Drain(), nil
	}
	return cfg
}

func (a *Agent) contextSnapshot() AgentContext {
	state := a.State()
	return AgentContext{SystemPrompt: state.SystemPrompt, Messages: state.Messages, Tools: state.Tools}
}

func (a *Agent) emit(ctx context.Context, ev AgentEvent) error {
	a.mu.Lock()
	a.reduceStateLocked(ev)
	ids := make([]uint64, 0, len(a.listeners))
	for id := range a.listeners {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	listeners := make([]func(context.Context, AgentEvent) error, 0, len(ids))
	for _, id := range ids {
		listeners = append(listeners, a.listeners[id])
	}
	a.dispatching = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.dispatching = false
		a.mu.Unlock()
	}()
	// Listener callbacks run after state reduction and after releasing a.mu.
	// They must not call state-mutating Agent methods; those methods panic
	// while dispatching so re-entrant races fail loudly.
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

func (a *Agent) panicIfDispatchingLocked(method string) {
	if a.dispatching {
		panic(method + " cannot be called from an Agent event listener")
	}
}

func (a *Agent) handleRunFailure(ctx context.Context, err error) error {
	stopReason := "error"
	if ctx.Err() != nil {
		stopReason = "aborted"
	}
	msg := ai.NewAssistantMessageForModel(a.State().Model, nil, ai.Usage{}, stopReason)
	if err != nil {
		msg.ErrorMessage = err.Error()
	}
	if emitErr := a.emit(ctx, MessageStartEvent{Message: msg}); emitErr != nil {
		return emitErr
	}
	if emitErr := a.emit(ctx, MessageEndEvent{Message: msg}); emitErr != nil {
		return emitErr
	}
	if emitErr := a.emit(ctx, TurnEndEvent{Message: msg, ToolResults: nil}); emitErr != nil {
		return emitErr
	}
	return a.emit(ctx, AgentEndEvent{Messages: []AgentMessage{msg}})
}
