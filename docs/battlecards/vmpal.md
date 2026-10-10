# Battlecard: pdfm-mcp vs VMPal MCP

**Internal, Parallels confidential. Do not share externally.** This card describes **the product as we are delivering it** (specs 001 to 020 plus the snapshot tools), not only what is verified today. Items marked *new* are written and unit-tested but **not yet verified on a real Mac**; do not claim them to a customer until their QA doc (`docs/qa/`) has passed. VMPal's side comes from its tool list, its `vmpal ui` help text and launch coverage; none of it has been tested hands-on.

## 30-second positioning

| | pdfm-mcp (target state) | VMPal MCP |
|---|---|---|
| What it is | An MCP server that **drives Parallels Desktop** from the CLI (`prlctl`). Single static Go binary | The built-in MCP server of **VMPal**, a new Mac VM app for Apple silicon (macOS 26+) from the TablePlus team |
| Core idea | **Manage the fleet and automate sessions**, and now also **see the screen** (screenshots + local OCR) | **Computer use**: an agent sees and operates the guest screen like a person |
| Best at | Lifecycle, fleet management, deterministic one-call automation, governance | GUI interaction (mouse, click by on-screen text), user hand-off |
| Consent model | Server-side policy: read-only mode, tool allow/deny lists, VM allow-list | The user enables agent control per VM in the app (Settings > AI Agents) |

One line for sales: *VMPal is an app built around letting an agent use a VM's screen. Parallels already runs your VMs, and pdfm-mcp lets an agent manage, observe and automate them, under policy you control.*

## Capability matrix

Legend: **Yes** / Partial / No / Planned / ? (unknown). *new* = delivered in this batch, unverified on a Mac.

| Capability | pdfm-mcp | VMPal MCP |
|---|---|---|
| List/inspect VMs (full config JSON) | **Yes** | Partial (`vmpal_status`: readiness, screen size) |
| Fleet mode (all VMs from one server) | **Yes** | ? |
| Start / stop | **Yes** (plus pause, resume, suspend, restart, reset, reset-uptime) | Yes |
| Screenshot | **Yes** (PNG image block, downscale control) | **Yes** (JPEG/PNG, region, marks, grid, "only if changed") |
| Keyboard input | **Yes** (key sequences, `type` text) | **Yes** |
| Mouse (click, drag, scroll) | **Planned** (next phase) | **Yes** |
| Read screen **text** with positions (`observe`) | **Yes, *new***: `parallels_vm_observe` (text / changes / find, region, limit), Apple Vision **on-device** (compiled in via cgo) | **Yes** |
| Click by on-screen **text** | **Planned** (needs pointer input; OCR coordinates already returned) | **Yes** |
| Wait until screen settles / changes | **Yes, *new***: sequence `wait.until` settled / changed | **Yes** |
| Wait until text appears / disappears | **Planned** (small follow-up on the OCR package) | **Yes** |
| Run guest command | **Yes** (exit code + capped output) | **Yes** |
| Admin / elevated exec | **Yes, *new***: `elevated` runs as the guest's system account (behaviour to verify per guest) | **Yes** (`vmpal_exec_admin`) |
| Long-running exec jobs (poll output, cancel) | **Yes, *new***: `exec_start` / `exec_result` / `exec_cancel`, incremental output, up to 4 jobs, 4 h max | **Yes** (`vmpal_exec_result`) |
| Snapshots | **Yes** (create, list, delete, switch) | Yes (4 tools) |
| Clone VM | **Yes** (full or linked) | ? |
| Delete VM | **Yes** (confirm-gated) | ? |
| Encrypt / decrypt / change password | **Yes** (passwords over stdin, never logged) | ? |
| Reclaim disk space | **Yes** (with estimate) | ? |
| Multi-step automation in one call | **Yes**: `parallels_vm_sequence` with `when` / `expect` / `gate` / `retry`, regex and image-diff checks, any tool as a step | Partial: `vmpal_act` batches GUI actions with waits and `expect` |
| Governance (read-only mode, tool allow/deny, VM allow-list) | **Yes, *new***: server-side flags, fail-closed for unclassified tools | Partial: per-VM consent switch in the app |
| Block the human while the agent works (`mask`) | **No** (no prlctl equivalent; app-level feature) | **Yes** |
| Standalone CLI for the same tools | No | **Yes** (`vmpal ui <VM> ...`, stream mode) |
| Install | Homebrew tap, Claude Desktop `.mcpb` bundle, MCP Registry listing: **files written, nothing published yet** (needs signing identity, tap, and publication approval) | One-click add to Claude Code, Codex, Grok CLI, Gemini CLI (per launch coverage) |

Tool counts: pdfm-mcp 29 tools in the target state (28 + `parallels_vm_observe` when the OCR helper is present); VMPal lists 19. Count is not the point: our breadth is VM management plus automation, theirs is GUI control.

## Where we win

