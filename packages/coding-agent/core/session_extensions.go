package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
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
	providerStop := a.extensionProviderStop
	runtime := a.extensionRuntime
	a.extensionUIContext = bindings.UIContext
	a.extensionCommandContextActions = bindings.CommandContextActions
	if strings.TrimSpace(bindings.Mode) != "" {
		a.extensionMode = strings.TrimSpace(bindings.Mode)
	}
	a.extensionTriggerTurnHandler = bindings.TriggerTurnHandler
	a.extensionUserMessageHandler = bindings.UserMessageHandler
	a.extensionCustomMessageHandler = bindings.CustomMessageHandler
	a.extensionAbortHandler = bindings.AbortHandler
	a.extensionShutdownHandler = bindings.ShutdownHandler
	a.extensionErrorListener = bindings.OnError
	a.extensionErrorStop = nil
	a.extensionProviderStop = nil
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
	if providerStop != nil {
		providerStop()
	}
	if runtime != nil && bindings.OnError != nil {
		a.mu.Lock()
		a.extensionErrorStop = runtime.OnError(coreext.ErrorListener(bindings.OnError))
		a.mu.Unlock()
	}
	a.installExtensionContextBridge()
	return nil
}

func (a *AgentSession) installExtensionContextBridge() {
	if a == nil {
		return
	}
	a.mu.Lock()
	runtime := a.extensionRuntime
	providerStop := a.extensionProviderStop
	a.extensionProviderStop = nil
	if strings.TrimSpace(a.extensionMode) == "" {
		a.extensionMode = "print"
	}
	a.mu.Unlock()
	if providerStop != nil {
		providerStop()
	}
	if runtime == nil {
		return
	}
	runtime.SetContextProvider(a.extensionContextSnapshot)
	runtime.SetContextActionHandler(a.handleExtensionContextAction)
	var stop func()
	if runtime.API != nil {
		stop = runtime.API.OnProviderChange(func(provider coreext.ProviderDefinition, registered bool) {
			if err := a.applyExtensionProviderDefinition(provider, registered); err != nil {
				runtime.EmitError(err)
			}
		})
	}
	for _, provider := range runtime.RegisteredProviders() {
		if err := a.applyExtensionProviderDefinition(provider, true); err != nil {
			runtime.EmitError(err)
		}
	}
	if stop != nil {
		a.mu.Lock()
		if !a.disposed && a.extensionRuntime == runtime {
			a.extensionProviderStop = stop
			stop = nil
		}
		a.mu.Unlock()
		if stop != nil {
			stop()
		}
	}
}

func (a *AgentSession) SetExtensionMode(mode string) {
	if a == nil {
		return
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "print"
	}
	a.mu.Lock()
	a.extensionMode = mode
	a.mu.Unlock()
}

func (a *AgentSession) extensionContextSnapshot() coreext.ExtensionContextSnapshot {
	if a == nil {
		return coreext.ExtensionContextSnapshot{Mode: "print", IsIdle: true}
	}
	a.mu.Lock()
	mode := strings.TrimSpace(a.extensionMode)
	if mode == "" {
		mode = "print"
	}
	model := a.Model
	hasModel := model.Provider != "" || model.ID != ""
	isIdle := !a.streaming && a.activeAgent == nil && !a.compacting
	hasPending := len(a.steeringQueue)+len(a.followUpQueue) > 0
	systemPrompt := a.SystemPrompt
	session := a.Session
	registry := a.Registry
	runtime := a.extensionRuntime
	a.mu.Unlock()

	var (
		cwd         string
		sessionID   string
		sessionFile string
		entries     []SessionEntry
		branch      []SessionEntry
		leafID      string
		usage       *ContextUsage
	)
	if session != nil {
		cwd = session.CWD()
		sessionID = session.SessionID()
		sessionFile = session.File()
		entries = session.EntriesSnapshot()
		branch = append([]SessionEntry(nil), session.Branch()...)
		leafID = session.CurrentLeafID()
		if model.ContextWindow > 0 {
			usage = &ContextUsage{
				UsedTokens:    estimateMessageTokens(session.BuildContext().Messages),
				ContextWindow: model.ContextWindow,
				EstimatedAt:   time.Now(),
			}
		}
	}
	var models []ai.Model
	var available []ai.Model
	if registry != nil {
		models = registry.ModelsSnapshot()
		available = append([]ai.Model(nil), registry.AvailableConfigured()...)
	}

	var modelPtr *ai.Model
	if hasModel {
		modelCopy := model
		modelPtr = &modelCopy
	}
	hasUI := false
	if runtime != nil && runtime.API != nil {
		hasUI = runtime.API.UIHandler() != nil
	}
	return coreext.ExtensionContextSnapshot{
		CWD:                cwd,
		Mode:               mode,
		HasUI:              hasUI,
		Model:              modelPtr,
		Models:             models,
		AvailableModels:    available,
		IsIdle:             isIdle,
		HasPendingMessages: hasPending,
		SystemPrompt:       systemPrompt,
		SessionID:          sessionID,
		SessionFile:        sessionFile,
		Entries:            entries,
		BranchEntries:      branch,
		LeafID:             leafID,
		ContextUsage:       usage,
	}
}

