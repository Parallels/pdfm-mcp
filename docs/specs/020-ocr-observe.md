# Spec 020: Screen Text Reading (`parallels_vm_observe`, Apple Vision OCR sidecar)

| | |
|---|---|
| Status | Superseded by [spec 022](022-builtin-ocr-observe.md) (2026-10-09): the sidecar design is reversed — Apple Vision is now compiled into `pdfm-mcp` via cgo and `observe` gained the `changes` mode. The implementation below (helper, discovery, conditional registration, distribution wiring) has been deleted; the validation, box/`Find`/`Join`/`Filter` and privacy analysis carry over into 022 |
| Audience | Implementing agents / engineers, release owner |
| Scope | Swift helper `pdfm-ocr` (`helpers/ocr/`); Go package `internal/ocr` (locate, run, parse, find); tool `parallels_vm_observe`; conditional registration; tests; QA doc; distribution deltas listed as follow-ups |
| Out of scope | Orchestrator integration (`screen_text` check, `wait until text`), click-by-text (needs pointer input), `observe changes` mode, non-Latin tuning, bundling OCR models, any cloud OCR |

## 1. Goal

Close the largest gap against VMPal (`docs/battlecards/vmpal.md`): the agent can **read what is on the VM screen** and **find where a piece of text is**, without sending an image to a model and without a new Go dependency.

```
parallels_vm_observe  ->  svc.Capture (prlctl capture)  ->  imaging.Downscale  ->  pdfm-ocr (stdin PNG, Vision)  ->  JSON lines + boxes
```

### Acceptance criteria

1. `make check` and `make test-e2e` pass without the helper present; no OCR runs in CI on Linux.
2. `parallels_vm_observe` is listed (both modes) only when the helper is found; otherwise absent, with one info log line.
3. `mode: text` returns lines with pixel boxes plus joined `text`; `mode: find` returns matching lines with `center` points. Coordinates are in the same downscaled image `parallels_vm_capture` returns by default, so they line up.
4. The result never contains image data. Logs never contain recognized text, queries or pixels.
5. The helper reads stdin only, writes one JSON document to stdout, never touches the network or the file system, and exits non-zero with a stderr message on any failure.
6. Every step in `docs/qa/020-ocr-observe-QA.md` passes on a real Mac.

## 2. Decisions

| Topic | Decision | Rationale |
|---|---|---|
| Engine | Apple Vision `VNRecognizeTextRequest` through a tiny Swift sidecar | Built into macOS (the product is macOS-only), on-device, uses the Neural Engine on Apple silicon, no models to ship, **no new Go dependency** (no cgo, no Tesseract, no ONNX) |
| Why a sidecar, not cgo | Separate process, fixed argv, image on stdin | Keeps the main binary static (`CGO_ENABLED=0`, spec 013), cross-compilable from Linux CI, and a crash or hang in Vision cannot take the server down (30 s timeout, killed on expiry) |
| Accuracy | `.accurate`, `usesLanguageCorrection = true`; `--fast` flag available; `--lang` list optional | Screen text is mostly clean UI type; accurate mode handles small fonts. Language correction helps prose but can alter codes, paths and identifiers: treat results as "what a person would read", not byte-exact |
| OS support | Deployment target macOS 13+ | On macOS 13 the default request revision is 3 (more languages, better accuracy than revision 2 on macOS 12). Older macOS is unsupported: no helper build, tool absent |
| Image source | The same downscaled PNG as `parallels_vm_capture` (1568 px default, `max_dimension` 256-4096) | Coordinates match what the agent sees from the capture tool; `scale` and `original_width` convert to native pixels |
| Coordinates | Ints, origin top-left, `[x0,y0,x1,y1]`; helper converts Vision's normalized bottom-left boxes | Same convention as screenshots and future pointer tools |
| Discovery | `PDFM_MCP_OCR_HELPER` (absolute path, must exist, regular, executable; **never falls through** when set), else `pdfm-ocr` next to `os.Executable()` (symlinks resolved first, so a Homebrew `bin` link finds the Cellar file), else PATH. Non-darwin = unavailable | An explicit override that is wrong is a configuration error to surface, not something to silently replace |
| Invocation | `exec.CommandContext`, fixed argv (`--fast`, `--lang a,b` built from validated options), PNG on stdin, stdout capped at 4 MB, stderr capped at 4 KB, never inherited, 30 s timeout | Stdout rule; bounded memory; no shell |
| Output validation | Strict JSON decode (unknown fields rejected, single document), at most 5000 lines, text at most 4096 characters, confidence 0-1 required, boxes inside the image | The helper is trusted to be ours, but its output is still data (org rule 4) |
| Result shape | `{vm_id, mode, width, height, original_width, original_height, scale, text?, lines[], matches[], count, truncated}`; both arrays always present (empty when unused) | Stable schema for clients |
| Defaults | `min_confidence` 0.3, `limit` 50 (max 500), text capped at 256 KB | Keeps the model payload small |
| Find semantics | Case-insensitive substring; `regex: true` = RE2 (linear time, max 1024 bytes, case-sensitive unless `(?i)`); region keeps lines whose box overlaps (positive area); `min_confidence` inclusive | Predictable, safe against ReDoS |
| Annotations | `ReadOnlyHint: true`, `DestructiveHint: false`, `OpenWorldHint: false` | No VM mutation |
| Sequence | `RegisterObserveStep(reg, rec)` registers it as a step under the same condition as the tool | Drift guard stays green (see 6.3) |

