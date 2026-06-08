# TypeScript Package Compatibility Audit

Source project: [badlogic/pi-mono](https://github.com/badlogic/pi-mono) (TypeScript)

Target project: [guanshan/pi-go](https://github.com/guanshan/pi-go) (this repo)

This file tracks the migration of the four TypeScript packages under
`packages/` in the upstream monorepo into Go packages under `packages/`.

## packages/ai

Implemented in Go:

- message/content/model/usage/tool-call types are defined directly in
  `packages/ai`
- auth storage for API keys/OAuth-shaped records, runtime keys, provider env-key
  resolution, stored credential listing/status, deletion, and `auth.json`
  persistence is defined directly in `packages/ai`
- text-model catalog support for generated models, built-in defaults, custom
  `models.json` loading for list, wrapped-list, and TS-style `providers`
  configs, provider/model override merging, provider `apiKey` env/literal
  fallback, lookup/match/list helpers, thinking-level clamping, and TS-style
  per-million-token cost calculation is defined directly in `packages/ai`
- provider-facing runtime tool interface, tool set, and deterministic tool
  schema definition builder are defined directly in `packages/ai`
- provider request transforms normalize persisted coding-agent custom/session
  roles (`bashExecution`, `custom`, `branchSummary`, `compactionSummary`) into
  ordinary user messages before provider payload builders run; `ai.CustomMessage`
  remains as a legacy session compatibility shape, while coding-agent/core now
  constructs core-owned session message types for new context entries and
  agent/harness session readers construct harness/session-owned equivalents
- public `packages/ai` model registry/runtime, built-in defaults, and generated
  text-model catalog coverage from TS `models.generated.ts` with provider/model lookup,
  reasoning-level metadata, context window, max-token, and cost fields
- auth storage and provider env key lookup
- API provider registry
- text completion and streaming helpers, including propagation of
  `Context.Tools` into provider tool schemas for simple completions
- image provider registry and image response types
- generated image-model catalog registry for OpenRouter image models with
  provider/model lookup helpers
- OpenRouter image generation provider with chat/completions payload
  construction, text/image output parsing from data URLs, usage/cost parsing,
  provider/model headers, env/API key lookup, and stopReason/errorMessage
  handling
- OpenAI-compatible prompt-cache controls for chat/completions, including
  `prompt_cache_key` clamping, long-retention payload fields, Anthropic-style
  cache_control markers for OpenRouter Anthropic models/custom compat, routing
  provider options, temperature/max-token overrides, and session-affinity
  headers
- native Mistral chat/completions transport using the TS
  `mistral-conversations` API name, including message/image/tool conversion,
  deterministic Mistral tool-call ID normalization, reasoning mode fields,
  `x-affinity` session header support, and usage/tool-call response parsing
- OpenAI Responses, Azure OpenAI Responses, and OpenAI Codex Responses
  transports, including Responses input/tool conversion, prompt-cache/session
  headers, Azure deployment/API-version resolution, Codex account headers,
  Codex SSE and native WebSocket parsing, websocket-cached input delta/reuse
  stats, reasoning/include fields, text/thinking signatures, and usage/tool-call
  response parsing
- Google Vertex basic REST transport with project/location endpoint resolution,
  API-key auth, Gemini content/image/tool conversion, thinking config,
  thought-signature preservation, and usage/tool-call parsing
- Amazon Bedrock Converse basic REST transport with region/endpoint resolution,
  bearer-token or AWS env credential auth detection, SigV4 signing,
  Claude cache-point/thinking fields, message/image/tool conversion, and
  usage/tool-call response parsing
- Cloudflare Workers AI / AI Gateway model support hooks, including generated
  catalog entries, credential readiness checks, `{CLOUDFLARE_*}` base URL
  placeholder resolution, and OpenAI-compatible AI Gateway chat endpoint
  routing
- OAuth credential types, provider registry, PKCE generation, device-code polling,
  API-key extraction, built-in login flows, and refresh helpers for Anthropic,
  GitHub Copilot, and OpenAI Codex credentials
- provider requests resolve credentials through the model registry, including
  expired OAuth token refresh and `auth.json` persistence before use
- diagnostics helpers
- context overflow detection
- JSON repair and streaming JSON parse helper
- JSON Schema/TypeBox-style tool argument validation helpers with primitive
  coercion, object/array traversal, required/additionalProperties checks,
  enum/const checks, union handling, and `StringEnum`
- deterministic short hash
- unicode sanitization and header merge helpers
- session resource cleanup registry
- tests for faux completion, stream events, utilities, OAuth, and schema
  validation/coercion
- `packages/ai` no longer imports coding-agent internals; provider runtime is
  available from the public AI package

Still incomplete versus TS:

- advanced OpenAI Responses/Codex Responses transport parity, including
  streaming event emission details, live Codex WebSocket probe coverage, retry
  policy, service-tier pricing, and provider-specific dynamic headers
- Bedrock Converse streaming/SDK edge-case parity and Google Vertex ADC/SDK
  streaming parity
- full interactive OAuth login UI parity in coding-agent TTY/TUI modes
- prompt cache/provider-specific parity outside OpenAI-compatible
  chat/completions
- full TypeBox compiler error wording/localization parity

## packages/agent

Implemented in Go:

- Agent state wrapper
- prompt, continue, abort, steering and follow-up queues
- event subscriptions and lifecycle events
- async-style event stream helper for loop consumers
- sequential and parallel tool execution path
- before/after tool-call hooks and turn update/stop callbacks
- low-level loop helper
- generic tool interface
- memory and JSONL session repositories
- SessionStorage-style in-memory and JSONL storage abstractions with metadata,
  leaf entries, entry lookup/find, label cache, path-to-root traversal, and
  storage error codes
- UUIDv7 session id generation, timestamp helper, and TS-compatible session CWD
  encoding helper
- harness message helpers for bash/custom/branch/compaction messages and LLM
  conversion
- shared truncation helpers for head/tail/line limits and byte/line metadata
- shell output capture helpers with sanitization, tail truncation, cancellation
  handling, and full-output spill files
- branch summary preparation helpers: branch entry collection, file-operation
  extraction, conversation serialization, file-list metadata, and local/LLM
  summary entry point
- proxy stream helper for server-routed LLM calls, including `/api/stream`
  request construction, SSE parsing, partial assistant reconstruction, text,
  thinking, tool-call, done, and error events
- local NodeExecutionEnv-style filesystem and shell environment: absolute/join
  path helpers, text/binary reads, line reads, writes/appends, file info,
  directory listing, canonical path, exists, directory/remove/temp helpers,
  shell execution, timeout/abort/shell-unavailable errors, and shell-capture
  integration
- local NodeExecutionEnv filesystem/shell error handling. NOTE: the TS
  `ExecutionEnv` returns a neverthrow-style `Result<TValue,TError>`; the Go port
  intentionally does not port that monad. There is no `Result[TValue,TError]`
  type and no `ResultExecutionEnv` adapter in this repo. Instead Go uses
  idiomatic `(value, error)` returns with typed errors (e.g. `*FileError` /
  execution errors that implement `Unwrap()`), matching `packages/agent/harness/env`.
- local compaction and token estimation helpers
- system prompt, prompt template, skill harness helpers
- tests for prompt flow, continue/tool flow, JSONL repo, compaction, harness
  messages, truncation, shell capture, branch summary preparation, and proxy
  stream reconstruction, NodeExecutionEnv filesystem and shell behavior

Still incomplete versus TS:

- exact async iterator API shape from TypeScript
- TypeBox-equivalent schema validation parity
- exact turn lifecycle behavior for every edge case
- exact branch summarization prompt/provider error taxonomy parity

## packages/tui

Classification (ported / wired / not-wired):

`packages/tui` is a ~9500-line component library. Most of it is **ported**
(compiles, is unit-tested, and tracks the upstream TS TUI) but, under route A
(see [docs/TUI_DESIGN.md](TUI_DESIGN.md)), the interactive coding-agent UI is
driven by Bubble Tea, so the bulk of the library is **not wired** into any
production path. To keep this distinction honest:

- **Wired** (consumed by production code in `cmd/` + `packages/coding-agent`,
  excluding tests): the exported symbols on a live path are
  `TruncateToWidth`, `VisibleWidth`, `NewMarkdown`, `MarkdownTheme`,
  `FuzzyMatchString`, and — added by interactive-TUI slice 2 — the SelectList
  surface (`NewSelectList`, `SelectList`, `SelectItem`, `SelectListTheme`,
  `SelectListLayoutOptions`), plus the app keybindings surface
  (`KeybindingsManager`, `NewKeybindingsManager`, `Keybinding`,
  `KeybindingDefinitions`, `KeybindingDefinition`, `KeybindingsConfig`,
  `TUIKeybindings`, `KeyID`, `SetKeybindings`). These are the allowlist enforced by
  `scripts/check_arch.go` (`wiredTUIComponents`): wiring an additional component
  requires adding it there and here, so dead code cannot silently become "live".
  The non-test importers are `core/interactive_tui.go`,
  `core/interactive_model_selector.go`, `core/theme.go`, `core/modes.go`, and
  `cli/list_models.go`.
- **Ported but not wired**: everything else listed under "Implemented in Go"
  below — Input, SettingsList, EditorComponent, Loader/
  CancellableLoader, Image, ProcessTerminal, the reference autocomplete
  providers, stdin paste buffer, undo stack, kill ring, terminal image
  encoders/capability detection, native-modifier helpers, etc. They exist and
  are tested but have no production consumer yet. Note that the live
  coding-agent autocomplete is no longer a text-only fallback: slash/model/
  prompt/skill/path/`@`-ref and extension-provider suggestions are wired in
  `core/interactive_tui.go`, but that product path uses core-specific merging and
  rendering rather than `packages/tui`'s reference autocomplete provider. The
  SelectList-backed `/model` picker (P1-4 selectors) is wired via the interactive
  overlay (Ctrl+L / bare `/model`). Inline image rendering and the settings
  selector remain TODO.
- **Intentionally not ported (route A)**: the upstream `TUI` event loop and
  overlay machinery (see the dedicated subsection below).

The "Implemented in Go" list below means **ported** (and where noted, wired); it
does not imply the component is on the production interactive path.

Implemented in Go:

- Component, Container, Text, TruncatedText, Spacer, Box
- single-line Input, Bubble Tea textarea integration in coding-agent, SelectList,
  SettingsList
- EditorComponent-style text/callback/history/autocomplete/padding surface for
  custom editor integration; the full upstream multi-line Editor remains a
  Bubble-layer responsibility
- Loader and CancellableLoader
- Markdown text renderer with headings, paragraphs, fenced code blocks,
  blockquotes, horizontal rules, unordered/ordered lists, pipe tables, inline
  code, bold/italic/strikethrough, links with OSC 8 or URL fallback, padding,
  ANSI/OSC-aware visible width, and image-line preservation
- Image component with text fallback plus Kitty/iTerm2 sequence output when image
  data and terminal capabilities are available
- ProcessTerminal basics
- key parsing, key matching, keybindings manager
- fuzzy matching and autocomplete provider composition
- stdin paste buffer
- undo stack and kill ring
- terminal image dimensions and Kitty/iTerm2 encoders
- terminal image capability detection for Kitty/Ghostty/WezTerm/iTerm2/Windows
  Terminal/VSCode/Alacritty, tmux/screen-safe fallbacks, truecolor and OSC 8
  hyperlink flags, image-line detection, OSC 8 hyperlink helper, and Kitty
  cleanup sequences
- ProcessTerminal output lifecycle helpers for bracketed paste mode, Kitty
  protocol and xterm modifyOtherKeys toggles, drain/reset best-effort handling,
  OSC 9;4 progress keepalive, write-log path resolution, terminal dimensions,
  and Apple Terminal Shift+Enter input normalization helper
- native modifier helper API with injectable helper and safe fallback
- tests for render, input, settings, paste, keybindings, image dimensions,
  editor component surface, and native modifier fallback

Intentionally not ported (route A — see [docs/TUI_DESIGN.md](TUI_DESIGN.md)):

- the upstream `TUI` type and its overlay machinery (`OverlayHandle`,
  hide/setHidden/focus/unfocus/isFocused, overlay positioning). `packages/tui`
  deliberately does not own a main event loop, raw-mode stdin reader, `SIGWINCH`
  handling, a differential renderer, or an overlay stack; the interactive
  coding-agent UI is driven by Bubble Tea instead. There is therefore no Go
  equivalent of the TS `TUI`/`OverlayHandle` public API.

Still incomplete versus TS:

- full differential renderer with cursor parity
- OS raw-mode stdin handling, full Kitty keyboard negotiation, and real stdin
  drain parity in `packages/tui` itself (the interactive coding-agent path now
  requests Bubble Tea keyboard enhancements)
- complete editor behavior and autocomplete dropdown rendering parity
- remaining markdown parser edge cases versus `marked`, nested block/list
  parity, and exact theme/style reset behavior
- packaged macOS native modifier binary loading

## packages/coding-agent

Public API surface and the `core` boundary (decision — parity review P1-F2):

Upstream TS ships a single package (`@earendil-works/pi-coding-agent`) whose
`src/index.ts` re-exports every public type from one entry point; downstream
users only ever import that one package. In Go, `packages/coding-agent` is the
public SDK facade, but its exported signatures reference roughly 103 distinct
`core` / `core/extensions` implementation types directly — for example
`CreateAgentSession` returns `*CreateAgentSessionResult{Session *core.AgentSession;
Diagnostics []core.Diagnostic}`, `BuildSessionContext` takes `[]core.SessionEntry`
and returns `core.SessionContext`, `NewCorePackageManager` returns
`core.PackageManager`, and `DefineTool` returns `coreext.ToolDefinition`
(`sdk_more.go`, `session_helpers.go`, `package_manager.go`). Arch guard rule P6
(`scripts/check_arch.go`) forbids type aliases inside `packages/coding-agent`, so
these types cannot be transparently re-exported behind a single-package facade
the way TS does.

**Decision: declare `packages/coding-agent/core` and
`packages/coding-agent/core/extensions` to be public, stable sub-APIs**, rather
than relaxing P6 to allow a re-export facade. Rationale:

- It is the lowest-cost option that matches the code as it stands today, and it
  is honest: downstream Go users already must import `core` / `core/extensions`
  to name these return and parameter types, so calling them "implementation
  detail" was misleading.
- Relaxing P6 to permit `type AgentSession = core.AgentSession` style re-exports
  would recover the TS "single import surface" shape, but at the cost of
  duplicated godoc, added guard complexity, and a larger refactor; it was judged
  not worth it for this pass and is explicitly not done here.
- Stability expectation: `core` and `core/extensions` are now covered by the same
  compatibility expectations as the `coding-agent` facade itself. Breaking
  changes to their exported symbols are public API changes and should be treated
  as such, not as free internal refactors.

This decision is mirrored by a comment next to the P6 check in
`scripts/check_arch.go`; do not loosen P6 without updating this section.

Implemented in Go:

- CLI entry through `cmd/pi`
- runnable core runtime now lives under `packages/coding-agent/core`; the
  repository root no longer contains an `internal/` package
- build metadata injection through `cmd/pi` (`version`, `commit`, `date`) is
  wired into `pi --version`
- args parser and help under `packages/coding-agent/cli`
- TS-compatible runtime CLI validation for RPC `@file` rejection and `--fork`
  session-selection flag conflicts
- settings/config/session wrappers
- SDK-style `CreateAgentSession`
- resource loading wrappers and a public `DefaultResourceLoader` surface with
  TS-style getters, resource extension paths, override hooks, context-file
  loading, system/append prompt discovery, additional resource paths,
  missing-path diagnostics, and inline extension factories
- session-manager helper functions for JSONL entry parsing, v1/v2/v3 entry
  migrations, latest compaction lookup, and tree-aware session context
  construction with compaction/custom/branch summary handling
- built-in coding tool factories
- public `packages/coding-agent/core/tools` implementation for read/write/edit,
  bash, grep, find, ls, truncation, and schema helpers
- print, JSON, RPC, lightweight interactive helpers
- HTML export with TS-style default filename, standalone session viewer shell,
  sidebar/search/filter controls, copy-link buttons, header/stats/model
  metadata, base64-embedded session data, static rendering for user,
  assistant, thinking, image, tool-call/tool-result, bash execution,
  compaction, branch summary, custom, label, and session metadata entries
- `/copy` support for copying the last assistant text via native clipboard
  tools or OSC 52 fallback, plus `/share` support for exporting the current
  session and creating a secret GitHub gist through `gh`
- lightweight `/tree`, `/tree <entry-id>`, `/fork <entry-id>`, and `/clone`
  session branch operations, including entry-id prefix resolution and SDK
  helpers for formatting/cloning session branches
- `/changelog` command plus shared changelog path/parse/filter/format helpers
- changelog helpers live under `packages/coding-agent/utils` and are shared by
  the public SDK facade and core slash-command handling
- model resolver helpers
- auth guidance messages
- public auth status and API key save helpers, plus lightweight interactive
  `/login` provider status, API-key saving, core OAuth provider login, and
  `/logout <provider>` stored credential removal
- TS-shaped `auth.json` object credentials with migration from legacy
  `oauth.json` and `settings.json.apiKeys`
- TS-style initial prompt assembly for stdin, `--file` text/image attachments,
  absolute `<file name="...">` tags, empty-file skipping, and first CLI message
  handling
- `images.blockImages` and `images.autoResize` settings compatibility,
  including legacy flat setting fallback and LLM-request image block filtering
- `shellCommandPrefix` settings compatibility for bash tools, with
  `bashCommandPrefix` retained as a legacy fallback
- TS nested settings compatibility for key runtime knobs, including
  `transport`, `compaction`, `retry`, `terminal`, markdown/warning flags,
  `httpIdleTimeoutMs`, interactive tree/editor options, and legacy flat
  fallback where applicable
- latest-version check helpers and package-version comparison
- changelog parser and newer-entry filter
- session-root, commands-to-prompts, tools-to-bin, and deprecated extension
  directory migration helpers
- frontmatter parsing/stripping, supported image MIME sniffing, HTML entity
  decoding, ANSI stripping, path normalization helpers, abortable sleep, and
  binary-output sanitization
- shell environment/config helpers (agent-bin PATH injection). WIRED via a single
  implementation: the bash tool and `AgentSession.ExecuteBash` set the command
  environment through `core/tools.ShellEnv`, so the agent `bin` directory
  (migrated `fd`/`rg` and package-installed CLIs) is prepended to `PATH`, mirroring
  TS `getShellEnv()` (`src/utils/shell.ts:112-124`). The earlier redundant
  map-based `GetShellEnv` in `packages/coding-agent/utils.go` was removed in favor
  of this one (parity review topic 8 P1-G1).
- public package manager with path/git/npm source parsing and progress events,
  including GitHub shorthand, explicit git URLs, scp-like SSH URLs, refs, and
  pinned-source detection
- package manager resource resolution for installed/local packages, package
  `pi` manifests, auto-discovered user/project resources, disabled resource
  flags, temporary extension sources, and source metadata
- package manager configured-source helpers for listing configured packages,
  adding/removing sources, installed-path lookup, install/remove-and-persist,
  TS-shaped `settings.json` `packages` entries with string/object forms,
  package resource filters, legacy `installedPackages` fallback, default
  progress callbacks, and TS-style separation between install/remove and
  settings persistence
- CLI package commands and the lightweight core runtime now read/write
  TS-shaped `packages` settings consistently, including package resource
  filters during startup resource loading
- event bus
- extension API/runner shape and event bus integration
- FooterDataProvider-style git branch/status surface with worktree-aware git
  path resolution, branch refresh callbacks, extension status storage, available
  provider count tracking, and built-in provider display names
- slash command metadata
- tests for SDK prompt, RPC state, resolver, event bus, session helpers,
  migrations, version checks, changelog, utility helpers, git source parsing,
  package resource resolution/configured-source helpers, and
  `DefaultResourceLoader`

Partial versus TS:

- executable TypeScript extension loading: a minimal Node JSONL bridge
  (`core/extensions/script_runtime.go`) loads `.ts`/`.js`/`.mjs` extensions and
  registers/executes simple custom tools, declares CLI flags, dispatches basic
	  slash commands, subscribes to events/hooks, returns mutation/cancel results
	  from before-hooks, and forwards basic `ctx.ui` notify/select/confirm/input
	  prompts to line/Bubble/RPC hosts. Script `pi.registerProvider` is now
	  bridged for text/content provider calls through the Go AI registry and for
	  TS-style model catalog configs (`baseUrl`, `apiKey`, `headers`, `models`,
	  `modelOverrides`) applied to the active model registry at startup and during
	  dynamic registration. `pi.registerMessageRenderer` / `pi.sendMessage` now
	  support custom-message text-line rendering. Script action APIs now cover
	  `pi.sendUserMessage`, `pi.appendEntry`, `pi.setSessionName`,
	  `pi.getSessionName`, and `pi.setLabel`; the virtual coding-agent module also
	  exports a lightweight `getSettingsListTheme` helper. `ctx.ui.setStatus`
	  now updates the live Bubble TUI footer and emits TS-shaped RPC
	  `extension_ui_request` lines, and the lightweight `ctx.ui` setters
	  (`setWorkingMessage`/`setWorkingVisible`/`setWorkingIndicator`/
	  `setHiddenThinkingLabel`/`setTitle`/`pasteToEditor`/`setEditorText`),
	  `getEditorText`, and `editor` are bridged to the interactive TUI and RPC
	  hosts. Rich `ctx.ui` components (`setWidget`/`setFooter`/`setHeader`/
	  `custom`) and full component message renderer layout are not yet implemented

Now wired (previously ported-but-dead helpers):

- Detached child tracking: the registry moved to `packages/coding-agent/core/tools`
  (`TrackDetachedChildPID` / `UntrackDetachedChildPID` /
  `KillTrackedDetachedChildren` in `detached.go`) — the lowest package both shell
  spawn sites and the signal handler can reach without an import cycle. Both
  spawn paths now track their detached process group: `BashTool.Execute`
  (`core/tools/bash.go`) and `AgentSession.ExecuteBash` (`core/session_api.go`)
  call `TrackDetachedChildPID` after `Start` and untrack on completion. The
  shutdown handler (`signal.go`) calls `catools.KillTrackedDetachedChildren()` on
  SIGTERM/SIGHUP, matching `trackDetachedChildPid` / `killTrackedDetachedChildren`
  in `src/utils/shell.ts`. Per-command aborts still kill the group via
  `cmd.Cancel`; the registry additionally reaps background descendants left by a
  normally-completed command when the whole agent is torn down.
- `MarkPathIgnoredByCloudSync` (`packages/coding-agent/utils.go`): now called on
  the shared npm install root right after it is created
  (`package_manager.go`), mirroring `ensureNpmProject ->
  markPathIgnoredByCloudSync` in `package-manager.ts` so large package directories
  are not disrupted by Dropbox/iCloud sync.
- Package-manager edge cases (`package_manager.go`,
  `package_manager_resources.go`): the npm install/update branches now route
  through `ensureNpmProject`, fully mirroring `ensureNpmProject` in
  `package-manager.ts`: create the root, `markPathIgnoredByCloudSync`, seed
  `.gitignore` (`"*\n!.gitignore\n"`), and write
  `{"name":"pi-extensions","private":true}` (2-space indent) when no
  `package.json` exists. The git install branch seeds the git root with the same
  `.gitignore` (`installGit -> ensureGitIgnore(gitRoot)`). A pinned npm source
  whose installed `package.json` version already matches the pin is treated as a
  cache hit and is not reinstalled, mirroring `resolvePackageSources`'
  `parsed.pinned && installedNpmMatchesPinnedVersion(...)` gate. The git update
  branch, for a ref-bearing record, replaces `git pull --ff-only` with
  `git fetch origin <ref>` then compares `git rev-parse HEAD` against
  `git rev-parse FETCH_HEAD^{commit}` and, on divergence, `git reset --hard
  FETCH_HEAD^{commit}` + `git clean -fdx` before reinstalling deps, mirroring
  `updateGit -> ensureGitRef`. `offlineModeEnabled()` (`PI_OFFLINE` =
  `1`/`true`/`yes`, case-insensitive; via `core.EnvOffline`) now short-circuits
  the resolve missing-source `install` action so `PI_OFFLINE` never installs a
  missing managed source, mirroring `resolvePackageSources`' `installMissing()`
  offline guard.
- Theme subsystem: `core/theme.go` now parses the TS theme JSON shape (all
  required color tokens, vars, integer/hex/default colors), resolves
  `settings.theme` against built-in and discovered/package/CLI theme paths with
  first-resource-wins duplicate handling, falls back to `dark` on invalid or
  missing selections, and applies the resolved theme to the live Bubble TUI
  transcript, footer/header/input, extension UI prompts, autocomplete selection,
  model selector, and markdown theme tokens.
- Keybindings slice: `core/keybindings.go` now mirrors the TS app-level command
  table, reads `<agentDir>/keybindings.json`, migrates legacy binding names,
  reports unknown/invalid bindings as startup diagnostics, and wires effective
  bindings into the real Bubble TUI for interrupt/clear/exit/suspend,
  model/thinking cycling, model select, follow-up, dequeue, thinking visibility,
  external editor, and placeholder status paths for tool expansion and clipboard
  image paste.
- Rich transcript slice: the live Bubble TUI now tracks tool execution metadata
  (tool name, call id, args, partial/error state) and bash command metadata, then
  renders tool/bash output with TS-style 20-line collapsed previews, `ctrl+o`
  expansion/collapse hints, bash command/status headers, and themed diff-line
  coloring for added/removed/context lines.
- Paste/image/autocomplete input slice: bracketed paste in the live Bubble TUI
  now mirrors the TS editor's large-paste behavior (`>10` lines or `>1000`
  chars) by storing paste content behind `[paste #N ...]` markers, expanding
  markers before submit/external-editor use, and preserving expanded history.
  Clipboard image paste now reads Linux Wayland/X11/WSL-style clipboard images
  through `wl-paste`, `xclip`, or PowerShell, writes a temp image path into the
  editor, and sends the corresponding `ai.ContentBlock{Type:"image"}` with the
  next prompt. Script and Go extensions can now register autocomplete providers;
  provider suggestions (`items` plus `prefix`) are merged into the live Bubble
  TUI dropdown and applied by replacing the typed prefix. The dropdown now
  renders completion descriptions for built-in slash commands, prompt templates
  (first content line fallback), skills, extension commands, model providers, and
  extension provider items while keeping accepted values separate from labels.
  Prompt/skill/extension command descriptions now include TS-style source tags
  such as `[p]`, `[u]`, and `[u:npm:...]` when resource source metadata is
  available.
  Timed-out/cancelled script autocomplete requests now send a bridge
  `cancel_request` frame so provider `options.signal` is aborted in Node instead
  of continuing silently in the background.
  Extension provider `applyCompletion` callbacks are bridged for text
  replacement results; the live Bubble TUI applies the returned text and logical
  `cursorLine`/`cursorCol` when provided.
- Extension shortcut slice: script and Go extensions can register shortcuts;
  the live Bubble TUI executes non-conflicting chords asynchronously and passes a
  host-backed `ExtensionContext` to script handlers. Conflict filtering and
  diagnostics mirror TS: reserved editor-global built-ins are skipped,
  non-reserved built-in overlaps warn and use the extension, duplicate extension
  shortcuts use the later registration, dynamic register/unregister calls after
  load update the host shortcut table, and `/hotkeys` lists only the shortcuts
  that remain usable after conflict filtering.
- Extension UI status slice: script `ctx.ui.setStatus(key, text)` now follows
  TS semantics for the common footer-status path. Interactive mode stores keyed
  status text, sanitizes newlines, sorts by key for rendering in the live footer,
  and clears on `undefined`; RPC mode emits `extension_ui_request` with
  `method:"setStatus"`, `statusKey`, and `statusText`, without waiting for a UI
  response.
- Extension UI lightweight slice: the string/boolean/editor-text setters
  `setWorkingMessage`, `setWorkingVisible`, `setWorkingIndicator`,
  `setHiddenThinkingLabel`, `setTitle`, `pasteToEditor`, `setEditorText`, plus
  `getEditorText` and `editor(title, prefill)`, are bridged. Interactive mode
  reflects them in the footer busy indicator (working message / visibility /
  first indicator frame), the terminal window title (`tea.View.WindowTitle`), and
  the input editor (`setEditorText` replaces, `pasteToEditor` re-uses paste
  folding, `getEditorText` returns the paste-expanded text); `editor` suspends the
  TUI for `$VISUAL`/`$EDITOR`. RPC mode mirrors `rpc-mode.ts`: `setTitle` /
  `set_editor_text` (from `setEditorText` and `pasteToEditor`) forward host
  requests, `editor` round-trips, `getEditorText` returns `""`, and the
  working/thinking setters are no-ops. **Divergences:** `getEditorText()` is
  synchronous in TS but resolves a `Promise<string>` across the out-of-process Go
  bridge (it cannot block on the host); `onTerminalInput()` returns a no-op
  unsubscribe because per-keystroke raw input is not forwarded to the extension
  subprocess. Rich component setters (`setWidget`, `setFooter`, `setHeader`,
  `setEditorComponent`, `custom()`) remain unsupported.
- Extension UI widget slice: `ctx.ui.setWidget(key, string[]|undefined, {placement})`
  is bridged for the plain-text subset TS rpc-mode forwards. Interactive mode
  renders keyed widgets above/below the editor (`renderExtensionWidgets`), with
  TS parity for move-on-replacement (re-setting a key with a new placement moves
  it), `undefined` removal, and a 10-line cap with a `... (widget truncated)`
  marker. RPC mode forwards `widgetKey`/`widgetLines`/`widgetPlacement`
  (`widgetLines:null` to remove). Component-factory widgets and the rich
  component setters (`setFooter`/`setHeader`/`setEditorComponent`/`custom`) warn
  and no-op; `getEditorComponent()` returns `undefined`. **Divergence:** widgets
  render in deterministic key-sorted order (Go maps are unordered) vs TS
  insertion order — unobservable cross-process.
- Extension provider slice: script `pi.registerProvider` / `unregisterProvider`
  now registers provider adapters into `packages/ai` and removes them on runtime
  shutdown. The bridge supports `complete`, `stream`, `completeSimple`, and
  `streamSimple` handlers that receive a JSON-safe ChatRequest snapshot plus
  host-backed `ExtensionContext`; string/content/assistant-like results and async
  iterables are collected into an assistant message. `registerProvider(name,
  config)` also reuses the `models.json` provider-config parser to add or
  override model catalog entries on the session registry, including dynamic
  registration and explicit model removal on unregister.
- Extension provider streaming slice: `stream`/`streamSimple` providers now emit
  token-level incremental events. The Node bridge writes out-of-band
  `provider_chunk` messages as it drains the handler's async iterable — passing
  through `AssistantMessageEvent`-shaped chunks (minus the terminal
  start/done/error) and synthesizing `text_start`/`text_delta`/`text_end` plus
  `toolcall_start`/`toolcall_end` from raw string/object chunks. The Go
  `ProviderStream` routes those to a per-call channel (non-blocking sends drop
  under backpressure since the final reply is authoritative) and maps them onto
  `ai.AssistantMessageEvent`s (`start` once → incremental deltas → terminal
  `done`/`error`) with a running `Partial`. The integer-id reply still drives the
  authoritative final message/usage/stopReason (identical to the prior
  collect-to-final behavior), and cancelling the Go context aborts the Node
  provider's `AbortSignal`.
- Extension custom-message slice: script `pi.registerMessageRenderer` /
  `unregisterMessageRenderer` registers renderers into the Go extension runner,
  and `pi.sendMessage` appends `custom_message` session entries. Renderer
  handlers receive custom message content/details plus `{expanded,width}` and can
  return strings, line arrays, `{lines}`, or virtual TUI objects with `render()`;
  the Bubble TUI now handles `triggerTurn` by starting an empty follow-on turn
  when idle or queuing one while busy. Display custom messages now render into the
  live interactive transcript (`renderCustomMessageLines`, via an
  `extensionCustomMessageHandler` that the Bubble TUI registers): the registered
  renderer's lines with **ANSI styling preserved** — `messageRendererTheme` emits
  real SGR codes (bold/dim/italic/underline exact; a fixed 16-color table
  approximates semantic `fg`/`bg`, degrading to no-op on unknown names) and the
  `Box` shim applies its style function per line — rendered once on receipt and
  honoring the shared expand/collapse, or a default bold `[customType]` + markdown
  fallback when no renderer is registered or it returns empty/errors. Renderer
  output is still flattened to ANSI lines (parity with TS `Component.render` →
  `string[]`); the theme color table approximates the live theme and
  markdown-inside-a-renderer is not re-parsed. Hosts without a trigger binding
  still report `triggerTurn` as unhandled.

Still incomplete versus TS:

- full interactive TUI mode parity
- exact autocomplete behavior parity for visual-wrap cursor edge cases
- complete TS transcript renderer parity beyond the first live tool/bash slice:
  built-in/custom tool renderers, inline images, code syntax highlighting,
  richer diff intraline highlighting, and component-level output framing
- remaining package manager edge cases around lockfile policy and advanced
  manifest glob/filter parity. The npm project layout (`ensureNpmProject` /
  `ensureGitIgnore`), the `PI_OFFLINE` skip on missing-source install, the
  ref-bearing git update (`fetch` + `reset --hard FETCH_HEAD^{commit}` +
  `clean -fdx`), and the pinned-npm cache-hit gate are now implemented. Still
  deferred: the temporary-extension-source hashing layout and its network
  refresh (TS `getTemporaryDir` / `refreshTemporaryGitSource`), which Go lacks an
  equivalent for; and the no-ref git update path still uses `git pull --ff-only`
  rather than deriving the origin-HEAD/upstream target (`getLocalGitUpdateTarget`).
- install/update telemetry beyond version checking
- full OAuth login UI wiring in interactive mode beyond the core `packages/ai`
  provider flows
- remaining pixel-level and advanced JavaScript parity with the TypeScript HTML
  viewer, including full tree branching behavior, vendored marked/highlight
  rendering, custom pre-rendered tool HTML, and theme injection
- Windows self-update behavior

Intentional behavioral divergences (safer or platform-specific; documented rather than aligned):

- **Atomic write/edit (hardlinks/ownership)**: the `write` and `edit` tools write
  via `atomicWriteFile` (temp file in the target dir, `EvalSymlinks`, then rename),
  whereas TS writes in place (`fsWriteFile`). The Go approach is more crash-safe,
  but `rename` swaps the inode, so it breaks hardlinks to the target, drops the
  original inode's xattrs/ACLs, and (running as root) the replacement file's owner
  may differ. Symlinks are preserved (the target is resolved before rename). This
  is a deliberate robustness trade-off, not an oversight (parity review topic 8
  P2-4). Two further consequences of writing via a temp file in the target
  directory rather than in place. Both consequences below are now aligned with TS
  (parity review topic on `atomicWriteFile` EACCES semantics):
  - **Writable file inside a read-only directory**: TS's in-place `fsWriteFile`
    succeeds (the directory's mode does not block rewriting an existing writable
    file). `os.CreateTemp(dir)` cannot create a temp sibling in a read-only dir,
    so `atomicWriteFile` now falls back to an in-place `os.WriteFile(target, …)`
    when `CreateTemp` fails with a permission/read-only error
    (`EACCES`/`EROFS`/`ENOTDIR`/`EPERM`), restoring TS's success path
    (`file_mutation_queue.go`, `isWriteFallbackError`). This trades the
    atomic-rename crash-safety for that read-only-dir corner case only.
  - **Read-only target file (`0o444`)**: TS `edit` surfaces a friendly
    `Could not edit file: <path>. Error code: EACCES.` for an unwritable target.
    `edit.go` now runs a `W_OK` preflight (`checkWritable`, an `O_WRONLY` probe)
    after reading the file, so a read-only-but-readable target reports the same
    `EACCES` message instead of being silently overwritten via the temp-file
    replace. This matches TS's access-then-write (with the same accepted TOCTOU
    race); tests skip where `chmod` is not enforced (root).
