# 028 — VM device management tools (add / set / del)

Status: Implemented

## 1. Background

`prlctl set <vm>` has a second, device-oriented surface — `--device-add`,
`--device-set`, `--device-del`, `--device-connect`, `--device-disconnect`
(virtual and physical hard drives, optical drives, network adapters,
serial/parallel ports, sound cards, USB support) — that is completely
unreachable from the server today. An AI can observe devices
(`parallels_vm_info` returns prlctl's raw JSON, whose `Hardware` object is
keyed `hdd0`, `cdrom0`, `net0`, …) but cannot attach, reconfigure, or
remove any of them.

`prlctl` allows **only one device operation per command**. The operations
also differ in required fields, VM-state preconditions, and risk:

- **add** needs a device `type` plus type-specific options; the VM must be
  stopped (prlctl authority).
- **set** needs an existing device name plus optional options and the
  `--enable/--disable` / `--connect/--disconnect` toggles; config changes
  need a stopped VM, connect/disconnect a **running** one.
- **del** needs a device name; for virtual HDDs it can destroy the disk
  image (`--destroy-image`, `--destroy-image-force`) — irreversible data
  loss.

## 2. Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Tool split | **three** tools: `parallels_vm_device_add`, `parallels_vm_device_set`, `parallels_vm_device_del` | One tool per concept, matching the repo's one-verb-per-tool rule. A single `op` discriminator would mix stopped-only and running-only preconditions and add-only vs modify-only fields in one schema; one tool per device type would be 16+ tools |
| connect/disconnect | folded into `device_set` as `connected` bool | the docs define `--device-set <name> --connect\|--disconnect` as a form of set; no fourth tool needed |
| Field set | every documented device option except `--network-conditioner` (tables below); the exclusion list accounts for the whole documented device surface, spec-027 standard | high value, model-friendly; conditioner is a testing-only niche (6 numeric fields) and a future-spec candidate |
| Self-documenting schema | per-field `jsonschema:` text states effect, allowed values, which device type the field applies to, and state preconditions; enum values injected into the schema (spec-027 `applySetEnums` pattern) | a model can read the tool and use it correctly on the first try |
| Validation | schema bounds **and** full re-validation in the service, including cross-field rules (which fields are allowed with which device type / device-name prefix) | repo rule: tool args are untrusted; cross-field misuse (e.g. `size_mb` on a `net0`) must fail before exec with a helpful error |
| prlctl is the authority | no state pre-checks (stopped vs running, macOS-VM unsupported adds) — prlctl's error is surfaced verbatim | spec-027 pattern; avoids duplicating prlctl knowledge in Go |
| argv building | one `prlctl set <ref> --device-<verb> …` call per request, fixed field order, single argv elements, never shell-interpolated | matches the repo's exec pattern; "one op per command" is enforced structurally |
| Timeouts | add: **600 s** (`--type plain --size` pre-allocates disk files); set/del: **60 s** | `set`/`del` return immediately; a large plain-disk add does data processing |
| Output | `{vm_id, output, applied}` — same contract as `parallels_vm_set` | the AI (and the user) can confirm exactly what changed |
| Policy classes | add/set: `ClassWrite`; del: `ClassDestructive` | add/set are recoverable config edits; del can destroy disk images — the classification table's harmful-mode rule (reclaim_disk precedent) puts it with delete/snapshot_delete, so read-only mode hides it automatically |
| Confirm gate | `device_del.confirm` must equal `device_name` when `image_action` ≠ `detach_only` | spec-008 precedent (delete repeats the VM ref); the model already knows the device name, so the gate costs nothing but blocks an accidental image destruction |
| Name | mirrors the prlctl verbs | pre-1.0, no alias needed |
| Sequence step | all three registered in `RegisterSteps` | VM-scoped tool rule; drift guard; "stop → add disk → start" sequences work. The generated sequence description's step-signature list grew ~600 bytes; its size budget moved 3072 → 4096 |
| Device names | `^[a-z]{2,8}[0-9]{1,3}$` (hdd0, cdrom0, net0, fdd0, serial0, parallel0, sound0), plus bare `usb` for set (enable/disable/connect toggles, live-verified) and del (removes USB support) | names come from `prlctl list -i` / `parallels_vm_info`; prlctl stays the final authority for unknown prefixes |
| Host devices | `host_device` accepts 1–1024 printable characters, described as "the name from `prlsrvctl disk list` / `prlsrvctl info` (host CLI)" | this server has no host-device discovery tool; a `parallels_host_devices` read tool is a follow-up candidate, out of scope here |
| fdd (floppy) | **not in the add surface**: prlctl 27.0.1 rejects `--device-add fdd` at the parameter level (live-verified); set still accepts `fddN` names with `image`/`recreate` (name-driven, prlctl authority); del accepts `fddN` by name | the add `type` is an enum — a type that can never be created would fail on the model's first try; set/del only reference devices that already exist in `Hardware`, so prlctl being the authority there is sufficient |
| cdrom media | `device_add` with `type=cdrom` requires `image` or `host_device` (validated before exec) | prlctl refuses to create an optical drive without media: `--device-add cdrom` fails with "The device type is not specified" (live-verified); an empty tray only exists on VMs that were created with one |

