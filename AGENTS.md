# AGENTS.md

Rules for AI agents working in this repo:

- Read `docs/specs/` before implementing; implement one spec at a time and update its status.
- Never write to stdout. Log with `slog` to stderr.
- Keep `main.go` thin; logic goes in `internal/`.
- Every tool: file in `internal/tools/`, `Register<Name>` function, unit test plus in-memory protocol test, entry in `server.New`.
- Tool handlers must be callable with a **nil** `*mcp.CallToolRequest` — the sequence orchestrator calls them in-process through `orchestrator.FromHandler`. Use `req` only inside `if req != nil`.
- Every new VM-scoped tool must also be added to `tools.RegisterSteps` so it is available as a `parallels_vm_sequence` step; the drift-guard test (`TestSequenceDriftGuard`) fails otherwise.
- Run `make check` and `make test-e2e` before declaring work done.
- Never commit secrets, tokens or `.env` files; never hardcode credentials. Read secrets from environment variables and never log them.
- Do not add dependencies without a reason documented in the relevant spec.
- Treat tool arguments as untrusted input: validate, bound sizes, avoid shell interpolation, and never build file paths from raw input without cleaning and confining them.