## 3. Repository layout (delta)

```
helpers/ocr/{main.swift, build.sh, README.md}      # NEW  Swift helper
internal/ocr/{ocr.go, locate.go, *_test.go}        # NEW  locate, run, parse, Find/Filter/Join
internal/tools/parallels_vm_observe.go             # NEW  tool, handler builder, step registration
internal/tools/parallels_observe_test.go           # NEW
docs/specs/020-ocr-observe.md, docs/qa/020-ocr-observe-QA.md
```

## 4. Helper `pdfm-ocr`

- `pdfm-ocr [--fast] [--lang en-US,de-DE] [--version] < image`.
- Reads at most 32 MB, accepts PNG or JPEG only, rejects images over 100 megapixels.
- Output (one line of JSON): `{"width":W,"height":H,"lines":[{"text":"...","confidence":0.97,"box":[x0,y0,x1,y1]}]}`; lines sorted by `y0` then `x0`; confidence rounded to 3 decimals; boxes floor/ceil-rounded and clamped to the image.
- Errors: single line on stderr, exit 1 (bad input) or 2 (bad usage), **no stdout**. Messages never include recognized text.
- Never reads the environment, files or network; never logs text.
- Build: `helpers/ocr/build.sh` compiles `arm64` and `x86_64` slices (`-target <arch>-apple-macos13.0`, `-O`, Vision/CoreGraphics/ImageIO/Foundation) and joins them with `lipo -create`.

## 5. Go package `internal/ocr`

Pure, no MCP knowledge. `Recognizer` interface (`Recognize(ctx, png) (Recognition, error)`), `Helper` implementation with an injectable `Runner` (tests), `Locate()`/`LocateWith(Env)`, `Parse`, `Find`, `Filter`, `Join`, `Box`/`NewBox`. Stdout and stderr go through a capping buffer that never fails the write, so a chatty helper cannot kill itself on a broken pipe nor grow memory. Errors quote only the helper's own stderr (capped, 512 bytes), never output text.

## 6. Tool `parallels_vm_observe`

### 6.1 Input

`vm_id` (mode contract of spec 003), `mode` (`text` default | `find`), `query` (find only, required), `regex`, `region` `[x0,y0,x1,y1]` in screenshot pixels, `min_confidence`, `limit`, `max_dimension`. All validation (including mode misuse such as `query` in text mode) happens before any capture.

