# QA 016 — MCP Registry listing

**Scope:** [spec 016](../specs/016-mcp-registry.md). **Blocked until owner and
Legal approve publication.** Do not run steps 7 onward otherwise.

**Executed 2026-10-09** on the developer Mac (mcp-publisher **v1.8.1**
downloaded and checksum-verified against the official
`registry_1.8.1_checksums.txt`; Node 24).

## Steps

### 1. Render tests — ✅ PASSED

`make test-scripts` → `ALL SCRIPT TESTS PASSED`; the render-server-json suite
(15 cases) covers bad version, bad hash, leftover token, hostile name.

### 2. Render by hand — ✅ PASSED

```
make mcpb                                   # dist/pdfm-mcp-0.0.0-dev.mcpb
make render-server-json NAME=io.github.parallels/pdfm-mcp VERSION_NUM=0.0.1 \
     SHA_FILE=<hash of the bundle> OUT=/tmp/server.json
```

`jq .` valid; `version` = `0.0.1`; `fileSha256` equals the bundle's sha256; no
`@...@` tokens remain.

### 3. Schema validation — ✅ PASSED (after one fix)

The rendered file was validated two ways:

- **Live, official:** `mcp-publisher validate` (v1.8.1) round-trips the file
  to `https://registry.modelcontextprotocol.io`. It initially **failed** with
  `expected length <= 100` at `body.description` (the template's description
  was 113 chars). Shortened the template description to 97 chars
  (Deviation), re-rendered, and the live check returned
  `✅ server.json is valid`.
- **Offline (now in CI):** the `registry-server-json` job in `ci.yml` renders
  the template with a dummy hash and validates it with `ajv` + `ajv-formats`
  against the schema URL from `$schema` (cached per schema date). The exact
  block was simulated locally: valid file passes, and a 101-char description
  is rejected with `must NOT have more than 100 characters` (negative
  control). Schema constraints checked: `description` ≤100, `title` ≤100,
  `name` ≤200, `version` ≤255.

### 4. Human review — ⏸ release owner

An owner must read `server.json` as an external-facing document and sign it
off (spec 016 section 9).

### 5. Gate closed — ✅ (gate wired; live check manual)

The `registry` job's `if:` requires
`prerelease == 'false' && vars.PUBLISH_PUBLIC == 'true'`. Live confirmation
(a stable tag showing the job skipped) happens with the first tag run.

### 6. Pins — ✅ (candidate verified; paste + review by release owner)

The install step fails closed while the env values contain `<`. Verified
candidate recorded in `release.yml`: **v1.8.1** (latest as of 2026-10-09);
linux/amd64 sha256 `a06c9096dcb9727c13555b6be26c7effa707b01f06a4c561ba7a3635443cf2cc`
per the official checksums file. The workflow's asset name was **fixed**:
assets carry no version segment (`mcp-publisher_linux_amd64.tar.gz`),
verified against the v1.8.1 release page. A human reviews the checksum and
pastes both values.

### 7. Publish — ⏸ blocked on approval (owner + Legal)

### 8. Advertised-hash check — ⏸ (after 7)

### 9. Client install — ⏸ (after 7)

### 10. Isolation — ✅ (design; live check with first tag)

The job `needs: [goreleaser, mcpb]` and only *reads* the release (downloads
the `.sha256`), so a failure cannot invalidate the published release; it is
re-runnable as a failed job.

### 11. Withdrawal drill — ✅ (CLI verified; live drill after 7)

`mcp-publisher status --status <active|deprecated|deleted> [--message]
[--all-versions] <name> [version]` is the supported mechanism (verified
against v1.8.1; matches the registry API changelog's status endpoints).
docs/release.md section 6 now documents the exact commands.

## Result

Everything local is green: render script + tests, rendered file accepted by
the **live** registry validation, offline CI validation with a negative
control, verified publisher pin candidate with corrected asset name, and the
withdrawal mechanism identified and documented. Steps 4, 7–9 are gated on
publication approval; 5/10 live-confirm on the first tag.

## Production configuration correction (2026-10-12)

`MCP_SERVER_NAME` was unset and caused the renderer to fail. The repository
variable is now `io.github.Parallels/pdfm-mcp`, with an owner-derived workflow
fallback. The publisher placeholders are replaced with v1.8.1 and the verified
Linux amd64 checksum. Render tests cover the exact namespace and actionable
empty-name diagnostics, and confirm all logging stays on stderr.
Live OIDC publication with the corrected workflow remains pending.

Verification: `make check`, `make test-e2e`, `make test-scripts` (19
registry assertions), `actionlint`, and `git diff --check` pass. Rendering
with the actual published v0.1.1 MCPB checksum succeeds for the configured
Parallels namespace. No registry publication was attempted.
