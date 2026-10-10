# QA: Built-in OCR + observe modes (spec 022)

## What this task delivers

- Apple Vision compiled into `pdfm-mcp` via cgo (`internal/ocr/vision.go` +
  `internal/ocr/vision.m`); no helper binary, no environment variable, no
  PATH lookup. The `pdfm-ocr` sidecar (spec 020) and all of its
  distribution wiring are deleted.
- `parallels_vm_observe` mimics the VMPal `observe` CLI: the screen's text
  (`text`, default), what changed (`changes`), or where the text is
  (`find <query>`), with `--region`/`--limit` equivalents.
- macOS-only build: CI and the release pipeline run Go builds on
  `macos-latest`; `CGO_ENABLED=1`.

**Privacy note.** Recognition is local, but the recognized text is returned
to the MCP client and reaches the model provider. Do not observe a VM that
shows credentials or confidential data while connected to an external AI
service.

## Prerequisites

- A Mac with Parallels Desktop (`prlctl`) and Xcode (cgo compiles the
  Objective-C wrapper with the system toolchain), a running VM with readable
  text on screen, and the Go toolchain.

```sh
cd pdfm-mcp
make build     # CGO_ENABLED=1 by default; -> bin/pdfm-mcp
```

**Executed 2026-10-09** on the developer Mac (Apple silicon, macOS 26,
Parallels 27.0.1). OCR targets: `capsule_manager_backend` (running, login
prompt) and the disposable `empty_catalog_test` (firmware boot menu, left
stopped after the run). No `pdfm-ocr` file existed anywhere (`bin/`,
`helpers/`, PATH) during any step, and `PDFM_MCP_OCR_HELPER` was unset.

## Steps

1. **Registration — ✅.** `tools/list` without any helper file or env var:
   29 tools, `parallels_vm_observe` present in fleet mode. `bin/` contains
   only `pdfm-mcp` (+ `-debug`). `grep -rn 'pdfm-ocr|OCR_HELPER'` over
   `*.go *.sh *.yml Makefile`: zero hits (docs carry historical notes only).
2. **text mode — ✅.** `observe {vm_id: capsule_manager_backend}` → 5 lines
   in 0.58 s, `scale: 1`, e.g.
   `{"text":"Debian GNU/Linux 13","confidence":1,"box":[0,18,154,32],"center":[77,25]}`;
   `text` field: `Debian GNU/Linux 13 capsule-manager tty1\ncapsule-manager
   login:`. Box/center in screenshot pixels as in spec 020.
3. **find mode — ✅.** `find query:"Debian"` → 1 match, `center:[77,25]`,
   `text` field = full screen, `matches` = subset. Same fields/validation
   as spec 020.
4. **changes mode, steady screen — ✅.** After the `text`/`find` calls above
   (each of which refreshes the baseline): `changes` → `added: []`,
   `removed: []`, `count: 0`, `previous_observed: true`.
5. **changes mode, real diff — ✅.** On the `empty_catalog_test` boot menu
   (language submenu open, 23 baseline lines): `send_key {key:"escape"}` →
   back to the main menu → `changes` → `added: 3`, `removed: 8`:
   the four language-list lines removed (`Standard English`,
   `Standard Français`, `English`, `Français`), the hint line changed
   (`Esc=Exit Entry` out; `↑↓=Move Highlight`, `<Enter>=Select Entry` in —
   OCR reads the arrow as `Tl`), the `<Standard English>` box line replaced.
   `find "Parallels"` on the new screen → 1 match at `[315,176]`.
6. **region — ✅.** `region:[0,0,1024,200]` on the main menu → 3 lines
   (header block only) of the 18 full-screen lines.
7. **Stopped VM — ✅.** `observe` on a stopped VM → tool error
   (`prlctl capture … PRL_ERR_IO_STOPPED`), clean JSON-RPC error result. No
   `pdfm-mcp-capture-*` temp dir left in `$TMPDIR`.
8. **Release pipeline on macOS — ✅.** `make release-snapshot` (GoReleaser
   2.12.5, `CGO_ENABLED=1`, `goos: [darwin]`): arm64 + amd64 cgo builds,
   universal merge, `pdfm-mcp_0.0.1-snapshot_darwin_universal.tar.gz`
   contains exactly `README.md` + `pdfm-mcp`; `lipo -archs` → `arm64
   x86_64`; the generated cask has a single `binary "pdfm-mcp"` line (no
   `binaries` stanza); the universal binary runs (`--version`).
   `make build-all` (the CI `check` job's cross-arch step) builds
   `darwin/arm64` + `darwin/amd64` natively. `make release-check` passes.
   `release.yml` has no `build-ocr` job; the `tap` job no longer injects a
   `binaries` stanza; the `mcpb` job passes 3 args to `build-mcpb.sh`.
9. **Gates — ✅.** `make check` (fmt, vet, golangci-lint, `go test -race
   ./...`), `make test-e2e` (observe present in the protocol suite),
   `make test-scripts` (24 passed). Unit tests: `internal/ocr` (canned JSON
   through `Parse`, runner errors, 30 s timeout via injected runner,
   language validation) and `internal/tools` changes-mode suite (first
   call all-added + `previous_observed: false`, edit diff, no-change,
   baseline refresh via `text`/`find`, limit split, region leak check,
   17-VM LRU eviction).

## Findings and fixes

- **cgo buffer not NUL-terminated (fixed).** `vision.m` allocated
  `json.length` bytes and `C.GoString` read past the end into adjacent
  heap memory: most captures were fine (fresh heap, zeroed neighbours) but
  one live run failed with `decode ocr output: trailing data after JSON
  document`. Fixed by allocating `json.length + 1` and writing the
  terminator; the live suite passed afterwards.
- **prlctl capture flake (known, spec 018).** A capture immediately after
  screen activity can fail once; the QA driver retries. Product behaviour
  unchanged: a failed capture is a tool error, not a crash.
- **Vision SDK names.** The SDK exposes `VNRecognizedTextObservation` (not
  `VNRecognizedTextAnnotation`) and candidates via the method
  `[obs topCandidates:1]`; the wrapper uses those.

## Result

All 8 acceptance criteria of spec 022 verified: built-in OCR works live in
all three modes with no sidecar, no env var and no helper file anywhere;
the pipeline produces a single universal binary end to end on macOS.
