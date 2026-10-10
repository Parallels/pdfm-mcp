# QA 007 — Parallels VM Sequence Orchestration

**Scope:** `parallels_vm_sequence`, implemented per
[spec 007](../specs/007-parallels-vm-sequence.md).

**Safety note:** a sequence can chain **destructive** steps
(`parallels_vm_delete`, `parallels_vm_reset`, `parallels_vm_stop --kill`,
arbitrary `exec` commands, keyboard input). Read the full step list before
confirming a destructive call, and prefer a scratch VM for anything beyond
captures. Read-only steps: `parallels_vm_capture` only.

## Prerequisites

- Parallels Desktop installed, `prlctl` on PATH or in a fallback dir.
- At least one **running** VM for the capture steps (used here:
  `capsule_manager_backend`, a headless infra VM — its screen is static,
  which conveniently exercises the "unchanged" branch).
- `make build` (produces `bin/pdfm-mcp`) and `make check` passing.

## Steps

### 1. Automated suites

```
make check
make test-e2e
```

Expect: lint + vet clean, all unit/protocol tests green, e2e green with
`parallels_vm_sequence` listed in fleet **and** VM mode. No step is ever
executed by the default e2e run.

### 2. Opt-in e2e (read-only capture sequence)

Pick a running VM and run:

```
PDFM_MCP_E2E_SEQ_VM=<vm> go test -tags e2e -count=1 -run TestParallelsGating ./test/e2e/
```

Expect: pass — `capture → wait 1 s → capture` returns
`status: "completed"`, `steps_run: 3`, `image_count: 2`, two decodable PNG
image blocks after the text block.

### 3. Wire-level walkthrough (stdio driver)

Verified 2026-10-09 on Parallels 27.0.1 against `capsule_manager_backend`.

Fleet mode (`bin/pdfm-mcp`):

1. `tools/list`: 17 tools including `parallels_vm_sequence`;
   `required: ["steps","vm_id"]`; annotations
   `{destructive: true, idempotent: false, openWorld: true, readOnly: false}`;
   generated description **1293 bytes** (< 3 KB) with the `name(arg, arg?)`
   signatures of all 14 step tools, **no** `vm_id` in any signature.
2. `validate_only` (capture / wait 5 s / exec): `status: "validated"`,
   zero prlctl invocations beyond the cache warm-up.
3. Live read-only sequence (capture, wait 2 s, capture) with
   `_meta.progressToken: "qa-prog-1"`: `status: "completed"`,
   `steps_run: 3`, `image_count: 2`, `duration_ms ≈ 2600`; **three
   progress notifications** arrived (`step 1/3: parallels_vm_capture`,
   `step 2/3: wait`, `step 3/3: parallels_vm_capture`) carrying the token,
   `progress`/`total` counters and no step arguments.
4. **Unchanged-screen branch** (capture, wait 1 s, capture with
   `expect: image_changed from "before"`, then exec): the idle VM's screen
   is static, so `status: "stopped"`, `stopped_at: 2`, the exec step is
   `not_run`, `stop_reason: step after: expect check "image_changed"
   failed`, and both images still come back. To see the **changed** branch
   live, run the same sequence while something animates on the VM (video,
   cursor movement); with the defaults (`tolerance 8`,
   `threshold_percent 0.1`) a blinking cursor or a clock tick is enough to
   cross the threshold on a 1568 px downscale.
5. **Validation errors reported together**: one call containing an unknown
   tool, a step-level `vm_id`, an unterminated regex in `expect`, an
   out-of-range `retry`, and a forward reference returned every error in a
   single tool error, each prefixed `step N (id):` — and executed nothing.
   51 steps in one call is rejected with `steps must have 1 to 50 entries`.
6. VM mode (`--vm-id <vm>`): tool listed with `required: ["steps"]` only;
   the same read-only sequence runs with no `vm_id`; an explicit
   top-level `vm_id` is rejected; `vm_id` inside any step's `args` is
   rejected in both modes.
7. **Concurrency**: two same-VM sequences issued together (each
   capture → wait 3 s → capture) finished at t≈4.0 s and t≈7.5 s —
   serialized by the per-VM lock, not interleaved.
8. **Temp dirs**: `ls "$TMPDIR" | grep pdfm-mcp-capture` is empty after all
   of the above (capture temp files are removed in defer, including on
   error paths).

### 4. Manual scenarios to try on a real workstation VM

(Not runnable headless; do these on a VM with a display session.)

1. **Motivating example, changed screen**: video playing or a cursor
   moving; expect `completed` with both images and the exec output.
2. **Retry-until**: poll `systemctl is-active <svc>` with
   `matches ^active` and `retry {max_attempts: 10, delay_ms: 2000}` after
   restarting the service; expect `attempts: N` in the result.
3. **`when` fallback**: an exec with `when: [{check: "exit_code", "step": "<prev>", "negate": true}]`
   that runs only when the previous command failed.
4. **`gate` stop**: a standalone `gate` step whose check fails mid-sequence;
   expect `status: "stopped"` and the later steps `not_run`.
5. **`on_error: continue`** against a stopped VM (start step errors, later
   steps keep running).
6. **Client behaviour**: a 60+ s sequence from Claude Desktop and Claude
   Code — record whether the per-request timeout is reset by the progress
   notifications (spec 007 §11/§15 unknown).
7. **Image display**: all returned images render in order; `image_index` in
   each capture result matches the position of its block in `content`.

## Deviations observed

- Progress notifications require the client to put the token in
  `params._meta.progressToken` (per the MCP spec); the SDK does not gate
  them on a capability, so any client that sends a token gets them.
- The generated description lists each step tool's arguments from its
  VM-mode input schema with `vm_id` omitted (the orchestrator owns the
  target; the validator rejects a step-level `vm_id`), matching the spec §7.3
  example shape.
