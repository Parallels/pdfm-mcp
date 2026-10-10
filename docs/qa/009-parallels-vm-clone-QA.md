# QA 009 — Parallels VM Clone

**Scope:** `parallels_vm_clone`, implemented per
[spec 009](../specs/009-parallels-vm-clone.md).

**Safety note:** cloning never modifies the source VM, but a clone is a
**permanent host artifact** — full clones copy all of the source's files
(they need that much free disk space), and cleanup requires a separate
`parallels_vm_delete` (with its confirm gate). The automated checks
below create nothing; the real-clone step is user-performed.

## Prerequisites

- Parallels Desktop installed, `prlctl` on PATH or in a fallback dir.
- `make build` (produces `bin/pdfm-mcp`) and `make check` passing.

## Steps

### 1. Automated suites (create nothing)

```
make check
make test-e2e
```

Expect: lint + vet clean, all unit/protocol tests green, e2e green with
`parallels_vm_clone` listed in fleet and VM mode. There is deliberately
no opt-in env: a real clone creates a VM that would need a delete to
clean up.

### 2. Safe live checks (nothing created)

Connect with `bin/pdfm-mcp` (fleet) and run:

1. `tools/list` → `parallels_vm_clone` present; fleet `required` is
   exactly `["name", "vm_id"]` (order aside); annotations show
   `destructiveHint: false` (the source is never modified).
2. Missing name: `{"vm_id": "<any-vm>"}` → schema error naming `name`.
3. Bad enum: `{"vm_id": "<any-vm>", "name": "c", "detach_external_hdd": "maybe"}`
   → tool error `detach_external_hdd must be "yes" or "no"`; prlctl
   never runs.
4. Combo rule: `{"vm_id": "<any-vm>", "name": "c", "snapshot_id": "snap-x"}`
   (without `linked`) → tool error `snapshot_id requires linked: true`.
5. Unknown source (safe — nothing to clone):
   `{"vm_id": "no-such-vm-xyz", "name": "c"}` → tool error carrying
   prlctl's "could not be found" text. No clone appears in
   `parallels_list_vms`.

### 3. Real clone (user-performed, needs a disposable source VM)

With a **disposable** source VM (or one the user approves cloning) and
free disk:

1. **Full clone:** `{"vm_id": "<src>", "name": "qa-clone-<ts>"}` →
   success; `parallels_list_vms` (next call) shows the new VM in stopped
   state; the source VM's state is unchanged; clone files exist on the
   host (default location: `~/Parallels/<name>.pvm`).
2. **Linked clone:** `{"vm_id": "<src>", "name": "qa-linked-<ts>", "linked": true}`
   → success; `prlctl list -i` shows the clone under "Linked Clones" of
   the source.
3. **dst:** `{"vm_id": "<src>", "name": "qa-dst-<ts>", "dst": "<empty-dir>"}`
   → clone files in the given directory.
4. Optional extras: `template`, `regenerate_src_uuid`,
   `detach_external_hdd` (needs a VM with external HDDs to observe).
5. Observe and record: does prlctl allow cloning a **running** source?
   (Spec 009 §11 unknown.) Exact error text for a name collision if
   reproduced.
6. Cleanup: `parallels_vm_delete` each `qa-*` clone (confirm gate
   included).

## Checklist

- [ ] `make check` and `make test-e2e` green (clone listed, never run)
- [ ] fleet required exactly `name` + `vm_id`; destructiveHint false
- [ ] missing name → schema error
- [ ] `detach_external_hdd: "maybe"` → validation error, no exec
- [ ] `snapshot_id` without `linked` → validation error, no exec
- [ ] unknown source → prlctl not-found error, nothing created
- [ ] VM mode: required exactly `["name"]`, explicit vm_id rejected
- [ ] real full + linked clone on a disposable VM (user step)
- [ ] running-source behaviour recorded (user step)

## Follow-ups

- `--unlink` and `--force-deep-copy` (prlctl 27.x flags not in the
  reference doc this spec follows).
- `prlctl create` — would let future QA create and destroy disposable
  VMs without touching real ones.
