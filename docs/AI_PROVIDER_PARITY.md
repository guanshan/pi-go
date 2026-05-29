# AI Provider Parity Tracking

The Go `packages/ai` layer is intentionally close to the upstream TypeScript
`packages/ai`, but provider behavior is broad enough that full parity needs a
repeatable inventory rather than one-off review notes.

Run the local report with the upstream TypeScript repository next to this repo:

```bash
./scripts/ai_provider_parity_report.sh
```

If the TypeScript repository is elsewhere:

```bash
PI_TS_ROOT=/path/to/pi ./scripts/ai_provider_parity_report.sh
```

The report is non-gating. It summarizes source/test coverage by provider area
and calls out known gaps. The current highest-risk item is OpenAI Codex
Responses transport: `websocket` and `websocket-cached` are accepted by pi-go
but fall back to SSE with a `provider_transport_fallback` diagnostic; this is
usable compatibility, not native WebSocket/cached transport parity.

Next provider parity work should convert upstream TypeScript fixtures into Go
golden tests for:

- stream event ordering and partial JSON cleanup
- tool call/result IDs and tool-result image routing
- reasoning/thinking/cache-control fields
- OAuth and API-key/env resolution edge cases
- context overflow and retry classification
- model catalog metadata such as context windows, cost, cache, and thinking
  support
