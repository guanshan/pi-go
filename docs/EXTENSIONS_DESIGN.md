# User-scriptable extensions: design options for the Go port

Status: **partial — minimal Node JSONL bridge landed.** A minimal script
runtime (`packages/coding-agent/core/extensions/script_runtime.go`) can load
`.ts`/`.js`/`.mjs` extensions through a Node bridge and register/execute simple
custom tools, commands, flags, hooks, host-backed context/UI requests,
autocomplete providers, shortcuts, a custom-provider subset, and a text-line
custom message renderer subset, plus the common session action APIs
(`sendUserMessage`, `appendEntry`, session name, and labels). The full upstream
ExtensionAPI (rich component message renderers, rich/custom `ctx.ui`, widgets)
is not
implemented yet. This document compares approaches for completing that runtime
and recommends a path; see "Current state" for what works today.

## Background

The TypeScript reference (`packages/coding-agent/src/core/extensions/loader.ts`)
loads user extensions at runtime:

- Extensions are `.ts`/`.js` modules discovered from the agent dir / project
  (`package.json` `pi.extensions`, or an `index.ts`/`index.js`).
- They are imported with **jiti** (TypeScript-aware loader). In the compiled Bun
  binary, the host's own packages (`@earendil-works/pi-agent-core`, `pi-ai`,
  `pi-tui`, `typebox`, and the coding-agent index) are injected as
  **virtual modules** so extensions can `import` them without a `node_modules`
  tree.
- An extension is a factory `(api) => void` that calls into a rich `ExtensionAPI`:
  `registerTool`, `registerCommand`, event hooks (`on("tool_call", …)`,
  `session_before_compact`, `input`, etc.), provider registration, message
  renderers, and a UI context.

The Go port supports two kinds of extensions today:

- **Compile-time** extensions: a Go `ExtensionFactory func(*ExtensionAPI) error`
  registered in-process (`packages/coding-agent/extensions.go`,
  `core/extensions/runtime.go`).
- **Script extensions (subset)**: a minimal Node JSONL bridge
  (`core/extensions/script_runtime.go`) loads an end user's `.ts`/`.js`/`.mjs`
  extension and supports registering/executing custom tools, subscribing to
  event hooks, and declaring CLI flags (`registerFlag`/`getFlag`, surfaced in
  `--help` and seeded from the command line).

What is still missing is the rest of the *rich* upstream `ExtensionAPI`: rich
component message renderer layout, widgets, and a full custom `ctx.ui`. Go has no built-in
equivalent of jiti and cannot `import` TypeScript/JavaScript or inject virtual
modules in-process, so closing that gap requires the transport work discussed
below rather than a drop-in loader.

## Goals & constraints

1. Users author extensions without rebuilding the Go binary.
2. Reuse the existing `core/extensions` event/registration model
   (`ExtensionAPI`, `Runner`, `EventBus`) as the in-process contract.
3. Cross-platform (Linux/macOS/Windows), single static binary friendly.
4. Acceptable performance: extension hooks fire on hot paths (every tool call,
   every message event), so per-event overhead matters.
5. Security: extensions run user code; sandboxing/escape-hatch posture must be a
   conscious choice.

## Options

### A. Embed a JS runtime (goja)

