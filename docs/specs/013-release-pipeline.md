# Spec 013: Release Pipeline (GoReleaser, signing, notarization, GitHub Releases)

| | |
|---|---|
| Status | Implemented (local verification complete 2026-10-09; tag rehearsal pending release owner) |
| Audience | Implementing agents / engineers, release owner |
| Scope | `.goreleaser.yaml`, tag-triggered `release.yml`, macOS code signing + notarization, checksums/SBOM, Makefile targets, runbook, secrets inventory, publication gate. This is the **foundation** of specs 014 (Homebrew), 015 (MCPB), 016 (MCP Registry) |
| Out of scope | The tap repo and cask (014), the MCPB bundle (015), registry listing (016), Linux/Windows artifacts (the product is macOS-only: Parallels Desktop for Mac) |

**Versioning update (2026-10-10):** [spec 023](023-semver-release-flow.md)
extends this foundation with VERSION/changelog release PRs and manual/reusable
canary/beta/production releases. VERSION is the base source of truth and the
validated tag supplies the distributed binary's version. Main is the release
branch; master is no longer accepted. Signing secrets are required before any
channel is published; snapshots remain unsigned. The historical examples below
describe the original tag-only flow; the current runbook and workflows include
specs 022/023 (macOS CGO, exact SHA checkouts, reviewed production notes).

## 1. Goal

One `git tag vX.Y.Z && git push --tags` produces a **signed, notarized, checksummed macOS binary** attached to a GitHub Release, from CI, with no manual steps and no secrets on developer machines. Every other distribution channel consumes that release asset; nothing is rebuilt per channel.

### Acceptance criteria

1. `make release-check` (`goreleaser check`) and `make release-snapshot` (`goreleaser release --snapshot --clean`, no publishing, no signing) pass locally and in CI on every PR.
2. Pushing tag `v0.1.0` runs `release.yml` and creates a GitHub Release containing: the universal macOS archive, `checksums.txt`, an SBOM, and a changelog.
3. The released binary is **Developer ID signed and notarized**: `codesign --verify --strict --verbose=2 pdfm-mcp` passes and a copy downloaded through a browser (quarantine flag set) runs without a Gatekeeper dialog.
4. `pdfm-mcp --version` on the released binary prints the tag version, commit and build date (the existing `internal/version` ldflags).
5. A tag with a pre-release suffix (`v0.2.0-rc.1`) creates a GitHub *pre-release* and never triggers downstream channels (014 to 016).
6. No secret appears in the repo, in logs, or in release assets. All signing material lives in GitHub Actions secrets/environment protection.
7. `docs/release.md` lets a second engineer cut a release unaided.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Tool | GoReleaser v2 (`version: 2`), run via `goreleaser/goreleaser-action` pinned to a major (`~> v2`) | Standard for Go; single config drives archives, checksums, SBOM, notarization and later the Homebrew cask |
| Targets | `darwin/arm64` + `darwin/amd64` merged into a **universal binary** (`universal_binaries`) | The product only makes sense on a Mac with Parallels Desktop. One binary serves Apple Silicon and Intel, so the MCPB (015) and cask (014) ship a single artifact |
| CGO | `CGO_ENABLED=0`, `-trimpath` | Static, reproducible, cross-compiled from Linux CI (matches the Makefile) |
| Signing + notarization | GoReleaser's cross-platform `notarize.macos` (anchore/quill), runs on `ubuntu-latest` | Works for bare binaries/universal binaries, no macOS runner or Keychain needed. Do **not** use it for `.app` bundles (not applicable) |
| Apple identity | A **Parallels-owned** Apple Developer team, Developer ID Application certificate, App Store Connect API key (`.p8`) | Corporate product must not ship under a personal identity. Request through the team that owns Parallels' Apple Developer account (IT/Security) |
| Stapling | Not possible for bare binaries; Gatekeeper validates the notarization ticket online on first run | Known macOS behaviour for CLI binaries. A zip/DMG/pkg could be stapled, not needed here |
| Release host | GitHub Releases of the origin repo (`Parallels/pdfm-mcp`) | Already the source of truth; consumed by Homebrew cask (014) and MCPB (015) |
| Versioning | SemVer tags `vMAJOR.MINOR.PATCH[-pre]`. Version comes **only** from the tag | Single source of truth; `internal/version` is set by ldflags |
| Checksums + SBOM | `checksums.txt` (sha256) and an SPDX/CycloneDX SBOM via GoReleaser `sboms` (syft) | Needed by MCPB `fileSha256` (015/016) and supply-chain hygiene |
| Provenance | GitHub artifact attestations (`attestations`) on archives if the org plan supports them; otherwise omitted | Optional hardening; check availability, do not block the release on it |
| Changelog | Generated from Conventional Commits (`feat`, `fix`, `perf`; excluding `docs`, `test`, `chore`, `ci`) | No hand-written release notes |
| Module path | Binary is built from module `github.com/cjlapao/pdfm-mcp`, but the remote is `Parallels/pdfm-mcp` | **Open decision** (section 9): rename the module to the org path before the first release so `go install` and ldflags match the repo. ldflags in this spec use the `MODULE` Makefile variable's value, so only that value changes |

