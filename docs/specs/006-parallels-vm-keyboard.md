# Spec 006: Parallels VM Keyboard Input

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers |
| Scope | `parallels_vm_send_key` (raw event sequences) and `parallels_vm_type` (type ASCII text) tools; `SendKey` on the Parallels service; `RunStdin` runner seam; `internal/parallels/keys` key table + text mapper; tests; QA doc |
| Out of scope | Mouse input, screen-coordinate input, Unicode/IME typing (no key mapping — rejected), Windows-specific key semantics beyond the shared PRL_KEY table — follow-up specs |

## 1. Goal

Let an AI client **drive the VM's keyboard**. The docs describe two prlctl
forms:

```
prlctl send-key-event <vm> -k <key> | -s <scancode> [-e press|release] [-d <msec>]
prlctl send-key-event <vm> -j          # JSON array of events on stdin
```

The JSON form is a **superset**: a single tap is a one-element sequence,
modifier combos are press/key/release sequences, and `{"delay": N}` entries
are pauses. Both tools therefore use ONE service path: build a validated
event array, serialize it, and feed it to `prlctl send-key-event <vm> -j`
via stdin.

### Tools

1. **`parallels_vm_send_key`** — low level. Accepts either a single event
   (`key` name or `scancode`, optional `event` press/release, optional
   `delay`) or a full `events` sequence (the documented JSON format,
   including the short alias keys `k`/`s`/`d` and bare `{"delay": N}`
   pauses). Key names come from the PRL_KEY table (section 3).
2. **`parallels_vm_type`** — high level. `text` → key events with
   automatic Shift handling for uppercase and shifted punctuation,
   `\n` → Enter, `\t` → Tab, optional per-event `delay_ms`.

### Acceptance criteria

1. `make check` and `make test-e2e` pass; no key event is ever sent in CI.
2. Both tools listed in fleet and VM mode; mode contract identical to the
   other VM-scoped tools (fleet: `vm_id` required; VM mode: configured
   target, explicit `vm_id` rejected).
3. A call serializes the events to the documented JSON format and runs
   `prlctl send-key-event <vm> -j` with the JSON on **stdin** (never argv);
   on prlctl failure the tool error carries prlctl's stderr.
4. Key names resolve through the full PRL_KEY table (prefixed or bare,
   case-insensitive); unknown names are rejected before any exec.
5. `type` maps the documented ASCII set and rejects unmappable characters
   with the rune named; Shift is held across consecutive shifted
   characters (one press/release pair per run, not per character).
6. Every event is validated (key 1–255 or scancode 0–255, event
   press/release, delay 0–60000 ms; ≤ 20000 events; total delay ≤ 300 s)
   before any exec.