- **`write` byte count**: "Successfully wrote N bytes" reports the raw UTF-8 byte
  length in Go vs the JS UTF-16 code-unit length in TS, so N differs for non-ASCII
  content (parity review topic 4 P2-5).
- **System prompt shape (full parity)**: project context files and skills are now
  injected with the TS XML shapes — `<project_context>` /
  `<project_instructions path="...">` (system-prompt.ts:58-67) and
  `<available_skills>` with `<name>/<description>/<location>` and the TS
  `escapeXml` entities (skills.ts `formatSkillsForPrompt`) — replacing the old
  markdown headings. One small message-format divergence: invalid YAML
  frontmatter in a SKILL.md surfaces as a `parse_failed` diagnostic whose message
  comes from `gopkg.in/yaml.v3` (e.g. `yaml: line 1: did not find expected ',' or
  ']'`), whereas the TS loader emits a message containing `at line`. The
  diagnostic is still emitted and the skill is still skipped, so behavior matches;
  only the exact wording differs (asserted via the "line" substring in
  `skills_test.go TestLoadSkillsEmitsInvalidNameYamlAndLongNameDiagnostics`).
  The default-prompt structure now matches TS buildSystemPrompt
  (system-prompt.ts:130-147): the lead paragraph, a one-line `Available tools:`
  list built from per-tool `toolSnippets`, a deduped `Guidelines:` section
  (bash-only-file-ops rule first, then per-tool `promptGuidelines` in registration
  order `read,bash,edit,write,grep,find,ls`, then the two always-on bullets), and
  a `Pi documentation:` block with absolute README/docs/examples paths
  (`core.ReadmePath`/`DocsPath`/`ExamplesPath`). A custom prompt
  (`--system-prompt` or a loaded `SYSTEM.md`) replaces all three sections, as in
  TS. Per-tool snippets/guidelines live on the tools via
  `core/tools.PromptMetadata`; the builder (`core/tools.go ToolPromptInfoFor` →
  `core/resources.go BuildSystemPrompt`/`defaultPromptBody`) preserves
  registration order rather than sorting. Byte-shape is locked by
  `TestBuildSystemPromptDefaultGoldenShape`.
