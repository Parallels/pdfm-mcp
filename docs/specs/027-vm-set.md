# 027 — VM configuration tool (`parallels_vm_set`)

Status: Implemented

## 1. Background

`prlctl set <vm> [options]` can modify almost every aspect of a VM's
configuration (~120 documented options: CPU/memory, boot, sharing, security,
… ). None of it is reachable from the server today — the AI can observe VMs
but cannot reconfigure them.

The full surface must NOT be exposed: most options are niche, cosmetic,
enterprise-only, or dangerous (credential governance, time bombs, data-loss
switches). But a curated subset is high-value, and the central design
requirement is that **a model can read the tool and use it correctly on the
first try** — the tool must document itself.

## 2. Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Shape | **one** tool `parallels_vm_set` = `vm_id` + optional, individually validated fields (≥1 required, enforced in the handler — JSON Schema cannot express "at least one of") | Keeps the tool list small for lower-capability models; one description to learn; the input schema itself is the documentation (per-field `jsonschema:` text = effect + values + preconditions); adding an option later is one struct field + one argv line |
| Field set | 33 curated fields (table below) | High value, safe, model-friendly. The rest are excluded with reasons (table after) |
| Validation | schema bounds **and** full re-validation in the service (untrusted input) | Repo rule: tool args are untrusted; defense in depth, same as every other tool |
| argv building | one `prlctl set <ref> --flag value …` call per request, fixed field order, single argv elements, never shell-interpolated | Matches the repo's exec pattern; prlctl applies all fields or fails |
| prlctl is the authority | no pre-checks of state/flag combos (e.g. `cpus` on a running VM) — prlctl's error is surfaced verbatim | Same pattern as `Power`; avoids duplicating prlctl's knowledge in Go |
| Cache | VM list cache invalidated on success | cached per-VM fields would be stale |
| Output | `{vm_id, output, applied}` where `applied` is the human-readable list of sent settings (`"cpus=8"`) | the AI (and the user) can confirm exactly what changed; no secrets exist on this surface |
| Policy class | new `ClassWrite` ("write") — recoverable changes to VM configuration, not power | read-only mode must hide it (class-based, automatic); `ClassPower` would conflate power transitions with config edits |
| Name | `parallels_vm_set` mirrors the prlctl command | pre-1.0, no alias needed |
| Sequence step | registered in `RegisterSteps` like every other `parallels_vm_*` tool | drift guard; multi-step "configure + restart" sequences work |
| `name` value | trimmed, one surrounding quote pair tolerated (`UnquoteVMRef`), 1–256 printable ASCII | spec 025 tolerance at every entry; prevents creating a VM literally named `"My VM"` |
| Folder `path` | 1–1024 printable ASCII, passed verbatim as ONE argv element; never built or shell-interpolated by us | AGENTS.md path rule: we don't construct paths from input — prlctl resolves it |
| Timeout | 60 s | `prlctl set` returns immediately (no data processing) |

### Included fields

| Field | Type / bounds | prlctl flag | Note |
|---|---|---|---|
| `name` | string 1–256 printable | `--name` | unquote-tolerant |
| `description` | string 1–256 printable | `--description` | unquote-tolerant |
| `cpus` | int 1–128 | `--cpus` | stopped VM |
| `memsize_mb` | int 16–131072 | `--memsize` | stopped VM |
| `videosize_mb` | int 2–8192 | `--videosize` | stopped VM; not supported on macOS VMs |
| `three_d_accelerate` | enum `off\|highest\|dx9` | `--3d-accelerate` | not supported on macOS VMs |
| `vertical_sync` | bool | `--vertical-sync` | not supported on macOS VMs |
| `high_resolution` | bool | `--high-resolution` | retina-display video mode; not supported on macOS VMs |
| `efi_boot` | bool | `--efi-boot on\|off` | |
| `select_boot_device` | bool | `--select-boot-device` | |
| `autostart` | enum `off\|open-window\|start-app\|start-host\|user-login` | `--autostart` | |
| `autostart_delay_s` | int 0–86400 | `--autostart-delay` | |
| `autostop` | enum `stop\|suspend\|shutdown` | `--autostop` | |
| `startup_view` | enum `same\|window\|coherence\|fullscreen\|modality\|headless` | `--startup-view` | |
| `on_shutdown` | enum `window\|close\|quit` | `--on-shutdown` | what Parallels does after the VM shuts down |
| `on_window_close` | enum `suspend\|shutdown\|stop\|ask\|keep-running` | `--on-window-close` | |
| `faster_vm` | bool | `--faster-vm` | |
| `auto_compress` | bool | `--auto-compress` | |
| `nested_virt` | bool | `--nested-virt` | |
| `smart_mount` | bool | `--smart-mount` | |
| `shared_clipboard` | enum `on\|host-to-guest\|guest-to-host\|off` | `--shared-clipboard` | macOS-on-Apple-Silicon guests: prlctl authority |
| `shf_host` | bool | `--shf-host` | user-defined host folders on/off |
| `shared_folder_add` | object `{name, path, mode?, description?, enabled?}` | `--shf-host-add … [--mode][--shf-description][--enable\|--disable]` | `name` 1–256 printable, `path` 1–1024 printable, `mode` `ro\|rw` (default rw), `enabled` default true |
| `shared_folder_remove` | object `{name}` | `--shf-host-del` | |
| `shared_folder_set` | object `{name, path, mode?, description?, enabled?}` | `--shf-host-set … [--mode][--shf-description][--enable\|--disable]` | same shape/bounds as `shared_folder_add`; `name` must already exist — prlctl errors on unknown names |
| `tpm` | enum `on\|off\|crb\|tis` | `--tpm` | `crb` required for Windows on ARM |
| `time_sync` | bool | `--time-sync` | |
| `lock_on_suspend` | bool | `--lock-on-suspend` | locks the guest screen on suspend |
| `smart_guard` | bool | `--smart-guard` | |
| `sg_notify_before_create` | bool | `--sg-notify-before-create` | Smart Guard: ask before creating a snapshot |
| `sg_interval_s` | int 1–2592000 | `--sg-interval` | Smart Guard: seconds between snapshots |
| `sg_max_snapshots` | int 1–32 | `--sg-max-snapshots` | Smart Guard: maximum snapshots kept |
| `isolate_vm` | bool | `--isolate-vm` | |