## 3. `parallels_vm_device_add`

`vm_id` + required `type` + optional per-type fields.

| Field | Applies to | Type / bounds | prlctl flag |
|---|---|---|---|
| `type` | all | required enum `hdd\|cdrom\|net\|serial\|parallel\|sound\|usb` (no fdd — see §2) | `--device-add <type>` |
| `image` | hdd (optional; omitted = prlctl auto-creates `harddiskN.hdd` next to the VM config), cdrom (**required**; iso/cue/ccd/dmg) | 1–1024 printable | `--image` |
| `disk_type` | hdd | enum `expand\|plain` (expand default) | `--type` |
| `size_mb` | hdd | int 1–33554432 (32 TiB) | `--size` |
| `split` | hdd | bool | `--split` |
| `iface` | hdd, cdrom | enum `ide\|scsi\|sata` | `--iface` |
| `position` | hdd, cdrom | int 0–3 (ide) / 0–6 (scsi, default) / 0–5 (sata), checked against `iface` when given | `--position` |
| `subtype` | hdd, cdrom | enum `buslogic\|lsi-spi\|lsi-sas` | `--subtype` |
| `online_compact` | hdd | bool | `--online-compact on\|off` |
| `host_device` | hdd/cdrom (physical passthrough), serial, parallel | 1–1024 printable; host name from `prlsrvctl disk list` / `prlsrvctl info` | `--device` |
| `passthrough` | hdd/cdrom (with `host_device`) | bool | `--passthr yes\|no` |
| `net_type` | net | **required** for net; enum `shared\|bridged\|host-only` | `--type` |
| `net_iface` | net (bridged/host-only) | 1–1024 printable host interface name | `--iface` |
| `mac` | net | `"auto"` or `aa:bb:cc:dd:ee:ff` | `--mac` |
| `ip_add` / `ip_del` | net | IP or IP/mask | `--ipadd` / `--ipdel` |
| `dhcp` / `dhcp6` | net | bool | `--dhcp` / `--dhcp6` (yes/no) |
| `gw` / `gw6` / `nameserver` | net | valid IP | `--gw` / `--gw6` / `--nameserver` |
| `search_domain` | net | 1–256 printable | `--searchdomain` |
| `configure` | net | bool | `--configure yes\|no` |
| `apply_ip_only` | net | bool | `--apply-iponly yes\|no` |
| `adapter_type` | net | enum `virtio\|e1000\|e1000e\|rtl` | `--adapter-type` |
| `output` | serial, parallel | 1–1024 printable (file for port output) | `--output` |
| `socket` | serial | 1–1024 printable | `--socket` |
| `socket_mode` | serial (only with `socket`) | enum `server\|client` | `--socket-mode` |
| `sound_output` / `sound_input` | sound (both required together) | 1–1024 printable | `--output` / `--input` |

