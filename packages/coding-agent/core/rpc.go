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
}

type rpcUIBroker struct {
	mu      sync.Mutex
	nextID  uint64
	pending map[string]chan extensionUIResult
	writer  *rpcWriter
}

func newRPCUIBroker(writer *rpcWriter) *rpcUIBroker {
	return &rpcUIBroker{pending: map[string]chan extensionUIResult{}, writer: writer}
}

func (b *rpcUIBroker) handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if b == nil || b.writer == nil {
		return nil, fmt.Errorf("pi.ui.%s requires an RPC host, which is not available", method)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	b.nextID++
	uiID := fmt.Sprintf("ui-%d", b.nextID)
	var ch chan extensionUIResult
	if method != "notify" {
		ch = make(chan extensionUIResult, 1)
		b.pending[uiID] = ch
	}
	b.mu.Unlock()

	payload := map[string]any{
		"type":   "ui_request",
		"uiId":   uiID,
		"method": method,
		"params": json.RawMessage(params),
	}
	if len(params) == 0 {
		payload["params"] = map[string]any{}
	}
	b.writer.writeLine(payload)
	if method == "notify" {
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

func (b *rpcUIBroker) respond(uiID string, result json.RawMessage, err error) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	ch, ok := b.pending[uiID]
	if ok {
		delete(b.pending, uiID)
	}
	b.mu.Unlock()
	if !ok {
		return false
	}
	ch <- extensionUIResult{Result: result, Err: err}
	return true
}

func RunRPC(ctx context.Context, runtime *AgentSessionRuntime, in io.Reader, out io.Writer) error {
	current, err := runtimeAgent(runtime)
	if err != nil {
		return err
	}
	w := &rpcWriter{out: out}
	uiBroker := newRPCUIBroker(w)
	bindRuntimeExtensionUI(runtime, uiBroker.handle)
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
