# Spec 001: Project Scaffold for `pdfm-mcp`

| | |
|---|---|
| Status | Implemented (2026-10-09; see Deviations) |
| Audience | Implementing agents / engineers |
| Scope | Repo layout, tooling, Makefile, VS Code debugging, CI, greeter reference tool |
| Out of scope | Any real `pdfm` domain tools (separate specs) |

## 1. Goal

Create the foundation of `pdfm-mcp`: a **Model Context Protocol server written in Go**, speaking **JSON-RPC 2.0 over stdio**, shipped as a single static binary with no Node runtime. The scaffold ends with a working `greet` tool that proves the whole loop: build, run, test, debug, and connect from an MCP client.

### Acceptance criteria

1. `make check` passes (fmt, vet, lint, unit tests with race detector).
2. `make build` produces `bin/pdfm-mcp`; `./bin/pdfm-mcp --version` prints version info and exits 0.
3. `make test-e2e` spawns the built binary over stdio using the SDK client and successfully calls `greet`.
4. `make inspect` opens the MCP Inspector against the binary and `greet` is listed and callable.
5. In VS Code, every launch configuration in section 8 works, including breakpoint hits inside the `greet` handler.
6. Nothing other than MCP protocol messages is ever written to **stdout** (see section 4.2).
7. `claude mcp add` (section 10) registers the server and Claude can call `greet`.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Language | Go (latest stable; `go.mod` directive set by `go mod tidy`) | Single static binary, easy cross-compile |
| MCP library | Official `github.com/modelcontextprotocol/go-sdk` (**v1.7.0** at time of writing; pin whatever `go get ...@latest` resolves to and commit `go.sum`) | Maintained with the MCP project; typed tool handlers with schema inference |
| Transport | stdio only (`mcp.StdioTransport`) | Simplest for local clients; Streamable HTTP can be added later without restructuring |
| Logging | `log/slog`, text handler, **stderr only** (optional file via env var) | stdout is reserved for the protocol |
| Module path | `github.com/cjlapao/pdfm-mcp` | **Assumption**: the repo has no remote yet. Change in one place (`go mod init`) if wrong |
| Lint | `golangci-lint` v2 | Standard |
| Dependencies | Standard library plus the SDK only, until a spec requires more | Small attack surface |

> The SDK API has changed across versions. After `go get`, treat the compiler and `go doc github.com/modelcontextprotocol/go-sdk/mcp` as the source of truth. If a signature below doesn't compile, fix the code to match the installed version and note it at the bottom of this file under "Deviations".

## 3. Repository layout

```
pdfm-mcp/
├── cmd/
│   └── pdfm-mcp/
│       └── main.go              # entrypoint: flags, logger, signal handling, run server
├── internal/
│   ├── config/
│   │   └── config.go            # env/flag parsing -> Config struct
│   ├── server/
│   │   └── server.go            # builds *mcp.Server, registers all tools
│   ├── tools/
│   │   ├── greet.go             # reference tool
│   │   └── greet_test.go        # unit + in-memory protocol test
│   └── version/
│       └── version.go           # ldflags-injected build info
├── test/
│   └── e2e/
│       └── stdio_test.go        # builds binary, drives it over stdio (build tag: e2e)
├── scripts/
│   └── inspect.sh               # wraps MCP Inspector invocation
├── docs/
│   └── specs/
│       ├── README.md
│       └── 001-project-scaffold.md   # this file
├── .github/
│   └── workflows/
│       └── ci.yml
├── .vscode/
│   ├── extensions.json
│   ├── launch.json
│   ├── settings.json
│   └── tasks.json
├── .editorconfig
├── .gitignore
├── .golangci.yml
├── AGENTS.md                    # rules for AI agents working in this repo
├── Makefile
├── README.md
├── go.mod
└── go.sum
```

Rules:

- All non-entrypoint code lives under `internal/` (nothing is importable by outsiders).
- One file per tool in `internal/tools/`, each exposing a `Register(s *mcp.Server)` function. Add new tools to `internal/server/server.go`.
- No business logic in `main.go`.
- `bin/`, `dist/`, `coverage.out` are build outputs and are git-ignored.

## 4. Core design

### 4.1 Process model

The MCP client launches `pdfm-mcp` as a child process, writes requests to its stdin, and reads responses from its stdout. `server.Run(ctx, &mcp.StdioTransport{})` blocks until the client closes stdin or the context is cancelled. `main` cancels the context on `SIGINT`/`SIGTERM`.

### 4.2 The stdout rule (critical)

stdout carries the protocol. Any stray byte breaks the client with confusing parse errors.

- Never use `fmt.Print*` or `os.Stdout` anywhere in the codebase.
- `log` and `slog` must write to `os.Stderr`.
- Enforce with lint (`forbidigo`, see section 7) so it fails CI.
- Subprocesses spawned by tools later must not inherit stdout (`cmd.Stdout` must be set explicitly).

### 4.3 Configuration

Environment variables, all optional:

| Variable | Default | Meaning |
|---|---|---|
| `PDFM_MCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `PDFM_MCP_LOG_FILE` | empty | If set, logs are also appended to this file (useful because MCP clients often hide stderr) |

Flags: `--version` prints version and exits; `--log-level` overrides the env var.

## 5. File contents

Create each file exactly as below, then adapt to compile if the SDK differs.

### 5.1 Initialise the module

```sh
cd /Users/cjlapao/code/GitHub/pdfm-mcp
go mod init github.com/cjlapao/pdfm-mcp
go get github.com/modelcontextprotocol/go-sdk@latest
go mod tidy   # after the source files exist
```

### 5.2 `internal/version/version.go`

```go
// Package version exposes build metadata injected via -ldflags.
package version

import "fmt"

// These are overwritten at build time by the Makefile.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human readable version line.
func String() string {
	return fmt.Sprintf("pdfm-mcp %s (commit %s, built %s)", Version, Commit, Date)
}
```

### 5.3 `internal/config/config.go`

```go
// Package config loads runtime configuration from flags and environment.
package config

