# Spec 023: SemVer release flow

| | |
|---|---|
| Status | Implemented (local verification complete 2026-10-10; label-free release PR fix 2026-10-12; GitHub signing/publication rehearsal pending release owner) |
| Scope | Version file, release PR workflow, release channel selection, changelog, release guards, CI and runbook |
| Reference | an internal Parallels release-flow reference: `create_release_pr.yml`, `release.yml`, `scripts/version.sh` |

## Goal

Adopt PLAI's reviewed version bumps and canary/beta/production release flow
while preserving specs 013–016 and the macOS/cgo build in spec 022.

## Decisions

- `VERSION` stores strict `MAJOR.MINOR.PATCH`, initially `0.1.0`. A manual
  Create Release PR workflow increments major/minor/patch and proposes
  `VERSION` plus `CHANGELOG.md` on `release/<version>`, targeting `main`.
- Changelog entries come from merged PRs on `main` since the previous release
  PR. Prefer their `## Changelog` bullets and fall back to PR titles. No AI
  service or credentials are required. Release owners review the generated PR.
- Release accepts manual/reusable channel selection, plus the existing `v*`
  tag trigger. Canary/beta use `VERSION-canary.<run_number>` and
  `VERSION-beta.<run_number>`; production uses `VERSION`. Tags remain plain
  `v<version>` and GoReleaser injects the resolved version into the binary.
- All jobs check out the workflow's original commit SHA. Release guards
  validate full SemVer prerelease identifiers (no build metadata), matching
  VERSION, a clean tree, main ancestry, tag identity, and increasing stable
  versions against every existing stable tag. Existing releases are skipped;
  tags are never moved and releases are never deleted/replaced automatically.
- Production requires a nonempty changelog section for VERSION. Prereleases
  use that section when available, otherwise GoReleaser's existing changelog.
- The workflow creates its tag after verification and signing-secret checks,
  using `GITHUB_TOKEN`, then builds in the same run. This avoids relying on
  token-created pushes to trigger another workflow. Pin GoReleaser's current
  tag explicitly because multiple channels may tag the same commit.
- Keep universal darwin arm64/amd64, CGO/Apple Vision, GoReleaser signing and
  notarization, SHA256, SBOM/provenance and cask generation. Require all five
  Apple secrets before publishing any channel. Preserve the protected
  `release` environment instead of introducing three signing environments.
- MCPB, gated Homebrew and gated registry stay stable-only and consume the
  same release asset. All downstream checkouts use the released SHA, including
  workflow dispatch runs. Preserve existing publication/Legal gates.
  Production checks bundle placeholders before publication; bundle uploads
  refuse to overwrite existing assets. CI's macOS bundle validation uses a
  real Mach-O fixture, matching its lipo check.
- Retain prereleases: MCP asset URLs and hashes are immutable. Do not copy
  PLAI's force rebuild, automatic prerelease cleanup, R2, multi-OS matrix,
  telemetry or backend settings.
- Python 3 standard library handles version/changelog logic (already used by
  packaging scripts); no new Go or Python dependencies. Scripts log to stderr
  and write machine results to files / `GITHUB_OUTPUT`. No stdout logging.
- CI uses `main`, checks VERSION/changelog and tests release scripts; snapshots
  use VERSION for their development version, without signing or publishing.
  Local builds report `VERSION-dev` plus commit/date; release versions come
  from the validated tag. The Go module rename is a separate existing decision.

## Acceptance criteria

1. Major/minor/patch reset the lower components correctly; malformed input,
   missing VERSION and unsafe/invalid prerelease identifiers fail closed.
2. Canary/beta/production outputs use plain SemVer tags and GitHub prerelease
   status is correct. A published stable version cannot regress, and a tag
   cannot point at a different commit or a branch outside main.
3. Release PR generation is serialized, uses main, requires no label setup,
   and cannot overwrite an existing branch. Optional `GH_WORKFLOW_TOKEN`
   permits generated PRs to trigger CI; document the default token limitation.
4. Changelog generation/extraction is deterministic and tested with PR bodies,
   missing entries, repeated sections, and newer unrelated sections. Stable
   release notes come from the reviewed VERSION section.
5. `make check`, `make test-e2e`, `make test-scripts`, `make release-check`,
   snapshot and workflow lint pass locally. Real signing/publication remains
   a release-owner rehearsal using GitHub secrets, not a local publish.

## Verification

Verified 2026-10-10; details in [QA 023](../qa/023-semver-release-flow-QA.md).

- `make check`: formatting, vet, lint (0 issues), all race-enabled unit tests.
  Sandbox execution used writable temporary Go/linter cache directories.
- `make test-e2e`: passes with access to the local Parallels service; optional
  guest command, capture, keyboard and sequence opt-ins were unset.
- `make test-scripts`: 17 bundle, 15 registry, 35 release guard assertions and
  12 Python version/changelog tests pass.
- `actionlint` passes for CI, release and release PR workflows.
- `make release-check` and `make release-snapshot` pass with GoReleaser 2.12.5
  and syft. Snapshot archive and SBOM hashes verify; extracted binary is
  arm64+x86_64 and reports `0.1.0-snapshot` with commit/date. Cask generated.
- No new dependencies, release, remote branch, PR or repository setting was
  created. Protected environment/secrets, generated PR CI and a signed channel
  release still require a GitHub rehearsal after these files are committed.

### Release PR label endpoint fix (2026-10-12)

- Removed label creation and assignment, and the Issues permission. Identify
  the previous release PR by its `release/<version>` head branch, preserving
  the merged-at cutoff. Changelog generation excludes release branches and
  retains legacy label exclusion. This avoids the failing labels REST endpoint
  when the chosen workflow token cannot access it. No new dependencies.
- Verification: `make check`, `make test-e2e`, `make test-scripts` (13 Python
  tests), workflow `actionlint`, and `git diff --check` pass. Go caches used
  writable temporary directories; checks requiring local listeners/Parallels
  were rerun outside the sandbox. GitHub workflow rerun remains pending.
