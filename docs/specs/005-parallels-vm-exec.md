# Spec 005: Parallels VM Command Execution

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers |
| Scope | `parallels_vm_exec` tool; `Exec` on the Parallels service; `RunCapture` runner seam; tests; QA doc |
| Out of scope | `--password` authentication, streaming/interactive sessions, `prlsrvctl` server tools, snapshots — follow-up specs |

## 1. Goal

Let an AI client **run a command inside a guest VM** and read the result.
The tool runs `prlctl exec <vm> <options> <command>` and returns the guest's
`exit_code`, `stdout` and `stderr` as structured output.

```
prlctl exec <ID | NAME> <options> <command> [arg...]
```

Linux guests run the command via `bash -c` (prlctl's behaviour, not ours).
Parallels Tools must be installed in the guest.

### Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Default identity | `--current-user`, no password | User instruction: default to the user currently logged in inside the guest |
| `user` argument | Optional. Empty → `--current-user`; set → `--user <user>` (validated with `ValidateVMRef`) | One identity knob; keeps the common path argument-free |
| **No `--password` argument** | Not exposed as a tool argument | A password would ride in argv, which appears in runner error strings and in client transcripts/logs; AGENTS.md forbids leaking secrets into logs. `--current-user` covers the default; password-gated guests are out of scope |
| `resolve_paths` | Optional bool → `--resolve-paths` | Lets the agent reference host paths in the command |
| Mode contract | Reuse `resolvePowerTarget` (spec 003): fleet mode requires `vm_id` (id or name); VM mode uses the configured target and rejects an explicit `vm_id` | One mode contract for every VM-scoped tool |
| Command validation | Required, non-empty (after trim), ≤ 4096 bytes, no NUL bytes; **passed as a single argv element — never through a host shell** | Host safety is absolute; the guest OS is the trust boundary (the MCP client is the host user's own agent) |
| Non-zero guest exit | **Not a tool error.** Returned as a successful result with `exit_code`, `stdout`, `stderr` | A failed `grep` or test suite is data the agent needs, not a protocol failure |
| Tool errors | Invalid arguments, unknown/stopped VM (prlctl stderr), module disabled, 300 s timeout, launch failure | The runner/VM layer failed, not the guest command |
| Timeout | `execTimeout = 300 * time.Second`, caller ctx wins when shorter | Guest commands (installs, builds) outlast the 30 s read cap and often the 180 s power cap; 5 min is the safety fence — long jobs should run in the background *inside* the guest |
| Output caps | Runner hard cap 10 MB per stream (memory guard, error on overflow). Tool result caps: `stdout` 1 MB, `stderr` 256 KB, with `stdout_truncated` / `stderr_truncated` flags | An MCP result is one JSON line on stdio; unbounded guest output would bloat it. Truncation is signalled, never silent |
| Logging | Log VM ref, exit code, output sizes, duration. **Never log the command text** (it may contain inline secrets, e.g. `curl -H "Authorization: …"`) | Same rule as spec 004 for pixels |
| Cache | Exec does **not** touch the VM list cache | We cannot know what the command did; invalidation is the power tools' job |
| Annotations | `ReadOnlyHint: false`, `DestructiveHint: true`, `OpenWorldHint: false` | Arbitrary guest commands can destroy state |

## 2. Repository layout (delta)

```
pdfm-mcp/
├── internal/
│   ├── parallels/
│   │   ├── interfaces/interfaces.go          # + ExecResult, + Exec on ParallelsService
│   │   └── service/
│   │       ├── exec.go                       # + RunCapture on CommandRunner + realRunner
│   │       ├── vm_exec.go                    # NEW — Exec + validateCommand
│   │       ├── vm_exec_test.go               # NEW
│   │       └── service_test.go               # fakeRunner: + RunCapture, + captureFn field
│   ├── tools/
│   │   ├── parallels_vm_exec.go              # NEW — tool + handler + Register
│   │   ├── parallels_exec_test.go            # NEW — unit + in-memory protocol tests
│   │   └── parallels_test.go                 # testRunner: + RunCapture
│   └── server/server.go                      # + RegisterParallelsVMExec(s, mode)
├── test/e2e/stdio_test.go                    # + listed assertions (+ opt-in real echo/exit)
└── docs/
    ├── specs/005-parallels-vm-exec.md        # this file
    └── qa/005-parallels-vm-exec-QA.md        # NEW manual QA spec
```

No new dependencies.

## 3. Service layer

### 3.1 Interface (`internal/parallels/interfaces`)

```go
// ExecResult is the outcome of an Exec call.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int // the guest command's exit code; -1 when no process ran
}

// added to ParallelsService:
//
// Exec runs `command` inside the VM identified by vmRef (id or name,
// pre-validated). An empty user means --current-user; otherwise
// --user <user> is used. Non-zero guest exit codes are results, not
// errors: err is non-nil only when prlctl itself failed (unknown/stopped
// VM, timeout, launch failure). Exec does not touch the VM list cache.
Exec(ctx context.Context, vmRef, command string, user string, resolvePaths bool) (ExecResult, error)
```

### 3.2 Runner seam (`internal/parallels/service/exec.go`)

`CommandRunner` gains a second method:

```go
// RunCapture is like Run but also reports stderr and the child's exit
// code, and preserves stdout when the child exits non-zero. err is
// non-nil only for launch failures and context expiry — a non-zero exit
// code is NOT an error here.
RunCapture(ctx context.Context, exe string, args ...string) (stdout, stderr string, exitCode int, err error)
```

`realRunner.RunCapture` mirrors `Run` (caller deadline authoritative, else
`defaultCommandTimeout`) with:

- `*exec.ExitError` (or any exit) with a live context →
  `stdout, stderr, exitCode, nil` (exit code via `ExitCode()`, -1 on signal);
- context expiry → `("", "", -1, "<exe> <args>: <err> (command timed out)")`;
- launch failure (e.g. missing binary) → `("", stderrText, -1, mapRunError(...))`;
- hard cap: either stream > `maxOutputBytes` → error (same guard as `Run`).

`fakeRunner` / `testRunner` implement `RunCapture` on top of the existing
`fn` (error → exit -1; success → exit 0) plus an optional
`captureFn func(exe string, args []string) (stdout, stderr string, exit int)`
field for non-zero exits and stderr.

### 3.3 `internal/parallels/service/vm_exec.go`

```go
const (
	// execTimeout bounds one guest command.
	execTimeout = 300 * time.Second
	// maxCommandLength bounds the command line.
	maxCommandLength = 4096
	// maxExecStdout / maxExecStderr are the tool-result caps.
	maxExecStdout = 1 << 20   // 1 MB
	maxExecStderr = 256 << 10 // 256 KB
)

func validateCommand(cmd string) (string, error) // non-empty, no NUL, <= 4096
```

`(*service).Exec`:

1. `enabled` check → `ErrNotEnabled`.
2. `ref, err := ValidateVMRef(vmRef)`; `cmd, err := validateCommand(command)`;
   when `user != ""`: `user, err = ValidateVMRef(user)`.
3. argv (options **before** the command, per `prlctl exec --help`):
   `exec <ref> --current-user | --user <user> [--resolve-paths] <cmd>`.
4. `context.WithTimeout(ctx, execTimeout)` →
   `s.runner.RunCapture(runCtx, s.prlctlPath, args...)`.
5. On runner error: log `vm` ref only (the error string contains the
   command via `joinArgs` — never log it) and return the error.
6. On success: truncate stdout to `maxExecStdout` / stderr to
   `maxExecStderr` (UTF-8-safe: cut on a rune boundary, drop partial bytes),
   set the truncation flags, log `vm, exit_code, stdout bytes, stderr bytes,
   took`, return `ExecResult`.

Truncation flags are reported to the tool layer: `ExecResult` therefore also
carries `StdoutTruncated, StderrTruncated bool` — or the service exposes a
`truncated` helper; either way the flags reach the structured output.

## 4. Tool layer

### 4.1 `internal/tools/parallels_vm_exec.go`

```go
type ExecInput struct {
	VMID         string `json:"vm_id,omitempty" jsonschema:"VM id or name to run the command in"`
	Command      string `json:"command" jsonschema:"command line to execute in the guest (Linux guests: bash -c), max 4096 characters"`
	User         string `json:"user,omitempty" jsonschema:"guest user to run as; defaults to the user currently logged in inside the guest"`
	ResolvePaths bool   `json:"resolve_paths,omitempty" jsonschema:"convert host paths in the command to guest paths"`
}

type ExecOutput struct {
	VMID            string `json:"vm_id" jsonschema:"the VM id or name the command ran in"`
	ExitCode        int    `json:"exit_code" jsonschema:"guest command exit code; -1 when no process ran"`
	Stdout          string `json:"stdout" jsonschema:"guest command stdout (truncated to 1 MB)"`
	Stderr          string `json:"stderr" jsonschema:"guest command stderr (truncated to 256 KB)"`
	StdoutTruncated bool   `json:"stdout_truncated" jsonschema:"true when stdout was cut at 1 MB"`
	StderrTruncated bool   `json:"stderr_truncated" jsonschema:"true when stderr was cut at 256 KB"`
}
```

Handler (`VMExec`): `resolvePowerTarget(svc, in.VMID)` →
`svc.Exec(ctx, target, in.Command, in.User, in.ResolvePaths)` → `ExecOutput`.
A non-zero `exit_code` is returned as a normal (non-error) result. No custom
`Content`: the SDK auto-generates the TextContent fallback from the
structured output.

### 4.2 Registration

```go
func RegisterParallelsVMExec(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "parallels_vm_exec",
		Description: powerDescription(
			"Runs a command inside the guest VM and returns its exit code, stdout and stderr. Defaults to the user currently logged in in the guest (set user to override). No password support. Guest needs Parallels Tools. Commands are bounded (4096 chars) and capped at 5 minutes.",
			mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  powerInputSchema[ExecInput](mode),
		OutputSchema: outputSchemaFor[ExecOutput]("parallels_vm_exec"),
	}, VMExec)
}
```

`command` (no `omitempty`) is inferred as required in both modes; `vm_id`
is added to `required` in fleet mode by `powerInputSchema`. Register in
`server.New` after `parallels_vm_capture`.

## 5. Security

- **No host shell, ever.** The command is one argv element; prlctl forwards
  it to the guest (`bash -c` on Linux). No host path is expanded unless
  `resolve_paths` asks for it.
- **No password parameter** (decisions table). The only secret the tool can
  see is an inline secret *inside* the command — which is why the command
  text is never logged.
- Command bound (4096 B), no NUL, identity bound via `ValidateVMRef`.
- 5-minute fence on every call; caller cancellation always wins.
- Output bounded end to end (runner 10 MB → result 1 MB / 256 KB).

## 6. Edge cases and invariants

| Case | Behaviour |
|---|---|
| Empty / whitespace-only command | Tool error before any exec |
| Command with NUL or > 4096 B | Tool error before any exec |
| Guest command exits non-zero | Normal result: `exit_code` set, stdout/stderr included |
| VM stopped / suspended / unknown, guest without tools, `--current-user` auth failure | **Normal result with `exit_code: 255`** and prlctl's stderr text (e.g. `PrlVm_TerminalConnect: … PRL_ERR_IO_STOPPED`, `Unable to authenticate the user…`). prlctl cannot be told apart from a guest command that itself exits 255, so these stay results — the stderr text is the signal for the agent (see Deviations) |
| Command runs > 300 s | Runner kills prlctl, "(command timed out)" tool error |
| Client cancels | Context propagates, child killed, tool error |
| stdout > 1 MB | Cut at 1 MB (rune-safe), `stdout_truncated: true` |
| stderr > 256 KB | Cut at 256 KB (rune-safe), `stderr_truncated: true` |
| Either stream > 10 MB | Runner error before result assembly |
| `resolve_paths: true` | `--resolve-paths` in argv, options before the command |
| Concurrent execs | Independent child processes; no shared state |
| VM list cache | Untouched |
| stdout | Untouched: runner buffers, nothing prints |

## 7. Testing

1. **`service/vm_exec_test.go`**: fake `captureFn` writes nothing to disk
   (no temp files for exec). Assert:
   - exact argv shapes: default (`exec <vm> --current-user <cmd>`),
     `--user`, `--resolve-paths`, combinations; options before the command;
   - success: stdout/stderr/exit_code pass through;
   - non-zero exit: `err == nil`, fields intact;
   - runner error (unknown VM): error returned, `exit_code` irrelevant;
   - context cancel: timeout-style error;
   - invalid ref / empty command / NUL / over-long command / over-long user:
     rejected with 0 runner calls;
   - module disabled → `ErrNotEnabled`;
   - 300 s deadline actually applied (via `lastDeadline()`);
   - truncation: 1 MB+1 stdout → cut + flag; rune-boundary safety (multi-byte
     char at the cut point);
   - cache untouched after Exec (following `ListVMs` still
     `Refreshed == false`).
2. **`tools/parallels_exec_test.go`**:
   - Table-driven handler tests over both modes (same four cases as the power
     tools);
   - non-zero exit → non-error result with fields;
   - `resolve_paths` and `user` reach the argv;
   - fleet protocol: listed, `required` contains `vm_id` **and** `command`;
     call with a failing command → `IsError` false, `exit_code` set,
     structured output carries stdout; no `["null","array"]` unions in the
     output schema;
   - VM mode: no `vm_id` in required, explicit `vm_id` rejected.
3. **`test/e2e/stdio_test.go`**: gating test asserts `parallels_vm_exec` is
   listed in both modes. Real execution is opt-in only
   (`PDFM_MCP_E2E_EXEC_VM=<running VM>`): call `echo e2e-exec-ok` (assert
   exit 0 + stdout) and `exit 3` (assert non-error result with
   `exit_code: 3`). Never default-on, never anything mutating.

## 8. QA (manual, real machine)

`docs/qa/005-parallels-vm-exec-QA.md`: read-only guest commands
(`echo`, `id`, `uname -a`) in both modes; non-zero exit case; stopped VM and
unknown VM errors; empty/over-long command errors; timeout note; cache
unchanged; opt-in e2e run.

## 9. Implementation order

1. This spec + index row in `docs/specs/README.md`.
2. `RunCapture` on `CommandRunner` + `realRunner` + both test fakes.
3. `interfaces.ExecResult` + `Exec`; `service/vm_exec.go` + tests.
4. `tools/parallels_vm_exec.go` + tests (unit then protocol).
5. `server.New` registration.
6. e2e listing + opt-in real exec.
7. QA doc + README.
8. `make check`, `make test-e2e`, manual QA on the real machine.

## 10. Follow-ups (out of scope)

- Password authentication (design a non-argv secret channel first).
- Streaming/interactive sessions, `--use-advanced-terminal`.
- Per-call timeout argument.
- Windows guest command-line semantics (prlctl's territory).

## 11. Known unknowns / verification notes

- **Verified on the real machine (Parallels 27.0.1):** `--current-user` on
  a headless guest without a logged-in session fails with
  `Unable to authenticate the user. Make sure that the specified
  credentials are correct and try again.` (prlctl exit 255);
  `--use-advanced-terminal` does not change that. A working `--current-user`
  needs a guest with Parallels Tools and an active user session.
- Guest stdout/stderr routing: verified — guest stdout → prlctl stdout,
  guest stderr → prlctl stderr (probe
  `echo out; echo err 1>&2; exit 7` once a guest can authenticate; the
  service tests cover the routing contract).
- `--resolve-paths` behaviour for VMs with no shared folders — unverified.

### Deviations (fill in during implementation)

- **prlctl-level failures are results, not tool errors.** `prlctl exec`
  exits with the guest's exit code on success-path completions and with 255
  on its own failures (stopped VM, no tools, auth failure); the two are
  indistinguishable at the process level (a guest command can also exit
  255). Keeping them as normal results with `exit_code` + stderr preserves
  the single "non-zero exit is data" rule; the stderr text
  (`PRL_ERR_IO_STOPPED`, `Unable to authenticate the user…`, …) is the
  signal. The section 6 table was updated to match.
- `powerInputSchema` **appends** `vm_id` to the inferred `required` list
  instead of replacing it: `jsonschema.For` already infers `command` as
  required (no `omitempty`), and the old assignment clobbered it in fleet
  mode. No behaviour change for the power/capture tools (their only
  non-omitempty field is `vm_id` itself).
- Otherwise as specified.
