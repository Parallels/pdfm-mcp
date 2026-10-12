# Spec 016: MCP Registry Listing

| | |
|---|---|
| Status | Implemented (local verification complete 2026-10-09; registry configuration fixed 2026-10-12; live publication verification pending; publication gate retained) |
| Audience | Implementing agents / engineers, release owner |
| Scope | `server.json` template, namespace + authentication choice, publishing job in the release workflow, verification, withdrawal procedure |
| Depends on | Spec 013 (release), spec 015 (the `.mcpb` asset and its sha256) |
| Out of scope | npm/PyPI/NuGet/OCI package types, remote (HTTP) server entries, third-party directories (Smithery, Glama, PulseMCP, ...) |

## 1. Goal

Make `pdfm-mcp` **discoverable** in the official MCP Registry so MCP clients and aggregators can find it and install it from its `.mcpb` bundle. The registry is a **metadata catalog only**: it points at the artifact on GitHub Releases and does not host it.

> **This is a public listing.** The registry is public, and the `.mcpb` URL it advertises must be downloadable by anyone. This only happens after Parallels explicitly approves the listing (and Legal has decided the license). Until then this spec stays unimplemented, and the job below must not run (`vars.PUBLISH_PUBLIC != 'true'`).

### Acceptance criteria

1. With the gate open, a stable tag publishes `server.json` for that version and `curl "https://registry.modelcontextprotocol.io/v0.1/servers?search=<name>"` returns it.
2. `server.json` validates against the schema URL it declares, its `version` equals the release version, and `fileSha256` equals the `.mcpb.sha256` produced in spec 015.
3. Pre-release tags and gate-closed repos never publish.
4. Authentication uses OIDC or a DNS key held as a secret; no long-lived personal token is stored in the repo.
5. A failed registry step never invalidates the already-published GitHub Release (the job is isolated and re-runnable).
6. The withdrawal procedure (section 8) is documented and tested against whatever the registry currently supports.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Package type | `registryType: "mcpb"`, `identifier` = the GitHub Release URL of the `.mcpb`, `fileSha256` = its hash, `transport.type: "stdio"` | MCPB is the registry's path for prebuilt binaries; our binary is Go, not npm/PyPI/NuGet/OCI |
| Publisher | The official `mcp-publisher` CLI, run in the workflow | GoReleaser's `mcp:` block documents only `oci`, `npm`, `pypi`, `nuget` registry types, so it cannot express an `mcpb` package. Verify again at implementation time; if GoReleaser adds `mcpb` support, prefer it |
| Name / namespace | Decide before implementing (below). Reverse-DNS `namespace/name`, exactly one slash | The registry binds a name to an authenticated identity |
| Authentication | Option A: **GitHub OIDC** (`mcp-publisher login github-oidc`, `id-token: write`): name must start `io.github.<owner>/`. Option B: **DNS** (`login dns --domain <domain> --private-key ...`): name `<reversed-domain>/pdfm-mcp`, requires a DNS TXT record and an Ed25519 key | OIDC needs no secret. DNS gives a corporate namespace (`com.parallels/...`) but needs IT to publish the record and securely hold the key |
| URL requirement | The `.mcpb` URL must contain the string `mcp` (satisfied by `pdfm-mcp-<v>.mcpb`) | Registry rule for MCPB packages |
| Hash | `fileSha256` mandatory. The registry does not check it; **clients do** before installing | Integrity |
| When it runs | Last job of `release.yml`, after the MCPB asset is uploaded; stable tags only; `vars.PUBLISH_PUBLIC == 'true'` | Gate + ordering |
| Preview risk | The registry is in preview ("breaking changes or data resets may occur") | Keep the job non-blocking and idempotent |

### Name decision (open, needs the owner)

| Option | Name | Auth | Notes |
|---|---|---|---|
| A | `io.github.Parallels/pdfm-mcp` | GitHub OIDC | Simplest; the owner segment must match the org that owns the repo (case and org-vs-user rules to verify) |
| B | `com.parallels/pdfm-mcp` | DNS | Best brand fit; needs Parallels IT to control the DNS record and the signing key |

Record the decision under Deviations.

## 3. Repository layout (delta)

```
pdfm-mcp/
├── packaging/registry/
│   └── server.json.tmpl        # NEW
├── scripts/
│   └── render-server-json.sh   # NEW
├── .github/workflows/release.yml   # + registry job
└── docs/
    ├── specs/016-mcp-registry.md
    └── qa/016-mcp-registry-QA.md
```

## 4. `packaging/registry/server.json.tmpl`

```json
{
  "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
  "name": "@NAME@",
  "title": "Parallels Desktop VMs",
  "description": "Control Parallels Desktop VMs from MCP: power, screenshot, exec, keyboard, sequences. macOS only.",
  "version": "@VERSION@",
  "repository": {
    "url": "https://github.com/Parallels/pdfm-mcp",
    "source": "github"
  },
  "packages": [
    {
      "registryType": "mcpb",
      "identifier": "https://github.com/Parallels/pdfm-mcp/releases/download/v@VERSION@/pdfm-mcp-@VERSION@.mcpb",
      "fileSha256": "@SHA256@",
      "transport": { "type": "stdio" },
      "environmentVariables": [
        {
          "name": "PDFM_MCP_VM_ID",
          "description": "Optional VM id or name to restrict the server to a single VM (empty = all VMs)",
          "isRequired": false,
          "isSecret": false,
          "format": "string"
        },
        {
          "name": "PDFM_MCP_LOG_LEVEL",
          "description": "debug, info, warn or error (default info)",
          "isRequired": false,
          "isSecret": false,
          "format": "string"
        }
      ]
    }
  ]
}
```

