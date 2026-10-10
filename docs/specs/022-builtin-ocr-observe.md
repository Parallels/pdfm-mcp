# Spec 022: Built-in OCR (`cgo` + Apple Vision in the server binary) and VMPal-style `observe`

| | |
|---|---|
| Status | Implemented (unit + e2e + live verification complete on a real Mac 2026-10-09; signed/notarized build pending the first real tag, like spec 013) |
| Audience | Implementing agents / engineers, release owner |
| Scope | Replace the `pdfm-ocr` sidecar (spec 020) with an in-process Apple Vision recognizer compiled via `cgo`; reshape `parallels_vm_observe` to the VMPal `observe` CLI shape (`--text` default / `--changes` / `--find <text>`, `--region`, `--limit`); remove all sidecar distribution wiring; move the darwin build (CI + release) to macOS runners |
| Out of scope | Orchestrator `screen_text` condition and `wait until text` (still follow-ups), click-by-text, non-text observation, non-macOS builds |
| Supersedes | The sidecar design of spec 020 (decision table rows "Engine", "Why a sidecar, not cgo", "Discovery", "Invocation") and section 8 (distribution deltas); the CGO/runner decisions of spec 013 (section 2 rows "CGO" and "Signing + notarization", pipeline sketch) — the darwin build now requires cgo and a macOS runner. Everything else in spec 020 (validation, `Find`/`Join`/`Filter`, boxes, privacy analysis, limits) stands |

## 1. Goal

The owner wants OCR to be **part of the tool, not a separate binary**: one
`pdfm-mcp` file to deploy, no helper to copy, no `PDFM_MCP_OCR_HELPER`, no
second file in the archive/cask/MCPB bundle. The `observe` tool should read
like the VMPal CLI it imitates:

```
observe [--text | --changes | --find <text>]
        The screen's text (the default), what changed, or where the text is
        [--region <region>] [--limit <n>]
```

The owner also confirmed: **no cross-compilation — the product is
macOS-only**, so the static-binary / Linux-CI constraints that motivated the
sidecar no longer apply.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Engine | Apple Vision `VNRecognizeTextRequest` called **in-process** through a small Objective-C wrapper (`cgo`) | One binary, zero deployment friction, no subprocess spawn per call. Cost accepted by the owner: a hung native call can be abandoned (30 s timeout) but not killed |
| No sidecar | `helpers/ocr`, `pdfm-ocr`, `make ocr-helper`, `ocr.Locate`/`Helper`/`Runner`, `PDFM_MCP_OCR_HELPER` and all spec-020 distribution wiring (release `build-ocr` job, archive glob, cask `binaries` injection, MCPB 4th argument) are deleted | The owner's request; the tool must stand alone |
| Build matrix | **darwin only**, `CGO_ENABLED=1`. No non-darwin build path, no stub | "No cross compatibility, just mac, pdfm is only mac" (owner). Linux CI no longer builds this project |
| CI / release runners | All Go build/test jobs and the goreleaser job move to `macos-latest`; the JSON-registry and mcpb jobs that only need node keep their runners as-is | cgo + Vision requires the macOS SDK; goreleaser builds both arches natively (arm64 host, `GOARCH=amd64` cgo cross within darwin works) |
| Wrapper | `internal/ocr/vision.m` (Objective-C) + `internal/ocr/vision.go` (cgo). C ABI: PNG/JPEG bytes in, one strict-JSON document out (same wire shape the sidecar emitted), bounded error string out | `internal/ocr.Parse` (strict decode, line/text caps, box bounds, sorting) is reused **unchanged**; all validation stays in Go |
| Recognition settings | `.accurate`, `usesLanguageCorrection = YES` (parity with the sidecar); the wrapper collects at most `MaxLines` (5000) annotations | Same accuracy behaviour as spec 020; native-side cap so a pathological screen cannot overflow the C string |
| Box conversion | Vision normalized bottom-left box → integer top-left `[x0,y0,x1,y1]` in pixels, clamped to the image, min 1 px size | Same convention as spec 020 (screenshots and future pointer tools) |
| Timeout | 30 s context deadline; on expiry the Go side returns an error and **abandons** the goroutine (it cannot be killed while inside Vision) | Documented trade-off vs the killable sidecar; Vision hangs are rare and bounded by the Neural Engine |
| Registration | Unconditional in `server.New` (Vision is always present in a darwin build); tool **and** sequence step always available (subject to the usual policy filtering) | No lookup, no conditional registration, no "OCR helper not available" log line |
| Interface | `mode: "text"` (default) \| `"changes"` \| `"find"`; `query` (find, required), `regex`, `region`, `limit` kept, plus the existing extras `min_confidence`, `max_dimension` | Mimics the VMPal CLI; existing extras stay for compatibility |
| `changes` semantics | The server keeps the **last full-screen recognition per VM** (LRU 16 VMs, TTL 1 h). `changes` reports lines **added** (present now, absent last time) and **removed** (absent now, present last time), matched by exact line text. The first call for a VM reports every line as added with `previous_observed: false`. **Every** observe call (any mode) refreshes that VM's baseline (the unfiltered recognition). `region`/`min_confidence`/`limit` apply to the reported diff, not to the baseline | "What changed since you last looked" is the useful agent semantics; a single process-level store is shared by the tool and sequence-step handlers |
| Privacy | Unchanged from spec 020 section 7: pixels never leave the machine; recognized text reaches the model provider (tool description warns); logs carry counts/timings only | Same surface as before, fewer moving parts |

