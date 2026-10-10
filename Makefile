# pdfm-mcp Makefile
SHELL := /bin/bash
.DEFAULT_GOAL := help

BINARY      := pdfm-mcp
MODULE      := github.com/cjlapao/pdfm-mcp
CMD         := ./cmd/pdfm-mcp
BIN_DIR     := bin
DIST_DIR    := dist

VERSION ?= $(shell cat VERSION)-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Telemetry (spec 024): the Amplitude key is baked in at build time, like
# the version. Empty (the default) = telemetry disabled. It is always
# exported — even empty — because the goreleaser ldflags template requires
# the variable to exist ({{ .Env.AMPLITUDE_API_KEY }} errors on a missing
# key, not an empty one).
AMPLITUDE_API_KEY ?=
export AMPLITUDE_API_KEY

LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE) \
	-X $(MODULE)/internal/telemetry.APIKey=$(AMPLITUDE_API_KEY)

# Debug builds keep symbols and disable optimisation/inlining so Delve works well.
DEBUG_GCFLAGS := all=-N -l
DEBUG_LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION)-debug

# macOS-only product: OCR is Apple Vision compiled in via cgo (spec 022),
# so there is no non-darwin build path.
PLATFORMS := darwin/arm64 darwin/amd64

export CGO_ENABLED ?= 1

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

##@ Release

.PHONY: version version-get
version: ## Show the base release version
	python3 -B scripts/version.py current

version-get: version

VERSION_TYPE ?= patch
.PHONY: version-set increment-version
version-set: ## Increment VERSION locally; VERSION_TYPE=major|minor|patch
	python3 -B scripts/version.py bump --type "$(VERSION_TYPE)"

increment-version: version-set

# Version for local bundle builds: the build version without a leading
# "v" when it is valid SemVer, else 0.0.0-dev.
MCPB_VERSION ?= $(shell v=$$(echo "$(VERSION)" | sed 's/^v//'); echo "$$v" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$$' && echo "$$v" || echo 0.0.0-dev)
# Pinned @anthropic-ai/mcpb version; empty = read packaging/mcpb/mcpb-version.txt.
MCPB_PIN ?=
export MCPB_PIN
comma := ,

.PHONY: release-check
release-check: ## Validate .goreleaser.yaml
	goreleaser check

.PHONY: release-snapshot
release-snapshot: ## Local dry run: build, archive, checksum (no publish, no signing)
	GORELEASER_CURRENT_TAG="v$$(cat VERSION)" goreleaser release --snapshot --clean --skip=publish,notarize,announce$(if $(shell command -v syft 2>/dev/null),,$(comma)sbom)

.PHONY: mcpb
mcpb: build ## Build dist/pdfm-mcp-<version>.mcpb from the local (thin, unsigned) binary: dev only
	MCPB_ALLOW_THIN=1 ./scripts/build-mcpb.sh "$(MCPB_VERSION)" "$(BIN_DIR)/$(BINARY)" "$(DIST_DIR)"

.PHONY: mcpb-validate
mcpb-validate: ## Validate packaging/mcpb/manifest.json.tmpl with the pinned mcpb CLI (needs Node)
	@pin="$(or $(MCPB_PIN),$$(head -n1 packaging/mcpb/mcpb-version.txt 2>/dev/null || true))"; \
	[[ -n $$pin ]] || { echo "no mcpb pin: set MCPB_PIN or packaging/mcpb/mcpb-version.txt" >&2; exit 1; }; \
	command -v npx >/dev/null 2>&1 || { echo "npx (Node.js) is required" >&2; exit 1; }; \
	tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	sed "s|@VERSION@|0.0.0-dev|g" packaging/mcpb/manifest.json.tmpl > "$$tmp/manifest.json"; \
	cp packaging/mcpb/icon.png "$$tmp/icon.png"; \
	(cd "$$tmp" && npx --yes "@anthropic-ai/mcpb@$$pin" validate manifest.json)

.PHONY: render-server-json
render-server-json: ## Render registry server.json: NAME=... VERSION_NUM=x.y.z SHA_FILE=... OUT=server.json
	./scripts/render-server-json.sh "$(NAME)" "$(VERSION_NUM)" "$(SHA_FILE)" "$(or $(OUT),server.json)"

.PHONY: test-scripts
test-scripts: ## Run release/packaging script tests (bash + Python, no network)
	bash scripts/test/run.sh
