# Spec 007: Parallels VM Sequence Orchestration

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers |
| Scope | `parallels_vm_sequence` tool; new `internal/orchestrator` package (step registry, validator, runner, condition engine); `imaging.Diff`; adapter that turns any existing tool handler into a step type; tests; QA doc |
| Depends on | 002 to 005 (implemented). 006 (keyboard) is optional: if its tools exist they are registered as steps like any other, if not they simply are not available |
| Out of scope | Multi-VM sequences, nested sequences, loops/branching beyond `when`/`expect`/`gate`/`retry`, OR-conditions, async jobs with polling, region-based image diff, OCR conditions, persisting or replaying sequences |

## 1. Goal

Let the AI **build a whole automation session as one JSON document** and get the full result back in one call, instead of paying a model round trip per action. The server runs the steps itself, **calling the same Go code the standalone tools use** (no MCP round trip, no second implementation), and returns an ordered array of results, each tagged with the tool that produced it.

The orchestration adds three things on top of the tools:

1. **`wait`**: a pause between steps. It is not a tool, it is a control step.
2. **Logic gates** that short-circuit the run: a step can *skip itself* (`when`), *assert its own result* (`expect`), *retry until the assertion holds* (`retry`), or a standalone `gate` step can stop the run. Conditions are declarative JSON (regex on output, exit code, image changed or not), never code.
3. **Image diff** as a first-class condition, so "capture, wait 3 s, capture, only continue if the screen changed" costs one tool call and no wasted time.

### Example: the motivating case

> capture the screen, wait 3 s, capture again, if different run a command, otherwise stop

```json
{
  "vm_id": "ubuntu_github_builder",
  "steps": [
    { "id": "before", "tool": "parallels_vm_capture", "args": { "max_dimension": 1024 } },
    { "wait": { "seconds": 3 } },
    { "id": "after",  "tool": "parallels_vm_capture", "args": { "max_dimension": 1024 },
      "expect": [ { "check": "image_changed", "from": "before" } ] },
    { "tool": "parallels_vm_exec", "args": { "command": "journalctl -n 20 --no-pager" } }
  ]
}
```

Screen changed: all four steps run; the response has the two images plus the exec output.
Screen unchanged: step `after` reports `expectation_failed`, the run **stops there** (`status: "stopped"`), the exec never runs.

### More examples

Poll until a service is up (no fixed sleeps):

```json
{ "id": "ready", "tool": "parallels_vm_exec",
  "args": { "command": "systemctl is-active nginx" },
  "expect": [ { "check": "matches", "field": "stdout", "regex": "^active" } ],
  "retry": { "max_attempts": 10, "delay_ms": 2000 } }
```

Run a fallback only when the previous command failed:

```json
{ "tool": "parallels_vm_exec", "args": { "command": "sudo systemctl restart nginx" },
  "when": [ { "check": "exit_code", "step": "ready", "negate": true } ] }
```

Start a VM, wait for it, look at it (keyboard steps work the same once spec 006 exists):

```json
{ "steps": [
  { "tool": "parallels_vm_start" },
  { "wait": { "seconds": 30 } },
  { "tool": "parallels_vm_capture" },
  { "tool": "parallels_vm_type", "args": { "text": "ls -la\n" } },
  { "wait": { "seconds": 1 } },
  { "tool": "parallels_vm_capture" }
] }
```

### Acceptance criteria

