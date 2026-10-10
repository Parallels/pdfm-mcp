# Spec 025: VM name targeting (AI-facing contract)

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers, release owner |
| Scope | The `vm_id` argument contract of every VM-scoped tool: the descriptions the AI client sees, and tolerant handling of quoted names in `ValidateVMRef` |
| Out of scope | Any change to how VMs are resolved at runtime (prlctl already accepts UUID or name), the allow-list, the VM-mode `--vm-id` target |

## 1. Goal

An AI client (e.g. an agent driving the server through MCP Inspector) can
reliably target a VM **by its display name**, not only by UUID, without
guessing or grepping: it sees exactly where the name is, knows that names
with spaces are fine as-is, and is never blocked by over-quoting.

### Background (the issue)

A live agent session against the server showed the model stuck after
`parallels_list_vms`: *"The VM has an 'ID' field (UUID) but the first entry
doesn't have a 'name'. The 'Name' key might have a different case or a
different key. Let me try grepping for 'Name'."* Two observations:

- Some VMs have an empty `Name` (the model saw an entry without a usable
  name and lost confidence that names exist at all), and the tool text never
  told it that `Name` is the field to use.
- The owner's concern: names with spaces. There is **no shell anywhere in
  the chain** — the reference is one argv element passed to `prlctl` via
  `exec.Command` — so a plain name with spaces needs no quoting. But LLM
  clients habitually quote shell-style, so an over-quoted value
  (`"My VM"`) would silently fail to resolve unless we tolerate it.

The runtime was already name-capable (owner: "the code itself is the same"):
every tool passes `vm_id` through `ValidateVMRef` into `prlctl`, which
resolves UUIDs **and** names; the allow-list resolves id↔name; the cache
resolves a configured name to its UUID (`resolvedID`). No resolution code
changes.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Name acceptance | **No behaviour change**: every VM-scoped tool already accepts a UUID or the exact display name (prlctl resolves; single argv element, never shell-interpolated) | Owner confirmed the code path is already identical for names |
| AI contract (property text) | A single shared description for every **input** `vm_id` schema: *"the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms. Pass the name exactly as shown — spaces are fine, no quotes needed"* — with the tool's own verb ("to capture", "to delete", …) | The agent reads the schema property description right where it fills the argument; one shared sentence keeps all 20 inputs in sync |
| AI contract (tool text) | `parallels_list_vms` now states that each VM has `ID` (UUID) and `Name` (display name, may be **empty**), and that either value works as `vm_id` anywhere (use `ID` when the name is empty or ambiguous); the fleet-mode power-tool description repeats the contract | The list output is where the agent finds names; the empty-`Name` case it actually hit is called out explicitly |
| Quoting | Names with spaces are passed **plain** (no shell exists). The `UnquoteVMRef` helper strips **one** pair of matching surrounding double quotes (and surrounding whitespace) and is applied at the earliest point the reference enters the server — `resolvePowerTarget` (so an over-quoted name still authorizes against `--allow-vms`) and `--vm-id` config parsing (the normalized form is what gets stored) — plus inside `ValidateVMRef` as defense in depth for direct service callers. The tolerance is silent — docs keep saying "no quotes needed" | Directly covers the failure mode the owner was worried about without encouraging quoting; a single named helper keeps the rule in one place |
| No name→UUID pre-resolution | Tools keep passing the reference to prlctl as-is; no extra list lookup per call | A round-trip on every call for zero functional gain; prlctl is the authority and surfaces "not found" itself |

## 3. Components

| Component | Change |
|---|---|
| `internal/parallels/service/power.go` | new `UnquoteVMRef` helper (trim + strip one pair of surrounding double quotes); `ValidateVMRef` uses it |
| `internal/parallels/parallels.go` | re-exports `UnquoteVMRef` |
| `internal/tools/parallels_power.go` `resolvePowerTarget` | unquotes the raw `vm_id` before allow-list authorization (spec 025) |
| `internal/config/config.go` | stores `UnquoteVMRef(vmID)` for `--vm-id` so prlctl receives the plain name |
| `internal/tools/parallels_power.go`, `parallels_vm_capture.go`, `parallels_vm_change_encryption_password.go`, `parallels_vm_clone.go`, `parallels_vm_decrypt.go`, `parallels_vm_delete.go`, `parallels_vm_encrypt.go`, `parallels_vm_exec.go`, `parallels_vm_exec_jobs.go`, `parallels_vm_info.go`, `parallels_vm_observe.go`, `parallels_vm_reclaim_disk.go`, `parallels_vm_send_key.go`, `parallels_vm_snapshot_create.go`, `parallels_vm_snapshot_delete.go`, `parallels_vm_snapshot_list.go`, `parallels_vm_snapshot_switch.go`, `parallels_vm_type.go`, `internal/orchestrator/types.go` | input `vm_id` jsonschema descriptions → the shared contract text; `powerDescription` fleet clause updated |
| `internal/tools/parallels_list_vms.go` | tool description + `vms` property text: `ID`/`Name` fields, either works as `vm_id`, `Name` may be empty |
| `internal/parallels/service/power_test.go` | `TestUnquoteVMRef` (one outer pair only) + `TestValidateVMRef` tables (plain spaces, quoted, double-quoted, unbalanced, empty, over-long) |
| `internal/tools/parallels_power_test.go` | every power tool: a quoted `vm_id` (` "guest x" `) reaches prlctl as the plain name |
| `internal/config/config_test.go` | `--vm-id '  "my vm"  '` is stored as `my vm` |
| `internal/tools/register_test.go` | `TestVMIDSchemaContract` guard: every input `vm_id` tag in `internal/tools/*.go` and `internal/orchestrator/*.go` must mention `parallels_list_vms` and "no quotes" |

## 4. Acceptance criteria

1. Every input `vm_id` schema description contains the shared contract
   (UUID/`Name` from `parallels_list_vms`, spaces fine, no quotes) — enforced
   by `TestVMIDSchemaContract`.
2. `ValidateVMRef("My VM")` and `ValidateVMRef("My VM" with one pair of
   surrounding quotes")` both resolve to `My VM`; a lone quote, an
   unbalanced quote, and empty/over-long refs are rejected as before.
3. No behavioural change for unquoted values: `make check` and
   `make test-e2e` stay green.
4. Live (release owner / QA): in an MCP client, `parallels_list_vms` shows
   names, and `parallels_vm_info` / a power tool called with a **name**
   (including a name with spaces) targets the right VM.

## 5. QA

- `make check` (unit + lint): green. `make test-e2e`: green.
- `TestValidateVMRef` (unit): plain spaces, one pair of surrounding quotes
  stripped, double-quoted, unbalanced, lone quote, empty, over-long, NUL.
- `TestVMIDSchemaContract` (guard): all 20 input `vm_id` tags in
  `internal/tools` + `internal/orchestrator` carry the shared contract.
- Live wire QA (2026-10-10, `wire_qa_025`, local Parallels Desktop, 31 VMs):
  - `tools/list` (29 tools): all 26 `vm_id` input schemas on the wire carry
    the contract text ("parallels_list_vms … no quotes");
  - `parallels_list_vms` returned 31 VMs (7 with spaces in `Name`);
  - `parallels_vm_info` with `vm_id: "macOS clone"` (plain name with spaces)
    returned exactly that VM's object (ID match);
  - the over-quoted form (`vm_id: "\"macOS clone\""`) resolved to the same ID;
  - an unknown name failed cleanly with prlctl's "could not be found" error
    (prlctl is the authority; no silent fallback).

## 6. Deviations

- (none recorded yet)
