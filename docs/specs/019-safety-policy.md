# Spec 019: Safety Policy (read-only mode, tool allow/deny, VM allow-list)

| | |
|---|---|
| Status | Implemented (local + live verification complete 2026-10-09 on a real Mac) |
| Audience | Implementing agents / engineers |
| Scope | Server-side governance: `--read-only`, `--allow-tools`, `--deny-tools`, `--allow-vms` (and `PDFM_MCP_*` env equivalents), the tool classification table, the policy-filtered registration table in `server.New`, the VM allow-list enforcement in every VM-scoped tool, and the unresolved-placeholder config guard from spec 015 section 6 |
| Depends on | 002 (module, mode contract), 003 (`resolvePowerTarget`), 007 (sequence registry), 015 section 6 |
| Out of scope | Per-call approval prompts, authentication of the MCP client, auditing/log shipping, per-argument policies (for example "exec only these commands") |

## 1. Goal and threat model

Competing products offer a per-VM consent switch. pdfm-mcp gives the **operator** (whoever writes the MCP client config) server-side controls over what an agent may do.

**Protects against**: a misconfigured or over-eager agent (or prompt-injected content steering an agent) calling tools or touching VMs the operator never meant to expose. Hidden tools are absent from `tools/list`, cannot be called, and cannot be reached through `parallels_vm_sequence`.

**Does not protect against**: a malicious local user or process that can edit the MCP client config, change the environment or run the binary directly (they can simply drop the flags or call `prlctl` themselves). The policy is a guardrail on the agent, not a security boundary against the machine's owner. It is also not a sandbox for allowed tools: an allowed `parallels_vm_exec` can run anything inside the allowed guest.

### Acceptance criteria

1. `make check` and `make test-e2e` pass.
2. With `--read-only`, `tools/list` contains only `read`-class tools plus `parallels_vm_sequence`, whose steps are limited to read tools.
3. Allow/deny lists and the VM allow-list behave exactly as in section 3 and are enforced for standalone calls and sequence steps alike.
4. Every tool registered by `server.New` has an entry in the classification table (a test fails otherwise); unknown names are classified `destructive`.
5. Unresolved `${...}` environment values are treated as unset with one warning naming the variable, never the value.

## 2. Configuration

| Flag | Env | Meaning |
|---|---|---|
| `--read-only` | `PDFM_MCP_READ_ONLY` | `true`/`1`/`yes`/`on` exposes only `read`-class tools (and the sequence tool, filtered) |
| `--allow-tools` | `PDFM_MCP_ALLOW_TOOLS` | Comma list; when set, ONLY matching tools are exposed |
| `--deny-tools` | `PDFM_MCP_DENY_TOOLS` | Comma list; matching tools are removed |
| `--allow-vms` | `PDFM_MCP_ALLOW_VMS` | Comma list of VM ids or names the tools may address |

- Flags beat env (an explicit empty flag, `--allow-tools=`, clears the env value; `--read-only=false` overrides `PDFM_MCP_READ_ONLY=true`).
- Tool entries are exact names (`parallels_vm_stop`) or a prefix glob with a single trailing `*` (`parallels_vm_power_*`, bare `*` matches everything). Lowercase letters, digits and `_` only; anything else (including `*` elsewhere) is a config error, exit code 2. At most 128 entries.
- VM entries are validated with `parallels.ValidateVMRef`; at most 64 entries; entries are comma separated, so a VM name containing a comma cannot be listed. Matching is case-sensitive.
- Entries are trimmed; empty entries and duplicates are dropped.
- Unmatched allow/deny patterns (typos) are not an error: a warning with the pattern names is logged. A mistyped allow entry fails closed (nothing matches); a mistyped deny entry fails open, which is why the warning exists.

## 3. Semantics

### 3.1 Tool policy (`internal/policy`)

A tool is exposed when all of these hold, evaluated in this order:

1. It does not match `--deny-tools` (**deny always wins** over allow).
2. `--allow-tools` is unset, or it matches.
3. Not read-only, or its class is `read` (or it is the sequence meta-tool).

Restrictions intersect: `--allow-tools` cannot widen `--read-only`.

### 3.2 Classification table

| Class | Tools | Rationale |
|---|---|---|
| `read` | `parallels_list_vms`, `parallels_vm_info`, `parallels_vm_capture`, `parallels_vm_observe`*, `parallels_vm_exec_result`*, `parallels_vm_snapshot_list` | No argument combination changes VM or host state (capture/observe read pixels only) |
| `input` | `parallels_vm_send_key`, `parallels_vm_type` | Inject keyboard input into the guest |
| `power` | `parallels_vm_start/resume/pause/suspend/restart/reset/reset_uptime/stop`, `parallels_vm_clone`, `parallels_vm_snapshot_create` | Recoverable state changes (clone and snapshot-create only add things) |
| `exec` | `parallels_vm_exec`, `parallels_vm_exec_start`*, `parallels_vm_exec_cancel`* | Run commands in the guest (cancel acts on a running job) |
| `destructive` | `parallels_vm_delete`, `parallels_vm_reclaim_disk`, `parallels_vm_snapshot_delete`, `parallels_vm_snapshot_switch` | Data loss or irreversible; `reclaim_disk` is destructive because its `estimate` flag is an argument, not a different tool |
| `secret` | `parallels_vm_encrypt`, `parallels_vm_decrypt`, `parallels_vm_change_encryption_password` | Handle passwords |
| `sequence` | `parallels_vm_sequence` | Meta-tool, see 3.3 |

