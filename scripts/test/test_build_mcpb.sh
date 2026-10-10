#!/usr/bin/env bash
set -uo pipefail
. "$(dirname "$0")/lib.sh"
S="$REPO_ROOT/scripts/build-mcpb.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export MCPB_SKIP_PACK=1 MCPB_ALLOW_THIN=1
# Minimal valid PNG signature is enough: nothing here parses the icon.
printf '\x89PNG\r\n\x1a\nfake' > "$T/icon.png"
# A real Mach-O is needed: on macOS build-mcpb.sh runs `lipo -archs` on the
# input and refuses non-Mach-O bytes. /usr/bin/true is small and portable
# (on Linux lipo is absent and the check is skipped with a warning).
cp /usr/bin/true "$T/pdfm-mcp"
chmod +x "$T/pdfm-mcp"
export MCPB_ICON="$T/icon.png"
OUT="$T/out dir"   # space on purpose

expect_ok "builds a bundle" "$S" 1.2.3 "$T/pdfm-mcp" "$OUT"
B="$OUT/pdfm-mcp-1.2.3.mcpb"
[[ -f $B ]] && ok "bundle exists" || bad "bundle exists"
if [[ -f $B ]]; then
  python3 -I - "$B" "$T/pdfm-mcp" <<'PY' && ok "exec bit, byte identity, manifest version" || bad "exec bit, byte identity, manifest version"
import sys, zipfile, json
z = zipfile.ZipFile(sys.argv[1])
zi = z.getinfo("server/pdfm-mcp")
assert (zi.external_attr >> 16) & 0o111, "not executable"
assert z.read(zi) == open(sys.argv[2], "rb").read(), "differs"
m = json.loads(z.read("manifest.json"))
assert m["version"] == "1.2.3" and m["server"]["entry_point"] == "server/pdfm-mcp"
names = z.namelist()
assert "icon.png" in names and "README.md" in names
assert not any(n.startswith(".") for n in names), names
PY
  exp=$(cut -d' ' -f1 "$B.sha256"); act=$( (command -v shasum >/dev/null && shasum -a 256 "$B" || sha256sum "$B") | cut -d' ' -f1)
  [[ $exp == "$act" ]] && ok "sha256 file matches bundle" || bad "sha256 file matches bundle"
fi
expect_ok "prerelease version" "$S" 1.2.3-rc.1 "$T/pdfm-mcp" "$OUT"
for v in v1.2.3 1.2 '1.2.3;id' '$(id)' '1.2.3 x' ''; do expect_fail "bad version '$v'" "$S" "$v" "$T/pdfm-mcp" "$OUT"; done
expect_fail "missing binary" "$S" 1.2.3 "$T/nope" "$OUT"
expect_fail "missing icon" env MCPB_ICON="$T/none.png" "$S" 1.2.3 "$T/pdfm-mcp" "$OUT"
expect_fail "wrong arg count" "$S" 1.2.3 "$T/pdfm-mcp"
# Placeholders remain in the template: strict mode must refuse, default must warn only.
expect_fail "MCPB_STRICT fails on <placeholders>" env MCPB_STRICT=1 "$S" 1.2.3 "$T/pdfm-mcp" "$OUT"
# Real pack with the unresolved pin must fail loudly (no network attempted).
expect_fail "unresolved MCPB_PIN rejected" env -u MCPB_SKIP_PACK MCPB_PIN='<pinned>' "$S" 1.2.3 "$T/pdfm-mcp" "$OUT"
# No env dump / secrets in output.
SECRET_CANARY=zzz-canary-zzz "$S" 1.2.3 "$T/pdfm-mcp" "$OUT" 2>&1 | grep -q zzz-canary && bad "output leaks environment" || ok "output does not leak environment"
finish