- The `$schema` URL and field names (`registryType`, `fileSha256`, `environmentVariables`, `isSecret`, `format`) come from the registry's published examples as of this spec; **validate against the live schema** when implementing and follow it if it has moved.
- `@NAME@` is the chosen name; `@VERSION@` and `@SHA256@` are substituted by `render-server-json.sh` from the release tag and `pdfm-mcp-<v>.mcpb.sha256`. Description length and other limits follow the schema.
- Do not add internal URLs, VM names, or anything not meant for the public.

## 5. `scripts/render-server-json.sh`

Usage: `render-server-json.sh <name> <version> <sha256-file> <out>`. `set -euo pipefail`; validate version (SemVer, no leading `v`) and that the hash is 64 hex chars; substitute with `sed`; `jq empty` the output; fail if any `@...@` token remains. Never read environment secrets.

## 6. Workflow job (extension point from spec 013)

```yaml
  registry:
    needs: [goreleaser, mcpb]
    if: needs.goreleaser.outputs.prerelease == 'false' && vars.PUBLISH_PUBLIC == 'true'
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write            # OIDC option only
    steps:
      - uses: actions/checkout@v7
      - name: Get the bundle hash
        env: { GH_TOKEN: "${{ secrets.GITHUB_TOKEN }}" }
        run: |
          v="${{ needs.goreleaser.outputs.version }}"
          gh release download "v$v" -p "pdfm-mcp-$v.mcpb.sha256"
      - name: Render server.json
        run: ./scripts/render-server-json.sh "${{ vars.MCP_SERVER_NAME }}" "${{ needs.goreleaser.outputs.version }}" "pdfm-mcp-${{ needs.goreleaser.outputs.version }}.mcpb.sha256" server.json
      - name: Install mcp-publisher (pinned release + checksum)
        run: |
          # Download a PINNED mcp-publisher release for linux/amd64 and verify its published checksum
          # before executing it. Do not use "latest".
      - name: Authenticate
        run: ./mcp-publisher login github-oidc      # or: login dns --domain ... --private-key "$MCP_PRIVATE_KEY"
        # DNS option: env MCP_PRIVATE_KEY: ${{ secrets.MCP_PRIVATE_KEY }}
      - name: Publish
        run: ./mcp-publisher publish
```

Rules:

- `mcp-publisher` must be a **pinned version with its checksum verified**; the registry docs' `releases/latest` one-liner is acceptable for a tutorial, not for a release pipeline.
- `vars.MCP_SERVER_NAME` holds the chosen name; `MCP_PRIVATE_KEY` (DNS option only) is an Ed25519 private key stored as an environment secret and never printed.
- The job is **independent**: a failure leaves the GitHub Release and the MCPB asset untouched and can be re-run (`gh workflow run` / re-run failed jobs). Publishing the same version twice must be treated as an error to investigate, never forced.

## 7. Verification

```sh
curl -s "https://registry.modelcontextprotocol.io/v0.1/servers?search=<name>" | jq '.servers[] | {name, version}'
# download the advertised identifier and compare
curl -L -o /tmp/p.mcpb "<identifier url>"
shasum -a 256 /tmp/p.mcpb        # must equal fileSha256
```

Then install from the registry entry in a client that supports it (where available) and confirm the tools list.

## 8. Withdrawal and mistakes

A listing is public and may be mirrored. Before publishing, treat it as irreversible. Document in `docs/release.md`:

- **Wrong metadata or a bad build**: publish a corrected higher patch version; do not rely on editing a published version.
- **Secret or confidential data exposed**: rotate the secret first (spec 013 section 7), then ask the registry maintainers to remove or deprecate the version (the process and any `mcp-publisher` status/unpublish commands must be checked at implementation time; they are not assumed here), and report it as a security incident per Parallels' process.
- **Asset removed or repo made private**: the entry becomes a dead link; remove or deprecate it rather than leaving it.

## 9. Security and privacy

- Listing = publishing source location, tool names and descriptions. Review `server.json` as an external-facing document before the first publish (a human, not the agent, signs it off).
- Auth material (OIDC token, DNS key) lives only in the workflow environment; never in the repo, logs or chat. If a DNS private key is ever exposed, rotate the DNS record and key immediately.
- Use only the metadata you intend the world to see; no internal paths, no customer names.
- The registry does not scan or endorse the artifact; users rely on your signing, notarization and the `fileSha256`.

## 10. Testing

