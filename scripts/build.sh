#!/usr/bin/env bash
# Build the pi binary with version metadata stamped in.
# Outputs to ./bin/pi (or bin/pi.exe on Windows under MSYS).

set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

BIN_DIR="${BIN_DIR:-bin}"
mkdir -p "$BIN_DIR"

ext=""
case "$(go env GOOS)" in
  windows) ext=".exe" ;;
esac

out="${BIN_DIR}/pi${ext}"

echo "building ${out} (version=${VERSION} commit=${COMMIT})"
go build \
  -trimpath \
  -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
  -o "$out" \
  ./cmd/pi

echo "ok: $(${out} --version 2>/dev/null || echo built)"
