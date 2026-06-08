package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/guanshan/pi-go/packages/ai"
)

// rpcWriter serializes all writes to the RPC output stream. Because prompts run
// on a background goroutine (so the read loop can keep processing steer/abort
// commands during streaming), the agent's event sink and the read loop's
// responses can race to write stdout; without serialization the interleaved
// JSON lines would be corrupted.
type rpcWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *rpcWriter) writeLine(value any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = writeRPCJSONLine(w.out, value)
}

// writeRPCJSONLine serializes a single RPC JSONL record without HTML-escaping
// `<`, `>`, `&`. TS `serializeJsonLine` is `${JSON.stringify(value)}\n`, which
// does not escape those characters; Go's default json.Marshal does, which would
// make the RPC wire bytes diverge for common code payloads (HTML, `&&`,
// `List<String>`). Scoped to the RPC output path only.
func writeRPCJSONLine(w io.Writer, value any) error {
	raw, err := marshalJSONStringifyLine(value)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func (w *rpcWriter) response(id any, command string, success bool, data any, errorMessage string) {
	resp := map[string]any{"type": "response", "command": command, "success": success}
	if id != nil {
		resp["id"] = id
	}
	if data != nil {
		resp["data"] = data
	}
	if errorMessage != "" {
		resp["error"] = errorMessage
	}
	w.writeLine(resp)
}

type rpcCommand struct {
	ID                 any               `json:"id,omitempty"`
	Type               string            `json:"type"`
	Message            string            `json:"message,omitempty"`
	Images             []ai.ContentBlock `json:"images,omitempty"`
	StreamingBehavior  string            `json:"streamingBehavior,omitempty"`
	ParentSession      string            `json:"parentSession,omitempty"`
	Session            string            `json:"session,omitempty"`
	SessionPath        string            `json:"sessionPath,omitempty"`
	Path               string            `json:"path,omitempty"`
	Cwd                string            `json:"cwd,omitempty"`
	Provider           string            `json:"provider,omitempty"`
	ModelID            string            `json:"modelId,omitempty"`
	Level              ai.ThinkingLevel  `json:"level,omitempty"`
	Mode               string            `json:"mode,omitempty"`
	CustomInstructions string            `json:"customInstructions,omitempty"`
	Enabled            *bool             `json:"enabled,omitempty"`
	Name               string            `json:"name,omitempty"`
	Command            string            `json:"command,omitempty"`
	ExcludeFromContext bool              `json:"excludeFromContext,omitempty"`
	OutputPath         string            `json:"outputPath,omitempty"`
	EntryID            string            `json:"entryId,omitempty"`
	UIID               string            `json:"uiId,omitempty"`
	Success            *bool             `json:"success,omitempty"`
	Data               json.RawMessage   `json:"data,omitempty"`
	Error              string            `json:"error,omitempty"`
	// Extension UI response fields (rpc-types.ts RpcExtensionUIResponse). The
	// host echoes the request id and exactly one of value/confirmed/cancelled.
	Value     string `json:"value,omitempty"`
	Confirmed *bool  `json:"confirmed,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

// rpcPendingUI tracks one in-flight extension UI request. The method is retained
// so an incoming extension_ui_response (which carries only value/confirmed/
// cancelled, per rpc-types.ts) can be mapped back to the result shape the
// extension (node bridge) expects: a string for select/input, a bool for
// confirm.
type rpcPendingUI struct {
	method string
	ch     chan extensionUIResult
}

type rpcUIBroker struct {
	mu      sync.Mutex
	nextID  uint64
	pending map[string]rpcPendingUI
	writer  *rpcWriter
}

func newRPCUIBroker(writer *rpcWriter) *rpcUIBroker {
	return &rpcUIBroker{pending: map[string]rpcPendingUI{}, writer: writer}
}

func (b *rpcUIBroker) handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if b == nil || b.writer == nil {
		return nil, fmt.Errorf("pi.ui.%s requires an RPC host, which is not available", method)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Lightweight ctx.ui.* methods that RPC mode cannot honor, mirroring TS
	// rpc-mode.ts: getEditorText cannot round-trip synchronously (returns ""),
	// and the working/thinking setters require TUI loader/render access (no-op).
	// These never reach the host, so they neither forward nor register a pending
	// response.
	switch method {
	case "getEditorText":
		return json.RawMessage(`""`), nil
	case "setWorkingMessage", "setWorkingVisible", "setWorkingIndicator",
		"setHiddenThinkingLabel", "onTerminalInput":
		return json.RawMessage("null"), nil
	}

	b.mu.Lock()
	b.nextID++
	uiID := fmt.Sprintf("ui-%d", b.nextID)
	needsResponse := rpcUINeedsResponse(method)
	var ch chan extensionUIResult
	if needsResponse {
		ch = make(chan extensionUIResult, 1)
		b.pending[uiID] = rpcPendingUI{method: method, ch: ch}
	}
	b.mu.Unlock()

	// Emit the TS host-facing shape (rpc-types.ts RpcExtensionUIRequest):
	// {type:"extension_ui_request", id, method, ...flattened method-specific
	// fields}. The internal node-bridge field names (message/choices/detail/
	// options/level) are remapped to the wire fields (title/options/placeholder/
	// notifyType) so the host sees the same protocol as TS.
	payload := flattenExtensionUIRequest(uiID, method, params)
	b.writer.writeLine(payload)
	if !needsResponse {
		// Fire-and-forget (notify/setStatus/setTitle/setEditorText/pasteToEditor):
		// the host acts on the request but sends no response.
		return json.RawMessage("null"), nil
	}

	select {
	case result := <-ch:
		return result.Result, result.Err
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, uiID)
		b.mu.Unlock()
		return nil, ctx.Err()
	}
}

// rpcUINeedsResponse reports which ctx.ui.* methods block on a host
// extension_ui_response. Everything else is fire-and-forget (TS rpc-mode.ts emits
// the request and moves on) or handled inline in handle().
func rpcUINeedsResponse(method string) bool {
	switch method {
	case "select", "confirm", "input", "editor":
		return true
	default:
		return false
	}
}

// resolveExtensionUIResponse maps a host-sent extension_ui_response (value /
// confirmed / cancelled, per rpc-types.ts) to the result the extension bridge
// expects for the originating method, then resolves the pending request. It
// returns false when the id is unknown.
func (b *rpcUIBroker) resolveExtensionUIResponse(id string, cmd rpcCommand) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	pending, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
	}
	b.mu.Unlock()
	if !ok {
		return false
	}
	result, err := mapExtensionUIResult(pending.method, cmd)
	pending.ch <- extensionUIResult{Result: result, Err: err}
	return true
}

// respond resolves a legacy ui_response carrying the raw result directly (the
// Go-only transition shape). The TS-faithful path is resolveExtensionUIResponse.
func (b *rpcUIBroker) respond(uiID string, result json.RawMessage, err error) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	pending, ok := b.pending[uiID]
	if ok {
		delete(b.pending, uiID)
	}
	b.mu.Unlock()
	if !ok {
		return false
	}
	pending.ch <- extensionUIResult{Result: result, Err: err}
	return true
}

// flattenExtensionUIRequest builds the host-facing extension_ui_request, mapping
// the internal node-bridge params (message/choices/detail/options/level) onto
// the TS wire fields (rpc-types.ts RpcExtensionUIRequest).
func flattenExtensionUIRequest(id, method string, params json.RawMessage) map[string]any {
	var p struct {
		Message   string          `json:"message"`
		Choices   []string        `json:"choices"`
		Detail    string          `json:"detail"`
		Level     string          `json:"level"`
		Key       string          `json:"key"`
		Text      *string         `json:"text"`
		Title     string          `json:"title"`
		Prefill   *string         `json:"prefill"`
		Lines     *[]string       `json:"lines"`
		Placement string          `json:"placement"`
		Options   json.RawMessage `json:"options"`
		Timeout   *int            `json:"timeout"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	out := map[string]any{"type": "extension_ui_request", "id": id, "method": method}
	switch method {
	case "select":
		out["title"] = p.Message
		out["options"] = p.Choices
		if p.Timeout != nil {
			out["timeout"] = *p.Timeout
		}
	case "confirm":
		out["title"] = p.Message
		out["message"] = p.Detail
		if p.Timeout != nil {
			out["timeout"] = *p.Timeout
		}
	case "input":
		out["title"] = p.Message
		if placeholder := extractInputPlaceholder(p.Options); placeholder != "" {
			out["placeholder"] = placeholder
		}
		if p.Timeout != nil {
			out["timeout"] = *p.Timeout
		}
	case "notify":
		out["message"] = p.Message
		if p.Level != "" {
			out["notifyType"] = p.Level
		}
	case "setStatus":
		out["statusKey"] = p.Key
		if p.Text != nil {
			out["statusText"] = *p.Text
		} else {
			out["statusText"] = nil
		}
	case "setTitle":
		out["title"] = p.Title
	case "setEditorText", "pasteToEditor":
		// TS rpc-mode emits set_editor_text for both (pasteToEditor falls back to
		// setEditorText in RPC mode, which has no bracketed-paste handling).
		out["method"] = "set_editor_text"
		if p.Text != nil {
			out["text"] = *p.Text
		} else {
			out["text"] = ""
		}
	case "editor":
		out["title"] = p.Title
		if p.Prefill != nil {
			out["prefill"] = *p.Prefill
		}
	case "setWidget":
		// TS rpc-mode forwards string[] widgets as widgetKey/widgetLines/
		// widgetPlacement (nil lines -> null, meaning remove).
		out["widgetKey"] = p.Key
		if p.Lines != nil {
			out["widgetLines"] = *p.Lines
		} else {
			out["widgetLines"] = nil
		}
		if p.Placement != "" {
			out["widgetPlacement"] = p.Placement
		}
	default:
		// Unknown methods forward their params verbatim so future host-facing
		// methods are not silently dropped.
		if len(params) > 0 {
			var extra map[string]any
			if json.Unmarshal(params, &extra) == nil {
				for k, v := range extra {
					out[k] = v
				}
			}
		}
	}
	return out
}

func extractInputPlaceholder(options json.RawMessage) string {
	if len(options) == 0 {
		return ""
	}
	var o struct {
		Placeholder string `json:"placeholder"`
	}
	_ = json.Unmarshal(options, &o)
	return o.Placeholder
}

// mapExtensionUIResult converts a host extension_ui_response into the result the
// extension bridge expects, mirroring the TS createDialogPromise parseResponse
// callbacks: select/input -> value (undefined when cancelled), confirm ->
// confirmed (false when cancelled).
func mapExtensionUIResult(method string, cmd rpcCommand) (json.RawMessage, error) {
	if cmd.Cancelled {
		switch method {
		case "confirm":
			return json.RawMessage("false"), nil
		default:
			return json.RawMessage("null"), nil
		}
	}
	switch method {
	case "confirm":
		if cmd.Confirmed != nil && *cmd.Confirmed {
			return json.RawMessage("true"), nil
		}
		return json.RawMessage("false"), nil
	default:
		// select / input resolve to the typed value string.
		raw, err := json.Marshal(cmd.Value)
		if err != nil {
			return json.RawMessage("null"), nil
		}
		return raw, nil
	}
}

func RunRPC(ctx context.Context, runtime *AgentSessionRuntime, in io.Reader, out io.Writer) error {
	current, err := runtimeAgent(runtime)
	if err != nil {
		return err
	}
	current.SetExtensionMode("rpc")
	w := &rpcWriter{out: out}
	uiBroker := newRPCUIBroker(w)
	bindRuntimeExtensionUI(runtime, uiBroker.handle, "rpc")
	defer clearRuntimeExtensionUI(runtime)
	w.writeLine(current.Session.Header)
	var wg sync.WaitGroup
	// Use a bufio.Reader (not bufio.Scanner) so a single RPC command line larger
	// than the previous 10MB scanner buffer cap does not abort the stream. TS
	// reads stdin line-by-line via readline with no size limit; large pasted
	// payloads (e.g. base64 images, big diffs) must survive intact.
	readErr := readJSONLLines(in, func(trimmed string) {
		var cmd rpcCommand
		if err := json.Unmarshal([]byte(trimmed), &cmd); err != nil {
			w.response(nil, "unknown", false, nil, err.Error())
		} else if err := handleRPCCommand(ctx, runtime, cmd, w, &wg, uiBroker); err != nil {
			w.response(cmd.ID, cmd.Type, false, nil, err.Error())
		}
	})
	// Wait for any in-flight prompt goroutines to finish so their events and
	// responses are flushed before we return / the stream closes.
	wg.Wait()
	if readErr == io.EOF {
		return nil
	}
	return readErr
}

// readJSONLLines reads newline-framed lines from in, stripping the trailing "\n"
// and any "\r" (CRLF) — on the EOF-partial final line as well as complete lines —
// and invokes onLine for each non-blank line. It returns the terminating read
// error (io.EOF on a clean end). This is the JSONL framing the RPC reader uses;
// keeping it in one place lets tests exercise the real framing rather than a copy.
func readJSONLLines(in io.Reader, onLine func(string)) error {
	reader := bufio.NewReader(in)
	for {
		line, readErr := reader.ReadString('\n')
		trimmed := strings.TrimSuffix(line, "\n")
		trimmed = strings.TrimSuffix(trimmed, "\r")
		if strings.TrimSpace(trimmed) != "" {
			onLine(trimmed)
		}
		if readErr != nil {
			return readErr
		}
	}
}

func handleRPCCommand(ctx context.Context, runtime *AgentSessionRuntime, cmd rpcCommand, w *rpcWriter, wg *sync.WaitGroup, uiBroker *rpcUIBroker) error {
	agent, err := runtimeAgent(runtime)
	if err != nil {
		return err
	}
	sink := func(event ai.Event) { w.writeLine(event) }
	switch cmd.Type {
	case "extension_ui_response":
		// TS host-facing response (rpc-types.ts RpcExtensionUIResponse):
		// {type:"extension_ui_response", id, value|confirmed|cancelled}. The id is
		// the request id echoed back; map it to the internal pending request and
		// translate value/confirmed/cancelled into the extension's result shape.
		id, _ := cmd.ID.(string)
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("id is required")
		}
		if !uiBroker.resolveExtensionUIResponse(id, cmd) {
			return fmt.Errorf("unknown extension UI id: %s", id)
		}
		return nil
	case "ui_response":
		if strings.TrimSpace(cmd.UIID) == "" {
			return fmt.Errorf("uiId is required")
		}
		success := true
		if cmd.Success != nil {
			success = *cmd.Success
		}
		result := cmd.Data
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		var responseErr error
		if !success || strings.TrimSpace(cmd.Error) != "" {
			responseErr = fmt.Errorf("%s", firstNonEmpty(strings.TrimSpace(cmd.Error), "ui_response failed"))
		}
		if !uiBroker.respond(cmd.UIID, result, responseErr) {
			return fmt.Errorf("unknown uiId: %s", cmd.UIID)
		}
		if cmd.ID != nil {
			w.response(cmd.ID, "ui_response", true, nil, "")
		}
		return nil
	case "prompt":
		if cmd.Message == "" {
			return fmt.Errorf("message is required")
		}
		// Dispatch the prompt on a background goroutine so the read loop keeps
		// processing steer/follow_up/abort commands while the agent streams.
		// The authoritative response is emitted from the preflight callback
		// (mirrors rpc-mode.ts), so a successfully started/queued prompt reports
		// success before the agent loop runs to completion.
		behavior := StreamingBehavior(cmd.StreamingBehavior)
		message := cmd.Message
		images := cmd.Images
		id := cmd.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			preflightSucceeded := false
			preflight := func(success bool) {
				if success {
					preflightSucceeded = true
					w.response(id, "prompt", true, nil, "")
				}
			}
			if err := agent.Send(ctx, message, images, behavior, preflight, sink); err != nil {
				if !preflightSucceeded {
					w.response(id, "prompt", false, nil, err.Error())
				}
			}
		}()
		return nil
	case "steer":
		if err := agent.Steer(ctx, cmd.Message, cmd.Images); err != nil {
			return err
		}
		w.response(cmd.ID, "steer", true, nil, "")
	case "follow_up":
		if err := agent.FollowUp(ctx, cmd.Message, cmd.Images); err != nil {
			return err
		}
		w.response(cmd.ID, "follow_up", true, nil, "")
	case "abort", "abort_retry":
		if cmd.Type == "abort" {
			if err := agent.Abort(ctx); err != nil {
				return err
			}
		} else {
			agent.AbortRetry()
		}
		w.response(cmd.ID, cmd.Type, true, nil, "")
	case "new_session":
		result, err := runtime.NewSession(ctx, NewSessionOptions{ParentSession: cmd.ParentSession})
		if err != nil {
			return err
		}
		w.response(cmd.ID, "new_session", true, map[string]any{"cancelled": result.Cancelled}, "")
	case "switch_session":
		sessionPath := firstNonEmpty(strings.TrimSpace(cmd.SessionPath), strings.TrimSpace(cmd.Session))
		if sessionPath == "" {
			return fmt.Errorf("session is required")
		}
		result, err := runtime.SwitchSession(ctx, sessionPath, SwitchSessionOptions{CwdOverride: cmd.Cwd})
		if err != nil {
			return err
		}
		w.response(cmd.ID, "switch_session", true, map[string]any{"cancelled": result.Cancelled}, "")
	case "import_session":
		if strings.TrimSpace(cmd.Path) == "" {
			return fmt.Errorf("path is required")
		}
		result, err := runtime.ImportFromJsonl(ctx, cmd.Path, cmd.Cwd)
		if err != nil {
			return err
		}
		w.response(cmd.ID, "import_session", true, map[string]any{"cancelled": result.Cancelled}, "")
	case "get_state":
		w.response(cmd.ID, "get_state", true, agent.State(), "")
	case "get_messages":
		w.response(cmd.ID, "get_messages", true, map[string]any{"messages": agent.Session.BuildContext().Messages}, "")
	case "set_model":
		model, err := agent.SetModel(cmd.Provider, cmd.ModelID)
		if err != nil {
			return err
		}
		w.response(cmd.ID, "set_model", true, model, "")
	case "cycle_model":
		data, _ := agent.CycleModel()
		w.response(cmd.ID, "cycle_model", true, data, "")
	case "get_available_models":
		w.response(cmd.ID, "get_available_models", true, map[string]any{"models": agent.Registry.AvailableConfigured()}, "")
	case "set_thinking_level":
		if err := agent.SetThinkingLevel(cmd.Level); err != nil {
			return err
		}
		w.response(cmd.ID, "set_thinking_level", true, nil, "")
	case "cycle_thinking_level":
		level, ok := agent.CycleThinkingLevel()
		var data any
		if ok {
			data = map[string]any{"level": level}
		}
		w.response(cmd.ID, "cycle_thinking_level", true, data, "")
	case "set_steering_mode":
		if cmd.Mode != "all" && cmd.Mode != "one-at-a-time" {
			return fmt.Errorf("invalid steering mode")
		}
		agent.SetSteeringMode(queueMode(cmd.Mode))
		w.response(cmd.ID, "set_steering_mode", true, nil, "")
	case "set_follow_up_mode":
		if cmd.Mode != "all" && cmd.Mode != "one-at-a-time" {
			return fmt.Errorf("invalid follow-up mode")
		}
		agent.SetFollowUpMode(queueMode(cmd.Mode))
		w.response(cmd.ID, "set_follow_up_mode", true, nil, "")
	case "compact":
		result, err := agent.CompactWithContext(ctx, cmd.CustomInstructions, sink)
		if err != nil {
			return err
		}
		w.response(cmd.ID, "compact", true, result, "")
	case "set_auto_compaction":
		if cmd.Enabled != nil {
			agent.SetAutoCompactionEnabled(*cmd.Enabled)
		}
		w.response(cmd.ID, "set_auto_compaction", true, nil, "")
	case "set_auto_retry":
		if cmd.Enabled != nil {
			agent.SetAutoRetryEnabled(*cmd.Enabled)
		}
		w.response(cmd.ID, "set_auto_retry", true, nil, "")
	case "set_session_name":
		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			return fmt.Errorf("session name cannot be empty")
		}
		if err := agent.SetSessionName(name); err != nil {
			return err
		}
		w.response(cmd.ID, "set_session_name", true, nil, "")
	case "bash":
		if strings.TrimSpace(cmd.Command) == "" {
			return fmt.Errorf("command is required")
		}
		result, err := agent.ExecuteBash(ctx, cmd.Command, BashRunOptions{ExcludeFromContext: cmd.ExcludeFromContext})
		if err != nil {
			return err
		}
		w.response(cmd.ID, "bash", true, result, "")
	case "abort_bash":
		agent.AbortBash()
		w.response(cmd.ID, "abort_bash", true, nil, "")
	case "get_session_stats":
		w.response(cmd.ID, "get_session_stats", true, agent.GetSessionStats(), "")
	case "export_html":
		path, err := agent.ExportToHTML(ctx, cmd.OutputPath)
		if err != nil {
			return err
		}
		w.response(cmd.ID, "export_html", true, map[string]any{"path": path}, "")
	case "fork":
		if strings.TrimSpace(cmd.EntryID) == "" {
			return fmt.Errorf("entryId is required")
		}
		result, err := runtime.Fork(ctx, cmd.EntryID, ForkOptions{})
		if err != nil {
			return err
		}
		w.response(cmd.ID, "fork", true, map[string]any{"text": result.SelectedText, "cancelled": result.Cancelled}, "")
	case "clone":
		leafID := agent.currentLeaf()
		if leafID == "" {
			return fmt.Errorf("cannot clone session: no current entry selected")
		}
		result, err := runtime.Fork(ctx, leafID, ForkOptions{Position: ForkPositionAt})
		if err != nil {
			return err
		}
		w.response(cmd.ID, "clone", true, map[string]any{"cancelled": result.Cancelled}, "")
	case "get_fork_messages":
		messages := agent.GetUserMessagesForForking()
		out := make([]map[string]any, 0, len(messages))
		for _, m := range messages {
			out = append(out, map[string]any{"entryId": m.EntryID, "text": m.Text})
		}
		w.response(cmd.ID, "get_fork_messages", true, map[string]any{"messages": out}, "")
	case "get_last_assistant_text":
		w.response(cmd.ID, "get_last_assistant_text", true, map[string]any{"text": agent.GetLastAssistantText()}, "")
	case "get_commands":
		w.response(cmd.ID, "get_commands", true, map[string]any{"commands": agent.rpcSlashCommands()}, "")
	default:
		return fmt.Errorf("unknown RPC command: %s", cmd.Type)
	}
	return nil
}

// rpcSlashCommands collects the commands invocable via prompt (extension
// commands, prompt templates, and skills), mirroring the get_commands handler
// in src/modes/rpc/rpc-mode.ts.
func (a *AgentSession) rpcSlashCommands() []map[string]any {
	commands := []map[string]any{}
	if a.extensionRuntime != nil {
		for _, cmd := range a.extensionRuntime.RegisteredCommands() {
			commands = append(commands, map[string]any{
				"name":        cmd.Name,
				"description": cmd.Description,
				"source":      "extension",
			})
		}
	}
	for _, template := range a.Resources.PromptTemplates {
		commands = append(commands, map[string]any{
			"name":       template.Name,
			"source":     "prompt",
			"sourceInfo": map[string]any{"path": template.Path},
		})
	}
	for _, skill := range a.Resources.Skills {
		commands = append(commands, map[string]any{
			"name":        "skill:" + skill.Name,
			"description": skill.Description,
			"source":      "skill",
			"sourceInfo":  map[string]any{"path": skill.Path},
		})
	}
	return commands
}
