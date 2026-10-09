# Spec 002: Parallels Desktop Module

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers |
| Scope | `internal/parallels` service module (detection, exec, cache, singleton), `--vm-id` targeting, `parallels_list_vms` + `parallels_vm_info` tools, e2e gating test, QA test spec |
| Out of scope | VM control commands (start/stop/suspend/resume/pause/restart), snapshots, `prlctl exec`, `prlsrvctl info`/`status`, VM config changes — each is a follow-up spec on this foundation |

## 1. Goal

Add a **Parallels Desktop module** to `pdfm-mcp` that lets an MCP client inspect and (in later specs) control Parallels VMs from the command line. The module must:

1. Detect `prlctl` and `prlsrvctl` at startup and **only expose `parallels_*` tools when both are found** (otherwise the module is silently disabled and `greet` keeps working).
2. Accept a target argument (`--vm-id`) that switches the server between **fleet mode** (no target: commands address all VMs) and **VM mode** (target: commands address that VM). The mode decides which commands are exposed.
3. Maintain an **in-memory cache** — a map of VM id → raw JSON blob plus a typed index — built at startup by running `prlctl list -a -i --json` (fleet) or `prlctl list <id> -a -i --json` (VM mode), so repeated tool calls are served from memory.
4. Provide the first two tools: `parallels_list_vms` (both modes) and `parallels_vm_info` (VM mode only).

The module is structured so that adding another Parallels command later is: one service method + one tool file + one registration line in `server.New` + tests.

### Acceptance criteria

1. `make check` passes (fmt, vet, lint, unit tests with race detector).
2. `make test-e2e` passes; the new gating test is skipped when `prlctl` is not on PATH (Linux CI) and asserts fleet-mode tool availability when it is.
3. On a Mac with Parallels Desktop: `make build && make smoke` lists `greet` + `parallels_list_vms`; with `--vm-id <id>` it also lists `parallels_vm_info`. The Inspector can call both tools; the second call within the TTL is served from cache (stderr log shows one `prlctl list` execution then cache hits).
4. With `prlctl`/`prlsrvctl` absent: the server starts, `greet` works, and no `parallels_*` tool is exposed.
5. Every step in `docs/qa/002-parallels-desktop-module-QA.md` passes on a machine with Parallels Desktop 26+.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Module location | `internal/parallels/` with PLAI-client-agent-style layout: `parallels.go` facade, `interfaces/`, `models/`, `service/` | Follows the service pattern of `plai/PLAI-client-agent` (`internal/system`: facade → `interfaces` + `service` with `Initialize`/`GetInstance`), scaled down |
| Singleton | `service` package: `var instance` + `sync.Once`; facade re-exports `Initialize(opts ...Option) (interfaces.ParallelsService, bool)`, `GetInstance() interfaces.ParallelsService` (panics if nil), `Reset()` (tests). Options: `WithVMID`, `WithLogger`, `WithRunner`, `WithLookPath`, `WithCacheTTL` | PLAI pattern; `WithRunner`/`WithLookPath` make the module fully unit-testable without Parallels installed |
| Detection gate | `exec.LookPath("prlctl")` **and** `exec.LookPath("prlsrvctl")`; on miss, fall back to stat-ing `/usr/local/bin/<exe>` then `/usr/bin/<exe>` (mirrors `FindPath` in the devops-service provider). **Both** must resolve or the whole module is disabled with an `info` log line | MCP clients (e.g. Claude Desktop) may launch us with a minimal PATH; both binaries are the agreed gate even though spec 002 only calls `prlctl` |
| Targeting | `--vm-id` flag + `PDFM_MCP_VM_ID` env (flag wins). Value is a VM UUID **or** name, passed verbatim to `prlctl` as a single argv element (no shell). Empty = fleet mode | `prlctl` accepts id or name; the user configures the target once and all VM-scoped commands share it |
| Tool availability | Fleet mode registers fleet-scoped tools only. VM mode registers fleet + VM-scoped tools. `parallels_list_vms` in both modes (VM mode returns only the target); `parallels_vm_info` in VM mode only. Future commands add to the conditional block in `server.New` | "the mcp can have an argument that is the vm id, that would also decide what commands are available" |
| Cache | `vmCache`: `raw map[string]json.RawMessage` (id → exact prlctl JSON blob), `vms []*models.VM` (typed index), `fetchedAt time.Time`, guarded by `sync.RWMutex` + a dedicated single-flight `refreshMu`. TTL default **30 s** (`PDFM_MCP_VM_CACHE_TTL`). `Invalidate()` / `InvalidateID(id)` for future mutating tools | "json maps with some blobs to make the mcp faster"; TTL prevents stale state; readers never block on a refresh |
| Warm-up | Synchronous, in `server.New` before serving: fleet → `prlctl list -a -i --json`; VM mode → `prlctl list <id> -a -i --json`. Failure is logged as `warn`; the server keeps running and tools report the error until a refresh succeeds | "when the mcp starts … if no id is added at start we run the prlctl list -a -i --json … then we cache it" |
| Exec | `CommandRunner` interface: `Run(ctx, exe string, args ...string) (string, error)`. Real impl: `exec.CommandContext` (30 s timeout), stdout/stderr into `bytes.Buffer` (never inherited — stdout rule), stderr folded into the error, 10 MB stdout cap | Mirrors devops-service `helpers.ExecuteWithNoOutput` semantics; AGENTS.md: bound sizes, no shell interpolation |
| Tool placement | One file per tool in `internal/tools/`, `Register<Name>(s *mcp.Server)` signature unchanged from spec 001 (handlers resolve `parallels.GetInstance()` at call time); tests pre-initialize the singleton with a fake runner and `Reset()` in cleanup | Keeps the AGENTS.md contract; `server.New` remains the single registration point |
| Dependencies | None added (stdlib + existing MCP SDK only) | AGENTS.md rule |