### Excluded options (deliberate; promote later = one field + one argv line)

- `--userpasswd` — creates guest accounts, secret-bearing → future ClassSecret work
- `--efi-secure-boot` — `reset` overrides BitLocker protection (KB 131109 danger)
- `--template` — converts a VM into a template (state change beyond config)
- `--expiration` — usage time bomb
- `--undo-disks` — can discard guest changes (data loss)
- `--require-pwd`, `--require-custom-pwd`, `--custom-pwd*`, `--allow-to-confirm`, `--lock-edit-settings`, `--host-admin` — credential/lock governance
- `--device-bootorder` — space-joined device names; error-prone for models
- `--external-boot-device` — needs a device name from `prlctl list -i`; niche
- `--shf-host-defined` (all values `off|alldisks|home`) — host-wide sharing toggle; `alldisks` is wide-open, `home` exposes the whole user home directory
- `--shf-guest`, `--shf-guest-automount` — guest→host folder sharing; niche
- `--sync-ssh-ids` — rewrites guest `authorized_keys`
- `--usedefanswers` — auto-answers VM prompts; niche
- `--password-to-edit` — dropped in Parallels Desktop 15 (use the custom-pwd commands, which are excluded above)
- printers (`--sync-host-printers`, `--sync-default-printer`), Coherence set, `--smart-mouse-optimize`, `--sticky-mouse`, `--keyboard-optimize`, `--swipe-from-edges`, `--keystroke-forwarding`, `--show-dev-tools`, `--pmu-virt`, `--battery-status`, `--disable-winlogo`, `--longer-battery-life`, `--adaptive-hypervisor`, `--pause-idle`, `--tools-autoupdate`, `--shared-profile`, `--shared-cloud`, `--sh-app-*`, `--sync-vm-hostname`, `--disable-timezone-sync`, `--distribution`, `--asset-id`, `--rename-ext-disks` — niche, cosmetic, or enterprise-only

