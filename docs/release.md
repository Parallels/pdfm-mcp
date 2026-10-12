# Release runbook

Implements specs [013](specs/013-release-pipeline.md) (pipeline),
[014](specs/014-homebrew-tap.md) (Homebrew), [015](specs/015-mcpb-bundle.md)
(MCPB), [016](specs/016-mcp-registry.md) (MCP Registry) and
[023](specs/023-semver-release-flow.md) (reviewed versioning and channels).

One tag produces one signed, notarized, universal macOS binary on a GitHub
Release. Every channel consumes that asset; nothing is rebuilt per channel.

**Publication gate (spec 013 section 3).** The repo is public. Downstream
public channels still need explicit owner and Legal approval before first use:
the registry job only runs when the repository variable `PUBLISH_PUBLIC` is
`'true'`, and the Homebrew tap is only updated when `PUBLISH_HOMEBREW` is
`'true'`. Release notes, archives and SBOMs must never contain security
findings, internal hostnames, VM names or customer data.

## Files

| File | Purpose |
| --- | --- |
| `.goreleaser.yaml` | build, universal binary, notarize, archive, checksums, SBOM, release, Homebrew cask |
| `VERSION`, `CHANGELOG.md` | base SemVer and reviewed notes for each version |
| `.github/workflows/create_release_pr.yml` | manual major/minor/patch bump and changelog PR to main |
| `.github/workflows/release.yml` | manual/reusable channel or tag trigger: prepare, verify, GoReleaser, stable MCPB/tap/registry |
| `.github/workflows/ci.yml` | adds `scripts` and `release-config` (goreleaser check + snapshot) jobs |
| `scripts/version.py`, `scripts/version-check.sh` | strict SemVer, version bumps/channel resolution, tag/commit/main guards, stable version advancement |
| `scripts/changelog.py` | merged PR changelog generation and exact version note extraction |
| `scripts/build-mcpb.sh` | assemble `pdfm-mcp-<v>.mcpb` from an existing binary |
| `scripts/render-server-json.sh` | render the registry `server.json` |
| `packaging/mcpb/*`, `packaging/registry/*` | manifest/server.json templates, `.mcpbignore`, packer pin |
| `scripts/test/` | plain-bash tests, `make test-scripts` |

Make targets (`make help`): `version`, `version-set` (`VERSION_TYPE=patch|minor|major`),
`increment-version` (alias), `release-check`, `release-snapshot`, `mcpb`,
`render-server-json`, `test-scripts`.

Script environment variables: `MCPB_PIN`, `MCPB_ICON`, `MCPB_ALLOW_THIN=1`
(dev, non-universal binary), `MCPB_STRICT=1` (fail on `<placeholder>`
values; set by the release job), `MCPB_SKIP_PACK=1` (tests only: python zip
instead of the mcpb CLI, bundle not validated; never use for a release).

## 1. Pre-flight

1. `main` is green; `make check` and `make test-e2e` pass; `make test-scripts` passes.
2. QA docs for specs changed since the last release are done on a real Mac.
3. All values in "Open items before the first release" below are resolved.
4. `make release-check` and `make release-snapshot` succeed locally.

## 2. Bump and cut

For the first release, the base version and initial notes are `0.1.0`.
For subsequent versions, run **Create Release PR** from `main`, choosing
`patch`, `minor` or `major`. It opens `release/<version>` against `main` with
VERSION and a generated changelog. Review the notes for accuracy and publication
suitability, adjust them as needed, wait for CI and merge. The workflow collects
merged PRs on main since the previous release PR up to the checked-out commit,
using their `## Changelog` bullets or their title as a fallback. The
previous release PR is identified by its `release/<version>` branch. No labels
or AI API key are needed.

Run **Release** from `main` and choose:

| Input | Resolved tag | GitHub status | Downstream channels |
| --- | --- | --- | --- |
| `canary` | `vX.Y.Z-canary.<workflow run number>` | prerelease | none |
| `beta` | `vX.Y.Z-beta.<workflow run number>` | prerelease | none |
| `production` | `vX.Y.Z` | stable | MCPB, gated tap and registry |

The same workflow supports `workflow_call` with the `environment` input and
outputs `release_tag`, `release_version`, `release_channel`. Call it from main
and use `secrets: inherit` or supply the five `MACOS_*` secrets (plus the tap
token if enabled). The existing protected **release** environment holds signing
credentials and approvals for all channels; separate channel environments are
not required.

The workflow verifies the original SHA, VERSION, tag identity, clean tree and
main ancestry. It compares the numeric base version against all stable tags,
so stable versions cannot regress and prereleases cannot reuse an already
released stable base. Production requires exactly one nonempty changelog
section for VERSION and no unresolved MCPB manifest placeholders. All channels
require the five signing secrets before tag creation or publication.

