# QA 015 — MCPB bundle

**Scope:** [spec 015](../specs/015-mcpb-bundle.md). Needs
`packaging/mcpb/icon.png`, a pinned `packaging/mcpb/mcpb-version.txt`, and the
manifest placeholders resolved for the release build.

**Executed 2026-10-09** on the developer Mac (Node 22, `@anthropic-ai/mcpb`
**2.1.2** pinned in `packaging/mcpb/mcpb-version.txt`, prlctl present).

## Automated — ✅ PASSED

```
make test-scripts    # build-mcpb suite: 15/15 (incl. placeholder/strict guards)
make test-e2e        # TestMCPBManifestToolDrift PASS, TestMCPBBundleHandshake PASS
```

- `TestMCPBManifestToolDrift`: every tool named in
  `packaging/mcpb/manifest.json.tmpl` is actually registered by the server.
- `TestMCPBBundleHandshake` (both modes):
  - default (`MCPB_SKIP_PACK=1`): bundle staged, unpacked, server started with
    `PDFM_MCP_VM_ID='${user_config.vm_id}'` arriving **literal** — server
    starts in fleet mode (config guard), `vm_id` required on
    `parallels_vm_start`. PASS.
  - `PDFM_MCP_E2E_MCPB_PACK=1` (real `mcpb pack` via npx, network): PASS.

## Manual — executed locally

### 1. Build — ✅ PASSED

```
make mcpb
```

Real packer path (not SKIP_PACK): `mcpb validate` passes, `mcpb pack` produces
`dist/pdfm-mcp-0.0.0-dev.mcpb` + `.sha256`. Bundle contents verified:

| Entry | Mode | Check |
|---|---|---|
| `manifest.json` | 644 | version substituted (`0.0.0-dev`), 7 tools, `user_config` = [vm_id, log_level] |
| `icon.png` | 644 | 512×512 PNG |
| `README.md` | 644 | repo README |
| `server/pdfm-mcp` | 755 | exec bit **in the zip**, **byte-identical** to the input binary (`cmp`) |

The extracted `server/pdfm-mcp --version` runs: `pdfm-mcp 2cef909-dirty
(commit 2cef909, built …)`.

- `make mcpb-validate` (new target) runs the pinned CLI's `validate` against
  the rendered template + icon: **passes** (icon 512×512 recommended).
- The script warns about the remaining `<placeholder>` (license) and
  `MCPB_STRICT=1` (set by the release job) fails closed on it.

**Fixed during verification:**

1. `packaging/mcpb/mcpb-version.txt` still held the placeholder
   `<pinned @anthropic-ai/mcpb version>` → pinned to **2.1.2** (npm latest at
   verification time).
2. `packaging/mcpb/icon.png` was missing (brand asset to be supplied) → a
   neutral 512×512 placeholder was generated; replace with the brand-approved
   icon before the first public release.
3. `.mcpbignore` used `!server/`; the mcpb CLI's `ignore` checker tests
   directory entries as bare paths, so a trailing-slash pattern matches
   nothing and the whole `server/` subtree (including the binary) was skipped.
   Fixed to `!server` (verified against the `ignore` package semantics).
4. Manifest URL placeholders (`<homepage url>`, `<issues or support url>`)
   failed `mcpb validate` (invalid URL) → set to the real project URLs
   (`https://github.com/Parallels/pdfm-mcp`, `…/issues`, author
   `https://www.parallels.com`) — no invented values.

## Manual — release owner / needs Claude Desktop

2. **Release bundle** (stable tag): `.mcpb` + `.sha256` attached; binary
   byte-identical to the archive's, `codesign --verify` passes (the `mcpb`
   job in release.yml does exactly this on a macOS runner).
3. **Install** (Apple Silicon and Intel): drag into Claude Desktop; settings
   dialog shows `Target VM (optional)` and `Log level`; record whether
   `${__dirname}/server/pdfm-mcp` resolves (spec 015 section 10).
4. **Fleet mode**: `vm_id` empty → `parallels_list_vms` works,
   `parallels_vm_start` requires `vm_id` (protocol-level behavior already
   covered by the e2e handshake).
5. **VM mode**: `vm_id` set → tools target that VM and reject an explicit
   `vm_id`.
6. **Log level**: change it; after restart, stderr verbosity changes.
7. **Quarantine**: browser download + install, no Gatekeeper block.
8. **Update / uninstall**: newer bundle over the old; uninstall removes it.
9. **Config guard**: ✅ verified — `internal/config` unit tests
   (literal placeholder, empty, normal, `$`-containing) pass in `make check`;
   e2e handshake covers the live literal-placeholder case.
10. **Platform**: manifest `compatibility.platforms: ["darwin"]` refuses
    other hosts (host-side behavior; verify on a non-macOS Claude Desktop).

## Result

The full local chain is green: template → rendered manifest (validated by the
pinned mcpb CLI) → packed bundle with an executable, byte-identical binary →
extracted server runs and starts in fleet mode with an unresolved
placeholder. Remaining items are release-tag/Claude-Desktop work.