1. `render-server-json.sh` table tests: valid input, bad version, bad hash, leftover token, hostile name containing quotes (must be rejected or safely escaped).
2. Schema validation of the rendered file in CI (JSON Schema validator against the URL in `$schema`, cached) on PRs touching `packaging/registry/**`.
3. Dry run: `mcp-publisher publish` against a throwaway namespace if the registry offers a staging instance; otherwise a one-off pre-approved publish of a `0.0.x` version and a recorded withdrawal drill.
4. Gate test: with `PUBLISH_PUBLIC` unset, the job is skipped (inspect the workflow run).
5. `docs/qa/016-mcp-registry-QA.md`: section 7 steps plus a client install from the entry.

## 11. Implementation order

1. **Obtain the publication approval** (owner + Legal) and decide the name/auth (section 2). Stop here if not granted.
2. If DNS: IT publishes the TXT record and provisions the key into the `release` environment.
3. Template + script + tests.
4. Add the job behind the gate; rehearse with a pre-approved version.
5. Verification and the withdrawal drill; update `docs/release.md`.

## 12. Follow-ups

- Third-party directories (Smithery, PulseMCP, Glama, mcp.so): optional, each has its own submission rules; same publication gate applies.
- If a remote (hosted HTTP) variant ever exists, add a `remotes` entry rather than a package.
- Replace the custom job with GoReleaser's `mcp:` block when it supports `mcpb`.

## 13. Known unknowns / verification notes

- Exact current `server.json` schema version URL and required fields (the examples reference `2025-12-11`).
- GitHub OIDC namespace rules for **organization-owned** repos (`io.github.<org>/` vs the user) and case-sensitivity.
- Registry policy on re-publishing a version, deprecating, or deleting entries (preview status: data resets possible).
- Whether `environmentVariables` is honoured for `mcpb` packages or only for npm/pypi/oci.
- Rate limits and whether publishing can be done from a CI runner IP.

### Deviations (fill in during implementation)

- `MCP_PUBLISHER_VERSION` / `MCP_PUBLISHER_SHA256` remain workflow env placeholders; the install step fails until a human pins them. **Verified candidate recorded in `release.yml`** (2026-10-09): v1.8.1, linux/amd64 sha256 per the official `registry_1.8.1_checksums.txt`. **Asset name fixed:** the real assets carry no version segment (`mcp-publisher_linux_amd64.tar.gz`), verified against the v1.8.1 release — the workflow's old `mcp-publisher_${VERSION}_…` name was wrong.
- Name/auth decision still open; `render-server-json.sh` accepts `[A-Za-z0-9.-]+/[A-Za-z0-9._-]+` and rejects anything else.
- **Live schema check (2026-10-09):** `mcp-publisher validate` (v1.8.1) against the real registry enforces `description` ≤ 100 characters (and `title` ≤ 100, `name` ≤ 200, `version` ≤ 255 per the 2025-12-11 schema). The template's 113-char description failed; shortened to 97 chars (`Control Parallels Desktop VMs from MCP: power, screenshot, exec, keyboard, sequences. macOS only.`) and the live check passes.
- **CI schema validation added** (section 10.2): new `registry-server-json` job in `ci.yml` validates a rendered `server.json` with `ajv` + `ajv-formats` against the live schema URL on PRs touching `packaging/registry/**` or `scripts/render-server-json.sh` (schema cached per version date; negative control verified locally).
- **Withdrawal mechanism identified** (section 8 / known unknown): `mcp-publisher status --status <active|deprecated|deleted> [--message] [--all-versions] <name> [version]` (v1.8.1; matches the registry's status endpoints). docs/release.md section 6 now documents the exact commands.
- **Verified locally (2026-10-09):** render script table tests pass; hand render produces a file whose `version`/`fileSha256` match; the rendered file passes the **live** official validation; the mcp-publisher v1.8.1 tarball verifies against its published checksum and the CLI runs.

### Production registry configuration fix (2026-10-12)

- The production renderer received an empty name: repository variable
  `MCP_SERVER_NAME` was absent. Set it to `io.github.Parallels/pdfm-mcp`,
  matching the GitHub repository owner and the workflow's OIDC authentication.
- The workflow also defaults the name to `io.github.<repository_owner>/pdfm-mcp`
  if the variable is unset. Explicit configured names still undergo validation.
- Pin mcp-publisher to the already documented v1.8.1 and its Linux amd64
  SHA256 `a06c9096dcb9727c13555b6be26c7effa707b01f06a4c561ba7a3635443cf2cc`.
  Rechecked the official checksum file and archive; no new dependencies.
  This supersedes the provisional publisher placeholders above.
- Empty-name diagnostics now identify the missing setting. Render success
  logs go to stderr. Regression coverage includes the Parallels namespace,
  empty-name rejection without output, and stdout remaining empty.
- Public publication stays gated by `PUBLISH_PUBLIC`; no registry entry was
  published while fixing configuration. Old workflow reruns keep their original
  publisher placeholders; merge the workflow correction before a new run.

Verification: `make check`, `make test-e2e`, `make test-scripts` (19
registry assertions), `actionlint`, and `git diff --check` pass. Rendering
with the actual published v0.1.1 MCPB checksum succeeds for the configured
Parallels namespace. No registry publication was attempted.
