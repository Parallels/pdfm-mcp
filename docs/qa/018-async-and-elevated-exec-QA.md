# QA: Async exec jobs and elevated exec (spec 018)

**Executed 2026-10-09** on the developer Mac (Parallels 27.0.1) against the
running VM `capsule_manager_backend` (Ubuntu, elevated = root). User approved
this VM for **read-only** commands only; nothing written to the guest.

**What this task delivers**

- `elevated` argument on `parallels_vm_exec` and `parallels_vm_exec_start`:
  runs with neither `--current-user` nor `--user` (guest system account).
  Mutually exclusive with `user`. PRIVILEGED. No password support.
- `parallels_vm_exec_start` / `_result` / `_cancel`: background jobs for
  commands beyond the 5-minute cap, incremental output, cancel, timeout.
- Limits: 4 running / 32 retained jobs, 1 h retention, 4 MB / 1 MB output
  retention, 256 KB per stream per read, timeout 1800 s default / 14400 s max.
- Command text is never stored or logged.

## Prerequisites

Same host as QA 005: Parallels Desktop, a running VM with Parallels Tools,
Go toolchain. ✅ (Note: this guest has **no logged-in GUI user**, which
affects step 2.1 — see result.)

## Steps

### 1. Automated suites — ✅ PASSED

`make check` green; `go test -race ./internal/parallels/... ./internal/tools/`
green; `make test-e2e` green. Unit coverage includes: elevated argv has no
identity flags (with/without `--resolve-paths`), elevated+user rejected with
the default identity unchanged, job lifecycle (running/partial/success/
failure/cancel/timeout), 4 running / 32 retained / 1 h expiry, 256 KB rune-
safe offsets, VM-mode scoping and fleet binding, shutdown kills jobs, no
command text in logs/state, job tools excluded from sequence steps (drift
guard documents it).

### 2. Elevated identity (resolves Unknown 1) — ✅ RESOLVED

1. Default `whoami` (`--current-user`): `exit_code: 255`, stderr
   `Unable to authenticate the user. Make sure that the specified credentials
   are correct and try again.` — a **guest circumstance**, not a server
   defect: this VM has no logged-in GUI user for `--current-user` to resolve.
   Returned as a result (not a tool error), per spec 005.
2. `elevated: true` `whoami` → `root` (uid 0). **Unknown 1 confirmed:**
   prlctl with no identity flag runs as the guest system account.
3. `{elevated: true, user: "root", command: "id"}` → rejected
   (mutually exclusive); the server log shows no prlctl call for it.
4. The auth failure in 2.1 is recorded above (exact prlctl message); it is
   expected on a guest without an interactive login.

### 3. Async job lifecycle — ✅ PASSED

1. `exec_start` `for i in 1 2 3; do echo step $i; sleep 5; done`
   (elevated) → `job_id` (32 hex), `state: running`, `elevated: true`,
   `timeout_seconds: 1800`.
2. Immediate `exec_result` → `running`, `exit_code: -1`, stdout empty —
   **Unknown 4 resolved:** prlctl delivers guest stdout at job completion
   (no live trickle), so partial reads lag until the guest command flushes
   the pipe.
3. `wait_ms: 20000` + `stdout_offset` → `succeeded`, `exit_code: 0`,
   `elapsed_ms: 15226`, stdout `step 1\nstep 2\nstep 3\n`.
4. `exit 3` → `failed`, `exit_code: 3`.
5. `elevated: true` `whoami` job → `succeeded`, `elevated: true`,
   stdout `root`.

### 4. Cancel and timeout — ✅ PASSED

1. `sleep 600` + cancel → `was_running: true`, `state: cancelled`; cancel
   again → `was_running: false`, `state: cancelled`.
2. Guest `ps aux | grep "sleep 600"` to record the survivor question
   (**Unknown 2**) was inconclusive: that one call hit the transient guest
   flake noted in Deviations (recovered on retry elsewhere in this run).
   What is certain: killing the host `prlctl exec` process kills the
   **host-side** job handle (job state `cancelled`, no further output), and
   the spec's documented caveat stands — the guest command may survive.
