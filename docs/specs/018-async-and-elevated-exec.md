# Spec 018: Async Exec Jobs and Elevated Exec

| | |
|---|---|
| Status | Implemented (local + live verification complete 2026-10-09 on a running Ubuntu VM) |
| Audience | Implementing agents / engineers |
| Scope | `elevated` flag on `parallels_vm_exec`; `parallels_vm_exec_start` / `parallels_vm_exec_result` / `parallels_vm_exec_cancel`; streaming runner seam; in-service job manager; tests; QA doc |
| Out of scope | `--password`, interactive sessions, stdin to the guest, persistence of jobs across server restarts, jobs as sequence steps |
| Builds on | Spec 005 (`parallels_vm_exec`, decisions there stand: no password argument, command never logged) |

## 1. Goal

Close two gaps, using only `prlctl exec`:

1. **Elevated exec**: run a command as the guest's system account.
2. **Async jobs**: run commands longer than the 5-minute synchronous cap
   (installers, builds) and read their output while they run.

### Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Elevation mechanism | `elevated: true` runs `prlctl exec <vm> [--resolve-paths] <cmd>` with **neither** `--current-user` **nor** `--user` | Without an identity flag prlctl executes as the guest's system account (root on Linux/macOS, SYSTEM on Windows). **Verified 2026-10-09: `whoami` → `root` (uid 0) on an Ubuntu guest** |
| `elevated` + `user` | Validation error (`elevated and user are mutually exclusive`) | One identity knob at a time |
| Password | Still no password argument (spec 005 stands) | Secrets must not ride in argv/transcripts |
| Default identity | Unchanged: `--current-user` | Backward compatible |
| Privilege signalling | Tool descriptions say **PRIVILEGED**; annotations stay `DestructiveHint: true`, `ReadOnlyHint: false` | Clients/policies can gate it; the policy layer (spec 019) classifies the new tools |
| Service API | `Exec(...)` unchanged; new `ExecWithOptions(ctx, vm, cmd, ExecOptions{User, Elevated, ResolvePaths})`; `Exec` delegates to it | No churn for existing callers/tests |
| Async tools | Three tools: start (returns `job_id`), result (poll / long-poll / incremental read), cancel | Job ids are the only state the client holds |
| Mode contract | Start: standard (`resolvePowerTarget`; fleet requires `vm_id`, VM mode uses the target and rejects an explicit one). Result/cancel take only `job_id`; in VM mode only jobs of the configured target are visible; in fleet mode a job is bound to its VM and reports `vm_id` | A single-VM server must not read other VMs' jobs |
| Not sequence steps | The three tools are NOT registered in `RegisterSteps`; the drift guard excludes them | Steps have no templating, so a `job_id` cannot flow from start to result. `parallels_vm_exec` (incl. `elevated`) remains a step |
| Streaming seam | Optional interface `StreamRunner{ Start(ctx, exe, args...) (Process, error) }` implemented by `realRunner`; the service type-asserts it | Does not break other `CommandRunner` implementers; both in-repo fakes implement it |
| Process | `Stdout()/Stderr()` live bounded `*OutputBuffer`s, `Done()`, `Result()`, `Kill()` | Partial output readable while running |
| stdout rule | Child stdin is /dev/null, stdout/stderr go to the buffers, never inherited | stdout belongs to the MCP protocol |
| Job context | Jobs run on a server-lifetime context (with the job timeout), NOT the request context | The tool request context ends when the call returns |
| Limits | 4 running, 32 retained jobs; finished jobs expire after 1 h (lazy cleanup on every job operation); retained output 4 MB stdout / 1 MB stderr per job (excess drained and dropped, flagged) | Bounded memory, no unbounded growth |
| Timeout | `timeout_seconds` default 1800, max 14400; exceeding it kills prlctl, state `timed_out` | |
| Ids | 128-bit `crypto/rand`, 32 lowercase hex chars; input validated before lookup | Unguessable, injection-safe |
| Logging/storage | Never store or log the command text; jobs keep only vm ref, user/elevated flags, timestamps, state, exit code. Logs carry job id, vm, elevated, state, exit code | Same rule as spec 005 |
| Shutdown | `parallels.Shutdown()` (called from `cmd/pdfm-mcp/main.go` on exit/signal) kills all running jobs and refuses new ones; `Reset()` (tests) does the same | No orphaned prlctl processes |

## 2. Layout (delta)

