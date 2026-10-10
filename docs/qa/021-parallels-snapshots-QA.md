# QA 021 — Parallels Snapshot Management (`snapshot` / `snapshot-delete` / `snapshot-list` / `snapshot-switch`)

**Scope:** the `parallels_vm_snapshot_create`, `parallels_vm_snapshot_list`,
`parallels_vm_snapshot_delete` and `parallels_vm_snapshot_switch` tools,
implemented per [spec 021](../specs/021-parallels-snapshots.md).

**Safety note:** snapshot creation, deletion and switching are state
mutations that leave persistent artifacts on the VM's disk (snapshot
deltas, reverted live state). **Only `snapshot_list` is read-only**; it is
the only snapshot op automated tests ever execute. All mutation checks were
run on the user-approved disposable VM `empty_catalog_test` and cleaned up
after (final state: stopped, unencrypted, no snapshots).

## Protocol facts (verified live, Parallels 27.0.1)

The full table is in the spec. Highlights beyond the documented CLI:

- `snapshot-list <vm> --json` emits a **JSON object keyed by snapshot id**
  (`{"{id}": {"name","date","state","current","parent"}, …}`) with odd
  `}\n,` separators — **not** an array. Empty tree → empty output (exit 0).
- `snapshot-list -i <id>` prints labeled text (`ID:/Name:/Date:/State:/
  Description:`); **`-j` is ignored in this mode**; the format has **no
  parent field**; **bogus id → empty output, exit 0** (silent success).
- The snapshot id works **with or without** the `{}` braces (prlctl
  normalizes).
- Create: works on stopped **and running** VMs (state preserved); omitted
  `-n` stores an **empty-string name**; duplicate names are **allowed**;
  success message: "The snapshot with id {uuid} has been successfully
  created."
- Delete: deleting a **parent without `-c` re-roots the children**
  (`parent` becomes "") — it does not delete them. `-c` deletes the whole
  subtree **including the current snapshot**. Deleting the **current**
  snapshot is allowed (VM reverts to its parent's state, base state when
  root). Bogus id on a non-empty tree → "Failed to delete snapshot:
  Unable to find the snapshot ID. The snapshot may have been deleted."
  (exit 255); on an **empty** tree the message is instead "Failed to
  delete snapshot: Unable to open the snapshot configuration file." (exit
  255) — both surface as clean tool errors.
- Switch: works on stopped **and running** VMs. Without `--skip-resume`, a
  `poweron` snapshot **starts the VM**, a `poweroff` one leaves it
  stopped; `--skip-resume` never starts it. Switching while running keeps
  the VM running. Bogus id → "Failed to switch to snapshot: The
  configuration file you specified is invalid…" (exit 255 — confusing
  message, clean failure).
- **Encrypted VMs: all four operations require the current password**
  (one stdin line). Without it: "Failed to authorise current session
  against virtual machine. Error: The password you specified is invalid…"
  (exit 255). (An earlier "no password needed" observation during probing
  was an artifact of a VM whose `prlctl encrypt` had silently failed
  because the stop had not completed — re-verified with a verified
  `Encrypted: yes` state.)
- Bogus VM (all four) → "Failed to get VM config: The virtual machine
  could not be found…" (exit 255).
- The VM list JSON (`prlctl list --json`) carries **no snapshot data**, so
  only `snapshot-switch` can change the cached `State` → cache is
  invalidated by switch only.
- Stopping the disposable VM can hang (guest ignores ACPI);
  `prlctl stop --kill` is the reliable path.

## Steps

### 1. Automated suites

```
make check
make test-e2e
```

Expect green. The e2e gating test now asserts, default-on: all four tools
listed in **both** modes; a `snapshot_list` against the first
**unencrypted** VM (any power state) returning the `snapshots` array (each
item with id/name/date/state/current/parent) — possibly empty. Skipped
with a log when every VM is encrypted. No mutation and no password is ever
transmitted by automated tests.

### 2. Live protocol probes (executed 2026-10-09, disposable
`empty_catalog_test`, CLI direct)

1. Empty tree: `snapshot-list -j` → empty output, exit 0; table → header
   only.
