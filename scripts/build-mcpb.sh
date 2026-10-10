#!/usr/bin/env bash
# build-mcpb.sh <version> <path-to-binary> <out-dir>
#
# Assembles dist/pdfm-mcp-<version>.mcpb from an EXISTING binary (spec 015
# section 5). It never builds Go code: the release bundle must contain the
# exact notarized binary from the GitHub Release. OCR is compiled into the
# binary (spec 022), so the bundle carries only server/pdfm-mcp.
#
# Environment (all optional, none are secrets):
#   MCPB_PIN              pinned @anthropic-ai/mcpb version; default: first
#                         line of packaging/mcpb/mcpb-version.txt
#   MCPB_ICON             icon path; default packaging/mcpb/icon.png
#   MCPB_ALLOW_THIN=1     accept a non-universal binary (dev builds only)
#   MCPB_STRICT=1         fail while <placeholder> values remain in the
#                         manifest (the release workflow sets this)
#   MCPB_SKIP_PACK=1      TEST ONLY: skip npx validate/pack and zip with
#                         python3 instead. The result is NOT validated.
set -euo pipefail

die()  { printf 'build-mcpb: error: %s\n' "$*" >&2; exit 1; }
warn() { printf 'build-mcpb: warning: %s\n' "$*" >&2; }

[[ $# -eq 3 ]] || die "usage: build-mcpb.sh <version> <path-to-binary> <out-dir>"
version=$1 bin=$2 outdir=$3

ver_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$'
[[ ${#version} -le 64 && $version =~ $ver_re ]] || die "version must be SemVer without a leading v"
[[ -f $bin ]] || die "binary not found"
[[ -n $outdir ]] || die "out-dir is empty"

for tool in jq python3 sed; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done

sha256_of() {
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
  elif command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else die "neither shasum nor sha256sum is available"; fi
}

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(cd "$script_dir/.." && pwd)
tmpl="$root/packaging/mcpb/manifest.json.tmpl"
[[ -f $tmpl ]] || die "manifest template not found"
icon=${MCPB_ICON:-$root/packaging/mcpb/icon.png}
[[ -f $icon ]] || die "icon not found (packaging/mcpb/icon.png is a brand-approved asset that must be supplied, or set MCPB_ICON)"

# Universal-binary check (spec 015 section 5, step 1).
universal_ok() { # $1 = mach-o path, $2 = label
  local archs
  if command -v lipo >/dev/null 2>&1; then
    archs=$(lipo -archs "$1" 2>/dev/null) || die "$2 is not a Mach-O file"
    if [[ ${MCPB_ALLOW_THIN:-0} != 1 ]]; then
      [[ " $archs " == *" arm64 "* && " $archs " == *" x86_64 "* ]] \
        || die "$2 is not universal (lipo reports: $archs)"
    fi
  else
    warn "lipo not available: universal-binary check skipped for $2"
  fi
}
universal_ok "$bin" "binary"

stage=$(mktemp -d)
verify=$(mktemp -d)
trap 'rm -rf "$stage" "$verify"' EXIT

# Stage exactly the bundle contents.
sed "s|@VERSION@|$version|g" "$tmpl" > "$stage/manifest.json"
jq empty "$stage/manifest.json" 2>/dev/null || die "rendered manifest.json is not valid JSON"
if grep -Eq '@[A-Z0-9_]+@' "$stage/manifest.json"; then die "unreplaced @TOKEN@ in manifest"; fi
jq -e --arg v "$version" '.version == $v' "$stage/manifest.json" >/dev/null || die "manifest version mismatch"

left=$(jq -r '[.. | strings | select(test("<[^>]+>"))] | length' "$stage/manifest.json")
if [[ $left -gt 0 ]]; then
  if [[ ${MCPB_STRICT:-0} == 1 ]]; then
    die "manifest still contains $left <placeholder> value(s); fill them in (docs/release.md, Open items)"
  fi
  warn "manifest still contains $left <placeholder> value(s); not release-ready"
fi

mkdir -p "$stage/server"
cp "$bin" "$stage/server/pdfm-mcp"
chmod 0755 "$stage/server/pdfm-mcp"
cp "$icon" "$stage/icon.png"
cp "$root/README.md" "$stage/README.md"
[[ -f $root/LICENSE ]] && cp "$root/LICENSE" "$stage/LICENSE"
cp "$root/packaging/mcpb/.mcpbignore" "$stage/.mcpbignore"

mkdir -p "$outdir"
outdir=$(cd "$outdir" && pwd)
out="$outdir/pdfm-mcp-$version.mcpb"
rm -f "$out" "$out.sha256"

if [[ ${MCPB_SKIP_PACK:-0} == 1 ]]; then
  warn "MCPB_SKIP_PACK=1: bundle is zipped with python3 and NOT validated by the mcpb CLI (tests only)"
  python3 -I - "$stage" "$out" <<'PY'
import os, sys, zipfile
stage, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for dirpath, dirs, files in os.walk(stage):
        dirs.sort()
        for f in sorted(files):
            if f == ".mcpbignore":
                continue
            p = os.path.join(dirpath, f)
            arc = os.path.relpath(p, stage)
            zi = zipfile.ZipInfo(arc, date_time=(1980, 1, 1, 0, 0, 0))
            mode = 0o755 if arc == "server/pdfm-mcp" else 0o644
            zi.external_attr = (0o100000 | mode) << 16
            zi.compress_type = zipfile.ZIP_DEFLATED
            with open(p, "rb") as fh:
                z.writestr(zi, fh.read())
PY
else
  pin=${MCPB_PIN:-}
  if [[ -z $pin && -f $root/packaging/mcpb/mcpb-version.txt ]]; then
    read -r pin < "$root/packaging/mcpb/mcpb-version.txt" || true
  fi
  [[ $pin =~ $ver_re ]] || die "MCPB_PIN is not a pinned version (set MCPB_PIN or packaging/mcpb/mcpb-version.txt)"
  command -v npx >/dev/null 2>&1 || die "npx (Node.js) is required to pack"
  (cd "$stage" && npx --yes "@anthropic-ai/mcpb@$pin" validate manifest.json)
  npx --yes "@anthropic-ai/mcpb@$pin" pack "$stage" "$out"
fi
[[ -f $out ]] || die "packer did not produce $out"

hash=$(sha256_of "$out")
printf '%s  %s\n' "$hash" "$(basename "$out")" > "$out.sha256"

# Integrity check of the packed result: entry present, executable, identical.
python3 -I - "$out" "$bin" <<'PY' || die "bundle verification failed"
import sys, zipfile
out, src = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out) as z:
    try:
        zi = z.getinfo("server/pdfm-mcp")
    except KeyError:
        sys.exit("server/pdfm-mcp missing from bundle")
    if not (zi.external_attr >> 16) & 0o111:
        sys.exit("server/pdfm-mcp is not executable in the bundle")
    if z.read(zi) != open(src, "rb").read():
        sys.exit("server/pdfm-mcp differs from the input binary")
    for required in ("manifest.json", "icon.png"):
        if required not in z.namelist():
            sys.exit(required + " missing from bundle")
PY

printf 'build-mcpb: %s\nbuild-mcpb: sha256 %s\n' "$out" "$hash"