- **`grep` engine**: the grep tool now prefers a local `rg` binary (agent bin dir
  then PATH), shelling out with the same flags as the TypeScript tool
  (`--json --line-number --color=never --hidden`) so traversal/ignore semantics
  and the Rust regex engine match exactly. When `rg` is absent and an agent bin
  dir is available, the managed tool resolver mirrors TS `ensureTool("rg")`:
  it downloads the latest supported GitHub release into `<agentDir>/bin` with a
  lockfile and atomic install. `PI_OFFLINE=1|true|yes`, unsupported platforms,
  or download failures fall back to a Go RE2 walk with an `engineFallback`
  detail and visible notice. The compile-error message names the active engine
  so a pattern that behaves differently than the TS CLI is explainable. Neither
  engine supports look-around or backreferences (ripgrep would need `--pcre2`,
  which the TS CLI does not pass), so that earlier "advanced regex works in TS"
  note was inaccurate. The Go path also always excludes `.git` via `--glob
  '!.git'` (ripgrep 14 does not skip it under `--hidden`), matching the RE2
  fallback and never surfacing git internals.
- **`find` engine**: the find tool now prefers a local `fd` binary (agent bin dir,
  PATH `fd`, then PATH `fdfind`) with TS-style hidden/glob output. Missing `fd`
  is resolved through the same managed GitHub-release download path as TS
  `ensureTool("fd")` (including the macOS x64 `10.3.0` pin); download/offline
  failures keep the Go `walkFiltered` fallback but include an `engineFallback`
  detail and visible notice. Covered by `TestFindToolUsesFdWhenAvailable` and
  `TestManagedToolDownloadsIntoCache`.

