package harness

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/guanshan/pi-go/packages/agent"
	harnesscompaction "github.com/guanshan/pi-go/packages/agent/harness/compaction"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

type ContextEvent struct {
	Messages []agent.AgentMessage
}

type BeforeAgentStartEvent struct {
	Prompt       string
	Images       []ai.ContentBlock
	SystemPrompt string
	Resources    Resources
}

type BeforeAgentStartResult struct {
	Messages     []agent.AgentMessage
	SystemPrompt string
}

type ContextResult struct {
	Messages []agent.AgentMessage
}

type BeforeProviderRequestEvent struct {
	Model         ai.Model
	SessionID     string
	StreamOptions StreamOptions
}

type BeforeProviderRequestResult struct {
	StreamOptions *StreamOptionsPatch
}

type BeforeProviderPayloadEvent struct {
	Model   ai.Model
	Payload any
}

type BeforeProviderPayloadResult struct {
	Payload any
}

type AfterProviderResponseEvent struct {
	Status  int
	Headers map[string]string
}

type ToolCallEvent struct {
	ToolCallID string
	ToolName   string
	Input      map[string]any
}

type ToolCallResult struct {
	Block  bool
	Reason string
}

type ToolResultEvent struct {
	ToolCallID string
	ToolName   string
	Input      map[string]any
	Content    []ai.ContentBlock
	Details    any
	IsError    bool
}

type ToolResultPatch struct {
	Content   []ai.ContentBlock
	Details   *AnyValue
	IsError   *bool
	Terminate *bool
}

type SessionBeforeTreeEvent struct {
	Preparation TreePreparation
}

type SessionBeforeCompactEvent struct {
	Preparation        *harnesscompaction.Preparation
	BranchEntries      []session.Entry
	CustomInstructions string
}

type SessionBeforeCompactResult struct {
	Cancel     bool
	Compaction *CompactionResult
}

type SessionCompactEvent struct {
	CompactionEntry session.CompactionEntry
	FromHook        bool
}

type SessionBeforeTreeResult struct {
	Cancel              bool
	Summary             *BranchSummary
	CustomInstructions  string
	ReplaceInstructions *bool
	Label               *string
}

type SessionTreeEvent struct {
	NewLeafID    string
	OldLeafID    string
	SummaryEntry *session.BranchSummaryEntry
	FromHook     bool
}

type ModelSelectSource string

const (
	ModelSelectSourceSet     ModelSelectSource = "set"
	ModelSelectSourceRestore ModelSelectSource = "restore"
)

type ModelSelectEvent struct {
	Model         ai.Model
	PreviousModel ai.Model
	Source        ModelSelectSource
}

type ThinkingLevelSelectEvent struct {
	Level         ai.ThinkingLevel
	PreviousLevel ai.ThinkingLevel
}

type ResourcesUpdateEvent struct {
	Resources         Resources
	PreviousResources Resources
}

func (h *AgentHarness) OnContext(f func(context.Context, ContextEvent) (*ContextResult, error)) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.contextHandlers, f)
}

func (h *AgentHarness) OnBeforeAgentStart(f func(context.Context, BeforeAgentStartEvent) (*BeforeAgentStartResult, error)) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.beforeAgentStartHandlers, f)
}

func (h *AgentHarness) OnBeforeProviderRequest(f func(context.Context, BeforeProviderRequestEvent) (*BeforeProviderRequestResult, error)) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.beforeProviderRequestHandlers, f)
}

func (h *AgentHarness) OnBeforeProviderPayload(f func(context.Context, BeforeProviderPayloadEvent) (*BeforeProviderPayloadResult, error)) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.beforeProviderPayloadHandlers, f)
}

func (h *AgentHarness) OnAfterProviderResponse(f func(context.Context, AfterProviderResponseEvent) error) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.afterProviderResponseHandlers, f)
}

func (h *AgentHarness) OnToolCall(f func(context.Context, ToolCallEvent) (*ToolCallResult, error)) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.toolCallHandlers, f)
}

