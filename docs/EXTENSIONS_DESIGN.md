# User-scriptable extensions: design options for the Go port

Status: **partial — minimal Node JSONL bridge landed.** A minimal script
runtime (`packages/coding-agent/core/extensions/script_runtime.go`) can load
`.ts`/`.js`/`.mjs` extensions through a Node bridge and register/execute simple
custom tools. The full upstream ExtensionAPI (hooks/events, commands/settings,
`ctx.ui`, message renderers, provider/model registration) is not implemented
yet. This document compares approaches for completing that runtime and
recommends a path; see "Current state" for what works today.

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

What is still missing is the rest of the *rich* upstream `ExtensionAPI`: command
dispatch, settings, provider/model registration, message renderers, widgets, and
a full `ctx.ui`. Go has no built-in equivalent of jiti and cannot `import`
TypeScript/JavaScript or inject virtual modules in-process, so closing that gap
requires the transport work discussed below rather than a drop-in loader.

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
  that assume in-process objects (custom TUI components, message renderers) can't
  cross the boundary and would be unsupported or degraded (the RPC UI context
  already documents these limitations).

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
event hooks, CLI flags** (`registerFlag`/`getFlag`), and basic slash command
dispatch. Still **not** implemented: settings, provider/model registration,
message renderers, widgets, and a rich `ctx.ui` (the bridge's `ctx.ui` is a
degraded stub). The recommended out-of-process JSON-RPC transport above is the
path to closing the remaining gap.

## Capability matrix (script bridge)

What an `.ts`/`.js`/`.mjs` extension can rely on through the current Node bridge
(`core/extensions/script_runtime.go`). "Fail-fast" entries throw a clear
`... is unsupported in the Go bridge` error at call time rather than silently
no-op'ing, so an unsupported upstream extension surfaces a precise message.

| Surface | Status | Notes |
| --- | --- | --- |
| `pi.registerTool` | ✅ supported | custom tools, executed back over the bridge |
| `pi.registerCommand` (+ handler) | ✅ supported | slash command dispatch |
| `pi.registerFlag` / `pi.getFlag` | ✅ supported | values seeded via `PI_EXTENSION_FLAG_VALUES` |
| `pi.on(event, …)` / `pi.events.on` | ✅ supported | event hooks (tool call/result, etc.) |
| `pi.onShutdown` | ✅ supported | |
| virtual `@earendil-works/pi-ai`, `typebox` | ✅ supported | `Type` / `StringEnum` schema helpers |
| virtual `@earendil-works/pi-coding-agent` | ✅ supported | `defineTool`, `createEventBus` |
| virtual `@earendil-works/pi-tui` | 🟡 subset | `Text`, `Container`, `Box`, `Spacer`, `Input`, `SelectList`, `SettingsList`, `Loader`, `CancellableLoader`, `Markdown`, `matchesKey`, `truncateToWidth`, and `Key` (named keys + modifier helpers, e.g. `Key.ctrlAlt('p')`). Layout/widget classes are inert stubs. |
| `ctx.ui` | 🟡 degraded stub | `notify` → stderr; `select` → first option; `confirm` → false; `hasUI` is false |
| `ctx.sessionManager` | 🟡 degraded stub | `getBranch()` returns `[]` |
| `pi.registerProvider` / `unregisterProvider` | ⛔ fail-fast | custom AI providers not bridged |
| `pi.registerMessageRenderer` | ⛔ fail-fast | message renderers not bridged |
| `pi.addAutocompleteProvider` | ⛔ fail-fast | autocomplete providers not bridged |
| `pi.registerShortcut` / `unregisterShortcut` | ⛔ fail-fast | interactive keybindings not bridged |
| Node `module.registerHooks` | ⚠️ required | the bridge needs a Node runtime that provides `module.registerHooks`; older Node throws at startup |

Closing the 🟡/⛔ rows is the job of the out-of-process JSON-RPC transport
described above.
