# pi-go Architecture

Product target: `pi-go` is a full-parity Go port of upstream Pi, not a
separate compatibility CLI. Any temporary lightweight path should either keep
the public behavior aligned with the TypeScript implementation or be tracked as
unfinished porting work.

`pi-go` mirrors the upstream TypeScript monorepo as one Go module. The target
dependency graph is:

```text
cmd/pi -> packages/coding-agent -> {packages/ai, packages/agent, packages/tui}
packages/agent -> packages/ai
```

The current implementation is still in migration. The runtime path is:

```text
cmd/pi -> packages/coding-agent -> packages/coding-agent/core -> {packages/ai, packages/agent, packages/tui}
```

`packages/agent` and `packages/tui` are wired into `packages/coding-agent`.
`core.AgentSession` delegates the provider turn loop, tool execution loop, and
turn lifecycle events to `packages/agent`; the interactive runtime is a Bubble
Tea program (`core/interactive_tui.go`) using `packages/tui` primitives, while
the full TypeScript-style component tree (rich selectors/dialogs, message cards)
is still being ported.

The repository intentionally has no root `internal/` package. Public packages
own their implementations so downstream Go users can import the same logical
surfaces that exist in the TypeScript packages.

## Packages

- `packages/ai`: shared LLM types, model catalogs, auth storage, provider
  runtime, text completion, image generation, OAuth helpers, validation, and
  provider-facing tool schema generation.
- `packages/agent`: reusable agent loop, event stream, session storage,
  compaction, harness helpers, execution environment, and proxy helpers.
- `packages/tui`: terminal components, key handling, markdown rendering,
  editor/input helpers, image protocol helpers, and terminal capability checks.
- `packages/coding-agent`: CLI/SDK entry, settings, sessions, resource loading,
  package management, export/share, RPC/print/interactive modes, and coding
  agent specific tools.
- `packages/coding-agent/cli`: argument parsing and CLI help text.
- `packages/coding-agent/core/tools`: built-in coding tools.
- `packages/coding-agent/utils`: shared coding-agent utilities that do not need
  session/runtime ownership.

## Boundaries

`cmd/pi` is only a binary entrypoint and build metadata shim. It imports
`packages/coding-agent` and does not contain product logic.

`packages/ai` is the bottom of the dependency graph and must not import other
project packages. `packages/agent` may import `packages/ai` but not
`packages/coding-agent` or `packages/tui`. `packages/tui` is independent.
`packages/coding-agent` may import `ai`, `agent`, `tui`, and its own
subpackages.

The architecture guard enforces package docs, dependency direction, temporary
god-package budgets, and the requirement that `packages/agent` and
`packages/tui` stay wired into `packages/coding-agent` while they are being
integrated more deeply.

Run the architecture guard with:

```bash
go run ./scripts/check_arch.go
```

The same guard is part of `make check` and `./scripts/test.sh`.
