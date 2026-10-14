#!/usr/bin/env bash
# Exercise the full installer with local release fixtures and no network.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/mock" "$T/fixture" "$T/destination"
cat > "$T/fixture/pdfm-mcp" <<'EOF'
#!/usr/bin/env bash
echo 'pdfm-mcp fixture'
EOF
chmod +x "$T/fixture/pdfm-mcp"
cat > "$T/mock/uname" <<'EOF'
#!/usr/bin/env bash
echo Darwin
EOF
cat > "$T/mock/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
url=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) output="$2"; shift ;;
    -w) shift ;;
    -*) ;;
    *) url="$1" ;;
  esac
  shift
done
base="https://github.com/Parallels/pdfm-mcp/releases"
asset="pdfm-mcp_${TEST_TAG#v}_darwin_universal.tar.gz"
case "$url" in
  https://api.github.com/repos/Parallels/pdfm-mcp/releases/latest|https://api.github.com/repos/Parallels/pdfm-mcp/releases)
    printf '{"tag_name": "%s"}\n' "$TEST_TAG" > "$output" ;;
  "$base/$TEST_TAG") ;;
  "$base/download/$TEST_TAG/$asset") cp "$TEST_FIXTURE/$asset" "$output" ;;
  "$base/download/$TEST_TAG/checksums.txt") cp "$TEST_FIXTURE/checksums.txt" "$output" ;;
  *) printf 'Unexpected URL: %s\n' "$url" >&2; printf 404; exit 0 ;;
esac
printf 200
EOF
chmod +x "$T/mock/"*
export PATH="$T/mock:$PATH" TEST_FIXTURE="$T/fixture"
unset PDFM_MCP_REPO PDFM_MCP_GH PDFM_MCP_API

check_install() {
  local description="$1"
  shift
  local asset="pdfm-mcp_${TEST_TAG#v}_darwin_universal.tar.gz"
  tar -czf "$T/fixture/$asset" -C "$T/fixture" pdfm-mcp
  (cd "$T/fixture" && shasum -a 256 "$asset" > checksums.txt)
  rm -f "$T/destination/pdfm-mcp"
  if bash "$REPO_ROOT/scripts/install.sh" --path "$T/destination" "$@" > "$T/install.log" 2>&1 &&
    cmp -s "$T/fixture/pdfm-mcp" "$T/destination/pdfm-mcp"; then
    ok "$description"
  else
    cat "$T/install.log" >&2
    bad "$description"
  fi
}

export TEST_TAG=v0.1.2
check_install 'prefixed stable version' --version v0.1.2
check_install 'unprefixed stable version' --version 0.1.2
check_install 'latest stable release'
export TEST_TAG=v0.1.3-beta.4
check_install 'prefixed prerelease version' --version v0.1.3-beta.4
check_install 'unprefixed prerelease version' --version 0.1.3-beta.4
check_install 'latest prerelease' --pre-release
finish