Use [goja](https://github.com/dop251/goja), a pure-Go ES5.1+/partial-ES6 engine.

- **Pros:** in-process (no IPC), pure Go so it keeps the single-binary story and
  cross-compiles cleanly; can expose host objects directly as the `api`; lets us
  approximate the TS authoring model.
- **Cons:** not full modern ES/TS — no native `import`, limited ES2020+, no TS
  types; we'd need a transpile step (esbuild-go can transpile TS→ES5) and a
  module shim to emulate `virtualModules`. Single-threaded VM per extension;
  blocking host calls need care. Existing TS extensions likely won't run
  unmodified (no Node APIs, different module semantics).

### B. Embed a JS runtime (v8go / quickjs cgo)

Use [v8go](https://github.com/rogchap/v8go) (V8 via cgo) or a QuickJS binding.

- **Pros:** full modern JS, fast.
- **Cons:** **cgo** — breaks easy cross-compilation and the pure-Go static
  binary; V8 is heavy to embed and maintain; still no TS or Node module
  resolution out of the box; platform build complexity. Poor fit for a portable
  CLI.

### C. Out-of-process extensions over stdio JSON-RPC (recommended)

Define a subprocess protocol: the host launches each extension as a child
process and communicates over stdin/stdout with newline-delimited JSON-RPC
(the same framing the agent already uses for RPC mode).

- The host sends lifecycle + event notifications (`tool_call`, `message_end`,
  `session_before_compact`, …) and requests (`execute_tool`, `before_agent_start`).
- The extension replies with registrations (tools/commands), transforms, and
  hook results. UI requests reuse the existing `extension_ui_request` shape.

> **RPC-mode extension UI wire shape.** When pi runs in `--mode rpc`, an
> extension's `ctx.ui.*` call is surfaced to the controlling host as a single
> flattened line `{"type":"extension_ui_request","id":<id>,"method":<m>,…}` with
> the method-specific fields inlined (`select` → `title`/`options`, `confirm` →
> `title`/`message`, `input` → `title`/`placeholder`, `notify` →
> `message`/`notifyType`). The host answers with
> `{"type":"extension_ui_response","id":<id>, …}` carrying exactly one of
> `value` (select/input), `confirmed` (confirm), or `cancelled:true`. This
> matches the TS `RpcExtensionUIRequest`/`RpcExtensionUIResponse` types in
> `rpc-types.ts`. (The internal host↔node-subprocess bridge keeps its own
> `ui_request`/`ui_response` framing with `uiId`/`ok`/`result`; that seam is
> distinct from the host-facing RPC protocol.)
- **Pros:** language-agnostic — users can write extensions in **TypeScript/Node,
  Python, or anything** that speaks the protocol, so existing TS extensions can
  run under their own Node/Bun with the real `@earendil-works/*` packages
  (closest behavioral parity). Strong isolation (crash/sandbox boundary, can
  drop privileges, kill on timeout). Pure-Go host, no cgo, trivial
  cross-compile. Mirrors the proven pattern of LSP / MCP / Terraform plugins.
- **Cons:** IPC and (de)serialization overhead per event — mitigated by batching
  and only forwarding events an extension subscribed to; async request/response
  needs correlation IDs (we already do this for RPC); richer UI/render hooks
  that assume in-process objects (custom TUI components, rich message renderer
  components) can't cross the boundary and would be unsupported or degraded (the
  RPC UI context already documents these limitations).

### D. Native Go plugins (`plugin` package) or pre-registered Go factories

- `plugin`: Linux/macOS only, fragile (exact toolchain/version match), no
  Windows — unsuitable.
- Pre-registered Go factories (status quo): requires recompiling the host; fine
  for first-party/bundled extensions, not for end users.

## Recommendation

Pursue **Option C (out-of-process stdio JSON-RPC)** as the user-scripting path:

- It preserves the Go single-binary, no-cgo, cross-platform properties.
- It maximizes behavioral parity for existing TS extensions by letting them run
  on real Node/Bun with the real host packages, rather than re-implementing the
  module/`virtualModules` machinery inside a constrained embedded VM.
- It gives a clean security boundary for arbitrary user code.

Keep the existing compile-time `ExtensionFactory` for bundled/first-party
extensions, and have the subprocess transport register into the same
`core/extensions` `Runner`/`EventBus` so both kinds of extensions look identical
to the rest of the agent.

Consider Option A (goja) only later as an optional in-process fast path for
simple, pure-logic extensions where IPC overhead dominates and no Node APIs are
needed.

## Suggested phasing (when implemented)

1. Specify the stdio extension protocol (messages, correlation IDs, lifecycle,
   capability negotiation) — reuse RPC framing.
2. Implement a `core/extensions` transport that spawns a child, forwards only
   subscribed events, and bridges registrations/results back into the `Runner`.
3. Provide a TS/Node extension host shim that adapts the existing `ExtensionAPI`
   onto the protocol so current extensions need minimal changes.
4. Discovery: read the agent dir / project `package.json` `pi.extensions`,
   matching the TS loader's resolution rules.
5. Document unsupported in-process-only hooks (custom TUI components, synchronous
   editor access) and their degraded RPC-style fallbacks.

## Current state

Partial. End-user `.ts`/`.js`/`.mjs` extensions run today through the minimal
Node JSONL bridge (`core/extensions/script_runtime.go`), alongside compile-time
Go `ExtensionFactory` registration. The supported surface is **custom tools,
event hooks, CLI flags** (`registerFlag`/`getFlag`), basic slash command
dispatch, host-backed `ctx.ui` prompts, common `ExtensionContext` snapshots and
actions, autocomplete provider suggestions, and interactive shortcut handlers.
Script `pi.registerProvider` is bridged for text/content provider calls through
the AI provider registry and for TS-style model catalog registration configs,
and script message renderers are bridged as a text-line renderer subset for
custom messages. Top-level script actions now cover custom message injection,
user-message delivery, custom state entries, session naming, and labels. Still
**not** implemented: full component message renderer layout, rich/custom
`ctx.ui`, and widgets. The
recommended out-of-process JSON-RPC transport above is the path to closing the
remaining gap.

## Capability matrix (script bridge)

What an `.ts`/`.js`/`.mjs` extension can rely on through the current Node bridge
(`core/extensions/script_runtime.go`). Unsupported registration APIs warn and
skip so the rest of the extension can still load.

| Surface | Status | Notes |
| --- | --- | --- |
| `pi.registerTool` | ✅ supported | custom tools, executed back over the bridge |
| `pi.registerCommand` (+ handler) | ✅ supported | slash command dispatch |
| `pi.registerFlag` / `pi.getFlag` | ✅ supported | values seeded via `PI_EXTENSION_FLAG_VALUES` |
| `pi.on(event, …)` / `pi.events.on` | ✅ supported | event hooks (tool call/result, etc.) |
| `pi.onShutdown` | ✅ supported | |
| virtual `@earendil-works/pi-ai`, `typebox` | ✅ supported | `Type` / `StringEnum` schema helpers |
| virtual `@earendil-works/pi-coding-agent` | ✅ supported | `defineTool`, `createEventBus`, `getSettingsListTheme` |
| virtual `@earendil-works/pi-tui` | 🟡 subset | `Text`, `Container`, `Box`, `Spacer`, `Input`, `SelectList`, `SettingsList`, `Loader`, `CancellableLoader`, `Markdown`, `matchesKey`, `truncateToWidth`, and `Key` (named keys + modifier helpers, e.g. `Key.ctrlAlt('p')`). Layout/widget classes are inert stubs. |
| `ctx.ui` | ✅ host-backed subset | `notify` / `select` / `confirm` / `input` route to the active TUI or RPC host; `setStatus(key, text)` updates the interactive footer or emits the TS RPC `setStatus` request, and `undefined` clears the keyed status. **Lightweight setters** `setWorkingMessage` / `setWorkingVisible` / `setWorkingIndicator` / `setHiddenThinkingLabel` / `setTitle` / `pasteToEditor` / `setEditorText` are bridged: in the interactive TUI they adjust the footer busy indicator, terminal window title, and editor text; in RPC mode `setTitle` / `setEditorText` / `pasteToEditor` forward TS-shaped requests while the working/thinking setters are no-ops (matching TS rpc-mode). `getEditorText()` resolves a `Promise<string>` over the bridge (TS is synchronous — documented divergence) returning the current editor text, `""` headless. `editor(title, prefill)` opens `$VISUAL`/`$EDITOR` and resolves the edited text (or `undefined`). `onTerminalInput()` returns a no-op unsubscribe (raw per-keystroke forwarding is not bridged). **Widgets:** `setWidget(key, string[]\|undefined, {placement})` is bridged — plain-text widgets render above/below the editor in the interactive TUI (keyed, move-on-replacement, undefined removes, capped at 10 lines with a truncation marker) and forward `widgetKey`/`widgetLines`/`widgetPlacement` in RPC mode (matching TS rpc-mode's string-array subset). Component-factory widgets warn-and-skip. Headless calls reject clearly for dialogs and no-op for the lightweight setters/widgets. `setFooter` / `setHeader` / `setEditorComponent` / `custom()` (rich component factories) remain unsupported (warn-and-no-op; `getEditorComponent()` returns `undefined`). |
| `ctx.cwd` / `ctx.mode` / `ctx.model` | ✅ host-backed | values come from the active `AgentSession` snapshot for every tool/command/event request |
| `ctx.modelRegistry` | 🟡 host-backed subset | `list`, `getAll`, `getAvailable`, `find`/`get`, `hasConfiguredAuth`, and `getApiKeyAndHeaders` use the session model registry snapshot/action bridge |
| `ctx.sessionManager` | 🟡 host-backed subset | `getEntries()`, `getBranch(fromId?)`, `getLeafId()`, and `getHeader()` use the current session snapshot |
| `ctx.abort` / `ctx.compact` / `ctx.shutdown` | 🟡 host-backed subset | `abort()` and `compact()` dispatch to the host; `compact({onComplete,onError})` callbacks are honored. `shutdown()` requires a host shutdown binding. |
| `ctx.navigateTree` / `reload` / `waitForIdle` (command context) | 🟡 host-backed subset | `navigateTree(targetId, {summarize,customInstructions,replaceInstructions,label})` and `reload()` dispatch to the host and return `{cancelled}` / resolve; `waitForIdle()` resolves when the agent stops streaming/compacting. The `withSession`/`setup` callbacks are dropped (functions cannot cross the process boundary). |
| `ctx.newSession` / `fork` / `switchSession` / `getSystemPromptOptions` (command context) | ⛔ unsupported (clear reject) | these reject with a descriptive "not supported by this host" error rather than crashing — full session replacement from an extension needs the mode-loop runtime-swap machinery (deferred); `getSystemPromptOptions` is unmodeled in Go (use `ctx.getSystemPrompt()` for the resolved prompt text). |
| cross-process semantics | ℹ️ documented divergence | `ctx.signal` is only populated inside an `emit()` event request (an `AbortSignal` cannot cross the process boundary), so it is `undefined` in tool/command/shortcut handlers; `ctx.getContextUsage()` returns a host token *estimate*, not the model's reported usage; `pi.events.emit` dispatches only the emitting extension's own in-process handlers (no cross-extension/host fan-out); `ctx.getEditorText()` is a `Promise` (TS is synchronous). |
| `pi.addAutocompleteProvider` | 🟡 bridged subset | provider factories can return `getSuggestions(...)` results with `items` and `prefix`; the Bubble TUI merges those values with built-in suggestions, renders item descriptions, and applies custom `applyCompletion` text and cursor results when provided. TS-exact visual-wrap cursor behavior is not fully matched. |
| `pi.registerShortcut` / `unregisterShortcut` | 🟡 bridged subset | script shortcuts registered during load or later handler execution are exposed to the live Bubble TUI, execute handlers with a host-backed `ExtensionContext`, and appear in `/hotkeys`. Shortcut conflict filtering/diagnostics mirror TS: reserved editor-global built-ins are skipped, non-reserved built-in overlaps warn and use the extension, and duplicate extension shortcuts use the later registration. |
| `pi.registerProvider` / `unregisterProvider` | 🟡 bridged subset | providers with `complete`, `stream`, `completeSimple`, or `streamSimple` handlers register into the Go AI provider registry; handlers receive a JSON-safe ChatRequest snapshot and a host-backed `ExtensionContext`, and may return strings, content blocks, assistant-like objects, or async iterables. `registerProvider(name, config)` also applies TS-style `baseUrl`/`apiKey`/`headers`/`models`/`modelOverrides` catalog configs to the active model registry during startup and dynamic registration. **Token-level streaming is bridged:** `stream`/`streamSimple` iterables emit out-of-band `provider_chunk` events (synthesized text/tool deltas, or passed-through `AssistantMessageEvent`-shaped chunks) that map onto incremental `ai.AssistantMessageEvent`s; the final integer-id reply remains authoritative for the message/usage/stopReason, and Go-context cancellation aborts the Node provider's signal. |
| `pi.registerMessageRenderer` / `unregisterMessageRenderer` | 🟡 bridged subset | renderers registered during load or later handler execution are exposed to Go and render `custom_message` payloads into the **live interactive transcript**. Handlers receive `{customType, content, display, details, timestamp}`, `{expanded,width}`, and a theme helper that now emits **real ANSI SGR** (exact bold/dim/italic/underline; a fixed 16-color table for `fg`/`bg`); return values may be strings, string arrays, `{lines}`, `{text}`, or virtual TUI objects with `render()` (the `Box` shim applies its style function). Display messages render once on receipt with shared expand/collapse and a default bold `[customType]`+markdown fallback. Output is flattened to ANSI lines (parity with TS `Component.render` → `string[]`); arbitrary rich component layout and markdown-inside-a-renderer are not re-parsed. |
| `pi.sendMessage` | 🟡 bridged subset | appends a `custom_message` session entry with `customType`, `content`, `display`, and `details`. `triggerTurn` is bridged to interactive hosts: the Bubble TUI starts an empty follow-on turn when idle or queues one as a follow-up while busy; hosts without a trigger binding report it as requested but unhandled. |
| `pi.sendUserMessage` | 🟡 bridged subset | sends a user message through the active host. In the Bubble TUI this starts a turn when idle; while busy it requires `deliverAs: "steer"` or `"followUp"` and queues through the existing agent queues. |
| `pi.appendEntry`, `setSessionName`, `getSessionName`, `setLabel` | ✅ supported | host-backed session persistence actions for custom state, display names, and entry labels. `setLabel` validates the target entry exists, matching TS. |
| Node `module.registerHooks` | ⚠️ required | the bridge needs a Node runtime that provides `module.registerHooks`; older Node throws at startup |

Closing the 🟡/⛔ rows is the job of the out-of-process JSON-RPC transport
described above.
