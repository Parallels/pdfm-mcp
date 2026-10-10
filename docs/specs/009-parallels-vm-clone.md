# Spec 009: Parallels VM Clone

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers |
| Scope | `parallels_vm_clone` tool; `Clone` on the Parallels service; tests; QA doc |
| Out of scope | `--unlink` and `--force-deep-copy` (newer 27.x flags not in the reference doc this spec follows — see §11), `prlctl create`, templates management |

## 1. Goal

Clone a VM with `prlctl clone <vm_id | vm_name> --name <new_name>`,
making an exact copy of the source VM. The copy is a **new, separate VM**
— the source is untouched. Cloning is mutating (it creates VM files on
the host) but not destructive.

### CLI reference (Parallels Desktop CLI, as pasted by the user)

```
prlctl clone <vm_id | vm_name> --name <new_name>

Optional parameters:
  --template                      Make the new virtual machine a template.
  --dst <path>                    Directory where the clone files go (default location if omitted).
  --regenerate-src-uuid           Regenerate the source ID (SMBIOS product ID also changes).
  --linked                        Create a linked clone.
  -i, --id <snapid>               Linked clone based on a snapshot with the given snapid.
  --detach-external-hdd <yes|no>  yes: outside HDDs removed from the destination VM;
                                  no: they remain. Either way they are NOT copied.
```

Verified on prlctl 27.0.1 (`clone --help`): same flag set (the local
help omits argument syntax, e.g. it shows `--dst` bare).

### Acceptance criteria

1. `make check` and `make test-e2e` pass; the tool is **listed but never
   executed** by any automated test — no opt-in env: a real clone
   permanently creates a VM (cleanup needs a separate delete) and
   consumes disk.
2. Listed in fleet and VM mode; mode contract identical to the other
   VM-scoped tools. Fleet required: `name` **and** `vm_id`; VM mode:
   `name` only.
