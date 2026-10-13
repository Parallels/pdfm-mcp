# Spec 029: Exec Command-Line Tokenization (All Guest OSes)

| | |
|---|---|
| Status | Implemented (unit + e2e 2026-10-13; live guest verification pending — see §7, prlctl was not reachable from this session's sandbox) |
| Audience | Implementing agents / engineers |
| Scope | argv construction of `parallels_vm_exec` **and** `parallels_vm_exec_start` (both go through `buildExecArgs`); new `splitCommandLine`; `command`/`user` field descriptions + tool descriptions (no-shell semantics, user/password note); unit + protocol tests |
| Out of scope | `--password` support (stays a follow-up of 005/018), a separate `args: []string` tool input, stdin, per-guest-OS tokenizers |
| Builds on | Spec 005 (its "single argv element" decision is superseded; logging/validation rules stand), 018 (job path reuses `buildExecArgs`), 025 (vm_id contract unchanged) |

## 1. Bug (field report, pdfm-mcp 0.1.1)

On a Windows 11 guest, `parallels_vm_exec` / `parallels_vm_exec_start` fail
for **any command that contains arguments**: `exit_code: 2`, empty
stdout/stderr. Single-token commands (`whoami`,
`C:\Windows\System32\whoami.exe`) work.

Root cause: `buildExecArgs` appended the whole command line as **one** argv
element ("the command is ONE element — no host shell"). On a Windows guest
prlctl takes the first element as the executable *path*, so
`cmd /c ver` is looked up as a file literally named `cmd /c ver` →
Windows error 2 (ERROR_FILE_NOT_FOUND). The reporter confirmed it
independently of the tool, with raw prlctl:

```
prlctl exec "Windows 11" --current-user "cmd /c ver"   → exit=2
prlctl exec "Windows 11" --current-user cmd /c ver     → exit=0
prlctl exec "Windows 11" cmd /c ver                    → exit=0   (elevated)
```

Side note from the report: with `user` set, prlctl prompts for credentials
on the terminal and the call ends `exit 255` / "Unable to authenticate the
user" — expected (no password support, spec 005) but undocumented in the
tool descriptions.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Split for **all** guests | `splitCommandLine` turns the command line into one argv element per argument for every guest OS — not just Windows | One uniform mechanism; the service has no guest-OS knowledge at argv-build time and shouldn't branch; matches prlctl's own usage `prlctl exec <ID\|NAME> <options> <command> [arg...]`; the user's explicit request for the array approach on all hosts |
| Tokenizer | Unquoted whitespace separates; `"` and `'` group to the matching quote; a backslash escapes a following whitespace-or-quote and is otherwise **literal**; unterminated quote or invalid UTF-8 → tool error before exec | One POSIX-like scheme that also preserves Windows paths (`C:\Users\...`) and `$variables`; failures are self-correcting for the model (clear error, nothing runs) |
| No shell, ever | Metacharacters (`\|`, `&&`, `;`, `>`, globs, `$var`) are ordinary characters | The model that needs shell semantics runs a shell explicitly: `sh -c "..."` / `bash -c '...'` (POSIX guests), `cmd /c "..."` / `powershell -NoProfile -Command "..."` (Windows). Documented in the schema |
| POSIX behaviour change | A plain command line on Linux/macOS guests is no longer interpreted by a guest shell (spec 005's "Linux guests run the command via `bash -c`" was an unverified assumption) | Uniformity across hosts; explicit `sh -c` restores shell semantics. Recorded as a known behaviour change in the spec 005 deviations |
| No `args` input | Keep `command` a string | Tokenizing gives the same expressiveness with one knob; a second input would double the documentation surface for little gain |
| `user` + password note | `user` field and both tool descriptions state: prlctl prompts for that user's password on the terminal, which cannot be answered in-band → the call fails with `exit_code 255` ("Unable to authenticate the user") unless the account has no password — the typical Windows outcome | Field-report side note; stops models from retrying a doomed auth |
| Validation | Unchanged bounds (non-empty after trim, no NUL, ≤ 4096 bytes) + new errors: unterminated quote, invalid UTF-8 | Untrusted-input rule (AGENTS.md); all rejections happen before any prlctl call |
| Logging | Unchanged: command text (and its elements) are **never** logged — an element may carry an inline secret | Spec 005 rule |
| Jobs | `parallels_vm_exec_start` inherits the change automatically (same `buildExecArgs`) | One fix point |

## 3. Tokenizer (`splitCommandLine`)

Lives in `internal/parallels/service/vm_exec.go` next to `validateCommand`.
Pure function, no VM knowledge.

Rules, in scan order:

1. Outside quotes: whitespace (space, tab, CR, LF, …) ends the current
   element; `"` / `'` open a quote; `\` escapes the **next** character only
   when that character is whitespace or a quote (the escaped char is kept,
   the backslash dropped), otherwise the backslash is kept literal.
2. Inside double quotes: everything is literal except `\"` and `\\`
   (Windows escape rule; it happens to match POSIX for these two) and the
   closing `"`.
3. Inside single quotes: everything is literal except the closing `'`.
4. An unclosed quote at end of input → `command has an unterminated quote`.
5. A non-decodable UTF-8 byte → `command contains invalid UTF-8` (MCP JSON
   makes this unreachable in practice; defence in depth).
6. Zero elements after scanning → `command must not be empty` (unreachable
   after `validateCommand`; defence in depth).

Quoted examples:

| `command` | argv elements |
|---|---|
| `whoami` | `whoami` |
| `ls -la` | `ls`, `-la` |
| `cmd /c ver` | `cmd`, `/c`, `ver` |
| `powershell -NoProfile -Command "$env:USERNAME"` | `powershell`, `-NoProfile`, `-Command`, `$env:USERNAME` |
| `C:\Windows\System32\whoami.exe` | `C:\Windows\System32\whoami.exe` (one element, backslashes kept) |
| `dir C:\Users\me\docs` | `dir`, `C:\Users\me\docs` |
| `sh -c "echo a && echo b"` | `sh`, `-c`, `echo a && echo b` |
| `powershell -Command 'Get-Process'` | `powershell`, `-Command`, `Get-Process` |
| `echo "a b" c` | `echo`, `a b`, `c` |
| `echo ""` | `echo`, (empty) |
| `echo a\ b` | `echo`, `a b` |
| `echo "a\"b"` | `echo`, `a"b` |

Error examples: `echo "unclosed`, `echo 'unclosed` → unterminated-quote
error before any exec.

## 4. argv shape (after)

```
prlctl exec <ref> [--current-user | --user <name>] [--resolve-paths] <elem1> [<elem2> ...]
```

Options still come **before** the command elements (spec 005). Examples:

| tool `command` | argv tail |
|---|---|
| `whoami` | `--current-user whoami` |
| `cmd /c ver` | `--current-user cmd /c ver` |
| `sh -c "echo a && echo b"` | `--current-user sh -c echo a && echo b` |
| `C:\Windows\System32\whoami.exe` | `--current-user C:\Windows\System32\whoami.exe` |

Elevated (no identity flag) and `--user` forms are unchanged apart from the
split elements.

## 5. Description changes (the schema is the documentation)

`command` (both `parallels_vm_exec` and `parallels_vm_exec_start`):

> guest command line (max 4096 chars): split on unquoted whitespace into
> program + arguments (single/double quotes group; a backslash escapes a
> following space or quote, so Windows paths are safe). No shell is started
> in the guest, so pipes, &&, redirects, globs and $variables are not
> interpreted — to use them, run a shell explicitly: Linux/macOS
> `sh -c "..."`, Windows `cmd /c "..."` or `powershell -NoProfile -Command "..."`

`user` (both tools):

> guest user to run as; defaults to the user currently logged in in the
> guest. Requires that user's password, which this tool cannot provide:
> prlctl prompts for it on the terminal, so the call fails with
> exit_code 255 ("Unable to authenticate the user") unless the account has
> no password — the typical outcome on Windows guests. Mutually exclusive
> with elevated

`descVMExec` / `descVMExecStart` get the same two sentences condensed; the
sequence-step registry picks up `descVMExec` automatically.

## 6. Tests

- **service `vm_exec_test.go`**
  - `TestSplitCommandLine`: table over the §3 examples incl. empty quoted
    args, escaped space/quote/backslash, tab-separated args, and the
    unterminated-quote / invalid-UTF-8 / all-whitespace errors.
  - `TestExecArgvShapes`: cases updated to the split argv (`ls -la` →
    `ls`, `-la`; `cat /host/file` → `cat`, `/host/file`) plus new cases:
    `cmd /c ver`, `sh -c "echo a && echo b"` (payload stays one element),
    Windows path keeps its backslashes.
  - `TestExecValidationBeforeExec`: + unterminated-quote case (0 runner
    calls).
- **tools `parallels_exec_test.go`**: argv assertions updated
  (`cat /host/file` → 2 elements; `uname -a` → `uname`, `-a`).
- **jobs**: `TestExecElevatedArgvShapes` uses single-token `id` — unchanged;
  the job path provably shares the fix via `buildExecArgs`.
- **e2e (hermetic, always on)**: `test/e2e/exec_argv_stub_test.go`
  (`TestExecArgvSplitOnTheWire`) builds the real binary and speaks real
  stdio MCP against a **stub `prlctl`** (bash script on PATH that logs the
  exact argv it receives; no Parallels install needed) and asserts the
  wire argv shape: `cmd /c ver` → `exec <vm> --current-user cmd /c ver`;
  `powershell -NoProfile -Command "$env:USERNAME"` → four elements;
  `sh -c "echo a && echo b"` → payload stays ONE element;
  `C:\Windows\System32\whoami.exe` → ONE element; elevated → no identity
  flag; unterminated quote → tool error with zero prlctl calls.
- **e2e (real machine)**: listing assertions unchanged; the opt-in
  real-exec test (`PDFM_MCP_E2E_EXEC_VM`) runs `echo e2e-exec-ok`, which
  now splits into two elements and must still print `e2e-exec-ok` on a
  POSIX guest — covered by the §7 checklist.

## 7. QA (manual, real machine) — PENDING this session

prlctl could not be executed from this session's sandbox (its bootstrap
wrapper needs `ps`, which the file sandbox blocks; escalation declined), so
the following checklist is recorded and not yet run.

Windows guest (the reporter's reproduction, expect the marked results):

| MCP call | Expected |
|---|---|
| `{vm_id, command: "whoami"}` | exit 0, user printed (regression check, was already OK) |
| `{vm_id, command: "cmd /c ver"}` | **exit 0, version printed — was exit 2** |
| `{vm_id, command: "cmd /c ver & hostname"}` | exit 0, both outputs (cmd re-parses its payload) |
| `{vm_id, command: "powershell -NoProfile -Command \"$env:USERNAME\""`} | exit 0, username — was exit 2 |
| `{vm_id, command: "C:\\Windows\\System32\\whoami.exe"}` | exit 0 (backslash path stays one element) |
| `{vm_id, command: "cmd /c ver", elevated: true}` | exit 0 (elevated form of the same fix) |
| `{vm_id, command: "whoami", user: "<any normal account>"}` | exit 255, "Unable to authenticate the user" (documents the new description) |

POSIX guest:

| MCP call | Expected |
|---|---|
| `{vm_id, command: "uname -r"}` | exit 0 (multi-element on a POSIX guest) |
| `{vm_id, command: "sh -c \"echo a && echo b\""`} | exit 0, `a` and `b` on two lines (explicit shell) |
| `{vm_id, command: "ls --version \| head -1"}` | **non-zero exit** with ls complaining about the literal `\|` argument — the documented no-shell semantics |
| `PDFM_MCP_E2E_EXEC_VM=<vm> make test-e2e` | `echo e2e-exec-ok` still prints (2-element exec on POSIX) |

