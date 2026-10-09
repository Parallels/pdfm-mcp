# QA — Parallels Desktop module (spec 002)

## What this task delivers

- Startup detection of `prlctl` / `prlsrvctl` (PATH + `/usr/local/bin`, `/usr/bin`
  fallbacks). Both must be present or the module stays off and no `parallels_*`
  tool is exposed.
- `--vm-id <uuid-or-name>` targeting: fleet mode exposes `parallels_list_vms`
  only; VM mode additionally exposes `parallels_vm_info`.
- In-memory VM cache: `prlctl list -a -i --json` (fleet) or
  `prlctl list <id> -a -i --json` (VM mode), typed model + raw JSON blobs,
  30s TTL (`PDFM_MCP_VM_CACHE_TTL`), single-flight refresh, stale data kept on
  failed refresh.
- `docs/specs/002-parallels-desktop-module.md` (design) + this QA spec.

## Prerequisites

- macOS with Parallels Desktop installed (≥ 27 tested: `prlctl version` →
  `27.0.1 (58670)`, binaries in `/usr/local/bin`).
- Go 1.25+ toolchain for the build/test commands.
- At least one VM for the VM-mode steps (`ubuntu_github_builder`,
  `90f90e2b-e19f-4429-86b3-b5146bc860d4`, on the reference machine).

Build once for all steps:

```sh
cd pdfm-mcp
go build -o bin/pdfm-mcp ./cmd/pdfm-mcp
```

## Step-by-step

### 1. Automated suites (run these first)

```sh
make check       # fmt, vet, lint, CGO_ENABLED=1 go test -race ./...
make test-e2e    # go test -tags e2e ./test/e2e/... (skips gating test w/o prlctl)
make smoke       # starts the server on stdio; expect tool list (with the
                 # eight parallels_vm_* power tools from spec 003):
                 #   fleet mode:  greet, parallels_list_vms, + 8 power tools
                 #   VM mode:     greet, parallels_list_vms, parallels_vm_info,
                 #                + 8 power tools
```

Expected: all green, no stdout pollution, no `panic` in stderr.

### 2. Fleet mode — list all VMs from the cache

```sh
./bin/pdfm-mcp        # no --vm-id
```

Client (or MCP inspector):

1. `tools/list` → `greet`, `parallels_list_vms` — **not** `parallels_vm_info`.
2. `tools/call` `parallels_list_vms` `{}` →
   `count` == number of VMs, each entry has `ID`, `Name`, `State`,
   `Hardware.cpu.cpus`, `fetched_at` (RFC3339), `source: "cache"`
   (warm-up already ran `prlctl list -a -i --json` at startup).
3. Call `parallels_list_vms` again → same data, `source: "cache"`, and the
   process log shows no second `prlctl list` (single exec per TTL window).
4. `tools/call` `parallels_list_vms` `{"refresh": true}` → `source: "refreshed"`.

### 3. VM mode — target a specific VM

```sh
./bin/pdfm-mcp --vm-id ubuntu_github_builder     # name works
./bin/pdfm-mcp --vm-id 90f90e2b-e19f-4429-86b3-b5146bc860d4   # UUID works
```

1. `tools/list` → `greet`, `parallels_list_vms`, `parallels_vm_info`.
2. `parallels_list_vms` → `count: 1`, the only entry is the target VM.
3. `parallels_vm_info` → `vm` is the **exact prlctl JSON object** (compare
   against `prlctl list ubuntu_github_builder -a -i --json` manually; fields
   the typed model does not carry, e.g. `Uptime`, must still be present),
   `vm.ID == 90f90e2b-…`, `fetched_at` set, `source` set.
4. Unknown target: `./bin/pdfm-mcp --vm-id does-not-exist` → tools still
   register, `parallels_list_vms` returns a tool error
   `VM "does-not-exist" not found (prlctl returned no results)`; server stays up.
5. Multiple matches: pass a name that matches several VMs → startup log
   contains a `warn` about multiple matches and the first one is used.

### 4. Negative gating — binaries missing

On a machine without Parallels (or, on the reference Mac, by faking PATH —
note the fallback dirs, so this is most reliable on a clean Linux box or a
macOS VM without Parallels):

```sh
PATH=/usr/bin:/bin ./bin/pdfm-mcp     # only meaningful without /usr/local/bin
```

1. Startup log line: `parallels module disabled: Parallels Desktop CLI not found`.
2. `tools/list` → only `greet`; `greet` still works.

On the reference Mac the fallback dirs intentionally find
`/usr/local/bin/prlctl`, so a missing-PATH run there is still "enabled" —
that is by design (MCP clients launch with a minimal PATH).

### 5. Cache TTL configuration

```sh
PDFM_MCP_VM_CACHE_TTL=5s ./bin/pdfm-mcp
```

1. Two `parallels_list_vms` calls 6s apart → second has a newer `fetched_at`
   and `source: "refreshed"`.
2. Invalid value: `PDFM_MCP_VM_CACHE_TTL=banana ./bin/pdfm-mcp` → the process
   exits with a startup error `invalid PDFM_MCP_VM_CACHE_TTL "banana"`, before
   any MCP handshake. `0s` is rejected too.

### 6. Untrusted input

`--vm-id` is a single argv element (no shell). Values are trimmed and
validated (≤ 256 printable chars):

```sh
./bin/pdfm-mcp --vm-id "$(printf 'a%.0s' {1..300})"   # rejected at startup
./bin/pdfm-mcp --vm-id 'vm; rm -rf /'                 # treated as a literal name
```

The second must fail cleanly with a `not found` tool error, not execute
anything.

## Edge cases & invariants

| # | Invariant | Verified by |
|---|-----------|-------------|
| 1 | Module enabled ⇔ both binaries resolved | unit `TestInitializeDetection` |
| 2 | Fallback dirs only when LookPath misses | unit `TestInitializeFallbackDir` |
| 3 | Fleet list = full prlctl array; VM list = only the target | unit + e2e `TestParallelsGating` |
| 4 | First call after TTL expiry re-runs prlctl exactly once, even under 8 concurrent callers (single-flight) | unit `TestRefreshSingleFlight` |
| 5 | Failed refresh keeps stale data; empty cache + failed fetch → error | unit `TestFetchErrorRetainsStaleCache` |
| 6 | `parallels_vm_info` never registered in fleet mode | unit protocol tests + e2e |
| 7 | Warm-up failure never fails startup (log `warn`, retry on demand) | server.New code path (review) |
| 8 | Tool args never reach a shell; output capped at 10 MiB | unit `exec_test.go` |
| 9 | stdout carries only the JSON-RPC protocol; all logs on stderr | all tests + smoke |
| 10 | Cache blobs are copies: mutating a tool result never corrupts the cache | unit `TestGetRawReturnsCopy` |

## Known follow-ups

- `prlsrvctl` is detected but not yet used for commands (server state /
  start-stop tools are the obvious next additions; the `CommandRunner`
  seam already supports them).
- VM names that collide between machines (same name, different IDs) resolve
  to the first match by prlctl; prefer UUIDs in `--vm-id` for precision.
- No watch/invalidation on the cache besides TTL + `refresh: true` +
  `InvalidateCache`. The power tools (spec 003) call it after every
  successful command, so reads always reflect the latest VM state.
- E2E "binaries missing" path is only exercisable where Parallels is not
  installed (covered at unit level instead).
