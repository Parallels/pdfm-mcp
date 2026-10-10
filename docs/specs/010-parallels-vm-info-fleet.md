# Spec 010: `parallels_vm_info` in Fleet Mode (Specific-VM Info by ID)

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers |
| Scope | Extend `parallels_vm_info` from VM-only to both modes: fleet mode fetches a specific VM by id or name with `prlctl list <id> -a -i --json` (the list command with a target). VM mode behaviour is unchanged (cached target VM). |
| Depends on | 002 (module, cache, VM mode info) |
| Out of scope | Cache integration for the fleet path, multi-VM info in one call, new output fields |

## 1. Goal

In fleet mode (no `--vm-id`), the only way to see one VM's full prlctl JSON
was to call `parallels_list_vms` and scan 31 VMs. Add the missing shape:
ask for **one specific VM by id or name**, implemented with the same command
the list fetch uses, but passing the target — `prlctl list <id> -a -i
--json`.

### Acceptance criteria

1. `make check` and `make test-e2e` pass; e2e exercises the fleet call
   read-only (one real VM by id + one not-found error) — safe default-on.
2. `parallels_vm_info` is listed in **both** modes whenever the module is
   enabled; absent otherwise. Mode contract (same as specs 003–007): fleet
   **requires** `vm_id`; VM mode uses the `--vm-id` target and **rejects**
   an explicit `vm_id`.
3. Fleet mode runs `prlctl list <vm_id> -a -i --json` on every call (no
   cache read or write), returns the exact prlctl JSON blob of the first
   match (`source: "live"`), and errors cleanly when nothing matches.
4. VM mode is unchanged: no `vm_id` argument, served from the in-memory
   cache, `source: "cache"`/`"refreshed"`.
5. `vm_id` is validated with the existing `ValidateVMRef` (printable,
   bounded, single argv element — never shell-interpolated).

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| New tool vs extension | **Extend `parallels_vm_info`** with the standard mode contract (vm_id required in fleet, rejected in VM mode) | One tool per capability, same shape as every power tool via `powerInputSchema` + `resolvePowerTarget`; a second tool would be a duplicate |
| Fleet data source | **Live fetch, never the cache** (`prlctl list <ref> -a -i --json`) | The fleet cache holds the all-VM list; a single-VM result would only ever update one entry. "Get me this VM's info" means fresh data. Documented on the tool |
| VM mode data source | Unchanged: the cache (whose fetch is already `prlctl list <target> -a -i --json`) | Spec 002 semantics untouched |
| Multiple matches | Warn in the log, return the first (same rule as the VM-mode fetch) | prlctl name matching can be loose; deterministic enough for a read |
| `refresh` argument | Kept (VM mode only); fleet mode ignores it (always live) | No breaking change to the VM-mode API; the schema description says so |
| Sequence step | Still excluded from `tools.RegisterSteps` (spec 007's exclusion of `parallels_vm_info` stands) | The drift-guard exclusion list is unchanged; a direct call is cheaper for a read |
| Timeout | Inherited from the request context, like `parallels_list_vms` | Consistent with the other read paths |

## 3. Repository layout (delta)

```
internal/
├── parallels/
│   ├── interfaces/interfaces.go    # + VMInfoRef(ctx, ref) (VMRawResult, error)
│   └── service/service.go          # + VMInfoRef; parseListOutput helper shared with fetch
├── tools/
│   ├── parallels_vm_info.go        # mode-aware input schema + handler + description
│   └── parallels_test.go           # fleet live fetch, name resolution, not-found, invalid ref, VM-mode rejection
├── server/server.go                # register in both modes (was VM-only)
├── test/e2e/stdio_test.go          # fleet listing assertion flipped + live read-only call
└── docs/
    ├── specs/010-parallels-vm-info-fleet.md
    └── qa/010-parallels-vm-info-fleet-QA.md
```

## 4. Service layer

`VMInfoRef(ctx, ref)`:

1. `enabled` check, then `ValidateVMRef(ref)`.
2. Run `prlctl list <ref> -a -i --json` (single argv elements; `ref` is
   pre-validated printable text).
3. Parse with the shared `parseListOutput` helper (extracted from
   `fetch`): zero blobs → `VM %q not found (prlctl returned no results)`;
   more than one → warn + first.
4. Return `VMRawResult{Blob, FetchedAt: now, Refreshed: true}`.

The cache is never read or written; `resolvedID` bookkeeping is untouched.

## 5. Tool layer

- `VMInfoInput{VMID, Refresh}` — `powerInputSchema[VMInfoInput](mode)`:
  fleet `required: ["vm_id"]`, VM mode `required: []`.
- Handler: `resolvePowerTarget(svc, in.VMID)` → fleet: `VMInfoRef(ctx,
  target)`, `source: "live"`; VM mode: `VMRaw(ctx, in.Refresh)`,
  `source: "cache"|"refreshed"`.
- Registration: `RegisterParallelsVMInfo(s, mode)` with a mode-aware
  description via `powerDescription`; `ReadOnlyHint: true`.
- `server.New`: registered in the module-enabled block for **both** modes.

## 6. Security

- Read-only tool (`prlctl list`), no state change, no cache invalidation.
- `ref` is validated (`ValidateVMRef`) and passed as one argv element; no
  shell, no path construction.
- Output is the VM's full prlctl JSON (same exposure class as
  `parallels_list_vms`); the stdout rule is unchanged.

## 7. Testing

- Service: covered through the tools tests (fake runner) — exact argv
  `list <id> -a -i --json`, id and name resolution, not-found, invalid ref
  rejected before any run.
- Tools/protocol: fleet listing with `required: ["vm_id"]` exactly;
  VM-mode explicit `vm_id` rejected; VM-mode no-argument path unchanged
  (cached blob, raw fields preserved).
- E2e: fleet mode asserts the tool is listed and executes two read-only
  calls (existing VM by id → `source: "live"`, wrong VM id → tool error);
  VM-mode block unchanged.

## 8. QA (manual, real machine)

`docs/qa/010-parallels-vm-info-fleet-QA.md`: automated suites, opt-in-free
e2e fleet info call, wire-level check of fleet info by id and by name,
not-found, and VM-mode regression.

### Deviations (fill in during implementation)

- Not-found for an unknown `vm_id` surfaces prlctl's own non-zero-exit
  message (`Failed to get VM config: The virtual machine ...`), not the
  empty-output branch (`not found (prlctl returned no results)` — kept as
  a backstop). Both are clean tool errors; the QA doc records the
  observed message.