## 3. Repository layout (delta)

```
pdfm-mcp/
├── internal/
│   ├── parallels/                    # NEW — the module
│   │   ├── parallels.go              # facade: Initialize / GetInstance / Reset + Option aliases
│   │   ├── interfaces/
│   │   │   └── interfaces.go         # ParallelsService interface
│   │   ├── models/
│   │   │   └── vm.go                 # VM + nested structs (JSON tags = prlctl keys)
│   │   └── service/
│   │       ├── service.go            # singleton, detection, WarmCache
│   │       ├── exec.go               # CommandRunner + realRunner
│   │       ├── cache.go              # vmCache (typed index + raw blobs, TTL, single-flight)
│   │       └── service_test.go / exec_test.go / cache_test.go
│   ├── config/
│   │   └── config.go                 # +VMID, +VMCacheTTL, validation
│   ├── tools/
│   │   ├── parallels_list_vms.go     # NEW tool (both modes)
│   │   ├── parallels_vm_info.go      # NEW tool (VM mode only)
│   │   ├── parallels_test.go         # unit + in-memory protocol tests
│   │   └── errors.go                 # +sentinels
│   └── server/
│       └── server.go                 # New(logger, cfg); conditional registration + warm-up
├── cmd/pdfm-mcp/main.go              # passes cfg to server.New
├── test/e2e/stdio_test.go            # +TestParallelsGating
├── docs/
│   ├── specs/002-parallels-desktop-module.md   # this file
│   └── qa/002-parallels-desktop-module-QA.md   # NEW manual QA spec
└── README.md                         # config table, layout, module section
```

## 4. Core design

### 4.1 Detection and enablement

`Initialize` (once):

1. Resolve `prlctl` and `prlsrvctl`: `LookPath` first (injectable), then stat `/usr/local/bin/<exe>`, then `/usr/bin/<exe>`. Record the resolved paths.
2. If either is missing: log `info` ("Parallels Desktop module disabled: prlctl not found …") and the facade returns `(nil, false)`. `server.New` registers no `parallels_*` tools.
3. If both are found: build the `vmCache`, record `VMID` from config (validated), return `(svc, true)`.

### 4.2 Targeting modes

