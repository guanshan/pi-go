# pi-go

[![CI](https://github.com/guanshan/pi-go/actions/workflows/ci.yml/badge.svg)](https://github.com/guanshan/pi-go/actions/workflows/ci.yml)
[![Lint](https://github.com/guanshan/pi-go/actions/workflows/lint.yml/badge.svg)](https://github.com/guanshan/pi-go/actions/workflows/lint.yml)
[![CodeQL](https://github.com/guanshan/pi-go/actions/workflows/codeql.yml/badge.svg)](https://github.com/guanshan/pi-go/actions/workflows/codeql.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/guanshan/pi-go.svg)](https://pkg.go.dev/github.com/guanshan/pi-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/guanshan/pi-go)](https://goreportcard.com/report/github.com/guanshan/pi-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Go implementation of the Pi coding agent CLI — a port of
[badlogic/pi-mono](https://github.com/badlogic/pi-mono) (TypeScript).
Compatibility with the upstream project — behavior, on-disk session format, and
tool semantics — is an explicit goal that is still being worked toward, not a
finished guarantee; tracked parity status lives in
[docs/TS_COMPATIBILITY.md](docs/TS_COMPATIBILITY.md).

> **⚠️ Alpha — porting in progress.** This is not yet a 1:1 replacement for the
> TypeScript CLI. Core agent loop, providers, tools, and non-interactive modes
> work, but several areas are still being ported (full interactive-UI parity,
> running `.ts`/`.js` extensions, some OAuth/transport edges). See
> [docs/TS_COMPATIBILITY.md](docs/TS_COMPATIBILITY.md) for the current gaps
> before relying on it as a drop-in replacement.

This port now mirrors the TypeScript monorepo package layout:

- `packages/ai`: shared LLM types, model registry, env API keys, provider registry, text/image APIs
- `packages/agent`: generic agent state, loop, events, queues, tools, and harness helpers
- `packages/tui`: terminal primitives, input/key parsers, leaf components, fuzzy matching, autocomplete (no main renderer — the interactive UI uses Bubble Tea; see [docs/TUI_DESIGN.md](docs/TUI_DESIGN.md))
- `packages/coding-agent`: CLI, session/runtime wrappers, coding tools, RPC/print helpers
- `examples/extensions`: upstream-compatible extension examples and fixtures

The CLI keeps the TypeScript session format and command surface where practical:

- CLI argument parsing compatible with `pi [options] [@files...] [messages...]`
- print, JSON event stream, RPC, and lightweight interactive modes
- JSONL session files with v3 tree entries
- built-in tools: `read`, `write`, `edit`, `bash`, `grep`, `find`, `ls`
- resource loading for `AGENTS.md`/`CLAUDE.md`, `SYSTEM.md`, prompt templates, skills, themes, and package metadata
- OpenAI-compatible, Anthropic, Google, and faux test providers
- package commands: `install`, `remove`, `uninstall`, `list`, `update`, `config`
- HTML session export

## Install

Pre-built binaries for Linux / macOS / Windows on amd64 + arm64 are attached to
each GitHub Release.

```bash
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/guanshan/pi-go/main/scripts/install.sh | bash

# Or with Go
go install github.com/guanshan/pi-go/cmd/pi@latest
```

Windows users: download the matching `pi_*_Windows_*.zip` from the [Releases page](https://github.com/guanshan/pi-go/releases)
and put `pi.exe` somewhere on your `PATH`.

## Build

```bash
make build           # -> ./bin/pi with version metadata stamped in
# or:
go build -o pi ./cmd/pi
```

## Run

```bash
go run ./cmd/pi --help
go run ./cmd/pi --model faux/faux -p "hello"
go run ./cmd/pi --mode json --model faux/faux "hello"
go run ./cmd/pi --mode rpc --model faux/faux --no-session
```

## Package Imports

```go
import (
    codingagent "github.com/guanshan/pi-go/packages/coding-agent"
    "github.com/guanshan/pi-go/packages/agent"
    "github.com/guanshan/pi-go/packages/ai"
    "github.com/guanshan/pi-go/packages/tui"
)
```

## Test

```bash
make test               # go test -race ./... with coverage
./scripts/test.sh       # the full pre-PR gate
make check              # tidy + fmt-check + vet + lint + test
make arch-check         # verify package dependency boundaries
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow, PR
checklist, and parity guidelines.

## License

MIT — see [LICENSE](LICENSE). The upstream TypeScript work it ports from is
also MIT; both copyright lines are preserved. Attribution to the upstream
project is in [NOTICE](NOTICE).

## Compatibility Tracking

See [docs/TS_COMPATIBILITY.md](docs/TS_COMPATIBILITY.md) for the current
package-by-package audit against the TypeScript source.
See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the Go package boundaries.

## Configuration

The Go port uses the same default config locations as the TypeScript CLI:

- global: `~/.pi/agent/settings.json`
- project: `.pi/settings.json`
- sessions: `~/.pi/agent/sessions/`, or `PI_CODING_AGENT_SESSION_DIR`
- agent dir: `PI_CODING_AGENT_DIR`, or `~/.pi/agent`

API keys are read from provider environment variables such as `ANTHROPIC_API_KEY`,
`OPENAI_API_KEY`, `GEMINI_API_KEY`, and the OpenAI-compatible provider variables
listed in `pi --help`.
