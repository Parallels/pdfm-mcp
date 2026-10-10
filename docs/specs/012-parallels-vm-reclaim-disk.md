# Spec 012 — Parallels VM Disk Space Reclaim (`reclaim-disk`)

| Field | Value |
|---|---|
| Status | Implemented |
| Depends on | 001 (scaffold), 002 (Parallels module), 005 (CommandRunner/RunStdin), 011 (password transport, long-op timeout) |
| CLI basis | `prlctl reclaim-disk <vm_id> [--estimate] [--allow-shutdown] [--json]` |
| QA | [docs/qa/012-parallels-vm-reclaim-disk-QA.md](../qa/012-parallels-vm-reclaim-disk-QA.md) |

## 1. Goal

Expose `prlctl reclaim-disk` as one tool, `parallels_vm_reclaim_disk`, that
either (a) reports how much disk space the VM could give back
(`--estimate`) or (b) actually optimizes the VM's disk file
(`prlctl reclaim-disk`), in both server modes. Reclaiming is the right
tool after the guest deleted a lot of files; it does not lose data, but
a real run touches the VM's disk file and (with `allow_shutdown`) may
power-cycle a running VM.

### Live-verified protocol facts (Parallels 27.0.1, disposable VM
`empty_catalog_test`)

| Case | Behaviour |
|---|---|
| `--estimate --json`, stopped, unencrypted | `{"diskSpace":{"hardDisks":N,"snapshots":N,"miscellaneous":N,"reclaimable":N}}` (bytes), exit 0 |
| `--estimate` without `--json` | Human text ("Disk space: / Hard Disks: 1.15 MiB / …") |
| `--estimate`, **running** VM | Works — estimate is read-only and state-independent, exit 0 |
| Real reclaim, stopped, unencrypted | stdout "Reclaiming disk, please wait…", exit 0. `--json` makes **no difference** — the flag only changes the estimate output; a real run never prints JSON |
| Real reclaim, **running** VM, no `--allow-shutdown` | **Succeeds on 27.0.1** (VM stays running) — the CLI doc text ("must be shut down first") is stale for this version |
| Real reclaim, running VM, `--allow-shutdown` | Succeeded; the VM was **not** shut down (reclaim worked while it ran), exit 0 |
| Encrypted VM, estimate, no password | "Failed to authorise current session … password … invalid", exit 255 |
| Encrypted VM, estimate, password piped on stdin | Works, exit 0 |
| Encrypted VM, real reclaim, no password | Authorise error, exit 255 |
| Encrypted VM, real reclaim, password piped | "An error occurred while compressing the hard disk …", exit 255 — **real reclaim is not supported on encrypted VMs** (estimate still works) |
| Piping a password to an **unencrypted** VM | Harmless — prlctl ignores stdin; both estimate and real reclaim succeed |
| Bogus VM id/name (both modes) | "Failed to get VM config: The virtual machine could not be found…", exit 255 |
| Reclaim on an already-reclaimed disk | No-op, exit 0 (idempotent) |

### Acceptance criteria

- One tool `parallels_vm_reclaim_disk` registered in **both** modes;
  standard mode contract (`vm_id` required in fleet, rejected in VM mode).
- `estimate: true` → `prlctl reclaim-disk <ref> --estimate --json`;
  the JSON is parsed into a structured `disk_space` object (bytes).
- Real run → `prlctl reclaim-disk <ref> [--allow-shutdown]`, stdout
  passed through trimmed. `--json` is never sent (it only affects
  estimate output).
- `password` (optional): required only when the VM is encrypted. When
  present it is fed over stdin as one line (never argv/logs/errors);
  when absent the plain Run path is used and an encrypted VM surfaces
  prlctl's clean authorise error (self-correcting for the agent).
- No password is ever sent by automated tests; e2e runs only the
  non-mutating estimate against an unencrypted VM.
- Real runs use the long-op backstop timeout (spec 011's
  `longDiskOpTimeout`), because the MCP request context carries no
  deadline (verified) and reclaim scales with disk size.
- All VM-scoped repo rules: in `tools.RegisterSteps` (drift guard),
  nil-`*mcp.CallToolRequest`-safe handler, unit + in-memory protocol
  tests, `make check` + `make test-e2e` green.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| One tool, two modes | `estimate` + `allow_shutdown` booleans | The CLI is one action with flags; splitting into two tools would duplicate the target/password plumbing |
