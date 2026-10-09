# pdfm-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server written in Go, speaking **JSON-RPC 2.0 over stdio**. It ships as a single static binary with no Node runtime — Node is only needed for the optional MCP Inspector debugging UI.

The scaffold provides a reference tool, `greet`, that exercises the full loop: build, run, test, debug, and connect from an MCP client. On top of that, the **Parallels Desktop module** lists and inspects VMs (see below) — and is the template for adding more command-backed modules.

## Requirements

- Go (latest stable; `go.mod` targets `go 1.25.0`+)
- `golangci-lint` v2 for `make check` / `make lint`
- Optional: Node + `npx` — only for `make inspect` (MCP Inspector)

## Quick start

```sh
make build      # -> bin/pdfm-mcp
make smoke      # raw stdio handshake + tools/list against the binary
make inspect    # open the MCP Inspector UI against the binary (needs npx)
make check      # fmt-check, vet, lint, unit tests with race detector
make test-e2e   # spawn the built binary over stdio and call greet
```

`./bin/pdfm-mcp --version` prints build info (version, commit, build date).

## Connecting to clients

Use an absolute path to the binary.

### Claude Code

```sh
make claude-add   # or:
claude mcp add pdfm-mcp -- /Users/cjlapao/code/GitHub/pdfm-mcp/bin/pdfm-mcp
claude mcp list
```

Remove with `make claude-remove`.

### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json`:

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

### Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PDFM_MCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `PDFM_MCP_LOG_FILE` | empty | If set, logs are also appended to this file |
| `PDFM_MCP_VM_ID` | empty | Parallels VM id or name to target (see module below) |
| `PDFM_MCP_VM_CACHE_TTL` | `30s` | Freshness window of the in-memory VM list cache |

Flags: `--version` prints version and exits; `--log-level` and `--vm-id` override the env vars.

### Parallels Desktop module

When both `prlctl` and `prlsrvctl` are found at startup (PATH, then `/usr/local/bin` and `/usr/bin`), the module enables read tools backed by a 30s in-memory cache of `prlctl list -a -i --json` plus the power-operation tools:

| Tool | Modes | What it does |
|---|---|---|
| `parallels_list_vms` | fleet + VM | The VM list with full prlctl detail (all VMs, or only the target) |
| `parallels_vm_info` | VM only | The exact cached prlctl JSON object of the target VM |
| `parallels_vm_start` | fleet + VM | `prlctl start` (start a stopped VM or resume a paused one) |
| `parallels_vm_resume` | fleet + VM | `prlctl resume` |
| `parallels_vm_pause` | fleet + VM | `prlctl pause` |
| `parallels_vm_suspend` | fleet + VM | `prlctl suspend` |
| `parallels_vm_restart` | fleet + VM | `prlctl restart` (graceful shutdown + start) |
| `parallels_vm_reset` | fleet + VM | `prlctl reset` (hard shutdown + start) |
| `parallels_vm_reset_uptime` | fleet + VM | `prlctl reset-uptime` |
| `parallels_vm_stop` | fleet + VM | `prlctl stop` (optional `kill` / `drop_state`) |

- **Fleet mode** (no `--vm-id`): reads target all VMs; `parallels_vm_info` is not exposed; power tools **require** a `vm_id` argument (a VM id or name).
- **VM mode** (`--vm-id <uuid-or-name>`): reads and power tools target that VM; prlctl resolves names to ids at fetch time; power tools **reject** an explicit `vm_id` (a single-VM server must not touch other VMs).
- The cache warms at startup (a failed warm-up only logs a warning; tools retry on demand), refreshes single-flight when the TTL expires, and keeps stale data if a refresh fails. Every successful power command invalidates the cache so the next read reflects the new state.
- Power commands run with a 180s timeout (guest shutdowns can be slow); reads keep the 30s cap.
- On hosts without Parallels Desktop the module stays off: the log says so and no `parallels_*` tool is exposed.

QA walkthrough: [`docs/qa/002-parallels-desktop-module-QA.md`](docs/qa/002-parallels-desktop-module-QA.md), power operations: [`docs/qa/003-parallels-power-operations-QA.md`](docs/qa/003-parallels-power-operations-QA.md).

**The stdout rule:** stdout carries the MCP protocol. Nothing else may be written there — logging goes to stderr (and optionally a file). `golangci-lint` (`forbidigo`) enforces this in CI.

## Debugging

stdin/stdout are the protocol channel, so a normal F5 session cannot feed JSON-RPC to the process. `.vscode/launch.json` provides three patterns:

| Pattern | When | How |
|---|---|---|
| **A. Debug tests** | Day-to-day development | In-memory and e2e tests run the real server logic; set breakpoints in handlers and use "Debug current test file" / "Debug e2e test". |
| **B. Launch in integrated terminal** | Poking the server by hand | Delve launches with `console: integratedTerminal`; paste JSON-RPC lines into the terminal. |
| **C. Attach to a client-spawned process** | Debugging exactly what Claude / the Inspector does | The client spawns the binary; VS Code attaches to its PID. |

### Workflow for pattern C

1. Run `make build-debug` (produces `bin/pdfm-mcp-debug`).
2. Start the Inspector against the debug binary: `./scripts/inspect.sh ./bin/pdfm-mcp-debug`, or register it with Claude: `claude mcp add pdfm-mcp-debug -- $PWD/bin/pdfm-mcp-debug`.
3. In VS Code run **"C. Attach to running pdfm-mcp (pick process)"** and pick the `pdfm-mcp-debug` process. The server waits on stdin, so you have time to attach before the first request (the Inspector's "Connect" button is a natural pause point).
4. Set a breakpoint in `Greet` (`internal/tools/greet.go`), then call the tool from the client.

macOS note: attaching requires Delve to have debugging permission. If attach fails, check `DevToolsSecurity -status` and enable developer-tools access for your terminal/VS Code (`sudo DevToolsSecurity -enable`), or fall back to patterns A or B.

## Adding a tool

1. Create `internal/tools/<name>.go` with `<Name>Input` / `<Name>Output` structs (`jsonschema` tags become argument descriptions; fields without `omitempty` are required).
2. Write handler `func(ctx, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)`. Return an `error` for tool-level failures (surfaced as `isError`); never panic.
3. Add `Register<Name>(s *mcp.Server)` using `mcp.AddTool` with accurate `Annotations` (`ReadOnlyHint`, `DestructiveHint`, `IdempotentHint`).
4. Register it in `internal/server/server.go`.
5. Add unit tests and an in-memory protocol test; extend `test/e2e/stdio_test.go` if the tool list changes.

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
docs/specs/          implementation specs
docs/qa/             QA walkthroughs, one per spec
```

All non-entrypoint code lives under `internal/`. No business logic in `main.go`. `bin/`, `dist/`, `coverage.out` are build outputs (git-ignored).

## Make targets

Run `make help` for the full list: `tidy`, `fmt`, `fmt-check`, `vet`, `lint`, `test`, `test-e2e`, `cover`, `check`, `build`, `build-debug`, `run`, `inspect`, `smoke`, `install`, `build-all`, `claude-add`, `claude-remove`, `clean`.
