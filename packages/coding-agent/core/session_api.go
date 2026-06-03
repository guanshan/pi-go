package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	agentcore "github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

type SessionEvent interface{ sessionEventTag() }

type AgentEventWrapper struct {
	Event ai.Event
}

func (AgentEventWrapper) sessionEventTag() {}

type SessionEventListener func(event SessionEvent)

type CompactionReason string

const (
	CompactionManual    CompactionReason = "manual"
	CompactionThreshold CompactionReason = "threshold"
	CompactionOverflow  CompactionReason = "overflow"
)

type QueueUpdateEvent struct {
	Steering []string
	FollowUp []string
}

func (QueueUpdateEvent) sessionEventTag() {}

type CompactionStartEvent struct {
	Reason CompactionReason
}

func (CompactionStartEvent) sessionEventTag() {}

type CompactionEndEvent struct {
	Reason       CompactionReason
	Result       map[string]any
	Aborted      bool
	WillRetry    bool
	ErrorMessage string
}

func (CompactionEndEvent) sessionEventTag() {}

type SessionInfoChangedEvent struct {
	Name string
}

func (SessionInfoChangedEvent) sessionEventTag() {}

type ThinkingLevelChangedEvent struct {
	Level ai.ThinkingLevel
}

func (ThinkingLevelChangedEvent) sessionEventTag() {}

type ModelChangedEvent struct {
	Model         ai.Model
	ThinkingLevel ai.ThinkingLevel
}

func (ModelChangedEvent) sessionEventTag() {}

type AutoRetryStartEvent struct {
	Attempt      int
	MaxAttempts  int
	DelayMs      int
	ErrorMessage string
}

func (AutoRetryStartEvent) sessionEventTag() {}

type AutoRetryEndEvent struct {
	Success    bool
	Attempt    int
	FinalError string
}

func (AutoRetryEndEvent) sessionEventTag() {}

type CycleDirection string

const (
	CycleForward  CycleDirection = "forward"
	CycleBackward CycleDirection = "backward"
)

type ClearedQueue struct {
	Steering []string
	FollowUp []string
}

type PromptOptions struct {
	ExpandPromptTemplates bool
	Images                []ai.ContentBlock
	StreamingBehavior     StreamingBehavior
	Source                InputSource
	PreflightResult       func(success bool)
}

type StreamingBehavior string

const (
	StreamingSteer    StreamingBehavior = "steer"
	StreamingFollowUp StreamingBehavior = "followUp"
)

type InputSource string

const (
	InputInteractive InputSource = "interactive"
	InputRPC         InputSource = "rpc"
	InputExtension   InputSource = "extension"
)

type SendUserMessageOptions struct {
	Text              string
	Images            []ai.ContentBlock
	StreamingBehavior StreamingBehavior
	Source            InputSource
	SkipPreflight     bool
}

