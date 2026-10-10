# Spec 024: Amplitude product telemetry (usage, errors, popularity — anonymised)

| | |
|---|---|
| Status | Implemented (unit + e2e + live wire QA complete on a real Mac 2026-10-10 with a throwaway key; live verification with the real project key pending release owner) |
| Audience | Implementing agents / engineers, release owner |
| Scope | Anonymous product-usage telemetry via Amplitude: one event per MCP tool call plus a startup event and a heartbeat; identifiers derived from the macOS hardware UUID and the Parallels Desktop license; the API key is baked into the binary at build time (ldflags, like the version), so telemetry is off in keyless builds; runtime opt-out via `PDFM_MCP_TELEMETRY_DISABLED`; no dependency changes besides the Amplitude Go SDK |
| Out of scope | In-app consent UI, event properties beyond the allowlist in section 3, any non-darwin behaviour (the binary is darwin-only), dashboard setup on the Amplitude side (release owner) |

## 1. Goal

Learn **what is used, how often, and what fails** — tool popularity, error
rates, latency — without collecting any content. The approach mirrors the
sibling repo `PLAI-client-agent` (`internal/amplitude`): the official Amplitude
Go SDK, environment-driven enablement, a hashed user identity, and a
fire-and-forget tracking surface.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| SDK | `github.com/amplitude/analytics-go` (v1.3.x, same major as PLAI-client-agent) | Official client; handles batching, retries, flush-on-close. AGENTS.md documents this new dependency here (spec 024) |
| API key | **Baked into the binary at build time** with the same `-ldflags -X` mechanism as the version: `internal/telemetry.APIKey` is injected by the Makefile (`AMPLITUDE_API_KEY` build env var) and by GoReleaser (`{{ env "AMPLITUDE_API_KEY" }}`). **Build without a key ⇒ telemetry is OFF** — no client is created and nothing is sent. There is deliberately no runtime env var for the key (owner: "injected during build with build flag, same as the version, no scripts, nothing") | Local/dev builds are keyless and therefore silent by default; release builds carry the key from the CI build environment. Trade-off accepted by the owner: the key is embedded in (and extractable from) a release binary — only builds that are meant to report carry it |
| Opt-out | Env var `PDFM_MCP_TELEMETRY_DISABLED` (strict boolean, `parseBoolEnv` convention: `1/true/yes/on` enable the opt-out). **Opt-out wins over the baked-in key** — the runtime escape hatch for managed fleets | Owner: "if the environment variable disabling the telemetry is set we disable it" |
| Version in events | `internal/version.Version` — already registered at build time by the Makefile (`-ldflags -X …/version.Version=…`); telemetry sends it verbatim, no new variable | Owner: "make a variable in the code where we register the version during the build" — that variable already exists (`internal/version`), spec documents its reuse |
| Hardware id | macOS IOPlatformUUID, read once at startup from `ioreg -rd1 -c IOPlatformExpertDevice` (the `IOPlatformUUID` string), cached for the process life; failure ⇒ `unknown` | Stable per-machine identifier the owner named as "the user" |
| PD license | Read once at startup from `prlsrvctl info --license --json` (same command as `devops-service` `GetLicense`), parsed for `serial` and `edition`; failure ⇒ unlicensed. Cached; never re-fetched | Owner: "collect the pd license and use that as part as the unique id" |
| `device_id` | The raw hardware UUID | Amplitude's standard device field; the owner's "device" half |
| `user_id` | `sha256("pdfm-mcp:" + hwUUID + ":" + licenseSerial)` hex; without a resolvable license `sha256("pdfm-mcp:" + hwUUID + ":unlicensed")` | The owner's "user" half: the same Mac + same Parallels license always maps to the same user, different machines/licenses differ. The raw serial **never** leaves the machine — only this hash is transmitted |
| Event names | Format **`{{SERVICE}}::{{TOOL}}`** with `SERVICE` = `PDFM`: `PDFM::<TOOL>` for every MCP tool call (the uppercase tool name, e.g. `PDFM::PARALLELS_VM_LIST`), `PDFM::SERVER_STARTED` (once, at startup when enabled), `PDFM::HEARTBEAT` (every 5 min, PLAI parity) | Owner-chosen format; "what is most used" is directly readable from the event name in Amplitude |
| Anonymisation | Every event carries **only** the allowlisted properties (section 3). No tool arguments, no VM ids/names, no guest output, no error text, no license serial, no file paths | Owner: "anonymised data, we just want to learn about usage, errors and what is most used" |
| Instrumentation | A generic `telemetry.Instrument[I, O]` wrapper replaces `mcp.AddTool` at every registration via a `tools.register` helper; the wrapper measures duration and records success/failure, then calls the original handler | One central pattern; the orchestrator's in-process step calls are *not* double-counted (only MCP-level calls are tracked) |
| Fail-safety | Telemetry never blocks or fails a tool call: the SDK queues in memory; identity fetch errors degrade to `unknown`/`unlicensed`; SDK HTTP errors are logged at warn only; `Close()` flushes on shutdown (wired next to `parallels.Shutdown` in `main`) | Telemetry is a side channel, never on the request path |
| Client settings | `FlushQueueSize: 100`, `FlushInterval: 5s` (PLAI values); logger forwards SDK output to `slog` at debug (nothing at info+ unless the SDK reports an HTTP error ≥400) | PLAI parity; keeps debug logs useful, normal logs quiet |
| Server zone / endpoint | US zone (default). The SDK's `ServerURL` is exposed to tests only (unit tests point it at a local httptest server; production never sets it) | Testability without a production override knob |