1. `make check` and `make test-e2e` pass; CI never executes a real step.
2. `parallels_vm_sequence` is listed in fleet and VM mode whenever the module is enabled; absent otherwise. Mode contract: fleet requires top-level `vm_id`, VM mode uses the configured target and rejects an explicit one.
3. Every VM-scoped tool registered on the server is available as a step (`tool` = the exact MCP tool name, `args` = exactly that tool's input). A drift-guard test fails if a new `parallels_vm_*` tool is registered without being available as a step.
4. The whole sequence is **validated before anything runs**: all errors reported together, nothing executed on a validation failure.
5. The motivating example above behaves as described for both the changed and unchanged screen (test with two distinct synthetic PNGs and a fake runner; the exec must not run in the unchanged case).
6. The result is an ordered array with one entry per declared step (including `skipped` and `not_run` ones), each with its `tool` and that tool's own output body. Images come back as native image content blocks, referenced from the results by index.
7. `wait`, `retry`, step count, total runtime, output size and image count are all bounded (section 8) and tested.
8. Every step in `docs/qa/007-parallels-vm-sequence-QA.md` passes.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Invocation of tools | **In-process.** The orchestrator calls the tools' typed handler functions directly (`VMExec`, `VMCapture`, ...) through a generic adapter. It does not speak MCP to itself | One implementation per capability; all validation (`ValidateVMRef`, command limits, mode rules) is reused for free; no serialization of images through a pipe |
| "Future tools" | A step type is an `Action` in a `Registry`. `orchestrator.FromHandler[In, Out](name, handler)` wraps any handler with the standard signature. Adding a tool to sequences is **one line** in `tools.RegisterSteps` | The requirement that new tools join automatically. The drift-guard test enforces it |
| Step addressing | `tool` = the MCP tool name (`parallels_vm_exec`, `parallels_vm_capture`, ...). `args` = that tool's input JSON verbatim | The AI already knows these shapes; one vocabulary |
| Control steps | Three step kinds, exactly one per step: `tool` (+`args`), `wait`, `gate`. `wait` and `gate` are **not tools** and never appear in `tools/list` | As requested: wait and gates are orchestration primitives |
| Modifiers | `when` (skip if false), `expect` (assert after running; stop on failure by default), `retry` (re-run until `expect` holds), `on_fail`, `on_error`, `quiet` | Covers skip, short-circuit and poll without a language |
| Condition language | A **flat list of leaf checks, all must pass (AND)**, each leaf with optional `negate`. No nesting, no OR, no expression strings | Portable JSON schema (no recursion, no `oneOf`, important for non-Claude clients, see spec 002 deviation 6), trivial for a model to emit, no eval surface. Regex alternation covers most OR needs |
| Checks | `exit_code`, `matches` (RE2 regex on an output field), `contains` (substring), `image_changed`, `succeeded` | The set the user asked for plus the generic basics. `matches`/`contains` read any string field of a step's output (default `stdout`), so future tools work without new check types |
| Regex engine | Go `regexp` (RE2), unanchored match, pattern <= 1024 chars, compiled at validation | Linear-time: no ReDoS from model-written patterns |
| Image diff | Per-pixel compare with per-channel `tolerance` (default 8) and `threshold_percent` (default 0.1) of changed pixels; different dimensions = changed. Operates on the bytes the capture step produced (so after its own downscale) | Ignores cursor blink / clock-tick noise by default, tunable per check, decisions match what the AI sees |
| One VM per sequence | Top-level `vm_id` (fleet) or configured target (VM mode). Steps must **not** set `vm_id` (rejected); the orchestrator injects it in fleet mode | Smallest blast radius; one mode contract; multi-VM is a follow-up |
| Serialization | One sequence at a time per VM (mutex keyed by the target; waiters honour ctx) | Two sequences typing into the same VM would interleave. Standalone tool calls are not serialized (documented limitation) |
| Failure policy | `on_error: stop` (default) or `continue`. A tool Go error is an *error*; a non-zero exec exit is *data* (spec 005), judged by checks | Keeps spec 005's rule |
| Overall outcome | Not a tool error when steps fail or a gate stops the run: the call succeeds and `status` says what happened. **Tool error only** for validation failure, cancellation, or an internal bug | The AI needs the partial results, not an opaque error |
| Result carrying | `content[0]` = text block with the compact JSON report (so clients that only forward `content` still see everything), then one image block per returned image in order. `structuredContent` = the same report (without images) | Same dual delivery as spec 004; many clients ignore structured output |
| Args in the report | Never echoed (commands and typed text may hold secrets and bloat the result). Only tool name, id, status, timings, outputs | Same logging rule as specs 005/006 |
| Dependencies | None new (stdlib, SDK, `jsonschema-go` and `x/image` already present) | AGENTS.md |

## 3. Repository layout (delta)

```
pdfm-mcp/
├── internal/
│   ├── imaging/
│   │   ├── diff.go                      # NEW: Diff(a, b []byte, DiffOptions) (DiffResult, error)
│   │   └── diff_test.go                 # NEW
│   ├── orchestrator/                    # NEW package (no import of internal/tools)
│   │   ├── types.go                     # Sequence, Step, Check, Retry, Report, StepResult
│   │   ├── registry.go                  # Action, Registry, FromHandler[In, Out]
│   │   ├── validate.go                  # Validate -> *Plan, []error
│   │   ├── conditions.go                # evaluate checks against run state
│   │   ├── runner.go                    # Runner.Run, wait, retry, budgets, truncation
│   │   ├── lock.go                      # per-VM mutex map
│   │   └── *_test.go
│   ├── tools/
│   │   ├── steps.go                     # NEW: RegisterSteps(reg): one line per tool
│   │   ├── parallels_vm_sequence.go     # NEW: tool, handler, description generation, Register
│   │   └── parallels_sequence_test.go   # NEW: unit + in-memory protocol + drift guard
│   └── server/server.go                 # builds the registry, registers the tool last
├── test/e2e/stdio_test.go               # + listed assertion (+ opt-in read-only run)
├── docs/
│   ├── specs/007-parallels-vm-sequence.md
│   └── qa/007-parallels-vm-sequence-QA.md
├── AGENTS.md                            # + two rules (section 10)
└── README.md                            # + sequence section with the examples above
```

Import direction: `tools` → `orchestrator` → `imaging`. `orchestrator` knows nothing about Parallels; actions are injected.

## 4. The JSON the AI builds

### 4.1 Top level

```go
// Sequence is the argument schema of parallels_vm_sequence.
type SequenceInput struct {
	VMID           string `json:"vm_id,omitempty" jsonschema:"VM id or name; required in fleet mode, omit in VM mode"`
	Steps          []Step `json:"steps" jsonschema:"ordered steps, 1 to 50"`
	OnError        string `json:"on_error,omitempty" jsonschema:"stop (default) or continue when a step errors"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"total runtime cap in seconds (1-900, default 120)"`
	MaxImages      int    `json:"max_images,omitempty" jsonschema:"max images returned (0-16, default 8); extra images are dropped but still usable by image_changed"`
	ValidateOnly   bool   `json:"validate_only,omitempty" jsonschema:"validate the sequence and return without running anything"`
}
```

### 4.2 Step

```go
type Step struct {
	ID      string         `json:"id,omitempty"      jsonschema:"unique name (a-z, 0-9, _; starts with a letter) used by checks; defaults to s1, s2, ..."`
	Tool    string         `json:"tool,omitempty"    jsonschema:"MCP tool name to run, e.g. parallels_vm_exec; mutually exclusive with wait and gate"`
	Args    map[string]any `json:"args,omitempty"    jsonschema:"that tool's arguments, exactly as for a direct call (without vm_id)"`
	Wait    *WaitSpec      `json:"wait,omitempty"    jsonschema:"pause before the next step; mutually exclusive with tool and gate"`
	Gate    []Check        `json:"gate,omitempty"    jsonschema:"checks that must all pass or the run stops; mutually exclusive with tool and wait"`
	When    []Check        `json:"when,omitempty"    jsonschema:"run this step only if all checks pass, else skip it"`
	Expect  []Check        `json:"expect,omitempty"  jsonschema:"tool steps: checks on this step's own result; on failure see on_fail"`
	OnFail  string         `json:"on_fail,omitempty" jsonschema:"stop (default) or continue when expect or gate fails"`
	Retry   *Retry         `json:"retry,omitempty"   jsonschema:"tool steps with expect: re-run the step until expect passes"`
	OnError string         `json:"on_error,omitempty" jsonschema:"per-step override of the sequence on_error"`
	Quiet   bool           `json:"quiet,omitempty"   jsonschema:"keep the step's output and image out of the report (still available to checks)"`
}

type WaitSpec struct {
	Seconds float64 `json:"seconds" jsonschema:"pause length in seconds (0.1-300)"`
}

type Retry struct {
	MaxAttempts int `json:"max_attempts" jsonschema:"total attempts including the first (2-50)"`
	DelayMS     int `json:"delay_ms,omitempty" jsonschema:"pause between attempts in ms (0-60000, default 1000)"`
}
```

`args` is `map[string]any`: its schema is a plain `object`. The per-tool argument shapes are published through the **generated tool description** (section 7.3), not through the input schema.

### 4.3 Check

```go
type Check struct {
	Check            string   `json:"check"                      jsonschema:"exit_code | matches | contains | image_changed | succeeded"`
	Step             string   `json:"step,omitempty"             jsonschema:"id of the step whose result is checked; in expect it defaults to the step itself"`
	Negate           bool     `json:"negate,omitempty"           jsonschema:"invert the result"`
	Equals           *int     `json:"equals,omitempty"           jsonschema:"exit_code: expected value (default 0)"`
	Field            string   `json:"field,omitempty"            jsonschema:"matches/contains: output field to read (default stdout; also stderr, ...)"`
	Regex            string   `json:"regex,omitempty"            jsonschema:"matches: RE2 regular expression, unanchored, max 1024 chars"`
	Text             string   `json:"text,omitempty"             jsonschema:"contains: substring"`
	From             string   `json:"from,omitempty"             jsonschema:"image_changed: id of the baseline capture step"`
	To               string   `json:"to,omitempty"               jsonschema:"image_changed: id of the later capture step (default: the step itself)"`
	ThresholdPercent *float64 `json:"threshold_percent,omitempty" jsonschema:"image_changed: percent of pixels that must differ (0-100, default 0.1)"`
	Tolerance        *int     `json:"tolerance,omitempty"        jsonschema:"image_changed: per-channel difference ignored as noise (0-255, default 8)"`
}
```

| `check` | Passes when | Reads |
|---|---|---|
| `exit_code` | the step's `exit_code` field equals `equals` (default 0) | structured output field `exit_code` |
| `matches` | RE2 `regex` matches somewhere in the string field `field` (default `stdout`) | any string field of the output |
| `contains` | the string field contains `text` | any string field |
| `image_changed` | the images of steps `from` and `to` differ by at least `threshold_percent` | the retained images |
| `succeeded` | the step finished with status `ok` | step status |

Rules:

- **Unavailable input is a failed check regardless of `negate`.** If the referenced step errored, was skipped or did not run, or the field/image does not exist, the check is `passed: false` with a detail such as `step "ready" did not run`. `negate` is *not* applied (otherwise a missing result would silently turn into a pass).
- All checks in a list must pass (AND). An empty list is vacuously true.
- Every evaluation returns a `detail` string that is included in the report (for example `3.2% of pixels changed (threshold 0.1%), bounds (120,40)-(980,610)`), so the AI knows *why*.
- Checks that reference another step may only reference **earlier** steps.
- In `when` and `gate` there is no owning result, so `step` (or `from` and `to`) is required. In `expect`, `step` defaults to the owning step.

## 5. Semantics

### 5.1 Step kinds and flow

For each step, in order:

1. Evaluate `when`. False: record `skipped` (with the checks' detail) and continue. (Not an error, never stops the run.)
2. Run by kind:
   - **tool**: inject `vm_id` (fleet mode), run the action, collect outcome.
   - **wait**: sleep `seconds` (cancellable; counts against the total timeout).
   - **gate**: evaluate its checks. Failure: step status `expectation_failed`; with `on_fail: stop` (default) the run **stops** (`status: "stopped"`), with `continue` it goes on.
3. For tool steps with `expect`: evaluate after the action. Failure with `retry` left: sleep `delay_ms`, re-run the action, re-evaluate. Failure with no attempts left: status `expectation_failed`; `on_fail: stop` (default) stops the run, `continue` goes on. Only the **last attempt's** output and image are kept.
4. A tool error (Go error from the handler): status `error`; `on_error: stop` (default) ends the run with `status: "failed"`, `continue` goes on.

Exec exit codes are data (spec 005), so "retry until the command succeeds" is `expect: [{check: exit_code}]` + `retry`. There is deliberately no retry-on-error.

### 5.2 Statuses

| Step status | Meaning |
|---|---|
| `ok` | ran, and its `expect` (if any) passed |
| `error` | the tool returned an error (message in `error`) |
| `skipped` | `when` was false |
| `expectation_failed` | `expect` or `gate` did not hold (after retries) |
| `not_run` | the run ended earlier |

| Sequence `status` | Meaning |
|---|---|
| `completed` | every step ran or was skipped, none failed |
| `completed_with_failures` | the run reached the end but some steps errored or failed an expectation under `continue` |
| `stopped` | a gate/expectation short-circuited the run (intentional, e.g. screen did not change) |
| `failed` | a step errored with `on_error: stop` |
| `timeout` | `timeout_seconds` elapsed; the in-flight step was cancelled |

Client cancellation propagates through the context, the in-flight child is killed (runner contract), cleanup is unconditional (spec 004 temp dirs), and the call returns a tool error.

### 5.3 Image handling

- A tool outcome may carry images (the adapter extracts `*mcp.ImageContent` blocks from the handler's `CallToolResult`).
- **All** images are retained in memory for the run (bounded, section 8) so `image_changed` can compare any two capture steps, even `quiet` ones or ones beyond `max_images`.
- An image is *returned* only if the step is not `quiet` and fewer than `max_images` images were already returned. Dropped images are flagged on the step (`image_dropped: true`).
- Returned images are appended to the response content in step order; the step's `image_index` (0-based among returned images) says which block is which.
- Diff operates on the bytes the capture step produced: if the AI wants a sharper comparison it passes `full_resolution: true` (or a larger `max_dimension`) in that capture's `args`.

### 5.4 Result shape

```go
type SequenceOutput struct {
	VMID        string       `json:"vm_id"`
	Status      string       `json:"status"`
	StoppedAt   *int         `json:"stopped_at,omitempty"   jsonschema:"0-based index of the step that stopped or failed the run"`
	StopReason  string       `json:"stop_reason,omitempty"`
	DurationMS  int64        `json:"duration_ms"`
	StepsRun    int          `json:"steps_run"`
	ImageCount  int          `json:"image_count" jsonschema:"number of image blocks that follow the text block"`
	Results     []StepResult `json:"results"`
}

type StepResult struct {
	Index       int           `json:"index"`
	ID          string        `json:"id"`
	Kind        string        `json:"kind"   jsonschema:"tool | wait | gate"`
	Tool        string        `json:"tool,omitempty" jsonschema:"the tool that produced output (discriminates the output shape)"`
	Status      string        `json:"status"`
	Attempts    int           `json:"attempts,omitempty"`
	DurationMS  int64         `json:"duration_ms,omitempty"`
	Output      any           `json:"output,omitempty" jsonschema:"the tool's own structured output; its shape depends on tool"`
	ImageIndex  *int          `json:"image_index,omitempty"`
	ImageDropped bool         `json:"image_dropped,omitempty"`
	Checks      []CheckResult `json:"checks,omitempty"`
	Error       string        `json:"error,omitempty"`
	Waited      float64       `json:"waited_seconds,omitempty"`
}

type CheckResult struct {
	Phase  string `json:"phase" jsonschema:"when | expect | gate"`
	Check  string `json:"check"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}
```

`Output` is `any` (not `json.RawMessage`: see spec 002 deviation 4). It carries exactly the tool's own output struct (`ExecOutput`, `CaptureOutput`, `PowerOutput`, ...), so "the body is different per tool" is preserved and discriminated by `tool`. `results` is never nil (empty array, not null).

Example (changed case, abridged):

```json
{ "vm_id": "ubuntu_github_builder", "status": "completed", "steps_run": 4, "image_count": 2, "duration_ms": 3610,
  "results": [
    { "index": 0, "id": "before", "kind": "tool", "tool": "parallels_vm_capture", "status": "ok",
      "output": { "width": 1024, "height": 640, "bytes": 81233, "mime_type": "image/png" }, "image_index": 0 },
    { "index": 1, "id": "s2", "kind": "wait", "status": "ok", "waited_seconds": 3 },
    { "index": 2, "id": "after", "kind": "tool", "tool": "parallels_vm_capture", "status": "ok",
      "output": { "width": 1024, "height": 640, "bytes": 83011, "mime_type": "image/png" }, "image_index": 1,
      "checks": [ { "phase": "expect", "check": "image_changed", "passed": true,
                    "detail": "3.2% of pixels changed (threshold 0.1%)" } ] },
    { "index": 3, "id": "s4", "kind": "tool", "tool": "parallels_vm_exec", "status": "ok",
      "output": { "vm_id": "ubuntu_github_builder", "exit_code": 0, "stdout": "...", "stderr": "" } } ] }
```

Unchanged case: step 2 `status: "expectation_failed"` (with its check detail `0.00% of pixels changed (threshold 0.1%)`), step 3 `not_run`, top-level `status: "stopped"`, `stopped_at: 2`, `stop_reason: "step after: expect image_changed failed"`.

## 6. Orchestrator package

### 6.1 Registry and adapter (`registry.go`)

```go
// Outcome is what an action returns to the runner.
type Outcome struct {
	Data   map[string]any // the tool's structured output as a generic map (for checks and the report)
	Images []Image        // images carried by the tool's content blocks
}

type Image struct {
	Data     []byte
	MIMEType string
}

// Action is one step type. Name is the MCP tool name.
type Action struct {
	Name        string
	Description string
	VMScoped    bool // true: the runner injects vm_id in fleet mode and rejects a step-level vm_id
	InputSchema *jsonschema.Schema // used for up-front validation and the generated help text
	Run         func(ctx context.Context, args json.RawMessage) (Outcome, error)
}

type Registry struct{ /* map[string]Action, insertion-ordered names */ }

func NewRegistry() *Registry
func (r *Registry) Register(a Action) // panics on duplicate name (startup bug)
func (r *Registry) Get(name string) (Action, bool)
func (r *Registry) Names() []string   // stable order

// FromHandler adapts any standard tool handler into an Action.
func FromHandler[In, Out any](
	name, description string, vmScoped bool, inputSchema *jsonschema.Schema,
	h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) Action
```

`FromHandler`'s `Run`:

1. Decode `args` into `In` with `json.Decoder.DisallowUnknownFields()`: a typo like `"comand"` is a clear validation error instead of a silently ignored field.
2. Call `h(ctx, nil, in)`. **The request is nil**: handlers must not dereference it (all current handlers ignore it; this becomes a repo rule, section 10).
3. Marshal `Out` and unmarshal into `map[string]any` → `Outcome.Data`.
4. For each `*mcp.ImageContent` in the returned result's `Content`, append an `Image`.
5. Return the handler's error as is.

### 6.2 `internal/tools/steps.go`

```go
// RegisterSteps makes every VM-scoped tool available as a sequence step.
// Add one line here for each new VM-scoped tool.
func RegisterSteps(r *orchestrator.Registry) {
	r.Register(orchestrator.FromHandler("parallels_vm_capture", capture.desc, true, vmToolSchema[CaptureInput](), VMCapture))
	r.Register(orchestrator.FromHandler("parallels_vm_exec", ..., true, ..., VMExec))
	r.Register(orchestrator.FromHandler("parallels_vm_start", ..., true, ..., VMStart))
	// resume, pause, suspend, restart, reset, reset_uptime, stop
	// parallels_vm_send_key, parallels_vm_type (spec 006, when implemented)
}
```

Descriptions are shared constants with the `Register<Name>` functions so the two cannot diverge. **Not** registered as steps: `parallels_list_vms`, `parallels_vm_info` (fleet-scoped reads, no use in a VM session) and `parallels_vm_sequence` itself (no nesting, no recursion).

### 6.3 Validation (`validate.go`)

`Validate(in SequenceInput, reg *Registry, ctx ValidateContext) (*Plan, []error)` runs before any execution and **collects all errors** (each prefixed with `step <n> (<id>):`):

- 1 <= steps <= 50; `timeout_seconds` 1..900 (default 120); `max_images` 0..16 (default 8); `on_error`/`on_fail` in {stop, continue}.
- Exactly one of `tool` / `wait` / `gate` per step.
- Step `id`: matches `^[a-z][a-z0-9_]{0,31}$`, unique; missing ids become `s<index+1>`.
- `tool` exists in the registry. `args` decode strictly into the action's input; `vm_id` in `args` is **rejected** (both modes). The decoded args are then validated against the action's `InputSchema` (jsonschema-go `Resolve` + `Validate`) *after* the orchestrator-injected `vm_id` is added in fleet mode, so the same `required` rules a direct call gets apply.
- Handler-level validation (command length, ref format, `max_dimension` range, ...) is **not duplicated**: it happens when the step runs. (Known gap: a bad `max_dimension` is found at run time, not validate time. Acceptable; the failing step reports it.)
- `wait.seconds` in [0.1, 300]. `retry.max_attempts` in [2, 50], `delay_ms` in [0, 60000] (default 1000); `retry` requires `expect` and a tool step.
- `expect` only on tool steps.
- Checks: `check` is one of the five; required params per check (`regex` for `matches` and it must compile and be <= 1024 chars; `text` non-empty for `contains`; `from` for `image_changed`; `threshold_percent` 0..100; `tolerance` 0..255); `field` non-empty if set; every referenced step id exists and is **earlier** than the referencing step; `image_changed` references must be steps whose tool can produce images (capture today: determined by a `ProducesImages bool` on `Action`, set by the capture registration).
- Budget pre-check: `sum(wait seconds) + sum(retry delay * (attempts-1))` must be <= `timeout_seconds`, else error `declared waits need at least N s but timeout_seconds is M`.

On any error the tool returns a **tool error** listing all of them (one per line) and runs nothing. `validate_only: true` runs this phase and returns a normal result with `status: "validated"` and `steps_run: 0`.

### 6.4 Runner (`runner.go`)

```go
type Runner struct {
	Reg      *Registry
	Logger   *slog.Logger
	Sleep    func(ctx context.Context, d time.Duration) error // injected; tests use an instant recorder
	Now      func() time.Time
	Progress func(done, total int, msg string)                // optional; may be nil
	locks    *vmLocks
}

func (r *Runner) Run(ctx context.Context, plan *Plan) (Report, error)
```

- Acquire the per-VM lock (blocks, honouring `ctx`) before the first step; release in `defer`.
- Total deadline: `context.WithTimeout(ctx, plan.Timeout)`; hitting it yields `status: "timeout"` (not an error).
- Per step: `r.Progress(i, total, "<tool|wait|gate>")` (message never contains args).
- Per-run state: `results []StepResult`, `outputs map[id]map[string]any`, `images map[id]Image`, and counters for the budgets.
- Logging (slog, stderr): sequence start (VM ref, step count), per step (index, tool, status, duration, attempts), end (status, duration). **Never args, never output text, never pixels.**

### 6.5 Conditions (`conditions.go`)

`evaluate(phase, owner, checks []Check, state) []CheckResult`. Pure function over `state` (statuses, outputs, images), fully table-testable. `image_changed` calls `imaging.Diff`; results are cached per (from, to, threshold, tolerance) so retries and duplicate checks do not recompute.

## 7. Tool layer

### 7.1 Handler

```go
func VMSequence(reg *orchestrator.Registry, runner *orchestrator.Runner) func(ctx context.Context, req *mcp.CallToolRequest, in SequenceInput) (*mcp.CallToolResult, SequenceOutput, error)
```

1. `svc := parallels.GetInstance()`; `target, err := resolvePowerTarget(svc, in.VMID)` (same mode contract as specs 003 to 006).
2. `plan, errs := orchestrator.Validate(...)` → on errors, return a joined tool error.
3. `validate_only` → return `status: "validated"`.
4. Wire progress: if `req != nil` and the request carries a progress token, send `notifications/progress` per step through the session (this keeps clients with per-request timeouts alive during long sequences). Absent token: no-op.
5. `report, err := runner.Run(ctx, plan)`; a cancelled context becomes a tool error.
6. Build the result:

```go
text, _ := json.Marshal(out)                       // compact report, content[0]
content := []mcp.Content{&mcp.TextContent{Text: string(text)}}
for _, img := range report.ReturnedImages {
	content = append(content, &mcp.ImageContent{Data: img.Data, MIMEType: img.MIMEType})
}
return &mcp.CallToolResult{Content: content}, out, nil
```

### 7.2 Registration

```go
func RegisterParallelsVMSequence(s *mcp.Server, mode PowerMode, reg *orchestrator.Registry) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "parallels_vm_sequence",
		Description: sequenceDescription(reg, mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(), // it can chain exec, keys and power operations
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  powerInputSchema[SequenceInput](mode),
		OutputSchema: outputSchemaFor[SequenceOutput]("parallels_vm_sequence"),
	}, VMSequence(reg, runner))
}
```

`powerInputSchema` adds `vm_id` to `required` in fleet mode and keeps the inferred `steps` required. All slice fields (`steps`, `when`, `expect`, `gate`, `results`, `checks`) go through the existing `normalizeNullArrays`. The protocol test asserts no `["null","array"]` unions on the wire.

`server.New`: build the registry (`reg := orchestrator.NewRegistry(); tools.RegisterSteps(reg)`) **after** the individual tools and register `parallels_vm_sequence` last, inside the module-enabled block, in both modes.

### 7.3 Generated description

The step tools' argument shapes are not in the input schema (`args` is a free object), so the description is generated at registration from the registry:

```
Runs a sequence of steps against one VM in a single call and returns every result, with screenshots as images.
Step kinds: {"tool": "<name>", "args": {...}}, {"wait": {"seconds": N}}, {"gate": [checks]}.
Modifiers: when (skip if false), expect (assert own result; stops the run on failure), retry {max_attempts, delay_ms} (repeat until expect passes), on_fail, on_error, quiet.
Checks (all must pass): exit_code{step,equals}, matches{step,field,regex}, contains{step,field,text}, image_changed{from,to,threshold_percent}, succeeded{step}; negate inverts.
Available tools (args are exactly those of the standalone tool, without vm_id):
  parallels_vm_capture(max_dimension?, full_resolution?)
  parallels_vm_exec(command, user?, resolve_paths?)
  ... generated from each action's InputSchema: name, required marked, optional with "?"
