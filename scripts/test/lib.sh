# Tiny assertion helpers for the script tests. Source this file.
PASS=0; FAIL=0
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
ok()   { PASS=$((PASS+1)); printf '  ok   %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf '  FAIL %s\n' "$1"; }
# expect_ok <desc> <cmd...>   /   expect_fail <desc> <cmd...>
expect_ok()   { local d=$1; shift; if "$@" >/dev/null 2>&1; then ok "$d"; else bad "$d"; fi; }
expect_fail() { local d=$1; shift; if "$@" >/dev/null 2>&1; then bad "$d"; else ok "$d"; fi; }
finish() { printf '%s: %d passed, %d failed\n' "$(basename "$0")" "$PASS" "$FAIL"; [[ $FAIL -eq 0 ]]; }
