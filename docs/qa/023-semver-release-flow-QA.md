# QA 023 — SemVer release flow

Scope: [spec 023](../specs/023-semver-release-flow.md), adapting the local
PLAI-client-agent release PR and channel flow to specs 013–016/022.

Executed 2026-10-10 on the developer Mac. No release or remote PR was created.

## Automated verification

| Check | Result |
|---|---|
| `make check` | Pass: formatting, vet, lint (0 issues), race-enabled Go tests |
| `make test-e2e` | Pass: stdio + bundle tests, local read-only Parallels checks |
| `make test-scripts` | Pass: 17 bundle, 15 registry, 35 version-guard assertions; 12 Python tests |
| `actionlint .github/workflows/{ci,release,create_release_pr}.yml` | Pass |
| `make release-check` | Pass: GoReleaser 2.12.5 configuration valid |
| `make release-snapshot` | Pass: unsigned universal archive, SHA256, SBOM and generated cask |
| Snapshot checksum verification | Archive + SBOM OK |
| Extracted snapshot `lipo -archs` | `arm64 x86_64` |
| Extracted snapshot `--version` | `pdfm-mcp 0.1.0-snapshot (commit 2cef909, built 2026-10-10T06:57:32Z)` |

The initial sandbox lint/snapshot attempts could not write the default Go
caches. Successful checks used temporary writable Go and linter caches. The
initial sandbox e2e attempt could not reach the Parallels service; the suite
passed outside the sandbox with optional VM capture/exec/key/sequence/wait
environment opt-ins explicitly unset. No VM mutation was requested.

Version tests cover bump resets, strict SemVer and prerelease identifiers,
run-number validation, channel tag/status output, VERSION matching, absent
files, output files and no stdout logging. Git fixture tests cover dirty or
untracked files, tag/HEAD mismatch, numeric stable progression across all tags,
main ancestry, authoritative origin/main and rejection of master-only repos.
Changelog tests cover PR body sections, HTML comments, multiline bullets, title
fallbacks, release-PR exclusion, duplicate entries, preserved old versions,
missing/empty/duplicate sections and exact extraction of a requested version.

## GitHub rehearsal pending

1. Commit/push the implementation and configure Actions PR creation,
   optional `GH_WORKFLOW_TOKEN` for generated PR CI, the `release` environment
   reviewers, five `MACOS_*` secrets and bot branch/tag permissions.
2. Run Create Release PR on main with each bump type in a rehearsal repository.
   Check the lower version components reset, branch/PR are correct (no label required),
   notes contain only merged main changes through the checkout, and CI runs.
3. On the real repository, run Release/canary then beta. Verify plain
   `v0.1.0-canary.N` / `v0.1.0-beta.N` tags, correct prerelease status,
   Developer ID/notarization and exact version/commit metadata. MCPB, tap and
   registry must skip. Published prereleases must remain available.
4. In a rehearsal repository, verify a missing signing secret fails before
   tag creation/publication, mismatched VERSION or an off-main commit fails,
   and an existing release is skipped without deletion or tag movement.
5. Resolve existing Legal/license/brand/distribution prerequisites in
   [the runbook](../release.md), then run production after owner approval.
   Production must reject unresolved bundle placeholders before publishing.
   Confirm signed universal archive, reviewed notes and stable MCPB; enable
   tap/registry only after their existing prerequisites and approvals.
6. Test recovery via Re-run failed jobs; verify SHA/run-number stay fixed and
   bundle uploads refuse to overwrite existing published assets.

Signing, GitHub permissions and downstream publication require real secrets
and infrastructure and were not represented as locally verified.