Raw-prlctl cross-checks (host shell):

```
prlctl exec <vm> --current-user cmd /c ver        # split form the tool now produces
prlctl exec <vm> --current-user sh -c 'echo a && echo b'
```

## 8. Unknowns / risks

1. **Guest-agent argument rejoining.** The guest agent receives
   `(program, args...)` and must reassemble a native command line. Unquoted
   multi-arg invocations are field-verified on Windows (`cmd /c ver`).
   Payloads containing spaces inside one argument
   (`powershell -Command "Get-Content C:\Program Files\..."`) depend on the
   agent joining with spaces — verify with the §7 Windows row 5 analogue
   before assuming it.
2. **POSIX guests: is the guest shell gone for good?** Spec 005 assumed
   `bash -c`; that was never verified. If the Linux agent shells *only* a
   single-element command, multi-element execs may behave differently than
   the POSIX checklist assumes — the checklist is designed to detect it
   (row 1 is the canary: it must still work).
3. `--resolve-paths` with split elements is assumed to scan each element;
   unverified (also unverified for the single-element form, spec 005).

## 9. Follow-ups

- `args: []string` as an explicit alternative input, if models keep hitting
  tokenizer corners.
- Password authentication (005/018 follow-up) — would also fix `user`.
- Per-guest-OS tokenization only if §7 reveals a guest that needs it.

### Deviations (fill in during implementation)

- One bug found and fixed by the hermetic wire test during
  implementation: the closing quote was initially written into the element
  (the `quote != 0` branch fell through to the write); the tokenizer now
  consumes it. Caught by `TestSplitCommandLine`, not by the original
  argv-shape table alone.
- `make test-e2e` on this session's machine fails in
  `TestParallelsGating` at `parallels_list_vms` because prlctl cannot run
  inside the session sandbox (its bootstrap wrapper needs `ps`; escalation
  was declined). Verified the **identical** failure on a clean HEAD
  worktree — environmental, not a regression. `TestExecArgvSplitOnTheWire`
  (hermetic stub) passes in the same environment.
- Otherwise as specified.
