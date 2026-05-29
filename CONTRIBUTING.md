# Contributing to pi-go

Thanks for your interest. pi-go is a Go port of [badlogic/pi-mono](https://github.com/badlogic/pi-mono) (TypeScript), so contributions usually fall into one of these buckets:

1. **Porting / parity** — translating behavior or modules that don't yet exist on the Go side. See [docs/TS_COMPATIBILITY.md](docs/TS_COMPATIBILITY.md) for what's done and what's pending.
2. **Bug fixes** — Go-side defects, divergences from the TypeScript implementation, or regressions.
3. **Go-specific improvements** — better error handling, idiomatic refactors, performance, tests.

Pure cosmetic refactors are unlikely to land — please open an issue first to confirm interest.

## Before you start

1. Open an issue (use the templates) describing the problem or change. For non-trivial work, wait for a maintainer to confirm direction before writing a lot of code.
2. Make sure your work is rooted in real upstream behavior. If you're adding/changing a tool, protocol, or session-format detail, link the upstream TypeScript source in your PR description.
3. AI-generated code is fine, but **you must understand and be able to defend every line**. PRs that look like raw model output without curation will be closed.

## Development setup

Requirements:

- Go (version pinned in [`go.mod`](go.mod))
- `make`, `bash`, `git`
- Optional: `golangci-lint`, `goreleaser` (for local release checks)

Common tasks:

```bash
make build         # compile to ./bin/pi
make test          # go test -race with coverage
make lint          # run golangci-lint (auto-installs if missing)
make fmt           # gofmt -s -w .
make check         # full pre-PR gate: tidy + fmt-check + vet + lint + test
make snapshot      # local goreleaser snapshot build (no publish)
```

The same checks can be run via plain scripts:

```bash
./scripts/test.sh           # tidy + fmt-check + vet + test
./scripts/test.sh --lint    # ... plus golangci-lint
./scripts/build.sh          # stamped binary into ./bin
```

## PR checklist

Before opening a PR:

- [ ] `make check` passes locally
- [ ] New behavior has a test
- [ ] If you ported logic from TypeScript, the PR description links the upstream file(s)
- [ ] Commit messages are descriptive (`feat: ...`, `fix: ...`, `port: ...` etc. are fine but not required)
- [ ] You did not commit secrets, API keys, recorded sessions, or large binaries

CI will re-run all of those plus a 3-platform build matrix (Linux, macOS, Windows) and CodeQL.

## Coding conventions

- Standard `gofmt -s` + `goimports`. The lint workflow enforces both.
- Prefer the standard library and the modules already in `go.mod`. Adding a new dependency needs justification in the PR.
- Avoid breaking on-disk session compatibility with TypeScript pi without explicit discussion — the JSONL session format is shared.
- Public API in `packages/...` should match TypeScript naming intent where reasonable, but use idiomatic Go (CamelCase exported names, error returns over throws, contexts where they belong).
- Don't add error handling, fallbacks, or validation for impossible cases. Validate at boundaries (CLI input, network, file I/O); trust internal callers.

## Licensing

By contributing you agree your changes are released under the [MIT License](LICENSE). The repository carries dual copyright: upstream TypeScript work belongs to Mario Zechner; the Go port is copyright guanshan. Both notices stay in [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Reporting security issues

Please do **not** open a public issue for security problems. Instead use GitHub's [private vulnerability reporting](https://github.com/guanshan/pi-go/security/advisories/new) on this repo.
