#!/usr/bin/env bash
# Run the pre-PR test gate locally.
#
# Mirrors what CI does on every push:
#   * go mod tidy + verify
#   * gofmt -s check
#   * architecture, package-doc, god-package, and wired-in checks
#   * release hygiene
#   * go vet
#   * go test -race with coverage
#
# Optional: pass --lint to also run golangci-lint.

set -euo pipefail

cd "$(dirname "$0")/.."

run_lint=false
for arg in "$@"; do
  case "$arg" in
    --lint) run_lint=true ;;
    -h|--help)
      sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

echo "==> go mod tidy && go mod verify"
go mod tidy
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

if [[ "$run_lint" == "true" ]]; then
  echo "==> golangci-lint run"
  if ! command -v golangci-lint >/dev/null 2>&1; then
    echo "installing golangci-lint..."
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
  fi
  golangci-lint run --timeout 5m
fi

echo "==> go test -race ./..."
go test -race -timeout 5m -coverprofile=coverage.txt -covermode=atomic ./...

echo
echo "all checks passed."