## Accepted Intentional Divergences

These are deliberate, tested differences from the TypeScript source. They are not
bugs and should not be "fixed" toward TS without a corresponding decision.

- **`packages/coding-agent/core` god-package split is DEFERRED**: the upstream TS
  `coding-agent` is many small modules; the Go port concentrates session/runtime/
  modes/export/compaction/config into one large `core` package held at an explicit
  `scripts/check_arch.go` line/file budget (ratcheted with a rationale per slice).
  Splitting it into subpackages (export / compaction / share / config / session /
  modes) is intentionally postponed until behavioral parity stabilizes: doing it
  concurrently with parity feature work would mix large mechanical moves with
  behavior changes, and `packages/coding-agent` forbids the type aliases (P6) a
  staged split would lean on. The budget is the holding mechanism; the split is a
  dedicated, behavior-preserving effort to be scheduled once the extension-UI /
  renderer / provider-streaming surfaces settle. Tracked here so future agents do
  not repeatedly re-litigate the same decision.

- **`SanitizeBinaryOutput` preserves a real U+FFFD**: a legitimate U+FFFD
  (REPLACEMENT CHARACTER, valid `EF BF BD`) in tool output is preserved; only
  genuinely invalid bytes — which Go decodes as `utf8.RuneError` with size 1 — are
  dropped. All three implementations decode byte-wise (`utf8.DecodeRuneInString`
  plus a `size == 1` check) and are kept in sync: `utils.go`,
  `core/tools/bash_executor.go` (the path `ExecuteBash` actually uses), and
  `packages/agent/harness/utils` `SanitizeShellBinaryOutput`. Regression test:
  `core/tools/bash_executor_test.go` `TestSanitizeBinaryOutputPreservesRealReplacementChar`.