### 6.2 Flow

resolve target, validate, `svc.Capture`, `imaging.Downscale(maxDim)`, `Recognize`, verify the helper reported the same width/height as the image it was given (else error), filter/find, apply `limit`, build output. The handler returns no `CallToolResult`, so the SDK serializes the structured output; no image content block exists.

### 6.3 Registration

`RegisterParallelsVMObserve(s, mode, rec)` and `RegisterObserveStep(reg, rec)`; both are called only when `ocr.Locate()` and `ocr.NewHelper` succeed. Input schema is normalized so `region` is a plain `array`. `TestSequenceDriftGuard` stays green because the tool is not listed in its fixture (no helper) and the step is only registered next to the tool.

## 7. Privacy and security analysis

| Concern | Position |
|---|---|
| Pixels | Never leave the machine from this tool: recognition is local (Vision, on-device). The screenshot is held in memory only; the capture temp dir is removed as in spec 004 |
| Recognized text | **Is returned to the MCP client and therefore reaches the model provider.** A VM screen can show credentials, tokens, customer data or confidential source. README and the tool description must say so. Observe is strictly less revealing than a screenshot only in that it can be narrowed (`region`, `limit`, `min_confidence`) |
| Logs | Counts (lines, bytes) and timings only; never text, queries (a query may itself be a secret fragment) or pixels |
| Helper trust | Fixed argv built from validated options; absolute override path checked (regular, executable); output strictly validated and bounded; 30 s kill |
| Supply chain | The helper is built in our CI and signed with the same Developer ID as the main binary; there is no download at runtime |
| Prompt injection | Recognized text is untrusted data from the guest screen. It must be treated as data, never as instructions (org rule 4). The tool description states it is screen content |
| Classification | Read tool for the safety policy (spec 019); the policy agent classifies `parallels_vm_observe` as read |

## 8. Distribution (follow-ups, specs 013/014/015 not edited here)

The helper needs a **macOS runner** (swiftc, lipo, codesign, notarytool); GoReleaser on Linux cannot build it. Deltas:

- **013 (release pipeline):** add a macOS job (or `before` hook on the existing macOS runner) running `helpers/ocr/build.sh`; sign with the Developer ID (hardened runtime, `codesign --options runtime --timestamp`); include `pdfm-ocr` in the notarization submission (zip) and in the universal-archive `files`/extra files so the archive contains `pdfm-mcp` and `pdfm-ocr` side by side; add `pdfm-ocr` to `checksums.txt` and the SBOM; `make release-snapshot` skips it when not on macOS.
- **014 (Homebrew cask):** list `pdfm-ocr` under `binaries` (or `artifact`/`binary` stanza) so it lands in the same directory as `pdfm-mcp`; the "next to the executable" lookup resolves symlinks to cover Homebrew's bin links.
- **015 (MCPB):** ship `server/pdfm-ocr` beside `server/pdfm-mcp`, covered by the bundle's signature/notarization and the placeholder guard; no manifest change is needed because lookup is relative to the executable.
- Unsigned dev builds: set `PDFM_MCP_OCR_HELPER` to `helpers/ocr/build/pdfm-ocr`.

## 9. Performance expectations (unknown, measure on a real Mac)

Estimates only: capture under 1 s (spec 004); helper start-up 50-200 ms; `.accurate` recognition of a 1568 px screen on Apple silicon roughly 0.2-1 s, slower on Intel; `--fast` several times quicker. Record real timings in the QA doc and tune `max_dimension` defaults (small text may need a larger value) and whether to expose `fast`/`languages` as tool or config options.

## 10. Testing