2. Create `qa-snap-1` on the stopped VM → success line with braced UUID;
   VM stayed stopped. `-n`/`-d` accepted; create without `-n` →
   `"name": ""`; duplicate name → allowed.
3. List shapes: `--json` → object keyed by id; table → two columns, `*`
   marks current; `-t` → ASCII `_{parent}*{current}`.
4. Single mode: `-i <id>` with and without braces → labeled text; `-j`
   ignored; bogus id → empty output, exit 0.
5. Switch to ancestor: does **not** delete children (27.0.1); stopped VM
   stays stopped.
6. Resume semantics: switch to a `poweron` snapshot without
   `--skip-resume` → VM **started** (verified running); with
   `--skip-resume` → stays stopped.
7. Delete: leaf → ok; bogus id → exit 255 message; delete parent without
   `-c` → child re-rooted (`parent: ""`); chain c1→c2→c3 with `-c` on c1
   → whole tree gone; delete the **current** snapshot → VM reverts, tree
   emptied.
8. Switch while **running** (snapshots created while running) → success,
   VM stays running.
9. Encrypted cycle (verified `Encrypted: yes`): list/create/switch/delete
   **without** password → clean authorise error (exit 255); **with**
   password → full create→list→switch→delete cycle works.
10. Bogus VM id for all four commands → "could not be found", exit 255.
11. Cleanup: `-c` on the root snapshot removed the whole tree; VM
    decrypted; final state stopped/unencrypted/no snapshots (re-verified
    after each phase).

### 3. Live wire QA (executed 2026-10-09, `bin/pdfm-mcp` fleet mode, MCP
tools/call over stdio — driver `test/e2e/wire_qa_021.py`, transcript
`docs/qa/021-wire-transcript.jsonl`, server stderr
`docs/qa/021-wire-server.stderr`)

23-call cycle on `empty_catalog_test`, all green:

1. Baseline `vm_info` → stopped, unencrypted.
2. `snapshot_list` (empty tree) → `snapshots: []` (not null).
3. `snapshot_create` `qa-wire-1` → parsed braced `snapshot_id`.
4. `snapshot_create` `qa-wire-2` + description with a colon → parsed id.
5. `snapshot_list` → both entries, `parent`/`current` relations correct,
   deterministic date-sorted order.
6. `snapshot_list snapshot_id` (single mode) → parsed object incl.
   description.
7. `snapshot_list tree` → ASCII `_{id1}*{id2}`.
8. `snapshot_switch` to the poweroff snapshot → success; `vm_info` →
   still stopped.
9. `snapshot_list` → switched snapshot is `current`.
10. `snapshot_delete` child → ok; `snapshot_delete` current → ok;
    `snapshot_list` → `[]`.
11. `snapshot_delete` bogus id (empty tree) → clean tool error with
    prlctl's message, no silent success.
12. `vm_encrypt` (password over the wire) → `vm_info` → `Encrypted: yes`.
13. `snapshot_list` **without** password → clean authorise tool error
    (self-correcting hint, no crash).
14. `snapshot_list` **with** password → `[]`.
15. `snapshot_create` + `snapshot_delete` with password on the encrypted
    VM → both ok; list with password → `[]`.
16. `vm_decrypt` → `vm_info` → stopped, unencrypted.
17. Server exit 0.

**Password-leak audit:** the QA password appears in **no** server stderr
line (only prlctl's generic "password you specified is invalid" text,
which carries no value); the driver redacts passwords from the transcript
request log (6 request lines redacted post-hoc in the first run). No
password material in argv anywhere (transport is stdin only).

### 4. State audit

After the wire QA: `prlctl list empty_catalog_test -a -i --json` →
`State: stopped`, `Security.Encrypted: no`; `snapshot-list -j` → empty.
The disposable VM was left exactly as found (stopped, unencrypted, no
snapshots).

## Result

All acceptance criteria met. See the spec for deviations discovered
during implementation (notably: `--json` output is an object keyed by id,
not an array; single mode has no parent field and is silent on bogus ids;
encrypted VMs require the password for **all four** operations).
