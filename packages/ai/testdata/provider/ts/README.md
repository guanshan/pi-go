These fixtures are the TypeScript-provider reference payloads consumed by the
Go cross-language provider parity tests.

They are intentionally separate from `../*.golden.json`: the golden files are
Go regression snapshots that `UPDATE_GOLDEN=1` may refresh, while files in this
directory are only updated from the upstream TypeScript request builders or a
capture script. This keeps the Go tests from proving parity by comparing Go
output back to Go output.

## Provenance of individual fixtures

Each fixture below is hand-derived field-by-field from the upstream TypeScript
request builder cited next to it (path relative to `../pi`), not copied from the
Go snapshot. The Go `*_golden_test.go` re-derives the same payload through
`registry.StreamlessChat` and asserts byte-equality (after JSON
canonicalization) against the fixture here; a diff is a real parity finding.

The Go non-streaming golden harness omits the TypeScript `stream: true` field on
Anthropic requests (the StreamlessChat path is non-streaming), matching the
pre-existing `anthropic_messages_payload` / `anthropic_adaptive_thinking_payload`
fixtures.

- `anthropic_image_content_payload.golden.json` —
  `packages/ai/src/providers/anthropic.ts` `convertContentBlocks` (lines
  110-157). A user message with a text + image block becomes a content-block
  array; the image is `{type:"image", source:{type:"base64", media_type, data}}`.
- `anthropic_thinking_budget_payload.golden.json` —
  `packages/ai/src/providers/anthropic.ts` (lines 935-974). A reasoning model
  with `forceAdaptiveThinking` unset and a thinking budget emits
  `thinking:{type:"enabled", budget_tokens, display:"summarized"}`; temperature
  is dropped because extended thinking is incompatible with it. `budget_tokens`
  (12000) is the budget the request resolves for level `high` from the supplied
  `ThinkingBudgets`.
- `openai_chat_tool_result_images_payload.golden.json` —
  `packages/ai/src/providers/openai-completions.ts` (lines 918-983), porting
  `packages/ai/test/openai-completions-tool-result-images.test.ts`. Two
  consecutive tool results with text + image produce two `role:"tool"` messages
  followed by ONE synthetic trailing `role:"user"` message batching every
  `image_url` part behind the `"Attached image(s) from tool result:"` text part.
  The tool-call-only assistant message has `content: null` and an empty `tools`
  array is sent because the conversation has tool history.
- `openai_responses_image_content_payload.golden.json` —
  `packages/ai/src/providers/openai-responses-shared.ts` (lines 143-155). A user
  image becomes `{type:"input_image", detail:"auto", image_url:"data:..."}`.
- `openai_responses_tool_result_images_payload.golden.json` —
  `packages/ai/src/providers/openai-responses-shared.ts` (lines 221-260). When
  the model supports image tool-result input, a text + image tool result becomes
  a `function_call_output` whose `output` is an `[input_text, input_image]`
  content array, and an image-ONLY tool result becomes `[input_image]` (the
  image is still attached). The `"(see attached image)"` string fallback in that
  same branch only fires for the model-does-not-support-image-input path, which
  this image-capable fixture does not exercise; it is covered structurally by the
  Go provider unit tests.
- `openai_responses_reasoning_replay_payload.golden.json` —
  `packages/ai/src/providers/openai-responses-shared.ts` (lines 171-217). A
  same-model assistant turn replays a thinking block whose signature is an
  encrypted `reasoning` item (emitted verbatim) and a following tool call whose
  id `call_x|fc_x` is split into `call_id:"call_x"` + item `id:"fc_x"`.