func (h *AgentHarness) OnToolResult(f func(context.Context, ToolResultEvent) (*ToolResultPatch, error)) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.toolResultHandlers, f)
}

func (h *AgentHarness) OnSessionBeforeCompact(f func(context.Context, SessionBeforeCompactEvent) (*SessionBeforeCompactResult, error)) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.sessionBeforeCompactHandlers, f)
}

func (h *AgentHarness) OnSessionCompact(f func(context.Context, SessionCompactEvent) error) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.sessionCompactHandlers, f)
}

func (h *AgentHarness) OnSessionBeforeTree(f func(context.Context, SessionBeforeTreeEvent) (*SessionBeforeTreeResult, error)) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.sessionBeforeTreeHandlers, f)
}

func (h *AgentHarness) OnSessionTree(f func(context.Context, SessionTreeEvent) error) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.sessionTreeHandlers, f)
}

func (h *AgentHarness) OnModelSelect(f func(context.Context, ModelSelectEvent) error) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.modelSelectHandlers, f)
}

func (h *AgentHarness) OnThinkingLevelSelect(f func(context.Context, ThinkingLevelSelectEvent) error) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.thinkingLevelSelectHandlers, f)
}

func (h *AgentHarness) OnResourcesUpdate(f func(context.Context, ResourcesUpdateEvent) error) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.resourcesUpdateHandlers, f)
}

func (h *AgentHarness) OnToolsUpdate(f func(context.Context, ToolsUpdateEvent) error) func() {
	if f == nil {
		return func() {}
	}
	return addHarnessHandler(&h.mu, &h.toolsUpdateHandlers, f)
}

func addHarnessHandler[T any](mu *sync.Mutex, handlers *[]T, f T) func() {
	mu.Lock()
	*handlers = append(*handlers, f)
	index := len(*handlers) - 1
	mu.Unlock()
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if index >= 0 && index < len(*handlers) {
			var zero T
			(*handlers)[index] = zero
		}
	}
}

func (h *AgentHarness) emitBeforeAgentStart(ctx context.Context, ev BeforeAgentStartEvent) (*BeforeAgentStartResult, error) {
	h.mu.Lock()
	handlers := append([]func(context.Context, BeforeAgentStartEvent) (*BeforeAgentStartResult, error)(nil), h.beforeAgentStartHandlers...)
	h.mu.Unlock()
	var out BeforeAgentStartResult
	var changed bool
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		current := BeforeAgentStartEvent{
			Prompt:       ev.Prompt,
			Images:       append([]ai.ContentBlock(nil), ev.Images...),
			SystemPrompt: firstString(out.SystemPrompt, ev.SystemPrompt),
			Resources:    cloneResources(ev.Resources),
		}
		result, err := handler(ctx, current)
		if err != nil {
			return nil, err
		}
		if result == nil {
			continue
		}
		if result.SystemPrompt != "" {
			out.SystemPrompt = result.SystemPrompt
			changed = true
		}
		if result.Messages != nil {
			out.Messages = append(out.Messages, result.Messages...)
			changed = true
		}
	}
	if !changed {
		return nil, nil
	}
	return &out, nil
}

func (h *AgentHarness) transformContext(ctx context.Context, msgs []agent.AgentMessage) ([]agent.AgentMessage, error) {
	h.mu.Lock()
	handlers := append([]func(context.Context, ContextEvent) (*ContextResult, error)(nil), h.contextHandlers...)
	h.mu.Unlock()
	current := append([]agent.AgentMessage(nil), msgs...)
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		result, err := handler(ctx, ContextEvent{Messages: append([]agent.AgentMessage(nil), current...)})
		if err != nil {
			return current, err
		}
		if result != nil && result.Messages != nil {
			current = append([]agent.AgentMessage(nil), result.Messages...)
		}
	}
	return current, nil
}

func (h *AgentHarness) beforeToolCall(ctx context.Context, tc agent.BeforeToolCallContext) (agent.BeforeToolCallResult, error) {
	result, err := h.emitToolCall(ctx, ToolCallEvent{
		ToolCallID: tc.ToolCall.ID,
		ToolName:   tc.ToolCall.Name,
		Input:      argsToInputMap(tc.Args),
	})
	if err != nil || result == nil {
		return agent.BeforeToolCallResult{}, err
	}
	return agent.BeforeToolCallResult{Block: result.Block, Reason: result.Reason}, nil
}

