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
- OpenAI Responses, Azure OpenAI Responses, and OpenAI Codex Responses basic
  transports, including Responses input/tool conversion, prompt-cache/session
  headers, Azure deployment/API-version resolution, Codex account headers,
  Codex SSE parsing, reasoning/include fields, text/thinking signatures, and
  usage/tool-call response parsing
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
  streaming event emission details, Codex WebSocket reuse/fallback, retry
  policy, service-tier pricing, and provider-specific dynamic headers
- Bedrock Converse streaming/SDK edge-case parity and Google Vertex ADC/SDK
  streaming parity
- full interactive OAuth login UI parity in coding-agent TTY/TUI modes
- prompt cache/provider-specific parity outside OpenAI-compatible
  chat/completions
- full TypeBox compiler error wording/localization parity
- image generation providers beyond OpenRouter

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
- generic `Result<TValue,TError>` helpers plus a result-returning
  `ExecutionEnv`/`ResultExecutionEnv` adapter for NodeExecutionEnv filesystem
  and shell operations
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

Implemented in Go:

- Component, Container, Text, TruncatedText, Spacer, Box
- Input/Editor, SelectList, SettingsList
- EditorComponent-style text/callback/history/autocomplete/padding surface for
  custom editor integration
- Loader and CancellableLoader
- Markdown text renderer with headings, paragraphs, fenced code blocks,
  blockquotes, horizontal rules, unordered/ordered lists, pipe tables, inline
  code, bold/italic/strikethrough, links with OSC 8 or URL fallback, padding,
  ANSI/OSC-aware visible width, and image-line preservation
- Image fallback component
- TUI and ProcessTerminal basics
- overlay handles, hide/focus lifecycle, and simple overlay positioning
- exported diff renderer helper
- key parsing, key matching, keybindings manager
- fuzzy matching and autocomplete provider composition
- stdin paste buffer
- undo stack and kill ring
- terminal image dimensions and Kitty/iTerm2 encoders
- terminal image capability detection for Kitty/Ghostty/WezTerm/iTerm2/Windows
  Terminal/VSCode/Alacritty, tmux/screen-safe fallbacks, truecolor and OSC 8
  hyperlink flags, image-line detection, OSC 8 hyperlink helper, and Kitty
  cleanup sequences
- ProcessTerminal lifecycle surface for bracketed paste mode, Kitty protocol and
  xterm modifyOtherKeys toggles, drain/reset handling, OSC 9;4 progress
  keepalive, write-log path resolution, terminal dimensions, and Apple Terminal
  Shift+Enter input normalization
- native modifier helper API with injectable helper and safe fallback
- tests for render, input, settings, paste, keybindings, image dimensions,
  editor component surface, and native modifier fallback

Still incomplete versus TS:

- full differential renderer with cursor parity
- OS raw-mode stdin handling, full Kitty keyboard negotiation, and real stdin
  drain parity
- complete editor behavior and autocomplete dropdown rendering parity
- remaining markdown parser edge cases versus `marked`, nested block/list
  parity, and exact theme/style reset behavior
- packaged macOS native modifier binary loading

## packages/coding-agent

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
  decoding, ANSI stripping, shell environment/config helpers, path
  normalization helpers, abortable sleep, binary-output sanitization, and
  detached-child tracking helpers
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
  slash commands, subscribes to events/hooks, and returns mutation/cancel
  results from before-hooks; settings, rich `ctx.ui`, message renderers, and
  provider/model registration are not yet implemented

Still incomplete versus TS:

- full interactive TUI mode parity
- remaining package manager edge cases around npm project layout, network
  refresh, lockfile policy, package-manager selection, lifecycle scripts, and
  advanced manifest glob/filter parity
- install/update telemetry beyond version checking
- full OAuth login UI wiring in interactive mode beyond the core `packages/ai`
  provider flows
- remaining pixel-level and advanced JavaScript parity with the TypeScript HTML
  viewer, including full tree branching behavior, vendored marked/highlight
  rendering, custom pre-rendered tool HTML, and theme injection
- Windows self-update behavior

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