After verification, the signing job creates/pushes the immutable tag with
`GITHUB_TOKEN` and runs GoReleaser in the same workflow. Token-created pushes
do not start another tag workflow. GoReleaser is explicitly pinned to the
selected tag, including when multiple channels tag the same commit. Every
downstream job uses that same SHA. Production release notes come from the
reviewed changelog section; prereleases use it when present and otherwise
fall back to GoReleaser's commit changelog.

Manual tag pushes remain supported. Check out the release commit on main,
ensure VERSION matches the tag's base, then:

```sh
git tag -a vX.Y.Z -m "pdfm-mcp vX.Y.Z"
git push origin vX.Y.Z
```

Tags must be `vMAJOR.MINOR.PATCH[-prerelease]`, without build metadata and with
valid SemVer identifiers (numeric prerelease identifiers cannot have leading
zeros). A pre-release suffix creates a
GitHub pre-release and never triggers downstream channels.

Local builds use `VERSION-dev` and include the commit/date. `make release-snapshot`
produces `VERSION-snapshot`, without signing or publication.

## 3. Approve and watch

The `goreleaser` job waits on the `release` environment's required reviewers.
Approve it, then watch `prepare`, `verify`, `goreleaser`, `mcpb` (and tap/registry when
the gate is open). Notarization can take up to 20 minutes.

## 4. Verify

```sh
gh release download vX.Y.Z -R Parallels/pdfm-mcp -p '*darwin_universal.tar.gz' -p checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
tar xzf pdfm-mcp_*_darwin_universal.tar.gz
codesign --verify --strict --verbose=2 ./pdfm-mcp
codesign -dv --verbose=4 ./pdfm-mcp 2>&1 | grep -E 'Authority|TeamIdentifier|flags'   # Developer ID + hardened runtime
lipo -archs ./pdfm-mcp                                                                   # arm64 x86_64
./pdfm-mcp --version
```

Also launch a quarantined copy (browser download, or
`xattr -w com.apple.quarantine "0081;00000000;Safari;" ./pdfm-mcp`) and confirm
no Gatekeeper block. `spctl --assess` is not meaningful for bare CLI binaries.
Then check the `.mcpb` and `.mcpb.sha256` assets (stable tags), the tap commit
(if enabled) and the registry entry (if the gate is open).

## 5. Roll back

Delete the GitHub Release and the tag, fix forward with the next patch. Never
re-use or force-move a published tag: Homebrew sha256 and MCPB hashes pin to
it. The workflow retains prereleases and skips existing GitHub Releases; it
does not implement force rebuild or automatic release/tag deletion. If a job
fails after the tag was created, use **Re-run failed jobs** on the original
workflow run so its SHA and channel run number stay fixed. If publication has
already succeeded, re-run only the failed downstream job. If the signature
check in the `mcpb` job fails after the release was
created, the release is unsigned: delete it immediately.
If the tap step fails (token missing/expired) the GitHub Release already
exists: fix the secret and re-run the failed **`tap`** job (it is isolated;
the release and the `mcpb` job are untouched), or commit the cask from
`dist/` by hand through a reviewed PR to the tap.
The `registry` job is independent and re-runnable; publishing the same version
twice is an error to investigate, never to force.
Bundle uploads also refuse to overwrite existing assets. If a partially
completed upload left a bundle behind, verify it and complete its matching
checksum upload, or fix forward with a new patch. Do not rebuild and overwrite
an asset whose URL/hash may already have been consumed.

## 6. Registry withdrawal and mistakes (spec 016 section 8)

A listing is public and may be mirrored — treat publishing as irreversible.
The official `mcp-publisher` CLI (verified against v1.8.1) manages status:

```
mcp-publisher status --status deprecated --message "replaced by 0.0.2" <name> <version>
mcp-publisher status --status deleted    --message "..."             <name> <version>
mcp-publisher status --status active                      <name> <version>   # restore
mcp-publisher status --status deprecated --all-versions --message "..." <name>
```

- Wrong metadata or bad build: publish a corrected higher patch version and
  `status --status deprecated` the broken one. Do not rely on editing a
  published version.
- Secret or confidential data exposed: rotate the secret first (section 7 of
  spec 013), then `status --status deleted --message "..."` the version and
  report the incident through Parallels' internal security process.
- Asset removed or repo made private: `status --status deleted` the entry
  rather than leaving a dead link.

## 7. Private repository mode (specs 014, 015; not applicable while the repo is public)

Anonymous downloads of private release assets return 404. Record the chosen
option here before the first release: (1) approved public or intranet location
for assets, (2) authenticated download with a read-only token (never pasted
into chat or tickets), (3) fallback formula that builds from source.
Chosen: n/a (repo is public, release assets download anonymously).

## 8. Secrets and variables inventory (names only)