1. **We manage the fleet, not one VM.** Clone, delete, snapshots, encrypt, reclaim disk, power states and full config for every VM, plus a single-VM scoped mode (`--vm-id`).
2. **Deterministic automation.** One `parallels_vm_sequence` call runs a whole flow server-side (tools, waits, gates, retries, regex and image-diff checks, screen-settle waits) with one result and no model round trip per step. Strong for repeatable work and for token cost.
3. **Works with the VMs people already run on Parallels.** No new VM app, no migration, existing images and licenses.
4. **Governance an IT team can enforce.** Read-only mode, tool allow/deny lists and a VM allow-list set by whoever deploys the server, not only by the person using the agent. Unknown tools fail closed.
5. **Local OCR with no new Go dependency.** Screen text is read on the Mac by Apple Vision. (The recognized text still goes to the model provider through the client, like screenshots do.)
6. **Safer handling of secrets in the tools we have.** Passwords over stdin only, command text and typed text never logged, destructive tools annotated, delete needs an exact-name confirm.
7. **Small, auditable surface.** Specs and QA docs per tool, a single static binary.

## Where they win (be honest)

1. **Mouse and click-by-text.** Pointer input, drag, scroll and "click the Save button" by name. For us this is planned, and until it ships our agents act with keys, typed text and screenshots/OCR only. This is the main functional gap.
2. **Block the human while the agent acts (`mask`).** Not possible through prlctl.
3. **Consent lives in the product.** A per-VM switch in the app's settings is friendlier than server flags for an individual user. Ours is built for deployers and IT.
4. **Packaging today.** One-click add for the common agent CLIs and a standalone `vmpal ui` CLI. Ours is manual config until the release pipeline is run (specs 013 to 016) and approved.
5. **Marketing story.** "Give your agent its own safe machine" is simple and already covered in the Mac press at launch.
6. **Possible latency edge** for GUI loops, since it is native to the app (unmeasured; we shell out to `prlctl` per call).

## Objection handling

| They say | We say |
|---|---|
| "VMPal lets the agent click anything by name." | For GUI work that is true today, and pointer input is on our roadmap. For setup and operations, a command or a sequence is faster and more reliable than clicking, and we already read screen text (`parallels_vm_observe`). |
| "VMPal asks the user to enable agent control." | We offer enforceable controls for deployers: read-only mode, tool allow/deny, VM allow-list. Nothing destructive is exposed unless you allow it. |
| "Why not just use VMPal's VMs?" | You would migrate off Parallels. pdfm-mcp works on the VMs you already have, including snapshots, clones and encryption. |
| "Parallels has no agent story." | It does: a Parallels-native MCP server with fleet management, automation sequences, screen reading and policy controls. |
| "VMPal is Apple silicon / macOS 26+." | Per launch coverage. Check our own supported host matrix before using this as a differentiator. |
| "Does your OCR send my screen to the cloud?" | No, recognition runs on the Mac. The returned text (like a screenshot) goes to whichever model your client uses. |

## Remaining gaps and roadmap (confidential)

1. **Pointer input**: move, click, drag, scroll (next phase, per the team).
2. **Click by text**: pointer + the existing OCR `find` coordinates.
3. **Wait until text**: a `screen_text` sequence check and `wait.until: text` on the OCR package.
4. **Verification of the new items on real Macs**: elevated exec behaviour per guest OS, OCR accuracy on Parallels guest screens, wait-until tuning on Retina VMs, job cancel semantics.
5. **Distribution**: ship the signed, notarized release (Apple identity, tap, MCPB bundle, publication approval) and include the OCR helper in all three channels.
6. **Not feasible through prlctl**: input masking; consider a different mechanism or accept the gap.

## What we do not know yet (research before external use)

- VMPal's real pricing and licensing: sources disagree ($69 one-time vs free download with a licence), and its version/build naming also differs between sources.
- How VMPal's tools address multiple VMs, and whether it has clone/delete/encrypt-style management tools.
- Real-world reliability and speed of its text targeting; Windows guest details and host requirements.
- Hands-on comparison: run the same 3 tasks on both (install an app and verify it, reproduce a UI bug, run a build) and record time, tokens and success.

## Sources

- VMPal tool list and `vmpal ui` help text: provided by the team.
- [VMPal Is a Mac Virtual Machine App Built for Apple Silicon and AI Agents (iThinkDiff)](https://www.ithinkdiff.com/vmpal-virtual-machines-apple-silicon-mac-ai-agents/)
- [VMPal AI agent virtual machine (Laravel News)](https://laravel-news.com/vmpal-ai-agent-virtual-machine)
- [VMPal for Apple silicon Macs (WindowsForum)](https://windowsforum.com/news/vmpal-for-apple-silicon-macs-run-windows-11-vms-and-ai-coding-agents.447423/)
- [VMPal notes](https://notes.nicolasdeville.com/apps/vmpal/)
- pdfm-mcp: this repository (`README.md`, `internal/server/server.go`, `docs/specs/`).
