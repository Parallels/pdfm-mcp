# QA 010 — `parallels_vm_info` in Fleet Mode

**Scope:** `parallels_vm_info` extended to fleet mode, implemented per
[spec 010](../specs/010-parallels-vm-info-fleet.md).

**Safety note:** read-only (`prlctl list <vm_id> -a -i --json`); never
mutates anything.

## Prerequisites

- Parallels Desktop installed, `prlctl` on PATH or in a fallback dir.
- At least one VM on the machine (any state — info works on stopped VMs).
- `make build` (produces `bin/pdfm-mcp`) and `make check` passing.

## Steps

### 1. Automated suites

```
make check
make test-e2e
```

Expect: lint + vet clean, all unit/protocol tests green. The e2e run now
asserts `parallels_vm_info` is listed in fleet mode and executes two
read-only fleet calls (an existing VM by id → `source: "live"` with the
matching `vm.ID`; a bogus id → clean tool error). No opt-in env needed.

### 2. Wire-level walkthrough (MCP Inspector or stdio)

Connect with `bin/pdfm-mcp` (fleet mode) and check:

1. `tools/list`: `parallels_vm_info` present, `inputSchema.required` is
   exactly `["vm_id"]`, `readOnlyHint: true`.
2. Info by **id**:
   `{"name":"parallels_vm_info","arguments":{"vm_id":"<id>"}}`
   → `vm` is the full prlctl JSON object for that VM (`ID` matches),
   `source: "live"`, `fetched_at` is current. Watch the server stderr:
   one `prlctl list <id> -a -i --json` per call (no cache involved).
3. Info by **name**: same call with the VM's name → same object,
   `vm.ID` filled in (prlctl resolves the name).
4. **Not found**: `{"vm_id":"no-such-vm-xyz"}` → tool error. prlctl
   itself exits non-zero for an unknown VM, so the message is prlctl's
   (`Failed to get VM config: The virtual machine ... not found`); the
   code path for empty output reports `not found (prlctl returned no
   results)`. Either way: clean tool error, not a crash.
5. **Invalid ref**: a name with a non-printable character (or > 128 chars)
   → tool error `invalid VM id or name` before prlctl is invoked.
6. **Freshness**: change something visible in the config (e.g. start the
   VM) and call twice in a row — both calls reflect the current state
   (the fleet path never serves stale cache data).

VM mode regression (`bin/pdfm-mcp --vm-id <vm>`):

7. `tools/list`: `parallels_vm_info` present with **no** required args.
8. `{"name":"parallels_vm_info","arguments":{}}` → the target VM's cached
   object, `source: "cache"` (or `"refreshed"` right after a TTL expiry).
9. `{"vm_id":"<other>"}` → tool error `vm_id must be omitted`.

## Results — 2026-10-09, live machine (Parallels 27.0.1, 31 VMs, target `Maui_DevBox` stopped)

- `make check` and `make test-e2e` green (e2e executed the fleet info
  assertions read-only: real VM by id → `source: "live"`, bogus id →
  tool error).
- Fleet: listed, `required: ["vm_id"]`, `readOnlyHint: true`.
- By id: `source: "live"`, `vm.ID` matched the requested id.
- By name (`Maui_DevBox`): resolved to the same `vm.ID`, `source: "live"`.
- Not found: tool error — prlctl's own non-zero-exit message
  (`Failed to get VM config: The virtual machine ...`), no crash.
- Invalid ref (embedded NUL): `invalid VM id or name: contains
  non-printable characters`, prlctl never invoked.
- VM mode (`--vm-id Maui_DevBox`): listed with no required args;
  no-argument call returned the cached object (`source: "cache"`, correct
  `vm.ID`); explicit `vm_id` → `vm_id must be omitted`.
