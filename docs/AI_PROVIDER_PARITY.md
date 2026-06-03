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
and calls out known gaps. OpenAI Codex Responses now has native `websocket` and
`websocket-cached` transport in pi-go, including connection reuse, cached input
delta requests, per-session debug stats, idle timeout, and pre-stream SSE
fallback with a `provider_transport_failure` diagnostic.

Full request wire-shape goldens now exist for both major providers:
`TestOpenAIChatPayloadGolden` (`openai_chat_payload.golden.json`) and
`TestAnthropicMessagesPayloadGolden` (`anthropic_messages_payload.golden.json`),
covering system blocks, multi-turn tool_use/tool_result content, tools,
tool_choice, and cache_control. The Codex transport contract is covered by
`TestValidateOpenAICodexResponsesTransport` (accept/reject),
`TestOpenAICodexResponsesWebSocketTransportStreams`,
`TestOpenAICodexResponsesWebSocketCachedSendsInputDelta`, and the SSE fallback
diagnostic tests. Regenerate the payload goldens with `UPDATE_GOLDEN=1 go test
./packages/ai/...`.

Several formerly-remaining areas now have upstream TypeScript fixtures converted
into Go tests:

- **Tool-result image routing** for the OpenAI providers is covered by
  `TestOpenAIChatBatchesToolResultImagesIntoTrailingUserMessage`, and the Gemini
  side by `TestGoogleGemini2xSeparateSyntheticImageTurn`.
- **Context overflow and retry classification** is covered by the expanded
  `TestIsContextOverflow*` table in `overflow_test.go`.
- **OAuth and API-key/env resolution edge cases** are covered by
  `TestEnvApiKeyGitHubCopilotOnlyFromCopilotToken` (the Copilot token must not
  leak into the generic OpenAI key resolution).
- **Reasoning/thinking wire-shape goldens** are now real goldens via
  `TestOpenAIResponsesReasoningPayloadGolden` (previously assertion-tested only).
- **Anthropic SSE** stream parsing has dedicated coverage via the Anthropic SSE
  stream test.

Remaining provider parity work should convert more upstream TypeScript fixtures
into Go golden tests for:

- stream event ordering and partial JSON cleanup
- model catalog metadata such as context windows, cost, cache, and thinking
  support
- live Codex WebSocket/cached probes against upstream service behavior

## Targeted parity fixes and intentional divergences

These items were aligned to the upstream TypeScript behavior; the divergences
listed are deliberate and covered by tests.

### OpenAI Chat assistant content default (P2-02a)

`OpenAIChatMessages` (`packages/ai/providers/openai_chat.go`) now mirrors
`openai-completions.ts:816`: the default assistant `content` is `""` when
`RequiresAssistantAfterToolResult` is set and JSON `null` otherwise, and the
default is overwritten only when there is real text/thinking content. Previously
the Go code always seeded `""`, so a tool-call-only assistant message serialized
`"content":""` instead of `"content":null`. Covered by
`TestOpenAIChatAssistantDefaultContentNullVsEmpty`.

### Anthropic stop_reason mapping (P2-02b)

`AnthropicStopReason` (`packages/ai/providers/openai.go`) was realigned to the TS
`mapStopReason` in `packages/ai/src/providers/anthropic.ts`. The previous Go code
rewrote `end_turn`/`pause_turn`/`stop_sequence`/`""` to `"toolUse"` whenever the
message contained tool-call blocks. TS does **not** do this for the Anthropic
provider — the Anthropic API natively returns `tool_use` when the model calls
tools, and the agent loop detects tool calls from content blocks
(`toolCallsFromAssistant`), not from the stop reason. The `hasToolCall` parameter
was therefore removed. This is safe: a `stop` stop-reason is not flagged as an
error by the UI (which keys off `stopReason !== "stop" && stopReason !== "toolUse"`).

**Intentional divergence:** the empty-string reason `""` maps to `"stop"` in Go,
whereas TS `mapStopReason` would throw `Unhandled stop reason`. Streaming
`message_delta` events can carry no `stop_reason`, so mapping `""` to `"stop"`
avoids spurious stream errors. Also, `refusal`/`sensitive` now return `error`
with an empty message (matching TS, which returns `"error"` and lets the caller
supply the message) rather than the previous Go-specific `"Provider stop_reason: …"`
text. Covered by `TestAnthropicStopReasonParity` and `TestAnthropicStopReasonErrors`.

### Bedrock/Vertex ambient auth status (P2-02c)

`AuthStorage.AuthStatus` (`packages/ai/auth_storage.go`) previously only consulted
`ProviderEnvKeys`, so ambient AWS credentials (`AWS_PROFILE`, IAM access/secret
keys, container/web-identity creds) and Google Vertex Application Default
Credentials reported the provider as unconfigured even though `HasAuth`/SigV4
signing would succeed. It now probes those ambient sources via
`ambientAuthLabel` (reusing `BedrockEnvCredentials` / `HasGoogleVertexADC`),
mirroring TS `getEnvApiKey` which resolves them to `"<authenticated>"`. The Go
status reports `Configured: true, Source: "environment"` for ambient auth.
Covered by `TestAuthStatusBedrockAmbientCredentials`. Note: the TS check list
does not include `AWS_ROLE_ARN`, so it is not consulted here.

### Streaming idle timeout (P1-08, ai side)

`ai.ChatRequest` gained an `IdleTimeoutMs int` field, threaded into
`aiproviders.RequestOptions` via `providerRequestOptions`. When
`IdleTimeoutMs > 0`, `NewHTTPClientWithOptions` installs an
`idleTimeoutTransport` that wraps each response body in a per-read idle deadline,
so a stalled stream errors with `stream idle timeout after …` instead of hanging
until the total `TimeoutMs`. The total-request `Timeout` is unchanged. Covered by
`TestIdleTimeoutFiresOnStalledStream`, `TestIdleTimeoutAllowsCompleteStream`, and
`TestNoIdleTimeoutWhenUnset`.

### OAuth login HTTP client injection (P2-04)

`OAuthLoginCallbacks` gained an optional `HTTPClient *http.Client` (default
`http.DefaultClient` via `httpClient()`), threaded through `LoginGitHubCopilot`
and the OpenAI Codex login flows so tests can inject a fake transport. No
existing public signature changed except by additive fields/variadic options.
Covered by `TestLoginGitHubCopilotUsesInjectedClient`,
`TestLoginOpenAICodexDeviceCodeUsesInjectedClient`, and
`TestOAuthCallbacksHTTPClientDefault`.

### Version source of truth (P1-09)

`UpstreamVersion` (`packages/ai/model_runtime.go`) is pinned to the upstream
TypeScript `packages/ai/package.json` version (`0.78.0`); `Version` is
`UpstreamVersion + "-go"`. coding-agent/core derives its version from `ai.Version`.
Covered by `TestVersionSourceOfTruth`.