7. Every step in `docs/qa/006-parallels-vm-keyboard-QA.md` passes.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Wire form | Always `-j` + JSON on stdin; the single-event tools wrap into a one-element array | One code path; the `-k` single form is semantically a tap = `{"key": N}` |
| Event default | Omitted `event` = **tap** (press+release), matching prlctl's documented single-form behaviour and the Win+PrtScr doc example | Documented behaviour |
| Key identity | `key` = name (`enter`, `f5`, `left_shift`, `PRL_KEY_ENTER`, …) or decimal string; `scancode` = int (0–255); exactly one per event, or neither (delay-only pause) | The PRL_KEY table is the stable public reference; raw scancodes escape hatch |
| Key table | Full table from the Parallels CLI reference, embedded as a static Go map in `internal/parallels/keys`; lookup accepts `PRL_KEY_*` and bare short names, case-insensitive | Fixed upstream table; no runtime source |
| `type` character set | ASCII letters, digits, space, Tab, Enter (LF/CR), and QWERTY punctuation (both shift states); anything else → tool error naming the first unmappable rune | No IME/Unicode path in prlctl; fail loud instead of typing garbage |
| Shift handling in `type` | `left_shift` press → taps → release, held across consecutive shifted characters | Fewer events, matches real typing |
| No guest login needed | `send-key-event` takes no user/password: it injects into the VM's display, not a terminal session | Per the CLI reference |
| Timeout / bounds | 300 s timeout; per-event delay ≤ 60000 ms; ≤ 20000 events (≈ worst case for a 4096-char `type` with alternating Shift); total delay ≤ 300 s | Same fence as exec; bounds keep stdin small and total time predictable |
| Failure semantics | prlctl non-zero exit → **tool error** with its stderr (unlike exec: there is no guest exit code here — the "command" is prlctl's own) | Distinct from spec 005's exit-code-as-result rule |
| Cache | SendKey does not touch the VM list cache | No VM state change |
| Annotations | `ReadOnlyHint: false`, `DestructiveHint: true`, `OpenWorldHint: false` | Keys can type anything into anything, including power keys |
| Logging | Log VM ref, event count, duration. Never log the event content beyond counts (typed text can contain secrets) | Same rule as exec's command |

## 3. Key table

The full PRL_KEY table (1…192, with the upstream gaps) is embedded in
`internal/parallels/keys/keys.go` as `var codes = map[string]int{…}` keyed
by the lowercased short name (e.g. `"enter": 36`, `"f5": 71`, `"left_win":
115`, `"np_star": 63`, `"www_home": 130`). `CodeFor(name string) (int,
error)` normalizes: trim, lowercase, strip an optional `prl_key_` prefix,
look up; unknown → error `unknown key name %q`. Numeric strings
(`"36"`) resolve to the code directly.

## 4. Repository layout (delta)

```
pdfm-mcp/
├── internal/
│   ├── parallels/
│   │   ├── keys/
│   │   │   ├── keys.go                   # NEW — table, CodeFor, EventsForText
│   │   │   └── keys_test.go
│   │   ├── interfaces/interfaces.go      # + KeyEvent, + SendKey on ParallelsService
│   │   └── service/
│   │       ├── exec.go                   # + RunStdin on CommandRunner + realRunner
│   │       ├── vm_sendkey.go             # NEW — SendKey + event validation + serialization
│   │       ├── vm_sendkey_test.go        # NEW
│   │       └── service_test.go           # fakeRunner: + RunStdin, + lastStdin
│   ├── tools/
│   │   ├── parallels_vm_send_key.go      # NEW — low-level tool + handler + Register
│   │   ├── parallels_vm_type.go          # NEW — type-text tool + handler + Register
│   │   ├── parallels_sendkey_test.go     # NEW — unit + protocol tests for both
│   │   └── parallels_test.go             # testRunner: + RunStdin
│   └── server/server.go                  # + both registrations
├── test/e2e/stdio_test.go                # + listed assertions (+ opt-in single event)
└── docs/
    ├── specs/006-parallels-vm-keyboard.md
    └── qa/006-parallels-vm-keyboard-QA.md
```

No new dependencies.

## 5. Service layer

### 5.1 Interface

```go
// KeyEvent is one validated keyboard event in the wire form. Key (1–255)
// and Scancode (0–255) are mutually exclusive; both nil means a pure
// delay pause. Event is "" (tap), "press" or "release".
type KeyEvent struct {
	Key      *int   `json:"key,omitempty"`
	Scancode *int   `json:"scancode,omitempty"`
	Event    string `json:"event,omitempty"`
	Delay    *int   `json:"delay,omitempty"`
}

// added to ParallelsService:
//
// SendKey sends a sequence of keyboard events to the VM identified by
// vmRef (id or name, pre-validated) by running
// `prlctl send-key-event <vm> -j` with the serialized events on stdin.
// prlctl failures are errors; there is no guest exit-code concept here.
// SendKey does not touch the VM list cache.
SendKey(ctx context.Context, vmRef string, events []KeyEvent) (string, error)
```

### 5.2 Runner seam

`CommandRunner` gains:

```go
// RunStdin is like RunCapture but also feeds stdin to the child.
RunStdin(ctx context.Context, exe, stdin string, args ...string) (string, string, int, error)
```

`realRunner` mirrors `RunCapture` with `cmd.Stdin = strings.NewReader(stdin)`.
Fakes record `lastStdin` for assertions.

### 5.3 `service/vm_sendkey.go`

Constants: `sendKeyTimeout = 300s`, `maxKey = 255`, `maxScancode = 255`,
`maxEventDelay = 60000`, `maxEvents = 20000`, `maxTotalDelayMS = 300000`.

`(*service).SendKey`:

1. `enabled` check → `ErrNotEnabled`.
2. `ValidateVMRef(vmRef)`.
3. Validate: `0 < len(events) ≤ maxEvents`; per event — at most one of
   Key/Scancode, at least one of Key/Scancode/Delay, Key in 1–255, Scancode
   in 0–255, Event in {"", "press", "release"}, Delay in 0–60000; total
   delay ≤ maxTotalDelayMS.
4. `json.Marshal(events)` (the wire form — field names match prlctl's
   documented JSON exactly).
5. `context.WithTimeout(ctx, sendKeyTimeout)` →
   `s.runner.RunStdin(runCtx, s.prlctlPath, string(data), "send-key-event", ref, "-j")`.
6. `err != nil` or `exitCode != 0` → log `vm` + event count (never the
   event content) and return an error carrying prlctl's stderr.
7. Success → return prlctl's stdout (usually empty).

## 6. Tool layer

### 6.1 `parallels_vm_send_key`

```go
type KeyEventArg struct {
	Key      *string `json:"key,omitempty"      jsonschema:"key name (enter, f5, left_shift, a, PRL_KEY_ENTER, …) or numeric code"`
	K        *string `json:"k,omitempty"`        // prlctl JSON alias, kept for compatibility
	Scancode *int    `json:"scancode,omitempty" jsonschema:"keyboard scan code (0-255); alternative to key"`
	S        *int    `json:"s,omitempty"`        // prlctl JSON alias
	Event    string  `json:"event,omitempty"    jsonschema:"press or release; omit for a tap (press+release)"`
	Delay    *int    `json:"delay,omitempty"    jsonschema:"delay in milliseconds before this event (0-60000)"`
	D        *int    `json:"d,omitempty"`        // prlctl JSON alias
}

type SendKeyInput struct {
	VMID     string        `json:"vm_id,omitempty" jsonschema:"VM id or name to send the key event to"`
	Key      string        `json:"key,omitempty"    jsonschema:"single key: name (enter, f5, a, …) or numeric code; omit when using events"`
	Scancode *int          `json:"scancode,omitempty" jsonschema:"single key by scan code (0-255); alternative to key"`
	Event    string        `json:"event,omitempty"  jsonschema:"press or release; omit for a tap"`
	Delay    *int          `json:"delay,omitempty"  jsonschema:"delay in milliseconds before the key (0-60000)"`
	Events   []KeyEventArg `json:"events,omitempty"  jsonschema:"full event sequence; takes precedence; each entry: {key|scancode, event, delay} or {delay} for a pause"`
}

type SendKeyOutput struct {
	VMID   string `json:"vm_id" jsonschema:"the VM id or name the events were sent to"`
	Events int    `json:"events" jsonschema:"number of events sent"`
	Output string `json:"output" jsonschema:"prlctl stdout (usually empty)"`
}
```

Handler rules: `events` and single-form (`key`/`scancode`) are mutually
exclusive (error if both); single form requires exactly one of key/scancode;
names resolve via `keys.CodeFor`; build `[]interfaces.KeyEvent`; call
`svc.SendKey`.

### 6.2 `parallels_vm_type`

```go
type TypeTextInput struct {
	VMID    string `json:"vm_id,omitempty" jsonschema:"VM id or name to type into"`
	Text    string `json:"text" jsonschema:"ASCII text to type (letters, digits, space, punctuation; \\n = Enter, \\t = Tab); max 4096 chars"`
	DelayMS int    `json:"delay_ms,omitempty" jsonschema:"delay in ms between key events (0-5000, default 0)"`
}

type TypeTextOutput struct {
	VMID   string `json:"vm_id" jsonschema:"the VM id or name the text was typed into"`
	Chars  int    `json:"chars" jsonschema:"number of characters typed"`
	Events int    `json:"events" jsonschema:"number of key events sent"`
	Output string `json:"output" jsonschema:"prlctl stdout (usually empty)"`
}
```

`keys.EventsForText(text string, delayMS int) ([]interfaces.KeyEvent,
error)` maps the documented ASCII set (section 2) with Shift held across
consecutive shifted characters; `delayMS` is applied to every generated
event. Unmappable rune → error `cannot type U+XXXX %q`.

### 6.3 Registration

Both register with the mode-aware `powerDescription`/`powerInputSchema`
helpers (fleet adds `vm_id` to required), `DestructiveHint: true`,
`outputSchemaFor` for the outputs. In `server.New`, after the exec tool.

## 7. Security

- **No host shell.** The VM ref is validated; the event JSON is built from
  validated integers/enum strings and passed on stdin — tool arguments
  never become argv or shell text.
- **Key names resolve against a fixed table** — no injection surface.
- Bounds: ≤ 20000 events, per-event delay ≤ 60 s, total ≤ 300 s, stdin
  stays well under the 10 MB runner cap.
- **Typed text can contain secrets** — it reaches the guest and its model
  transcript, and is never logged (counts only).
- DestructiveHint: keys can invoke power keys, type destructive commands,
  etc.

## 8. Edge cases and invariants

| Case | Behaviour |
|---|---|
| `events` + `key`/`scancode` together | Tool error before any exec |
| Neither key nor scancode (single form) | Tool error |
| Unknown key name | Tool error naming it; no exec |
| key 0 or > 255, scancode > 255 | Tool error |
| Event not press/release | Tool error |
| Negative or > 60000 delay, > 20000 events, total delay > 300 s | Tool error |
| `{"delay": 100}` pause-only event | Valid |
| `type` with non-ASCII / unmappable rune | Tool error naming the rune; nothing sent |
| `type` empty text | Tool error (0 events is invalid) |
| VM stopped / unknown / no display | prlctl error surfaced as tool error |
| prlctl times out at 300 s | Tool error "(command timed out)" |
| Client cancels | Context propagates, tool error |
| Cache | Untouched |
| stdout | Untouched: runner buffers, nothing prints |

## 9. Testing

1. **`keys_test.go`**: `CodeFor` for prefixed/bare/case variants, digits,
   unknown name, every table entry resolves to a distinct documented code
   (spot-check the doc's "test"+Enter example: T=28, E=26, S=39, T=28,
   Enter=36). `EventsForText`: "aB\n" → correct sequence with one Shift
   press/release pair; punctuation shift states (incl. `!` … `)`);
   rejected rune named; delay applied to every event.
2. **`service/vm_sendkey_test.go`**: exact argv `send-key-event <vm> -j`
   with the JSON on `lastStdin` (byte-for-byte against expected, including
   field order of the serialized struct); validation table (0 events, 10001
   events, key 0/256, scancode 256, bad event, negative delay, 60001 delay, 20001 events, total delay overflow); runner error → error with stderr;
   non-zero exit → error; disabled → `ErrNotEnabled`; invalid ref → no
   exec; 300 s deadline applied; cache untouched.
3. **`tools/parallels_sendkey_test.go`**: both modes (four cases each);
   single form key name / scancode / event / delay → exact events; alias
   fields `k`/`s`/`d` accepted; events+key conflict; protocol test: both
   listed, fleet required contains `vm_id` (send_key has no other
   required field; type has `text`), call with `{"key":"enter"}` →
   non-error result with `events: 1`; `type` with "hi\n" → `events: 3`
   (h, i, enter taps) and `chars: 3`, stdin byte-for-byte
   `[{"key":43},{"key":31},{"key":36}]`.
4. **e2e**: listed in both modes; opt-in `PDFM_MCP_E2E_KEY_VM` sends a
   single `{"key": "escape"}` (no-op in normal contexts) and asserts a
   non-error result. Never default-on.

## 10. QA (manual, real machine)

`docs/qa/006-parallels-vm-keyboard-QA.md`: send_key single tap (verify on
screen), type_text into a visible text field or terminal, modifier combo
(verified against the doc example shape), pause event, error paths
(stopped VM, unknown VM, unknown key), alias fields, fleet/VM contract,
cache untouched.

## 11. Implementation order

1. This spec + index row.
2. `internal/parallels/keys` + tests.
3. `RunStdin` on `CommandRunner` + realRunner + fakes.
4. `interfaces.KeyEvent` + `SendKey`; `service/vm_sendkey.go` + tests.
5. Both tools + tests.
6. `server.New` registrations.
7. e2e listing + opt-in.
8. QA doc + README.
9. `make check`, `make test-e2e`, manual QA.

## 12. Known unknowns / verification notes

- **Resolved (verified on Parallels 27.0.1):** `send-key-event` works
  against the headless running VM — no user session or Parallels Tools
  authentication needed (unlike `prlctl exec`). A live Escape key and a
  live 2-character `type_text` both returned success.
- **Resolved:** stopped-VM error is prlctl exit 255 with stderr
  `Failed to connect to VM: PrlJob_Wait: PRL_ERR_IO_STOPPED`; unknown VM
  → `Failed to get VM config: The virtual machine could not be found…`
  (both surfaced as tool errors).

### Deviations (fill in during implementation)

- **Plain values, not pointers, in the tool input structs.** The spec's
  section 6 showed `*int` fields (`Scancode`, `Delay`, …) so 0 vs absent
  could be told apart; pointer fields make `jsonschema.For` emit
  `["integer","null"]` unions in the input schema — the portability
  warnings removed in spec 004. The implementation uses plain
  `int`/`string` with `omitempty` and treats 0/"" as "not provided"
  (scancode 0 is not a meaningful USB scan code). Alias conflict checks
  (key vs k, etc.) are done in the handler.
- Otherwise as specified; `maxEvents` was raised to 20000 before
  implementation (4096-char worst-case typing needs ~12288 events).
