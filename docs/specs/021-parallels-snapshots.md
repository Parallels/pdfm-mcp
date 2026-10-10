# Spec 021 — Parallels Snapshot Management (`snapshot` /
`snapshot-delete` / `snapshot-list` / `snapshot-switch`)

| Field | Value |
|---|---|
| Status | Implemented |
| Depends on | 001 (scaffold), 002 (module), 005 (CommandRunner/RunStdin), 011 (password transport), 012 (long-op timeout) |
| CLI basis | `prlctl snapshot <vm> [-n <name>] [-d <desc>]`, `prlctl snapshot-delete <vm> -i <snapid> [-c]`, `prlctl snapshot-list <vm> [{-t} \| {-i <snapid>}] [-j]`, `prlctl snapshot-switch <vm> -i <snapid> [--skip-resume]` |
| QA | [docs/qa/021-parallels-snapshots-QA.md](../qa/021-parallels-snapshots-QA.md) |

## 1. Goal

Expose the four snapshot commands as four tools —
`parallels_vm_snapshot_create`, `parallels_vm_snapshot_list`,
`parallels_vm_snapshot_delete`, `parallels_vm_snapshot_switch` — in both
server modes. Snapshots are the module's remaining state-management
surface: checkpoints a VM can be reverted to.

### Live-verified protocol facts (Parallels 27.0.1, disposable VM
`empty_catalog_test`)

| Fact | Detail |
|---|---|
| Create | Works on **stopped and running** VMs (VM state preserved). stdout: "Creating the snapshot… The snapshot with id {UUID} has been successfully created." Omitted name → **empty-string name**; duplicate names are **allowed**. |
| Delete | `snapshot-delete <vm> -i <id> [-c]`. Deleting a **parent without `-c` re-roots the children** (their `parent` becomes "") — it does not delete them. `-c` deletes the snapshot **and all descendants, including the current one**. Deleting the **current** snapshot is allowed — the VM reverts to its parent's state (base state when root). Bogus id → "Failed to delete snapshot: Unable to find the snapshot ID…", exit 255. |
| List, tree + `-j` | A JSON **object keyed by snapshot id**: `{"{id}": {"name","date","state","current","parent"}, …}` with `}\n,` separators — **not** an array. **Empty tree → empty output** (not `{}`). Table mode: two columns, `*` marks current. `-t` → ASCII tree. |
| List, `-i <id>` single mode | Human text "ID:/Name:/Date:/State:/Description:"; **`-j` is ignored in this mode**; works **with or without** the `{}` braces; **bogus id → empty output, exit 0** (silent!). |
| Switch | Works on stopped **and running** VMs. Without `--skip-resume`: a `poweron` snapshot **starts the VM**, a `poweroff` one leaves it stopped. `--skip-resume` never starts it. Bogus id → "Failed to switch to snapshot: The configuration file you specified is invalid…" (confusing but clean), exit 255. |
| Snapshot JSON fields | `name` (may be ""), `date` "YYYY-MM-DD HH:MM:SS", `state` "poweron"/"poweroff", `current` bool, `parent` ("" = root); ids carry `{…}` braces |
| Encrypted VMs | **All four** operations require the current password (one stdin line) — without it: "Failed to authorise current session … invalid", exit 255. (An earlier "no password needed" observation was an artifact of a VM that had not actually encrypted.) |
| Bogus VM (all four) | "Failed to get VM config: The virtual machine could not be found…", exit 255 |
| VM list JSON | Contains **no snapshot data** — only `snapshot-switch` can change the VM's `State` |

### Acceptance criteria

- Four tools registered in **both** modes; standard mode contract
  (`vm_id` required in fleet, rejected in VM mode).
- `snapshot_list` default mode runs `--json` and parses the id-keyed
  object into a structured, date-sorted `snapshots` list (empty tree →
  empty list, never an error);
  `snapshot_id` selects the single-snapshot mode (parsed into a
  `snapshot` object; empty output for a bogus id → `not_found: true`);
  `tree: true` returns the ASCII art as `output`.
- `snapshot_create` returns the new snapshot id (parsed from prlctl's
  message) plus the raw output; `name`/`description` optional.
- `snapshot_delete` requires `snapshot_id`; `children: true` adds `-c`;
  annotated destructive.
- `snapshot_switch` requires `snapshot_id`; `skip_resume` adds
  `--skip-resume`; annotated destructive.
- `password` (optional) on all four: fed over stdin (never argv/logs/
  errors) when present; absent → plain Run and an encrypted VM surfaces
  prlctl's clean authorise error (self-correcting).