The excluded list plus the field table above account for every documented
`prlctl set` option (verified against the Parallels knowledge-base "Virtual
Machine Configuration Tasks" page, 2026-10-12).

## 3. Components

| Component | Change |
|---|---|
| `internal/parallels/interfaces` | `VMSetOptions` (all fields pointers; `""` = unset for enum strings), `VMSharedFolderAdd`, `VMSetResult{Output, Applied}`, `VMSet` on `ParallelsService` |
| `internal/parallels/service/vm_set.go` (new) | validation (ranges, enum membership, printable-charset, lengths, unquote-tolerant names), fixed-order argv build, 60 s run, cache invalidation on success |
| `internal/tools/parallels_vm_set.go` (new) | `VMSetInput` (spec-025 `vm_id` contract + 33 documented fields), handler (nil-req-callable, `resolvePowerTarget`), `RegisterParallelsVMSet(s, mode)` via `powerInputSchema`/`powerDescription`, `VMSetOutput{VMID, Output, Applied}` |
| `internal/tools/steps.go` | `RegisterSteps` entry (VM-mode schema) |
| `internal/policy/policy.go` | `ClassWrite = "write"` + `"parallels_vm_set": ClassWrite`; class list in test error text |
| `internal/config/config.go` | `--read-only` help mentions config/write tools |
| `internal/server/server.go` | `toolTable` row (both modes) |
| `test/e2e/stdio_test.go` | listed (never executed) in fleet and VM mode |
| README | tool table row |

## 4. Acceptance criteria

1. `parallels_vm_set` listed in both modes; `vm_id` required in fleet, omitted in VM mode; hidden under `--read-only`.
2. Request with no configuration field → clean "at least one" error, no prlctl call.
3. Every field maps to the exact prlctl argv (unit table); combined fields = one prlctl call; out-of-range / bad-enum / non-printable / over-long values rejected before exec; `name` tolerates one surrounding quote pair.
4. Success returns `applied` with the sent settings and invalidates the VM list cache; prlctl failure is surfaced verbatim.
5. Portable schema (nullable pointer fields split, bare nodes pinned) — covered by the existing fleet protocol sweep; `vm_id` schema carries the spec-025 contract (`TestVMIDSchemaContract`).
6. Registered as a sequence step (drift guard green).
7. `make check` + `make test-e2e` green.

## 5. QA

- Unit + in-memory protocol tests (see `internal/parallels/service/vm_set_test.go`,
  `internal/tools/parallels_vm_set_test.go`); `make check` and `make test-e2e`
  green. 31 tools on the wire.
- Live wire QA (2026-10-12) on the disposable stopped `empty_catalog_test`
  VM (31-VM fleet); every change verified via `parallels_vm_info` and
  reverted to the exact baseline:
  - negative battery: no fields → `no configuration fields provided (set at
    least one)` before prlctl runs; `cpus: 999` → `invalid cpus: 999 (allowed
    1-128)`; `autostart: "bogus"` → schema anyOf rejection; non-printable
    `name` → `invalid name: contains non-printable characters`; unknown VM →
    prlctl error surfaced verbatim;
  - `name: "empty_catalog_test_qa"` → `applied: ["name=empty_catalog_test_qa"]`;
    the new name is visible in `parallels_search_vms` from the **cache**
    immediately after the set (cache invalidation works); the original name
    was restored by addressing the tool with the *new* name (name→ID
    resolution);
  - `cpus: 3, memsize_mb: 5120` in one call (was 2 / 4096) → `applied` lists
    both; `vm_info` shows 3 / 5120; reverted to 2 / 4096 and re-verified;
  - `faster_vm` + `time_sync` flipped in one call → `applied:
    ["faster_vm=off", "time_sync=off"]`; `vm_info` shows
    `Optimization "Faster virtual machine"` on→off and
    `Time Synchronization/enabled` true→false; both reverted.
  - `--read-only` mode: `parallels_vm_set` (and power tools) hidden — 8 read
    tools listed.
  - Video group (added 2026-10-12): `three_d_accelerate: "off"`,
    `vertical_sync: false`, `high_resolution: false` in one call on the
    disposable Ubuntu VM → prlctl: "The VM has been successfully
    configured."; the wire schema carries the "not supported on macOS VMs"
    note on all four video fields. These three values are not readable
    back from `prlctl list -i`, so they were set to conservative `off`
    values and left as-is on the disposable VM.
- Coverage batch (2026-10-12): audit against the documented `prlctl set`
  option list found `description` and `shared_folder_set` unaccounted for
  (neither implemented nor excluded) plus eight more undocumented
  exclusions. Added 7 fields (`description`, `shared_folder_set`,
  `on_shutdown`, `lock_on_suspend`, `sg_notify_before_create`,
  `sg_interval_s`, `sg_max_snapshots` → 33 total) and listed the remaining
  options explicitly so every documented option is now accounted for.
  Verified: unit argv cases per field, combined-order test, tool handler
  test, `make check` + `make test-e2e` green, and a live smoke on the
  disposable stopped `empty_catalog_test` VM (31-VM fleet, 2026-10-12):
  - all six new scalar fields in one call → `applied` in fixed order;
    `description` and `on_shutdown` (close→window) verified in
    `prlctl list -i` and `on_shutdown` reverted; the Smart Guard
    parameters (`sg_*`, `lock_on_suspend`) are stored but only readable
    in `list -i` while Smart Guard is enabled — left set on the
    disposable VM, like the video group;
  - `shared_folder_add` `qa2 → /tmp/qa2` (ro) → verified in `list -i`;
    `shared_folder_set` flipped it to rw → `qa2 (+) path='/tmp/qa2'
    mode='rw'` in `list -i`; folder removed in cleanup;
  - negative battery: non-existent host path → prlctl "Unable to use
    '/tmp/qa2' because it does not exist." surfaced verbatim;
    `shared_folder_set` on an unknown name → "The shared folder with
    name 'no_such_folder' does not exist." surfaced verbatim;
  - the wire schema now carries all 33 fields and the `on_shutdown`
    enum `["window","close","quit"]` on the non-null anyOf branch.
- Note: the output `vm_id` echoes the reference as passed (name or UUID),
  consistent with the power tools (spec 025).
