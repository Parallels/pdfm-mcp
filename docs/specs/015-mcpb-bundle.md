# Spec 015: MCPB Bundle (one-click install for Claude Desktop)

| | |
|---|---|
| Status | Implemented (local verification complete 2026-10-09; Claude Desktop install + release bundle pending) |
| Audience | Implementing agents / engineers, release owner |
| Scope | `manifest.json` (MCPB spec 0.3, `server.type: binary`), bundle assembly script, `make mcpb`, release workflow job, config-guard for unresolved placeholders, tests, QA doc |
| Depends on | Spec 013 (the signed, notarized universal binary on a GitHub Release) |
| Out of scope | Windows/Linux bundles, Node/Python server types, the registry listing (016), publishing to any extension directory |

## 1. Goal

Ship `pdfm-mcp-<version>.mcpb`, a single file a user can **double-click or drag into Claude Desktop** to install the server with a small settings dialog (optional target VM, log level), no JSON editing, no Homebrew, no PATH issues. The bundle contains the **same notarized binary** produced by spec 013. It is never rebuilt.

MCPB (formerly DXT) is a zip with a `manifest.json` that tells the host how to launch the server. Binary servers are first-class: the manifest declares `"type": "binary"` and an executable in the bundle.

### Acceptance criteria

1. `make mcpb` builds `dist/pdfm-mcp-<version>.mcpb` from a given binary and passes `mcpb validate` (the packer's manifest validation).
2. The release workflow attaches the `.mcpb` and its `.sha256` to the GitHub Release for stable tags only.
3. Installing the bundle in Claude Desktop on macOS (Apple Silicon and Intel) works with no extra tools, shows the two settings (`vm_id`, `log_level`), and the server's tools appear.
4. The binary inside the bundle is byte-identical to the one in the release archive, still carries a valid signature, and is executable after install.
5. Leaving the optional `vm_id` setting empty starts the server in **fleet mode**; setting it starts VM mode (verified by the tool list).
6. An unresolved `${user_config.*}` placeholder can never be interpreted as a VM id (config guard, section 6).
7. The tools listed in the manifest match what the server actually exposes (drift test).
8. `docs/qa/015-mcpb-bundle-QA.md` passes.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Manifest version | `0.3` (the current spec). Do not use `0.4` features (that version is only needed for the `uv` runtime) | Maximum client compatibility |
| Server type | `binary`, `entry_point: server/pdfm-mcp` | Self-contained Go executable, no runtime required |
| Platforms | `compatibility.platforms: ["darwin"]` | Parallels Desktop for Mac only. The host should refuse installation elsewhere |
| Architecture | Universal binary inside | One bundle covers arm64 and x86_64 |
| What is bundled | Binary, `manifest.json`, `icon.png`, `README.md`, `LICENSE` (once Legal decides, spec 013 section 3) | Nothing else: no config files, no secrets |
| Settings (`user_config`) | `vm_id` (string, optional) and `log_level` (string, default `info`) | Mirrors the real configuration surface; mapped to **environment variables** so empty values mean "unset" |
| Why env, not args | `PDFM_MCP_VM_ID`, `PDFM_MCP_LOG_LEVEL` already exist in `internal/config` | Avoids passing an empty `--vm-id ""` argument and keeps one parsing path |
| Sensitive settings | None. There are no secrets in this server | Nothing to mark `sensitive` |
| Packer | The official `@anthropic-ai/mcpb` CLI, **version-pinned**, run with `npx --yes @anthropic-ai/mcpb@<pinned> pack`. Node is needed only at build time | Official validation and packaging; end users need no Node |
| Bundle signing | Optional: sign the bundle with `mcpb sign` using a Parallels-owned code-signing cert if the CLI/host supports it for the intended audience | Adds a second integrity layer; verify support before committing (section 10) |
| Source of the binary in CI | Downloaded from the just-created GitHub Release, checksum-verified, `codesign --verify`-ed on a macOS runner | Guarantees the bundle contains exactly what was notarized |
| Distribution | Attached to the GitHub Release. Internal visibility follows spec 013 section 3 | Private repos need authenticated download for users; see section 9 |

## 3. Repository layout (delta)

```
pdfm-mcp/
├── packaging/mcpb/
│   ├── manifest.json.tmpl      # NEW: version placeholder, otherwise final
│   ├── icon.png                # NEW: 512x512 (brand-approved, see section 9)
│   └── .mcpbignore             # NEW
├── scripts/
│   └── build-mcpb.sh           # NEW
├── internal/config/config.go   # + unresolved-placeholder guard
├── test/e2e/mcpb_test.go       # NEW (tag e2e): manifest/tool drift + packed-binary handshake
├── .github/workflows/release.yml   # + mcpb job (extension point from spec 013)
├── Makefile                    # + mcpb, mcpb-validate
└── docs/
    ├── specs/015-mcpb-bundle.md
    └── qa/015-mcpb-bundle-QA.md
```

## 4. `packaging/mcpb/manifest.json.tmpl`

```json
{
  "manifest_version": "0.3",
  "name": "pdfm-mcp",
  "display_name": "Parallels Desktop VMs",
  "version": "@VERSION@",
  "description": "Inspect and control Parallels Desktop virtual machines from Claude: list, power, screenshot, run commands, type keys, and run multi-step sequences.",
  "long_description": "pdfm-mcp lets Claude work with Parallels Desktop VMs on this Mac. It can list VMs, start/stop/suspend them, take screenshots of a VM screen, run commands inside the guest, send keyboard input, and run multi-step automation sequences. Requires Parallels Desktop on macOS.\n\nPrivacy: screenshots and command output are returned to Claude and therefore reach your AI provider. Do not use it on VMs showing credentials or confidential data unless that is acceptable for your organization. Everything runs locally; the server itself makes no network calls.",
  "author": { "name": "Parallels", "url": "<homepage url>" },
  "repository": { "type": "git", "url": "https://github.com/Parallels/pdfm-mcp.git" },
  "homepage": "<homepage url>",
  "support": "<issues or support url>",
  "icon": "icon.png",
  "license": "<SPDX id or 'Proprietary' as decided by Legal>",
  "keywords": ["parallels", "virtual machines", "vm", "automation", "macos"],
  "server": {
    "type": "binary",
    "entry_point": "server/pdfm-mcp",
    "mcp_config": {
      "command": "${__dirname}/server/pdfm-mcp",
      "args": [],
      "env": {
        "PDFM_MCP_VM_ID": "${user_config.vm_id}",
        "PDFM_MCP_LOG_LEVEL": "${user_config.log_level}"
      }
    }
  },
  "tools": [
    { "name": "parallels_list_vms", "description": "List Parallels VMs with state and hardware" },
    { "name": "parallels_vm_info", "description": "Full details of the target VM (single-VM mode)" },
    { "name": "parallels_vm_start", "description": "Start or resume a VM" },
    { "name": "parallels_vm_stop", "description": "Stop a VM (graceful or forced)" },
    { "name": "parallels_vm_capture", "description": "Take a screenshot of a VM" },
    { "name": "parallels_vm_exec", "description": "Run a command inside the guest" },
    { "name": "parallels_vm_sequence", "description": "Run a multi-step automation sequence with waits and conditions" }
  ],
  "tools_generated": true,
  "user_config": {
    "vm_id": {
      "type": "string",
      "title": "Target VM (optional)",
      "description": "VM id or name. Leave empty to work with all VMs; set it to restrict this server to a single VM.",
      "required": false
    },
    "log_level": {
      "type": "string",
      "title": "Log level",
      "description": "debug, info, warn or error (default info).",
      "default": "info",
      "required": false
    }
  },
  "compatibility": {
    "platforms": ["darwin"]
  }
}
```

Notes:

- `@VERSION@` is substituted by `build-mcpb.sh` (no other templating). The rest is the final manifest.
- `tools` is a **subset** summary for the install UI; `tools_generated: true` tells the host the server exposes more at runtime (power operations, keyboard tools, VM-mode extras).
- Author/URL/support/license values are placeholders until Parallels fills them in; **do not invent** them.
- `mcp_config.command` uses `${__dirname}` so the path survives wherever the host unpacks the bundle. The MCPB docs show a bare relative path for binaries in one example and `${__dirname}` for node/python: confirm which the installed Claude Desktop resolves (section 10) and keep the working form.
- No tool descriptions or screenshots may include real VM names, hostnames or customer data.

## 5. `scripts/build-mcpb.sh`

Usage: `build-mcpb.sh <version> <path-to-binary> <out-dir>`.

1. `set -euo pipefail`; validate `<version>` against SemVer; refuse if the binary does not exist or is not a Mach-O universal (`lipo -archs` must list both `arm64` and `x86_64` when available; skip the check with a warning on non-macOS hosts).
2. Stage in a **fresh temp directory** (`mktemp -d`, removed by a `trap`): `manifest.json` (version substituted with `sed`, then `jq empty` to confirm valid JSON), `server/pdfm-mcp` (copied, `chmod 0755`), `icon.png`, `README.md`, `LICENSE` if present.
3. Run `npx --yes @anthropic-ai/mcpb@<PINNED> validate manifest.json` then `pack <stage> <out>/pdfm-mcp-<version>.mcpb`.
4. Write `<out>/pdfm-mcp-<version>.mcpb.sha256` (`shasum -a 256`), the exact value later used by spec 016's `fileSha256`.
5. Unzip the result into a second temp dir and assert: `server/pdfm-mcp` exists, is executable (mode bit), and is byte-identical to the input (`cmp`).
6. Print the path and hash. Never print environment variables.

`.mcpbignore` excludes everything except the staged files (defence against accidentally packaging build artifacts or `.env` files).

Makefile:

```makefile
MCPB_PIN ?= <pinned @anthropic-ai/mcpb version>

.PHONY: mcpb
mcpb: build ## Build dist/pdfm-mcp-<version>.mcpb from the local binary (unsigned: dev only)
	./scripts/build-mcpb.sh "$(VERSION)" "$(BIN_DIR)/$(BINARY)" "$(DIST_DIR)"
```

(`make mcpb` is a developer convenience; the **release** bundle is always built from the notarized release binary, section 7.)

## 6. Config guard for unresolved placeholders (code change)

If a host fails to substitute an unset optional `user_config` value, the env var may arrive as the literal text `${user_config.vm_id}`. That string is printable ASCII and would pass `ValidateVMRef`, so the server would run in VM mode against a VM called `${user_config.vm_id}`. Prevent it in `internal/config`:

```go
// unresolvedPlaceholder reports values that look like an unsubstituted
// template variable (for example "${user_config.vm_id}") left by a host that
// had no value for an optional setting. Treat them as unset.
func unresolvedPlaceholder(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}")
}
```

Apply to `PDFM_MCP_VM_ID` (treated as empty = fleet mode), `PDFM_MCP_LOG_LEVEL` (falls back to `info`) and `PDFM_MCP_VM_CACHE_TTL`; log one `warn` line naming the variable (never the value). Unit tests: literal placeholder, empty string, normal value, value that merely contains `$`.

## 7. Release workflow job (extension point from spec 013)

```yaml
  mcpb:
    needs: goreleaser
    if: needs.goreleaser.outputs.prerelease == 'false'
    runs-on: macos-latest          # needs codesign for the integrity check
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v7
      - name: Download release archive and checksums
        env: { GH_TOKEN: "${{ secrets.GITHUB_TOKEN }}" }
        run: |
          v="${{ needs.goreleaser.outputs.version }}"
          gh release download "v$v" -p "pdfm-mcp_${v}_darwin_universal.tar.gz" -p checksums.txt
          shasum -a 256 -c checksums.txt --ignore-missing
          tar xzf "pdfm-mcp_${v}_darwin_universal.tar.gz" pdfm-mcp
          codesign --verify --strict --verbose=2 ./pdfm-mcp
      - uses: actions/setup-node@v5
        with: { node-version: "lts/*" }
      - name: Build bundle
        run: ./scripts/build-mcpb.sh "${{ needs.goreleaser.outputs.version }}" ./pdfm-mcp dist
      - name: Attach to release
        env: { GH_TOKEN: "${{ secrets.GITHUB_TOKEN }}" }
        run: |
          v="${{ needs.goreleaser.outputs.version }}"
          gh release upload "v$v" "dist/pdfm-mcp-$v.mcpb" "dist/pdfm-mcp-$v.mcpb.sha256" --clobber
```

Rules: the job never rebuilds the Go binary; it verifies checksum **and** signature before packing; it uploads with the release token only; third-party actions pinned as in spec 013. `--clobber` is allowed only for these two files (so a failed job can be re-run); if the org enables immutable releases, replace it with "delete and re-create the draft" per the then-current guidance and record a deviation.

## 8. Testing

1. **Config guard** unit tests (section 6).
2. **`test/e2e/mcpb_test.go`** (tag `e2e`, skipped without `prlctl`):
   - *Manifest/tool drift*: start the built binary in fleet mode, list tools over stdio, and assert every `tools[].name` in the rendered manifest exists in the live list. (The reverse is not required because `tools_generated` is true.)
   - *Bundle handshake*: build a bundle with the script from the locally built binary, unzip it, launch `server/pdfm-mcp` with `PDFM_MCP_VM_ID='${user_config.vm_id}'` and `PDFM_MCP_LOG_LEVEL=info`, and assert the server starts in fleet mode (no VM-only tool such as `parallels_vm_info`).
3. **Script test**: version validation, non-existent binary, executable bit, byte-identity with the input.
4. **`mcpb validate`** on the rendered manifest in CI (PRs touching `packaging/**`).
5. **Manual QA** (`docs/qa/015-mcpb-bundle-QA.md`): install on Apple Silicon and Intel; settings dialog shows both fields; empty `vm_id` gives fleet tools, set `vm_id` gives VM tools; tool call (`parallels_list_vms`) works; update by installing a newer bundle; uninstall removes it; installing the same bundle from a quarantined download (browser) shows no Gatekeeper block; log level change takes effect after restart.

## 9. Security and privacy

- The bundle is an **executable** distribution: only distribute it through channels you control, always with the published `.sha256`, and tell users to verify it. MCP clients validate the `fileSha256` for registry-based installs (spec 016); manual installs rely on the user checking the hash.
- No secrets are bundled or requested. `user_config` has no `sensitive` fields.
- The manifest `long_description` carries the screenshot/command-output privacy statement; keep it. For Parallels use, link the internal data-handling guidance.
- Brand assets: the icon and product naming must be approved by Parallels brand/legal. Do not reuse third-party logos.
- Private repo: Claude Desktop users cannot download an asset from a private release without authentication. Distribute the `.mcpb` via an approved internal location (artifact store, intranet, or a release repo open to employees) and record the choice (spec 013 section 3).
- Hardened runtime and notarization are inherited from the release binary; do not re-sign inside the bundle unless the signature is verified afterwards.

## 10. Known unknowns / verification notes

- **Command path form** for binary servers (`${__dirname}/server/pdfm-mcp` vs relative `server/pdfm-mcp`): confirm with the installed Claude Desktop and keep whichever resolves.
- **Behaviour of unset optional `user_config`** in `env` substitution (empty string, omitted, or literal): the config guard covers all three; verify which actually happens and note it.
- **Executable bit** is preserved by `mcpb pack` and by the host's unzip: step 5 of the script and QA both check; if not, the manifest `command` may need a wrapper strategy.
- **`mcpb sign`** support and trust UX in Claude Desktop for corporate-signed bundles.
- Whether Claude Desktop enterprise admin controls (extension allowlists/blocklists) apply: coordinate with whoever manages Claude for Parallels before announcing.
- Pinned `@anthropic-ai/mcpb` version and whether the CLI's schema has moved past manifest `0.3`.
- Bundle size with a universal Go binary; MCPB has no stated hard limit, but check the host's.

### Deviations (fill in during implementation)

- The packer pin lives in `packaging/mcpb/mcpb-version.txt` and is overridable with `MCPB_PIN`; `make mcpb` passes it through the environment. **Pinned to 2.1.2** (npm latest at verification time, 2026-10-09); the placeholder value is gone.
- Extra script switches: `MCPB_ALLOW_THIN=1` (dev builds), `MCPB_STRICT=1` (release: fail on `<placeholder>` values), `MCPB_SKIP_PACK=1` (tests only). `build-mcpb.sh` fails without `packaging/mcpb/icon.png` unless `MCPB_ICON` is set.
- `.mcpbignore` is an allow-list; the post-pack check fails loudly if the packer drops a required file. **Fixed:** the allow-list must re-include the binary's directory as `!server` (no trailing slash) — the mcpb CLI's `ignore` checker tests directory entries as bare paths, so `!server/` silently skipped the whole subtree and the binary was missing from the packed bundle.
- e2e handshake: every tool is listed in both modes (including `parallels_vm_info`), so fleet mode is detected by `vm_id` being required on `parallels_vm_start`, not by absence of a VM-only tool.
- The CI `mcpb validate` step runs only when `packaging/mcpb/icon.png` exists.
- **`make mcpb-validate`** added (spec section 3 promised it): renders the template with a dev version + icon into a temp dir and runs the pinned CLI's `validate`.
- **Manifest URLs** (`author.url`, `homepage`, `support`) were `<…>` placeholders; `mcpb validate` rejects them as invalid URLs. Set to the real project URLs — `https://github.com/Parallels/pdfm-mcp` (repo moves from `Parallels-Corp/PDFM-mcp`), `…/issues`, and `https://www.parallels.com` — no invented values. Only the `license` placeholder remains (Legal decision; `MCPB_STRICT=1` fails on it).
- `packaging/mcpb/icon.png` was not supplied; a neutral 512×512 placeholder is committed so dev builds work. Replace with the brand-approved icon before the first public release.
- **Verified 2026-10-09:** `make mcpb` through the real pinned CLI (validate + pack) produces a bundle whose `server/pdfm-mcp` keeps its exec bit in the zip and is byte-identical to the input binary; the extracted binary runs; `make mcpb-validate` passes; both e2e MCPB tests pass, including `PDFM_MCP_E2E_MCPB_PACK=1` (real pack).