func (h *AgentHarness) afterToolCall(ctx context.Context, tc agent.AfterToolCallContext) (agent.AfterToolCallResult, error) {
	patch, err := h.emitToolResult(ctx, ToolResultEvent{
		ToolCallID: tc.ToolCall.ID,
		ToolName:   tc.ToolCall.Name,
		Input:      argsToInputMap(tc.Args),
		Content:    append([]ai.ContentBlock(nil), tc.Result.Content...),
		Details:    tc.Result.Details,
		IsError:    tc.IsError,
	})
	if err != nil || patch == nil {
		return agent.AfterToolCallResult{}, err
	}
	return convertToolResultPatch(*patch), nil
}

func (h *AgentHarness) emitBeforeProviderRequest(ctx context.Context, ev BeforeProviderRequestEvent) (StreamOptions, error) {
	h.mu.Lock()
	handlers := append([]func(context.Context, BeforeProviderRequestEvent) (*BeforeProviderRequestResult, error)(nil), h.beforeProviderRequestHandlers...)
	h.mu.Unlock()
	current := cloneStreamOptions(ev.StreamOptions)
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		next := BeforeProviderRequestEvent{
			Model:         ev.Model,
			SessionID:     ev.SessionID,
			StreamOptions: cloneStreamOptions(current),
		}
		result, err := handler(ctx, next)
		if err != nil {
			return current, err
		}
		if result != nil && result.StreamOptions != nil {
			current = applyStreamOptionsPatch(current, result.StreamOptions)
		}
	}
	return current, nil
}

func (h *AgentHarness) wrapProviderHooks(ctx context.Context, opts ai.StreamOptions) ai.StreamOptions {
	basePayload := opts.OnPayload
	opts.OnPayload = func(payload any, model ai.Model) (any, error) {
		current := payload
		var err error
		if basePayload != nil {
			current, err = basePayload(current, model)
			if err != nil {
				return current, err
			}
		}
		return h.emitBeforeProviderPayload(ctx, model, current)
	}
	baseResponse := opts.OnResponse
	opts.OnResponse = func(resp ai.ProviderResponse, model ai.Model) error {
		if baseResponse != nil {
			if err := baseResponse(resp, model); err != nil {
				return err
			}
		}
		return h.emitAfterProviderResponse(ctx, resp)
	}
	return opts
}

func (h *AgentHarness) emitBeforeProviderPayload(ctx context.Context, model ai.Model, payload any) (any, error) {
	h.mu.Lock()
	handlers := append([]func(context.Context, BeforeProviderPayloadEvent) (*BeforeProviderPayloadResult, error)(nil), h.beforeProviderPayloadHandlers...)
	h.mu.Unlock()
	current := payload
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		result, err := handler(ctx, BeforeProviderPayloadEvent{Model: model, Payload: current})
		if err != nil {
			return current, err
		}
		if result != nil {
			current = result.Payload
		}
	}
	return current, nil
}

