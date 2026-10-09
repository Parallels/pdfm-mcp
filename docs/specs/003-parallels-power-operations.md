# Spec 003: Parallels Power Operations

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers |
| Scope | Eight mutable `parallels_vm_*` tools (start, resume, pause, suspend, restart, reset, reset_uptime, stop) on the existing `internal/parallels` module; target resolution per mode; power command timeout; cache invalidation after mutations |
| Out of scope | `prlctl exec`, VM config changes, snapshots, `prlsrvctl` server commands, auto-generated UI affordances — follow-up specs |

## 1. Goal

Expose the Parallels power commands as MCP tools, extending the module from
read-only inspection to lifecycle control:

| Tool | prlctl command | Destructive |
|---|---|---|
| `parallels_vm_start` | `prlctl start <vm>` (starts a stopped VM or resumes a paused one) | no |
| `parallels_vm_resume` | `prlctl resume <vm>` | no |
| `parallels_vm_pause` | `prlctl pause <vm>` | no |
| `parallels_vm_suspend` | `prlctl suspend <vm>` | no |
| `parallels_vm_restart` | `prlctl restart <vm>` (graceful shutdown + start) | yes |
| `parallels_vm_reset` | `prlctl reset <vm>` (hard shutdown + start) | yes |
| `parallels_vm_reset_uptime` | `prlctl reset-uptime <vm>` | no |
| `parallels_vm_stop` | `prlctl stop <vm> [--kill] [--drop-state]` | yes |

## 2. Target resolution (the mode contract)

- **VM mode** (server started with `--vm-id`): the configured target is used.
  An explicit `vm_id` argument is **rejected** with a clear error — a
  single-VM-scoped server must not power-cycle other VMs.
- **Fleet mode** (no `--vm-id`): the `vm_id` argument is **required** (enforced
  in the input schema per mode and at runtime). It accepts a VM **id or name**;
  prlctl resolves names itself.
- The argument is trimmed and validated exactly like `--vm-id`
  (`service.ValidateVMRef`: non-empty after trim, ≤ 256 chars, printable
  ASCII, no NUL). It is passed as a single argv element — never through a
  shell. The prlctl subcommand comes from a fixed enum, so no attacker input
  can become a command or flag.

## 3. Service layer

- `interfaces.PowerOp` enum (the eight subcommands) +
  `ParallelsService.Power(ctx, op, vmRef, kill, dropState) (string, error)`
  returning prlctl's (usually empty) stdout.
- `stop` maps the `kill` / `drop_state` tool arguments to `--kill` /
  `--drop-state`.
- **Timeout**: reads keep the 30s cap; power commands get a **180s** cap
  (guest shutdowns can be slow). Implementation: `realRunner` honors the
  caller's context deadline when one is set, and applies the 30s default only
  as a backstop for deadline-free contexts.
- **Cache invalidation**: a successful power command marks the whole VM list
  cache stale (the VM's `State`/`Uptime` changed). The next read re-runs
  `prlctl list`. This wires up the `InvalidateCache` hook left by spec 002.
- prlctl failures (wrong state transition, unknown VM, …) surface as tool
  errors with the child's stderr folded in, capped as before.

## 4. Tool layer

- One file per tool in `internal/tools/` + `Register<Name>(s, mode)`, per the
  repo convention. The handlers are plain exported functions
  (`VMStart`, `VMStop`, …) that resolve the singleton at call time; the
  **mode** (fleet vs VM) only shapes registration (description + whether
  `vm_id` is in the input schema's `required` list).
- Input: `vm_id` (string, optional in schema; enforced per mode) and, for
  `stop` only, `kill` and `drop_state` (booleans).
- Output: `{ vm_id, output }` — the targeted reference and prlctl's stdout.
- Annotations: all tools are non-read-only; `restart`, `reset`, `stop` carry
  `DestructiveHint: true`.
- Output schemas are registered with the same portable normalization as spec
  002 (no `["null","array"]` unions).

## 5. Registration

`server.New` registers all eight power tools whenever the module is enabled
(both modes); the mode is passed to each `Register…` for the description and
required-ness of `vm_id`.

## 6. Testing

- **Unit (service)**: argv construction per op (incl. stop flags), ctx
  deadline propagation, validation (empty/NUL/over-long), unknown op rejected,
  failure propagation, cache invalidation on success.
- **Unit (tools, table-driven over all eight)**: VM mode uses configured
  target; VM mode + explicit `vm_id` rejected; fleet mode without `vm_id`
  rejected; fleet mode with `vm_id` targets it; stop flags mapped.
- **Protocol (in-memory)**: all eight listed in both modes; `vm_id` present
  in `required` only in fleet mode; wire call executes the right argv and the
  next `parallels_list_vms` reports `source: "refreshed"` (cache invalidated).
- **e2e**: the gating test asserts the power tools are **listed** in both
  modes. It must never *execute* a power command in CI — that would power
  cycle real VMs on the developer's machine.
- **Manual QA** (`docs/qa/003`): safe negative executions on the real machine
  (state-transition errors, unknown VM) — actual start/stop left to the user.

## 7. Known follow-ups

- Per-operation result detail (prlctl prints little; richer state could come
  from a follow-up `list` of the affected VM).
- `prlctl exec`, snapshots, `prlsrvctl status/info`, VM config edits.
- Optional confirmation flow for destructive ops (the MCP spec has no
  built-in; could be an argument like `confirm: true`).

## 8. Deviations (fill in during implementation)

1. `DestructiveHint` is a `*bool` in the SDK — the tools pass explicit
   pointers instead of bare bools.
2. `ValidateVMRef` lives in `internal/parallels/service` (re-exported by the
   facade); `internal/config` delegates to it instead of keeping a private
   duplicate, so one security rule has one implementation.
3. The exec runner's default cap became a package variable
   (`defaultCommandTimeout`) so tests can shorten it; production value is
   unchanged (30s).