func (a *AgentSession) handleExtensionContextAction(ctx context.Context, action coreext.ExtensionContextAction) (json.RawMessage, error) {
	if a == nil {
		return nil, fmt.Errorf("agent session is nil")
	}
	switch normalizeExtensionEventType(action.Name) {
	case "abort":
		if err := a.Abort(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage("null"), nil
	case "compact":
		var params struct {
			CustomInstructions string `json:"customInstructions"`
		}
		if len(action.Params) > 0 {
			_ = json.Unmarshal(action.Params, &params)
		}
		result, err := a.CompactWithContext(ctx, params.CustomInstructions, nil)
		if err != nil {
			return nil, err
		}
		return marshalExtensionContextActionResult(result)
	case "shutdown":
		a.mu.Lock()
		handler := a.extensionShutdownHandler
		listener := a.extensionErrorListener
		a.mu.Unlock()
		if handler == nil {
			return nil, fmt.Errorf("ExtensionContext action shutdown is not supported by this host")
		}
		a.invokeShutdownHandler(ctx, handler, listener)
		return json.RawMessage("null"), nil
	case "get_api_key_and_headers", "getapikeyandheaders":
		return a.extensionModelAuth(ctx, action.Params)
	case "send_message", "sendmessage":
		return a.extensionSendMessage(ctx, action.Params)
	case "send_user_message", "sendusermessage":
		return a.extensionSendUserMessage(ctx, action.Params)
	case "append_entry", "appendentry":
		return a.extensionAppendEntry(action.Params)
	case "set_session_name", "setsessionname":
		return a.extensionSetSessionName(action.Params)
	case "get_session_name", "getsessionname":
		return a.extensionGetSessionName()
	case "set_label", "setlabel":
		return a.extensionSetLabel(action.Params)
	case "navigate_tree", "navigatetree":
		return a.extensionNavigateTree(ctx, action.Params)
	case "reload":
		if err := a.Reload(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage("null"), nil
	case "wait_for_idle", "waitforidle":
		if err := a.extensionWaitForIdle(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage("null"), nil
	case "get_system_prompt_options", "getsystempromptoptions":
		// The Go host exposes the resolved system-prompt text via getSystemPrompt(),
		// but does not model TS BuildSystemPromptOptions (the construction options).
		return nil, fmt.Errorf("ExtensionContext action %s is not supported by this host; use ctx.getSystemPrompt() for the resolved prompt text", action.Name)
	case "new_session", "newsession", "fork", "switch_session", "switchsession":
		// Session replacement (new/fork/switch) is driven by the mode loop's
		// runtime-swap machinery and is not reachable from an extension action in
		// this host build. navigateTree moves within the current session tree.
		return nil, fmt.Errorf("ExtensionContext action %s is not supported by this host (session replacement from an extension is unavailable; use ctx.navigateTree to move within the current session)", action.Name)
	default:
		return nil, fmt.Errorf("ExtensionContext action %s is not supported by this host", action.Name)
	}
}

// extensionNavigateTree routes the command-context navigateTree(targetId, options)
// call to AgentSession.NavigateTree and reports the {cancelled} result TS returns.
func (a *AgentSession) extensionNavigateTree(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var p struct {
		TargetID            string `json:"targetId"`
		Summarize           bool   `json:"summarize"`
		CustomInstructions  string `json:"customInstructions"`
		ReplaceInstructions bool   `json:"replaceInstructions"`
		Label               string `json:"label"`
	}
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(p.TargetID) == "" {
		return nil, fmt.Errorf("navigateTree requires a target entry id")
	}
	res, err := a.NavigateTree(ctx, p.TargetID, NavigateTreeOptions{
		Summarize:           p.Summarize,
		CustomInstructions:  p.CustomInstructions,
		ReplaceInstructions: p.ReplaceInstructions,
		Label:               p.Label,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"cancelled": res.Cancelled})
}

// extensionWaitForIdle blocks until the agent is no longer streaming/compacting,
// mirroring the TS command-context waitForIdle(). It is ctx-aware so session
// shutdown or request cancellation unblocks it.
func (a *AgentSession) extensionWaitForIdle(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		a.mu.Lock()
		idle := !a.streaming && a.activeAgent == nil && !a.compacting
		a.mu.Unlock()
		if idle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type extensionSendMessageContent struct {
	CustomType  string `json:"customType"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Content     any    `json:"content"`
	Text        any    `json:"text"`
	Display     *bool  `json:"display"`
	Details     any    `json:"details"`
	TriggerTurn bool   `json:"triggerTurn"`
}

type extensionSendMessagePayload struct {
	Message extensionSendMessageContent `json:"message"`
	Options struct {
		TriggerTurn bool `json:"triggerTurn"`
	} `json:"options"`
	CustomType  string `json:"customType"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Content     any    `json:"content"`
	Text        any    `json:"text"`
	Display     *bool  `json:"display"`
	Details     any    `json:"details"`
	TriggerTurn bool   `json:"triggerTurn"`
}

func (a *AgentSession) extensionSendMessage(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.Session == nil {
		return nil, fmt.Errorf("agent session is not available")
	}
	var payload extensionSendMessagePayload
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, fmt.Errorf("invalid sendMessage payload: %w", err)
		}
	}
	message := payload.Message
	customType := strings.TrimSpace(firstNonEmpty(message.CustomType, message.Type, message.Name))
	if customType == "" {
		customType = strings.TrimSpace(firstNonEmpty(payload.CustomType, payload.Type, payload.Name))
		message = extensionSendMessageContent{
			CustomType:  payload.CustomType,
			Type:        payload.Type,
			Name:        payload.Name,
			Content:     payload.Content,
			Text:        payload.Text,
			Display:     payload.Display,
			Details:     payload.Details,
			TriggerTurn: payload.TriggerTurn,
		}
	}
	customType = strings.TrimSpace(firstNonEmpty(message.CustomType, message.Type, message.Name, customType))
	if customType == "" {
		return nil, fmt.Errorf("sendMessage customType is required")
	}
	content := message.Content
	if content == nil {
		content = message.Text
	}
	display := true
	if message.Display != nil {
		display = *message.Display
	}
	details := message.Details
	triggerTurn := payload.Options.TriggerTurn || payload.TriggerTurn || message.TriggerTurn
	entry := SessionEntry{
		Type:       "custom_message",
		ID:         shortID(),
		CustomType: customType,
		Content:    content,
		Display:    display,
		Details:    details,
	}
	if err := a.Session.Append(entry); err != nil {
		return nil, err
	}
	// Surface display custom messages to the live host (interactive transcript)
	// best-effort; mirrors TS interactive-mode rendering the custom_message entry.
	if display {
		a.mu.Lock()
		customHandler := a.extensionCustomMessageHandler
		a.mu.Unlock()
		if customHandler != nil {
			customHandler(customType, content, details)
		}
	}
	triggerHandled := false
	if triggerTurn {
		a.mu.Lock()
		handler := a.extensionTriggerTurnHandler
		a.mu.Unlock()
		if handler != nil {
			if err := handler(ctx); err != nil {
				return nil, err
			}
			triggerHandled = true
		}
	}
	result := map[string]any{
		"ok":      true,
		"entryId": entry.ID,
	}
	if triggerTurn {
		result["triggerTurnRequested"] = true
		result["triggerTurnHandled"] = triggerHandled
	}
	return marshalExtensionContextActionResult(result)
}

type extensionSendUserMessagePayload struct {
	Content any `json:"content"`
	Options struct {
		DeliverAs string `json:"deliverAs"`
	} `json:"options"`
	DeliverAs string `json:"deliverAs"`
}

func (a *AgentSession) extensionSendUserMessage(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil || a.Session == nil {
		return nil, fmt.Errorf("agent session is not available")
	}
	var payload extensionSendUserMessagePayload
	if len(params) == 0 || string(params) == "null" {
		return nil, fmt.Errorf("sendUserMessage content is required")
	}
	if params[0] == '"' || params[0] == '[' {
		payload.Content = params
	} else if err := json.Unmarshal(params, &payload); err != nil {
		return nil, fmt.Errorf("invalid sendUserMessage payload: %w", err)
	}
	text, images, err := extensionUserContent(payload.Content)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" && len(images) == 0 {
		return nil, fmt.Errorf("sendUserMessage content is required")
	}
	behavior, err := extensionStreamingBehavior(firstNonEmpty(payload.Options.DeliverAs, payload.DeliverAs))
	if err != nil {
		return nil, err
	}
	opts := SendUserMessageOptions{
		Text:              text,
		Images:            images,
		StreamingBehavior: behavior,
		Source:            InputExtension,
	}
	a.mu.Lock()
	handler := a.extensionUserMessageHandler
	a.mu.Unlock()
	if handler != nil {
		err = handler(ctx, opts)
	} else {
		err = a.SendUserMessage(ctx, opts)
	}
	if err != nil {
		return nil, err
	}
	return marshalExtensionContextActionResult(map[string]any{"ok": true})
}

func extensionUserContent(content any) (string, []ai.ContentBlock, error) {
	switch value := content.(type) {
	case nil:
		return "", nil, nil
	case string:
		return value, nil, nil
	case json.RawMessage:
		if len(value) == 0 || string(value) == "null" {
			return "", nil, nil
		}
		if value[0] == '"' {
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				return "", nil, fmt.Errorf("invalid sendUserMessage content: %w", err)
			}
			return text, nil, nil
		}
		var blocks []ai.ContentBlock
		if err := json.Unmarshal(value, &blocks); err != nil {
			return "", nil, fmt.Errorf("invalid sendUserMessage content blocks: %w", err)
		}
		return extensionTextAndImages(blocks), extensionImageBlocks(blocks), nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return "", nil, fmt.Errorf("invalid sendUserMessage content: %w", err)
		}
		return extensionUserContent(json.RawMessage(raw))
	}
}