```
internal/parallels/interfaces/interfaces.go   # + ExecOptions, ExecJob* types, job state consts, 4 service methods
internal/parallels/parallels.go               # + re-exports (StreamRunner, Process, OutputBuffer, limits, Shutdown)
internal/parallels/service/process.go         # NEW  OutputBuffer, Process, StreamRunner, realRunner.Start
internal/parallels/service/jobs.go            # NEW  job manager + StartExecJob/ExecJobResult/CancelExecJob/Shutdown
internal/parallels/service/vm_exec.go         # buildExecArgs, ExecWithOptions
internal/parallels/service/service.go         # jobs field, Reset shuts jobs down
internal/tools/parallels_vm_exec.go           # + elevated
internal/tools/parallels_vm_exec_jobs.go      # NEW  start/result/cancel tools
cmd/pdfm-mcp/main.go                          # defer parallels.Shutdown()
```

## 3. Tool contracts

`parallels_vm_exec` / `parallels_vm_exec_start` input: `vm_id` (fleet), `command`,
`user`, `elevated`, `resolve_paths`; start adds `timeout_seconds` (0 = 1800,
1..14400). Start output: `vm_id, job_id, state ("running"), elevated, timeout_seconds`.

`parallels_vm_exec_result` input: `job_id`, `wait_ms` (0-30000, long-poll for
completion), `stdout_offset`, `stderr_offset` (bytes already consumed).
Output: `vm_id, job_id, state` (`running|succeeded|failed|cancelled|timed_out`),
`elevated`, `exit_code` (-1 while running or when no exit code exists),
`message`, `elapsed_ms`, `stdout`, `stderr` (slices, at most 256 KB per stream
per call, rune-safe: an incomplete trailing UTF-8 sequence is held back until
more bytes arrive), `stdout_next`, `stderr_next` (offsets to pass next time),
`stdout_has_more`, `stderr_has_more` (retained bytes remain, read again),
`stdout_truncated`, `stderr_truncated` (bytes beyond the retention cap were
dropped). Offsets count raw bytes; offsets beyond the end are clamped.

`parallels_vm_exec_cancel` input: `job_id`. Output: `vm_id, job_id, state,
was_running, exit_code`. Cancelling a finished job is a no-op reporting its
state. Annotations: start/cancel destructive; result read-only.

State rules: cancel requested -> `cancelled`; job context deadline ->
`timed_out`; server shutdown -> `cancelled`; wait error -> `failed` with
`message`; exit 0 -> `succeeded`; other exit codes -> `failed`.

## 4. Unknowns (verified 2026-10-09 on a real Mac + Ubuntu guest, see QA 018)

1. **`prlctl exec` without `--current-user`/`--user` runs as the system
   account — CONFIRMED.** `elevated: true` with `whoami` printed `root`
   (uid 0) on the Ubuntu guest; no credentials prompt. No rework needed.
2. **Killing the host `prlctl exec` process:** host-side job handle dies
   (state `cancelled`, no further output). The confirming guest `ps` hit a
   transient guest-side `PrlJob_GetRetCode: Invalid argument` flake (see
   QA 018), so the documented caveat stands: the guest command may survive.
3. Behaviour of `prlctl exec` when the VM is stopped/suspended mid-job —
   not re-tested live (would stop a shared VM); unit tests cover the
   non-zero exit → `failed` mapping.
4. **Guest output streaming — CONFIRMED to be completion-delivered for
   tested commands:** mid-flight `exec_result` reads were empty until the
   guest command finished, then the full output arrived at once. The
   offset/has_more/256 KB machinery works; live trickle would need a
   guest command that flushes incrementally.
5. **New observation (guest-side, not ours):** a transient
   `PrlJob_GetRetCode: Invalid argument` (exit 255) from the guest
   Parallels Tools job API occurred twice (always recovered on retry; raw
   prlctl of the same command always worked). Recorded in QA 018.

## 5. Tests

- Service: argv shapes (elevated, elevated+resolve, default, user),
  elevated+user rejection, lifecycle with a fake streaming process
  (running, partial reads, success, failed exit code, cancel, timeout),
  timeout validation, 5th concurrent job rejected, retention eviction,
  expiry with an injected clock, offsets and 256 KB caps, `OutputBuffer`
  bound and rune safety, VM-mode scoping, `Shutdown` kills jobs, concurrent
  access (`-race`), logs never contain the command text.
- Tools: elevated reaches argv, elevated+user error, in-memory protocol
  tests in both modes (schemas, annotations, lifecycle, validation errors),
  job outliving the request context. The drift guard excludes the three job
  tools with an explanatory comment.

## 6. Known follow-ups

Job templating in sequences; persisting jobs across restarts; a stdin channel;
password authentication.
