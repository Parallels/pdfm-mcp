# QA 008 — Parallels VM Deletion

**Scope:** `parallels_vm_delete`, implemented per
[spec 008](../specs/008-parallels-vm-delete.md).

**⚠️ This is the only irreversible operation in the module.** A
successful call permanently removes a VM and all its files from the host.
The automated checks below delete **nothing**. The one real-deletion step
is user-performed and requires a VM you are willing to lose.

## Prerequisites

- Parallels Desktop installed, `prlctl` on PATH or in a fallback dir.
- `make build` (produces `bin/pdfm-mcp`) and `make check` passing.

## Steps

### 1. Automated suites (delete nothing)

```
make check
make test-e2e
```

Expect: lint + vet clean, all unit/protocol tests green, e2e green with
`parallels_vm_delete` listed in fleet and VM mode. The e2e suite
deliberately has **no** opt-in env for this tool — any real run would
delete a real VM.

### 2. Safe live checks (no data loss possible)

Connect with `bin/pdfm-mcp` (fleet) and run:

1. `tools/list` → `parallels_vm_delete` present; fleet `required`
   contains `confirm` **and** `vm_id`; annotations show
   `destructiveHint: true`.
2. Confirm gate: `{"vm_id": "<any-real-vm>", "confirm": "wrong-vm"}` →
   tool error `confirm must repeat the VM id or name exactly (got …,
   want …)`. prlctl never runs — the named VM must still be present and
   unchanged afterwards (check with `parallels_list_vms`).
3. Missing confirm: `{"vm_id": "<any-real-vm>"}` → schema error naming
   `confirm`.
4. Unknown VM (safe: nothing to delete):
   `{"vm_id": "no-such-vm-xyz", "confirm": "no-such-vm-xyz"}` → tool
   error carrying prlctl's "The virtual machine could not be found"
   text.
5. VM-mode connection (`--vm-id <vm>`): `required` is `["confirm"]` only;
   `{"confirm": "<configured-vm>"}` **do not run this** — it would
   delete the configured VM. Instead verify the contract only: an
   explicit `vm_id` argument is rejected, and the matching-confirm call
   is covered by the protocol test in the unit suite.

### 3. Real deletion (user-performed, explicit)

Only with a **disposable** VM you have created for this test (or one you
are fully willing to lose):

1. Note the VM's id and host directory (`prlctl list <vm> -a -i --json` →
   "Home path").
2. If running, stop it first: `parallels_vm_stop`.
3. Call `parallels_vm_delete` with `vm_id` and `confirm` both set to the
   same id/name.
4. Verify:
   - result `vm_id` correct, no error;
   - `parallels_list_vms` (next call) no longer shows the VM (cache was
     invalidated, so this is a fresh prlctl list);
   - the host directory is gone.
5. Observe and record: did prlctl delete immediately (exit 0) or does it
   ever prompt/hang in non-TTY mode? (Spec 008 §11 unknown.)

### 4. Optional: deleting a running VM

With a disposable running VM: call delete directly (no stop first) and
record prlctl's error text (expected: refusal). **Never do this against a
VM you care about.**

**Observed on the real machine (2026-10-09):** a live walkthrough step
accidentally issued a matching-confirm delete call against the running
infra VM `capsule_manager_backend`. prlctl refused (running VMs cannot
be deleted): the VM stayed running, its 37 GB directory and config
verified intact on disk. The call returned promptly — no non-TTY hang.
Lesson encoded in step 5: verify the VM-mode contract **without**
issuing the matching-confirm call.

## Checklist

- [ ] `make check` and `make test-e2e` green (delete listed, never run)
- [ ] fleet required list has `confirm` + `vm_id`; destructiveHint true
- [ ] confirm mismatch → tool error, VM untouched
- [ ] missing confirm → schema error
- [ ] unknown VM → prlctl not-found error
- [ ] VM mode: `confirm` only required, explicit vm_id rejected
- [ ] real deletion on a disposable VM: gone from list + host (user step)
- [ ] non-TTY prompt behaviour recorded (user step)

## Follow-ups

- VM creation (`prlctl create`) — would make delete testable end-to-end
  with disposable VMs in CI-adjacent manual runs.