\* Not-yet-existing tools, pre-classified so adding them needs no policy change. The table lives in `internal/policy/policy.go`.

**Unknown names fail closed**: `Classify` returns `destructive`, so a new unclassified tool is hidden in read-only mode. `TestEveryRegisteredToolIsClassified` (in `internal/tools`) fails with an explicit message when `server.New` registers a tool that is missing from the table. Rule for classifying: a tool whose arguments choose between a harmless and a harmful mode takes the class of the harmful mode.

### 3.3 `parallels_vm_sequence`

The sequence step registry is built from the same tools and then filtered with the same policy (`orchestrator.Registry.Filter`) **before** `RegisterParallelsVMSequence` generates the description. Hidden tools therefore disappear from `tools/list`, from the allowed step tools and from the generated description, and a sequence naming one fails validation as an unknown tool. The sequence tool is itself exposed unless denied or excluded by `--allow-tools`, and is hidden when no step survives. In read-only mode it is therefore still available and can only run read steps (capture, waits, gates, diffs). Every in-process step passes through `resolvePowerTarget`, so the VM allow-list also covers sequences.

### 3.4 VM allow-list

- **Fleet mode**: every `vm_id` goes through `resolvePowerTarget` (used by all VM-scoped tools and sequence steps). The ref is accepted when it equals an entry exactly (case-sensitive), or when the VM list cache resolves it to a VM whose other identifier (id or name) equals an entry. Otherwise the call fails with "this VM is not permitted: the server restricts VM access with --allow-vms". The message names the rule only; it never echoes or lists other VMs. A cache failure denies (fail closed).
- `parallels_list_vms` returns only allowed VMs (`vms` and `count` reflect the filtered set; the cache itself is untouched). Fleet `parallels_vm_info` is single-VM, so the same authorization applies.
- **VM mode**: `--vm-id` must literally appear in `--allow-vms` (exact match; no cache is available at config time), otherwise `config.Load` fails.
- Implementation: `tools.SetVMAllowList` (set by `server.New` on every start, nil when unset) holds a process-wide `atomic.Pointer`, consulted by `authorizeVM` inside `resolvePowerTarget`. New VM-scoped tools must keep calling `resolvePowerTarget`.
- `parallels_vm_clone` authorizes the **source** VM; the clone's new name is not on the list, so the clone is unreachable until the operator adds it (intended).

### 3.5 Registration table and logging

`server.New` builds a table `{Name, Register}` (`toolTable`). Each entry is registered only when `policy.Decide(Name)` allows it; hidden tools are logged at info level (`tool hidden by policy`, `tool`, `reason`; names only). To add a tool: add one line to `toolTable`, add its classification, and (for VM-scoped tools) its `tools.RegisterSteps` line. `TestRegistrationTableNamesMatchToolNames` denies every listed tool by name and checks exactly that tool disappears, so a table name that differs from the real MCP name fails the test.

### 3.6 Unresolved placeholder guard (spec 015 section 6)

Values of `PDFM_MCP_VM_ID`, `PDFM_MCP_LOG_LEVEL`, `PDFM_MCP_VM_CACHE_TTL`, `PDFM_MCP_READ_ONLY`, `PDFM_MCP_ALLOW_TOOLS`, `PDFM_MCP_DENY_TOOLS` and `PDFM_MCP_ALLOW_VMS` that, after trimming, start with `${` and end with `}` are treated as unset. One warn-level line names the variable (`var=...`), never the value. A value that merely contains `$` is validated normally.

## 4. Security notes

- Fail closed: unknown tools are destructive, unresolvable VM refs are denied, an invalid hand-built policy exposes no tools.
- Logs contain tool names, flag names and counts only.
- Read-only still exposes screen contents via `capture`/`observe`; remove those with `--deny-tools` when needed.
- Deny wins: `--allow-tools parallels_vm_stop --deny-tools parallels_vm_stop` hides it (not an error).

## 5. Testing

Unit: `internal/policy` (table, fail closed, glob/deny/allow/read-only matrix, list parsing and limits, VM allow-list rules and filtering), `internal/config` (flags beat env, bad entries, VM-mode check, placeholder guard), `orchestrator` (`Remove`/`Filter`). In-memory protocol tests (`internal/tools/parallels_policy_test.go`): classification of every registered tool, name/table consistency, read-only tool list and sequence description, allow/deny/widening, allow-vms in fleet and VM mode with list filtering and no-leak assertions.

## 6. QA

See [QA 019](../qa/019-safety-policy-QA.md).

## 7. Deviations

- Verified live on 2026-10-09 with no code changes needed. The fail-closed
  classification guard was demonstrated by temporarily registering an
  unclassified tool (the guard test failed as designed) and reverting.
- `parallels_vm_observe` is pre-classified `read` but not registered on
  hosts without the OCR helper (spec 020); the read-only set therefore shows
  the five always-present read tools plus the sequence tool. Not a policy
  deviation.
- The placeholder guard's warnings go to the **startup stderr logger**
  (`config.Load(args, stderr)`), not the `PDFM_MCP_LOG_FILE` slog — by
  design; QA 019 captures them from stderr.
- QA 019 step 6 "Claude Desktop shows only the filtered tools" remains a
  manual check pending the Claude Desktop install (QA 015 pending item).