| | Fleet mode (`--vm-id` empty) | VM mode (`--vm-id` set) |
|---|---|---|
| Warm-up command | `prlctl list -a -i --json` | `prlctl list <id> -a -i --json` |
| `parallels_list_vms` | registered; returns all cached VMs | registered; returns only the target VM |
| `parallels_vm_info` | **not registered** | registered; returns the raw cached blob of the target |
| Future VM commands (start/stop/…) | not registered | registered (follow-up specs) |

`VMID` validation (config load): non-empty → ≤ 256 chars, no NUL bytes, printable. Invalid → `Load` returns an error (fail fast before serving).

### 4.3 The cache

```go
type vmCache struct {
    mu        sync.RWMutex
    vms       []*models.VM          // typed index, stable order = prlctl output order
    raw       map[string]json.RawMessage // id -> exact prlctl JSON blob ("the blobs")
    fetchedAt time.Time             // zero = never fetched
    ttl       time.Duration
    refreshMu sync.Mutex            // single-flight: only one refresh goroutine
}
```

- `GetAll(ctx, force)` / `GetRaw(ctx, id, force)`: read under `RLock`; if `force` or `time.Since(fetchedAt) > ttl`, run `refreshIfStale` (double-checked under `refreshMu`, so N concurrent callers trigger exactly one `prlctl` run); readers holding a copy are never blocked by a refresh.
- `Refresh(ctx)`: run the list command (with or without the target id), parse the JSON array, validate `ID` on each entry, rebuild `vms` + `raw` under `Lock`, set `fetchedAt`. On failure the previous cache is **retained** (stale beats empty) and the error is returned.
- Empty array is valid (zero VMs on the machine) → `count: 0`, no error.
- `Invalidate()` / `InvalidateID(id)` zero `fetchedAt` / drop one entry — used by future mutating commands.

### 4.4 Exec

```go
type CommandRunner interface {
    Run(ctx context.Context, exe string, args ...string) (string, error)
}
```

Real runner: `exec.CommandContext` with a 30 s timeout (shorter if `ctx` is tighter); `cmd.Stdout`/`cmd.Stderr` are `bytes.Buffer`s — **never inherited** (stdout rule, enforced by the linter and design); on non-zero exit the error includes trimmed stderr; stdout > 10 MB → error. No shell, no interpolation: `VMID` is one argv element.

### 4.5 Tools

**`parallels_list_vms`** (both modes, `ReadOnlyHint: true`)

- Input: `{"refresh": false}` — optional; `true` forces a re-run of the list command.
- Output:
  ```json
  { "vms": [ /* models.VM, JSON keys identical to prlctl */ ],
    "count": 1,
    "fetched_at": "2026-10-09T10:00:00Z",
    "source": "cache" | "refreshed" }
  ```
- Errors (→ tool error): module disabled (cannot happen if registered), VM not found (VM mode), prlctl failure (with its stderr text), unparseable output.

**`parallels_vm_info`** (VM mode only, `ReadOnlyHint: true`)

- Input: `{"refresh": false}`.
- Output:
  ```json
  { "vm": { /* the exact cached prlctl JSON blob for the target VM */ },
    "fetched_at": "…", "source": "cache" | "refreshed" }
  ```
  The raw blob is returned verbatim so no field is ever lost by the typed model.

### 4.6 Wiring in `server.New`

```go
func New(logger *slog.Logger, cfg config.Config) *mcp.Server {
    s := mcp.NewServer(...)
    tools.RegisterGreet(s)

    if svc, ok := parallels.Initialize(
        parallels.WithVMID(cfg.VMID),
        parallels.WithLogger(logger),
        parallels.WithCacheTTL(cfg.VMCacheTTL),
    ); ok {
        tools.RegisterParallelsListVms(s)
        if cfg.VMID != "" {
            tools.RegisterParallelsVmInfo(s)
        }
        if err := svc.WarmCache(context.Background()); err != nil {
            logger.Warn("parallels warm-up failed; tools will retry on demand", "err", err)
        }
    }
    return s
}
```

