# Spec 008: Parallels VM Deletion

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers |
| Scope | `parallels_vm_delete` tool; `Delete` on the Parallels service; tests; QA doc |
| Out of scope | `prlctl create` (VM creation), snapshots/archiving, batch deletion — follow-up specs if ever requested |

## 1. Goal

Delete a VM with `prlctl delete <vm_id | vm_name>`, which **permanently**
removes the VM and all its files from the host. Irreversible.

Because the CLI itself is unconditional (no force flag, no prompt option
in Parallels 27.0.1), the safety gate lives in this tool: a required
`confirm` argument that must repeat the exact VM id or name. This is the
only tool in the Parallels module that is destructive **and** irreversible,
so it gets a stricter contract than the power tools.

### Acceptance criteria

1. `make check` and `make test-e2e` pass; the tool is **listed but never
   executed** by any automated test — there is deliberately no opt-in
   env var, because any real run deletes a real VM.
2. Listed in fleet and VM mode; mode contract identical to the other
   VM-scoped tools. Fleet required list: `vm_id` **and** `confirm`; VM
   mode: `confirm` only.
3. A call resolves the target via `resolvePowerTarget`, requires
   `confirm` (trimmed) to equal the resolved target, and only then runs
   `prlctl delete <ref>` (single argv element, never a shell).
4. prlctl failure (non-zero exit, launch error, timeout) → tool error
   carrying prlctl's stderr. Success → trimmed stdout in the structured
   result and the VM list cache is invalidated.
