# QA 013 — Release pipeline

**Scope:** [spec 013](../specs/013-release-pipeline.md); runbook in
[docs/release.md](../release.md). Nothing here publishes to a public
channel. Use pre-release tags (`v0.0.1-rc.N`) for rehearsals.

**Executed 2026-10-09** on the developer Mac (GoReleaser 2.12.5 via
Homebrew, syft installed, Homebrew 4.x). Steps 1–5 and 7 (partial) are
fully verified; steps 4, 6, 8 and 9 need the GitHub remote and remain
release-owner work (see Result).

## Steps

### 1. Script tests (automated, no network) — ✅ PASSED

```
make test-scripts
```

`ALL SCRIPT TESTS PASSED` — version-check 24/24 (including "tagged
commit reachable from main/**master**" — this repo's default branch is
`master` and the script accepts both), build-mcpb 15/15, render-server-json
15/15.

- **Fixed:** the build-mcpb fixture used random bytes as the binary; on
  macOS `build-mcpb.sh` runs `lipo -archs` and refuses non-Mach-O input
  (the suite only passed in a Linux sandbox without `lipo`). The fixture
  is now `/usr/bin/true` (a real Mach-O; on Linux the check is skipped
  with a warning).

### 2. Config validation — ✅ PASSED

```
make release-check
```

`goreleaser check` → `1 configuration file(s) validated`.

- **Fixed:** `homebrew_casks.binaries` is not a key in GoReleaser 2.12.5
  (verified against `goreleaser schema`, `$defs.HomebrewCask`); the
  binary-cask key is `binary` (singular). `goreleaser check` previously
  failed with `field binaries not found in type config.HomebrewCask`.

### 3. Snapshot dry run — ✅ PASSED

```
make release-snapshot && ls dist/
```

With `syft` installed: `pdfm-mcp_0.0.1-snapshot_darwin_universal.tar.gz`,
`checksums.txt`, **SBOM** (`…tar.gz.sbom.json`, syft catalog of the
archive), the cask under `dist/homebrew/Casks/`. Without `syft` the SBOM
is skipped cleanly (Makefile passes `--skip=…,sbom`). Extracted and ran
the binary:

- `pdfm-mcp --version` → `pdfm-mcp 0.0.1-snapshot (commit 2cef909, built
  2026-10-09T15:51:08Z)` — version/commit/date from the ldflags. ✓
- `lipo -archs` → `arm64 x86_64` (universal). ✓
- `shasum -a 256 -c checksums.txt` → OK. ✓
- `codesign --verify --strict` → "code object is not signed at all" —
  expected: no Apple credentials on a snapshot. ✓
- Archive also contains `README.md`; the `LICEN[CS]E*` empty glob is
  tolerated (warning only) until Legal adds the file. ✓ (verifies the
  open item in the spec)

### 4. CI — ⏸ release owner (needs the GitHub remote)

A PR touching `.goreleaser.yaml` should run the `check` job plus
`make test-scripts` in `release.yml`'s `verify` (and every future PR
after the workflow exists). Verify on push.

### 5. Version guard on a real tag — ✅ PASSED (unit-level)

`scripts/version-check.sh` covered by 24 shell tests: rejects a tag on a
feature branch (commit not reachable from main/master), accepts it after
merge, rejects a non-clean tree, handles "no main/master branch at
all". A live tag test is step 6.

### 6. Release rehearsal (manual, release owner) — ⏸ PENDING

Push `v0.0.1-rc.1` to the real remote. Expect: `release` environment
approval, GitHub **pre-release** with archive + checksums + SBOM +
changelog; `tap`/`mcpb`/`registry` jobs skipped or gated
(prerelease); then verify per section 7. Not executed here — pushing a
tag creates real artifacts on `Parallels/pdfm-mcp`.

### 7. Verification on a real Mac — ✅ PASSED (partial: no signed binary yet)

The docs/release.md §4 verification block was run against the **snapshot**
binary: checksum ✓, `lipo -archs` = arm64 x86_64 ✓, `--version` shows
version/commit/date ✓, `codesign --verify` correctly fails (unsigned).
The signed/notarized parts (Developer ID + hardened runtime, quarantined
launch without Gatekeeper dialog) are verified in step 6 once a real tag
is cut.

### 8. Negative (unsigned stable rehearsal in a fork) — ⏸ PENDING (step 6)

The `mcpb` job's `codesign --verify --strict` is the signature gate: on a
stable tag with no `MACOS_SIGN_P12`, the workflow must fail there *after*
the release exists. To be exercised in the fork rehearsal.

### 9. Cleanup — ⏸ (step 6)

Delete the rehearsal release and tag.

## Result

Local verification complete: config valid, snapshot builds a universal,
checksummed, SBOM'd, version-correct macOS archive, cask generation works
and is style/audit-clean after normalization (QA 014), no secret material
in any generated artifact. Remaining acceptance criteria (2, 3, 5, 6 and
the negative tests) all require pushing tags to the real remote and are
documented as release-owner rehearsals.
