#!/usr/bin/env bash
# Run the pre-PR test gate locally.
#
# Mirrors what CI does on every push:
#   * go mod download + tidy + verify
#   * gofmt -s check
#   * architecture, package-doc, god-package, and wired-in checks
#   * release hygiene
#   * go vet
#   * go build
#   * go test -race with coverage
#
# Optional:
#   --lint   run golangci-lint at the same version as .github/workflows/lint.yml
#   --cross  cross-compile test binaries for Windows/macOS. This does not run
#            those binaries; real platform runtime issues still need those OSes.

set -euo pipefail

cd "$(dirname "$0")/.."

run_lint=false
run_cross=false
for arg in "$@"; do
  case "$arg" in
    --lint) run_lint=true ;;
    --cross) run_cross=true ;;
    -h|--help)
      sed -n '2,16p' "$0"; exit 0 ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

echo "==> go mod download"
go mod download

echo "==> go mod tidy"
go mod tidy
if ! git diff --exit-code -- go.mod go.sum; then
  echo "go.mod or go.sum is not tidy. Commit the tidy result." >&2
  exit 1
fi

echo "==> go mod verify"
go mod verify

echo "==> gofmt check"
unformatted=$(gofmt -s -l .)
if [[ -n "$unformatted" ]]; then
  echo "needs gofmt -s -w:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "==> architecture check"
go run ./scripts/check_arch.go

echo "==> release hygiene"
./scripts/check_release_hygiene.sh

echo "==> go vet ./..."
go vet ./...

echo "==> go build ./..."
go build -v ./...

if [[ "$run_lint" == "true" ]]; then
  echo "==> golangci-lint v2.12.2"
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run --timeout=5m
fi

echo "==> go test -race ./..."
go test -race -timeout 5m -coverprofile=coverage.txt -covermode=atomic ./...

if [[ "$run_cross" == "true" ]]; then
  echo "==> GOOS=windows go test -exec=true ./... (compile-only)"
  GOOS=windows go test -exec=true ./...

  echo "==> GOOS=darwin go test -exec=true ./... (compile-only)"
  GOOS=darwin go test -exec=true ./...
fi

echo
echo "all checks passed."
