# QA: Screen text reading (spec 020)

## What this task delivers

- `helpers/ocr/` Swift sidecar `pdfm-ocr` (Apple Vision text recognition, universal binary via `build.sh`).
- `internal/ocr` package and the `parallels_vm_observe` tool (modes `text` and `find`).
- Conditional registration: the tool exists only when the helper is found.

**Privacy note.** Recognition is local, but the recognized text is returned to the MCP client and reaches the model provider. Do not observe a VM that shows credentials or confidential data while connected to an external AI service.

## Prerequisites

- A Mac with Parallels Desktop (`prlctl`), Xcode command line tools (`swiftc`, `lipo`), a running VM showing readable text (a terminal or an app with a dialog), the Go toolchain, and an MCP client (Inspector).

```sh
cd pdfm-mcp
go build -o bin/pdfm-mcp ./cmd/pdfm-mcp
./helpers/ocr/build.sh          # -> helpers/ocr/build/pdfm-ocr
```

**Executed 2026-10-09** on the developer Mac (Apple silicon, macOS 26, Parallels
27.0.1). The OCR target was the disposable VM `empty_catalog_test`, which
boots to the Parallels firmware boot manager — a static screen with clean
readable text ("Parallels ARM Virtual Machine", "Select Language",
"Continue", …). The VM was left stopped after the run. `make check` and
`make test-e2e` were run without the helper on PATH (AC 1: no OCR in CI).

## Steps

1. **Helper alone — ✅.** `--version` → `pdfm-ocr 1.0.0 (Vision
   VNRecognizeTextRequest, macOS 13+)`; `lipo -info` → `x86_64 arm64`.
   Capture → helper: one JSON document, 18 lines, boxes + confidence 1.0,
   e.g. `{"box":[196,167,435,184],"confidence":1,"text":"Parallels ARM
   Virtual Machine"}`.
2. **Helper errors — ✅.** Non-PNG stdin: exit 1, 0 bytes stdout, stderr
   `stdin is not a PNG or JPEG image`. `--bogus`: exit 2 + usage, 0 bytes
   stdout. Empty stdin: exit 1, `no image on stdin`.
3. **Discovery — ✅.** `PDFM_MCP_OCR_HELPER=<absolute>` → listed. Missing
   path → not listed, exactly one log line: `OCR helper not available;
   parallels_vm_observe not registered` with the stat reason. Relative
   path → not listed (never falls through). Next-to-executable copy (fresh
   `pdfm-mcp` + `pdfm-ocr` in a temp dir) → listed. PATH → listed.
   Non-executable copy → not listed.
4. **Text mode — ✅.** `count: 18`, joined `text` matches the boot menu;
   `width`/`height` = 1024×768 = the capture default (`scale: 1`,
   `original_width: 1024`). Coordinates line up with the capture (visual
   check: boxes sit on the text).
5. **Coordinates — ✅** (merged into 4: box [214,282,338,299] is the
   "Select Language" line of the same 1024×768 PNG).
6. **Find — ✅.** `query: "Language"` → 2 matches with `center` inside each
   box (first: box [214,282,338,299], center [276,290]). Case-insensitive:
   `"language"` matches. `regex: true, query: "^Continue$"` → the "Continue"
   line, center [247,404]. No match: `count: 0`, `matches: []`.
7. **Filters — ✅.** `region [0,0,512,768]` → 12 lines, every box
   **intersects** the region (edge-touching boxes are included, e.g.
   "Standard English)" [422,277,581,308] — intersect, not contain).
   `min_confidence: 0.95` → 18 lines (all at 1.0). `limit: 1` → `count: 1`,
   `truncated: true`.
8. **Validation — ✅.** `find` without `query` (`query is required in find
   mode`), `query` in text mode (`query and regex are only valid in find
   mode`), 3-value region, `x0>=x1`, `limit: 501`, `max_dimension: 255`,
   `mode: "x"` — all tool errors, no capture performed. Stopped VM: tool
   error from the capture step (`PrlDevDisplay_ConnectToVm … PRL_ERR_IO_S…`).
   No `pdfm-mcp-capture-*` directory remained in `$TMPDIR`.
9. **Mode contract — ✅.** VM mode (`PDFM_MCP_VM_ID`) lists the tool,
   rejects explicit `vm_id` (`vm_id must be omitted: this server targets a
   specific VM`), works without it. Fleet mode: `vm_id` required.
10. **No leakage — ✅.** No image content block, no base64 in the result.
    Server log has no screen words and no query; only counts/timings:
    `msg="ocr completed" lines=18 image_bytes=8025 took=339ms`.
11. **Timing (Apple silicon, 1024×768 PNG, helper wall time) — ✅.**
    default `.accurate`: **0.493 s**; `--fast`: **0.165 s** (~3×). Both
    recognize 18 lines. Within the spec's 0.2–1 s estimate.
12. **Sequence — ✅.** `parallels_vm_sequence` with an `observe` step
    (`mode: find, query: "Continue"`) → `status: completed`, step
    `kind: tool` with the same `count`/`matches` structure in `output`.
13. **Signed build — ⏸ manual (release owner).** The `build-ocr` job in
    `release.yml` (build → codesign hardened-runtime → notarytool, same
    `MACOS_*` secrets in the `release` environment) is implemented, but
    signing/notarization needs the Developer ID P12 and App Store Connect
    key — run at the first real tag, then re-verify
    `codesign --verify --strict pdfm-ocr` + quarantined download.

## Distribution wiring (verified alongside, per spec section 8)

- **Archive:** snapshot with the helper staged at the project root →
  `pdfm-mcp` + `pdfm-ocr` side by side in
  `pdfm-mcp_*_darwin_universal.tar.gz`; without it → the release still
  succeeds (the `pdfm-ocr*` glob is empty). Note: a plain name that matches
  nothing **fails** GoReleaser 2.12.5; only a glob is tolerated — the
  entry is therefore a glob.
- **Cask:** GoReleaser 2.12.5's cask schema has no second-binary field
  (`goreleaser check` rejects `binaries`), so the `tap` job injects
  `binaries "pdfm-ocr"` into the generated cask before `brew style` /
  `brew audit`. Verified locally: injection + `brew style --fix` +
  `brew style` clean on a real generated cask.
- **MCPB:** `build-mcpb.sh` takes an optional 4th argument (helper); the
  bundle then contains `server/pdfm-ocr` (0755, universal, byte-identical
  to the input) and the integrity check covers it; the 3-arg form is
  unchanged. `.mcpbignore` allow-lists the new entry.

## Result

All 12 executable steps pass on a real Mac with no code changes needed.
Only step 13 (signed/notarized build) remains, gated on the release
environment secrets.
