#!/usr/bin/env bash
# Emit a lightweight AI provider parity inventory against the TypeScript source.
#
# Usage:
#   PI_TS_ROOT=/path/to/pi ./scripts/ai_provider_parity_report.sh > report.md
#
# The script is intentionally non-gating: it highlights coverage gaps so follow-up
# work can pick concrete provider/test areas without blocking ordinary CI.

set -euo pipefail

cd "$(dirname "$0")/.."

ts_root="${PI_TS_ROOT:-../pi}"
ts_ai="${ts_root}/packages/ai"
go_ai="packages/ai"

if [[ ! -d "$ts_ai" ]]; then
  cat >&2 <<EOF
TypeScript AI package not found at: $ts_ai
Set PI_TS_ROOT to the upstream Pi repository path.
EOF
  exit 2
fi

count_files() {
  find "$1" -type f "$@" 2>/dev/null | wc -l | tr -d ' '
}

echo "# AI Provider Parity Report"
echo
echo "- TypeScript source: \`$ts_ai\`"
echo "- Go source: \`$go_ai\`"
echo "- Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo
echo "## Source Inventory"
echo
echo "| Area | TypeScript files | Go files |"
echo "| --- | ---: | ---: |"
for area in openai anthropic google bedrock mistral cloudflare openrouter oauth images models stream validation overflow; do
  ts_count=$(find "$ts_ai/src" "$ts_ai/test" -type f -iname "*${area}*" 2>/dev/null | wc -l | tr -d ' ')
  go_count=$(find "$go_ai" -type f -iname "*${area}*" 2>/dev/null | wc -l | tr -d ' ')
  echo "| $area | $ts_count | $go_count |"
done

echo
echo "## Test File Coverage"
echo
echo "| Topic | TS tests | Go tests | Notes |"
echo "| --- | ---: | ---: | --- |"
for topic in \
  openai-codex \
  openai-responses \
  openai-completions \
  anthropic \
  google \
  bedrock \
  mistral \
  cloudflare \
  openrouter \
  oauth \
  context-overflow \
  images \
  cache \
  thinking \
  tool; do
  go_topic=${topic//-/_}
  ts_tests=$(find "$ts_ai/test" -type f -iname "*${topic}*.test.ts" 2>/dev/null | wc -l | tr -d ' ')
  go_tests=$(find "$go_ai" -type f -iname "*${go_topic}*_test.go" 2>/dev/null | wc -l | tr -d ' ')
  note=""
  if [[ "$topic" == "openai-codex" ]]; then
    note="Go websocket/websocket-cached currently falls back to SSE."
  elif [[ "$go_tests" == "0" && "$ts_tests" != "0" ]]; then
    note="No obvious filename-level Go fixture."
  fi
  echo "| $topic | $ts_tests | $go_tests | $note |"
done

echo
echo "## Known Follow-Ups"
echo
echo "- Implement or formally document native OpenAI Codex WebSocket/cached transport parity."
echo "- Turn high-value TypeScript stream/tool-call fixtures into Go request/response golden tests."
echo "- Diff model catalog metadata for context windows, cost, thinking support, cache controls, and env/API-key resolution."
echo "- Add per-provider wire-shape tests for images, tool result images, reasoning/thinking fields, retry, and overflow errors."
