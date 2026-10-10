# QA — Parallels VM screen capture (spec 004)

## What this task delivers

- `parallels_vm_capture` tool (both modes): runs `prlctl capture <vm> --file
  <generated temp path>`, reads the PNG into memory, removes the private temp
  directory on every path, optionally downscales (long edge ≤ 1568 px by
  default, `max_dimension` 256–4096, `full_resolution` for native), and
  returns a native MCP **image content block** plus a short text metadata
  block. Structured output carries metadata only — never the image.
- `internal/imaging` (pure PNG helpers: `Downscale`, `Dimensions`) backed by
  `golang.org/x/image/draw` CatmullRom.
- Bounded memory: 32 MB read cap, 100 MP decode cap, 10 MB result cap; 30 s
  capture timeout.
- `docs/specs/004-parallels-vm-capture.md` (design) + this QA spec.

**Safety/privacy note.** Capturing is read-only for the VM, but a screenshot
is sensitive data: it leaves the machine to whichever model provider the
MCP client is connected to. Do not capture a VM that is displaying
credentials, tokens, customer data or confidential source while connected
to an external AI service. No screenshot ever persists on disk — the temp
directory is removed on every path.

## Prerequisites

- Same host as the QA 002/003 walkthrough (Parallels Desktop, `prlctl` on
  PATH), Go toolchain.
- **A running VM** for the positive steps (a stopped or suspended VM cannot
  be captured; prlctl refuses and the tool surfaces its error).
- An MCP client that renders images (the Inspector, or Claude Desktop /
  Claude Code).

Build once for all steps:

```sh
cd pdfm-mcp
go build -o bin/pdfm-mcp ./cmd/pdfm-mcp
```

## Step-by-step

### 1. Automated suites (run these first)

```sh
make check       # fmt, vet, lint, CGO_ENABLED=1 go test -race ./...
make test-e2e    # gating test asserts parallels_vm_capture is LISTED in both
                 # modes; no capture is ever executed
PDFM_MCP_E2E_CAPTURE_VM=<running-vm-id> make test-e2e   # opt-in: real capture,
                 # asserts an image block with decodable PNG data
```

Expected: all green. Unit suites cover resizing math, temp-dir cleanup on
every error path, mode contracts, and the wire content types.

### 2. Fleet mode — contract and a capture

```sh
./bin/pdfm-mcp        # no --vm-id
```

1. `tools/list` → `parallels_vm_capture` present; `inputSchema.required`
   contains `vm_id`; annotation `readOnlyHint: true`.
2. `parallels_vm_capture` `{}` → schema validation error (missing `vm_id`).
3. `parallels_vm_capture` `{"vm_id": "<running-vm>"}` →
   - `content[0]` = text: "Screenshot of VM …: WxH px (native OWxOH), N bytes, PNG."
   - `content[1]` = image block, `mimeType: "image/png"`, valid PNG.
   - Default: long edge ≤ 1568 px, `resized: true` (for large native screens).
4. `{"vm_id": "<running-vm>", "max_dimension": 512}` → long edge 512 px.
5. `{"vm_id": "<running-vm>", "full_resolution": true}` → native
   dimensions, `resized: false`.
6. `max_dimension: 255` and `4097` → tool errors, no prlctl run.

### 3. VM mode — target contract

```sh
./bin/pdfm-mcp --vm-id <running-vm>
```

1. `parallels_vm_capture` `{}` → captures the configured VM;
   `inputSchema.required` does **not** contain `vm_id`.
2. `{"vm_id": "<other-vm>"}` → tool error "vm_id must be omitted"; nothing
   runs.

### 4. Error paths (VM state unchanged)

1. `{"vm_id": "<stopped-vm>"}` → tool error from prlctl; no image block.
   On Parallels 27 a stopped VM surfaces
   `PrlDevDisplay_ConnectToVm: PrlJob_Wait: PRL_ERR_IO_STOPPED`
   (prlctl prints `Capture the VM screen...` to stdout, exit 255).
2. `{"vm_id": "no-such-vm"}` → prlctl "not found"-style error.

### 5. Temp dir hygiene

```sh
ls "$TMPDIR" | grep pdfm-mcp-capture   # run after steps 2-4, including failures
```

Expected: **no output** (empty). Every call — success, error, timeout —
removes its `pdfm-mcp-capture-*` directory.

### 6. Client rendering (acceptance)

1. Point the Inspector (or Claude Desktop) at the server, call the tool with
   a running VM.
2. The client renders the screenshot; ask Claude to describe what is on the
   screen (e.g. "what window is open?"). It should describe real UI detail,
   confirming the image — not base64 text — reaches the model.

## Checklist

| # | Check | Where |
|---|---|---|
| 1 | Listed in both modes, absent when module disabled | e2e + protocol tests |
| 2 | Fleet: `vm_id` required; VM: configured target, explicit rejected | protocol + unit tests, steps 2–3 |
| 3 | content[0] text, content[1] image/png with valid PNG | protocol test + step 2 |
| 4 | Default 1568 px downscale, aspect preserved | imaging + tool tests |
| 5 | `max_dimension` 256–4096 enforced; `full_resolution` byte-for-byte | unit tests + step 2 |
| 6 | Temp dir removed on every path | service tests + step 5 |
| 7 | 32 MB / 100 MP / 10 MB caps | service + imaging + tool tests |
| 8 | Stopped/unknown VM → prlctl error, no image | step 4 |
| 9 | Cache untouched by capture | service test |
| 10 | No `["null","array"]` unions in the output schema | protocol test |
| 11 | Image bytes never in structured output, never logged | protocol test + code |
| 12 | Client renders and model describes the screen | step 6 (manual) |

## Known follow-ups

- Region/window capture, multi-monitor behaviour (verify with
  `prlctl capture --help` on the installed version).
- JPEG/WebP for bandwidth-sensitive clients.
- `resource_link` for very large captures.