- **Multibyte-rune reassembly lives in the execution env, not in
  `ExecuteShellWithCapture` (matches TS)**: a multibyte rune split across raw
  byte chunks is reassembled by `LocalExecutionEnv`'s `executionStreamWriter`
  (`splitTrailingPartialRune` + `flush` in `packages/agent/harness/env/local.go`),
  which buffers a trailing partial rune so the `OnStdout`/`OnStderr` callback
  never observes a split rune. This mirrors the TS NodeJS env reading stdout with
  `setEncoding("utf8")` (a `StringDecoder` that buffers incomplete multibyte
  sequences), so TS's `executeShellWithCapture.onChunk` likewise only ever sees
  whole code points. Neither TS `executeShellWithCapture` nor Go
  `ExecuteShellWithCapture` reassembles runes itself — both rely on the env's
  decoder, so this is parity, not a divergence. If a caller bypasses the env
  decoder and feeds raw split fragments straight into `OnStdout`, each fragment is
  sanitized independently: the lead and continuation bytes each decode as
  `utf8.RuneError` (size 1) and are dropped by `SanitizeShellBinaryOutput`, so the
  rune is lost (it is not surfaced as U+FFFD). Covered at the env layer by
  `packages/agent/harness/env/local_test.go`
  `TestExecutionStreamWriterReassemblesSplitRune`/`TestSplitTrailingPartialRune`,
  and at the capture-boundary layer by `packages/agent/harness/utils`
  `TestExecuteShellWithCaptureHandlesMultibyteSplitAcrossChunks`.

