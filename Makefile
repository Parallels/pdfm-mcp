# pdfm-mcp Makefile
SHELL := /bin/bash
.DEFAULT_GOAL := help

BINARY      := pdfm-mcp
MODULE      := github.com/cjlapao/pdfm-mcp
CMD         := ./cmd/pdfm-mcp
BIN_DIR     := bin
DIST_DIR    := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)

# Debug builds keep symbols and disable optimisation/inlining so Delve works well.
DEBUG_GCFLAGS := all=-N -l
DEBUG_LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION)-debug

PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64

export CGO_ENABLED ?= 0

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n\n"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

##@ Development

.PHONY: tidy
tidy: ## Sync go.mod/go.sum
	go mod tidy

.PHONY: fmt
fmt: ## Format code
	gofmt -s -w .

.PHONY: fmt-check
fmt-check: ## Fail if code is not formatted
	@test -z "$$(gofmt -s -l .)" || (echo "unformatted files:"; gofmt -s -l .; exit 1)

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (must be installed)
	golangci-lint run ./...

.PHONY: test
test: ## Run unit tests with race detector
	CGO_ENABLED=1 go test -race -count=1 ./...

.PHONY: test-e2e
test-e2e: ## Run end-to-end tests (spawns the binary over stdio)
	go test -tags e2e -count=1 ./test/e2e/...

.PHONY: cover
cover: ## Coverage report (coverage.out + summary)
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

.PHONY: check
check: fmt-check vet lint test ## Everything CI runs (minus e2e)

##@ Build & run

.PHONY: build
build: ## Build optimised binary into bin/
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

.PHONY: build-debug
build-debug: ## Build with debug symbols (no optimisation) for Delve
	@mkdir -p $(BIN_DIR)
	go build -gcflags "$(DEBUG_GCFLAGS)" -ldflags "$(DEBUG_LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-debug $(CMD)

.PHONY: run
run: build ## Run the server on the terminal (type JSON-RPC lines manually)
	./$(BIN_DIR)/$(BINARY)

.PHONY: inspect
inspect: build ## Open MCP Inspector against the binary (needs npx)
	./scripts/inspect.sh ./$(BIN_DIR)/$(BINARY)

.PHONY: smoke
smoke: build ## Raw stdio handshake + tools/list, prints server responses
	@{ printf '%s\n%s\n%s\n' \
	 '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
	 '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	 '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; \
	  sleep 1; } | ./$(BIN_DIR)/$(BINARY)

.PHONY: install
install: ## Install into $GOBIN / $GOPATH/bin
	go install -trimpath -ldflags "$(LDFLAGS)" $(CMD)

.PHONY: build-all
build-all: ## Cross-compile for all PLATFORMS into dist/
	@mkdir -p $(DIST_DIR)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; [ $$os = windows ] && ext=".exe"; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(DIST_DIR)/$(BINARY)-$$os-$$arch$$ext $(CMD) || exit 1; \
	done

##@ Integration

.PHONY: claude-add
claude-add: build ## Register the local binary with Claude Code
	claude mcp add pdfm-mcp -- $(abspath $(BIN_DIR)/$(BINARY))

.PHONY: claude-remove
claude-remove: ## Unregister from Claude Code
	claude mcp remove pdfm-mcp

##@ Housekeeping

.PHONY: clean
clean: ## Remove build outputs
	rm -rf $(BIN_DIR) $(DIST_DIR) coverage.out