| Name | Kind | Holds |
| --- | --- | --- |
| `MACOS_SIGN_P12` | secret (`release` env) | base64 Developer ID Application `.p12` |
| `MACOS_SIGN_PASSWORD` | secret | `.p12` password |
| `MACOS_NOTARY_KEY` | secret | base64 App Store Connect `.p8` key |
| `MACOS_NOTARY_KEY_ID` | secret | key id |
| `MACOS_NOTARY_ISSUER_ID` | secret | issuer UUID |
| `HOMEBREW_TAP_TOKEN` | secret | fine-grained PAT, `contents:write` on the tap repo only |
| `AMPLITUDE_API_KEY` | secret (`release` env) | Amplitude project API key; baked into release binaries at build time (spec 024). Unset = the release ships without telemetry and the run logs a warning |
| `MCP_PRIVATE_KEY` | secret (DNS auth option only) | Ed25519 private key |
| `GH_WORKFLOW_TOKEN` | optional repo secret | Contents and Pull requests read/write on this repo; lets generated release PRs trigger CI |
| `PUBLISH_PUBLIC` | repo variable | `'true'` when public channels are approved |
| `PUBLISH_HOMEBREW` | repo variable | `'true'` to update the tap (tap visibility approved) |
| `MCP_SERVER_NAME` | repo variable | registry name, e.g. chosen per spec 016 |

Owners and expiry dates live in the team password manager, never in the repo.
If any value is pasted into a chat, issue or log, treat it as compromised,
rotate it, and report it through Parallels' internal security process.

Rotation: create the new credential, update the `release` environment secret,
run a pre-release rehearsal tag, then revoke the old one.

For **Create Release PR**, enable GitHub Actions to create pull requests under
repository Actions settings. The default `GITHUB_TOKEN` can create a PR, but
its pushes/PR events do not automatically run CI. Configure `GH_WORKFLOW_TOKEN`
(an appropriately scoped PAT or app token) for automatic PR checks, or have a
maintainer push a reviewed change to the generated branch. Branch protections
and tag rules must allow the chosen bot to push release branches/tags. Repository
settings and secrets are configured by the release owner; the files do not
change those settings.

See [GitHub's workflow trigger rules](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow)
and [GoReleaser's tag override](https://goreleaser.com/resources/cookbooks/set-a-custom-git-tag/).

## 9. Install (for the README, once released)

See the Install draft supplied with this work; Homebrew:
`brew tap Parallels/tap && brew install --cask Parallels/tap/pdfm-mcp`
(confirm the working form on a clean Mac), MCPB: double-click the `.mcpb`.

## Open items before the first release

Every `<...>` placeholder and unverified assumption:

| Item | Where | Owner |
| --- | --- | --- |
| Apple Developer team, Developer ID cert, notary API key, the five `MACOS_*` secrets, `release` environment with reviewers | GitHub | IT / Security |
| MIT `LICENSE` added as an interim choice; Legal to confirm the license and copyright holder line | `LICENSE`, `.goreleaser.yaml`, `packaging/mcpb/manifest.json.tmpl` | Legal |
| Tap repo `Parallels/homebrew-tap` creation, bot email `<bot-email>`, `HOMEBREW_TAP_TOKEN`, `PUBLISH_HOMEBREW` | `release.yml` `tap` job, `.goreleaser.yaml` | release owner |
| Brand-approved `packaging/mcpb/icon.png` (512x512). A neutral placeholder is committed so dev builds work; replace before a public release | packaging | brand/Legal |
| Registry: GitHub OIDC, name `io.github.Parallels/pdfm-mcp` (confirm owner casing with `mcp-publisher validate`), set `MCP_SERVER_NAME`, `PUBLISH_PUBLIC` approval; keep `mcp-publisher` >= 1.7.9 | repo vars, spec 016 | owner + Legal |
| `mcp-publisher` pin: v1.8.1 verified as the candidate (2026-10-09; linux amd64 sha256 in the official `registry_1.8.1_checksums.txt`, recorded in `release.yml`); a human must review and paste the values into the `registry` job env | `release.yml` `registry` job env | release owner |
| `AMPLITUDE_API_KEY` in the `release` environment (Amplitude project key) before the first release that should report usage; the run warns loudly when it is absent | `release.yml` `goreleaser` job | release owner |
| Verify action majors: `anchore/sbom-action/download-syft@v0`, `actions/attest-build-provenance@v3`, `actions/setup-node@v5` | workflows | implementer |
| `${__dirname}` command form, empty `user_config` behaviour, exec bit survival in Claude Desktop | spec 015 section 10, QA 015 | QA |
| Built-in OCR (spec 022, supersedes the spec-020 helper wiring): Apple Vision is compiled into `pdfm-mcp` via cgo; the Go build (CI + release) runs on `macos-latest`, the archive/cask/MCPB carry a single binary. Remaining: first real release run with the Developer ID P12 + App Store Connect key, then `codesign --verify --strict` + Gatekeeper check on the universal binary | `release.yml`, `ci.yml`, `.goreleaser.yaml`, `scripts/build-mcpb.sh` | release owner |