## 3. Publication gate (read before anything ships)

The repo is a Parallels corporate repository, and per Parallels policy source code and roadmaps are confidential. **Nothing in 013 to 016 may be published outside Parallels without an explicit approval** from the owner of the project (and Legal for the license). Concretely:

- Default posture: the repo and its Releases stay **private/internal**. Everything in this spec works in that mode.
- Choosing a license is a Legal decision. Do not invent one: the repo currently has no `LICENSE`. A proprietary "all rights reserved" notice is a valid placeholder until Legal decides.
- Public channels (public GitHub Releases, public Homebrew tap, MCP Registry) are gated by a repository variable `PUBLISH_PUBLIC` (`'true'` to enable). Without it, specs 014, 015, 016 jobs do not run or are limited to their internal form.
- Security findings, internal hostnames, VM names and customer data must never appear in release notes, the README packaged in archives, or the SBOM.

## 4. Repository layout (delta)

```
pdfm-mcp/
├── .goreleaser.yaml              # NEW
├── .github/workflows/
│   ├── ci.yml                    # + goreleaser check job
│   └── release.yml               # NEW (tag-triggered)
├── LICENSE                       # NEW (after Legal decides; see section 3)
├── docs/
│   ├── release.md                # NEW runbook
│   └── specs/013-release-pipeline.md
├── scripts/
│   └── version-check.sh          # NEW: asserts tag == expected semver, no dirty tree
└── Makefile                      # + release-check, release-snapshot
```

## 5. `.goreleaser.yaml`

```yaml
# yaml-language-server: $schema=https://goreleaser.com/static/schema.json
version: 2
project_name: pdfm-mcp

before:
  hooks:
    - go mod tidy
    - go mod verify

builds:
  - id: pdfm-mcp
    main: ./cmd/pdfm-mcp
    binary: pdfm-mcp
    env: [CGO_ENABLED=0]
    goos: [darwin]
    goarch: [arm64, amd64]
    flags: [-trimpath]
    mod_timestamp: "{{ .CommitTimestamp }}"
    ldflags:
      - -s -w
      - -X github.com/cjlapao/pdfm-mcp/internal/version.Version={{ .Version }}
      - -X github.com/cjlapao/pdfm-mcp/internal/version.Commit={{ .ShortCommit }}
      - -X github.com/cjlapao/pdfm-mcp/internal/version.Date={{ .Date }}

universal_binaries:
  - ids: [pdfm-mcp]
    replace: true          # only the merged binary is archived and notarized
    name_template: pdfm-mcp

notarize:
  macos:
    - enabled: '{{ isEnvSet "MACOS_SIGN_P12" }}'
      ids: [pdfm-mcp]
      sign:
        certificate: "{{ .Env.MACOS_SIGN_P12 }}"
        password: "{{ .Env.MACOS_SIGN_PASSWORD }}"
      notarize:
        issuer_id: "{{ .Env.MACOS_NOTARY_ISSUER_ID }}"
        key_id: "{{ .Env.MACOS_NOTARY_KEY_ID }}"
        key: "{{ .Env.MACOS_NOTARY_KEY }}"
        wait: true
        timeout: 20m       # documented maximum

archives:
  - id: default
    formats: [tar.gz]
    name_template: "pdfm-mcp_{{ .Version }}_darwin_universal"
    files:
      - README.md
      - LICENSE            # remove this line until LICENSE exists (section 3)

checksum:
  name_template: checksums.txt
  algorithm: sha256

sboms:
  - artifacts: archive

changelog:
  sort: asc
  use: github
  filters:
    exclude: ["^docs:", "^test:", "^chore:", "^ci:", "^Merge "]

release:
  github:
    owner: Parallels          # confirm; GoReleaser can infer from the remote
    name: pdfm-mcp
  prerelease: auto                 # v1.2.3-rc.1 => GitHub pre-release
  draft: false
  name_template: "pdfm-mcp {{ .Version }}"

snapshot:
  version_template: "{{ incpatch .Version }}-snapshot"
```