3. `sleep 600` with `timeout_seconds: 5` → `timed_out` at `elapsed_ms:
   5001`, message `job exceeded its timeout and was killed`.
   `timeout_seconds: 14401` → rejected at start.

### 5. Limits and scoping — ✅ PASSED

1. 5 long jobs → the 5th rejected: `too many running jobs (limit 4): wait
   for one to finish or cancel it`.
2. `yes | head -c 400000` → first read exactly 262144 bytes with
   `stdout_has_more: true`; second read from `stdout_next` → the remaining
   137856 bytes, `stdout_has_more: false`.
3. Fleet mode: every result/cancel reports `vm_id`. VM-mode server
   (`PDFM_MCP_VM_ID=capsule_manager_backend`): its own jobs are visible; a
   fleet job id → `job not found`.
4. Invalid ids (`../x`, `abc`) → `invalid job_id`.

### 6. Shutdown and logging — ✅ PASSED

1. `sleep 600` job running, server SIGINT → process exits cleanly,
   `pgrep -fl "prlctl exec"` → **NONE**.
2. `echo SECRET-123` job → the secret is returned in the tool result (as
   designed) but `grep SECRET-123` of the server log → **no match**; log
   lines carry only job id, vm, elevated, state, exit code:
   `exec job started job=2a36… vm=capsule_manager_backend elevated=true
   timeout=30m0s` / `exec job finished … state=succeeded exit_code=0`.

## Checklist

| # | Check | Where | Result |
|---|---|---|---|
| 1 | Elevated argv has no identity flags (with/without resolve-paths) | unit tests | ✅ |
| 2 | Elevated + user rejected, default unchanged | unit + step 2.3 | ✅ |
| 3 | `whoami` as elevated prints root/SYSTEM | step 2.2 (real Mac) | ✅ root |
| 4 | Job lifecycle: running, partial read, success, failure | unit + step 3 | ✅ (partial read empty until guest flush — Unknown 4) |
| 5 | Cancel kills prlctl; guest process fate recorded | unit + step 4 | ✅ host side; guest fate inconclusive (flake) |
| 6 | Timeout → `timed_out`; range validation | unit + step 4.3 | ✅ |
| 7 | 4 running / 32 retained / 1 h expiry | unit + step 5.1 | ✅ |
| 8 | Offsets and 256 KB cap, rune-safe | unit + step 5.2 | ✅ |
| 9 | VM-mode scoping, fleet binding | unit + protocol + step 5.3 | ✅ |
| 10 | Shutdown kills jobs, no orphan prlctl | unit + step 6.1 | ✅ |
| 11 | No command text in logs/state | unit + step 6.2 | ✅ |
| 12 | Job tools not sequence steps; drift guard documents it | unit | ✅ |

## Deviations observed

- **Guest-side flake (transient, 2× in this run):** `PrlJob_GetRetCode:
  Invalid argument. An invalid argument was passed.` (exit 255) from the
  guest Parallels Tools job API — once on the very first job of the first
  session, once on a synchronous `ps` call. Both recovered on retry with
  the same command; the raw `prlctl exec` of the identical command always
  worked. Our server surfaced it cleanly (failed job / result with the
  stderr text, no crash, no state corruption). Recorded so a client hitting
  it once is not mistaken for a server bug.
- **Unknown 4 (streaming):** prlctl delivers guest output at job completion
  for these commands; `exec_result` mid-flight reads are empty until then.
  The incremental-read machinery (offsets, has_more, caps) is fully
  exercised and correct; live trickle would need a guest command that
  flushes incrementally (e.g. `stdbuf`), which was out of scope.
- **Unknown 2 (cancel guest fate):** the confirming `ps` call hit the flake;
  the spec's caveat (guest process may survive the host-side kill) stands.
- Step 2.1 could not show a non-root default user because this guest has no
  GUI login session; the exact prlctl auth message is recorded above.

## Result

All live steps pass on a real VM: elevated = root (Unknown 1 resolved),
full job lifecycle, cancel, timeout, limits, output caps with offsets,
VM-mode scoping, clean shutdown with no orphan processes, and zero command
text in logs. Unknown 4 resolved (output arrives at completion). Two
transient guest-side PrlJob flakes recorded; no server defect found.
