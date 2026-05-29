# pi-go — common developer tasks.
# Run `make help` for the full list.

SHELL := /bin/bash

# ---- metadata ----------------------------------------------------------------
BINARY      := pi
PKG         := github.com/guanshan/pi-go
CMD_PATH    := ./cmd/pi
BIN_DIR     := bin
DIST_DIR    := dist

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
               -X main.version=$(VERSION) \
               -X main.commit=$(COMMIT) \
               -X main.date=$(DATE)

GO          ?= go
GOFLAGS     ?= -trimpath
TEST_FLAGS  ?= -race -timeout 5m

GOBIN       := $(shell $(GO) env GOPATH)/bin

# ---- default -----------------------------------------------------------------
.DEFAULT_GOAL := help

## help: show this help.
.PHONY: help
help:
	@awk 'BEGIN {FS = ":.*?## "} \
	     /^[a-zA-Z0-9_.-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' \
	     $(MAKEFILE_LIST) | sort

# ---- build / install ---------------------------------------------------------
## build: build the pi binary into ./bin.
.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(CMD_PATH)
	@echo "built $(BIN_DIR)/$(BINARY) ($(VERSION))"

## install: install pi into $$GOPATH/bin.
.PHONY: install
install:
	$(GO) install $(GOFLAGS) -ldflags '$(LDFLAGS)' $(CMD_PATH)
	@echo "installed to $(GOBIN)/$(BINARY)"

## run: build and run the binary with $$ARGS.
.PHONY: run
run: build
	$(BIN_DIR)/$(BINARY) $(ARGS)

# ---- quality gates -----------------------------------------------------------
## test: run unit tests with race detector and coverage.
.PHONY: test
test:
	$(GO) test $(TEST_FLAGS) -coverprofile=coverage.txt -covermode=atomic ./...

## test-short: run only short tests.
.PHONY: test-short
test-short:
	$(GO) test $(TEST_FLAGS) -short ./...

## cover: open coverage.html in your browser.
.PHONY: cover
cover: test
	$(GO) tool cover -html=coverage.txt -o coverage.html
	@echo "wrote coverage.html"

## vet: run go vet.
.PHONY: vet
vet:
	$(GO) vet ./...

## arch-check: verify the target package dependency boundaries.
.PHONY: arch-check
arch-check:
	$(GO) run ./scripts/check_arch.go

## release-hygiene: check release archives and generated-artifact tracking.
.PHONY: release-hygiene
release-hygiene:
	./scripts/check_release_hygiene.sh

## fmt: format all Go files in place.
.PHONY: fmt
fmt:
	gofmt -s -w .
	@command -v goimports >/dev/null 2>&1 && goimports -w . || true

## fmt-check: fail if any file needs formatting.
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -s -l .); \
	if [ -n "$$unformatted" ]; then \
	  echo "::error::needs gofmt -s -w:"; \
	  echo "$$unformatted"; exit 1; \
	fi

## lint: run golangci-lint (installs to $$GOBIN if missing).
.PHONY: lint
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "installing golangci-lint..."; \
	  $(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	}
	golangci-lint run --timeout 5m

## tidy: run go mod tidy and verify.
.PHONY: tidy
tidy:
	$(GO) mod tidy
	$(GO) mod verify

## check: full local pre-PR gate (tidy + fmt-check + arch-check + release-hygiene + vet + lint + test).
.PHONY: check
check: tidy fmt-check arch-check release-hygiene vet lint test

# ---- release -----------------------------------------------------------------
## snapshot: build a goreleaser snapshot locally (no publish).
.PHONY: snapshot
snapshot:
	@command -v goreleaser >/dev/null 2>&1 || { \
	  echo "install goreleaser: https://goreleaser.com/install/"; exit 1; \
	}
	goreleaser release --snapshot --clean

## release-check: validate .goreleaser.yaml.
.PHONY: release-check
release-check:
	@command -v goreleaser >/dev/null 2>&1 || { \
	  echo "install goreleaser: https://goreleaser.com/install/"; exit 1; \
	}
	goreleaser check

# ---- housekeeping ------------------------------------------------------------
## clean: remove build artifacts.
.PHONY: clean
clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) coverage.txt coverage.html