func extensionTextAndImages(blocks []ai.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extensionImageBlocks(blocks []ai.ContentBlock) []ai.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	images := make([]ai.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			images = append(images, block)
		}
	}
	return images
}

func extensionStreamingBehavior(value string) (StreamingBehavior, error) {
	switch strings.TrimSpace(value) {
	case "":
		return "", nil
	case "steer":
		return StreamingSteer, nil
	case "followUp", "follow_up", "follow-up":
		return StreamingFollowUp, nil
	default:
		return "", fmt.Errorf("unsupported sendUserMessage deliverAs: %s", value)
	}
}

func (a *AgentSession) extensionAppendEntry(params json.RawMessage) (json.RawMessage, error) {
	if a == nil || a.Session == nil {
		return nil, fmt.Errorf("agent session is not available")
	}
	var payload struct {
		CustomType string `json:"customType"`
		Type       string `json:"type"`
		Name       string `json:"name"`
		Data       any    `json:"data"`
	}
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, fmt.Errorf("invalid appendEntry payload: %w", err)
		}
	}
	customType := strings.TrimSpace(firstNonEmpty(payload.CustomType, payload.Type, payload.Name))
	if customType == "" {
		return nil, fmt.Errorf("appendEntry customType is required")
	}
	var data json.RawMessage
	if payload.Data != nil {
		encoded, err := marshalJSONNoHTMLEscape(payload.Data)
		if err != nil {
			return nil, err
		}
		data = json.RawMessage(encoded)
	}
	entry := SessionEntry{
		Type:       "custom",
		ID:         shortID(),
		CustomType: customType,
		Data:       data,
	}
	if err := a.Session.Append(entry); err != nil {
		return nil, err
	}
	return marshalExtensionContextActionResult(map[string]any{"ok": true, "entryId": entry.ID})
}