import (
	"flag"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config holds runtime settings.
type Config struct {
	LogLevel    slog.Level
	LogFile     string
	ShowVersion bool
}

// Load parses args (without the program name) and the environment.
func Load(args []string, stderr io.Writer) (Config, error) {
	var (
		cfg      Config
		levelStr = envOr("PDFM_MCP_LOG_LEVEL", "info")
	)

	fs := flag.NewFlagSet("pdfm-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&cfg.ShowVersion, "version", false, "print version and exit")
	fs.StringVar(&levelStr, "log-level", levelStr, "log level: debug|info|warn|error")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	cfg.LogFile = os.Getenv("PDFM_MCP_LOG_FILE")
	if err := cfg.LogLevel.UnmarshalText([]byte(strings.ToLower(levelStr))); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

### 5.4 `internal/tools/greet.go`

```go
// Package tools contains MCP tool implementations. One file per tool.
package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GreetInput is the argument schema for the greet tool.
// The jsonschema tag becomes the argument description shown to the model.
type GreetInput struct {
	Name  string `json:"name" jsonschema:"the name of the person to greet"`
	Shout bool   `json:"shout,omitempty" jsonschema:"return the greeting in upper case"`
}

// GreetOutput is the structured result of the greet tool.
type GreetOutput struct {
	Greeting string `json:"greeting" jsonschema:"the greeting message"`
}

// Greet is the handler. Keep handlers thin: validate, call domain code, map result.
func Greet(ctx context.Context, req *mcp.CallToolRequest, in GreetInput) (*mcp.CallToolResult, GreetOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		// Returning an error yields a tool result with isError=true.
		return nil, GreetOutput{}, errEmptyName
	}

	msg := "Hello, " + name + "!"
	if in.Shout {
		msg = strings.ToUpper(msg)
	}
	// Returning a nil result lets the SDK populate both structured content
	// and a text content block from the output value.
	return nil, GreetOutput{Greeting: msg}, nil
}

// RegisterGreet adds the greet tool to the server.
func RegisterGreet(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "greet",
		Description: "Greets a person by name. Reference tool used to verify the MCP plumbing.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, Greet)
}
```

Add a sentinel error in the same package (`internal/tools/errors.go`):

```go
package tools

import "errors"

var errEmptyName = errors.New("name must not be empty")
```

### 5.5 `internal/server/server.go`

```go
// Package server assembles the MCP server and registers tools.
package server

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/tools"
	"github.com/cjlapao/pdfm-mcp/internal/version"
)

// New builds the MCP server with all tools registered.
func New(logger *slog.Logger) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "pdfm-mcp", Version: version.Version},
		&mcp.ServerOptions{
			Logger:       logger,
			Instructions: "pdfm-mcp server. Use the available tools to interact with pdfm.",
		},
	)

	tools.RegisterGreet(s)
	// Register new tools here.

	return s
}
```

### 5.6 `cmd/pdfm-mcp/main.go`

```go
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/config"
	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load(os.Args[1:], os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		return 2
	}
	if cfg.ShowVersion {
		// --version is the one allowed stdout write: it never runs in server mode.
		fmt.Fprintln(os.Stdout, version.String()) //nolint:forbidigo
		return 0
	}

	logger, closeLog := newLogger(cfg)
	defer closeLog()
	slog.SetDefault(logger)
	logger.Info("starting", "version", version.Version, "commit", version.Commit)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.New(logger).Run(ctx, &mcp.StdioTransport{}); err != nil {
		logger.Error("server exited with error", "err", err)
		return 1
	}
	logger.Info("server stopped")
	return 0
}

func newLogger(cfg config.Config) (*slog.Logger, func()) {
	var w io.Writer = os.Stderr
	closer := func() {}
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot open log file:", err)
		} else {
			w = io.MultiWriter(os.Stderr, f)
			closer = func() { _ = f.Close() }
		}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: cfg.LogLevel})), closer
}
```

## 6. Tests

### 6.1 `internal/tools/greet_test.go` (unit + in-memory protocol)

Two layers in one file:

1. Table-driven test calling `Greet` directly (empty name, whitespace name, normal, shout).
2. A protocol test using `mcp.NewInMemoryTransports()` that connects a real client to the real server and calls `greet` through JSON-RPC. This exercises schema inference and validation without spawning a process.

```go
package tools_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/tools"
)

func TestGreetHandler(t *testing.T) {
	cases := []struct {
		name    string
		in      tools.GreetInput
		want    string
		wantErr bool
	}{
		{"normal", tools.GreetInput{Name: "Carlos"}, "Hello, Carlos!", false},
		{"shout", tools.GreetInput{Name: "Carlos", Shout: true}, "HELLO, CARLOS!", false},
		{"trims", tools.GreetInput{Name: "  Carlos "}, "Hello, Carlos!", false},
		{"empty", tools.GreetInput{Name: "   "}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := tools.Greet(context.Background(), nil, tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if out.Greeting != tc.want {
				t.Fatalf("greeting = %q, want %q", out.Greeting, tc.want)
			}
		})
	}
}

func TestGreetOverInMemoryTransport(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	clientT, serverT := mcp.NewInMemoryTransports()
	srvSession, err := server.New(logger).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srvSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "Carlos"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["greeting"] != "Hello, Carlos!" {
		t.Fatalf("structured content = %v", res.StructuredContent)
	}
}
```

### 6.2 `test/e2e/stdio_test.go` (real binary over stdio)

Build tag `e2e` keeps it out of `make test`. It builds the binary into a temp dir, connects with `mcp.CommandTransport`, lists tools, and calls `greet`. This is the closest thing to what Claude does.

```go
//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bin := filepath.Join(t.TempDir(), "pdfm-mcp")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/pdfm-mcp").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "v0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tl := range tools.Tools {
		if tl.Name == "greet" {
			found = true
		}
	}
	if !found {
		t.Fatalf("greet not listed: %+v", tools.Tools)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "e2e", "shout": true},
	})
	if err != nil || res.IsError {
		t.Fatalf("call failed: err=%v res=%+v", err, res)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["greeting"] != "HELLO, E2E!" {
		t.Fatalf("unexpected result: %v", res.StructuredContent)
	}
}
```

## 7. Tooling configuration

### 7.1 `Makefile`

Recipes **must be indented with tabs**.

```makefile
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
	@printf '%s\n%s\n%s\n' \
	 '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
	 '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	 '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
	 | ./$(BIN_DIR)/$(BINARY)

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
```

Note: the `smoke` protocol version string should match one the SDK supports. If the server rejects it, replace with the version the SDK's `initialize` response reports (check `go doc` or the SDK release notes).

### 7.2 `scripts/inspect.sh`

```bash
#!/usr/bin/env bash
# Launch the MCP Inspector against a stdio server binary.
# Node/npx is only needed for this debugging UI, not for the server itself.
set -euo pipefail
BIN="${1:?usage: inspect.sh <path-to-binary> [args...]}"
shift || true
exec npx --yes @modelcontextprotocol/inspector "$BIN" "$@"
```

Then `chmod +x scripts/inspect.sh`.

### 7.3 `.golangci.yml` (golangci-lint v2 format)

```yaml
version: "2"

run:
  timeout: 5m

linters:
  default: standard
  enable:
    - errcheck
    - govet
    - staticcheck
    - unused
    - ineffassign
    - misspell
    - revive
    - gocritic
    - bodyclose
    - noctx
    - forbidigo
  settings:
    forbidigo:
      forbid:
        - pattern: ^fmt\.Print(f|ln)?$
          msg: "stdout is reserved for the MCP protocol; use slog (stderr)"
        - pattern: ^os\.Stdout$
          msg: "stdout is reserved for the MCP protocol"
  exclusions:
    rules:
      - path: _test\.go
        linters: [forbidigo]

formatters:
  enable:
    - gofmt
    - goimports
```

If `golangci-lint` rejects any key for its installed version, run `golangci-lint migrate` or consult its docs, and keep the intent: standard linters plus `forbidigo` banning stdout writes.

### 7.4 `.gitignore`

```gitignore
# Build outputs
/bin/
/dist/
coverage.out
*.test

# Go workspace/vendor
go.work
go.work.sum

# Editor / OS
.DS_Store
.idea/
*.swp

# VS Code: keep shared config, ignore the rest
.vscode/*
!.vscode/launch.json
!.vscode/tasks.json
!.vscode/settings.json
!.vscode/extensions.json

# Local logs and secrets
*.log
.env
.env.*
```

### 7.5 `.editorconfig`

```ini
root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
trim_trailing_whitespace = true

[*.go]
indent_style = tab

[Makefile]
indent_style = tab

[*.{json,yml,yaml,md,sh}]
indent_style = space
indent_size = 2
```

### 7.6 `.github/workflows/ci.yml`

```yaml
name: ci
on:
  push:
    branches: [main, master]
  pull_request:

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: golangci/golangci-lint-action@v8
        with:
          version: latest
      - run: make fmt-check vet test
      - run: make test-e2e
      - run: make build-all
```

Pin action versions to the latest available majors at implementation time.

## 8. VS Code integration

### 8.1 The stdio debugging problem

stdin/stdout are the protocol channel, so a normal "press F5" session has no way to feed JSON-RPC to the process. There are three practical patterns; this scaffold supports all of them:

| Pattern | When to use | How |
|---|---|---|
| **A. Debug tests** | Day-to-day development. Fastest loop. | The in-memory and e2e tests run the real server logic; set breakpoints in handlers and use "Debug Test". |
| **B. Launch in integrated terminal** | Poking the server by hand. | Delve launches the server with `console: integratedTerminal`, so you can paste JSON-RPC lines into the terminal's stdin. |
| **C. Attach to a client-spawned process** | Debugging exactly what Claude / Inspector does. | The client spawns the binary; VS Code attaches to its PID. Use `build-debug` binary so breakpoints bind correctly. |

Tip for pattern C: the server starts and waits on stdin, so you have time to attach before sending the first request (Inspector's "Connect" button is a natural pause point).

### 8.2 `.vscode/extensions.json`

```json
{
  "recommendations": [
    "golang.go",
    "editorconfig.editorconfig",
    "streetsidesoftware.code-spell-checker"
  ]
}
```

### 8.3 `.vscode/settings.json`

```json
{
  "go.useLanguageServer": true,
  "go.lintTool": "golangci-lint",
  "go.lintOnSave": "workspace",
  "go.formatTool": "gofmt",
  "go.testFlags": ["-race", "-count=1"],
  "go.testEnvVars": { "CGO_ENABLED": "1" },
  "go.buildTags": "",
  "gopls": {
    "formatting.gofumpt": false,
    "ui.semanticTokens": true
  },
  "editor.formatOnSave": true,
  "[go]": {
    "editor.defaultFormatter": "golang.go",
    "editor.codeActionsOnSave": { "source.organizeImports": "explicit" }
  },
  "[makefile]": { "editor.insertSpaces": false }
}
```

### 8.4 `.vscode/tasks.json`

```json
{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "build-debug",
      "type": "shell",
      "command": "make build-debug",
      "group": "build",
      "problemMatcher": ["$go"]
    },
    {
      "label": "build",
      "type": "shell",
      "command": "make build",
      "group": { "kind": "build", "isDefault": true },
      "problemMatcher": ["$go"]
    },
    {
      "label": "test",
      "type": "shell",
      "command": "make test",
      "group": { "kind": "test", "isDefault": true },
      "problemMatcher": ["$go"]
    },
    {
      "label": "check",
      "type": "shell",
      "command": "make check",
      "problemMatcher": ["$go"]
    },
    {
      "label": "inspect",
      "type": "shell",
      "command": "make inspect",
      "isBackground": true,
      "problemMatcher": {
        "owner": "inspector",
        "pattern": { "regexp": "^$" },
        "background": {
          "activeOnStart": true,
          "beginsPattern": "Starting MCP inspector",
          "endsPattern": "MCP Inspector is up|localhost:6274"
        }
      }
    }
  ]
}
```

### 8.5 `.vscode/launch.json`

```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "B. Server (integrated terminal, type JSON-RPC)",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/cmd/pdfm-mcp",
      "console": "integratedTerminal",
      "args": ["--log-level", "debug"],
      "env": { "PDFM_MCP_LOG_FILE": "${workspaceFolder}/pdfm-mcp.log" }
    },
    {
      "name": "C. Attach to running pdfm-mcp (pick process)",
      "type": "go",
      "request": "attach",
      "mode": "local",
      "processId": "${command:pickProcess}",
      "preLaunchTask": "build-debug"
    },
    {
      "name": "A. Debug current test file",
      "type": "go",
      "request": "launch",
      "mode": "test",
      "program": "${fileDirname}",
      "env": { "CGO_ENABLED": "1" },
      "args": ["-test.v", "-test.count=1"]
    },
    {
      "name": "A. Debug e2e test",
      "type": "go",
      "request": "launch",
      "mode": "test",
      "program": "${workspaceFolder}/test/e2e",
      "buildFlags": "-tags=e2e",
      "args": ["-test.v", "-test.count=1"]
    }
  ]
}
```

### 8.6 Workflow for pattern C (document this in README)

1. Run `make build-debug` (produces `bin/pdfm-mcp-debug`).
2. Start the Inspector against the debug binary: `./scripts/inspect.sh ./bin/pdfm-mcp-debug`, or register that binary in Claude with `claude mcp add pdfm-mcp-debug -- $PWD/bin/pdfm-mcp-debug`.
3. In VS Code run **"C. Attach to running pdfm-mcp"**, pick the `pdfm-mcp-debug` process.
4. Set a breakpoint in `Greet`, then call the tool from the client.

macOS note: attaching requires Delve to have debugging permission. If attach fails, run `DevToolsSecurity -status` and enable developer-tools access for the terminal/VS Code (`sudo DevToolsSecurity -enable`), or fall back to patterns A or B.

## 9. Docs to create

### 9.1 `docs/specs/README.md`

An index listing specs with status. Initial content: a table with row `001 | Project scaffold | Ready`.

### 9.2 `README.md` (repo root)

Sections: what it is, requirements (Go, optional Node for Inspector only), quick start (`make build`, `make smoke`, `make inspect`), connecting to clients (section 10), debugging (link to section 8 content), adding a tool, project layout, make targets (`make help`).

### 9.3 `AGENTS.md`

Rules for AI agents working in this repo:

- Read `docs/specs/` before implementing; implement one spec at a time and update its status.
- Never write to stdout. Log with `slog` to stderr.
- Keep `main.go` thin; logic goes in `internal/`.
- Every tool: file in `internal/tools/`, `Register<Name>` function, unit test plus in-memory protocol test, entry in `server.New`.
- Run `make check` and `make test-e2e` before declaring work done.
- Never commit secrets, tokens or `.env` files; never hardcode credentials. Read secrets from environment variables and never log them.
- Do not add dependencies without a reason documented in the relevant spec.
- Treat tool arguments as untrusted input: validate, bound sizes, avoid shell interpolation, and never build file paths from raw input without cleaning and confining them.

## 10. Connecting to clients

Use an absolute path to the binary.

**Claude Code**

```sh
claude mcp add pdfm-mcp -- /Users/cjlapao/code/GitHub/pdfm-mcp/bin/pdfm-mcp
claude mcp list
```

**Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "pdfm-mcp": {
      "command": "/Users/cjlapao/code/GitHub/pdfm-mcp/bin/pdfm-mcp",
      "args": ["--log-level", "debug"],
      "env": { "PDFM_MCP_LOG_FILE": "/tmp/pdfm-mcp.log" }
    }
  }
}
```

Restart the client after editing. Desktop hides stderr, which is why `PDFM_MCP_LOG_FILE` exists.

## 11. Implementation order for agents

1. `go mod init`, add `.gitignore`, `.editorconfig`, directory skeleton.
2. `internal/version`, `internal/config`.
3. `internal/tools` (greet + errors), `internal/server`, `cmd/pdfm-mcp/main.go`.
4. Tests (6.1), then `go mod tidy`, then confirm `go test ./...` passes.
5. `Makefile`, `scripts/inspect.sh`, `.golangci.yml`; run `make check`, `make build`, `make smoke`.
6. E2E test (6.2); run `make test-e2e`.
7. `.vscode/*`; manually verify patterns A, B and C hit a breakpoint in `Greet`.
8. CI workflow, README, AGENTS.md, specs index.
9. Walk through the acceptance criteria in section 1 and tick each one.

## 12. Extending: adding a tool (template)

1. Create `internal/tools/<name>.go` with `<Name>Input`, `<Name>Output` structs (use `jsonschema` tags for descriptions; fields without `omitempty` are required).
2. Write handler `func(ctx, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)`. Return an `error` for tool-level failures (surfaced as `isError`), never panic.
3. Add `Register<Name>(s *mcp.Server)` using `mcp.AddTool` with accurate `Annotations` (`ReadOnlyHint`, `DestructiveHint`, `IdempotentHint`).
4. Register it in `internal/server/server.go`.
5. Add unit tests and an in-memory protocol test; extend the e2e test if it changes the tool list.

## 13. Known unknowns / verification notes

- This spec was written without compiling the code (no Go toolchain was available in the authoring environment). The first implementer must compile and fix any API drift against the installed SDK version, then record changes below.
- Unconfirmed against the installed SDK: `mcp.ServerOptions.Instructions`, `mcp.ToolAnnotations`, `mcp.NewInMemoryTransports`. All are expected to exist in v1.x.
- Confirm the GitHub remote and module path before the first push.

### Deviations (fill in during implementation)

Implemented 2026-10-09 with Go 1.27.1 (darwin/arm64), golangci-lint v2, Make, Node 24.

1. **SDK pinned to v1.8.0** (spec was written against v1.7.0; §2 says pin whatever `@latest` resolves to). Every API referenced by this spec was verified against the v1.8.0 sources — `NewServer`, `ServerOptions{Instructions, Logger}`, `Server.Run`/`Connect`, `ServerSession.Close`, `AddTool` (typed `ToolHandlerFor`), `Tool.Annotations`/`ToolAnnotations{ReadOnlyHint}`, `StdioTransport`, `CommandTransport{Command}`, `NewInMemoryTransports`, `NewClient`/`Client.Connect`, `ClientSession.CallTool`/`ListTools`, `CallToolResult{StructuredContent, IsError, Content}`, `ListToolsResult.Tools`, `Implementation` — all present. The smoke protocol version `2025-06-18` is in the SDK's supported list (2024-11-05 … 2026-07-28), so `make smoke` was kept as written.
2. **`go.mod` targets `go 1.25.0`**, not the toolchain's 1.27.1. The implementer machine's golangci-lint 2.5.0 (built with go1.25.1) refuses a `go 1.27.1` directive ("Go language version used to build golangci-lint is lower than the targeted Go version") and panics type-checking the go1.27 stdlib anyway ("file requires newer Go version go1.26"). `go 1.25.0` is the SDK's own minimum and works with the local toolchain and with golangci-lint v2.14.0 (built with go1.27.0), which is what the verification run used. **Action for the machine owner: upgrade `golangci-lint` to ≥ 2.14** (e.g. `brew upgrade golangci-lint` or the official install script) — 2.5.0 cannot lint a go1.27 toolchain.
3. **CI actions pinned to the latest majors at implementation time** (per §7.6): `actions/checkout@v7`, `actions/setup-go@v7`, `golangci/golangci-lint-action@v9`.
4. **Lint-compliance fixes** flagged by golangci-lint v2.14.0 (standard set + `errcheck`/`revive`), none of which change behaviour:
   - `cmd/pdfm-mcp/main.go`: added a package doc comment (`// Command pdfm-mcp …`); `--version` write is now `_, _ = fmt.Fprintln(…)` (errcheck), keeping the `//nolint:forbidigo`.
   - `internal/tools/greet.go`: unused handler parameters renamed to `_` (revive unused-parameter); signature still matches `ToolHandlerFor`.
   - `internal/tools/greet_test.go` and `test/e2e/stdio_test.go`: `Close()` calls wrapped in defers that log the error instead of `defer x.Close()` (errcheck).
6. **`greet` reference tool removed (2026-10-09, post-implementation).** It was scaffolding for testing the MCP plumbing only. With it gone the server exposes no tools when the Parallels module is disabled, `TestStdioEndToEnd` now asserts connect + tools/list + module gating instead of calling `greet`, and the disabled-mode protocol test asserts zero tools. The VS Code breakpoint walkthrough in the README now points at `ListVms`.
5. **`make smoke` keeps stdin open for 1 s after printing the requests** (`{ printf …; sleep 1; } | ./bin/pdfm-mcp`). As written in §7.1, the pipe closes immediately after the third line; SDK v1.8.0 tears the session down on stdin EOF, which in a tight race tears it down before the `initialize`/`tools/list` responses are flushed, so stdout comes back empty. With the 1 s hold, `make smoke` reliably prints both responses (verified). Real clients keep stdin open, so this only affects the smoke target.