| Estimate output | Always `--json`, parsed to `disk_space{hard_disks,snapshots,miscellaneous,reclaimable}` (int64 bytes) | Machine-readable; agents can decide whether a real run is worth it |
| `--json` on real runs | Never sent | Verified: it has no effect on real-run output; only estimate honours it |
| Password | Optional; stdin via `RunStdin` when present; `ValidatePassword` when present; absent → plain `Run` | Verified: unencrypted VMs ignore stdin; encrypted VMs need exactly one line; same secret hygiene as spec 011 |
| Timeouts | Real run: `longDiskOpTimeout` (4 h backstop). Estimate: runner default (30 s) | Reclaim scales with disk size; the request context has no deadline (probe-verified); estimate is a fast metadata read |
| Cache | Invalidate after a **real** run only (an `allow_shutdown` run may flip VM state; the disk file changed). Estimate never invalidates | Same principle as spec 011's dry-run handling |
| Annotations | `ReadOnlyHint: false` (real runs mutate the disk file), `DestructiveHint: false`, `IdempotentHint: true`, `OpenWorldHint: true` | Reclaim is a no-op when already optimized and loses no data; an `allow_shutdown` run may stop a running VM gracefully — that is documented in the description, not destructive in the MCP sense |
| Mode contract | `powerInputSchema` + `resolvePowerTarget`, registered in both modes | Consistency with every other VM-scoped tool |
| Sequence step | Registered in `tools.RegisterSteps` (VM-scoped, may carry a password arg — caller's responsibility) | Repo rule; drift guard enforces it |

## 3. Repository layout (delta)

| File | Change |
|---|---|
| `internal/parallels/interfaces/interfaces.go` | + `ReclaimDiskSpace`, `ReclaimDiskResult` types; + `ParallelsService.ReclaimDisk(ctx, vmRef, password string, estimate, allowShutdown bool) (ReclaimDiskResult, error)` |
| `internal/parallels/service/reclaim.go` | New: `ReclaimDisk` — enabled check, `ValidateVMRef`, argv build, password via `RunStdin` (or `Run`), estimate JSON parse, long-op timeout for real runs, cache invalidation on real runs |
| `internal/tools/parallels_vm_reclaim_disk.go` | New: `VMReclaimDiskInput` / `VMReclaimDiskOutput` (+ `VMReclaimDiskSpace`), `VMReclaimDisk` handler, `RegisterParallelsVMReclaimDisk` |
| `internal/tools/descriptions.go` | + `descVMReclaimDisk` |
| `internal/tools/steps.go` | + reclaim action in `RegisterSteps` |
| `internal/server/server.go` | + registration in both modes |
| `internal/tools/parallels_reclaim_test.go` | New: service-free tool/protocol tests |
| `internal/parallels/service/reclaim_test.go` | New: fake-runner argv/stdin/parse/validation tests |
| `internal/tools/parallels_sequence_test.go` | Drift-guard count 17 → 18 |
| `test/e2e/stdio_test.go` | Listing checks both modes + default-on estimate exec (unencrypted VM only, never a password) |
| `docs/qa/012-parallels-vm-reclaim-disk-QA.md`, `docs/specs/README.md`, `README.md` | Docs |

## 4. Service layer

```
ReclaimDisk(ctx, vmRef, password string, estimate, allowShutdown bool) (ReclaimDiskResult, error)
  enabled? → ValidateVMRef
  args = [reclaim-disk, ref]
  if estimate: args += [--estimate, --json]
  if allowShutdown && !estimate: args += [--allow-shutdown]
  if password != "": ValidatePassword → RunStdin(pw+"\n", args)
  else if estimate: Run(args)                       // fast, default cap
  else: RunCapture(args) with longDiskOpTimeout     // scales with disk size
  exit != 0 → error with prlctl's stderr (password never in it)
  if estimate: parse JSON → ReclaimDiskSpace (bytes); Output = trimmed stdout
  if !estimate: Output = trimmed stdout; cache.invalidate()
```

## 5. Tool layer

- Input: `vm_id` (mode contract), `estimate` bool, `allow_shutdown` bool,
  `password` string (omitempty; the handler does not require it — the
  encrypted-VM requirement is enforced by prlctl's authorise error, which
  is self-describing).
- Output: `{estimate: bool, disk_space: {hard_disks, snapshots,
  miscellaneous, reclaimable} (bytes, estimate only, omitempty),
  output: string (prlctl's trimmed stdout)}`.
- Annotations: `ReadOnlyHint: false, DestructiveHint: false,
  IdempotentHint: true, OpenWorldHint: true`.

## 6. Security

- Password: same rules as spec 011 — stdin only, `ValidatePassword`,
  never in argv/logs/server errors; prlctl's stderr pass-through is
  password-free (verified).
- `allow_shutdown` may stop a running VM (gracefully) as a side effect of
  the reclaim — the tool description says so; the agent must pass it
  deliberately.
- Real runs are never executed by automated tests (state mutation on a
  real VM's disk file); e2e runs the estimate only.

## 7. Testing

- Service (fake runner): exact argv per flag combination (4 cases),
  stdin payload `pw\n` when password set / no stdin when absent, estimate
  JSON parsed to the byte fields, real-run stdout trimmed, non-zero exit
  surfaces prlctl's stderr without the password, validation rejects
  invalid passwords before any run, cache invalidates on real run only,
  bogus-VM error pass-through.
- Tools (in-memory): fleet required = exactly `[vm_id]`; VM mode rejects
  explicit `vm_id`; estimate output structure; real-run output pass-through;
  `allow_shutdown` in argv; nil-request safety via the sequence registry.
- e2e: listed in both modes; default-on estimate against the first
  **unencrypted** VM (any power state) asserting the `disk_space`
  structure; skipped with a log if every VM is encrypted.
- Drift guard: registry count 18.

## 8. QA (manual, real machine)

See [QA 012](../qa/012-parallels-vm-reclaim-disk-QA.md): estimate on a
stopped and a running VM; real reclaim on the disposable VM (before/after
estimate diff where meaningful); `allow_shutdown` on a running VM;
encrypted-VM estimate with password; real reclaim on an encrypted VM
(expected prlctl error); password-never-logged audit of the server log.

### Deviations (fill in during implementation)

None — the spec was written from a complete live probe (protocol table
in §1) and the implementation followed it. Two live observations worth
keeping next to the (stale) CLI doc text:

1. **Real reclaim works on running VMs in 27.0.1** without
   `--allow-shutdown` (the doc's "must be shut down first" does not
   hold on this version), and `--allow-shutdown` did not shut the VM
   down when the reclaim succeeded while it ran. The flag is passed
   through verbatim; the description sets the expectation that it is
   rarely needed.
2. **A real run on an encrypted VM fails** with prlctl's
   "compressing the hard disk" error even with the correct password —
   only the estimate is supported there. The tool surfaces the error
   as a clean tool result (verified in QA).
