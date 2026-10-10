# QA 012 — Parallels VM Disk Space Reclaim (`reclaim-disk`)

**Scope:** the `parallels_vm_reclaim_disk` tool, implemented per
[spec 012](../specs/012-parallels-vm-reclaim-disk.md).

**Safety note:** `estimate` is read-only (prlctl checks disk metadata;
works on running and stopped VMs; an *encrypted* VM needs its current
password to authorise the check). A **real** run optimizes the VM's
disk file — it frees space without losing data, but it is a state
mutation that scales with disk size and (`allow_shutdown`) may stop a
running VM. Real runs are never executed by automated tests.

## Protocol facts (verified live, Parallels 27.0.1)

See the spec's protocol table. Highlights: `--json` only affects the
estimate output (a real run prints "Reclaiming disk, please wait…"
either way); real reclaim works on running VMs in this version;
`--allow-shutdown` did not shut down a running VM when the reclaim
succeeded while it ran; real reclaim on an encrypted VM fails with
"An error occurred while compressing the hard disk" while the estimate
works with the password.

## Steps

### 1. Automated suites

```
make check
make test-e2e
```

Expect green. The e2e gating test now asserts, read-only and
default-on: the tool listed in both modes; an `estimate` against the
first **unencrypted** VM (any power state) with a `disk_space`
structure (four byte fields) in the result. No password is ever
transmitted and no real reclaim ever runs.

### 2. Live checks (executed 2026-10-09, disposable
`empty_catalog_test` plus one running-VM observation)

Using `bin/pdfm-mcp` in fleet mode, MCP tools/call:

1. `parallels_vm_reclaim_disk {vm_id, estimate: true}` on a stopped
   VM → `disk_space` object with four byte fields; `prlctl list`
   confirms the VM untouched.
2. Same estimate while the VM is **running** → same shape (estimate is
   state-independent).
3. `parallels_vm_reclaim_disk {vm_id}` (real run, stopped VM) →
   "Reclaiming disk, please wait…"; VM still fine.
4. Real run on a **running** VM with `allow_shutdown: true` → the run
   completes; on 27.0.1 the VM stayed running (reclaim worked without
   a shutdown).
5. Encrypted VM: encrypt → estimate **with** `password` → works; real
   run with `password` → prlctl's "compressing the hard disk" error
   (unsupported); decrypt → VM restored.
6. Encrypted VM, estimate **without** password → clean tool error
   ("password you specified is invalid").
7. Server log audit (stderr): `has_password` is a **boolean** field —
   no password material anywhere.

## Results — 2026-10-09, live machine (Parallels 27.0.1)

All steps passed; the disposable VM was left exactly as found
(`stopped`, `Encrypted: no`).

- `make check`: green (fmt, golangci-lint 0 issues, vet, race tests).
- `make test-e2e`: green — tool listed in both modes; the default-on
  estimate returned a `disk_space` structure with four non-negative
  byte fields; no real reclaim ran.
- Wire run on `empty_catalog_test`:
  1. `estimate` stopped → `{"hard_disks":1204224,"snapshots":0,
     "miscellaneous":1081344,"reclaimable":0}`.
  2. Same estimate while **running** → same shape (state-independent).
  3. Real run with `allow_shutdown: true` on the **running** VM →
     "Reclaiming disk, please wait…"; the VM stayed running (27.0.1
     reclaims without a shutdown — the flag is a pass-through).
  4. Encrypted cycle: `estimate` **with** `password` → same
     `disk_space` shape; real run **with** `password` → clean tool
     error "An error occurred while compressing the hard disk …"
     (unsupported on encrypted VMs, as documented); `estimate`
     **without** password → clean tool error "password you specified
     is invalid".
  5. VM mode: explicit `vm_id` → "vm_id must be omitted".
- Server log audit (full session stderr): `has_password` logged as a
  boolean; **no password material** anywhere.
- Unit coverage: exact argv per flag combination (estimate / real /
  allow_shutdown / estimate+allow_shutdown), stdin payload `pw
` vs
  no stdin, estimate JSON parsed to byte fields, real-run stdout
  trimmed, non-zero exit surfaces prlctl's stderr without the password,
  invalid password rejected pre-run, cache invalidates on real runs
  only, fleet `required = ["vm_id"]`, VM mode target contract.
