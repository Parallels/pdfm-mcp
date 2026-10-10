# QA 011 — Parallels VM Encryption (`encrypt` / `decrypt` / `change-passwd`)

**Scope:** the three encryption-lifecycle tools, implemented per
[spec 011](../specs/011-parallels-vm-encryption.md).

**Safety note:** `dry_run` calls are non-mutating (prlctl checks
preconditions; on an *encrypted* VM it still wants the current password
to authorise the check). Real encrypt/decrypt/change-password runs are
state mutations that lock/unlock data behind secrets — **never** executed
by automated tests. The live round-trip below uses only a disposable VM.

## Protocol facts (verified live, Parallels 27.0.1)

- prlctl reads the password **from stdin** when not on a TTY: one line
  (encrypt/decrypt), two lines current-then-new with **no confirmation**
  (change-passwd).
- Unencrypted + stopped: `encrypt --dry-run` → "All prerequisites … met"
  (exit 0, no password); `decrypt --dry-run` → "Virtual machine is not
  encrypted" (exit 255).
- Running VM: `encrypt --dry-run` → "…currently running…" (exit 255).
- Wrong password → "The password you specified is invalid" (exit 255);
  the VM stays encrypted; prlctl never echoes the password.
- Encrypted VM + dry-run **without** password → both dry-runs fail with
  the password-invalid error (the check needs authorisation).
- An interrupted encrypt rolls back cleanly (VM returns to
  `Encrypted: no`, stopped).
- State visible in the list JSON: `Security.Encrypted` = `yes`/`no`.

## Steps

### 1. Automated suites

```
make check
make test-e2e
```

Expect green. The e2e gating test now asserts, read-only and default-on:
all three tools listed in both modes; `encrypt --dry-run` +
`decrypt --dry-run` against the first stopped VM with the outcome
determined by its `Security.Encrypted` flag (unencrypted → encrypt
dry-run succeeds and decrypt dry-run is a clean tool error; encrypted →
both are clean tool errors because no password is sent). No password is
ever transmitted by the automated tests.

### 2. Live round-trip (disposable stopped VM — executed 2026-10-09 on
`empty_catalog_test`, VM left exactly as found)

Using `bin/pdfm-mcp` in fleet mode, MCP tools/call:

1. `parallels_vm_encrypt {vm_id, password: "<throwaway-A>"}` →
   output "The virtual machine has been successfully encrypted."
2. `parallels_vm_info {vm_id}` → `Security.Encrypted: "yes"`.
3. `parallels_vm_decrypt {vm_id, password: "<wrong>"}` → tool error
   "…password you specified is invalid…", VM still encrypted.
4. `parallels_vm_change_password {vm_id, current_password: A,
   new_password: "<throwaway-B>"}` → "The password has been successfully
   changed." (2 stdin lines, no confirmation).
5. `parallels_vm_decrypt {vm_id, password: B}` → "…successfully
   decrypted."; `parallels_vm_info` → `Security.Encrypted: "no"`, VM
   stopped.
6. Verify the server log (stderr) contains `ref` and `dry_run` fields but
   **no password material** anywhere.

### 3. Mode contract (wire level)

- Fleet: all three listed, `required: ["vm_id"]`; a call without `vm_id`
  is a schema error.
- VM mode (`--vm-id <vm>`): listed with no required args; explicit
  `vm_id` → "vm_id must be omitted"; the configured target is used.
- Real run without password → tool error "password is required" before
  prlctl runs; `dry_run: true` without password succeeds.

## Results — 2026-10-09, live machine (Parallels 27.0.1)

All steps passed; the VM was left exactly as found
(`stopped`, `Encrypted: no`).

- `make check`: green (fmt, golangci-lint 0 issues, vet, race tests).
- `make test-e2e`: green — all three tools listed in both modes; the
  dry-run precondition checks ran default-on against a stopped VM with
  the expected outcomes; no password transmitted.
- Wire round-trip on `empty_catalog_test` (disposable, user-approved):
  1. `encrypt {password: A}` → "The virtual machine has been
     successfully encrypted." (prlctl progress lines pass through in the
     trimmed stdout); `info` → `Encrypted: "yes"`.
  2. `encrypt {dry_run: true}` on the now-encrypted VM, no password →
     clean tool error "Failed to authorise current session … password …
     invalid" (confirms encrypted dry-runs require the password).
  3. `decrypt {password: wrong}` → clean tool error, VM stayed encrypted.
  4. `change_encryption_password {current: A, new: B}` → "The password has been
     successfully changed."
  5. `decrypt {password: B}` → "The virtual machine has been
     successfully decrypted."; `info` → `Encrypted: "no"`.
- Contract checks: fleet real run without `password` → "password is
  required for this operation (only a dry_run call may omit it)";
  VM mode with explicit `vm_id` → "vm_id must be omitted: this server
  targets a specific VM (started with --vm-id)".
- Server log audit (full session stderr): **no password material** in
  any log line; only `vm` and `dry_run` fields.

Unit coverage: exact argv + stdin payloads (`pw\n`, `cur\nnew\n`),
dry-run uses the Run path with no stdin, validation rejects empty /
whitespace-only / non-printable / >256-char passwords before prlctl
runs, non-zero exit surfaces prlctl's message without the password,
cache invalidates on real runs and change-passwd but not on dry-runs.