- **Faux provider supports per-registration state (matches TS)**: the faux
  provider (`provider_faux.go`) carries its scripted response queue and call
  counter per instance, mirroring the TS `registerFauxProvider` per-registration
  state. `ai.RegisterFauxProvider()` mints a private instance under a unique API
  (with `Provider` kept as `"faux"` so the always-available auth/availability
  gates still apply) and returns a handle exposing `SetResponses`/`AppendResponses`/
  `ResetResponses`/`CallCount`/`PendingResponseCount` plus `Unregister`; this is
  the path for `t.Parallel()` subtests and multiple concurrent scripts, with no
  crosstalk (`provider_faux_test.go` `TestFauxTwoInstancesIsolated`,
  `TestFauxParallelSubtestsNoCrosstalk`). The package-level
  `ai.SetFauxResponses`/`AppendFauxResponses`/`ResetFauxResponses`/
  `PendingFauxResponseCount`/`FauxCallCount` functions remain as thin shims over a
  shared process-wide default instance (the builtin `faux/faux` model) for serial
  tests that script the builtin without a registry handle; those still isolate
  with `defer ai.ResetFauxResponses()` (`TestFauxDefaultInstanceShimsStillWork`).

- **Compaction `SummaryMaxChars` is a Go-only safety truncation**: the TS
  `CompactionSettings` has no summary-length cap; the Go harness adds a defensive
  rune-safe truncation of the generated summary (`SummaryMaxChars`) so a runaway
  summary cannot blow up the context. The truncation is rune-aware (never splits a
  multi-byte rune). This field has no TS counterpart by design.