func (h *AgentHarness) emitAfterProviderResponse(ctx context.Context, resp ai.ProviderResponse) error {
	h.mu.Lock()
	handlers := append([]func(context.Context, AfterProviderResponseEvent) error(nil), h.afterProviderResponseHandlers...)
	h.mu.Unlock()
	event := AfterProviderResponseEvent{Status: resp.Status, Headers: mergeStringMaps(resp.Headers)}
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		if err := handler(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (h *AgentHarness) emitToolCall(ctx context.Context, ev ToolCallEvent) (*ToolCallResult, error) {
	h.mu.Lock()
	handlers := append([]func(context.Context, ToolCallEvent) (*ToolCallResult, error)(nil), h.toolCallHandlers...)
	h.mu.Unlock()
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		current := ToolCallEvent{ToolCallID: ev.ToolCallID, ToolName: ev.ToolName, Input: mergeAnyMaps(ev.Input)}
		result, err := handler(ctx, current)
		if err != nil || result == nil {
			return result, err
		}
		if result.Block {
			return result, nil
		}
	}
	return nil, nil
}

func (h *AgentHarness) emitToolResult(ctx context.Context, ev ToolResultEvent) (*ToolResultPatch, error) {
	h.mu.Lock()
	handlers := append([]func(context.Context, ToolResultEvent) (*ToolResultPatch, error)(nil), h.toolResultHandlers...)
	h.mu.Unlock()
	current := ev
	var out ToolResultPatch
	var changed bool
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		next := ToolResultEvent{
			ToolCallID: current.ToolCallID,
			ToolName:   current.ToolName,
			Input:      mergeAnyMaps(current.Input),
			Content:    append([]ai.ContentBlock(nil), current.Content...),
			Details:    current.Details,
			IsError:    current.IsError,
		}
		patch, err := handler(ctx, next)
		if err != nil {
			return nil, err
		}
		if patch == nil {
			continue
		}
		if patch.Content != nil {
			current.Content = append([]ai.ContentBlock(nil), patch.Content...)
			out.Content = append([]ai.ContentBlock(nil), patch.Content...)
			changed = true
		}
		if patch.Details != nil {
			current.Details = patch.Details.V
			out.Details = patch.Details
			changed = true
		}
		if patch.IsError != nil {
			current.IsError = *patch.IsError
			out.IsError = patch.IsError
			changed = true
		}
		if patch.Terminate != nil {
			out.Terminate = patch.Terminate
			changed = true
		}
	}
	if !changed {
		return nil, nil
	}
	return &out, nil
}

func (h *AgentHarness) emitSessionBeforeCompact(ctx context.Context, ev SessionBeforeCompactEvent) (*SessionBeforeCompactResult, error) {
	h.mu.Lock()
	handlers := append([]func(context.Context, SessionBeforeCompactEvent) (*SessionBeforeCompactResult, error)(nil), h.sessionBeforeCompactHandlers...)
	h.mu.Unlock()
	var out SessionBeforeCompactResult
	var changed bool
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		result, err := handler(ctx, ev)
		if err != nil {
			return nil, err
		}
		if result == nil {
			continue
		}
		if result.Cancel {
			out.Cancel = true
			changed = true
		}
		if result.Compaction != nil {
			compaction := *result.Compaction
			out.Compaction = &compaction
			changed = true
		}
	}
	if !changed {
		return nil, nil
	}
	return &out, nil
}

func (h *AgentHarness) emitSessionCompact(ctx context.Context, ev SessionCompactEvent) error {
	h.mu.Lock()
	handlers := append([]func(context.Context, SessionCompactEvent) error(nil), h.sessionCompactHandlers...)
	h.mu.Unlock()
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		if err := handler(ctx, ev); err != nil {
			return err
		}
	}
	return h.emitHarness(ctx, ev)
}

func (h *AgentHarness) emitSessionBeforeTree(ctx context.Context, ev SessionBeforeTreeEvent) (*SessionBeforeTreeResult, error) {
	h.mu.Lock()
	handlers := append([]func(context.Context, SessionBeforeTreeEvent) (*SessionBeforeTreeResult, error)(nil), h.sessionBeforeTreeHandlers...)
	h.mu.Unlock()
	var out SessionBeforeTreeResult
	var changed bool
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		result, err := handler(ctx, ev)
		if err != nil {
			return nil, err
		}
		if result == nil {
			continue
		}
		if result.Cancel {
			out.Cancel = true
			changed = true
		}
		if result.Summary != nil {
			summary := *result.Summary
			out.Summary = &summary
			changed = true
		}
		if result.CustomInstructions != "" {
			out.CustomInstructions = result.CustomInstructions
			changed = true
		}
		if result.ReplaceInstructions != nil {
			value := *result.ReplaceInstructions
			out.ReplaceInstructions = &value
			changed = true
		}
		if result.Label != nil {
			value := *result.Label
			out.Label = &value
			changed = true
		}
	}
	if !changed {
		return nil, nil
	}
	return &out, nil
}