- Ids pass through verbatim (prlctl accepts braces and no braces);
  validated like VM refs (printable, bounded).
- Cache: only `snapshot_switch` invalidates (it is the only op that can
  flip the VM's `State`).
- e2e: list-only (read-only, default-on, unencrypted VM); create/delete/
  switch never run in automated tests (persistent artifacts / state
  reverts on real VMs).
- Repo rules: all four in `tools.RegisterSteps` (drift guard 18 → 22),
  nil-`*mcp.CallToolRequest`-safe handlers, unit + in-memory protocol
  tests, `make check` + `make test-e2e` green.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| One tool per CLI action | 4 tools | Repo convention; each action has a different safety profile and argument set |
| Password | Optional on all four, stdin via `RunStdin` when present | Verified: encrypted VMs need exactly one line; unencrypted VMs ignore stdin (harmless) |
| List default | Always `-j`, parsed; empty output → `snapshots: []` | Machine-readable; the empty-output quirk is handled, not propagated |
| Single-snapshot mode | `-i <id>` without `-j` (ignored by prlctl there); the labeled output is parsed into `snapshot{id,name,date,state,description}`; empty output → `not_found: true` with a hint | `-j` is useless in this mode; the silent-bogus-id quirk becomes an explicit flag |
| Ids | Validated (printable, ≤256 chars) and passed through verbatim — braces optional, prlctl normalizes | Both forms verified working |
| Name / description validation | Name: trim-checked non-empty, ≤128 printable chars (spaces allowed). Description: ≤512 printable chars, may be empty | User-facing labels; keep them sane without silently altering |
| Annotations | list: `ReadOnlyHint: true`. create: destructive false, idempotent false. delete: **destructive true**. switch: **destructive true**, idempotent false. All: `OpenWorldHint: true` | Delete is irreversible (and `-c` cascades); switch reverts live state — changes made after the snapshot are lost from the running state |
| Timeouts | create/delete/switch: `longDiskOpTimeout` (snapshots move data — a dirty large disk can be slow). list: runner default | Same reasoning as specs 011/012 |
| Cache | Invalidate after switch only | The list JSON carries no snapshot data; only switch flips `State` |
| Sequence steps | All four registered (may carry a password arg — caller's responsibility) | Repo rule; drift guard |

## 3. Repository layout (delta)

| File | Change |
|---|---|
| `internal/parallels/interfaces/interfaces.go` | + `SnapshotInfo`, `SnapshotListResult`; + 4 `ParallelsService` methods |
| `internal/parallels/service/snapshot.go` | New: the four service ops + `ValidateSnapshotID`, `ValidateSnapshotName`, `ValidateSnapshotDescription`, shared password-stdin helper, id parsing from the create message |
| `internal/tools/parallels_vm_snapshot_{create,list,delete,switch}.go` | New: one tool per action |
| `internal/tools/descriptions.go` | + 4 `descVMSnapshot*` constants |
| `internal/tools/steps.go` | + 4 actions (drift guard 22) |
| `internal/server/server.go` | + 4 registrations in both modes |
| `internal/parallels/service/snapshot_test.go`, `internal/tools/parallels_snapshot_test.go` | New tests |
| `internal/tools/parallels_sequence_test.go` | Drift guard 18 → 22 |
| `test/e2e/stdio_test.go` | Listing checks both modes + default-on list exec |
| `docs/qa/021-parallels-snapshots-QA.md`, `docs/specs/README.md`, `README.md` | Docs |

## 4. Service layer

```
CreateSnapshot(ctx, vmRef, name, description, password) (id string, output string, error)
  args = [snapshot, ref] (+ -n <name> / -d <desc>)
  → Run/RunStdin → parse "The snapshot with id {UUID} has been successfully created."
    (regex `snapshot with id (\{[0-9a-fA-F-]+\})`; fall back to raw output if no match)

DeleteSnapshot(ctx, vmRef, snapID, password, children) (string, error)
  args = [snapshot-delete, ref, -i, snapID] (+ -c)

ListSnapshots(ctx, vmRef, snapshotID, password, tree) (SnapshotListResult, error)
  snapshotID != "" → -i mode: parse the labeled lines; empty stdout → NotFound
  tree            → -t: Output = trimmed stdout
  default         → --json: empty stdout → Snapshots = []; else parse the
                    id-keyed object (array variant tolerated), sorted by
                    Date then ID; unrecognized shape → raw Output
  SnapshotInfo: ID, Name, Date, State, Description (single mode), Current, Parent

SwitchSnapshot(ctx, vmRef, snapID, password, skipResume) (string, error)
  args = [snapshot-switch, ref, -i, snapID] (+ --skip-resume)
  → cache.invalidate()
```

All four: enabled check, `ValidateVMRef`, optional
`ValidatePassword` + `RunStdin(pw+"\n")` vs plain run, non-zero exit →
error with prlctl's stderr (password never in it), create/delete/switch
use `longDiskOpTimeout`.

## 5. Tool layer

- `VMCreateSnapshotInput {vm_id, name, description, password}` →
  `VMCreateSnapshotOutput {snapshot_id, output}`.
- `VMListSnapshotsInput {vm_id, snapshot_id, tree, password}` →
  `VMListSnapshotsOutput {snapshots: [VMListSnapshotItem], snapshot:
  *VMListSnapshotItem (single mode), not_found: bool (single mode),
  output: string (tree mode)}` — `VMListSnapshotItem {id, name, date,
  state, current, parent}`.
- `VMDeleteSnapshotInput {vm_id, snapshot_id, children, password}` →
  `{output}`.
- `VMSwitchSnapshotInput {vm_id, snapshot_id, skip_resume, password}` →
  `{output}`.
- `snapshot_id` is required (no omitempty) on delete/switch/single-list.

## 6. Security

- Password: spec 011 rules (stdin only, validated, never in argv/logs/
  errors; prlctl's stderr is password-free — verified).
- Delete (`children: true`) and switch are state-losing: destructive
  annotations + explicit descriptions ("reverting discards changes made
  after the snapshot from the live state").
- Create consumes host disk space (snapshot deltas) — noted in the
  description.

## 7. Testing

- Service (fake runner): exact argv per action/flag (create name/desc,
  delete -c, list default/-i/-t, switch --skip-resume), stdin payload
  `pw\n` vs none, create-id parsing (match + fallback), list JSON parse
  (multi-snapshot tree, empty output → empty list), single-mode line
  parse + empty → NotFound, bogus-id error pass-through without
  password, invalid id/name/description rejected pre-run, switch
  invalidates cache (create/delete/list do not).
- Tools (in-memory): fleet `required` = `[vm_id]` for
  create/list, `[vm_id, snapshot_id]` for delete/switch; VM mode rejects
  explicit vm_id; annotations (delete/switch destructive true; list read
  only true); nil-request safety via the sequence registry.
- e2e: all four listed in both modes; default-on `snapshot_list` on the
  first unencrypted VM (any power state) asserting the `snapshots` array
  (possibly empty); skipped with a log if every VM is encrypted.
- Drift guard: 22 actions.

## 8. QA (manual, real machine)

See [QA 021](../qa/021-parallels-snapshots-QA.md): full cycle on the
disposable VM — create (named/unnamed/duplicate), list (all three
modes), delete (leaf/parent/current/`-c` chain/bogus), switch (stopped
/ running, `--skip-resume` on), encrypted-VM cycle with password,
bogus VM, password-leak audit; VM restored to no snapshots, stopped,
unencrypted.

### Deviations (from the original draft)

1. **`--json` emits a JSON object, not an array.** The draft (and an
   early probe note) described `snapshot-list --json` as "a JSON array of
   single-key objects." Live wire output on 27.0.1 is a **single JSON
   object keyed by snapshot id** (`{"{id}": {...}, …}`), with `}\n,`
   separators. The parser now unmarshals `map[string]snapshotRaw` (an
   array of single-key objects is still tolerated as a fallback), and the
   unit/e2e fixtures use the real object shape.
2. **Deterministic ordering added.** The id-keyed object has no stable
   order (and Go map iteration is random), so the parsed `snapshots` list
   is sorted by `Date` then `ID`. Not in the draft; required for a
   stable agent-facing result.
3. **Single-snapshot mode reports no parent.** `snapshot-list -i <id>`
   prints `ID/Name/Date/State/Description` only — there is no `parent`
   line — so `parent` is always empty in single mode (documented on the
   output field). The draft assumed the same fields as tree mode.
4. **Delete error text differs on an empty tree.** A bogus id on a
   non-empty tree yields "Unable to find the snapshot ID. The snapshot
   may have been deleted."; on an **empty** tree prlctl instead says
   "Unable to open the snapshot configuration file." Both are clean
   exit-255 failures surfaced verbatim; the QA driver's expected text
   accounts for the empty-tree case.
5. **All four ops need the password on an encrypted VM.** Verified
   against a confirmed-encrypted VM: list, create, delete and switch each
   fail with the authorise error without the password and succeed with it
   (one stdin line). An earlier "no password needed" reading during
   probing was an artifact of a VM whose `encrypt` had silently not
   applied (its `stop` had not finished); re-verified cleanly.