- **Anthropic malformed `input_json_delta` repair is delta-level, not
  accumulator-level**: the TS port parses raw Anthropic SSE through its own
  `iterateAnthropicEvents`, so a malformed streamed tool-call argument (e.g. an
  invalid `\H` string escape or a raw tab inside `partial_json`) is repaired at
  finalize and surfaces as a valid `toolUse` result. The Go port consumes
  Anthropic streams through the official `anthropic-sdk-go` accumulator
  (`provider_anthropic.go` `accumulated.Accumulate(event)`), which re-marshals the
  accumulated `Message` and rejects an invalid escape before the port's own repair
  code runs — so an end-to-end stream carrying that exact malformed delta fails
  with a marshal error rather than returning a repaired `toolUse`. The repair the
  Go port owns lives in the streamed-delta path: `applyAnthropicDelta`
  (`provider_anthropic.go:202`) feeds each `input_json_delta` through
  `StreamingToolArguments` -> `ParseStreamingJSON` -> `RepairJSON`
  (`utils/json_parse.go`), which does repair `\H`/raw-tab into
  `{"path":"A\\H","text":"col1\tcol2"}`. The "ignore unknown events after
  `message_stop`" behavior matches TS (the SDK stops iterating at `message_stop`).
  Both halves are locked by `provider_anthropic_sse_parsing_test.go`
  `TestAnthropicRawSSEMalformedJSONRepairAndPostStop` (the repair subtest drives
  `applyAnthropicDelta` directly; the post-stop subtest drives a full fake SSE
  body).

