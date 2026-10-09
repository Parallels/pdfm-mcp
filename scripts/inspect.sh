#!/usr/bin/env bash
# Launch the MCP Inspector against a stdio server binary.
# Node/npx is only needed for this debugging UI, not for the server itself.
set -euo pipefail
BIN="${1:?usage: inspect.sh <path-to-binary> [args...]}"
shift || true
exec npx --yes @modelcontextprotocol/inspector "$BIN" "$@"