func (h *AgentHarness) emitSessionTree(ctx context.Context, ev SessionTreeEvent) error {
	h.mu.Lock()
	handlers := append([]func(context.Context, SessionTreeEvent) error(nil), h.sessionTreeHandlers...)
	h.mu.Unlock()
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		if err := handler(ctx, ev); err != nil {
			return err
		}
	}
	return h.emitHarness(ctx, ev)
}

func (h *AgentHarness) emitModelSelect(ctx context.Context, ev ModelSelectEvent) error {
	h.mu.Lock()
	handlers := append([]func(context.Context, ModelSelectEvent) error(nil), h.modelSelectHandlers...)
	h.mu.Unlock()
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		if err := handler(ctx, ev); err != nil {
			return err
		}
	}
	return h.emitHarness(ctx, ev)
}

func (h *AgentHarness) emitThinkingLevelSelect(ctx context.Context, ev ThinkingLevelSelectEvent) error {
	h.mu.Lock()
	handlers := append([]func(context.Context, ThinkingLevelSelectEvent) error(nil), h.thinkingLevelSelectHandlers...)
	h.mu.Unlock()
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		if err := handler(ctx, ev); err != nil {
			return err
		}
	}
	return h.emitHarness(ctx, ev)
}

func (h *AgentHarness) emitResourcesUpdate(ctx context.Context, ev ResourcesUpdateEvent) error {
	h.mu.Lock()
	handlers := append([]func(context.Context, ResourcesUpdateEvent) error(nil), h.resourcesUpdateHandlers...)
	h.mu.Unlock()
	event := ResourcesUpdateEvent{
		Resources:         cloneResources(ev.Resources),
		PreviousResources: cloneResources(ev.PreviousResources),
	}
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		if err := handler(ctx, event); err != nil {
			return err
		}
	}
	return h.emitHarness(ctx, event)
}

func (h *AgentHarness) emitToolsUpdate(ctx context.Context, ev ToolsUpdateEvent) error {
	h.mu.Lock()
	handlers := append([]func(context.Context, ToolsUpdateEvent) error(nil), h.toolsUpdateHandlers...)
	h.mu.Unlock()
	event := ToolsUpdateEvent{
		ToolNames:               append([]string(nil), ev.ToolNames...),
		PreviousToolNames:       append([]string(nil), ev.PreviousToolNames...),
		ActiveToolNames:         append([]string(nil), ev.ActiveToolNames...),
		PreviousActiveToolNames: append([]string(nil), ev.PreviousActiveToolNames...),
		Source:                  ev.Source,
	}
	for _, handler := range handlers {
		if handler == nil {
			continue
		}
		if err := handler(ctx, event); err != nil {
			return err
		}
	}
	return h.emitHarness(ctx, event)
}

func convertToolResultPatch(patch ToolResultPatch) agent.AfterToolCallResult {
	out := agent.AfterToolCallResult{
		Content:   patch.Content,
		IsError:   patch.IsError,
		Terminate: patch.Terminate,
	}
	// ToolResultPatch keeps the harness convention of nil Content == "not
	// provided"; map that onto the agent's explicit HasContent gate.
	if patch.Content != nil {
		out.HasContent = true
	}
	if patch.Details != nil {
		out.Details = patch.Details.V
		out.HasDetails = true
	}
	return out
}

func argsToInputMap(args any) map[string]any {
	if args == nil {
		return nil
	}
	if m, ok := args.(map[string]any); ok {
		return mergeAnyMaps(m)
	}
	data, err := json.Marshal(args)
	if err == nil {
		var out map[string]any
		if json.Unmarshal(data, &out) == nil {
			return out
		}
	}
	return map[string]any{"value": args}
}

func cloneResources(resources Resources) Resources {
	return Resources{
		Skills:          append([]Skill(nil), resources.Skills...),
		PromptTemplates: append([]PromptTemplate(nil), resources.PromptTemplates...),
	}
}
