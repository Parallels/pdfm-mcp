# Contributing to Parallels Desktop MCP

For installation and client setup, see the [README](README.md). This guide covers building, testing, debugging, and changing the server.

## Before you start

Read [AGENTS.md](AGENTS.md) and the relevant [implementation spec](docs/specs/README.md). Work on one spec at a time and update its status and QA notes with the change.

## Development requirements

- macOS with Parallels Desktop for VM integration tests.
- Go compatible with `go.mod` (currently Go 1.25.0 or newer) and the Xcode command line tools for cgo/Apple Vision.
- `golangci-lint` v2, built with a Go version compatible with your toolchain.
- Node and `npx` for the optional MCP Inspector.
- Delve for VS Code debugging.

## Build and test

```sh
git clone https://github.com/Parallels/pdfm-mcp.git
cd pdfm-mcp
make build
./bin/pdfm-mcp --version
make check
make test-e2e
```

`make check` runs formatting checks, vet, lint, and race-enabled unit tests. End-to-end tests launch the server over stdio; live VM checks need access to the local Parallels service. Use a scratch VM for manual tests that change VM state. Never automate VM deletion, encryption changes, or snapshot rollback against valuable VMs.

```sh
make smoke      # raw MCP handshake and tools/list
make inspect    # interactive MCP Inspector
make test-scripts
make help       # all development and packaging targets
```

For a local Claude Code connection, run `make claude-add`; remove it with `make claude-remove`. These helpers use the binary built in this checkout.

## Debugging

stdin/stdout are the protocol channel, so a normal F5 session cannot feed JSON-RPC to the process. `.vscode/launch.json` provides three patterns:

| Pattern | When | How |
| --- | --- | --- |
| **A. Debug tests** | Day-to-day development | In-memory and e2e tests run the real server logic; set breakpoints in handlers and use "Debug current test file" / "Debug e2e test". |
| **B. Launch in integrated terminal** | Poking the server by hand | Delve launches with `console: integratedTerminal`; paste JSON-RPC lines into the terminal. |
| **C. Attach to a client-spawned process** | Debugging exactly what Claude / the Inspector does | The client spawns the binary; VS Code attaches to its PID. |

### Workflow for pattern C

1. Run `make build-debug` (produces `bin/pdfm-mcp-debug`).
2. Start the Inspector against the debug binary: `./scripts/inspect.sh ./bin/pdfm-mcp-debug`, or register it with Claude: `claude mcp add pdfm-mcp-debug -- "$PWD/bin/pdfm-mcp-debug"`.
3. In VS Code run **"C. Attach to running pdfm-mcp (pick process)"** and pick the `pdfm-mcp-debug` process. The server waits on stdin, so you have time to attach before the first request (the Inspector's "Connect" button is a natural pause point).
4. Set a breakpoint in `ListVms` (`internal/tools/parallels_list_vms.go`), then call `parallels_list_vms` from the client.

macOS note: attaching requires Delve to have debugging permission. If attach fails, check `DevToolsSecurity -status` and enable developer-tools access for your terminal/VS Code (`sudo DevToolsSecurity -enable`), or fall back to patterns A or B.

## Adding a tool

1. Create `internal/tools/<name>.go` with input/output structs and a `Register<Name>` function. Use `jsonschema` tags to describe arguments.
2. Validate untrusted inputs, bound sizes, and confine paths. Pass arguments directly to subprocesses; avoid shell interpolation.
3. Make handlers callable with a nil `*mcp.CallToolRequest`: only access the request inside `if req != nil`.
4. Register the tool in `internal/server/server.go` and classify it in `internal/policy/` with accurate MCP annotations.
5. Add every VM-scoped tool to `tools.RegisterSteps` so it works in `parallels_vm_sequence`. `TestSequenceDriftGuard` checks registration drift.
6. Add unit tests and an in-memory protocol test. Update end-to-end expectations when the exposed tool list changes.
7. Update the spec, QA walkthrough, [tool reference](docs/tools.md), and README tool description. Run `make check` and `make test-e2e`.

Keep `main.go` thin; implementation belongs in `internal/`. Return errors rather than panicking. Tool handlers and sequence steps share the same implementation.

**Stdout carries the MCP protocol.** Log with `slog` to stderr or the configured log file. Never log passwords, tokens, or other secrets, and never commit credentials or `.env` files. Document any new dependency in the relevant spec.

## Architecture and configuration

The server uses the Go MCP SDK and JSON-RPC over stdio. Parallels commands run through the service layer; OCR uses cgo and Apple Vision inside the macOS binary.

## Project layout

```
cmd/pdfm-mcp/        entrypoint: flags, logger, signal handling, run server
internal/config/     env/flag parsing -> Config
internal/server/     builds *mcp.Server, registers all tools
internal/tools/      one file per tool + Register<Name>
internal/parallels/  Parallels Desktop module: facade + service (detection,
                     exec, VM cache) + models + interfaces
internal/version/    ldflags-injected build info
test/e2e/            stdio end-to-end test (build tag: e2e)
scripts/inspect.sh   MCP Inspector wrapper
scripts/install.sh   release installer (GitHub Releases -> /usr/local/bin)
docs/specs/          implementation specs
docs/qa/             QA walkthroughs, one per spec
```

All non-entrypoint code lives under `internal/`. No business logic in `main.go`. `bin/`, `dist/`, `coverage.out` are build outputs (git-ignored).

See [runtime configuration](docs/configuration.md) for logging, VM targeting, policy flags, and telemetry. The [tool reference](docs/tools.md) includes handler behavior, sequence examples, and links to the manual QA walkthroughs. All specs and QA guides live in [docs/specs](docs/specs/README.md) and [docs/qa](docs/qa).

## Versioning and releases

`VERSION` holds the base SemVer version (`0.1.0` initially). Use GitHub Actions
**Create Release PR** on `main` to choose a patch, minor or major bump and
review the generated `VERSION` / `CHANGELOG.md` changes. After merging, run
**Release** on `main` with `canary`, `beta` or `production`:

| Channel | Tag | Distribution |
| --- | --- | --- |
| Canary | `vX.Y.Z-canary.<run>` | GitHub prerelease |
| Beta | `vX.Y.Z-beta.<run>` | GitHub prerelease |
| Production | `vX.Y.Z` | GitHub Release, MCPB, optionally Homebrew and MCP Registry |

Every channel builds a signed, notarized universal macOS binary; production
channels reuse that binary. Existing `v*` tag pushes also work when the tag
matches `VERSION` and its commit is on `main`. For local bumps, use
`make version-set VERSION_TYPE=patch`; local builds report `X.Y.Z-dev`.
Setup, approval gates and first-release prerequisites are in
[`docs/release.md`](docs/release.md).

Release changes also need `make test-scripts`, `make release-check`, and workflow lint. `make release-snapshot` builds local unsigned artifacts without publishing. Follow the [release runbook](docs/release.md) for signing, publication gates, Homebrew updates, and the registry.

## Pull requests

Explain the problem, the resulting behavior, and how you verified it. Include user-facing changes under `## Changelog` so release PR generation can collect them. Keep changes focused and link the spec and QA notes when relevant.
