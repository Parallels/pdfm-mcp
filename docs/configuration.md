# Configuration

[Back to the README](../README.md)

| Variable | Default | Meaning |
| --- | --- | --- |
| `PDFM_MCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `PDFM_MCP_LOG_FILE` | empty | If set, logs are also appended to this file |
| `PDFM_MCP_VM_ID` | empty | Parallels VM id or name to target (see [VM targeting](tools.md)) |
| `PDFM_MCP_VM_CACHE_TTL` | `30s` | Freshness window of the in-memory VM list cache |
| `PDFM_MCP_READ_ONLY` | `false` | Expose only read-class tools (flag `--read-only`) |
| `PDFM_MCP_ALLOW_TOOLS` | unset | Comma list of tool names or trailing-`*` globs; when set, only these are exposed (flag `--allow-tools`) |
| `PDFM_MCP_DENY_TOOLS` | unset | Comma list of names or globs to hide; wins over allow (flag `--deny-tools`) |
| `PDFM_MCP_ALLOW_VMS` | unset | Comma list of VM ids or names the tools may address, max 64; in VM mode `--vm-id` must be in it (flag `--allow-vms`) |
| `PDFM_MCP_TELEMETRY_DISABLED` | `false` | Disables the usage telemetry collected by the released build (see below) |

## Telemetry

The released build of pdfm-mcp collects **anonymised usage telemetry**: which tools are used, whether they succeeded, and how long they took. It never collects tool arguments, VM ids or names, guest output, screen content, or error text. To disable telemetry, set `PDFM_MCP_TELEMETRY_DISABLED=true` — it always wins.

## Safety policy

pdfm-mcp can restrict what an agent may do. `--read-only` exposes only observation tools (`parallels_list_vms`, `parallels_vm_info`, `parallels_vm_capture`, ...). `--allow-tools` and `--deny-tools` take exact names or prefix globs such as `parallels_vm_exec*`; deny wins. Hidden tools disappear from `tools/list` and from `parallels_vm_sequence` steps. `--allow-vms` limits which VMs any tool can touch: `parallels_list_vms` returns only allowed VMs, and other VMs are refused without being named. Every flag has a `PDFM_MCP_*` env equivalent, and flags beat env. Unknown (new, unclassified) tools fail closed: they count as destructive and are hidden in read-only mode.

This guards against a misconfigured or over-eager agent, not against a local user who can edit the client config; an allowed `exec` can still run anything in the allowed guest. Details: [`specs/019-safety-policy.md`](specs/019-safety-policy.md).

Flags: `--version` prints version and exits; `--log-level` and `--vm-id` override the env vars.

