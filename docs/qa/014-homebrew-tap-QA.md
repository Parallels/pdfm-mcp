# QA 014 — Homebrew tap

**Scope:** [spec 014](../specs/014-homebrew-tap.md). Requires the tap repo,
`HOMEBREW_TAP_TOKEN`, `PUBLISH_HOMEBREW=true` and the placeholders in
`.goreleaser.yaml` resolved.

**Executed 2026-10-09** (Homebrew 4.x, `brew style`/`brew audit` against a
scratch tap). Steps 1–3 and 10 are verified locally; steps 4–9 need the
real tap repo + token and remain release-owner work.

## Steps

### 1. Key names — ✅ PASSED (after fix)

`make release-check` passes with the `homebrew_casks` block.

- **Fixed:** the binary-cask key is `binary` (singular), not `binaries`
  (checked against `goreleaser schema`, `$defs.HomebrewCask` on 2.12.5).
  `binaries` failed `goreleaser check`.

### 2. Dry run — ✅ PASSED

`make release-snapshot`; generated `dist/homebrew/Casks/pdfm-mcp.rb`:

- `version`, `sha256`, `url` (release-asset download URL), `name`, `desc`,
  `homepage`, `binary "pdfm-mcp"`, `caveats` with
  `#{HOMEBREW_PREFIX}/bin/pdfm-mcp`, `livecheck { skip }`. ✓
- **No** token, **no** `postflight`/quarantine hook. ✓
- Caveats are a single string (see "caveats" note under Deviations).
- The `license` stanza is omitted until `license` is set to a known SPDX
  id (GoReleaser only renders it then) — expected with the Legal
  placeholder.

### 3. Tap CI (scratch tap) — ✅ PASSED

Copied the cask into a scratch git tap and ran it through Homebrew:

```
brew style <cask>              # 11 template-order offenses in the raw file
brew style --fix <cask>        # corrects all of them
brew style <cask>              # "no offenses detected"
brew audit --cask --strict <tap>/pdfm-mcp   # exit 0
```

The raw file GoReleaser emits fails `brew style` (stanza order is
hard-coded in its template: `name/desc/homepage/version` instead of
Homebrew's `version`-first order). The `tap` job in `release.yml` runs
`brew style --fix` **before** `brew audit` and before committing, so the
tap repo only ever receives style-clean casks and its own CI
(`brew style` + `brew audit --cask`) stays green. See Deviations.

### 4. Pre-release tag does not touch the tap — ✅ (gate verified)

`release.yml` `tap` job: `if: prerelease == 'false' && vars.PUBLISH_HOMEBREW == 'true'`.
A `-rc.*` tag never satisfies the condition. (Live check = step 6.)

### 5. Stable install on a clean Mac — ⏸ release owner (needs real tap + token)

`brew tap Parallels/tap` (tap name — the repo is
`Parallels/homebrew-tap`; Homebrew strips the `homebrew-` prefix from
tap names, verified locally), then `brew install --cask Parallels/tap/pdfm-mcp`;
no Gatekeeper prompt (binary is notarized per spec 013); `pdfm-mcp
--version` prints the release version.

### 6. Upgrade / reinstall / uninstall — ⏸ release owner

`brew upgrade --cask pdfm-mcp` from the previous version; `brew
reinstall --cask`; `brew uninstall --cask` removes the symlink.

### 7. GUI client — ⏸ release owner

Configure Claude Desktop with the absolute path from `brew --prefix`
(`#{HOMEBREW_PREFIX}/bin/pdfm-mcp` per the caveats); tools appear.

### 8. Negative: missing token — ⏸ release owner

With `HOMEBREW_TAP_TOKEN` unset (and `PUBLISH_HOMEBREW=true`), the `tap`
job must fail at the token check *after* the GitHub Release exists;
document the re-run. The job's first line fails closed
(`HOMEBREW_TAP_TOKEN is not set in the release environment`).

### 9. Gate: `PUBLISH_HOMEBREW` unset — ✅ (gate verified)

Same `if` condition as step 4; the `tap` job is skipped entirely when the
variable is not `'true'`. (Live check = step 5/8.)

### 10. Secrets — ✅ PASSED

No token/secret in the generated cask (grep for token/secret/password/
x-access: clean). The token is consumed only in the `tap` job's git
request header (never in a URL, so it cannot leak into git error output).
The tap repo holds no secrets (layout: `Casks/`, CI, README, LICENSE).

## Result

The cask is generated correctly and is style/audit-clean after the
normalization step; the publish gate (stable tag + `PUBLISH_HOMEBREW` +
token) is wired in `release.yml`. Everything that touches the real tap
repo is gated behind a token and the publication variable and is
release-owner work.