`Initialize` is a no-op when the singleton already exists (tests initialize first with a fake runner; `Reset()` in cleanup). Warm-up failure never fails startup.

### 4.7 Configuration

| Variable / flag | Default | Meaning |
|---|---|---|
| `--vm-id` / `PDFM_MCP_VM_ID` | empty (fleet mode) | VM id or name to target; VM-scoped tools are only exposed when set |
| `PDFM_MCP_VM_CACHE_TTL` | `30s` | How long the VM list cache is considered fresh |

Existing `PDFM_MCP_LOG_LEVEL` / `PDFM_MCP_LOG_FILE` / `--log-level` / `--version` unchanged.

## 5. Models

`internal/parallels/models/vm.go` — `VM` struct ported from `devops-workspace/…/src/models/virtual_machines.go`, adjusted to the Parallels 27 sample (see QA doc): `ID`, `Name`, `Description`, `Type`, `State`, `OS`, `Template`, `Uptime` (string), `"Home path"` (`HomePath`), `Home`, `"Restore Image"`, `GuestTools`, `MouseAndKeyboard`, `USBAndBluetooth`, `StartupAndShutdown`, `Optimization`, `Security`, `SmartGuard`, `Modality`, `Fullscreen`, `Coherence`, `TimeSynchronization`, `Expiration`, `BootOrder`, `BIOSType`, `EFISecureBoot`, `AllowSelectBootDevice`, `ExternalBootDevice`, `SMBIOSSettings`, `Hardware{CPU, Memory, Video, MemoryQuota, Hdd0, Cdrom0, USB, Net0, Sound0}`, `HostSharedFolders`, `HostDefinedSharing`, `SharedProfile`, `SharedApplications`, `SmartMount`, `Network{Conditioned, IPAddresses}`, `MiscellaneousSharing`, `Advanced`. All JSON tags match prlctl's exact key spelling (including `"Show on all spaces "` with its trailing space). Fields not in the model are ignored on decode; the **raw blob** preserves them for `parallels_vm_info`.

## 6. Edge cases & invariants

- **Missing binaries** → module disabled, `info` log, `greet` unaffected (unit test with fake `LookPath`; e2e skip on Linux).
- **Parallels Server not running** → warm-up `warn`; tools return a tool error containing prlctl's stderr until a refresh succeeds.
- **Unknown `--vm-id`** → warm-up `warn`; VM tools return "VM not found" tool error.
- **Zero VMs** → `vms: []`, `count: 0`, no error.
- **Malformed JSON from prlctl** → tool error; debug log keeps the first 500 chars of output.
- **Concurrent tool calls during refresh** → single-flight (8 parallel `ListVMs` with a slow fake runner asserts exactly one exec, `-race`).
- **TTL expiry** → transparent re-fetch (50 ms TTL fake counting execs).
- **Hostile `--vm-id`** → rejected at config load (length/NUL/printable); always a single argv element, never shell-interpolated.
- **Very large list** → 10 MB stdout cap in the runner → clean error.
- **stdout rule** — the runner never inherits stdout; `forbidigo` keeps enforcing it; `make smoke` output stays pure protocol.

## 7. Tests

1. `internal/parallels/service/*_test.go` — detection (found / missing / fallback dir / both required), cache (TTL expiry, single-flight, invalidation, VM-mode fetch, unknown VM, malformed JSON, empty list, error retains stale cache), exec (timeout, stderr in error, output cap), config validation.
2. `internal/tools/parallels_test.go` — unit (handler via pre-initialized singleton with `FakeRunner` serving the sample JSON; fleet vs VM mode; `refresh:true` forces a second exec; unknown VM → tool error) + in-memory protocol (fleet: `parallels_list_vms` present, `parallels_vm_info` absent; VM mode: both; two calls → exactly one fake exec = cache hit).
3. `test/e2e/stdio_test.go` — `TestParallelsGating` (skips without `prlctl`): spawn the binary in fleet mode; assert `parallels_list_vms` listed, `parallels_vm_info` not; call `parallels_list_vms` → no tool error, `count >= 0`, every vm has a non-empty `ID`.
4. `docs/qa/002-parallels-desktop-module-QA.md` — manual steps incl. a real-machine VM-mode matrix and the negative PATH test.