Type cross-validation (service, before exec):

- hdd/cdrom: `image` and `host_device` are mutually exclusive (virtual XOR
  physical); with `host_device`, `image`/`disk_type`/`size_mb`/
  `split`/`online_compact` are rejected.
- cdrom: `image` or `host_device` required (prlctl cannot create an
  optical drive without media).
- net: `net_type` required; `net_iface` only with bridged/host-only.
- serial: exactly one of `host_device` | `output` | `socket`;
  `socket_mode` only with `socket`.
- parallel: exactly one of `host_device` | `output`.
- sound: both `sound_output` and `sound_input` required.
- usb: no other fields allowed.
- a type-specific field sent with the wrong `type` →
  `"<field> does not apply to type=<type>"` (checked up front for every
  provided field, so a mismatch can never be silently dropped).

Notes surfaced in the field descriptions: cdrom/serial/sound adds are not
supported on macOS VMs and prlctl cannot create floppy devices (prlctl
authority); the VM must be stopped for add (prlctl authority).

## 4. `parallels_vm_device_set`

| Field | Notes |
|---|---|
| `vm_id` | standard contract |
| `device_name` | **required**, `^[a-z]{2,8}[0-9]{1,3}$` or bare `usb`; "see the `Hardware` object in `parallels_vm_info`" |
| `enabled` | bool → `--enable`/`--disable`; mutually exclusive with `connected` |
| `connected` | bool → `--connect`/`--disconnect`; **VM must be running** |
| the §3 configuration fields (except `type`) | same per-field validation; `host_device` applies to serial/parallel ports; `recreate` applies to fdd devices (with `image`) |

Rules:

- at least one of `enabled`/`connected`/any config field (enforced in the
  service, same as `parallels_vm_set`).
- **prefix/type cross-check**: the name prefix identifies the device type,
  so `size_mb` on `net0` or `net_type` on `hdd0` is rejected with a
  helpful error; `usb` has an empty field table (toggles only); unknown
  prefixes pass through to prlctl.
- config changes need a stopped VM, connect/disconnect a running VM —
  prlctl's error is surfaced verbatim.

## 5. `parallels_vm_device_del`

| Field | Notes |
|---|---|
| `vm_id` | standard contract |
| `device_name` | required; same pattern as set, plus bare `usb` (removes USB support) |
| `image_action` | enum `detach_only` (default; disk image preserved) \| `destroy_image` \| `destroy_image_force` (also from all snapshots); applies to hdd devices |
| `confirm` | required to equal `device_name` when `image_action` ≠ `detach_only` (spec-008 confirm-gate pattern); ignored for `detach_only` |

`--detach-only` is emitted explicitly only for hdd-prefixed names; other
device types take no image flag. `DestructiveHint: true` on the MCP
annotation; `ClassDestructive` in the policy table (hidden in read-only
mode automatically).

## 6. Service layer

- `interfaces`: `VMDeviceAddOptions`, `VMDeviceSetOptions`,
  `VMDeviceDelOptions`, `VMDeviceResult{Output, Applied}`; three methods on
  `ParallelsService`: `VMDeviceAdd`, `VMDeviceSet`, `VMDeviceDel` (doc
  comments match the `VMSet` style: full re-validation, prlctl authority,
  cache invalidation on success).
- `internal/parallels/service/device.go`: three `buildDevice*Args` argv
  builders in fixed field order, single argv elements, never a shell;
  shared validation helpers (printable ASCII, closed enums, int ranges,
  MAC/IP/CIDR via stdlib `net`, device-name pattern, per-iface position
  ranges).
