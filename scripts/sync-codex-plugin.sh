#!/usr/bin/env bash
# sync-codex-plugin.sh [--check]
#
# Keeps the Codex plugin manifest version equal to VERSION and validates the
# plugin/marketplace JSON. Without --check it rewrites the version in place.
# Reads no environment secrets and never evals its inputs.
set -euo pipefail

die() { printf 'sync-codex-plugin: error: %s\n' "$*" >&2; exit 1; }

mode=sync
case "${1:-}" in
  "") ;;
  --check) mode=check ;;
  *) die "usage: sync-codex-plugin.sh [--check]" ;;
esac

command -v jq >/dev/null 2>&1 || die "jq is required"
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
manifest="$root/plugins/pdfm-mcp/.codex-plugin/plugin.json"
mcp="$root/plugins/pdfm-mcp/.mcp.json"
market="$root/.agents/plugins/marketplace.json"

version=$(tr -d '[:space:]' < "$root/VERSION")
ver_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ $version =~ $ver_re ]] || die "VERSION is not a stable SemVer"

for f in "$manifest" "$mcp" "$market"; do
  [[ -f $f ]] || die "missing ${f#"$root"/}"
  jq empty "$f" 2>/dev/null || die "invalid JSON: ${f#"$root"/}"
done

jq -e '.mcpServers["pdfm-mcp"].command | type == "string" and length > 0' "$mcp" >/dev/null \
  || die ".mcp.json must define mcpServers.pdfm-mcp.command"
jq -e '.name == "pdfm-mcp" and .mcpServers == "./.mcp.json"' "$manifest" >/dev/null \
  || die "plugin.json must be named pdfm-mcp and reference ./.mcp.json"
jq -e '.plugins | any(.name == "pdfm-mcp" and .source.path == "./plugins/pdfm-mcp")' "$market" >/dev/null \
  || die "marketplace.json must list pdfm-mcp at ./plugins/pdfm-mcp"
[[ -d "$root/plugins/pdfm-mcp" ]] || die "plugin directory missing"

current=$(jq -r '.version' "$manifest")
if [[ $current != "$version" ]]; then
  if [[ $mode == check ]]; then
    die "plugin.json version $current != VERSION $version (run make sync-codex-plugin)"
  fi
  tmp=$(mktemp)
  trap 'rm -f "$tmp"' EXIT
  jq --arg v "$version" '.version = $v' "$manifest" > "$tmp"
  cp "$tmp" "$manifest"
  printf 'sync-codex-plugin: plugin.json version -> %s\n' "$version" >&2
else
  printf 'sync-codex-plugin: ok (%s)\n' "$version" >&2
fi