- **Harness hook dispatch is chain/merge, not last-wins**: for the emitHook-class
  hooks (`context`, `tool_call` [short-circuits on a block result], `tool_result`,
  `before_agent_start`, `session_before_compact`, `session_before_tree`) the Go
  harness runs every registered handler and chains/merges their results, an
  intentional and tested Go design. This differs from TS, where "the last
  non-undefined result wins". The Go behavior lets multiple security/transform
  extensions cooperate on the same event rather than silently clobbering each
  other. Locked by golden tests in `packages/agent/harness/hooks_test.go`:
  `TestHarnessBeforeAgentStartHooksChainAndAppend` (SystemPrompt last-non-empty,
  Messages appended in order), `TestHarnessToolResultHooksChainPatches` (patch
  chaining), and `TestHarnessToolCallHookShortCircuitsOnBlock` (block
  short-circuits remaining handlers).

- **Generated model catalog is regenerated from the TS source of truth**: the Go
  text and image catalogs (`packages/ai/models_generated.go`,
  `packages/ai/image_models_generated.go`) are generated, never hand-edited, by
  `node packages/ai/scripts/generate-go-models.ts`. The script imports the
  committed TS runtime objects (`MODELS` / `IMAGE_MODELS`) from
  `$PI_TS_AI_SRC` (default `/root/guanshan/pi/packages/ai/src`) so the baked
  compat / `thinkingLevelMap` and derived `ThinkingLevels` stay byte-identical to
  TS. **Regeneration flow whenever the upstream TS catalog is bumped:**

  ```bash
  cd packages/ai && PI_TS_AI_SRC=/root/guanshan/pi/packages/ai/src \
    node scripts/generate-go-models.ts
  gofmt -w models_generated.go image_models_generated.go
  go test ./packages/ai/ -run TestGeneratedTextModelCatalog
  ```

  Catalog version: **964 text models / 29 image models** (the bump from 923 added
  the `nvidia`, `ant-ling`, and `zai-coding-cn` providers plus their baked
  compat). `TestGeneratedTextModelCatalogMatchesTS` is a drift check: it parses
  the TS `models.generated.ts` `(provider, id)` set and asserts equality with the
  Go catalog (skipping gracefully when the TS checkout is unreachable, e.g. CI
  without the sibling repo). It replaces the old `len(...) == 923` magic-number
  assertion that silently masked the missing 41 models / 3 providers. When the
  catalog is regenerated, also confirm `providers/env.go` `ProviderEnvKeys`,
  `cmd`/`coding-agent` help text, and `detectOpenAICompletionsCompat` (`types.go`)
  cover any newly-introduced provider.

- **Provider env-key lookup mirrors TS `env-api-keys.ts` 1:1, with one
  resolution-only superset**: `ProviderEnvKeys` (`packages/ai/providers/env.go`)
  is a 1:1 port of `getApiKeyEnvVars` in TS `env-api-keys.ts`. Notable
  consequences of the parity correction:
  - `google` reads only `GEMINI_API_KEY` (the prior Go-only `GOOGLE_API_KEY`
    fallback was removed).
  - `kimi-coding` reads only `KIMI_API_KEY` (the prior `MOONSHOT_API_KEY`
    fallback and the plain `kimi` alias, neither of which exists in TS, were
    removed).
  - `amazon-bedrock` is **not** enumerated here — its bearer token and ambient
    AWS credentials are resolved via `BedrockEnvCredentials` / `ambientAuthLabel`
    (matching TS `getEnvApiKey`'s ambient branch), and `azure-openai` (only the
    `-responses` variant is mapped in TS) and `openai-codex` (OAuth-only) are
    likewise absent.
  - Unknown/custom providers return an **empty** slice and do not implicitly
    resolve a synthesized `<PROVIDER>_API_KEY`, matching TS `findEnvKeys`
    returning `undefined`. Custom providers can still opt into a key by declaring
    an explicit model `EnvKey`. Locked by `env_api_keys_per_provider_test.go` and
    `auth_storage_test.go` `TestProviderEnvKeysDefaultFallback`.

## Verification Commands

Current broad checks:

```bash
go test ./...
go vet ./...
go run ./cmd/pi --version
go run ./cmd/pi --model faux/faux --no-session -p 'packages smoke'
```

These checks prove the current Go package structure builds and the implemented
surface works. They do not prove full TypeScript parity for the incomplete items
above.

CI matrix note: `go test -race` runs on `ubuntu-latest` and `macos-latest` only;
`windows-latest` runs a plain `go test` (no `-race`) because the Go race detector
needs a cgo/C toolchain that the Windows runner image does not provide. Concurrency
regressions are therefore gated on the POSIX runners; Windows validates functional
behavior without the race detector (`.github/workflows/ci.yml`).
