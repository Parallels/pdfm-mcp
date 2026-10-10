# Spec 011: Parallels VM Encryption (`encrypt`, `decrypt`, `change-passwd`)

| | |
|---|---|
| Status | Implemented |
| Audience | Implementing agents / engineers |
| Scope | Three VM-scoped tools wrapping the Parallels encryption lifecycle: `parallels_vm_encrypt`, `parallels_vm_decrypt`, `parallels_vm_change_encryption_password`. Passwords are fed to prlctl over stdin (never argv, never logs). |
| Depends on | 002 (module, cache, mode contract), 003 (power pattern: resolvePowerTarget, cache invalidation) |
| Out of scope | Encrypted-VM boot/restore flows, `prlsrvctl` server tools, snapshots, key management |

## 1. Goal

Expose the Parallels VM encryption lifecycle as MCP tools. All three CLI
commands are interactive (prlctl prompts for a password), but live probing
on Parallels 27.0.1 showed prlctl reads the password **from stdin when
stdin is not a TTY** — one line for encrypt/decrypt, two lines (current,
new, no confirmation) for change-passwd. That makes all three fully
scriptable through the existing `CommandRunner.RunStdin`.

### Live-verified protocol facts (Parallels 27.0.1, disposable VM
`empty_catalog_test`, full round-trip executed and VM restored)

- `encrypt <vm>` + 1 stdin line → `The virtual machine has been successfully encrypted.` (exit 0); an interrupted encrypt rolls back cleanly (VM returned to `Encrypted: no`).
- `encrypt <vm> --dry-run` → non-interactive: `All prerequisites for the virtual machine encryption have been met` (exit 0); running VM → `...currently running...` (exit 255).
- `decrypt <vm>` + 1 stdin line → success message (exit 0); wrong password → `The password you specified is invalid...` (exit 255, VM stays encrypted); not encrypted → `Virtual machine is not encrypted` (exit 255).
- `change-passwd <vm>` + 2 stdin lines → `The password has been successfully changed.` (exit 0); not encrypted → `Virtual machine is not encrypted` (exit 255, checked before stdin is read).
- prlctl never echoes the password in its output (safe stderr pass-through).
- State is visible in the list JSON: `Security.Encrypted` = `"yes"`/`"no"`.

### Acceptance criteria

1. `make check` and `make test-e2e` pass. E2e executes only the
   **non-mutating** calls: `encrypt --dry-run` and `decrypt --dry-run`
   precondition checks (outcome asserted from `Security.Encrypted` of a
   stopped VM — deterministic either way). Real encrypt/decrypt/
   change-password are **never** executed in automated tests (they are
   secret-bearing state mutations; manual QA only).
2. All three tools listed in both modes with the standard mode contract:
   fleet **requires** `vm_id`; VM mode targets `--vm-id` and rejects an
   explicit `vm_id`.
3. Passwords reach prlctl only via stdin: `encrypt`/`decrypt` send one
   line, `change_encryption_password` sends two lines (`current\nnew\n`). Passwords
   never appear in argv, in slog output, or in error messages built by
   this server.
4. `password` is required for encrypt/decrypt **except** with
   `dry_run: true`; `current_password` and `new_password` are both
   required for change_encryption_password. Passwords are validated: non-empty,
   printable ASCII, ≤ 256 chars (no trimming — spaces are significant).
