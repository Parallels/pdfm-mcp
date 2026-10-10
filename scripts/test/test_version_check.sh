#!/usr/bin/env bash
set -uo pipefail
. "$(dirname "$0")/lib.sh"
S="$REPO_ROOT/scripts/version-check.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
unset GITHUB_OUTPUT
git_() { git -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false -c tag.gpgsign=false "$@"; }
set_version() { printf '%s\n' "$1" > VERSION; git_ add VERSION; git_ commit -qm version; }

cd "$T" && git init -q -b main . && echo a > f && git_ add f && git_ commit -qm init
expect_fail "missing VERSION fails closed" "$S" v1.2.3
set_version 1.2.3
for t in v1.2.3 v1.2.3-rc.1 v1.2.3-canary.42 v1.2.3-beta.42; do expect_ok "valid matching tag $t" "$S" "$t"; done
for t in 1.2.3 v1.2 v1.2.3.4 v01.2.3 v1.2.3- 'v1.2.3;id' 'v1.2.3 x' '' 'v1.2.3+build' 'v1.2.3-$(id)' v1.2.3-rc.01 v1.2.3-rc..1 v1.2.3-rc. v1.2.4 v1.2.4-beta.1; do
  expect_fail "invalid or mismatched '$t'" "$S" "$t"
done
expect_fail "no argument" "$S"
expect_fail "two arguments" "$S" v1.2.3 v1.2.4

echo b >> f
expect_fail "dirty tree" "$S" v1.2.3
git_ checkout -q -- f
touch untracked
expect_fail "untracked file counts as dirty" "$S" v1.2.3
rm untracked

git_ tag -a v1.2.3 -m stable
expect_ok "existing annotated tag on HEAD" "$S" v1.2.3
expect_fail "prerelease after same stable version" "$S" v1.2.3-beta.43
echo c >> f && git_ commit -qam second
expect_fail "HEAD differs from tag" "$S" v1.2.3
set_version 1.2.4
expect_ok "newer patch" "$S" v1.2.4
git_ tag v2.0.0
expect_fail "stable regression checks all tags" "$S" v1.2.4
git_ tag -d v2.0.0 >/dev/null
set_version 1.10.0
expect_ok "numeric ordering handles double digits" "$S" v1.10.0

git_ checkout -q -b feature && echo d >> f && git_ commit -qam feat
expect_fail "commit outside main" "$S" v1.10.0
git_ checkout -q main && git_ merge -q --ff-only feature
expect_ok "after merge to main" "$S" v1.10.0
git_ update-ref refs/remotes/origin/main HEAD
echo e >> f && git_ commit -qam unpushed
expect_fail "unpushed local main cannot override origin/main" "$S" v1.10.0
git_ update-ref refs/remotes/origin/main HEAD
expect_ok "pushed main" "$S" v1.10.0
git_ update-ref -d refs/remotes/origin/main
git_ branch -m main master
expect_fail "master no longer authorizes releases" "$S" v1.10.0
finish
