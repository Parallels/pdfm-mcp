# QA 006 — Parallels VM Keyboard Input

**Scope:** `parallels_vm_send_key` (single events + sequences) and
`parallels_vm_type` (ASCII text), implemented per
[spec 006](../specs/006-parallels-vm-keyboard.md).

**Safety note:** these tools drive the guest's keyboard — keys land in
whatever window/field is focused inside the guest. Before running any
step, make sure the target VM's focused window tolerates stray input, and
prefer a scratch VM. A failed call is still "just" prlctl error text.

## Prerequisites

- Parallels Desktop installed, `prlctl` on PATH or in a fallback dir.
- At least one **running** VM for the positive steps; one stopped VM for
  the failure step.
- `make build` (produces `bin/pdfm-mcp`) and `make check` passing.

## Steps

### 1. Automated suites

```
make check
make test-e2e
```

Expect: lint + vet clean, all unit/protocol tests green, e2e green with
both keyboard tools listed in fleet and VM mode. No key event is ever sent
by the default e2e run.

### 2. Opt-in e2e (single no-op key)

Pick a running VM (e.g. `capsule_manager_backend`) and run:

```
PDFM_MCP_E2E_KEY_VM=<vm> go test -tags e2e -count=1 -run TestParallelsGating ./test/e2e/
```

Expect: pass — one `escape` key event is sent, result `events: 1`. (If the
guest cannot receive key input — e.g. headless with no display session —
the opt-in will fail with prlctl's error; that is the documented
environmental outcome, see spec 006 §12.)

### 3. Wire-level walkthrough (MCP Inspector or stdio)

Connect with `bin/pdfm-mcp` (fleet) and check:

1. `tools/list` shows `parallels_vm_send_key` and `parallels_vm_type`;
   fleet `required` contains `vm_id` (+ `text` for type); VM-mode
   connection (`--vm-id <vm>`) does not.
2. `send_key` single tap: `{"vm_id": "<vm>", "key": "enter"}` →
   structured `events: 1`; watch the guest: the active window receives an
   Enter.
3. `send_key` by scancode: `{"vm_id": "<vm>", "scancode": 48}` → works
   (scancode space is the guest's scan code, not the PRL_KEY code).
4. Modifier combo (doc example shape):
   `{"vm_id": "<vm>", "events": [{"key": "left_win", "event": "press", "delay": 100}, {"key": "print", "delay": 100}, {"key": "left_win", "event": "release"}]}`
   → Windows guest: Win+PrtScr screenshot behaviour.
5. Pause event: `{"vm_id": "<vm>", "events": [{"key": "a"}, {"delay": 500}, {"key": "b"}]}`
   → visible ~500 ms gap between a and b.
6. Alias fields: `{"vm_id": "<vm>", "events": [{"k": "a", "d": 100}]}`
   → accepted (prlctl JSON compatibility).
7. `type_text`: focus a text field (e.g. a terminal or Notes) and call
   `{"vm_id": "<vm>", "text": "hello World?\n"}` → types
   `hello World?` + Enter with correct capitals/punctuation.
8. `type_text` with `delay_ms: 100` → visibly paced typing.

### 4. Error paths (no guest input expected)

1. `{"vm_id": "<vm>"}` (no key/scancode/events) → error
   "provide a key, a scancode, or an events sequence".
2. `{"vm_id": "<vm>", "key": "nope"}` → "unknown key name".
3. `{"vm_id": "<vm>", "key": "a", "scancode": 7}` → "not both".
4. `{"vm_id": "<vm>", "key": "a", "events": [{"key": "b"}]}` → "not both".
5. `{"vm_id": "<vm>", "events": [{}, {"delay": 0}]}` → "needs a key, a
   scancode or a delay" / "at least one".
6. `{"vm_id": "<vm>", "events": [{"key": 256}]}` → "out of range".
7. `{"vm_id": "<vm>", "events": [{"key": "a", "event": "hold"}]}` →
   "unknown event".
8. `type_text` with `{"text": "café"}` → "cannot type U+00E9"; with
   `{"text": ""}` → "text must not be empty"; 4097 chars → "limit".
9. `type_text` with `delay_ms: 5001` → "delay_ms must be between 0 and
   5000".

### 5. Failure paths (VM state unchanged)

prlctl-level failures (unlike exec) are **tool errors**:

1. Stopped VM: `{"vm_id": "<stopped-vm>", "key": "a"}` → error carrying
   prlctl's stderr (e.g. `PrlVm_TerminalConnect … PRL_ERR_IO_STOPPED` or a
   display-connection error — record the exact text).
2. Unknown VM: `{"vm_id": "no-such-vm", "key": "a"}` → prlctl
   "not found"-style error.

### 6. Mode contract + cache

1. VM-mode connection: `send_key`/`type` work without `vm_id`; an explicit
   `vm_id` is rejected ("vm_id must be omitted").
2. `list_vms` immediately after a send → `source: "cache"` (send-key does
   not invalidate the cache).

## Checklist

- [ ] `make check` and `make test-e2e` green
- [ ] opt-in e2e single escape key passes (or documented env failure)
- [ ] both tools listed in fleet + VM mode with correct `required`
- [ ] single tap verified on screen
- [ ] scancode form works
- [ ] modifier combo (Win+PrtScr shape) works
- [ ] pause event produces the gap
- [ ] alias fields k/s/d accepted
- [ ] `type_text` produces correct text incl. Shift + punctuation
- [ ] `delay_ms` paces typing
- [ ] all section 4 validation errors (no key sent)
- [ ] stopped VM / unknown VM → tool error with prlctl stderr
- [ ] VM-mode contract + cache untouched

## Follow-ups

- Mouse input (click/move/drag) — separate spec.
- Unicode/IME text entry (no prlctl key mapping; would need guest-side
  clipboard paste — separate spec).
