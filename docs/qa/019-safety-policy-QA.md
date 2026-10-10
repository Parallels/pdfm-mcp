# QA 019 — Safety policy (read-only, allow/deny tools, allow-vms)

**Scope:** [spec 019](../specs/019-safety-policy.md). Never run destructive
tools against real VMs; use a disposable VM.

**Executed 2026-10-09** on the developer Mac (Parallels 27.0.1). VM A =
disposable `empty_catalog_test` (started/stopped via the allow-listed server
during step 4, stopped again after the run); VM B =
`capsule_manager_backend` used for **denial checks only** (every call against
B was rejected before any prlctl work).

## Steps

### 1. Automated suites — ✅ PASSED

`make check` and `make test-e2e` green. Fail-closed classification guard
demonstrated live: a temporary `zz_unclassified_probe` entry was added to the
registration table, `TestEveryRegisteredToolIsClassified` **failed** with
`tool "zz_unclassified_probe" is registered but missing from the
classification table in internal/policy/policy.go: add it with the right
class (read/input/power/exec/destructive/secret).`, and the entry was
reverted (tests green again).

### 2. Read-only (fleet mode) — ✅ PASSED

1. `PDFM_MCP_READ_ONLY=true` → `tools/list` = exactly
   `parallels_list_vms`, `parallels_vm_info`, `parallels_vm_capture`,
   `parallels_vm_exec_result`, `parallels_vm_snapshot_list`,
   `parallels_vm_sequence`. (`parallels_vm_observe` is absent on this host
   because the OCR helper is not built — spec 020 — not a policy effect.)
2. The sequence description lists only read steps (contains
   `parallels_vm_capture`; does not contain `parallels_vm_start`,
   `parallels_vm_exec`, `parallels_vm_send_key`, `parallels_vm_type`). A
   sequence with a `parallels_vm_start` step was rejected:
   `invalid sequence: step 0 (s1): unknown tool "parallels_vm_start"`; the
   VM's state was unchanged.
3. Stderr shows 44 `tool hidden by policy` lines (plus orchestrator
   `sequence step hidden by policy` lines for the filtered registry), each
   with `tool=<name> reason="…"` — names only.

### 3. Allow / deny — ✅ PASSED

1. `--allow-tools parallels_list_vms,parallels_vm_info` → exactly those two
   tools.
2. `--deny-tools 'parallels_vm_exec*'` → all four exec tools hidden; the
   other 24 tools (incl. `parallels_vm_sequence`) remain.
3. `--allow-tools parallels_vm_stop --deny-tools parallels_vm_stop` → zero
   tools exposed (deny wins; also hides the sequence, which then has no
   allowed steps… the sequence is itself excluded by the allow list).
4. `--allow-tools Bad` and `--deny-tools 'a*b'` → **exit code 2**:
   `config error: invalid --allow-tools: invalid entry "Bad": only lowercase
   letters, digits and _ are allowed, with an optional trailing *`.
5. `--deny-tools parallels_vm_dellete` → WARN
   `tool policy entries match no known tool entries=[parallels_vm_dellete]`;
   the server still starts with 28 tools.

### 4. VM allow-list — ✅ PASSED

1. Fleet, `--allow-vms empty_catalog_test`: `parallels_list_vms` →
   `count: 1`, only A's UUID. `parallels_vm_info` for A **by name and by
   UUID** both succeed (live fetch). `parallels_vm_start A` (by name) and
   `parallels_vm_stop A` (by UUID) succeed on the disposable VM
   (`prlctl status` confirmed the state transitions). The same calls for B:
   `this VM is not permitted: the server restricts VM access with
   --allow-vms (PDFM_MCP_ALLOW_VMS)` — the message names no other VM and
   leaks no id/name.
2. Unknown ref `no_such_vm_xyz` → denied with the same `--allow-vms` message
   (fail closed).
3. A sequence with `vm_id: B` and a read step → denied with the same
   message before any step runs.
4. VM mode: `PDFM_MCP_VM_ID=<B> PDFM_MCP_ALLOW_VMS=<A>` → **exit 2**
   (`config error: --vm-id is not in --allow-vms`);
   `PDFM_MCP_VM_ID=<B> PDFM_MCP_ALLOW_VMS=<B>,<A>` → server starts
   (`VM allow-list active vms=2`, `server run start`).

### 5. Placeholder guard — ✅ PASSED

`PDFM_MCP_VM_ID='${user_config.vm_id}' PDFM_MCP_ALLOW_VMS='${user_config.allow_vms}'`:
server starts in **fleet mode** (`vm_id=""`, `list_vms` count 31 = all VMs,
no restriction), with two stderr warnings, one per variable, naming the
variable only:

```
level=WARN msg="ignoring unresolved placeholder in environment variable; treating it as unset" var=PDFM_MCP_VM_ID
level=WARN msg="ignoring unresolved placeholder in environment variable; treating it as unset" var=PDFM_MCP_ALLOW_VMS
```

The value `user_config.vm_id` / `user_config.allow_vms` appears nowhere in
the log.

### 6. Items verified on a real Mac

- **id ↔ name resolution via the VM cache:** ✅ — `info` by name and by UUID
  both resolved against live `prlctl` data; `list_vms` filtering matched the
  UUID entry.
- **Claude Desktop shows only the filtered tools after a restart:** ⏸
  manual — needs the Claude Desktop install (pending item from QA 015);
  the server-side filtering is what Claude Desktop would receive
  (`tools/list` verified above).

## Result

All policy semantics verified live: read-only set, filtered sequence
description and rejection, allow/deny/deny-wins, exit-2 on invalid entries
with exact config errors, typo warning, VM allow-list (name/UUID
resolution, filtered list, no-leak denial message, sequence coverage,
VM-mode config check), and the placeholder guard (treated-as-unset +
names-only warnings). The fail-closed classification guard was demonstrated
by breaking it on purpose and reverting. Only the Claude Desktop display
check remains manual.