## 3. Native wrapper contract

- `vision.m` exposes one C function, called from `vision.go` via cgo:

  ```c
  // Returns a malloc'd UTF-8 JSON document, or NULL on failure with a
  // bounded message in errbuf. PNG or JPEG bytes in; no network, no file
  // system access, no globals.
  char *pdfm_vision_recognize(const unsigned char *data, int len,
                              int fast, const char *lang_csv,
                              char *errbuf, int errbuf_len);
  ```

- JSON document: `{"width":W,"height":H,"lines":[{"text":"...","confidence":0..1,"box":[x0,y0,x1,y1]}]}` — identical to the retired sidecar's output so `ocr.Parse` is reused.
- The cgo side wraps it in `ocr.Vision`, an `ocr.Recognizer`:
  `Recognize(ctx, png)` bounds the input (≤ `MaxInputBytes`), applies the
  30 s context deadline on a **dedicated goroutine** (expiry → error,
  abandoned goroutine), and runs the output through `ocr.Parse`.
- No new Go dependency (cgo is part of the toolchain; the Objective-C file
  links the system frameworks `Vision`, `CoreGraphics`, `ImageIO`,
  `Foundation`).
- Tests never call Vision: `ocr.Vision` takes an injectable
  `run func(ctx, png, fast, langs) ([]byte, error)` (default: the native
  call). Unit tests feed it canned JSON through the real `Parse`.

## 4. `parallels_vm_observe` contract

Input (same shape as spec 020, plus `changes`):

| Field | Meaning |
|---|---|
| `vm_id` | mode contract of spec 003 (fleet required, VM mode omitted) |
| `mode` | `text` (default) \| `changes` \| `find` |
| `query` | `find` only, required, ≤ 1024 bytes; substring case-insensitive or RE2 with `regex: true` |
| `region` | `[x0,y0,x1,y1]` screenshot pixels, intersect semantics (unchanged) |
| `limit` | 1-500, default 50 (unchanged) |
| `min_confidence` | 0-1, default 0.3 (unchanged) |
| `max_dimension` | 256-4096, default 1568 (unchanged) |

Output (existing fields kept; `changes` adds two):

- `text` mode: `lines` + joined `text` (unchanged).
- `find` mode: `matches` with `center` (unchanged).
- `changes` mode: `added` and `removed` (both `[]ObserveItem`),
  `previous_observed` (bool), `count` = reported `added + removed` after
  filters/limit; `truncated` when the limit cut anything.

Description starts: "Reads the text on the VM's screen: the screen's text
(the default), what changed since the previous observation, or where a
piece of text is. …" and keeps the spec-020 privacy warning.

