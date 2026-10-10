# Spec 017: Screen Wait Primitives (`wait.until`)

| | |
|---|---|
| Status | Implemented (local + live verification complete 2026-10-09, scratch VM) |
| Audience | Implementing agents / engineers |
| Scope | Extend the `parallels_vm_sequence` `wait` step with `until: "settled"` and `until: "changed"`, implemented as `parallels_vm_capture` polling plus `imaging.Diff`; orchestrator types/validator/runner; generated tool description; tests; QA doc |
| Depends on | 004 (capture), 007 (sequence, `imaging.Diff`) |
| Out of scope | `wait until text` (arrives with the OCR spec 020), region-restricted diff, new `prlctl` calls, async jobs |

## 1. Goal

Replace guesswork pauses ("wait 5 s and hope the installer finished painting") with a bounded wait that returns as soon as the screen **stops changing** or **changes**, inside one sequence call and with no model round trip per poll.

```json
{"wait": {"until": "settled", "timeout_seconds": 10, "poll_ms": 500, "stable_polls": 2, "threshold_percent": 0.1, "tolerance": 8, "max_dimension": 512}}
{"wait": {"until": "changed", "from": "before", "timeout_seconds": 10}}
```

## 2. Acceptance criteria

1. `{"wait": {"seconds": N}}` behaves exactly as before.
2. A wait has exactly one of `seconds` or `until`; both or neither is a validation error. Until-only fields on a seconds wait are a validation error.
3. `settled` is met after `stable_polls` consecutive polls with no change versus the previous poll.
4. `changed` is met when a poll differs from the baseline: the image of the earlier capture step `from`, or, without `from`, a capture taken when the wait starts.
5. Unmet at timeout gives status `expectation_failed` with the step's `on_fail` semantics (stop by default, `continue` allowed). Met gives `ok`.
6. `StepResult.Output` carries `{until, met, polls, elapsed_ms, last_changed_percent}`; `waited_seconds` is kept (elapsed time).
7. Poll images are never returned as image blocks and never count toward `max_images`.
8. Validation errors: capture action not registered (policy may remove it), empty target, bad bounds, unknown / later / non-capture `from`, `from` on `settled`, `stable_polls` on `changed`.
9. The worst case (`timeout_seconds`) of every until-wait counts toward the sequence `timeout_seconds` budget pre-check.
10. `ctx` cancellation, the sequence timeout, the injected `Sleep` and `Now` are honored; capture errors follow `on_error`.

## 3. Decisions

- **Polling goes through the registered `parallels_vm_capture` action** (`Plan` holds the `Action` and final args `{"max_dimension": N}` plus `vm_id` in fleet mode, built with `buildArgs` exactly like tool steps). No new prlctl calls, no second capture implementation. It is not a visible step: no result entry, no progress message.
- **`settled` seeding.** The first poll only seeds the comparison, so `stable_polls: 2` takes at least 3 captures.
- **`polls` counts every capture taken**, including the seed (settled) and the start baseline (changed without `from`).
- **Baseline from `from`** is downscaled with `imaging.Downscale` to the wait's `max_dimension`, so a 1568 px capture step and 512 px polls compare at the same size (the capture tool downscales deterministically, same algorithm). Without this, a size mismatch would always read as "changed".
- **Retained image.** The final poll image (met: the deciding poll; unmet: the last poll) is stored in run state under the wait step's id, so a later `image_changed` check or a later `changed` wait can reference it. Judged useful (compare "before the install" with "after it settled") and cheap: one image per wait step at `max_dimension` (default 512), counted once against the 64 MB retained-image budget. `image_changed` validation accepts a wait-until step as `from`/`to`. It is not returned. Note that sizes differ from a full-size capture step (size mismatch counts as changed): use the same `max_dimension` on both, or reference only wait steps.
- **Deadline loop.** After each unmet poll: if `Now() - start >= timeout` stop; otherwise sleep `min(poll, remaining)` and poll again, so one last poll happens at the deadline. Deterministic with the instant fake `Sleep` that advances the fake clock.
- **Unmet reporting** adds one check entry `{phase: "expect", check: "wait_until", passed: false, detail}` and stop reason `step X: wait until settled timed out after 10s`.
- **Capture errors are not retried** inside the wait: status `error`, `on_error` semantics (stop by default). Retrying transient failures is a follow-up.
- `when` applies as to any step; `quiet` hides `output` (state still keeps it); `retry`/`expect` on a wait remain validation errors.

