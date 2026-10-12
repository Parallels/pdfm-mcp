# pdfm-mcp

[![Join the pdfm-mcp community on Discord — join the server, then head to #pdfm-mcp](docs/assets/discord-banner.svg)](https://discord.gg/7y3AxDUc8Y)

[Join our Discord server](https://discord.gg/7y3AxDUc8Y), then open **#pdfm-mcp** to ask questions, share ideas, and show what you're building.

A [Model Context Protocol](https://modelcontextprotocol.io) server written in Go, speaking **JSON-RPC 2.0 over stdio**. It ships as a single static binary with no Node runtime — Node is only needed for the optional MCP Inspector debugging UI.

The **Parallels Desktop module** lists and inspects VMs (see below) and is the template for adding more command-backed modules. The build/run/test/debug/connect loop is exercised by the test suites: `make check` (unit + in-memory protocol tests) and `make test-e2e` (spawns the real binary over stdio).

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
make test-e2e   # spawn the built binary over stdio and verify tool gating
```

`./bin/pdfm-mcp --version` prints build info (version, commit, build date).

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

## Install

Distribution is specified in [`docs/specs/013-release-pipeline.md`](docs/specs/013-release-pipeline.md) to [`016`](docs/specs/016-mcp-registry.md); the files exist (`.goreleaser.yaml`, `release.yml`, `packaging/`) but nothing is published yet. Open items before the first release are in [`docs/release.md`](docs/release.md). Once released:

### Install script (macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/Parallels/pdfm-mcp/main/scripts/install.sh | bash
```

Downloads the universal (Apple silicon + Intel) binary from GitHub Releases, verifies it against the release's `checksums.txt`, and installs it to `/usr/local/bin` (sudo only if that directory is not writable). Options: `--version 1.2.3` (default: latest production release), `--pre-release` for canary/beta, `--path DIR`, `--update`, `--uninstall`. As with the cask, GUI clients need the absolute path: `/usr/local/bin/pdfm-mcp`.

### Homebrew (macOS)

```sh
brew tap Parallels/tap
brew install --cask Parallels/tap/pdfm-mcp
pdfm-mcp --version
```

GUI clients ignore your shell PATH, so use the absolute path: `claude mcp add pdfm-mcp -- "$(brew --prefix)/bin/pdfm-mcp"`, or in Claude Desktop `"command": "/opt/homebrew/bin/pdfm-mcp"` (`/usr/local/bin/pdfm-mcp` on Intel). Upgrade: `brew upgrade --cask pdfm-mcp`.

### Claude Desktop bundle (.mcpb)

Download `pdfm-mcp-<version>.mcpb` from the Releases page, verify it against `pdfm-mcp-<version>.mcpb.sha256` (`shasum -a 256 -c`), then double-click it. Optional settings: target VM (empty = all VMs) and log level.

### OCR (built in)

`parallels_vm_observe` reads screen text with Apple Vision compiled into the
binary (cgo, macOS only) — no separate helper to install. See
[`docs/specs/022-builtin-ocr-observe.md`](docs/specs/022-builtin-ocr-observe.md).

## Connecting to clients

Use an absolute path to the binary.

### Claude Code

```sh
make claude-add   # or:
claude mcp add pdfm-mcp -- /path/to/pdfm-mcp/bin/pdfm-mcp
claude mcp list
```

Remove with `make claude-remove`.

### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "pdfm-mcp": {
      "command": "/path/to/pdfm-mcp/bin/pdfm-mcp",
      "args": ["--log-level", "debug"],
      "env": { "PDFM_MCP_LOG_FILE": "/tmp/pdfm-mcp.log" }
    }
  }
}
```

Restart the client after editing. Desktop hides stderr, which is why `PDFM_MCP_LOG_FILE` exists.

### Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `PDFM_MCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `PDFM_MCP_LOG_FILE` | empty | If set, logs are also appended to this file |
| `PDFM_MCP_VM_ID` | empty | Parallels VM id or name to target (see module below) |
| `PDFM_MCP_VM_CACHE_TTL` | `30s` | Freshness window of the in-memory VM list cache |
| `PDFM_MCP_READ_ONLY` | `false` | Expose only read-class tools (flag `--read-only`) |
| `PDFM_MCP_ALLOW_TOOLS` | unset | Comma list of tool names or trailing-`*` globs; when set, only these are exposed (flag `--allow-tools`) |
| `PDFM_MCP_DENY_TOOLS` | unset | Comma list of names or globs to hide; wins over allow (flag `--deny-tools`) |
| `PDFM_MCP_ALLOW_VMS` | unset | Comma list of VM ids or names the tools may address, max 64; in VM mode `--vm-id` must be in it (flag `--allow-vms`) |
| `PDFM_MCP_TELEMETRY_DISABLED` | `false` | Disables the usage telemetry collected by the released build (see below) |

### Telemetry

The released build of pdfm-mcp collects **anonymised usage telemetry**: which tools are used, whether they succeeded, and how long they took. It never collects tool arguments, VM ids or names, guest output, screen content, or error text. To disable telemetry, set `PDFM_MCP_TELEMETRY_DISABLED=true` — it always wins.

### Safety policy

pdfm-mcp can restrict what an agent may do. `--read-only` exposes only observation tools (`parallels_list_vms`, `parallels_vm_info`, `parallels_vm_capture`, ...). `--allow-tools` and `--deny-tools` take exact names or prefix globs such as `parallels_vm_exec*`; deny wins. Hidden tools disappear from `tools/list` and from `parallels_vm_sequence` steps. `--allow-vms` limits which VMs any tool can touch: `parallels_list_vms` returns only allowed VMs, and other VMs are refused without being named. Every flag has a `PDFM_MCP_*` env equivalent, and flags beat env. Unknown (new, unclassified) tools fail closed: they count as destructive and are hidden in read-only mode.

This guards against a misconfigured or over-eager agent, not against a local user who can edit the client config; an allowed `exec` can still run anything in the allowed guest. Details: [`docs/specs/019-safety-policy.md`](docs/specs/019-safety-policy.md).

Flags: `--version` prints version and exits; `--log-level` and `--vm-id` override the env vars.

### Parallels Desktop module

When both `prlctl` and `prlsrvctl` are found at startup (PATH, then `/usr/local/bin` and `/usr/bin`), the module enables read tools backed by a 30s in-memory cache of `prlctl list -a -i --json` plus the power-operation tools:

| Tool | Modes | What it does |
| --- | --- | --- |
| `parallels_list_vms` | fleet + VM | The VM list with full prlctl detail (all VMs, or only the target) |
| `parallels_vm_info` | fleet + VM | The exact prlctl JSON object of one VM. VM mode: the `--vm-id` target from the in-memory cache. Fleet mode: any VM by id or name, fetched live on every call with the list command scoped to it (`prlctl list <vm_id> -a -i --json`) — `vm_id` is required |
| `parallels_vm_start` | fleet + VM | `prlctl start` (start a stopped VM or resume a paused one) |
| `parallels_vm_resume` | fleet + VM | `prlctl resume` |
| `parallels_vm_pause` | fleet + VM | `prlctl pause` |
| `parallels_vm_suspend` | fleet + VM | `prlctl suspend` |
| `parallels_vm_restart` | fleet + VM | `prlctl restart` (graceful shutdown + start) |
| `parallels_vm_reset` | fleet + VM | `prlctl reset` (hard shutdown + start) |
| `parallels_vm_reset_uptime` | fleet + VM | `prlctl reset-uptime` |
| `parallels_vm_stop` | fleet + VM | `prlctl stop` (optional `kill` / `drop_state`) |
| `parallels_vm_capture` | fleet + VM | Screenshot of the VM screen as a PNG image block (default long edge ≤ 1568 px; `max_dimension` 256–4096, `full_resolution` for native). The VM must be running |
| `parallels_vm_exec` | fleet + VM | Run a command in the guest (`prlctl exec`): returns `exit_code`, `stdout` (1 MB cap), `stderr` (256 KB cap). Defaults to `--current-user` (set `user` to override; no password support), optional `resolve_paths`. Command ≤ 4096 chars, 5-minute cap. Non-zero exits are results, not errors. Guest needs Parallels Tools. `elevated: true` is **privileged**: it omits `--current-user`/`--user` so prlctl runs the command as the guest's system account (root/SYSTEM; verify on your guest, cannot be combined with `user`) |
| `parallels_vm_exec_start` | fleet + VM | Start a long-running guest command as a background job and get a `job_id`. Same arguments as `parallels_vm_exec` (incl. privileged `elevated`) plus `timeout_seconds` (default 1800, max 14400). Max 4 running jobs |
| `parallels_vm_exec_result` | fleet + VM | Job state (`running`/`succeeded`/`failed`/`cancelled`/`timed_out`), exit code and incremental stdout/stderr slices via offsets (256 KB per call); `wait_ms` (0–30000) long-polls for completion |
| `parallels_vm_exec_cancel` | fleet + VM | Kill a running job's `prlctl` process (the guest process may keep running: check with `parallels_vm_exec`) |
| `parallels_vm_observe` | fleet + VM | Read the VM screen as text with positions, mimicking the VMPal `observe` CLI: the screen's text (`text`, default), what changed since the previous observation (`changes`), or where a piece of text is (`find <query>`) — Apple Vision OCR compiled into the binary, **locally**. `region`/`limit` as in the CLI. Coordinates are screenshot pixels (same space as `parallels_vm_capture`). The recognized text still reaches the model provider |
| `parallels_vm_send_key` | fleet + VM | Send keyboard events to the VM (`prlctl send-key-event -j`): a single key (name like `enter`, `f5`, `a` or a scancode, optional `press`/`release` + delay) or a full `events` sequence (modifier combos, `{delay}` pauses). Omitted event = tap. The VM must be running |
| `parallels_vm_type` | fleet + VM | Type ASCII text into the VM (letters, digits, space, punctuation) with automatic Shift handling. Escapes: `\n`=Enter, `\t`=Tab, `\\`=literal backslash (a backslash before any other character is typed literally). Per-event `delay_ms` (0-5000, default 200). Unmappable characters are rejected before anything is sent. The VM must be running |
| `parallels_vm_delete` | fleet + VM | **Permanently** delete a VM and all its files from the host (`prlctl delete`) — irreversible. Requires a `confirm` argument repeating the VM id or name exactly. Stop the VM first if running |
| `parallels_vm_clone` | fleet + VM | Create a copy of a VM (`prlctl clone`): required `name`; options `template`, `dst` (destination dir), `regenerate_src_uuid`, `linked` (+ `snapshot_id`), `detach_external_hdd` yes/no. The source VM is never modified; full clones need free disk for all VM files |
| `parallels_vm_encrypt` | fleet + VM | Encrypt a VM and all of its data with a password (`prlctl encrypt`). The VM must be stopped; `dry_run` checks the prerequisites without encrypting (and without a password). The password is fed to prlctl over stdin and never appears in argv or logs. A real run takes as long as the VM's data (minutes to hours on large VMs) |
| `parallels_vm_decrypt` | fleet + VM | Remove the encryption from an encrypted VM (`prlctl decrypt`). Requires the current password (stdin, never logged); a wrong password is a clean tool error and leaves the VM encrypted. `dry_run` checks prerequisites — but on an encrypted VM prlctl requires the password to authorise the check |
| `parallels_vm_change_encryption_password` | fleet + VM | Change the encryption password of an encrypted VM (`prlctl change-passwd`): `current_password` + `new_password`, both fed to prlctl over stdin (no confirmation prompt), never logged |
| `parallels_vm_reclaim_disk` | fleet + VM | Reclaim disk space a VM no longer uses (`prlctl reclaim-disk`) after deleting files in the guest. `estimate` reports the disk-space breakdown in bytes without reclaiming (read-only, any power state); a real run frees the space (as long as the disk). `allow_shutdown` may stop a running VM first. Encrypted VMs need their current password (stdin, never logged); a real run on an encrypted VM is not supported by prlctl (estimate works) |
| `parallels_vm_snapshot_create` | fleet + VM | Take a snapshot of a VM (`prlctl snapshot`): a checkpoint it can later be reverted to. Works on running and stopped VMs (state preserved); optional `name`/`description`; the new braced id comes back in `snapshot_id`. Snapshots consume host disk for their deltas. Encrypted VMs need their current password (stdin, never logged) |
| `parallels_vm_snapshot_list` | fleet + VM | List a VM's snapshots (`prlctl snapshot-list`): the whole tree by default (id, name, date, state, current, parent — date-sorted, empty list when the VM has none), one snapshot with `snapshot_id` (braces optional, unknown ids report `not_found`), or the ASCII tree with `tree`. Read-only. Encrypted VMs need their current password (stdin, never logged) |
| `parallels_vm_snapshot_delete` | fleet + VM | Delete a snapshot by id (`prlctl snapshot-delete`) — **irreversible**. Without `children`, deleting a parent re-roots its children; with `children` the whole subtree is deleted, including the current snapshot (the VM reverts to the deleted snapshot's parent state). Encrypted VMs need their current password (stdin, never logged) |
| `parallels_vm_snapshot_switch` | fleet + VM | Revert a VM to a snapshot by id (`prlctl snapshot-switch`): the live state rolls back and changes made after the snapshot are lost (the snapshots themselves stay listed). Works on running and stopped VMs; without `skip_resume` a snapshot taken while running starts the VM afterwards. Encrypted VMs need their current password (stdin, never logged) |
| `parallels_vm_sequence` | fleet + VM | Run a whole automation as one JSON document: an ordered list of steps — `{"tool": "<name>", "args": {...}}` (any of the 22 tools above), `{"wait": {"seconds": N}}` or `{"gate": [checks]}` — with `when`/`expect`/`retry`/`on_fail`/`on_error`/`quiet` modifiers and declarative checks (`exit_code`, `matches`, `contains`, `image_changed`, `succeeded`). Steps run in-process through the same handler code as the standalone tools; the full report comes back in one call, screenshots as native image blocks. Max 50 steps, 120 s default total timeout (1–900), 8 returned images max. One VM per sequence — fleet mode requires the top-level `vm_id`, VM mode uses the configured target, and steps must never set `vm_id` |

- **Fleet mode** (no `--vm-id`): reads target all VMs; `parallels_vm_info` takes a required `vm_id` (id or name) and fetches that VM live; power tools **require** a `vm_id` argument (a VM id or name).
- **VM mode** (`--vm-id <uuid-or-name>`): reads and power tools target that VM (info is served from the cache); prlctl resolves names to ids at fetch time; power tools and `parallels_vm_info` **reject** an explicit `vm_id` (a single-VM server must not touch other VMs).
- The cache warms at startup (a failed warm-up only logs a warning; tools retry on demand), refreshes single-flight when the TTL expires, and keeps stale data if a refresh fails. Every successful power command invalidates the cache so the next read reflects the new state.
- Power commands run with a 180s timeout (guest shutdowns can be slow); reads keep the 30s cap.
- `parallels_vm_capture` writes to a private temp directory (`0700`) that is
  removed on every path: screenshots never persist on disk.
- **Screenshots are sensitive data.** `parallels_vm_capture` returns the VM
  screen to the MCP client, so it reaches whichever model provider that
  client is connected to. A VM screen can show credentials, tokens, customer
  data or confidential source — do not capture such VMs while connected to an
  external AI service.
- **`parallels_vm_exec` runs arbitrary commands in the guest** as the
  logged-in user — including destructive ones. The command text is never
  logged, but it (and its output) reach the MCP client and its model
  provider; do not run commands with secrets in their arguments against VMs
  you do not fully control, and confirm destructive commands with the user.
- **`parallels_vm_type` / `parallels_vm_send_key` drive the guest
  keyboard.** Keys land in whatever window is focused in the guest — typed
  text can contain secrets and reaches the guest (and the model transcript).
  Confirm destructive input with the user, and avoid VMs whose focused
  window would react badly to stray keys.
- **`parallels_vm_delete` is irreversible.** It removes the VM and all of
  its files from the host. It requires a `confirm` argument that repeats
  the exact VM id or name, but that gate is a contract, not a backstop —
  only expose this tool to clients/agents you trust, and confirm any
  deletion with the user first.
- **`parallels_vm_clone` creates permanent host artifacts.** Full
  clones copy all of the source's VM files (check free disk space), and
  each clone needs a `parallels_vm_delete` to clean up. The source VM is
  never modified.
- **`parallels_vm_reclaim_disk` real runs mutate the VM's disk file**
  and can take minutes to hours on large VMs (estimate is read-only and
  instant; `allow_shutdown` may stop a running VM first). Automated
  tests only ever run the estimate; run a real reclaim deliberately and
  prefer a scratch VM when in doubt.
- **The encryption tools handle secrets.** `parallels_vm_encrypt`,
  `parallels_vm_decrypt` and `parallels_vm_change_encryption_password` are annotated
  `destructive: true` and take passwords as arguments. Passwords go to
  prlctl over stdin only — never argv, never logs, never error messages —
  but they do pass through the MCP client (and its transcript). A real
  encrypt locks the VM's data behind the password: you cannot start the
  VM without it, so never encrypt a VM you cannot afford to lock. Real
  encrypt/decrypt/change-password runs are never executed by the
  automated tests; the e2e suite only runs the non-mutating `dry_run`
  precondition checks.
- **Snapshot management mutates VM state.**
  `parallels_vm_snapshot_create` consumes host disk space for its deltas,
  `parallels_vm_snapshot_delete` is irreversible (with `children` it
  removes the whole subtree), and `parallels_vm_snapshot_switch` reverts
  the live state — changes made after the snapshot are lost from the
  running state (the snapshots stay listed). Only
  `parallels_vm_snapshot_list` is read-only, and it is the only snapshot
  op the automated tests ever run. All four are also available as
  `parallels_vm_sequence` steps.
- **`parallels_vm_sequence` can chain destructive steps.** It is annotated
  `destructive: true` and can contain anything its steps' standalone tools
  can — including deletion, hard resets, arbitrary guest commands and
  keyboard input. Read the full step list before confirming a sequence,
  prefer a scratch VM for non-read-only runs, and note that a sequence is
  serialized per VM (one at a time per target). `validate_only: true`
  checks the document without running anything.
- On hosts without Parallels Desktop the module stays off: the log says so and no `parallels_*` tool is exposed.

QA walkthroughs: [`docs/qa/002-parallels-desktop-module-QA.md`](docs/qa/002-parallels-desktop-module-QA.md), power operations: [`docs/qa/003-parallels-power-operations-QA.md`](docs/qa/003-parallels-power-operations-QA.md), screen capture: [`docs/qa/004-parallels-vm-capture-QA.md`](docs/qa/004-parallels-vm-capture-QA.md), command execution: [`docs/qa/005-parallels-vm-exec-QA.md`](docs/qa/005-parallels-vm-exec-QA.md), keyboard input: [`docs/qa/006-parallels-vm-keyboard-QA.md`](docs/qa/006-parallels-vm-keyboard-QA.md), sequence orchestration: [`docs/qa/007-parallels-vm-sequence-QA.md`](docs/qa/007-parallels-vm-sequence-QA.md), VM deletion: [`docs/qa/008-parallels-vm-delete-QA.md`](docs/qa/008-parallels-vm-delete-QA.md), VM cloning: [`docs/qa/009-parallels-vm-clone-QA.md`](docs/qa/009-parallels-vm-clone-QA.md), fleet-mode VM info: [`docs/qa/010-parallels-vm-info-fleet-QA.md`](docs/qa/010-parallels-vm-info-fleet-QA.md), VM encryption: [`docs/qa/011-parallels-vm-encryption-QA.md`](docs/qa/011-parallels-vm-encryption-QA.md), disk-space reclaim: [`docs/qa/012-parallels-vm-reclaim-disk-QA.md`](docs/qa/012-parallels-vm-reclaim-disk-QA.md), snapshot management: [`docs/qa/021-parallels-snapshots-QA.md`](docs/qa/021-parallels-snapshots-QA.md).

### Sequences

`parallels_vm_sequence` runs a whole automation in one call. Steps are
tool calls, waits, or logic gates; checks are declarative JSON — never
code. The server runs everything itself (same handler code as the
standalone tools, no MCP round trip) and returns an ordered result per
step, with screenshots as image blocks.

The motivating case — *capture, wait 3 s, capture again, only continue if
the screen changed*:

```json
{
  "vm_id": "ubuntu_github_builder",
  "steps": [
    { "id": "before", "tool": "parallels_vm_capture", "args": { "max_dimension": 1024 } },
    { "wait": { "seconds": 3 } },
    { "id": "after",  "tool": "parallels_vm_capture", "args": { "max_dimension": 1024 },
      "expect": [ { "check": "image_changed", "from": "before" } ] },
    { "tool": "parallels_vm_exec", "args": { "command": "journalctl -n 20 --no-pager" } }
  ]
}
```

Screen changed: all four steps run. Screen unchanged: the run stops at
`after` (`status: "stopped"`) and the exec never runs.

Poll until a service is up (no fixed sleeps):

```json
{ "id": "ready", "tool": "parallels_vm_exec",
  "args": { "command": "systemctl is-active nginx" },
  "expect": [ { "check": "matches", "field": "stdout", "regex": "^active" } ],
  "retry": { "max_attempts": 10, "delay_ms": 2000 } }
```

Wait for the screen instead of sleeping (polls `parallels_vm_capture`
internally and diffs the frames; the unmet case is `expectation_failed`
with the usual `on_fail` semantics):

```json
{ "wait": { "until": "settled", "timeout_seconds": 10, "poll_ms": 500, "stable_polls": 2 } }
{ "wait": { "until": "changed", "from": "before", "timeout_seconds": 10 } }
```

Useful forms:

- `when`: run a step only if a check on an earlier step holds (fallback
  branches: add `"negate": true`), e.g. restart a service only when the
  health check failed.
- `gate`: a standalone check step that stops the run when it fails.
- `on_error` / `on_fail`: `continue` to collect partial results instead of
  stopping.
- `quiet`: drop a step's output and image from the report (checks still
  see them — useful for a large intermediate capture in an
  `image_changed` chain).
- `validate_only: true`: validate the whole document and return
  `status: "validated"` without running anything.
- Progress: clients that send a `_meta.progressToken` get a
  `notifications/progress` per step, which keeps long calls alive through
  per-request client timeouts.

Limits: 50 steps, `wait` 0.1–300 s, retries 2–50 with 0–60 000 ms delay,
120 s default total timeout (max 900), regexes ≤ 1024 chars (RE2), 8
returned images (max 16, extras dropped but still diffable), 1 sequence
per VM at a time. See
[`docs/specs/007-parallels-vm-sequence.md`](docs/specs/007-parallels-vm-sequence.md)
for the full grammar and budgets.

**The stdout rule:** stdout carries the MCP protocol. Nothing else may be written there — logging goes to stderr (and optionally a file). `golangci-lint` (`forbidigo`) enforces this in CI.

## Debugging

stdin/stdout are the protocol channel, so a normal F5 session cannot feed JSON-RPC to the process. `.vscode/launch.json` provides three patterns:

| Pattern | When | How |
| --- | --- | --- |
| **A. Debug tests** | Day-to-day development | In-memory and e2e tests run the real server logic; set breakpoints in handlers and use "Debug current test file" / "Debug e2e test". |
| **B. Launch in integrated terminal** | Poking the server by hand | Delve launches with `console: integratedTerminal`; paste JSON-RPC lines into the terminal. |
| **C. Attach to a client-spawned process** | Debugging exactly what Claude / the Inspector does | The client spawns the binary; VS Code attaches to its PID. |

### Workflow for pattern C

1. Run `make build-debug` (produces `bin/pdfm-mcp-debug`).
2. Start the Inspector against the debug binary: `./scripts/inspect.sh ./bin/pdfm-mcp-debug`, or register it with Claude: `claude mcp add pdfm-mcp-debug -- $PWD/bin/pdfm-mcp-debug`.
3. In VS Code run **"C. Attach to running pdfm-mcp (pick process)"** and pick the `pdfm-mcp-debug` process. The server waits on stdin, so you have time to attach before the first request (the Inspector's "Connect" button is a natural pause point).
4. Set a breakpoint in `ListVms` (`internal/tools/parallels_list_vms.go`), then call `parallels_list_vms` from the client.

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
 scripts/install.sh   release installer (GitHub Releases -> /usr/local/bin)
docs/specs/          implementation specs
docs/qa/             QA walkthroughs, one per spec
```

All non-entrypoint code lives under `internal/`. No business logic in `main.go`. `bin/`, `dist/`, `coverage.out` are build outputs (git-ignored).

## Make targets

Run `make help` for the full list: `tidy`, `fmt`, `fmt-check`, `vet`, `lint`, `test`, `test-e2e`, `cover`, `check`, `build`, `build-debug`, `run`, `inspect`, `smoke`, `install`, `build-all`, `claude-add`, `claude-remove`, `clean`.