5. On success the VM cache is invalidated (the `Security.Encrypted`
   field changed).

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Password transport | **stdin via `RunStdin`**, one line (encrypt/decrypt) or two (change-passwd), each terminated by `\n` | Verified non-TTY behaviour; keeps secrets out of argv (`ps`-visible) and logs |
| `dry_run` | `encrypt`/`decrypt` only; `change-passwd` has no dry-run in the CLI | Mirrors the CLI exactly |
| Password with `dry_run: true` | Ignored (sent as empty stdin? **No** — the dry-run path never feeds stdin; password validation is skipped) | prlctl's dry-run reads no password (verified) |
| Validation | New `ValidatePassword` in the service: len 1–256, all runes in 0x20–0x7e; **no TrimSpace** | Same threat model as `ValidateVMRef`; trimming would silently alter a legitimate password |
| Timeouts | Inherited from the request context, **no fixed cap** | A real encrypt time scales with VM size (minutes–hours on large VMs); a fixed cap would corrupt long legitimate runs. The client's request timeout governs |
| Success output | prlctl's stdout, trimmed (usually the one-line success message; real encrypt may append progress) | Honest, minimal; agents can match on the message |
| Cache | `invalidate()` after a successful encrypt/decrypt/change-passwd | `Security.Encrypted` (or the VM's state) changed |
| Mode contract | Standard: `powerInputSchema` + `resolvePowerTarget`, registered in both modes | Consistency with every other VM-scoped tool |
| Sequence steps | All three registered in `tools.RegisterSteps` (VM-scoped) | Repo rule; the drift guard enforces it. Steps may carry password args — the caller's responsibility |
| Annotations | `ReadOnlyHint: false`, `DestructiveHint: true`, `IdempotentHint: false`, `OpenWorldHint: true` for all three | Encrypt locks data behind a password; decrypt irreversibly removes the lock once the old password is forgotten; change-passwd invalidates the current password |
| Tool rename (2026-10-10) | `parallels_vm_change_password` → `parallels_vm_change_encryption_password`; the description now states explicitly that this is a disk-encryption password, not a guest OS/account/remote-desktop password | Live observation with a lower-capability model: it invoked the bare-named tool while asked to change a power state / guest password — the name reads as a guest account password, but the tool only touches the disk-encryption password. No alias: the project is pre-1.0, MCP clients discover tool names at runtime, and the ambiguity was worse than a rename |

## 3. Repository layout (delta)

```
internal/
├── parallels/
│   ├── interfaces/interfaces.go      # + Encrypt / Decrypt / ChangePasswd
│   └── service/
│       ├── service.go                # ValidatePassword (or new encrypt.go)
│       ├── encrypt.go                # service methods (new file)
│       └── encrypt_test.go           # service-level: argv, stdin payload, not-logged, cache invalidation
├── tools/
│   ├── descriptions.go               # + descVMEncrypt / descVMDecrypt / descVMChangeEncryptionPassword
│   ├── parallels_vm_encrypt.go       # tool + Register (both modes)
│   ├── parallels_vm_decrypt.go
│   ├── parallels_vm_change_encryption_password.go
│   ├── parallels_encrypt_test.go     # handler + protocol tests (both modes, dry-run, validation)
│   └── steps.go                      # + 3 actions in RegisterSteps
├── server/server.go                  # register all three in both modes
├── test/e2e/stdio_test.go            # listing + dry-run precondition checks (read-only, default-on)
└── docs/
    ├── specs/011-parallels-vm-encryption.md
    └── qa/011-parallels-vm-encryption-QA.md
```

## 4. Service layer

```go
// interfaces
Encrypt(ctx context.Context, vmRef, password string, dryRun bool) (string, error)
Decrypt(ctx context.Context, vmRef, password string, dryRun bool) (string, error)
ChangePasswd(ctx context.Context, vmRef, currentPassword, newPassword string) (string, error)
```

- `enabled` check; `ValidateVMRef(vmRef)`.
- `dryRun == true`: `Run(ctx, prlctl, "encrypt", ref, "--dry-run")` — no
  stdin, no password validation.
- `dryRun == false`: `ValidatePassword(password)` (or both for
  change-passwd), then `RunStdin(ctx, prlctl, "<password>\n", "encrypt",
  ref)` / `RunStdin(ctx, prlctl, "<current>\n<new>\n", "change-passwd",
  ref)`.
- Log lines include `ref` and `dry_run` — **never** password material.
- Non-zero exit → error with prlctl's stderr (its messages contain no
  secret; verified).
- `s.cache.invalidate()` after success.

## 5. Tool layer

- `VMEncryptInput{VMID, Password string, DryRun bool}`; same shape for
  decrypt; `VMChangeEncryptionPasswordInput{VMID, CurrentPassword, NewPassword
  string}` (no omitempty on the secret fields → always required).
- Handler: `resolvePowerTarget` → service call →
  `VMEncryptOutput{DryRun bool, Output string}` (trimmed prlctl stdout).
- Description (shared constants for the step registry): what it does,
  stopped-VM requirement, password semantics, dry-run meaning + mode
  suffix via `powerDescription`.