Notes for the implementer:

- The `notarize.macos.enabled` template means a **snapshot or fork build without secrets still succeeds** (unsigned), so `make release-snapshot` and PR checks never need Apple credentials.
- `replace: true` on `universal_binaries` must be verified against the installed GoReleaser version (it controls whether the per-arch binaries are also archived).
- `before.hooks` must not reach the network for anything but module verification.
- If the config is rejected by `goreleaser check`, fix it to the installed schema and record the change under Deviations.

## 6. `.github/workflows/release.yml`

```yaml
name: release
on:
  push:
    tags: ["v*"]

permissions:
  contents: read

concurrency:
  group: release-${{ github.ref }}
  cancel-in-progress: false

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
      - run: ./scripts/version-check.sh "${GITHUB_REF_NAME}"
      - run: make fmt-check vet test
      - run: make test-e2e

  goreleaser:
    needs: verify
    runs-on: ubuntu-latest
    environment: release            # protected environment: required reviewers + holds the secrets
    permissions:
      contents: write               # create the release
      id-token: write               # attestations / OIDC
      attestations: write
    outputs:
      version: ${{ steps.meta.outputs.version }}
      prerelease: ${{ steps.meta.outputs.prerelease }}
    steps:
      - uses: actions/checkout@v7
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
      - uses: goreleaser/goreleaser-action@v7
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          MACOS_SIGN_P12: ${{ secrets.MACOS_SIGN_P12 }}
          MACOS_SIGN_PASSWORD: ${{ secrets.MACOS_SIGN_PASSWORD }}
          MACOS_NOTARY_KEY: ${{ secrets.MACOS_NOTARY_KEY }}
          MACOS_NOTARY_KEY_ID: ${{ secrets.MACOS_NOTARY_KEY_ID }}
          MACOS_NOTARY_ISSUER_ID: ${{ secrets.MACOS_NOTARY_ISSUER_ID }}
          HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}   # used by spec 014 only
      - id: meta
        run: |
          v="${GITHUB_REF_NAME#v}"
          echo "version=$v" >> "$GITHUB_OUTPUT"
          case "$v" in *-*) echo "prerelease=true" >> "$GITHUB_OUTPUT";; *) echo "prerelease=false" >> "$GITHUB_OUTPUT";; esac

  # Extension points (added by later specs, each `needs: goreleaser` and
  # guarded by `if: needs.goreleaser.outputs.prerelease == 'false'`):
  #   mcpb      (spec 015)  builds and uploads pdfm-mcp-<ver>.mcpb to the release
  #   registry  (spec 016)  publishes server.json, only when vars.PUBLISH_PUBLIC == 'true'
```

Rules:

- Third-party actions pinned to a **major tag or commit SHA** at implementation time (use the latest majors; `ci.yml` already uses `@v7`/`@v9` conventions).
- `permissions` at workflow level is read-only; each job opts into what it needs.
- The `release` environment requires **manual approval** by the release owner(s) before signing material is exposed.
- Secrets are never echoed. No `set -x` in any step that sees them. Fork PRs never get them (the workflow only runs on tags).

## 7. Secrets and variables inventory

| Name | Kind | Holds | Notes |
|---|---|---|---|
| `MACOS_SIGN_P12` | secret (`release` env) | base64 of the Developer ID Application `.p12` | `base64 -w0 < cert.p12` (Linux) or `base64 -i cert.p12` (macOS) |
| `MACOS_SIGN_PASSWORD` | secret | password for the `.p12` | |
| `MACOS_NOTARY_KEY` | secret | base64 of the App Store Connect `.p8` API key | |
| `MACOS_NOTARY_KEY_ID` | secret | key id | |
| `MACOS_NOTARY_ISSUER_ID` | secret | issuer UUID | |
| `HOMEBREW_TAP_TOKEN` | secret | fine-grained PAT, `contents:write` on the **tap repo only** (spec 014) | |
| `PUBLISH_PUBLIC` | repo variable | `'true'` when public channels are approved | default unset = internal only |

Rotation: certificates and API keys have owners and expiry dates recorded in the team's password manager (never in the repo). If any value is ever pasted into a chat, issue, or log, treat it as compromised and **rotate it immediately** (and tell the security contacts per Parallels' incident process).

## 8. Makefile and CI additions

```makefile
.PHONY: release-check
release-check: ## Validate .goreleaser.yaml
	goreleaser check

.PHONY: release-snapshot
release-snapshot: ## Local dry run: build, archive, checksum (no publish, no signing)
	goreleaser release --snapshot --clean --skip=publish,notarize,announce
```

`ci.yml` gains a step `goreleaser check` (via `goreleaser/goreleaser-action` with `args: check`) and a snapshot build on PRs touching `.goreleaser.yaml`, `cmd/**`, `internal/version/**`. The existing `make build-all` stays (multi-OS compile check) but is **not** a release artifact source.

### `scripts/version-check.sh`

Fails when the tag is not `^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`, when the working tree is dirty, or when the tag does not point at a commit reachable from `main`/`master`.

## 9. Runbook (`docs/release.md`) must cover

1. Pre-flight: green `main`, `make check`, `make test-e2e`, QA docs for changed specs done on a real Mac.
2. Cut: `git tag -a vX.Y.Z -m "..." && git push origin vX.Y.Z`.
3. Approve the `release` environment gate; watch the workflow.
4. Verify (below).
5. Roll back: delete the GitHub Release and tag, fix forward with the next patch. **Never re-use or force-move a published tag** (Homebrew checksums and MCPB hashes pin to it).
6. Secret rotation procedure and who owns each secret.

### Verification of a release

```sh
gh release download vX.Y.Z -R Parallels/pdfm-mcp -p '*darwin_universal.tar.gz' -p checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
tar xzf pdfm-mcp_*_darwin_universal.tar.gz
codesign --verify --strict --verbose=2 ./pdfm-mcp
codesign -dv --verbose=4 ./pdfm-mcp 2>&1 | grep -E 'Authority|TeamIdentifier|flags'   # expect Developer ID + hardened runtime
lipo -archs ./pdfm-mcp                                                                   # arm64 x86_64
./pdfm-mcp --version
```

Also run the downloaded copy with the quarantine flag set (download via browser or `xattr -w com.apple.quarantine "0081;00000000;Safari;" ./pdfm-mcp`) and confirm no Gatekeeper block (`spctl --assess` is not meaningful for bare CLI binaries: use an actual launch).

## 10. Testing

