# QA — Parallels power operations (spec 003)

## What this task delivers

- Eight power tools on the existing module: `parallels_vm_start`,
  `parallels_vm_resume`, `parallels_vm_pause`, `parallels_vm_suspend`,
  `parallels_vm_restart`, `parallels_vm_reset`, `parallels_vm_reset_uptime`,
  `parallels_vm_stop` (stop takes optional `kill` / `drop_state`).
- Mode contract: fleet mode **requires** the `vm_id` argument (id or name —
  enforced in the input schema *and* at runtime); VM mode uses the `--vm-id`
  target and **rejects** an explicit `vm_id`.
- 180s timeout for power commands (reads keep 30s); prlctl stderr surfaced in
  tool errors; successful commands invalidate the VM list cache.
- `docs/specs/003-parallels-power-operations.md` (design) + this QA spec.

**Safety note.** Power commands mutate real VMs. The automated suites use a
fake runner only; the e2e test asserts the tools are *listed* but never
*executes* them. The manual steps below stick to **negative executions**
(state transitions prlctl refuses, so nothing changes) — actual
start/stop/restart is left to the operator.

## Prerequisites

- Same host as the QA 002 walkthrough (Parallels Desktop, `prlctl` on
  PATH), Go toolchain.
- A stopped VM to point negative commands at (on the reference machine:
  `ubuntu_github_builder`, `90f90e2b-e19f-4429-86b3-b5146bc860d4`).

Build once for all steps:

```sh
cd pdfm-mcp
go build -o bin/pdfm-mcp ./cmd/pdfm-mcp
```

## Step-by-step

### 1. Automated suites (run these first)

```sh
make check       # fmt, vet, lint, CGO_ENABLED=1 go test -race ./...
make test-e2e    # gating test now also asserts all 8 power tools are listed
```

Expected: all green. No test executes a real power command.

### 2. Fleet mode — tool list and schema

```sh
./bin/pdfm-mcp        # no --vm-id
```

1. `tools/list` → all eight `parallels_vm_*` power tools plus
   `parallels_list_vms`.
2. Inspect `parallels_vm_start.inputSchema` → `"required": ["vm_id"]`.
3. Call `parallels_vm_start` with `{}` → **schema validation error**
   ("missing properties: [\"vm_id\"]"); no prlctl invocation.

### 3. Fleet mode — safe negative executions (nothing changes)

Against a stopped VM (e.g. `ubuntu_github_builder`):

1. `parallels_vm_pause` `{"vm_id": "ubuntu_github_builder"}` → tool error
   from prlctl (cannot pause a stopped VM). The VM stays stopped — verify
   with `parallels_list_vms` (`State` unchanged, `source: "cache"`).
2. `parallels_vm_start` `{"vm_id": "no-such-vm"}` → tool error containing
   prlctl's "not found"-style message.

Do **not** use `parallels_vm_stop` as a "safe negative": a stopped VM can
still carry leftover suspend state, and `stop --drop-state` (or a plain
`stop` on a suspended VM) then performs a real state change. Stop flag
wiring (`--kill` / `--drop-state`) is covered by the service unit tests and
can be confirmed in the process log during the operator-only step 5, where
the failing/succeeding argv is part of the logged error/result.

### 4. VM mode — target contract

```sh
./bin/pdfm-mcp --vm-id ubuntu_github_builder
```

1. `tools/list` → power tools present; `parallels_vm_start.inputSchema`
   does **not** require `vm_id`.
2. `parallels_vm_pause` `{}` → targets `ubuntu_github_builder` (prlctl error
   because it is stopped; log shows `pause <target>`). No state change.
3. `parallels_vm_stop` `{"vm_id": "windows11"}` → tool error
   "vm_id must be omitted"; nothing runs (the single-VM server refuses to
   touch another VM).

### 5. Operator-only: a real transition (optional, at your discretion)

```
parallels_vm_start {"vm_id": "<a stopped VM>"}   # or the same tool in VM mode with {}
```

Then `parallels_list_vms` → `source: "refreshed"` (the cache was invalidated
by the successful command) and `State` changed. Stop the VM again when done
(`parallels_vm_stop`, or the Parallels UI).

### 6. Error & timeout behavior

1. Stop Parallels Server Manager (`prlsrvctl status` → down), then call any
   power tool → prlctl failure surfaced with its stderr text.
2. Timeout is bounded at 180s for power ops; a wedged guest shutdown can take
   minutes without tripping it (unit-tested with the fake runner).

## Checklist

| # | Check | Where |
|---|---|---|
| 1 | All eight power tools listed in both modes | e2e `TestParallelsGating` + steps 2, 4 |
| 2 | Fleet: `vm_id` required (schema + runtime) | protocol tests + step 2 |
| 3 | VM: explicit `vm_id` rejected, configured target used | unit + protocol tests + step 4 |
| 4 | Argv construction per op, stop flags | service unit tests (+ step 5 log) |
| 5 | prlctl errors surfaced with stderr | unit tests + step 3 |
| 6 | Cache invalidated after a successful op | unit + protocol tests + step 5 |
| 7 | 180s power deadline applied (not the 30s read cap) | service unit test |
| 8 | No `["null","array"]` unions in any power tool schema | protocol tests (spec 002 scanner) |
| 9 | No test ever executes a real power command | code review of suites |

## Known follow-ups

- No built-in confirmation for destructive ops (`restart`, `reset`,
  `stop --kill`); consider a `confirm: true` argument if desired.
- `prlctl exec` and `prlsrvctl` status/server tools are the natural next
  additions (spec 004+).
