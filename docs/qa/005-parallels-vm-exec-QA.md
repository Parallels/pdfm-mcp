# QA — Parallels VM command execution (spec 005)

## What this task delivers

- `parallels_vm_exec` tool (both modes): runs
  `prlctl exec <vm> <options> <command>` and returns `exit_code`, `stdout`
  and `stderr` as structured output.
- Identity defaults to `--current-user` (the user logged in inside the
  guest); `user` overrides via `--user`. **No password support by design**
  (secrets must not ride in argv/transcripts).
- `resolve_paths` → `--resolve-paths`.
- Non-zero guest exits are **results**, not errors. Tool errors only when
  prlctl itself fails (unknown/stopped VM, timeout, launch failure).
- Bounds: command ≤ 4096 chars (no NUL), 300 s timeout, stdout 1 MB /
  stderr 256 KB result caps with `stdout_truncated` / `stderr_truncated`
  flags, 10 MB runner hard cap.
- The command is one argv element — never a host shell; the guest OS is the
  trust boundary. The command text is never logged.
- `docs/specs/005-parallels-vm-exec.md` (design) + this QA spec.

**Safety note.** This tool runs arbitrary commands inside the guest as the
logged-in user — including destructive ones (`rm -rf`, package removal,
service changes). Confirm with the user before running anything beyond
read-only inspection commands, exactly as you would on a shared machine.
The automated suites only ever run `echo` / `exit` (opt-in, never
default-on).

## Prerequisites

- Same host as QA 002–004 (Parallels Desktop, `prlctl` on PATH), Go
  toolchain.
- **A running VM with Parallels Tools installed and a logged-in user** for
  the positive steps (a stopped VM or a headless guest without a session
  cannot use `--current-user`; prlctl refuses and the tool surfaces the
  error).

Build once for all steps:

```sh
cd pdfm-mcp
go build -o bin/pdfm-mcp ./cmd/pdfm-mcp
```

## Step-by-step

### 1. Automated suites (run these first)

```sh
make check       # fmt, vet, lint, CGO_ENABLED=1 go test -race ./...
make test-e2e    # asserts parallels_vm_exec is LISTED in both modes; no
                 # exec is ever executed
PDFM_MCP_E2E_EXEC_VM=<running-vm> make test-e2e   # opt-in: real `echo`
                 # (exit 0, stdout check) and `exit 3` (non-error result
                 # with exit_code 3)
```

Expected: all green. Unit suites cover argv shapes, non-zero exits,
validation-before-exec, truncation (including rune-boundary safety), the
300 s deadline, and cache non-invalidation.

### 2. Fleet mode — contract and a read-only exec

```sh
./bin/pdfm-mcp        # no --vm-id
```

1. `tools/list` → `parallels_vm_exec` present; `inputSchema.required`
   contains `vm_id` **and** `command`; annotations:
   `readOnlyHint: false`, `destructiveHint: true`.
2. `parallels_vm_exec` `{"vm_id": "<running-vm>"}` (no command) → schema
   validation error naming `command`.
3. `{"vm_id": "<running-vm>", "command": "id"}` → normal result:
   `exit_code: 0`, `stdout` with the uid line, `stderr` empty,
   `vm_id` echoes the target.
4. `{"vm_id": "<running-vm>", "command": "grep -q def /etc/hostname"}`
   (or any command known to fail) → **not** an error: `exit_code`
   non-zero, output fields intact.
5. `{"vm_id": "<running-vm>", "command": "echo out; echo err 1>&2; exit 7"}`
   → `exit_code: 7`, both streams populated (verifies stdout/stderr
   routing).
6. `command: ""` and a 4097-char command → tool errors, no prlctl run.

### 3. VM mode — target contract

```sh
./bin/pdfm-mcp --vm-id <running-vm>
```

1. `parallels_vm_exec` `{"command": "id"}` → runs in the configured VM;
   `inputSchema.required` does **not** contain `vm_id`.
2. `{"vm_id": "<other-vm>", "command": "id"}` → tool error "vm_id must be
   omitted"; nothing runs.

### 4. Identity and options

1. `{"command": "whoami"}` → returns the guest's logged-in user
   (`--current-user` default).
2. `{"command": "whoami", "user": "<guest-user>"}` → `--user` path
   (needs that user to exist; without a password the login must not
   require one — otherwise prlctl's error is surfaced).
3. `{"command": "cat <host-path>", "resolve_paths": true}` on a VM with
   shared folders → path converted; without shared folders document
   prlctl's error.

### 5. Failure paths (VM state unchanged)

prlctl-level failures come back as **normal results with `exit_code: 255`**
and prlctl's stderr (prlctl's own failures are not distinguishable from a
guest command exiting 255 — see spec 005 Deviations).

1. `{"vm_id": "<stopped-vm>", "command": "id"}` →
   `exit_code: 255`, stderr
   `PrlVm_TerminalConnect: PrlJob_Wait: PRL_ERR_IO_STOPPED` (verified on
   Parallels 27).
2. `{"vm_id": "no-such-vm", "command": "id"}` → `exit_code: 255`, stderr
   `Failed to get VM config: The virtual machine could not be found…`
   (verified).
3. A guest without Parallels Tools or without a logged-in session →
   `exit_code: 255`, stderr `Unable to authenticate the user. Make sure
   that the specified credentials are correct and try again.` (verified on
   a headless guest; `--use-advanced-terminal` does not change it).

### 6. Cache and timeouts

1. Call `parallels_list_vms` right after an exec: `source: "cache"` — exec
   does not invalidate the list cache.
2. Long command (`sleep 310`) → tool error "(command timed out)" after
   ~5 minutes (skip in a hurry: the unit test asserts the 300 s deadline).

## Checklist

| # | Check | Where |
|---|---|---|
| 1 | Listed in both modes, absent when module disabled | e2e + protocol tests |
| 2 | Fleet: `vm_id`+`command` required; VM: configured target, explicit rejected | protocol + unit tests, steps 2–3 |
| 3 | argv: `exec <vm> [--current-user\|--user u] [--resolve-paths] <cmd>`, options before command | unit tests |
| 4 | Non-zero exit → normal result with exit_code/stdout/stderr | unit + protocol + opt-in e2e |
| 5 | No password argument; `--current-user` default | code review + step 4 |
| 6 | Command bounds (empty/NUL/4096) enforced before any exec | unit tests + step 2.6 |
| 7 | 300 s deadline applied; caller ctx wins | service test + step 6 |
| 8 | stdout 1 MB / stderr 256 KB caps, rune-safe, flagged | service tests |
| 9 | Stopped/unknown VM / no tools → prlctl error, no result | step 5 |
| 10 | Cache untouched by exec | service test + step 6.1 |
| 11 | Command text never logged | code review (service logs vm/exit/sizes only) |
| 12 | Output schema portable (no null/array unions) | code review + schema |

## Known follow-ups

- Password authentication (needs a non-argv secret channel).
- Streaming/interactive sessions, `--use-advanced-terminal`.
- Per-call timeout argument.