5. 300 s timeout (deleting large VM images is file-IO bound).
6. Every step in `docs/qa/008-parallels-vm-delete-QA.md` passes.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Safety gate | Required `confirm` string that must equal the resolved VM ref exactly (after trim); mismatch → tool error **before any exec** | The CLI has no interactive prompt to lean on; echoing the target binds the confirmation to this specific VM and forces the agent/client to restate it — a natural confirmation moment. No magic phrases: matching the ref is simpler and can't drift between VMs |
| Gate location | Tool layer (the service stays a faithful prlctl wrapper, like every other op) | `confirm` is an MCP-contract concern, not prlctl's |
| Failure semantics | Non-zero exit = **tool error** with stderr (there is no guest exit-code concept; the "command" is prlctl's own) | Same as power / send-key |
| Timeout | 300 s (`deleteTimeout`) | Deleting large .hdd/.sparsebundle files can exceed the 180 s power cap |
| Cache | Invalidate after **success** (the VM list changed) | Same as power operations |
| VM-mode target deleted | No special handling: subsequent `list_vms`/`vm_info` calls surface prlctl's not-found error; the server keeps running | Documented, not a fault state |
| Annotations | `ReadOnlyHint: false`, `DestructiveHint: true`, `IdempotentHint: false`, `OpenWorldHint: false` | It is the irreversible op |
| e2e | **Listed, never executed** — no opt-in env var | Unlike capture/exec/key (harmless probes), a real delete is unrecoverable data loss |
| Logging | Log VM ref + took. The ref is not sensitive; there is no command content | Consistent with power |
| No pre-state checks | Do not pre-check "is it stopped?"; prlctl is the authority (deleting a running VM fails with its own error, which we surface) | Same rule as power: no state guessing |

## 3. Repository layout (delta)

```
pdfm-mcp/
├── internal/
│   ├── parallels/
│   │   ├── interfaces/interfaces.go   # + Delete on ParallelsService
│   │   └── service/
│   │       ├── vm_delete.go           # NEW — Delete + deleteTimeout
│   │       └── vm_delete_test.go      # NEW
│   ├── tools/
│   │   ├── parallels_vm_delete.go     # NEW — input/output + handler + Register
│   │   └── parallels_delete_test.go   # NEW — unit + protocol tests
│   └── server/server.go               # + registration
├── test/e2e/stdio_test.go             # + listed assertions (fleet + VM mode)
└── docs/
    ├── specs/008-parallels-vm-delete.md
    └── qa/008-parallels-vm-delete-QA.md
```

No new dependencies.

## 4. Service layer

```go
// added to ParallelsService:
//
// Delete permanently removes the VM identified by vmRef (a VM id or
// name, pre-validated) and all its files from the host via
// `prlctl delete <vm>`. The operation is irreversible. On success the
// VM list cache is invalidated. Any prlctl failure is an error
// carrying its stderr.
Delete(ctx context.Context, vmRef string) (string, error)
```

`(*service).Delete`:

1. `enabled` check → `ErrNotEnabled`.
2. `ValidateVMRef(vmRef)`.
3. argv `delete <ref>` (two elements, fixed first).
4. `context.WithTimeout(ctx, deleteTimeout)` → `s.runner.Run(...)`.
5. `err != nil` → log `vm` + `err` and return it.
6. `s.cache.invalidate()`; return trimmed stdout.

## 5. Tool layer

```go
type DeleteInput struct {
	VMID    string `json:"vm_id,omitempty" jsonschema:"VM id or name to delete (the VM and all its files are removed permanently)"`
	Confirm string `json:"confirm" jsonschema:"safety gate: must be exactly the same VM id or name as vm_id (or the configured target in VM mode)"`
}

type DeleteOutput struct {
	VMID   string `json:"vm_id" jsonschema:"the VM id or name that was deleted"`
	Output string `json:"output" jsonschema:"prlctl stdout (usually empty)"`
}
```

Handler: `resolvePowerTarget` → `strings.TrimLeft/Right(in.Confirm)`
(plain trim) must equal the resolved target, else error
`confirm must repeat the VM id or name exactly (got %q, want %q)` →
`svc.Delete`.

`RegisterParallelsVMDelete`: description
"Permanently deletes a VM and all its files from the host —
irreversible. Requires confirm to repeat the VM id or name exactly.
Use parallels_vm_stop first if the VM is running (prlctl rejects
deleting a running VM)." — mode-aware via `powerDescription`.

## 6. Security

- `confirm` is validated by equality only; it is never executed or
  interpolated anywhere.
- The VM ref goes through `ValidateVMRef`; argv is two fixed-size
  elements; no shell.
- The irreversible gate is a contract, not a backstop: the tool
  description and README say so explicitly, and DestructiveHint is set.
- No logs beyond the ref and timing.

## 7. Edge cases and invariants

| Case | Behaviour |
|---|---|
| `confirm` missing (fleet or VM mode) | Schema error (required) |
| `confirm` ≠ target (after trim) | Tool error before any exec; prlctl never runs |
| `confirm` = target | Exec runs |
| VM running | prlctl rejects it; tool error with its stderr |
| VM unknown | prlctl not-found error; tool error |
| VM already deleted / prlctl non-zero | Tool error with stderr |
| Timeout at 300 s | Tool error "(command timed out)" |
| Client cancels | Context propagates |
| Success | `output` returned; cache invalidated; next `list_vms` re-runs prlctl |
| VM-mode: the configured target itself | Allowed (that is what VM mode means); `confirm` must equal the configured ref |

## 8. Testing

1. **`service/vm_delete_test.go`**: exact argv `delete <vm>`; success
   returns trimmed stdout; runner error surfaces; disabled →
   `ErrNotEnabled`; invalid ref → no exec; 300 s deadline applied
   (290 s < remaining ≤ 300 s+1 ms); **cache invalidated** (list call
   count goes 1 → 2 after a successful delete, vs. the untouched case).
2. **`tools/parallels_delete_test.go`**: mode table (four cases: VM mode
   configured target with matching confirm → argv `delete <vmOneID>`; VM
   mode explicit vm_id rejected; fleet without vm_id rejected; fleet
   with vm_id ok); confirm gate table (mismatch, leading/trailing
   spaces in confirm accepted via trim, empty confirm, missing confirm
   → 0 runner calls, error text contains both got and want); protocol
   test: listed, fleet required list `["confirm", "vm_id"]` (order per
   powerInputSchema append), call with matching confirm → non-error,
   structured `vm_id` correct; VM-mode protocol: required `["confirm"]`,
   call `{"confirm": vmOneID}` → argv `delete <vmOneID>`, explicit
   vm_id rejected.
3. **e2e**: listed in both modes; **no execution, no opt-in** (the
   assertion is the listing only).

## 9. QA (manual, real machine)

`docs/qa/008-parallels-vm-delete-QA.md`:

- Automated: `make check`, `make test-e2e` (listed in both modes).
- Safe live checks (no data loss): confirm-gate mismatch on a real VM
  name (prlctl must NOT run — verify via log absence / no change),
  unknown VM error, deleting a **running** VM (prlctl error, VM
  untouched).
- Real deletion (user-performed, explicit): user picks a **disposable**
  VM they are willing to lose (or creates one), runs the tool with a
  matching confirm, verifies: exit success, `list_vms` no longer shows
  it (cache re-fetched), host directory gone.

## 10. Implementation order

1. This spec + index row.
2. `interfaces.Delete` + `service/vm_delete.go` + tests.
3. `tools/parallels_vm_delete.go` + tests.
4. `server.New` registration.
5. e2e listing assertions.
6. QA doc + README (tool row + strong safety bullet).
7. `make check`, `make test-e2e`, live QA (safe checks only, plus real
   deletion only with a user-approved disposable VM).

## 11. Known unknowns / verification notes

- **Resolved (observed on the real machine, Parallels 27.0.1):** a
  delete attempt with a matching confirm against a **running** VM is
  refused by prlctl itself — the VM stayed running, its 37 GB directory
  and config untouched, and the call returned promptly (no non-TTY
  hang). prlctl is therefore a backstop even if the confirm gate were
  bypassed. The exact refusal error text should be captured on a
  disposable VM during QA.
- Non-TTY prompt behaviour on a successful (stopped, disposable)
  deletion — still open until the user-performed QA step; expect
  immediate exit 0 (no prompt flags exist in the CLI).

### Deviations (fill in during implementation)

- None — implemented exactly as specified. (One QA-script slip is
  recorded in the QA doc: a live walkthrough step issued a
  matching-confirm call against the running VM and relied on prlctl's
  running-VM refusal; the VM was verified intact afterwards.)
