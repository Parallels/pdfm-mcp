#!/usr/bin/env bash
set -uo pipefail
. "$(dirname "$0")/lib.sh"
S="$REPO_ROOT/scripts/render-server-json.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
H=$(printf 'a%.0s' $(seq 64))
echo "$H  pdfm-mcp-1.2.3.mcpb" > "$T/good.sha256"
echo "nothex  x" > "$T/bad.sha256"
echo "abc  x" > "$T/short.sha256"
N=io.github.example/pdfm-mcp

expect_ok "valid render" "$S" "$N" 1.2.3 "$T/good.sha256" "$T/out.json"
jq -e --arg h "$H" '.version=="1.2.3" and .name=="io.github.example/pdfm-mcp" and .packages[0].fileSha256==$h and (.packages[0].identifier|test("v1.2.3/pdfm-mcp-1.2.3.mcpb$"))' "$T/out.json" >/dev/null && ok "rendered values" || bad "rendered values"
expect_ok "prerelease version" "$S" "$N" 1.2.3-rc.1 "$T/good.sha256" "$T/o2.json"
expect_fail "leading v rejected" "$S" "$N" v1.2.3 "$T/good.sha256" "$T/o3.json"
expect_fail "bad version" "$S" "$N" 1.2 "$T/good.sha256" "$T/o3.json"
expect_fail "non-hex hash" "$S" "$N" 1.2.3 "$T/bad.sha256" "$T/o3.json"
expect_fail "short hash" "$S" "$N" 1.2.3 "$T/short.sha256" "$T/o3.json"
expect_fail "missing hash file" "$S" "$N" 1.2.3 "$T/nope" "$T/o3.json"
expect_fail "name with quote" "$S" 'io.github.x/a"b' 1.2.3 "$T/good.sha256" "$T/o3.json"
expect_fail "name with sed delimiter" "$S" 'io.github.x/a|b' 1.2.3 "$T/good.sha256" "$T/o3.json"
expect_fail "name without slash" "$S" noslash 1.2.3 "$T/good.sha256" "$T/o3.json"
expect_fail "name with two slashes" "$S" a/b/c 1.2.3 "$T/good.sha256" "$T/o3.json"
expect_fail "wrong arg count" "$S" "$N" 1.2.3
[[ ! -e "$T/o3.json" ]] && ok "no output written on failure" || bad "no output written on failure"
# leftover token
printf '{"name":"@NAME@","v":"@VERSION@","h":"@SHA256@","x":"@OTHER@"}\n' > "$T/t.tmpl"
RENDER_SERVER_JSON_TEMPLATE="$T/t.tmpl" expect_fail "leftover @TOKEN@ rejected" "$S" "$N" 1.2.3 "$T/good.sha256" "$T/o4.json"
finish