func (a *AgentSession) extensionSetSessionName(params json.RawMessage) (json.RawMessage, error) {
	if a == nil || a.Session == nil {
		return nil, fmt.Errorf("agent session is not available")
	}
	name, err := extensionStringParam(params, "name")
	if err != nil {
		return nil, err
	}
	if err := a.Session.AppendSessionName(strings.TrimSpace(name)); err != nil {
		return nil, err
	}
	a.emitSessionEvent(SessionInfoChangedEvent{Name: strings.TrimSpace(name)})
	return marshalExtensionContextActionResult(map[string]any{"ok": true})
}

func (a *AgentSession) extensionGetSessionName() (json.RawMessage, error) {
	if a == nil || a.Session == nil {
		return json.RawMessage("null"), nil
	}
	a.Session.mu.RLock()
	entries := append([]SessionEntry(nil), a.Session.Entries...)
	a.Session.mu.RUnlock()
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Type == "session_info" {
			name := strings.TrimSpace(entry.Name)
			if name == "" {
				return json.RawMessage("null"), nil
			}
			return marshalExtensionContextActionResult(name)
		}
	}
	return json.RawMessage("null"), nil
}

func (a *AgentSession) extensionSetLabel(params json.RawMessage) (json.RawMessage, error) {
	if a == nil || a.Session == nil {
		return nil, fmt.Errorf("agent session is not available")
	}
	var payload struct {
		EntryID  string `json:"entryId"`
		ID       string `json:"id"`
		TargetID string `json:"targetId"`
		Label    string `json:"label"`
	}
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, fmt.Errorf("invalid setLabel payload: %w", err)
		}
	}
	targetID := strings.TrimSpace(firstNonEmpty(payload.EntryID, payload.ID, payload.TargetID))
	if targetID == "" {
		return nil, fmt.Errorf("setLabel entryId is required")
	}
	if !a.Session.HasEntry(targetID) {
		return nil, fmt.Errorf("entry %s not found", targetID)
	}
	entry := SessionEntry{
		Type:     "label",
		ID:       shortID(),
		TargetID: targetID,
		Label:    payload.Label,
	}
	if err := a.Session.Append(entry); err != nil {
		return nil, err
	}
	return marshalExtensionContextActionResult(map[string]any{"ok": true, "entryId": entry.ID})
}