## 8. Implementation order

1. This spec + index row in `docs/specs/README.md`.
2. `internal/parallels` bottom-up: `models` → `exec` → `cache` → `service` → `interfaces` → facade; service tests as we go.
3. `internal/config` additions + validation.
4. Tools + tool tests.
5. `server.New(logger, cfg)` + `main.go` + existing test call sites.
6. e2e gating test.
7. QA doc + README update.
8. `make check`, `make test-e2e`, manual QA walk-through.

## 9. Follow-up specs (out of scope here)

- VM lifecycle: `start` / `stop` / `restart` / `suspend` / `resume` / `pause` (+ `InvalidateID` after each).
- Snapshots: `snapshot` / `snapshot-list` / `snapshot-delete` / `snapshot-switch` (`--json`).
- `prlctl exec` (command execution inside the VM) with timeout + user scoping.
- `prlsrvctl info` / `prlsrvctl status` (server-level tools, prlsrvctl-backed).
- VM configuration changes (`set -net0 ...` etc.), `delete` / `clone` (destructive — `DestructiveHint`).

## 10. Known unknowns / verification notes

- `prlctl list <id> -a -i --json` output shape for a single VM is assumed identical to one element of the full list (verified in QA step 5 against this machine).
- `prlctl` version 27 field drift vs the devops model (26-era) — the QA sample in `docs/qa` is the source of truth; unknown keys are dropped by the typed model but kept in the raw blob.

### Deviations (fill in during implementation)

1. **Timeout detection via `runCtx.Err()`**: on go1.27.1 a context-killed child
   surfaces as a plain `signal: killed` `*exec.ExitError` with no `Unwrap`
   method, so `errors.Is(err, context.DeadlineExceeded)` is false. `exec.go`
   checks `runCtx.Err()` after a failed `Run` instead of wrapping context
   errors into the exit error.
2. **`RefreshCache` instead of `WarmCache`**: the interface method is named
   `RefreshCache(ctx)` (the plan/spec draft said `WarmCache`); server.New calls
   it once at startup as the warm-up.
3. **Result structs instead of multi-value returns**: `ListVMs`/`VMRaw` return
   `VMListResult`/`VMRawResult` (carrying `FetchedAt` and a `Refreshed` flag)
   so tools can report `source: cache|refreshed`.
4. **`VmInfoOutput.VM` is `any`, not `json.RawMessage`**: the SDK's schema
   inference maps a `[]byte`-backed type to an array/null schema, which
   rejects the real object. `any` marshals the raw blob verbatim.
5. **`WithFallbackDirs` option (tests only)**: detection fallback dirs are
   injectable so unit/protocol tests stay hermetic on hosts that do have
   Parallels installed (empty = disable fallbacks; not passed = defaults).
6. **Portable output schemas + direct dependency `github.com/google/jsonschema-go`
   v0.4.3**: the SDK's inference (this same library, reached via the go-sdk)
   emits `["null","array"]` type unions for every Go slice. That is legal JSON
   Schema but is rejected or mishandled by clients that map tool schemas onto
   single-`type` dialects (e.g. the OpenAPI subset for Gemini function
   declarations). The parallel tools therefore register an explicit
   `OutputSchema` — generated with `jsonschema.For` (kept in sync with the Go
   model) and normalized so slice fields are plain `"type": "array"` — and the
   handlers guarantee those fields are never nil (`models.VM.Normalize` fills
   `Network.IPAddresses`; the handlers nil-guard the top-level `vms`).
   Reason for the direct dependency (repo rule): the SDK only exposes its
   inferred schema via `Tool.OutputSchema`, and there is no SDK-level option to
   suppress null-union inference, so the normalization must be done on the
   inferred `*jsonschema.Schema` value, which requires the library directly.
   Protocol tests assert no `["null","array"]` union survives on the wire.
