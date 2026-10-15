#!/usr/bin/env bash
# sync-plugins.sh [--check]
#
# Keeps every plugin/extension manifest version equal to VERSION and validates
# the plugin and marketplace JSON for Codex, Claude Code, Cursor and Gemini CLI.
# Without --check it rewrites the versions in place.
# Reads no environment secrets and never evals its inputs.
set -euo pipefail

die() { printf 'sync-plugins: error: %s\n' "$*" >&2; exit 1; }

mode=sync
case "${1:-}" in
  "") ;;
  --check) mode=check ;;
  *) die "usage: sync-plugins.sh [--check]" ;;
esac

command -v jq >/dev/null 2>&1 || die "jq is required"
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
plugin=plugins/pdfm-mcp

# Manifests that carry the release version (keep in step with scripts/version.py).
version_files=(
  "$plugin/.codex-plugin/plugin.json"
  "$plugin/.claude-plugin/plugin.json"
  "$plugin/.cursor-plugin/plugin.json"
  "gemini-extension.json"
)
# Catalogs and configs that are validated but carry no version.
other_files=(
  "$plugin/.mcp.json"
  ".agents/plugins/marketplace.json"
  ".claude-plugin/marketplace.json"
  ".cursor-plugin/marketplace.json"
)

version=$(tr -d '[:space:]' < "$root/VERSION")
ver_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ $version =~ $ver_re ]] || die "VERSION is not a stable SemVer"

for f in "${version_files[@]}" "${other_files[@]}"; do
  [[ -f $root/$f ]] || die "missing $f"
  jq empty "$root/$f" 2>/dev/null || die "invalid JSON: $f"
done

# MCP server definition: the plugin config and the Gemini extension must launch the same command.
jq -e '.mcpServers["pdfm-mcp"].command | type == "string" and length > 0' "$root/$plugin/.mcp.json" >/dev/null \
  || die ".mcp.json must define mcpServers.pdfm-mcp.command"
want=$(jq -c '.mcpServers["pdfm-mcp"]' "$root/$plugin/.mcp.json")
got=$(jq -c '.mcpServers["pdfm-mcp"]' "$root/gemini-extension.json")
[[ $want == "$got" ]] || die "gemini-extension.json mcpServers.pdfm-mcp differs from $plugin/.mcp.json"

# Plugin manifests.
for f in "${version_files[@]}"; do
  jq -e '.name == "pdfm-mcp"' "$root/$f" >/dev/null || die "$f must be named pdfm-mcp"
done
jq -e '.mcpServers == "./.mcp.json"' "$root/$plugin/.codex-plugin/plugin.json" >/dev/null \
  || die "codex plugin.json must reference ./.mcp.json"
jq -e '.mcpServers == "./.mcp.json"' "$root/$plugin/.cursor-plugin/plugin.json" >/dev/null \
  || die "cursor plugin.json must reference ./.mcp.json"
for asset in "$plugin/assets/icon.png" "$plugin/assets/logo.png"; do
  [[ -f $root/$asset ]] || die "missing $asset"
done

# Marketplaces list the plugin at the right path.
jq -e '.plugins | any(.name == "pdfm-mcp" and .source.path == "./plugins/pdfm-mcp")' \
  "$root/.agents/plugins/marketplace.json" >/dev/null || die ".agents marketplace must list pdfm-mcp at ./plugins/pdfm-mcp"
jq -e '.plugins | any(.name == "pdfm-mcp" and .source == "./plugins/pdfm-mcp")' \
  "$root/.claude-plugin/marketplace.json" >/dev/null || die ".claude-plugin marketplace must list pdfm-mcp at ./plugins/pdfm-mcp"
jq -e '.plugins | any(.name == "pdfm-mcp" and .source == "plugins/pdfm-mcp")' \
  "$root/.cursor-plugin/marketplace.json" >/dev/null || die ".cursor-plugin marketplace must list pdfm-mcp at plugins/pdfm-mcp"

# Versions.
changed=0
for f in "${version_files[@]}"; do
  current=$(jq -r '.version' "$root/$f")
  [[ $current == "$version" ]] && continue
  if [[ $mode == check ]]; then
    die "$f version $current != VERSION $version (run make sync-plugins)"
  fi
  tmp=$(mktemp)
  jq --arg v "$version" '.version = $v' "$root/$f" > "$tmp"
  cp "$tmp" "$root/$f"
  rm -f "$tmp"
  printf 'sync-plugins: %s -> %s\n' "$f" "$version" >&2
  changed=1
done
[[ $changed -eq 1 ]] || printf 'sync-plugins: ok (%s)\n' "$version" >&2
