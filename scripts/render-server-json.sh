#!/usr/bin/env bash
# render-server-json.sh <name> <version> <sha256-file> <out>
#
# Renders packaging/registry/server.json.tmpl (spec 016 section 5).
#   name        reverse-DNS registry name, exactly one slash
#   version     SemVer without a leading "v"
#   sha256-file output of `shasum -a 256` for the .mcpb ("<hash>  <file>")
#   out         destination file
# Reads no environment secrets and never evals its inputs.
set -euo pipefail

die() { printf 'render-server-json: error: %s\n' "$*" >&2; exit 1; }

[[ $# -eq 4 ]] || die "usage: render-server-json.sh <name> <version> <sha256-file> <out>"
name=$1 version=$2 sha_file=$3 out=$4

[[ -n $name ]] || die "server name is empty; set MCP_SERVER_NAME to io.github.<repository-owner>/pdfm-mcp for GitHub OIDC"
name_re='^[A-Za-z0-9.-]+/[A-Za-z0-9._-]+$'
[[ ${#name} -le 200 && $name =~ $name_re ]] || die "invalid server name (expected reverse-DNS namespace/name)"

ver_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$'
[[ ${#version} -le 64 && $version =~ $ver_re ]] || die "invalid version (SemVer, no leading v)"

[[ -f $sha_file ]] || die "sha256 file not found"
command -v jq >/dev/null 2>&1 || die "jq is required"

read -r sha _ < "$sha_file" || true
sha=${sha:-}
sha_re='^[0-9a-fA-F]{64}$'
[[ $sha =~ $sha_re ]] || die "sha256 file does not start with a 64-hex-char hash"
sha=$(printf '%s' "$sha" | tr 'A-F' 'a-f')

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
tmpl="${RENDER_SERVER_JSON_TEMPLATE:-$script_dir/../packaging/registry/server.json.tmpl}"
[[ -f $tmpl ]] || die "template not found"

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
# All three values are validated above: none can contain "|", "&", "\" or quotes.
sed -e "s|@NAME@|$name|g" -e "s|@VERSION@|$version|g" -e "s|@SHA256@|$sha|g" "$tmpl" > "$tmp"

jq empty "$tmp" 2>/dev/null || die "rendered output is not valid JSON"
if grep -Eq '@[A-Z0-9_]+@' "$tmp"; then
  die "unreplaced @TOKEN@ remains in the rendered output"
fi
jq -e --arg n "$name" --arg v "$version" --arg h "$sha" \
  '.name == $n and .version == $v and .packages[0].fileSha256 == $h' "$tmp" >/dev/null \
  || die "rendered values do not match the inputs"

mkdir -p "$(dirname "$out")"
cp "$tmp" "$out"
printf 'render-server-json: wrote %s\n' "$out" >&2
