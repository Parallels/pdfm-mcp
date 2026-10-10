# QA 017 - Screen Wait Primitives

**Scope:** `wait.until` (`settled`, `changed`) in `parallels_vm_sequence`, per [spec 017](../specs/017-screen-wait-primitives.md).

**Safety note:** all scenarios below are read-only (capture polling) except one
key press at the VM firmware boot menu (moves a highlight, selects nothing).
Ran on the scratch VM `empty_catalog_test`, which was stopped and re-stopped
around the run.

**Executed 2026-10-09** (Parallels 27.0.1, macOS, scratch VM parked at its
firmware boot manager — a fully static screen with no guest OS).

## Prerequisites

- Parallels Desktop, `prlctl` on PATH, one running VM with a display session. ✅
- `make check` green. ✅

## Steps

### 1. Automated suites — ✅ PASSED

`make check` green. `go test -race ./internal/orchestrator/... ./internal/tools/...`
green, including `TestRunnerWaitUntil*`, `TestValidateWaitUntil*`,
`TestSequenceWaitUntil*` (fake-capture protocol tests: settled met/unmet,
changed with/without `from`, validation matrix, budget pre-check, ctx
cancellation, output shape `{until, met, polls, elapsed_ms,
last_changed_percent}`) and the description size guard.

### 2. Idle screen, settled — ✅ PASSED (live, opt-in e2e)

New opt-in e2e section in `test/e2e/stdio_test.go` (gate
`PDFM_MCP_E2E_WAIT_VM=<vm>`, fleet session). Result:

```
settled on empty_catalog_test: met=true polls=3 elapsed_ms=1849
```

`completed`, `met: true`, `polls: 3`, zero image blocks, `image_count: 0` —
exactly as the spec predicts (seed + 2 stable polls).

### 3. Never-settling wait, timeout — ✅ PASSED (live; static screen variant)

The scratch VM has no guest OS, so instead of an animating desktop the test
uses the inverse case on the same static screen, which exercises the same
deadline path: `{"until":"changed","timeout_seconds":3}` (no `from`).

```
changed on empty_catalog_test: met=false polls=5 elapsed_ms=3265
```

Result `status: stopped`, step `status: expectation_failed`, `met: false`,
detail `until changed not met after 5 polls in 3265ms (last change 0.00%)`,
`stop_reason: "step chg: wait until changed timed out after 3s"`. The
timeout is a **result**, not a tool error (consistent with exec non-zero
exits). `on_fail: continue` → `completed_with_failures` is covered by the
protocol tests.

### 4. Changed — ✅ PASSED (live, real change)

Two live shapes:

- **Without `from`:** the start-baseline timeout above.
- **With `from` and a real change:** `capture "before"` → `send_key down`
  (moves the boot-menu highlight) → `wait {until: "changed", from: "before"}`:
  `status: completed`, i.e. `met: true` on the first poll after the key
  press. The baseline downscale path (full-size capture step vs default
  512-px polls) worked — no false size-mismatch alarm.

```
changed from pre-key capture: status=completed
```

### 5. Tuning — ⏸ PARTIAL (no guest OS on the scratch VM)

The scratch VM boots to the firmware boot manager: no blinking cursor, no
menu-bar clock, no toasts to observe. What was observed with the defaults
(`tolerance: 8`, `threshold_percent: 0.1`): a perfectly static firmware
screen settles in 3 polls (~1.9 s, ~500 ms per poll); a single highlight
move is a definite "changed" (first poll); an unchanged screen reads
`last_changed_percent: 0.00%` at timeout. Cursor/clock/toast behaviour still
needs a guest-OS session — record values when the first real desktop is
driven.

### 6. Validation and policy — ✅ PASSED

- Validation matrix (seconds+until, neither, bad bounds, unknown `from`,
  `from` on `settled`, `stable_polls` on `changed`, budget pre-check): all
  covered by the protocol/unit suites (step 1); live spot check confirmed
  nothing runs on a rejected call.
- **Policy live check** (server built with
  `PDFM_MCP_DENY_TOOLS=parallels_vm_capture`, JSON-RPC over stdio):
  - `wait.until` → `invalid sequence: step 0 (s1): wait until needs the
    parallels_vm_capture tool, which is not available (disabled by policy?)`
  - `wait.seconds: 1` → `status: completed` (seconds waits unaffected).

### 7. Cancellation and timing — ✅ PASSED

Client cancels a `changed` wait (60 s timeout) after 4 s:

```
canceled mid-wait after 4.000384333s: context canceled
```

- Return is prompt (≈ the cancel point, not the timeout).
- No stray `prlctl capture` processes after cancel (`pgrep -f`).
- No stray `pdfm-mcp-capture` temp files in `$TMPDIR`.
- Real capture latency measured via the polls: ~500 ms per poll cycle at
  `poll_ms: 500` (capture itself is sub-second on this hardware).

## Deviations observed

- Live "animating VM, settled timeout" (step 3 as written) needs a guest OS
  with motion; the scratch VM has none, so the same deadline path was
  verified the other way (`changed` timing out on a static screen). No code
  difference; the unit tests cover `settled`-timeout and `on_fail: continue`
  with fakes.
- An opt-in e2e scenario was added (`PDFM_MCP_E2E_WAIT_VM`) so steps 2, 3, 4
  and 7 are repeatable; it also covers a real `changed`-from-`from` via one
  boot-menu key press (safe: moves a highlight, selects nothing).

## Result

All local and live-possible steps pass: automated suites (incl. race),
settled/changed met paths, timeout path, real-change path, policy
interaction, and cancellation with no stray processes or temp files.
Step 5 (guest-OS tuning) is deferred to a VM with a desktop session.