Example: [capture "before", wait 3s, capture "after" with expect image_changed from "before", exec ...]. Max 50 steps, default 120 s total.
<mode sentence from powerDescription>
```

Keep it under ~3 KB. The compact `name(arg, arg?)` signature list is generated from `InputSchema.Properties` (required first), so it stays correct as tools change. Per-tool argument descriptions still reach the model on the standalone tools' own schemas; if models mis-shape `args`, a follow-up can add a `parallels_vm_sequence_help` tool returning full per-tool schemas.

## 8. Limits and budgets

| Limit | Value | On breach |
|---|---|---|
| Steps per sequence | 50 | validation error |
| Total runtime | `timeout_seconds` default 120, max 900 | `status: "timeout"` |
| `wait` | 0.1 to 300 s per step | validation error |
| `retry.max_attempts` / `delay_ms` | 2 to 50 / 0 to 60000 | validation error |
| Declared wait + retry delay vs timeout | must fit | validation error |
| Regex pattern | 1024 chars, RE2 | validation error |
| Images returned | `max_images` default 8, max 16 | extras dropped (`image_dropped`) |
| Images retained in memory | 64 MB total (each is already capped at 10 MB by spec 004) | step errors `image budget exceeded`; earlier images stay usable |
| Returned image payload | 24 MB total | extras dropped |
| Per-step text in the report | `stdout`/`stderr` (any string field) cut to 64 KB (rune-safe) with `"<field>_truncated_in_report": true` | truncated; **checks always see the full (service-capped) text** |
| Total report text | 1 MB; beyond it further step strings are cut to 1 KB | truncated with markers |
| Concurrent sequences per VM | 1 | later ones wait for the lock (ctx-aware) |

## 9. Security and privacy

- **No new capability.** A sequence can do exactly what its steps' standalone tools can, through the same handlers and validation. It does not add a shell, file access, or network.
- **Blast radius.** One VM per sequence; steps cannot name another VM; fleet mode still requires an explicit `vm_id`. The tool is annotated `DestructiveHint: true` (it can chain exec, keys, power operations). Clients that confirm destructive tools will confirm the sequence as a whole, so the description must say plainly what a sequence may contain.
- **Conditions are data, not code.** Fixed predicates, RE2 regex, no expression strings, no eval, no templating of step output into later args (step outputs are never interpolated into commands). A hostile screen or guest output cannot steer execution beyond the predicates the AI wrote.
- **Untrusted output.** Screenshots and exec output are untrusted content that goes back to the model. The orchestrator never treats it as instructions.
- **Secrets.** `args` can carry inline secrets (commands, typed text). They are never logged and never echoed in the report. Screenshots can show secrets (spec 004 privacy note applies to every image in a sequence).
- **Bounded everything** (section 8), including a hard wall-clock cap, so a malformed or adversarial sequence cannot run unbounded or exhaust memory.
- **Cancellation is honoured** at every wait, retry delay and in-flight step.
- Never write to stdout. Everything uses `slog` to stderr; no runner change is needed.

## 10. AGENTS.md additions (apply when implementing)

- Tool handlers must be callable with a **nil** `*mcp.CallToolRequest` (the orchestrator calls them in-process). Use `req` only inside `if req != nil`.
- Every new VM-scoped tool must also be added to `tools.RegisterSteps`; the drift-guard test enforces it.

## 11. Testing

1. **`imaging/diff_test.go`**: identical bytes → unchanged (fast path); one pixel inside tolerance → unchanged; a block beyond tolerance → changed with correct percent and bounding box; threshold behaviour (0, 0.1, high); different dimensions → changed + `SizeMismatch`; corrupt PNG → error; oversize pixel count rejected before decode.
2. **`orchestrator` (fake actions, instant fake `Sleep`, no Parallels)**:
   - Validation table: no kind / two kinds, duplicate id, bad id, unknown tool, step-level `vm_id`, unknown field in args, forward reference, unknown reference, bad regex, regex too long, `retry` without `expect`, `expect` on a wait, waits exceeding the timeout, 51 steps, `image_changed` pointing at a non-image step. All errors collected in one pass; zero actions run on failure.
   - Flow: `when` true/false; `expect` pass; `expect` fail with stop (later steps `not_run`, `status: stopped`, `stopped_at`); with `on_fail: continue` (`completed_with_failures`); gate stop; `on_error` stop vs continue; per-step `on_error` override.
   - Retry: succeeds on attempt 3 (3 attempts recorded, 2 delays slept); exhausted → `expectation_failed`; only the last attempt's image kept.
   - Wait: sleeps recorded with exact durations; cancellation during a wait; timeout mid-sequence → `status: timeout`.
   - Checks: each of the five, `negate`, **unavailable input fails even when negated**, multi-check AND, `field` defaults, regex unanchored, `exit_code` default 0.
   - Images: retention for diff when `quiet`; `max_images` drop with `image_dropped` yet still diffable; index mapping.
   - Budgets: per-step truncation (rune-safe) while checks see the full text; total report cap; image memory budget.
   - Locking: two runs on the same VM serialize; different VMs do not; a waiter is released by ctx cancel.
   - `-race` clean.
3. **Adapter (`registry_test.go`)**: `FromHandler` rejects unknown fields, passes a nil request, converts `Out` to a map (field names from JSON tags), extracts image blocks, propagates handler errors.
4. **`tools/parallels_sequence_test.go`**:
   - Mode table: VM mode uses the configured target and rejects `vm_id`; fleet requires it; step-level `vm_id` rejected in both.
   - **Motivating example, both branches**, through the in-memory transport with a fake `CommandRunner` that writes distinct PNGs on successive `capture` calls: *changed* → 4 results, 2 image blocks after the text block, exec ran once; *unchanged* → `status: "stopped"`, step 3 `not_run`, **fake runner saw no exec argv**.
   - Retry-until-regex against a fake exec returning `inactive`, `inactive`, `active`.
   - Response shape: `content[0]` is a `TextContent` whose JSON equals `structuredContent`; subsequent blocks are `ImageContent` with decodable PNG; `image_index` maps correctly; no base64 in structured output.
   - `validate_only` runs nothing (fake runner call count 0).
   - Schema: listed in both modes; `vm_id` in `required` only in fleet mode; `steps` always required; no `["null","array"]` unions; description mentions every registered step tool (generated list).
   - **Drift guard**: list the server's tools; every tool named `parallels_vm_*` except `parallels_vm_sequence` and `parallels_vm_info` must have a registry action. Fails with the missing name.
5. **`test/e2e/stdio_test.go`**: gating test asserts `parallels_vm_sequence` is listed in both modes. Opt-in real run (`PDFM_MCP_E2E_SEQ_VM=<running VM>`): `capture → wait 1 s → capture` (read-only), asserting two image blocks and `status: "completed"`. Never default-on, never mutating, never exec/keys/power.

## 12. QA (manual, real machine)

`docs/qa/007-parallels-vm-sequence-QA.md`:

1. Motivating example on an idle VM (expect: stopped, exec skipped) and with something animating on screen (expect: completed). Tune `threshold_percent`/`tolerance` notes (cursor blink, clock, notification toasts).
2. Retry-until: a service restart polled with `matches`.
3. `when` fallback branch; `gate` stop; `on_error: continue` with a stopped VM.
4. A start → wait → capture → type → capture flow (once spec 006 is in).
5. Limits: 51 steps, bad regex, forward reference, step-level `vm_id`: all rejected with every error listed.
6. Client behaviour: a 60+ second sequence in Claude Desktop and Claude Code (progress notifications keep it alive? record per-client timeout findings).
7. Image display: do all returned images render, in order, with `image_index` matching?
8. Temp dirs: `ls "$TMPDIR" | grep pdfm-mcp-capture` empty afterwards, also after a timeout and a cancelled call.
9. Concurrency: two simultaneous sequences on one VM serialize; on two VMs run in parallel.

## 13. Implementation order

1. This spec + index row in `docs/specs/README.md`.
2. `imaging.Diff` + tests.
3. `orchestrator` bottom-up: types → registry/adapter → conditions → validate → runner (+ lock) with tests as you go (fake actions only).
4. `tools/steps.go` + `parallels_vm_sequence.go` + generated description; unit + protocol + drift-guard tests.
5. `server.New` wiring (registry built after the other tools, sequence registered last).
6. e2e listing + opt-in run.
7. QA doc, README section, AGENTS.md rules.
8. `make check`, `make test-e2e`, manual QA.

## 14. Follow-ups (out of scope)

- **Async mode**: `start_sequence` returns a job id; `get_sequence` polls. Needed for sequences longer than client timeouts; progress notifications are the stopgap.
- Region-restricted image diff (ignore a clock area, or watch only one area), a diff-highlight image as an optional result, a perceptual hash check.
- OCR-based checks (`screen_text_matches`) once an OCR dependency is approved.
- OR / nested conditions (only if flat AND proves too limiting in practice).
- Multi-VM sequences, named variables carried between steps (`${steps.build.stdout}` templating, deliberately excluded for now because it feeds output into commands).
- Per-tool allow/deny list for sequences (for example `PDFM_MCP_SEQUENCE_DENY_TOOLS=parallels_vm_reset`).
- A `parallels_vm_sequence_help` tool returning full per-tool schemas and examples.
- Saved/named sequences (macros).

## 15. Known unknowns / verification notes

- **Client per-request timeouts.** Many MCP clients cap a tool call (often around 60 s; some reset the timer on progress notifications, some do not). The 120 s default may exceed what a given client tolerates. Verify in QA (Claude Desktop, Claude Code) and adjust the default and the description text accordingly.
- **Progress API names** in the Go SDK (`req.Params.GetProgressToken()`, `req.Session.NotifyProgress`) are unverified against the installed version.
- **jsonschema-go `Resolve`/`Validate` API** for up-front argument validation is unverified; if unavailable, rely on the handlers' own validation and strict decoding, and record it under Deviations.
- **Image diff defaults** (`tolerance` 8, `threshold_percent` 0.1) are educated guesses; tune against real VM screens in QA (cursor blink, clock, subpixel text, video playback, Retina downscaling).
- **Clients that drop `structuredContent`**: handled by `content[0]` carrying the full JSON report; verify the model reliably reads it.
- **Description size.** The generated description is long; check its token cost in `tools/list` and trim the example if needed.
- `ProducesImages` on `Action` is set by hand on the capture registration; if more image-producing tools appear, derive it from `Outcome` metadata instead.

### Deviations (fill in during implementation)

- **No jsonschema-go `Validate` API (§15).** `google/jsonschema-go v0.4.3` is generate-only (it has no Validate/Resolve surface), so up-front argument validation falls back to the spec's contingency: the `FromHandler` adapter strict-decodes step args (`DisallowUnknownFields`) both at validation time (via `action.ValidateArgs`, so unknown fields are reported in the up-front error list) and at run time, and each handler keeps its own domain validation (VM ref, command limits, mode rules).
- **Progress API verified (§15).** Against go-sdk v1.8.0: `req.Params.GetProgressToken()` reads the standard `_meta.progressToken`, and `(*ServerSession).NotifyProgress` sends `notifications/progress` unconditionally (no capability gate) — any client that supplies a token receives the per-step progress. Verified live with a raw stdio client (QA §3.3).
- **Generated description is 1 293 bytes (< 3 KB, §7.3/§15)**, with the `name(arg, arg?)` signatures generated from the registry input schemas. The signatures omit `vm_id` (as in the §7.3 example): steps must not set it, and the validator rejects it, so listing it would invite errors.
- **`timeout_seconds` 0 means "unset" → default 120**, matching the repo's 0/"" = unset argument convention; a negative value or a value above 900 is a validation error. (The spec text says "1–900"; 0 is the unset sentinel, not a duration.)
- **Report safety valve.** Beyond the 64 KB per-field truncation and the 1 MB total cap (after which further strings are cut to 1 KB), `fitReportJSON` applies a last-resort trim ladder (1 KB → 256 B → 64 B per string) if the serialized report would still exceed 1 MB. This path is not reached by the bounded test inputs; it is a defence against unexpected output shapes.
- **Input schema normalized too.** §7.2 names `normalizeNullArrays` for the slice fields; it is applied to the sequence *input* schema as well as the output schema, so neither wire schema carries a `["null","array"]` union (protocol test asserts both).
- **`golang.org/x/image` promoted to a direct dependency.** `imaging.Diff` imports `golang.org/x/image/draw`; the module was already in the dependency graph (indirect via the capture downscale), so no new dependency is added — only the direct/indirect marker changed after `go mod tidy`.
