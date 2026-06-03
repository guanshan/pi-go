package extensions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/guanshan/pi-go/packages/ai"
)

type scriptToolMetadata struct {
	Name        string         `json:"name"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type scriptCommandMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type scriptFlagMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Default     any    `json:"default"`
}

type scriptReadyMessage struct {
	Type     string                  `json:"type"`
	Tools    []scriptToolMetadata    `json:"tools"`
	Commands []scriptCommandMetadata `json:"commands"`
	Flags    []scriptFlagMetadata    `json:"flags"`
	Events   []string                `json:"events"`
	Error    string                  `json:"error"`
}

type scriptResponseMessage struct {
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

type scriptRuntime struct {
	path   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *syncBuffer
	nextID int64

	// writeMu serializes concurrent stdin writes (each request emits one JSON
	// line). It is held only for the duration of the write, never across the
	// blocking wait for a response, so a slow/blocked extension never serializes
	// unrelated requests or blocks cancellation.
	writeMu sync.Mutex

	// A single background reader goroutine (readLoop) owns the scanner and
	// dispatches each response to the per-request channel registered in pending,
	// keyed by request id. request() selects on {response, ctx.Done(), readDone}
	// so a cancelled context unblocks it without depending on the extension.
	pendingMu sync.Mutex
	pending   map[int64]chan scriptResponseMessage
	readDone  chan struct{}
	readErr   error // protected by pendingMu; set once before readDone closes

	// ctx is the session-scoped context; cancel tears down the runtime so the
	// event callback and any in-flight requests stop after cancellation.
	ctx    context.Context
	cancel context.CancelFunc

	// uiHandler resolves the host's server-initiated UI request handler at request
	// time (so a handler bound after load — e.g. by the TUI — is still seen). nil
	// when the host wired none.
	uiHandler func() UIRequestHandler

	// hasUI* carry the latest ctx.hasUI capability to hasUIWriteLoop without
	// blocking the caller: sendSetHasUI records the seq-stamped state under
	// hasUIMu and wakes the loop via the buffered(1) hasUIWake; see ui_bridge.go.
	hasUIMu      sync.Mutex
	hasUISeq     uint64
	hasUIPending bool
	hasUIWake    chan struct{}
}

func LoadScriptExtensions(ctx context.Context, api *API, paths []string, flagValues map[string]any) []error {
	if api == nil || len(paths) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var errs []error
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := loadScriptExtension(ctx, api, path, flagValues); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func loadScriptExtension(ctx context.Context, api *API, path string, flagValues map[string]any) error {
	// ctx.hasUI mirrors whether a UI handler is bound (TS model). It may be bound
	// before load (reflected in the spawn env) or after (pushed via set_has_ui).
	runtime, ready, err := startScriptRuntime(ctx, path, flagValues, api.UIHandler() != nil, api.UIHandler)
	if err != nil {
		return err
	}
	// Forward later handler binds/unbinds so the extension's ctx.hasUI stays live.
	api.registerUIListener(runtime.sendSetHasUI)
	for _, flag := range ready.Flags {
		if flag.Name == "" {
			continue
		}
		// Declared so the host can surface it in --help; the flag's value is
		// resolved inside the script runtime (seeded from flagValues at spawn).
		// scriptFlagMetadata is layout-identical to FlagDefinition by design.
		api.RegisterFlag(FlagDefinition(flag))
	}
	for _, tool := range ready.Tools {
		tool := tool
		if tool.Name == "" {
			continue
		}
		api.RegisterTool(ToolDefinition{
			Name:        tool.Name,
			Label:       firstNonEmpty(tool.Label, tool.Name),
			Description: tool.Description,
			Parameters:  tool.Parameters,
			Execute: func(ctx context.Context, raw []byte) (ai.ToolResult, error) {
				return runtime.ExecuteTool(ctx, tool.Name, raw)
			},
		})
	}
	for _, command := range ready.Commands {
		command := command
		if command.Name != "" {
			api.RegisterCommandHandler(command.Name, command.Description, func(ctx context.Context, args string) (string, error) {
				return runtime.ExecuteCommand(ctx, command.Name, args)
			})
		}
	}
	for _, event := range ready.Events {
		event := event
		if strings.TrimSpace(event) == "" {
			continue
		}
		api.On(event, func(payload any) {
			// Use the session-scoped context (not context.Background) so that once
			// the runtime is cancelled/shut down, the event callback declines fast
			// instead of dispatching to a torn-down extension process.
			if runtime.ctx.Err() != nil {
				return
			}
			result, err := runtime.Emit(runtime.ctx, event, payload)
			if err == nil {
				applyScriptEventResult(event, payload, result)
			}
		})
	}
	api.OnShutdown(runtime.Shutdown)
	return nil
}

func startScriptRuntime(ctx context.Context, path string, flagValues map[string]any, hasUI bool, uiHandler func() UIRequestHandler) (*scriptRuntime, scriptReadyMessage, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: node executable is required to load script extensions", path)
	}
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "--eval", scriptRuntimeBridge, "--", path)
	cmd.Dir = filepath.Dir(path)
	env := os.Environ()
	// Seed extension CLI flag values before the factory runs so getFlag resolves
	// command-line values (the host does not yet know which flags the extension
	// declares, so it forwards all unknown flags; the bridge gates by name).
	if len(flagValues) > 0 {
		if encoded, err := json.Marshal(flagValues); err == nil {
			env = append(env, "PI_EXTENSION_FLAG_VALUES="+string(encoded))
		}
	}
	// Tell the bridge whether the host can answer ctx.ui requests so ctx.hasUI is
	// accurate (UI-gated extensions check it before prompting).
	if hasUI {
		env = append(env, "PI_EXTENSION_HAS_UI=1")
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: %w", path, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: %w", path, err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: %w", path, err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	if !scanner.Scan() {
		_ = cmd.Wait()
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: extension loader exited before ready%s", path, scriptStderrSuffix(stderr))
	}
	var ready scriptReadyMessage
	if err := json.Unmarshal(scanner.Bytes(), &ready); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: invalid extension loader response: %w", path, err)
	}
	if ready.Type == "error" || ready.Error != "" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, scriptReadyMessage{}, fmt.Errorf("%s: %s%s", path, firstNonEmpty(ready.Error, "extension failed to load"), scriptStderrSuffix(stderr))
	}
	// Derive a session-scoped context so cancelling the parent (or Shutdown)
	// propagates to in-flight requests and the event callback.
	runtimeCtx, cancel := context.WithCancel(ctx)
	r := &scriptRuntime{
		path:      path,
		cmd:       cmd,
		stdin:     stdin,
		stderr:    stderr,
		pending:     make(map[int64]chan scriptResponseMessage),
		readDone:    make(chan struct{}),
		ctx:         runtimeCtx,
		cancel:      cancel,
		uiHandler:   uiHandler,
		hasUIWake:   make(chan struct{}, 1),
	}
	// uiHandler is set above, before the reader goroutine starts, so the
	// readLoop-spawned handleUIRequest never races the assignment.
	// The ready message was already consumed above; the reader goroutine takes
	// over the scanner for all subsequent response lines.
	go r.readLoop(scanner)
	// Dedicated writer for set_has_ui frames so late handler binds never block the
	// host; exits when runtimeCtx is cancelled (Shutdown).
	go r.hasUIWriteLoop()
	return r, ready, nil
}

// readLoop is the single owner of the stdout scanner. It dispatches each
// response to the per-request channel keyed by id, and on EOF/scan error records
// the terminal error and wakes every pending request (and future ones) so none
// block forever on a dead process.
func (r *scriptRuntime) readLoop(scanner *bufio.Scanner) {
	for scanner.Scan() {
		line := scanner.Bytes()
		// A server-initiated UI request from the extension (ctx.ui.*) carries
		// type:"ui_request" and a string uiId, distinct from the integer-id
		// responses to host-initiated requests. Dispatch it on its own goroutine so
		// a handler that blocks on interactive input never stalls this reader.
		var probe struct {
			Type string `json:"type"`
			UIID string `json:"uiId"`
		}
		if json.Unmarshal(line, &probe) == nil && probe.Type == "ui_request" {
			var req uiRequestMessage
			if err := json.Unmarshal(line, &req); err != nil {
				// Improbable (the probe parsed): reject so the extension's awaiting
				// ctx.ui promise doesn't hang forever on a dropped request.
				go r.writeUIResponse(uiResponseMessage{Type: "ui_response", UIID: probe.UIID, Error: "malformed ui_request"})
			} else {
				go r.handleUIRequest(req)
			}
			continue
		}
		var response scriptResponseMessage
		if err := json.Unmarshal(line, &response); err != nil {
			// A malformed line is not addressable to a request id; skip it rather
			// than tear down the runtime (mirrors ignoring non-response chatter).
			continue
		}
		r.pendingMu.Lock()
		ch, ok := r.pending[response.ID]
		if ok {
			delete(r.pending, response.ID)
		}
		r.pendingMu.Unlock()
		if ok {
			ch <- response
		}
	}
	loopErr := scanner.Err()
	if loopErr == nil {
		loopErr = io.EOF
	}
	r.pendingMu.Lock()
	r.readErr = loopErr
	close(r.readDone)
	r.pendingMu.Unlock()
}

func (r *scriptRuntime) ExecuteTool(ctx context.Context, toolName string, raw []byte) (ai.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var params json.RawMessage
	if len(raw) > 0 {
		params = append(json.RawMessage(nil), raw...)
	} else {
		params = json.RawMessage(`{}`)
	}
	response, err := r.request(ctx, map[string]any{
		"type":     "execute_tool",
		"toolName": toolName,
		"params":   params,
	})
	if err != nil {
		return ai.ToolResult{}, err
	}
	var result ai.ToolResult
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return result, nil
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return ai.ToolResult{}, fmt.Errorf("%s: invalid tool result for %s: %w", r.path, toolName, err)
	}
	return result, nil
}

func (r *scriptRuntime) ExecuteCommand(ctx context.Context, commandName, args string) (string, error) {
	response, err := r.request(ctx, map[string]any{
		"type":        "execute_command",
		"commandName": commandName,
		"args":        args,
	})
	if err != nil {
		return "", err
	}
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(response.Result, &text); err == nil {
		return text, nil
	}
	return strings.TrimSpace(string(response.Result)), nil
}

func (r *scriptRuntime) Emit(ctx context.Context, event string, payload any) (json.RawMessage, error) {
	response, err := r.request(ctx, map[string]any{
		"type":    "emit",
		"event":   event,
		"payload": payload,
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func applyScriptEventResult(event string, payload any, result json.RawMessage) {
	if payload == nil || len(result) == 0 || string(result) == "null" {
		return
	}
	switch normalizeEventKey(event) {
	// session_before_* hooks carry their decision (cancel/result) back in the
	// payload; tool_call/tool_result carry the handler's block/mutation/override so
	// the BeforeToolCall/AfterToolCall hooks can apply them to the execution chain.
	case "session_before_switch", "session_before_fork", "session_before_compact", "session_before_tree",
		"tool_call", "tool_result":
	default:
		return
	}
	_ = json.Unmarshal(result, payload)
}

func (r *scriptRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	_, err := r.request(ctx, map[string]any{"type": "shutdown"})
	// Cancel the session context so the event callback and any in-flight requests
	// stop after shutdown, then tear down the process and reader goroutine.
	if r.cancel != nil {
		r.cancel()
	}
	_ = r.stdin.Close()
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	if r.cmd != nil {
		_ = r.cmd.Wait()
	}
	return err
}

func (r *scriptRuntime) request(ctx context.Context, payload map[string]any) (scriptResponseMessage, error) {
	if r == nil {
		return scriptResponseMessage{}, fmt.Errorf("script extension runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Honor both the caller's context and the runtime's session context, so
	// cancelling either unblocks the request.
	if err := ctx.Err(); err != nil {
		return scriptResponseMessage{}, err
	}
	if r.ctx != nil {
		if err := r.ctx.Err(); err != nil {
			return scriptResponseMessage{}, err
		}
	}

	id := atomic.AddInt64(&r.nextID, 1)
	payload["id"] = id
	line, err := json.Marshal(payload)
	if err != nil {
		return scriptResponseMessage{}, err
	}

	// Register the response channel BEFORE writing, so a fast reply cannot race
	// ahead of the registration. Buffered so the reader never blocks delivering.
	respCh := make(chan scriptResponseMessage, 1)
	r.pendingMu.Lock()
	// If the reader already terminated, fail fast instead of blocking forever.
	if r.readErr != nil {
		readErr := r.readErr
		r.pendingMu.Unlock()
		return scriptResponseMessage{}, fmt.Errorf("%s: extension runtime stopped: %w%s", r.path, readErr, scriptStderrSuffix(r.stderr))
	}
	r.pending[id] = respCh
	r.pendingMu.Unlock()

	cleanup := func() {
		r.pendingMu.Lock()
		delete(r.pending, id)
		r.pendingMu.Unlock()
	}

	// Serialize only the stdin write; never hold the lock across the wait below.
	r.writeMu.Lock()
	_, writeErr := r.stdin.Write(append(line, '\n'))
	r.writeMu.Unlock()
	if writeErr != nil {
		cleanup()
		return scriptResponseMessage{}, fmt.Errorf("%s: failed to write extension request: %w%s", r.path, writeErr, scriptStderrSuffix(r.stderr))
	}

	rctxDone := func() <-chan struct{} {
		if r.ctx == nil {
			return nil
		}
		return r.ctx.Done()
	}()

	select {
	case response := <-respCh:
		if response.ID != id {
			return scriptResponseMessage{}, fmt.Errorf("%s: extension response id mismatch: got %d want %d", r.path, response.ID, id)
		}
		if !response.OK {
			return scriptResponseMessage{}, fmt.Errorf("%s: %s%s", r.path, firstNonEmpty(response.Error, "extension request failed"), scriptStderrSuffix(r.stderr))
		}
		return response, nil
	case <-ctx.Done():
		cleanup()
		return scriptResponseMessage{}, ctx.Err()
	case <-rctxDone:
		cleanup()
		return scriptResponseMessage{}, r.ctx.Err()
	case <-r.readDone:
		cleanup()
		r.pendingMu.Lock()
		readErr := r.readErr
		r.pendingMu.Unlock()
		if readErr == nil {
			readErr = io.EOF
		}
		return scriptResponseMessage{}, fmt.Errorf("%s: extension runtime stopped: %w%s", r.path, readErr, scriptStderrSuffix(r.stderr))
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer wrapper. os/exec's stderr copy
// goroutine writes to cmd.Stderr while the process runs, while request() and
// scriptStderrSuffix read it concurrently to attach stderr context to errors;
// the mutex prevents a data race between those reads and the copy goroutine's
// writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func scriptStderrSuffix(stderr *syncBuffer) string {
	if stderr == nil || stderr.Len() == 0 {
		return ""
	}
	text := strings.TrimSpace(stderr.String())
	if text == "" {
		return ""
	}
	if len(text) > 4096 {
		text = text[len(text)-4096:]
	}
	return ": " + text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

const scriptRuntimeBridge = `
import { registerHooks } from "node:module";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
import { inspect } from "node:util";

const extensionPath = process.argv[1];
const tools = new Map();
const commands = new Map();
const flags = new Map();
const eventHandlers = new Map();
const shutdownHandlers = [];
let flagValues = {};
try {
	flagValues = JSON.parse(process.env.PI_EXTENSION_FLAG_VALUES || "{}") || {};
} catch {
	flagValues = {};
}

for (const level of ["log", "info", "warn", "error"]) {
	console[level] = (...args) => {
		process.stderr.write(args.map((arg) => typeof arg === "string" ? arg : inspect(arg, { depth: 4 })).join(" ") + "\n");
	};
}

function write(message) {
	process.stdout.write(JSON.stringify(message) + "\n");
}

// Server-initiated UI requests (ctx.ui.*): emit a ui_request keyed by a string
// uiId and resolve when the host writes back the matching ui_response. The uiId
// namespace is disjoint from the integer ids the host uses for execute_* requests.
let uiRequestSeq = 0;
const uiPending = new Map();
// hasUIState tracks whether the host has a UI handler bound. Seeded at spawn and
// updated live via set_has_ui, so ctx.hasUI reflects late handler binding.
let hasUIState = process.env.PI_EXTENSION_HAS_UI === "1";
function sendUIRequest(method, params) {
	return new Promise((resolve, reject) => {
		const uiId = "ui-" + (++uiRequestSeq);
		uiPending.set(uiId, { resolve, reject });
		write({ type: "ui_request", uiId, method, params: params ?? {} });
	});
}

function typeModuleSource() {
	return [
		"const optionalKey = \"__piOptional\";",
		"function clean(schema) {",
		"  if (!schema || typeof schema !== \"object\") return schema;",
		"  const out = { ...schema };",
		"  delete out[optionalKey];",
		"  return out;",
		"}",
		"export const Type = {",
		"  Object(properties = {}, options = {}) {",
		"    const required = [];",
		"    const cleaned = {};",
		"    for (const [key, schema] of Object.entries(properties ?? {})) {",
		"      if (!schema || schema[optionalKey] !== true) required.push(key);",
		"      cleaned[key] = clean(schema);",
		"    }",
		"    const result = { type: \"object\", properties: cleaned, additionalProperties: false, ...options };",
		"    if (required.length > 0) result.required = required;",
		"    return result;",
		"  },",
		"  String(options = {}) { return { type: \"string\", ...options }; },",
		"  Number(options = {}) { return { type: \"number\", ...options }; },",
		"  Integer(options = {}) { return { type: \"integer\", ...options }; },",
		"  Boolean(options = {}) { return { type: \"boolean\", ...options }; },",
		"  Array(items = {}, options = {}) { return { type: \"array\", items, ...options }; },",
		"  Optional(schema = {}) { return { ...schema, [optionalKey]: true }; },",
		"  Literal(value, options = {}) { return { const: value, ...options }; },",
		"  Union(anyOf = [], options = {}) { return { anyOf, ...options }; },",
		"  Record(keySchema = {}, valueSchema = {}, options = {}) { return { type: \"object\", additionalProperties: valueSchema, ...options }; },",
		"  Any(options = {}) { return { ...options }; },",
		"  Unknown(options = {}) { return { ...options }; },",
		"  Null(options = {}) { return { type: \"null\", ...options }; },",
		"};",
		"export function StringEnum(values, options = {}) {",
		"  return { type: \"string\", enum: Array.from(values ?? []), ...options };",
		"}",
		"export default { Type, StringEnum };",
	].join("\n");
}

const virtualModules = new Map([
	["pi-virtual:ai", typeModuleSource()],
	["pi-virtual:typebox", typeModuleSource()],
	["pi-virtual:coding-agent", [
		"export function defineTool(definition) { return definition; }",
		"export function createEventBus() {",
		"  const listeners = new Map();",
		"  return {",
		"    on(event, listener) {",
		"      const list = listeners.get(event) ?? [];",
		"      list.push(listener);",
		"      listeners.set(event, list);",
		"      return () => listeners.set(event, (listeners.get(event) ?? []).filter((item) => item !== listener));",
		"    },",
		"    emit(event, payload) {",
		"      for (const listener of listeners.get(event) ?? []) listener(payload);",
		"    },",
		"  };",
		"}",
		"export default { defineTool, createEventBus };",
	].join("\n")],
	["pi-virtual:tui", [
		"export function matchesKey(data, key) { return data === key; }",
		"export function truncateToWidth(value, width) { const text = String(value ?? \"\"); return text.length > width ? text.slice(0, Math.max(0, width)) : text; }",
		"export class Text { constructor(text = \"\") { this.text = text; } render() { return [String(this.text)]; } }",
		"export class Container { constructor(children = []) { this.children = children; } render(width) { return this.children.flatMap((child) => typeof child?.render === \"function\" ? child.render(width) : [String(child ?? \"\")]); } }",
		"export class Box extends Container {}",
		"export class Spacer { render() { return [\"\"]; } }",
		"export class Input {}",
		"export class SelectList {}",
		"export class SettingsList {}",
		"export class Loader {}",
		"export class CancellableLoader {}",
		"export class Markdown { constructor(markdown = \"\") { this.markdown = markdown; } render() { return String(this.markdown).split(\"\\n\"); } }",
		// Key mirrors @earendil-works/pi-tui's Key: named key constants plus modifier
		// helpers (e.g. Key.ctrlAlt('p') -> 'ctrl+alt+p'). The backtick key uses the
		// \\u0060 escape because the surrounding bridge is a Go raw string literal.
		"export const Key = (() => {",
		"  const k = { escape: \"escape\", esc: \"esc\", enter: \"enter\", return: \"return\", tab: \"tab\", space: \"space\", backspace: \"backspace\", delete: \"delete\", insert: \"insert\", clear: \"clear\", home: \"home\", end: \"end\", pageUp: \"pageUp\", pageDown: \"pageDown\", up: \"up\", down: \"down\", left: \"left\", right: \"right\", f1: \"f1\", f2: \"f2\", f3: \"f3\", f4: \"f4\", f5: \"f5\", f6: \"f6\", f7: \"f7\", f8: \"f8\", f9: \"f9\", f10: \"f10\", f11: \"f11\", f12: \"f12\" };",
		"  Object.assign(k, { backtick: \"\\u0060\", hyphen: \"-\", equals: \"=\", leftbracket: \"[\", rightbracket: \"]\", backslash: \"\\\\\", semicolon: \";\", quote: \"'\", comma: \",\", period: \".\", slash: \"/\", exclamation: \"!\", at: \"@\", hash: \"#\", dollar: \"$\", percent: \"%\", caret: \"^\", ampersand: \"&\", asterisk: \"*\", leftparen: \"(\", rightparen: \")\", underscore: \"_\", plus: \"+\", pipe: \"|\", tilde: \"~\", leftbrace: \"{\", rightbrace: \"}\", colon: \":\", lessthan: \"<\", greaterthan: \">\", question: \"?\" });",
		"  const mods = { ctrl: \"ctrl\", shift: \"shift\", alt: \"alt\", super: \"super\", ctrlShift: \"ctrl+shift\", shiftCtrl: \"shift+ctrl\", ctrlAlt: \"ctrl+alt\", altCtrl: \"alt+ctrl\", shiftAlt: \"shift+alt\", altShift: \"alt+shift\", ctrlSuper: \"ctrl+super\", superCtrl: \"super+ctrl\", shiftSuper: \"shift+super\", superShift: \"super+shift\", altSuper: \"alt+super\", superAlt: \"super+alt\", ctrlShiftAlt: \"ctrl+shift+alt\", ctrlShiftSuper: \"ctrl+shift+super\" };",
		"  for (const name of Object.keys(mods)) { const prefix = mods[name]; k[name] = (key) => prefix + \"+\" + key; }",
		"  return k;",
		"})();",
		"export default { matchesKey, truncateToWidth, Text, Container, Box, Spacer, Input, SelectList, SettingsList, Loader, CancellableLoader, Markdown, Key };",
	].join("\n")],
]);

function virtualURL(specifier) {
	if (specifier === "@earendil-works/pi-ai" || specifier.startsWith("@earendil-works/pi-ai/")) return "pi-virtual:ai";
	if (specifier === "typebox") return "pi-virtual:typebox";
	if (specifier === "@earendil-works/pi-coding-agent" || specifier.startsWith("@earendil-works/pi-coding-agent/")) return "pi-virtual:coding-agent";
	if (specifier === "@earendil-works/pi-tui" || specifier.startsWith("@earendil-works/pi-tui/")) return "pi-virtual:tui";
	return "";
}

if (typeof registerHooks !== "function") {
	throw new Error("Node.js module.registerHooks is required for script extension loading");
}

registerHooks({
	resolve(specifier, context, nextResolve) {
		const url = virtualURL(specifier);
		if (url) return { url, shortCircuit: true };
		return nextResolve(specifier, context);
	},
	load(url, context, nextLoad) {
		if (virtualModules.has(url)) {
			return { format: "module", source: virtualModules.get(url), shortCircuit: true };
		}
		return nextLoad(url, context);
	},
});

function normalizeToolResult(result) {
	if (result == null) return { content: [], isError: false };
	if (typeof result === "string") return { content: [{ type: "text", text: result }], isError: false };
	const out = { ...result };
	if (typeof out.content === "string") out.content = [{ type: "text", text: out.content }];
	if (!Array.isArray(out.content)) out.content = [];
	out.isError = Boolean(out.isError);
	return out;
}

const api = {
	registerTool(definition) {
		if (!definition?.name) throw new Error("Extension tool is missing a name");
		tools.set(definition.name, definition);
	},
	registerCommand(name, info = {}) {
		if (!name) throw new Error("Extension command is missing a name");
		let description = "";
		let handler;
		if (typeof info === "string") {
			description = info;
		} else if (typeof info === "function") {
			handler = info;
		} else {
			description = info.description ?? "";
			handler = info.handler ?? info.execute ?? info.run;
		}
		commands.set(name, { name, description, handler: typeof handler === "function" ? handler : undefined });
	},
	registerFlag(name, options = {}) {
		if (!name) throw new Error("Extension flag is missing a name");
		const type = options.type === "string" ? "string" : "boolean";
		flags.set(name, { name, description: options.description ?? "", type, default: options.default });
		if (!(name in flagValues) && options.default !== undefined) {
			flagValues[name] = options.default;
		}
	},
	getFlag(name) {
		if (!flags.has(name)) return undefined;
		return flagValues[name];
	},
	on(event, handler) {
		const key = String(event ?? "").trim();
		if (!key || typeof handler !== "function") return () => {};
		const list = eventHandlers.get(key) ?? [];
		list.push(handler);
		eventHandlers.set(key, list);
		return () => eventHandlers.set(key, (eventHandlers.get(key) ?? []).filter((item) => item !== handler));
	},
	onShutdown(handler) {
		if (typeof handler === "function") shutdownHandlers.push(handler);
	},
	// Unsupported registration APIs degrade gracefully: warn and skip rather than
	// throw at load time, so the rest of an extension (tools/commands/flags/hooks)
	// still loads instead of the whole extension failing. The warning goes to
	// stderr via the console shim above.
	registerProvider() {
		console.warn("pi.registerProvider is not supported in the Go bridge; skipping (custom providers are unavailable).");
	},
	unregisterProvider() {
		console.warn("pi.unregisterProvider is not supported in the Go bridge; skipping.");
	},
	registerMessageRenderer() {
		console.warn("pi.registerMessageRenderer is not supported in the Go bridge; skipping (custom message renderers are unavailable).");
	},
	addAutocompleteProvider() {
		console.warn("pi.addAutocompleteProvider is not supported in the Go bridge; skipping (custom autocomplete is unavailable).");
	},
	registerShortcut() {
		console.warn("pi.registerShortcut is not supported in the Go bridge; skipping (interactive keybindings are not bridged).");
	},
	unregisterShortcut() {
		console.warn("pi.unregisterShortcut is not supported in the Go bridge; skipping (interactive keybindings are not bridged).");
	},
	events: {
		on(event, handler) { return api.on(event, handler); },
		emit() {},
	},
	// ctx.ui requests are routed to the host over the bridge (ui_request ->
	// ui_response). When the host bound no handler (truly headless) the host
	// replies with an error and these reject, so a UI-gated extension fails loudly
	// instead of silently taking the wrong branch. Signatures mirror TS:
	// notify(message, level), select(message, choices[]) -> choice,
	// confirm(message, detail) -> boolean, input(message, options) -> string.
	ui: {
		notify(message, level) { return sendUIRequest("notify", { message: String(message ?? ""), level: level ?? "info" }).catch(() => { process.stderr.write(String(message ?? "") + "\n"); }); },
		select(message, choices) { return sendUIRequest("select", { message: String(message ?? ""), choices: Array.isArray(choices) ? choices : [] }); },
		confirm(message, detail) { return sendUIRequest("confirm", { message: String(message ?? ""), detail: detail == null ? "" : String(detail) }); },
		input(message, options) { return sendUIRequest("input", { message: String(message ?? ""), options: options ?? {} }); },
		custom() {
			console.warn("pi.ui.custom is not supported in the Go bridge; skipping (custom overlays are unavailable).");
			return Promise.resolve(undefined);
		},
	},
};

function extensionContext() {
	return {
		hasUI: hasUIState,
		ui: api.ui,
		sessionManager: { getBranch() { return []; } },
	};
}

try {
	if (!extensionPath) throw new Error("extension path is required");
	const mod = await import(pathToFileURL(extensionPath).href);
	const factory = mod.default ?? mod;
	if (typeof factory !== "function") throw new Error("extension default export must be a function");
	await factory(api);
	write({
		type: "ready",
		tools: Array.from(tools.values()).map((tool) => ({
			name: tool.name,
			label: tool.label ?? tool.name,
			description: tool.description ?? "",
			parameters: tool.parameters ?? { type: "object", properties: {} },
		})),
		commands: Array.from(commands.values()).map((command) => ({ name: command.name, description: command.description ?? "" })),
		flags: Array.from(flags.values()),
		events: Array.from(eventHandlers.keys()),
	});
} catch (error) {
	write({ type: "error", error: error?.stack ?? error?.message ?? String(error) });
	process.exit(1);
}

const rl = createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on("line", async (line) => {
	let request;
		try {
			request = JSON.parse(line);
			if (request.type === "set_has_ui") {
				hasUIState = request.value === true;
				return;
			}
			if (request.type === "ui_response") {
				const pending = uiPending.get(request.uiId);
				if (pending) {
					uiPending.delete(request.uiId);
					if (request.ok) pending.resolve(request.result);
					else pending.reject(new Error(request.error || "ui request failed"));
				}
				return;
			}
			if (request.type === "execute_tool") {
				const tool = tools.get(request.toolName);
				if (!tool) throw new Error("unknown extension tool: " + request.toolName);
				const result = await tool.execute(String(request.id ?? ""), request.params ?? {}, undefined, undefined, extensionContext());
				write({ id: request.id, ok: true, result: normalizeToolResult(result) });
				return;
		}
			if (request.type === "execute_command") {
				const command = commands.get(request.commandName);
				if (!command) throw new Error("unknown extension command: " + request.commandName);
				if (typeof command.handler !== "function") throw new Error("extension command has no handler: " + request.commandName);
				const result = await command.handler(String(request.args ?? ""), extensionContext());
				write({ id: request.id, ok: true, result: result ?? null });
				return;
			}
			if (request.type === "emit") {
				const payload = request.payload ?? {};
				for (const handler of eventHandlers.get(request.event) ?? []) {
					const result = await handler(payload, extensionContext());
					if (result && typeof result === "object") Object.assign(payload, result);
				}
				delete payload.signal;
				delete payload.Signal;
				write({ id: request.id, ok: true, result: payload });
				return;
			}
		if (request.type === "shutdown") {
			for (let i = shutdownHandlers.length - 1; i >= 0; i--) {
				await shutdownHandlers[i](extensionContext());
			}
			write({ id: request.id, ok: true, result: null });
			process.exit(0);
			}
			throw new Error("unknown extension request: " + request.type);
		} catch (error) {
		write({ id: request?.id ?? 0, ok: false, error: error?.stack ?? error?.message ?? String(error) });
	}
});
`