func extensionStringParam(params json.RawMessage, field string) (string, error) {
	if len(params) == 0 || string(params) == "null" {
		return "", nil
	}
	if params[0] == '"' {
		var text string
		if err := json.Unmarshal(params, &text); err != nil {
			return "", err
		}
		return text, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(params, &obj); err != nil {
		return "", err
	}
	value, ok := obj[field]
	if !ok {
		return "", nil
	}
	if value == nil {
		return "", nil
	}
	return fmt.Sprint(value), nil
}

func (a *AgentSession) extensionModelAuth(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var payload struct {
		Model ai.Model `json:"model"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &payload)
	}
	if payload.Model.Provider == "" || payload.Model.ID == "" {
		return json.RawMessage(`{"ok":false,"error":"model is required"}`), nil
	}
	if a.Registry == nil {
		return json.RawMessage(`{"ok":false,"error":"model registry is not available"}`), nil
	}
	apiKey, err := a.Registry.APIKey(ctx, payload.Model)
	if err != nil {
		raw, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		return raw, nil
	}
	out := map[string]any{"ok": true}
	if apiKey != "" {
		out["apiKey"] = apiKey
	}
	if len(payload.Model.Headers) > 0 {
		out["headers"] = payload.Model.Headers
	}
	return marshalExtensionContextActionResult(out)
}

func marshalExtensionContextActionResult(value any) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("null"), nil
	}
	raw, err := marshalJSONNoHTMLEscape(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
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

func (a *AgentSession) SetExtensionTriggerTurnHandler(handler func(context.Context) error) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.extensionTriggerTurnHandler = handler
	a.mu.Unlock()
}

func (a *AgentSession) SetExtensionUserMessageHandler(handler func(context.Context, SendUserMessageOptions) error) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.extensionUserMessageHandler = handler
	a.mu.Unlock()
}

// SetExtensionCustomMessageHandler registers a host callback invoked (on the
// extension request goroutine) when a display pi.sendMessage custom_message entry
// is appended, so an interactive host can render it into the live transcript.
func (a *AgentSession) SetExtensionCustomMessageHandler(handler func(customType string, content any, details any)) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.extensionCustomMessageHandler = handler
	a.mu.Unlock()
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
			a.extensionTriggerTurnHandler != nil ||
			a.extensionUserMessageHandler != nil ||
			a.extensionAbortHandler != nil ||
			a.extensionShutdownHandler != nil ||
			a.extensionErrorListener != nil
	case "ui", "ui_context", "input", "select", "confirm", "editor":
		return a.extensionUIContext != nil
	case "command", "command_context", "command_context_actions", "slash_command":
		return a.extensionCommandContextActions != nil
	case "trigger_turn", "send_message", "sendmessage":
		return a.extensionTriggerTurnHandler != nil
	case "send_user_message", "sendusermessage", "user_message":
		return a.extensionUserMessageHandler != nil
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
	theme, _ := ResolveTheme(a.Settings, resources)
	a.mu.Lock()
	a.Resources = resources
	a.SystemPrompt = resources.BuildSystemPrompt(args, ToolPromptInfoFor(a.Tools))
	a.Theme = theme
	if a.Keybindings != nil {
		a.Keybindings.Reload()
	}
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
	providerStop := a.extensionProviderStop
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
	a.extensionMode = "print"
	a.extensionTriggerTurnHandler = nil
	a.extensionUserMessageHandler = nil
	a.extensionAbortHandler = nil
	a.extensionShutdownHandler = nil
	a.extensionErrorListener = nil
	a.extensionErrorStop = nil
	a.extensionProviderStop = nil
	a.mu.Unlock()
	if providerStop != nil {
		providerStop()
	}
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
