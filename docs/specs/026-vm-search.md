# Spec 026: parallels_search_vms (compact VM lookup)

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers, release owner |
| Scope | One new read-only, fleet-scoped tool: find VMs by substring of name/UUID (optional state filter) and return a **compact** summary instead of the full prlctl detail |
| Out of scope | Any change to `parallels_list_vms` behaviour (one cross-link sentence in its description only), new resolution semantics (spec 025 contract unchanged) |

## 1. Goal

Lower-capability models must be able to locate a VM without ingesting the
full VM list. A live session showed the failure: the model asked to suspend
a VM, called `parallels_list_vms`, and the full-detail output for all 31
VMs was truncated by the client; the model then flailed ("I need to read
the file… use glob… I don't have the full VM list") instead of acting.

`parallels_list_vms` stays the full-detail tool (`prlctl list -a -i`, every
field). The new `parallels_search_vms` answers "which VM is this?" with a
few lines of JSON that fit any client window.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Tool name | `parallels_search_vms` | Fleet-scoped like `parallels_list_vms`; **not** a `parallels_vm_*` tool, so it is (correctly) not a sequence step and passes the drift guard untouched |
| Input | `query` (required, case-insensitive substring of VM **Name** or **UUID**), `state` (optional, case-insensitive exact state match), `limit` (default 10, max 50), `refresh` (same semantics as `parallels_list_vms`) | One simple, predictable matching rule a small model can use on the first try; no regex (untrusted input, no DoS surface) |
| Ranking | exact Name match → exact UUID match → Name prefix → Name substring → UUID substring; ties by Name ascending | The model's target ("Ubuntu 26.04 PLAI") lands first without reading the list |
| Output | compact typed summary per VM: `id`, `name`, `state`, `os`, `ip` (first non-empty address, omitted when none) + `count`, `total` (VMs scanned), `fetched_at`, `source` | Bounded size (≤ `limit` × ~90 chars) — the truncation failure mode disappears; `total` tells the model the search covered the fleet |
| Data source | the same in-memory list cache as `parallels_list_vms` (no extra prlctl calls on cache hit); `--allow-vms` filtering identical (disallowed VMs never leave the server) | Consistency with the list tool; no new prlctl surface |
| Class | `ClassRead`, read-only annotation, registered in both modes (VM mode: the cache already restricts to the target VM) | No argument combination changes state |
| AI guidance | tool description: use it *instead of* `parallels_list_vms` to locate a VM; the returned `id` or `name` is the `vm_id` for every VM-scoped tool (spec 025 contract); `parallels_list_vms` description cross-links back | The small model must be steered to the cheap tool first |

## 3. Components

| Component | Change |
|---|---|
| `internal/tools/parallels_search_vms.go` | new: input/output types, handler (cache lookup + filter + rank + limit), `RegisterParallelsSearchVms` |
| `internal/server/server.go` | one `toolTable` row (both modes) |
| `internal/policy/policy.go` | `"parallels_search_vms": ClassRead` |
| `internal/tools/parallels_list_vms.go` | description cross-link to the search tool |
| `internal/tools/parallels_search_vms_test.go` | handler unit tests (match, rank, state filter, limit, cache reuse, VM mode, allow-list) + in-memory protocol test (schema, `query` required, structured output) |

## 4. Acceptance criteria

1. `parallels_search_vms` is listed in fleet and VM mode; `query` is
   required; the wire schema is portable (no multi-type arrays, no
   keyword-less nodes — the fleet protocol test's schema sweep covers it).
2. Substring match on Name or UUID, case-insensitive; `state` filters
   exactly (case-insensitive); ranking order as decided; `limit` clamped
   to 1..50 (default 10).
3. No extra prlctl call on a cache hit (`runner.count()` stays at the
   warm-up count); `refresh=true` forces one.
4. `--allow-vms`: disallowed VMs never appear in results.
5. `make check` and `make test-e2e` stay green.

## 5. QA

- `make check` (unit + lint, incl. the portable-schema sweep over all wire
  schemas, the toolTable/policy consistency tests, and the sequence drift
  guard) and `make test-e2e`: green.
- Live wire QA (2026-10-10, local Parallels Desktop, 31 VMs, 30 tools on
  the wire):
  - `query: "ubuntu 26.04 plai"` → exactly `[Ubuntu 26.04 PLAI, running]`,
    total scanned 31 — the truncation failure scenario from the issue now
    resolves in one compact call;
  - `query: "baseimage"` → 4 ranked matches (BaseImage family);
  - `query: "gh-runner" + state: running + limit: 3` → correctly 0 (those
    runners are stopped on this host; state filter works);
  - `parallels_vm_info` addressed with a name returned by the search → ID
    match (spec 025 contract closed loop).

## 6. Deviations

- (none recorded yet)