1. `internal/ocr` unit tests: JSON decode bounds (line count, text length, trailing data, unknown fields, confidence and box checks, error messages do not quote text), `Find` (case-insensitive substring, RE2, query limits, confidence, region incl. edge-touching, nth), `Join`, `NewBox`, `Helper` with a fake runner (argv, stdin, error and stderr capping, timeout), language validation, cap buffer, `Locate` precedence with temp dirs and fake executables (env explicit, next-to, symlink resolution, PATH, non-executable, non-darwin).
2. Tool tests with a fake `Recognizer` and fake capture runner: both modes and the mode contract, validation table (no capture on invalid input), scale math, limit/truncation, `max_dimension`, failure propagation, nil recognizer, no text in logs.
3. In-memory protocol tests: listed with a recognizer (annotations, portable schemas, description contract, `vm_id` required only in fleet mode, no image content, no base64 PNG), VM mode contract, absent from `server.New` when the helper override points nowhere.
4. QA doc on a real Mac.

## 11. Implementation order

Helper and package first, tool second, then the coordinator wires `server.New` (section 12), then README rows, Makefile target, release deltas (section 8).

## 12. Coordinator wiring

See the report accompanying this spec: `server.New` builds the recognizer from `ocr.Locate()` and registers the tool and step conditionally; Makefile gets `ocr-helper`; README gets a tool row and a privacy note.

## 13. Follow-ups

- Orchestrator: `screen_text` condition (regex/substring over `Recognize`) and `wait until text` / `text gone` built on the `ocr` package (after the sequence wait primitives land).
- Click-by-text (`center` of a match) once pointer input exists.
- `observe changes` mode: diff recognized text between two captures.
- Config for languages and `--fast` (`PDFM_MCP_OCR_LANGS`).
- Row/column grouping and confidence-based re-OCR of small regions at native resolution.

## 14. Known unknowns

- Vision accuracy on Parallels guest rendering (Windows ClearType, dark themes, tiny fonts) is unmeasured. (The firmware boot menu — static, high-contrast — recognized 18/18 lines at confidence 1.0 on 2026-10-09.)
- Whether the sandbox/hardened runtime of a signed helper needs any entitlement (none expected: Vision is on-device and needs no network). Unverifiable until the first signed build (QA step 13).
- Inherited environment is passed to the helper unchanged (Vision may need `HOME`/`TMPDIR`); verify a stripped environment works before restricting it. (Not tested: the live runs inherited the full environment.)
- ~~Not compiled or run in the authoring sandbox (no Go or Swift toolchain): the first real build may need small fixes.~~ — Resolved 2026-10-09: the first real build (`make ocr-helper`) succeeded with no fixes.

## 15. Deviations

- Verified live on 2026-10-09 with no code changes; QA 020 records timings
  (0.493 s `.accurate` / 0.165 s `--fast` on a 1024×768 PNG, Apple
  silicon).
- **Cask (section 8, 014 bullet):** GoReleaser 2.12.5's cask schema cannot
  express a second binary (`goreleaser check` rejects `binaries`), so the
  `tap` job in `release.yml` injects `binaries "pdfm-ocr"` into the
  generated cask before `brew style`/`brew audit` (verified locally). The
  pre-implementation placeholder in `.goreleaser.yaml` had suggested *no*
  cask entry; the spec text is followed — owner, confirm.
- **Archive (section 8, 013 bullet):** the helper rides in the archive via
  a **glob** entry (`pdfm-ocr*`) — GoReleaser 2.12.5 fails a release on a
  plain name that matches nothing, but tolerates an empty glob (verified by
  snapshot). The `build-ocr` job (macos-latest, `release` environment)
  builds, codesigns (hardened runtime) and notarizes the helper and hands
  it to the `goreleaser` job as a `pdfm-ocr` artifact.
- **MCPB (section 8, 015 bullet):** `scripts/build-mcpb.sh` gained an
  optional 4th argument (helper path); the release `mcpb` job passes the
  helper extracted from the release archive and additionally verifies its
  signature.
- QA step 13 (signed build, Gatekeeper) is manual — needs the release
  environment secrets (Developer ID P12, App Store Connect API key).