## 3. Event contract (allowlist — exhaustive)

Every event carries `user_id` (hashed, section 2) and `device_id`
(hardware UUID) plus these properties and **nothing else**:

| Property | Type | Meaning |
|---|---|---|
| `service` | string | always `pdfm-mcp` |
| `version` | string | `internal/version.Version` as built |
| `os` / `arch` | string | `runtime.GOOS` / `runtime.GOARCH` |
| `mode` | string | `fleet` or `vm` (the server's target mode) |
| `pd_edition` | string | license `edition` (e.g. `Pro`), or `unknown` |

Event-specific:

| Event | Extra properties |
|---|---|
| `PDFM::SERVER_STARTED` | — |
| `PDFM::<TOOL>` (one per MCP tool, e.g. `PDFM::PARALLELS_VM_LIST`) | `tool` (MCP tool name, also carried by the event name), `ok` (bool), `duration_ms` (int, wall clock) |
| `PDFM::HEARTBEAT` | — |

`ok=false` means the handler returned an error. The error **message is never
sent** (it can contain VM names, paths, guest output).

## 4. Components

- `internal/telemetry/telemetry.go` — public API:
  `Init(ctx, InitConfig)` (idempotent; decides enablement, builds the
  client, sends `PDFM::SERVER_STARTED`, starts the heartbeat goroutine),
  `IsEnabled()`, `TrackToolCall(name string, ok bool, d time.Duration)`,
  `Instrument[I any, O any](name string, h mcp.ToolHandlerFor[I, O])`,
  `Close()`. The build-time key is the package variable
  `internal/telemetry.APIKey` (ldflags-injected, section 2); the key is
  **never** logged.
- `internal/telemetry/identity.go` — `Identity{DeviceID, UserID, Edition}`;
  `ResolveIdentity(ctx, runner)` runs `ioreg` (10 s cap) and
  `prlsrvctl info --license --json` (10 s cap) through an injected command
  runner (tests use fakes; production uses `exec.CommandContext`).
  `userIDFor(hwUUID, serial)` is a pure function (unit-tested).
- `Makefile` / `.goreleaser.yaml` — the key injection point:
  `-X $(MODULE)/internal/telemetry.APIKey=$(AMPLITUDE_API_KEY)` (Makefile,
  `AMPLITUDE_API_KEY ?=` + `export` so local builds are keyless but the
  variable always exists) and
  `-X …/internal/telemetry.APIKey={{ .Env.AMPLITUDE_API_KEY }}`
  (GoReleaser; the release workflow sets the variable from the secret).
  GoReleaser v2 templates **error on a missing env key** (not an empty one),
  so every build path exports the variable even when empty.
- `internal/config` — `Config` gains `TelemetryDisabled bool`
  (parseBoolEnv of `PDFM_MCP_TELEMETRY_DISABLED`). The key is not runtime
  config and never passes through `config`.
- `internal/tools` — `register(s, tool, handler)` helper (this package) that
  calls `mcp.AddTool` with the `telemetry.Instrument`-wrapped handler; every
  `Register*` uses it. `mcp.AddTool` disappears from the tools package
  (a grep-guard test enforces it so a future tool cannot skip telemetry).
- `internal/server` — `New` calls `telemetry.Init` after mode is known and
  before registration; a disabled init logs one info line ("telemetry
  disabled: no API key" / "…: opt-out set").
- `cmd/pdfm-mcp` — `defer telemetry.Close()` next to `defer parallels.Shutdown()`.

## 5. Identity edge cases

| Situation | `device_id` | `user_id` | `pd_edition` |
|---|---|---|---|
| Both resolvable | hwUUID | `sha256(pdfm-mcp:<hwUUID>:<serial>)` | license edition |
| License fetch fails / no serial | hwUUID | `sha256(pdfm-mcp:<hwUUID>:unlicensed)` | `unknown` |
| ioreg fails | `unknown` | `sha256(pdfm-mcp:unknown:<serial\|unlicensed>)` | as resolved |

A trial license is still a license (its serial is used); the hash does not
encode trial status.

## 6. Privacy statement (for the README)

Sent: the Mac's hardware UUID as device id, a SHA-256 hash of
`pdfm-mcp:<hardware UUID>:<Parallels license serial>` as user id (never the
raw serial), the Parallels edition name, the build version, OS/arch, the
server mode, and per call the tool name + success + duration. Not sent: tool
arguments, VM ids or names, guest command output, screenshots or screen text,
error messages, the license serial, home directory or any file path. Disabled
by default (no API key) and always overridable with
`PDFM_MCP_TELEMETRY_DISABLED=true`.

## 7. Acceptance criteria

1. **Build without a key** ⇒ `telemetry.IsEnabled()==false`, no HTTP client is created, all tools work, one info log line at startup.
2. **Key-injected build + `PDFM_MCP_TELEMETRY_DISABLED=true`** ⇒ same as 1 (opt-out wins).
3. **Key-injected build, no opt-out** ⇒ enabled; against a fake `ServerURL`: the capture server receives `PDFM::SERVER_STARTED` at startup and one `PDFM::<TOOL>` event per MCP call with the **exact** allowlisted property keys (asserted set-equal, so any accidental new property fails the test).
4. `user_id` is the documented SHA-256 and is stable across two `ResolveIdentity` calls with the same inputs; the raw serial and raw hwUUID never appear in any event property (asserted by scanning the captured payload).
5. A failing handler produces `ok=false` with no error text in the payload.
6. `make check` (incl. race) and `make test-e2e` green; e2e runs with no telemetry env vars set (disabled path in the live server).
7. A grep-guard test fails if `internal/tools` calls `mcp.AddTool` directly (every tool is instrumented).

## 8. QA

Unit tests cover the enablement matrix, the identity table (section 5),
`userIDFor` determinism, the property allowlist and the fake-endpoint payload
shape. `test/e2e/wire_qa_024.py` builds two throwaway binaries (keyless and
key-injected) and asserts all three enablement states plus a live
`tools/call` event. Manual (release owner): build a release binary with the
**real** key in the build environment and confirm the Amplitude project
shows `PDFM::SERVER_STARTED`; call one tool and confirm its `PDFM::<TOOL>`
event (e.g. `PDFM::PARALLELS_LIST_VMS`); set
`PDFM_MCP_TELEMETRY_DISABLED=true` and confirm the log line and the absence of
events.

## 9. Deviations (implementation + QA notes)

- Live wire QA (`test/e2e/wire_qa_024.py`, run 2026-10-10) confirms all three
  enablement states on built binaries (keyless build, key-injected build,
  key-injected + opt-out), and that a live `tools/call`
  (`parallels_list_vms`) produced exactly one `PDFM::PARALLELS_LIST_VMS` event
  whose `event_properties` are exactly the allowlisted keys
  (`arch, duration_ms, mode, ok, os, pd_edition, service, tool, version`).
  With a throwaway key Amplitude answered `400 Invalid API key` (logged at
  warn), which doubles as proof the HTTP path ran; the raw serial never
  appears in the payload, only the sha256 `user_id`. The machine's license
  edition reported `business`.
- The SDK logs a full (anonymised) event payload at error level when a flush
  is rejected; that is accepted — it is the machine's own log and the
  identifiers in it are already the final anonymised forms.
- CI wiring (implemented): `release.yml` passes
  `AMPLITUDE_API_KEY: ${{ secrets.AMPLITUDE_API_KEY }}` into the goreleaser
  job (which the `.goreleaser.yaml` ldflags consume) and declares the
  secret `required: false` on `workflow_call`; a "Check telemetry key" step
  logs a `::warning::` when it is absent so a keyless release is visible but
  never blocks. The secret lives in the `release` environment (inventory in
  `docs/release.md` section 8 + open items). `ci.yml` snapshot builds are
  intentionally keyless. All release channels (canary/beta/production)
  report by default; to keep a channel silent, unset the secret for that
  run's environment.