- **Config**: `goreleaser check` in CI; snapshot build asserts `dist/` contains the archive, `checksums.txt`, SBOM, and that the archived binary reports `--version` as `…-snapshot`.
- **Script**: `scripts/version-check.sh` unit-tested with `bats` or a small Go/shell table (valid tags, bad tags, dirty tree).
- **Release rehearsal**: cut `v0.0.1-rc.1` on a throwaway tag in a fork or the real repo marked pre-release; run the full verification list; delete the release afterwards.
- **Negative**: run the release workflow with `MACOS_SIGN_P12` unset and confirm it produces an unsigned snapshot-style asset and the verification step clearly fails (so an unsigned release cannot go unnoticed). Add a post-release job step that runs `codesign --verify` on the uploaded asset in a `macos-latest` runner and fails the workflow if signing is absent on a non-prerelease tag.

## 11. Implementation order

1. This spec + index row.
2. Decide module path and Apple identity owner; obtain Legal's licence decision (section 3).
3. `.goreleaser.yaml`; `goreleaser check`; `make release-snapshot` locally.
4. `scripts/version-check.sh`, Makefile targets, CI check job.
5. Create the `release` environment and secrets; `release.yml`.
6. Rehearse with a pre-release tag; run section 9 verification.
7. `docs/release.md`; README "Install" section pointing at the release.
8. First real tag.

## 12. Follow-ups

- Linux/Windows artifacts (only if Parallels ever needs them; the tool is meaningless without Parallels Desktop).
- `.pkg` installer (native signing/notarization requires GoReleaser Pro and a macOS runner).
- Reproducible-build verification job; SLSA provenance.
- Auto-update notification inside the binary (not planned; privacy and network surface).

## 13. Known unknowns / verification notes

- `universal_binaries.replace` semantics and `notarize.macos.ids` interplay with a universal binary: confirm against the installed GoReleaser; the docs say quill supports "binaries/universal binaries only", so this layout should be valid.
- Whether GitHub artifact attestations and **immutable releases** are available/required for the org (GoReleaser has a note on immutable releases): if immutable releases are on, a published tag/asset can never be replaced, which makes the rehearsal step important.
- Notarization turnaround (`wait: true`, max 20m) vs. the CI job timeout; if Apple is slow, switch to `wait: false` plus a follow-up verification job.
- Private repo: downstream consumers (cask, MCPB download) need authenticated asset access; see specs 014 and 015.
- Whether Parallels already has a Developer ID team and signing infrastructure to reuse instead of provisioning new keys.

### Deviations (fill in during implementation)

- Added `go`-free `make test-scripts` and a `scripts` CI job; `ci.yml` detects relevant PR changes with `git diff` instead of path filters (the existing `check` job must run on every PR).
- `sboms` needs `syft`: `release.yml` and CI install it with `anchore/sbom-action/download-syft`; `make release-snapshot` skips the SBOM when `syft` is absent.
- `LICENSE` archive entry is the glob `LICEN[CS]E*` so builds work before Legal adds it — **verified** 2026-10-09: GoReleaser 2.12.5 tolerates the empty glob (warning only).
- `release.yml` runs `make test-scripts` in `verify`. Provenance uses `actions/attest-build-provenance` with `continue-on-error`.
- The post-release signature gate is the `mcpb` job's `codesign --verify` on a macOS runner (stable tags only); there is no separate job.
- **Verified 2026-10-09** (GoReleaser 2.12.5 + syft, repo branch `master`): `make release-check` passes; `make release-snapshot` produces a universal (arm64+x86_64), checksummed, SBOM'd archive whose `--version` prints version/commit/date; `notarize` skips cleanly without secrets; the `homebrew_casks` block generates the cask.
- **Fixed:** `homebrew_casks.binaries` → `binary` (the v2.12.5 key for binary casks, per `goreleaser schema`); the old key failed `goreleaser check`.
- **Fixed:** `scripts/test/test_build_mcpb.sh` used random bytes as its binary; on macOS `build-mcpb.sh`'s `lipo -archs` check refuses non-Mach-O input (the suite only passed in the `lipo`-less authoring sandbox). The fixture is now `/usr/bin/true`.
- `scripts/version-check.sh` accepts `main` **or** `master` (this repo's default branch is `master`); covered by the 24-case shell suite.
