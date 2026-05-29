#!/usr/bin/env bash
# Check release/archive hygiene without requiring a local release build.

set -euo pipefail

cd "$(dirname "$0")/.."

fail=false

tracked_artifacts=$(
  git ls-files \
    -- bin dist \
    examples/extensions/doom-overlay/doom/build/doom.js \
    examples/extensions/doom-overlay/doom/build/doom.wasm
)
if [[ -n "$tracked_artifacts" ]]; then
  echo "release hygiene: generated artifacts are tracked:" >&2
  echo "$tracked_artifacts" >&2
  fail=true
fi

for pattern in /bin/ /dist/; do
  if ! grep -Fxq "$pattern" .gitignore; then
    echo "release hygiene: .gitignore must contain $pattern" >&2
    fail=true
  fi
done

if [[ -d bin || -d dist ]]; then
  echo "release hygiene: local bin/ or dist/ exists; run 'make clean' before packaging manually." >&2
  if [[ "${STRICT_LOCAL_RELEASE_ARTIFACTS:-}" == "1" ]]; then
    fail=true
  fi
fi

inspect_archive() {
  local archive="$1"
  local entries
  case "$archive" in
    *.tar.gz|*.tgz) entries=$(tar -tzf "$archive") ;;
    *.zip)
      if ! command -v unzip >/dev/null 2>&1; then
        echo "release hygiene: unzip is required to inspect $archive" >&2
        fail=true
        return
      fi
      entries=$(unzip -Z1 "$archive")
      ;;
    *) return ;;
  esac
  if grep -Eq '(^|/)(\.workspace|\.git|bin|dist)/|examples/extensions/doom-overlay/doom/build/' <<<"$entries"; then
    echo "release hygiene: $archive contains local or generated paths:" >&2
    grep -E '(^|/)(\.workspace|\.git|bin|dist)/|examples/extensions/doom-overlay/doom/build/' <<<"$entries" >&2
    fail=true
  fi
}

if [[ -d dist ]]; then
  while IFS= read -r -d '' archive; do
    inspect_archive "$archive"
  done < <(find dist -type f \( -name '*.tar.gz' -o -name '*.tgz' -o -name '*.zip' \) -print0)
fi

if [[ "$fail" == "true" ]]; then
  exit 1
fi