type BashResult struct {
	Command        string `json:"command"`
	Output         string `json:"output"`
	ExitCode       int    `json:"exitCode"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

type BashRunOptions struct {
	ExcludeFromContext bool
}

type NavigateTreeOptions struct {
	Summarize           bool
	CustomInstructions  string
	ReplaceInstructions bool
	Label               string
}

type BranchSummaryEntry struct {
	Type    string `json:"type"`
	FromID  string `json:"fromId"`
	Summary string `json:"summary"`
	Details any    `json:"details,omitempty"`
}

type NavigateTreeResult struct {
	NewLeafID    string
	OldLeafID    string
	SummaryEntry *BranchSummaryEntry
	Cancelled    bool
}

type ForkableUserMessage struct {
	EntryID string
	Text    string
}

type ContextUsage struct {
	UsedTokens    int       `json:"usedTokens"`
	ContextWindow int       `json:"contextWindow"`
	EstimatedAt   time.Time `json:"estimatedAt"`
}

type SessionStats struct {
	SessionFile       string        `json:"sessionFile"`
	SessionID         string        `json:"sessionId"`
	UserMessages      int           `json:"userMessages"`
	AssistantMessages int           `json:"assistantMessages"`
	ToolCalls         int           `json:"toolCalls"`
	ToolResults       int           `json:"toolResults"`
	TotalMessages     int           `json:"totalMessages"`
	Tokens            TokenStats    `json:"tokens"`
	Cost              float64       `json:"cost"`
	ContextUsage      *ContextUsage `json:"contextUsage,omitempty"`
}

type TokenStats struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Total      int `json:"total"`
}

type ParsedSkillBlock struct {
	Name        string
	Location    string
	Content     string
	UserMessage string
}

type ExtensionErrorListener func(error)

type ShutdownHandler func(context.Context) error

type ExtensionBindings struct {
	UIContext             any
	CommandContextActions any
	AbortHandler          func()
	ShutdownHandler       ShutdownHandler
	OnError               ExtensionErrorListener
}

var skillBlockPattern = regexp.MustCompile(`(?s)^<skill name="([^"]+)" location="([^"]+)">\n(.*?)\n</skill>(?:\n\n(.*))?$`)

func (a *AgentSession) promptWithRetry(ctx context.Context, text string, images []ai.ContentBlock, behavior StreamingBehavior, preflight func(bool), sink ai.EventSink) error {
	if ctx == nil {
		ctx = context.Background()
	}
	retriesUsed := 0
	overflowCompacted := false
	for {
		// Messages are persisted incrementally on message_end during the run.
		// Capture the branch leaf beforehand so a retried (failed) attempt can be
		// rolled off the branch, keeping the retry's rebuilt context clean and
		// avoiding a duplicated user message.
		preLeaf := a.currentLeaf()
		messages, maxLoopHit, queued, err := a.promptOnce(ctx, text, images, behavior, preflight, sink)
		// Streaming behavior and the preflight callback only apply to the first
		// attempt; retries reuse the already-claimed streaming slot.
		behavior = ""
		preflight = nil
		if err != nil {
			return err
		}
		if queued {
			// Message was steered/queued onto the in-flight agent; nothing to
			// persist or follow up on here.
			return nil
		}
		retryError := a.retryablePromptError(messages, maxLoopHit)
		if overflowError := a.contextOverflowPromptError(messages, maxLoopHit); overflowError != "" && !overflowCompacted {
			_ = a.Session.SetLeaf(preLeaf)
			overflowCompacted = true
			if _, err := a.compactInternal(ctx, CompactionOverflow, "", sink, true); err != nil {
				return err
			}
			continue
		}
		if retryError != "" && retriesUsed < a.maxAutoRetries() {
			// Discard the failed attempt's messages from the active branch (they
			// remain in the file as off-branch history) before retrying.
			_ = a.Session.SetLeaf(preLeaf)
			retriesUsed++
			delayMs := retryDelayMS(a.retryBaseDelayMS(), retriesUsed)
			retryCtx, cancel := context.WithCancel(ctx)
			a.mu.Lock()
			a.retryCancel = cancel
			a.mu.Unlock()
			a.emitSessionEvent(AutoRetryStartEvent{Attempt: retriesUsed, MaxAttempts: a.maxAutoRetries(), DelayMs: delayMs, ErrorMessage: retryError})
			err := sleepContext(retryCtx, time.Duration(delayMs)*time.Millisecond)
			a.mu.Lock()
			a.retryCancel = nil
			a.mu.Unlock()
			cancel()
			if err != nil {
				a.emitSessionEvent(AutoRetryEndEvent{Success: false, Attempt: retriesUsed, FinalError: err.Error()})
				return err
			}
			continue
		}
		// Messages were already persisted incrementally on message_end during
		// the run, so there is no batch append here.
		if retriesUsed > 0 {
			a.emitSessionEvent(AutoRetryEndEvent{Success: retryError == "", Attempt: retriesUsed, FinalError: retryError})
		}
		if maxLoopHit {
			return fmt.Errorf("agent stopped after %d tool iterations", DefaultAgentMaxLoop)
		}
		if retryError == "" {
			if err := a.maybeAutoCompact(ctx, sink); err != nil {
				return err
			}
		}
		for {
			next, ok := a.popFollowUp()
			if !ok {
				return nil
			}
			if err := a.promptWithRetry(ctx, next.Message, next.Images, "", nil, sink); err != nil {
				return err
			}
		}
	}
}

// promptOnce performs a single agent run. The streaming/queue decision and the
// preflight signal happen atomically under a.mu so that concurrent callers
// (e.g. RPC commands dispatched while a prompt is already in flight) cannot race
// past the streaming guard. When the agent is already streaming the message is
// steered or queued as a follow-up depending on behavior, and queued=true is
// returned so the caller skips the retry/persist loop.
func (a *AgentSession) promptOnce(ctx context.Context, text string, images []ai.ContentBlock, behavior StreamingBehavior, preflight func(bool), sink ai.EventSink) ([]agentcore.AgentMessage, bool, bool, error) {
	signalPreflight := func(success bool) {
		if preflight != nil {
			preflight(success)
		}
	}
	a.mu.Lock()
	if a.disposed {
		a.mu.Unlock()
		signalPreflight(false)
		return nil, false, false, errorsString("agent session is disposed")
	}
	if a.streaming {
		a.mu.Unlock()
		switch behavior {
		case StreamingFollowUp:
			a.QueueFollowUp(text, images)
			signalPreflight(true)
			return nil, false, true, nil
		case StreamingSteer:
			a.QueueSteer(text, images)
			signalPreflight(true)
			return nil, false, true, nil
		default:
			signalPreflight(false)
			return nil, false, false, errorsString("agent is already streaming; use steer or follow_up")
		}
	}
	a.streaming = true
	a.mu.Unlock()
	signalPreflight(true)
	var loopAgent *agentcore.Agent
	released := false
	defer func() {
		if !released {
			a.finishPrompt(loopAgent)
		}
	}()

	if expanded, ok := a.Resources.ExpandInput(text); ok {
		text = expanded
	}
	maxLoopHit := false
	var persistErr error
	loopAgent = a.newLoopAgent(sink, &maxLoopHit, &persistErr)
	a.mu.Lock()
	a.activeAgent = loopAgent
	a.mu.Unlock()
	a.drainSteeringInto(loopAgent)
	messages, err := loopAgent.Prompt(ctx, ai.NewUserMessage(text, images))
	a.finishPrompt(loopAgent)
	released = true
	// Surface a persistence failure that occurred while incrementally saving
	// messages, unless the run itself already failed.
	if err == nil && persistErr != nil {
		err = persistErr
	}
	return messages, maxLoopHit, false, err
}

// currentLeaf returns the current branch leaf id ("" for an empty session).
func (a *AgentSession) currentLeaf() string {
	if a.Session == nil || a.Session.CurrentID == nil {
		return ""
	}
	return *a.Session.CurrentID
}

func (a *AgentSession) retryablePromptError(messages []agentcore.AgentMessage, maxLoopHit bool) string {
	if maxLoopHit {
		return ""
	}
	a.mu.Lock()
	enabled := a.autoRetryEnabled
	a.mu.Unlock()
	if !enabled || len(messages) == 0 {
		return ""
	}
	last := messages[len(messages)-1]
	assistant, ok := ai.AsAssistantMessage(last)
	if !ok || assistant.StopReason != "error" {
		return ""
	}
	model := a.CurrentModel()
	if ai.IsContextOverflow(assistant, model.ContextWindow) {
		return ""
	}
	msg := firstNonEmpty(strings.TrimSpace(assistant.ErrorMessage), strings.TrimSpace(ai.MessageText(last)), "assistant error")
	// Only retry transient provider/network errors. Provider-limit errors
	// (quota/billing/usage caps) must not be retried, matching the TypeScript
	// _isRetryableError / _isNonRetryableProviderLimitError classification.
	if !ai.IsRetryableProviderError(msg) {
		return ""
	}
	return msg
}

func (a *AgentSession) contextOverflowPromptError(messages []agentcore.AgentMessage, maxLoopHit bool) string {
	if maxLoopHit || len(messages) == 0 {
		return ""
	}
	last := messages[len(messages)-1]
	assistant, ok := ai.AsAssistantMessage(last)
	model := a.CurrentModel()
	if !ok || assistant.StopReason != "error" || !ai.IsContextOverflow(assistant, model.ContextWindow) {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(assistant.ErrorMessage), strings.TrimSpace(ai.MessageText(last)), "context overflow")
}

func (a *AgentSession) maxAutoRetries() int {
	if a.Settings == nil {
		return 0
	}
	return max(0, a.Settings.RetryMaxRetries())
}

func (a *AgentSession) retryBaseDelayMS() int {
	if a.Settings == nil {
		return 2000
	}
	return max(1, a.Settings.RetryBaseDelayMS())
}

func retryDelayMS(base, attempt int) int {
	delay := max(1, base)
	for i := 1; i < attempt; i++ {
		if delay >= 30000 {
			return 30000
		}
		delay *= 2
	}
	if delay > 30000 {
		return 30000
	}
	return delay
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *AgentSession) compact(ctx context.Context, reason CompactionReason, customInstructions string, sink ai.EventSink) (map[string]any, error) {
	return a.compactInternal(ctx, reason, customInstructions, sink, false)
}

func (a *AgentSession) compactInternal(ctx context.Context, reason CompactionReason, customInstructions string, sink ai.EventSink, willRetry bool) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	compactionCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	if a.disposed {
		a.mu.Unlock()
		cancel()
		return nil, errorsString("agent session is disposed")
	}
	if a.compacting {
		a.mu.Unlock()
		cancel()
		return nil, errorsString("agent is already compacting")
	}
	a.compacting = true
	a.compactionCancel = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.compacting = false
		a.compactionCancel = nil
		a.mu.Unlock()
		cancel()
	}()

	emit(sink, ai.Event{"type": "compaction_start", "reason": string(reason)})
	a.emitSessionEvent(CompactionStartEvent{Reason: reason})

	pathEntries := append([]SessionEntry(nil), a.Session.Branch()...)
	preparation := prepareCompaction(pathEntries, compactionSettingsFromManager(a.Settings))
	fromExtension := false
	var compacted *compactionResult
	if preparation != nil && a.extensionHasHandlers("session_before_compact") {
		event := a.emitExtensionBeforeCompaction(compactionCtx, preparation, pathEntries, customInstructions)
		if event.Cancel {
			err := context.Canceled
			emit(sink, ai.Event{"type": "compaction_end", "reason": string(reason), "result": nil, "aborted": true, "willRetry": willRetry})
			a.emitSessionEvent(CompactionEndEvent{Reason: reason, Aborted: true, WillRetry: willRetry})
			return nil, err
		}
		if event.Result != nil {
			firstKeptEntryID := event.Result.FirstKeptEntryID
			if firstKeptEntryID == "" {
				firstKeptEntryID = preparation.FirstKeptEntryID
			}
			tokensBefore := event.Result.TokensBefore
			if tokensBefore == 0 {
				tokensBefore = preparation.TokensBefore
			}
			compacted = &compactionResult{
				Summary:          event.Result.Summary,
				FirstKeptEntryID: firstKeptEntryID,
				TokensBefore:     tokensBefore,
				Details:          compactionDetailsFromAny(event.Result.Details),
			}
			fromExtension = true
		}
	}
	var err error
	if compacted == nil {
		snapshot := a.modelSnapshot()
		compacted, err = runCompaction(compactionCtx, a.Registry, snapshot.Model, snapshot.ThinkingLevel, preparation, customInstructions)
	}
	if err == nil && compacted == nil {
		emit(sink, ai.Event{"type": "compaction_end", "reason": string(reason), "result": nil, "aborted": false, "willRetry": willRetry})
		a.emitSessionEvent(CompactionEndEvent{Reason: reason, Aborted: false, WillRetry: willRetry})
		return nil, nil
	}
	var result map[string]any
	if compacted != nil {
		result = map[string]any{
			"summary":          compacted.Summary,
			"firstKeptEntryId": compacted.FirstKeptEntryID,
			"tokensBefore":     compacted.TokensBefore,
			"details":          compacted.Details,
		}
	}
	entry := SessionEntry{}
	if compacted != nil {
		entry = SessionEntry{
			Type:         "compaction",
			Summary:      compacted.Summary,
			FirstKeptID:  compacted.FirstKeptEntryID,
			TokensBefore: compacted.TokensBefore,
			Details:      compacted.Details,
		}
	}
	if compacted != nil {
		err = a.Session.Append(entry)
	}
	if err != nil {
		aborted := errors.Is(err, context.Canceled)
		event := ai.Event{"type": "compaction_end", "reason": string(reason), "result": nil, "aborted": aborted, "willRetry": false}
		errorMessage := ""
		if !aborted {
			errorMessage = err.Error()
			event["errorMessage"] = errorMessage
		}
		event["willRetry"] = willRetry
		emit(sink, event)
		a.emitSessionEvent(CompactionEndEvent{Reason: reason, Aborted: aborted, WillRetry: willRetry, ErrorMessage: errorMessage})
		return nil, err
	}
	emit(sink, ai.Event{"type": "compaction_end", "reason": string(reason), "result": result, "aborted": false, "willRetry": willRetry})
	a.emitSessionEvent(CompactionEndEvent{Reason: reason, Result: cloneMap(result), Aborted: false, WillRetry: willRetry})
	a.emitExtensionSessionCompact(entry, fromExtension)
	return result, nil
}

func (a *AgentSession) maybeAutoCompact(ctx context.Context, sink ai.EventSink) error {
	a.mu.Lock()
	enabled := a.autoCompactionEnabled
	compacting := a.compacting
	a.mu.Unlock()
	if !enabled || compacting {
		return nil
	}
	usage := a.GetContextUsage()
	if usage == nil || !shouldCompact(usage.UsedTokens, usage.ContextWindow, compactionSettingsFromManager(a.Settings)) {
		return nil
	}
	_, err := a.compact(ctx, CompactionThreshold, "", sink)
	return err
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func (a *AgentSession) Subscribe(listener SessionEventListener) func() {
	if listener == nil {
		return func() {}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.nextSessionListenerID
	a.nextSessionListenerID++
	if a.sessionListeners == nil {
		a.sessionListeners = map[uint64]SessionEventListener{}
	}
	a.sessionListeners[id] = listener
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.sessionListeners, id)
	}
}

func (a *AgentSession) emitSessionEvent(event SessionEvent) {
	if a == nil || event == nil {
		return
	}
	a.mu.Lock()
	listeners := make([]SessionEventListener, 0, len(a.sessionListeners))
	for _, listener := range a.sessionListeners {
		if listener != nil {
			listeners = append(listeners, listener)
		}
	}
	a.mu.Unlock()
	for _, listener := range listeners {
		listener(event)
	}
}

func (a *AgentSession) emitQueueUpdate() {
	a.mu.Lock()
	event := QueueUpdateEvent{Steering: queueMessages(a.steeringQueue), FollowUp: queueMessages(a.followUpQueue)}
	a.mu.Unlock()
	a.emitSessionEvent(event)
}

func queueMessages(queue []queuedPrompt) []string {
	if len(queue) == 0 {
		return nil
	}
	result := make([]string, 0, len(queue))
	for _, item := range queue {
		result = append(result, item.Message)
	}
	return result
}

func (a *AgentSession) SetScopedModels(models []ScopedModel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.scopedModels = append([]ScopedModel(nil), models...)
}

func (a *AgentSession) availableModelsWithScoped() ([]ai.Model, bool) {
	a.mu.Lock()
	scoped := append([]ScopedModel(nil), a.scopedModels...)
	a.mu.Unlock()
	if len(scoped) > 0 {
		models := make([]ai.Model, 0, len(scoped))
		for _, scopedModel := range scoped {
			if scopedModel.Model.Provider == "" {
				continue
			}
			models = append(models, scopedModel.Model)
		}
		if len(models) > 0 {
			return models, true
		}
	}
	if a.Registry == nil {
		return nil, false
	}
	return a.Registry.AvailableConfigured(), false
}

func (a *AgentSession) availableModels() []ai.Model {
	models, _ := a.availableModelsWithScoped()
	return models
}

func (a *AgentSession) hasScopedModels() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.scopedModels) > 0
}

func (a *AgentSession) GetAvailableThinkingLevels() []ai.ThinkingLevel {
	return availableThinkingLevelsForModel(a.CurrentModel())
}

func availableThinkingLevelsForModel(model ai.Model) []ai.ThinkingLevel {
	levels := append([]ai.ThinkingLevel(nil), model.ThinkingLevels...)
	if len(levels) == 0 {
		levels = []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingMinimal, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh}
	}
	return levels
}

func (a *AgentSession) SupportsThinking() bool {
	return modelSupportsThinking(a.CurrentModel())
}

func modelSupportsThinking(model ai.Model) bool {
	levels := availableThinkingLevelsForModel(model)
	return model.Reasoning || len(levels) > 1 || (len(levels) == 1 && levels[0] != ai.ThinkingOff)
}

func (a *AgentSession) SetSteeringMode(mode agentcore.QueueMode) {
	if mode == "" {
		mode = agentcore.QueueOneAtATime
	}
	a.mu.Lock()
	a.steeringMode = string(mode)
	active := a.activeAgent
	a.mu.Unlock()
	if active != nil {
		active.SetSteeringMode(mode)
	}
}

func (a *AgentSession) SetFollowUpMode(mode agentcore.QueueMode) {
	if mode == "" {
		mode = agentcore.QueueOneAtATime
	}
	a.mu.Lock()
	a.followUpMode = string(mode)
	active := a.activeAgent
	a.mu.Unlock()
	if active != nil {
		active.SetFollowUpMode(mode)
	}
}

func (a *AgentSession) ClearQueue() ClearedQueue {
	a.mu.Lock()
	cleared := ClearedQueue{Steering: queueMessages(a.steeringQueue), FollowUp: queueMessages(a.followUpQueue)}
	a.steeringQueue = nil
	a.followUpQueue = nil
	active := a.activeAgent
	a.mu.Unlock()
	if active != nil {
		active.ClearAllQueues()
	}
	a.emitSessionEvent(QueueUpdateEvent{})
	return cleared
}

func (a *AgentSession) Steer(ctx context.Context, text string, images []ai.ContentBlock) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	a.QueueSteer(text, images)
	return nil
}

func (a *AgentSession) FollowUp(ctx context.Context, text string, images []ai.ContentBlock) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	a.QueueFollowUp(text, images)
	return nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (a *AgentSession) SendUserMessage(ctx context.Context, opts SendUserMessageOptions) error {
	if strings.TrimSpace(opts.Text) == "" && len(opts.Images) == 0 {
		return fmt.Errorf("message is required")
	}
	err := a.Prompt(ctx, opts.Text, opts.Images, nil)
	if err == nil {
		return nil
	}
	if isAlreadyStreamingPromptError(err) {
		switch opts.StreamingBehavior {
		case StreamingSteer:
			return a.Steer(ctx, opts.Text, opts.Images)
		case StreamingFollowUp:
			return a.FollowUp(ctx, opts.Text, opts.Images)
		default:
			return err
		}
	}
	return err
}

func isAlreadyStreamingPromptError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "agent is already streaming")
}

func (a *AgentSession) Abort(ctx context.Context) error {
	return a.abortActiveAgent(ctx, true)
}

func (a *AgentSession) abortActiveAgent(ctx context.Context, notifyExtensions bool) error {
	a.mu.Lock()
	active := a.activeAgent
	abortHandler := a.extensionAbortHandler
	retryCancel := a.retryCancel
	a.mu.Unlock()
	if active == nil && retryCancel == nil && (!notifyExtensions || abortHandler == nil) {
		return nil
	}
	if active != nil {
		active.Abort()
	}
	if retryCancel != nil {
		retryCancel()
	}
	if notifyExtensions {
		a.invokeAbortHandler(abortHandler)
	}
	if ctx == nil {
		return nil
	}
	if active == nil {
		return nil
	}
	return active.WaitForIdle(ctx)
}

func (a *AgentSession) AbortCompaction() {
	a.mu.Lock()
	cancel := a.compactionCancel
	a.compactionCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *AgentSession) AbortBranchSummary() {
	a.mu.Lock()
	cancel := a.branchSummaryCancel
	a.branchSummaryCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *AgentSession) SetAutoCompactionEnabled(enabled bool) {
	a.mu.Lock()
	a.autoCompactionEnabled = enabled
	a.mu.Unlock()
}

func (a *AgentSession) AbortRetry() {
	a.mu.Lock()
	cancel := a.retryCancel
	a.retryCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *AgentSession) SetAutoRetryEnabled(enabled bool) {
	a.mu.Lock()
	a.autoRetryEnabled = enabled
	a.mu.Unlock()
}

func (a *AgentSession) ExecuteBash(ctx context.Context, command string, opts BashRunOptions) (BashResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	customShell := ""
	if a.Settings != nil {
		if custom := strings.TrimSpace(a.Settings.mergedString(a.Settings.Global.ShellPath, a.Settings.Project.ShellPath, "")); custom != "" {
			customShell = custom
		}
	}
	shellConfig, err := catools.ResolveShellConfig(customShell)
	if err != nil {
		return BashResult{}, err
	}
	bashCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.activeBashCancel = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.activeBashCancel = nil
		a.mu.Unlock()
		cancel()
	}()
	execCommand := command
	if a.Settings != nil {
		if prefix := strings.TrimSpace(a.Settings.ShellCommandPrefix()); prefix != "" {
			execCommand = prefix + "\n" + execCommand
		}
	}
	cmd := catools.ShellCommandContext(bashCtx, shellConfig, execCommand)
	if a.Session != nil && a.Session.CWD() != "" {
		cmd.Dir = a.Session.CWD()
	}
	// Prepend the agent bin directory to PATH so migrated/installed tools (fd,
	// rg, package CLIs) resolve, mirroring the bash tool and getShellEnv() in TS.
	cmd.Env = catools.ShellEnv(BinDir())
	// Run in a dedicated process group and kill the whole tree on cancel, so
	// AbortBash / signal shutdown terminates detached children too, not just the
	// shell. Mirrors the bash tool's wiring (see ConfigureTreeKill).
	catools.ConfigureTreeKill(cmd)
	// Bound how long Wait blocks after the context is canceled (AbortBash /
	// signal shutdown). Without this, a background child that inherited the
	// command's output pipes can keep them open and wedge cmd.Run after the
	// shell itself is killed. Mirrors cmd.WaitDelay in the bash tool.
	cmd.WaitDelay = 2 * time.Second
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Run = Start + Wait, split so the detached process group can be tracked for
	// shutdown cleanup (see catools.KillTrackedDetachedChildren / shell.ts).
	if err = cmd.Start(); err == nil {
		if cmd.Process != nil {
			pid := cmd.Process.Pid
			catools.TrackDetachedChildPID(pid)
			defer catools.UntrackDetachedChildPID(pid)
		}
		err = cmd.Wait()
	}
	// Sanitize (strip ANSI / binary / CR) and truncate the combined output, then
	// spill the full output to a temp file when truncated, mirroring TS
	// bash-executor.ts executeBashWithOperations (the shared interactive+RPC bash
	// executor). Without this, the RPC/SDK bash path returned unbounded, unsanitized
	// output straight into the session context (8.md P1-1).
	sanitized := catools.SanitizeAndTruncateBashOutput(stdout.String() + stderr.String())
	result := BashResult{
		Command:        command,
		Output:         sanitized.Output,
		ExitCode:       0,
		Cancelled:      bashCtx.Err() == context.Canceled,
		Truncated:      sanitized.Truncated,
		FullOutputPath: sanitized.FullOutputPath,
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		err = nil
	} else if err != nil && result.Cancelled {
		result.ExitCode = -1
		err = nil
	}
	if !opts.ExcludeFromContext {
		a.RecordBashResult(command, result, opts)
	}
	return result, err
}

func (a *AgentSession) RecordBashResult(command string, result BashResult, opts BashRunOptions) {
	if a == nil || a.Session == nil || opts.ExcludeFromContext {
		return
	}
	details := map[string]any{"command": command, "exitCode": result.ExitCode, "cancelled": result.Cancelled, "truncated": result.Truncated}
	if result.FullOutputPath != "" {
		details["fullOutputPath"] = result.FullOutputPath
	}
	_ = a.Session.Append(SessionEntry{Type: "custom_message", CustomType: "bash_result", Content: result.Output, Display: true, Details: details})
}

func (a *AgentSession) AbortBash() {
	a.mu.Lock()
	cancel := a.activeBashCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