## 5. Deletions (spec-020 sidecar reversal)

- `helpers/ocr/` (Swift helper + `build.sh`), `Makefile: ocr-helper`.
- `internal/ocr/locate.go`, the `Helper`/`Runner`/`execRunner`/`Version`
  code in `ocr.go` (keep `Box`, `Line`, `Recognition`, `Parse`, `Join`,
  `Filter`, `Find`, `FindOptions`, limits).
- `release.yml`: the `build-ocr` job, the artifact staging step in
  `goreleaser`, the cask `binaries` injection in `tap`, the helper
  extract/verify/4th-arg in `mcpb`.
- `.goreleaser.yaml`: `pdfm-ocr*` archive entry and related comments.
- `scripts/build-mcpb.sh`: the 4th-argument helper path (back to 3 args);
  `packaging/mcpb/.mcpbignore`: the `!server/pdfm-ocr` line.
- README: the `PDFM_MCP_OCR_HELPER` env row and "Optional OCR helper"
  section (replaced by a one-liner: OCR is built in on macOS).

## 6. Release pipeline delta

- `ci.yml` and `release.yml`: every job that builds or tests Go code runs
  on `macos-latest` (`verify`, `release-config` snapshot, `goreleaser`).
  `.goreleaser.yaml` builds keep `goos: [darwin]` and switch
  `CGO_ENABLED=0` → `CGO_ENABLED=1`; the universal `lipo` merge is unchanged.
- The archive contains only `pdfm-mcp` (+README, future LICENSE); the cask
  is a plain binary cask again; the MCPB bundle contains only
  `server/pdfm-mcp`.
- Signing/notarization stays with the `release` environment and
  goreleaser's `notarize` block (no change); `make release-snapshot` runs
  on macOS.

## 7. Acceptance criteria

1. `make check` and `make test-e2e` pass on a macOS runner (and locally on the dev Mac); no `pdfm-ocr` file, path or string remains anywhere in the repo.
2. `parallels_vm_observe` is registered in both modes with no environment, file or PATH preconditions, and is present as a sequence step (drift guard green).
3. `mode: text` (default) and `mode: find` behave exactly as spec 020 verified them (same fields, same validation errors).
4. `mode: changes` returns `added`/`removed` against the previous observation of the same VM; the first call reports all lines as added with `previous_observed: false`; a subsequent `text` or `find` call refreshes the baseline; `limit` and `region` apply to the diff.
5. No image data in any result; logs carry counts/timings only (grep a screen word and a query: no hits).
6. Live on a real Mac: `text` and `find` on a running VM; `changes` after an actual screen change (e.g. a key press on the boot menu) reports the new lines; a stopped VM still yields a tool error with no temp capture dir left.
7. `make release-snapshot` on macOS produces the universal archive containing only `pdfm-mcp`; `make release-check` passes; no `build-ocr` job, no cask `binaries` stanza, no MCPB helper.
8. The cgo wrapper unit tests (canned JSON through `Parse`, error paths, timeout via injected runner) pass without a display.

## 8. QA

See [QA 022](../qa/022-builtin-ocr-observe-QA.md).

## 9. Deviations (implementation + QA notes)

- **NUL terminator (bug found live, fixed).** The first version of the
  wrapper allocated exactly `json.length` bytes; Go's `C.GoString` reads to
  the first NUL, so it consumed adjacent heap memory. Most captures were
  fine (fresh heap) but one failed with `trailing data after JSON
  document`. `vision.m` now allocates `json.length + 1` and writes the
  terminator.
- **Vision SDK names.** The SDK exposes `VNRecognizedTextObservation`
  (not `VNRecognizedTextAnnotation`); candidates come from the method
  `[obs topCandidates:1]`, not a `topCandidates` property. The wrapper
  uses the former; the contract in section 3 is unaffected.
- **Native hang is unkillable.** The 30 s timeout abandons the goroutine;
  the C call cannot be interrupted (documented trade-off, section 2).
- **prlctl capture flake.** A capture right after screen activity can fail
  once (spec 018); the QA driver retries. Product behaviour unchanged.