- Fixed argv forms: `set <ref> --device-add <type> …`,
  `set <ref> --device-set <name> …`, `set <ref> --device-del <name> [flag]`.
- VM list cache invalidated on success (device layout changed).

## 7. Excluded options

| Option | Reason |
|---|---|
| `--network-conditioner` (+ 6 numeric fields, + `--network-conditioner-profile`) | testing-only niche (imitating adverse network conditions); six more fields for a workflow a model rarely needs; future-spec candidate |
| `--device-add fdd` | prlctl 27.0.1 rejects it at the parameter level ("An incorrect parameter for --device-add is specified: fdd", live-verified); floppy devices are legacy and not creatable on current prlctl. Existing fdd devices remain reachable via `device_set`/`device_del` by name |

That is the entire exclusion list: the field tables above plus this list
account for the documented `prlctl set` device surface (add / set / del /
connect / disconnect and every device type's parameters).

## 8. Verification plan

- Unit: argv-builder tables for every device type and operation, cross-
  field rejection cases, validation rejections (bounds, enums, patterns,
  MAC/IP, position/iface combos), confirm gate, at-least-one, ref
  validation, `ErrNotEnabled`, cache invalidation.
- In-memory protocol tests per tool (fleet/VM-mode target resolution,
  nil-request handlers, `applied` contents, exact argv, schema enums).
- Drift guard: all three registered in `RegisterSteps`.
- Policy: class assertions; read-only hides all three.
- e2e: the three names appear in fleet- and VM-mode `tools/list`.
- Live QA (disposable VM, everything reverted, final `Hardware` diffed
  against the pre-QA snapshot): hdd expand auto-named add → del
  `detach_only` (image file preserved) → explicit-`/tmp`-image add → del
  `destroy_image` with confirm (file gone) → re-add → destroy without
  confirm (gate rejects, file kept) → cleanup; cdrom add **with** iso →
  del + cdrom-without-media rejected pre-exec; net shared add →
  `set net_type=host-only` (verified in `Hardware`) → del; fdd add
  rejected (schema enum); serial add with output file → del; usb
  `set enabled=false/true` → `del usb` → re-`add usb`; running-VM net
  `connected=false/true` (hot connect/disconnect); negatives: `size_mb`
  on net, `ide` position 5, set-with-nothing, `size_mb` on `net0`
  devices, `destroy_image` without confirm, sound one-side, both toggles.
  `make check` + `make test-e2e` green.

## 9. Results

Implemented on 2026-10-12 (prlctl 27.0.1, macOS host). `make check` (fmt,
vet, golangci-lint, `go test -race ./...`) and `make test-e2e` green.

Live QA on the disposable `empty_catalog_test` (ubuntu, stopped): **40/40
steps passed**, including the hot connect/disconnect cycle on a running
VM, and the final `Hardware` object is byte-identical to the pre-QA
snapshot (full revert). All added devices were removed and all `/tmp` QA
files deleted.

Live findings that shaped the design (all reflected in §2/§3/§7):

- `--device-add cdrom` without media is rejected by prlctl → cdrom add now
  requires `image` or `host_device` in the service (C3).
- `--device-add fdd` and `--device-set fddN` are rejected by prlctl 27 at
  the parameter level; `--device-del fddN` is recognized → fdd removed
  from the add `type` enum (schema-level rejection), kept name-based in
  set/del (E1).
- `--device-set usb --disable/--enable/--connect` works → bare `usb`
  accepted by `device_set` with an empty configuration-field table
  (G1–G4).
- prlctl stores a `host-only` adapter as `type: "host"` in its JSON output
  (the tool sends `--type host-only` per the docs; D2/D2b).
- Hot `--connect`/`--disconnect` of a net adapter on a running VM is
  accepted (H3/H4).
- The cdrom-on-macOS-VM negative was not run (no macOS VM on this host);
  prlctl remains the authority and its error would surface verbatim.