3. `name` is required and validated (non-empty after trim, ≤ 256 chars,
   printable — it becomes the new VM's name). All options default to
   prlctl defaults when omitted.
4. `snapshot_id` requires `linked: true` (per the reference: `--id`
   creates a *linked* clone based on a snapshot).
5. `detach_external_hdd` only accepts `""` (unset), `"yes"`, `"no"`.
6. Exec runs `prlctl clone <ref> --name <name> [options…]` with fixed
   argv elements (no shell, values never interpreted); 600 s timeout
   (full clones copy all VM files); any prlctl failure → tool error
   with stderr; on success the VM list cache is invalidated (the list
   gained a VM).
7. Every step in `docs/qa/009-parallels-vm-clone-QA.md` passes.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Tool surface | Exactly the pasted reference: `--name` (required), `--template`, `--dst`, `--regenerate-src-uuid`, `--linked`, `--id`, `--detach-external-hdd <yes\|no>` | Faithful to the user-provided CLI reference; the newer 27.x-only flags (`--unlink`, `--force-deep-copy`) are deliberately not exposed — follow-up if requested |
| `detach_external_hdd` type | Plain string, enum `{"", "yes", "no"}` (omitted = unset) | A bool cannot express "not provided"; the CLI takes an explicit yes/no value |
| Validation location | Tool: mode + `name` presence (schema). Service: value rules (name format, dst, snapshot_id, enum, linked↔snapshot_id combo) | Same split as power/exec/send-key; the service is the single place prlctl-arg sanity is enforced |
| Failure semantics | Non-zero exit = **tool error** with prlctl stderr (name collision, no space, source problems) — prlctl is the authority; no pre-state checks | Consistent with every other prlctl op |
| Timeout | 600 s (`cloneTimeout`) | The only op whose duration scales with VM image size; full clones of large VMs are the longest host-side operation in the module. Linked clones return quickly |
| Cache | Invalidate after **success** (a new VM appears in the list) | Same as power/delete |
| Annotations | `ReadOnlyHint: false`, `DestructiveHint: false`, `IdempotentHint: false`, `OpenWorldHint: false` | The source VM is never modified; the op is a creation. Disk usage is called out in the description instead |
| e2e | **Listed, never executed** — no opt-in | A clone is a permanent host artifact; safe automated runs would need a create+clone+delete dance with disposable VMs |
| Source VM state | No pre-check (running or stopped): prlctl decides and its error is surfaced | Same rule as power/delete; observed behaviour documented in QA |
| `dst` handling | Validated (trim, ≤ 1024 chars, printable) and passed to prlctl as a single argv element. No host-side confinement: prlctl performs the file operations, and confining to `~/Parallels` would break legitimate layouts | AGENTS.md "no shell interpolation / argv-only" satisfied; the path is data for prlctl, never used by this code |
| Logging | Log source ref, new name, took. Nothing else | Names are not sensitive |

## 3. Repository layout (delta)

```
pdfm-mcp/
├── internal/
│   ├── parallels/
│   │   ├── interfaces/interfaces.go   # + CloneRequest + Clone on ParallelsService
│   │   └── service/
│   │       ├── vm_clone.go            # NEW — Clone + cloneTimeout
│   │       └── vm_clone_test.go       # NEW
│   ├── tools/
│   │   ├── parallels_vm_clone.go      # NEW — input/output + handler + Register
│   │   └── parallels_clone_test.go    # NEW — unit + protocol tests
│   └── server/server.go               # + registration
├── test/e2e/stdio_test.go             # + listed assertions (fleet + VM mode)
└── docs/
    ├── specs/009-parallels-vm-clone.md
    └── qa/009-parallels-vm-clone-QA.md
```

No new dependencies.

## 4. Service layer

```go
// CloneRequest carries the validated clone options. Name is required;
// all other fields default to prlctl defaults when zero-valued.
type CloneRequest struct {
	Name              string // required: the new VM's name
	Template          bool   // --template
	Dst               string // --dst <path>; "" = prlctl default location
	RegenerateSrcUUID bool   // --regenerate-src-uuid
	Linked            bool   // --linked
	SnapshotID        string // -i, --id <snapid>; requires Linked
	DetachExternalHDD string // --detach-external-hdd <yes|no>; "" = unset
}

// added to ParallelsService:
//
// Clone creates a copy of the VM identified by vmRef (a VM id or name,
// pre-validated) named req.Name via `prlctl clone`. The source VM is
// never modified. On success the VM list cache is invalidated because
// the list gained a VM. Any prlctl failure is an error carrying its
// stderr.
Clone(ctx context.Context, vmRef string, req CloneRequest) (string, error)
```

`(*service).Clone`:

1. `enabled` check → `ErrNotEnabled`.
2. `ValidateVMRef(vmRef)`.
3. `name, err := ValidateVMRef(req.Name)` (reused: ≤ 256 chars,
   printable, non-empty after trim — the name becomes a VM ref).
4. `Dst`: if non-empty after trim → ≤ 1024 chars, printable.
5. `SnapshotID`: if non-empty after trim → ≤ 256 chars, printable, and
   `Linked` must be true (else error `snapshot_id requires linked: true`).
6. `DetachExternalHDD` ∈ {"", "yes", "no"} (else error).
7. argv: `clone <ref> --name <name>` + options in fixed order:
   `--template`, `--dst <dst>`, `--regenerate-src-uuid`, `--linked`,
   `--id <snapid>`, `--detach-external-hdd <yes|no>`.
8. `context.WithTimeout(ctx, cloneTimeout)` → `s.runner.Run(...)`;
   error → log `vm`, `name`, `err`; return it.
9. `s.cache.invalidate()`; return trimmed stdout.

## 5. Tool layer

```go
type CloneInput struct {
	VMID              string `json:"vm_id,omitempty" jsonschema:"VM id or name of the source VM to clone"`
	Name              string `json:"name" jsonschema:"name of the new VM (required)"`
	Template          bool   `json:"template,omitempty" jsonschema:"make the clone a template"`
	Dst               string `json:"dst,omitempty" jsonschema:"directory where the clone files are stored (prlctl default if omitted)"`
	RegenerateSrcUUID bool   `json:"regenerate_src_uuid,omitempty" jsonschema:"regenerate the source ID (SMBIOS product ID also changes)"`
	Linked            bool   `json:"linked,omitempty" jsonschema:"create a linked clone (much smaller and faster)"`
	SnapshotID        string `json:"snapshot_id,omitempty" jsonschema:"snapid for a snapshot-based linked clone; requires linked"`
	DetachExternalHDD string `json:"detach_external_hdd,omitempty" jsonschema:"yes|no: what to do with hard disks located outside the source VM (they are never copied)"`
}

type CloneOutput struct {
	VMID  string `json:"vm_id" jsonschema:"the source VM id or name"`
	Name  string `json:"name" jsonschema:"the name of the created clone"`
	Output string `json:"output" jsonschema:"prlctl stdout (usually empty)"`
}
```

Handler: `resolvePowerTarget` → build `CloneRequest` (trimmed
`Name`/`Dst`/`SnapshotID`; `DetachExternalHDD` as given — the service
validates) → `svc.Clone`.

`RegisterParallelsVMClone`: description
"Creates a copy of a VM via `prlctl clone`. `name` is required.
Options: template, dst (destination directory), regenerate_src_uuid,
linked (+ snapshot_id for a snapshot-based linked clone),
detach_external_hdd yes/no. The source VM is never modified; full
clones copy all VM files, so free disk space must be available. The
clone starts in stopped state." — mode-aware via `powerDescription`.

## 6. Security

- All values become fixed argv elements after validation; no shell, no
  interpolation, no file operations by this code (`dst` is data for
  prlctl).
- `name` reuses `ValidateVMRef` (printable, ≤ 256) — it will be used as
  a VM ref by other tools later.
- No logs beyond refs/names and timing.

## 7. Edge cases and invariants

| Case | Behaviour |
|---|---|
| `name` missing | Schema error (required) |
| `name` empty/whitespace or non-printable or > 256 | Service error, no exec |
| `snapshot_id` set without `linked` | Service error, no exec |
| `detach_external_hdd` = "yes"/"no" | Valid; "maybe"/"" (after explicit set) → error for bad values; empty input = unset |
| `dst` > 1024 or non-printable | Service error, no exec |
| Source VM unknown | prlctl not-found error; tool error |
| Name already taken | prlctl error surfaced (tool error); no clone created |
| No disk space | prlctl error surfaced |
| Source running | prlctl decides; error surfaced (observed in QA) |
| Timeout at 600 s | Tool error "(command timed out)" |
| Success | Trimmed stdout in `output`; cache invalidated; next `list_vms` shows the new VM |
| VM mode | Source = configured target; `name` still required; explicit `vm_id` rejected |

## 8. Testing

1. **`service/vm_clone_test.go`**: full argv for all options (fixed
   order, values byte-for-byte on `lastCall`); defaults → exactly
   `clone <ref> --name <name>`; each validation failure (bad name,
   snapshot without linked, bad detach value, long dst, non-printable)
   → 0 runner calls; success invalidates cache (list count 1 → 2 → 3);
   runner error surfaces stderr; disabled → `ErrNotEnabled`; 600 s
   deadline applied (590 s < remaining ≤ 600 s+1 ms).
2. **`tools/parallels_clone_test.go`**: mode table (VM mode configured
   target with name → argv `clone <vmOneID> --name <name>`; VM mode
   explicit vm_id rejected; fleet without vm_id rejected; fleet with
   vm_id ok); protocol test: listed, fleet required ⊇ `["name","vm_id"]`
   (and no other required fields), call with all options → argv
   byte-for-byte, structured `vm_id`+`name` correct; VM-mode protocol:
   required == `["name"]`; missing name → schema error; snapshot-without
  -linked via the wire → tool error naming both.
3. **e2e**: listed in both modes; **no execution, no opt-in**.

## 9. QA (manual, real machine)

`docs/qa/009-parallels-vm-clone-QA.md`:

- Automated: `make check`, `make test-e2e` (listed in both modes).
- Safe live checks (nothing created): missing name schema error;
  `detach_external_hdd: "maybe"` validation error; `snapshot_id` without
  `linked` validation error; unknown source VM → prlctl not-found error.
- Real clone (user-performed, needs a disposable source VM + free
  disk): full clone with a name → verify the new VM appears in
  `list_vms` (stopped), files on the host, source untouched; linked
  clone → verify under `prlctl list -i` "Linked Clones"; optional
  cleanup with `parallels_vm_delete`.

## 10. Implementation order

1. This spec + index row.
2. `interfaces.CloneRequest` + `Clone` + `service/vm_clone.go` + tests.
3. `tools/parallels_vm_clone.go` + tests.
4. `server.New` registration.
5. e2e listing assertions.
6. QA doc + README (tool row + safety note: disk usage).
7. `make check`, `make test-e2e`, live QA (safe checks only; real clone
   with a user-approved disposable VM).

## 11. Known unknowns / verification notes

- **Not exposed (deliberate scope):** prlctl 27.0.1's `clone --help`
  additionally lists `--unlink` (clone but unlink from parent) and
  `--force-deep-copy` (force deep copy instead of native clone
  capabilities). Neither appears in the CLI reference the user provided;
  both are follow-up candidates.
- Cloning a **running** source VM: does prlctl allow it or refuse?
  Observe during QA on a disposable VM and record the behaviour.
- Exact prlctl error text for name collision / no disk space — record
  in QA.
- `--detach-external-hdd` argument form: the reference says
  `<yes|no>` while the local help shows the flag bare (the local help
  omits argument syntax everywhere, e.g. `--dst`); implemented per the
  reference — verify on a VM with external HDDs during QA if available.

### Deviations (fill in during implementation)

- None — implemented exactly as specified. Live verification (safe
  checks only, nothing created): fleet required exactly
  `["name","vm_id"]`, VM mode `["name"]`, all validation paths correct,
  unknown-source error carries the exact argv in prlctl's message, and
  the fleet VM count was unchanged afterwards. Real full/linked clones
  remain the user-performed QA step (need a disposable source VM).