- Password requiredness with dry-run: the input schema cannot express
  "required unless dry_run" — `password` is declared required in the
  schema, and the handler accepts an **empty** password only when
  `dry_run` is true (documented in the description; a dry-run call with a
  password is also fine — it is ignored).

## 6. Security

- Secrets flow: client → tool argument → stdin of a child process. Not in
  argv (`ps`), not in logs, not in server-built error strings. prlctl's
  own stderr is passed through as with every other tool and was verified
  to never contain the password.
- `DestructiveHint: true` on all three. Real runs never happen in
  automated tests.
- Arguments are untrusted: refs and passwords validated and bounded before
  use; refs are single argv elements.

## 7. Testing

- Service (fake runner): exact argv in all shapes; stdin payload exactly
  `pw\n` / `cur\nnew\n`; no password in any logged/debug string; invalid
  password rejected before any run; success invalidates the cache;
  non-zero exit surfaces prlctl's stderr.
- Tools/protocol (in-memory): both-mode listing and required-args
  contract (fleet: `["vm_id","password"]` / `["vm_id","current_password",
  "new_password"]`; VM mode without the `vm_id`), dry-run success path,
  dry-run without password accepted, real call without password rejected,
  VM-mode explicit `vm_id` rejected.
- E2e (real prlctl, read-only, default-on): all three listed in both
  modes; pick the first **stopped** VM and run `encrypt --dry-run` +
  `decrypt --dry-run`; assert the outcome from `Security.Encrypted`
  (not encrypted → encrypt dry-run succeeds and decrypt dry-run is a
  clean tool error; encrypted → the reverse). No password is ever sent
  in e2e.

## 8. QA (manual, real machine)

`docs/qa/011-parallels-vm-encryption-QA.md` — includes the disposable-VM
round-trip procedure already executed once (encrypt → wrong-password
decrypt → change-passwd → decrypt → verify `Security.Encrypted: no`).

### Deviations (fill in during implementation)

1. **Password is omitempty in the input schema, enforced by the handler**
   (not `required` in the schema). A dry-run without a password is a
   common, natural call; declaring the password `required` in JSON
   Schema would make the schema reject it. Same convention as `vm_id`
   in VM mode: the schema stays permissive, the handler rejects a real
   run with an empty password ("password is required for this
   operation (only a dry_run call may omit it)") before prlctl runs.
   `change_encryption_password` is analogous for both fields.
2. **Dry-runs on an *encrypted* VM require the current password.**
   Live verification showed both `encrypt --dry-run` and
   `decrypt --dry-run` fail with "The password you specified is
   invalid" when no password is fed on an already-encrypted VM — the
   precondition check authorises the session. The e2e assertion for the
   encrypted case is therefore "both dry-runs are clean tool errors"
   (no password is ever sent by automated tests), not "decrypt dry-run
   succeeds."
3. **Dry-runs do not invalidate the cache.** Only real runs flip
   `Security.Encrypted`; a successful dry-run changes nothing, so
   invalidating on it would only force an unnecessary re-fetch.
4. **Real encrypt/decrypt runs are bounded by a 4 h backstop
   (`longDiskOpTimeout`)** instead of being purely "unbounded." The
   request context was probe-verified to carry **no deadline**, which
   meant the runner's 30 s default cap would have aborted any real run
   longer than 30 s — contradicting the "no fixed cap" decision. The
   backstop is far beyond any realistic operation; client cancellation
   still propagates, and a killed encrypt was verified to roll back
   cleanly (the 4 h cap was found and applied during spec 012).
5. **`ValidatePassword` additionally rejects whitespace-only passwords**
   (a string whose `TrimSpace` is empty), while still not trimming
   legitimate values — a spaces-only password is a near-certain
   mistake, and it would be a meaningless single stdin line.
6. **The change-passwd tool was renamed to
   `parallels_vm_change_encryption_password`** (2026-10-10) — see the
   "Tool rename" decision above. All identifiers followed
   (`VMChangeEncryptionPassword*`, `descVMChangeEncryptionPassword`,
   file `parallels_vm_change_encryption_password.go`); the `current_password`
   / `new_password` argument names were kept.