Bounds: `timeout_seconds` 0.5-120 (default 10), `poll_ms` 200-5000 (500), `stable_polls` 1-5 (2), `max_dimension` 256-4096 (512), `threshold_percent` 0-100 (0.1), `tolerance` 0-255 (8).

## 4. Layout delta

```
internal/orchestrator/types.go        WaitSpec extended; untilPlan; planStep.Until
internal/orchestrator/validate.go     validateUntil, hasUntilOnlyFields, budget, image_changed accepts wait-until
internal/orchestrator/runner.go       wait case dispatches to runWaitUntil
internal/orchestrator/waituntil.go    runWaitUntil (new)
internal/orchestrator/waituntil_test.go
internal/tools/parallels_vm_sequence.go   description
internal/tools/parallels_sequence_test.go protocol tests
docs/specs/017-screen-wait-primitives.md, docs/qa/017-screen-wait-primitives-QA.md
```

`WaitSpec.Seconds` gained `omitempty` (no longer a required schema property).

## 5. Design

`Validate` (wait step with `until`): check kind, bounds, `from` (earlier step that is a capture-producing tool or an earlier wait-until), capture action present, non-empty target, build and `ValidateArgs` the capture args, add `timeout_seconds` to the declared-wait budget. `runWaitUntil(ctx, runCtx, ...)` loops capture, diff, evaluate, sleep; error precedence mirrors tool steps: client cancellation returns the error, sequence timeout gives `not_run`/`timeout` and stops, otherwise a real error. Settled uses the diff percent versus the previous poll; `last_changed_percent` is the last comparison (0 when none was made). A `from` baseline of a different size after downscale is reported as changed immediately (`imaging.Diff` size mismatch).

## 6. Edge cases

- Animated cursor/clock: tune `tolerance`/`threshold_percent` or raise `stable_polls`; a screen that never settles times out as `expectation_failed`.
- `timeout_seconds` above the remaining sequence budget: the sequence timeout fires first (`status: timeout`).
- Baseline step errored/skipped (no image): the wait errors with `baseline step "x" has no image`.
- Capture action removed by policy: validation error naming the tool.
- VM mode: no `vm_id` injected, handler uses the configured VM.

## 7. Testing

Orchestrator (fakes, instant `Sleep`): validation table (both, neither, bad bounds, unknown/later/non-capture `from`, capture missing, empty target, until-only fields on seconds), defaults and vm_id injection, budget pre-check, settled (changes then stabilizes, polls=4, sleeps 3), settled timeout (polls=6, stopped, later step not_run), `on_fail: continue`, changed vs start baseline, changed vs `from` (+ gate reading the wait image), quiet/when, capture error under stop/continue, missing baseline, cancellation during capture and during sleep, no returned images. Protocol (in-memory MCP, fake runner): settled run returns only the text block and 4 captures; validation error runs nothing.

## 8. Implementation order

types, validate, waituntil + runner dispatch, orchestrator tests, description, protocol tests, QA doc.

## 9. Follow-ups

- `wait until text` (OCR match) arrives with spec 020.
- Retry transient capture errors inside a wait; region-restricted diff; `until: "changed"` returning the diff bounds.

## 10. Known unknowns

- Real-screen defaults (tolerance 8, threshold 0.1, 512 px, 500 ms) need tuning on Retina VMs (cursor blink, clocks, video).
- Cost of one prlctl capture per poll on a busy host; `poll_ms` 500 may be too aggressive.
- Generated description size with all steps registered (< 3072 bytes guard).

### Deviations

- The authoring sandbox had no Go toolchain; the code was verified here on the developer Mac (2026-10-09) with no changes needed to the wait-until implementation itself.
- Live verification used the scratch VM `empty_catalog_test` parked at its firmware boot manager (static screen, no guest OS): `settled` met in 3 polls; the deadline path was exercised as `changed` timing out on the static screen (a static VM cannot "animate" for a `settled` timeout); a real `changed` was produced with one boot-menu arrow key against a `from` baseline. Guest-OS tuning (blinking cursor, clock, toasts) deferred — see QA 017 step 5.
- Added an opt-in e2e scenario gate `PDFM_MCP_E2E_WAIT_VM` in `test/e2e/stdio_test.go` (settled met, changed timeout, changed-from-after-key, client cancellation with no stray `prlctl capture`).
