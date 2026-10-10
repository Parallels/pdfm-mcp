package tools_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/tools"
)

// capturePNG renders a w×h gradient PNG (compresses small, decodes fast).
func capturePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// noisePNG renders an incompressible w×h PNG (random pixels stay large
// after PNG compression) — used to trip the result-size cap.
func noisePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(42))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Intn(256))
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func dimensionsOf(data []byte) (int, int, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// captureFn emulates prlctl: it parses `capture <vm> --file <path>` and
// writes png to the requested path.
func captureFn(png []byte) func(exe string, args []string) (string, error) {
	return func(_ string, args []string) (string, error) {
		if len(args) != 4 || args[0] != "capture" || args[2] != "--file" {
			return "", nil // let the service report the failure
		}
		if err := writeFile(args[3], png); err != nil {
			return "", err
		}
		return "", nil
	}
}

func TestCaptureHandlerModes(t *testing.T) {
	src := capturePNG(t, 2000, 1000)
	cases := []struct {
		name string
		vmID string // configured target
		arg  string // explicit vm_id argument
		want string // expected argv target; "" = call must fail
	}{
		{"vm-mode uses configured target", vmOneID, "", vmOneID},
		{"vm-mode rejects explicit vm_id", vmOneID, vmTwoID, ""},
		{"fleet requires vm_id", "", "", ""},
		{"fleet uses given vm_id", "", "guest-x", "guest-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{fn: captureFn(src)}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, tc.vmID)
			res, out, err := tools.VMCapture(context.Background(), nil, tools.CaptureInput{VMID: tc.arg})
			if tc.want == "" {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.VMID != tc.want {
				t.Fatalf("out.VMID = %q, want %q", out.VMID, tc.want)
			}
			if runner.count() != 1 {
				t.Fatalf("runner calls = %d, want 1", runner.count())
			}
			// Result content: text first, image second.
			if res == nil || len(res.Content) != 2 {
				t.Fatalf("res.Content = %+v", res.Content)
			}
			if _, ok := res.Content[0].(*mcp.TextContent); !ok {
				t.Fatalf("content[0] = %T, want TextContent", res.Content[0])
			}
			img, ok := res.Content[1].(*mcp.ImageContent)
			if !ok {
				t.Fatalf("content[1] = %T, want ImageContent", res.Content[1])
			}
			if img.MIMEType != "image/png" {
				t.Fatalf("mimeType = %q", img.MIMEType)
			}
			// Default downscale: 2000x1000 -> long edge 1568.
			w, h, err := dimensionsOf(img.Data)
			if err != nil {
				t.Fatal(err)
			}
			if out.Width != w || out.Height != h {
				t.Fatalf("out dims %dx%d != data %dx%d", out.Width, out.Height, w, h)
			}
			if w != 1568 || h != 784 {
				t.Fatalf("default downscale = %dx%d, want 1568x784", w, h)
			}
			if !out.Resized {
				t.Fatal("resized = false, want true")
			}
		})
	}
}

func TestCaptureMaxDimensionValidation(t *testing.T) {
	src := capturePNG(t, 2000, 1000)
	for _, bad := range []int{255, 4097, -1} {
		runner := &testRunner{fn: captureFn(src)}
		initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
		if _, _, err := tools.VMCapture(context.Background(), nil, tools.CaptureInput{VMID: "g", MaxDimension: bad}); err == nil {
			t.Fatalf("max_dimension %d: want error", bad)
		}
		if runner.count() != 0 {
			t.Fatalf("prlctl must not run for invalid max_dimension: %d calls", runner.count())
		}
	}

	// 512 is honoured: 2000x1000 -> 512x256.
	runner := &testRunner{fn: captureFn(src)}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, out, err := tools.VMCapture(context.Background(), nil, tools.CaptureInput{VMID: "g", MaxDimension: 512})
	if err != nil {
		t.Fatal(err)
	}
	if out.Width != 512 || out.Height != 256 {
		t.Fatalf("downscaled = %dx%d, want 512x256", out.Width, out.Height)
	}
}

func TestCaptureFullResolution(t *testing.T) {
	src := capturePNG(t, 1200, 800) // smaller than 1568: passes through anyway
	runner := &testRunner{fn: captureFn(src)}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	res, out, err := tools.VMCapture(context.Background(), nil, tools.CaptureInput{VMID: "g", FullResolution: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Resized {
		t.Fatal("resized = true, want false")
	}
	img, _ := res.Content[1].(*mcp.ImageContent)
	if !bytes.Equal(img.Data, src) {
		t.Fatal("full_resolution must return the original bytes")
	}
	if out.Width != 1200 || out.Height != 800 || out.OriginalWidth != 1200 || out.OriginalHeight != 800 {
		t.Fatalf("dims = %dx%d (native %dx%d)", out.Width, out.Height, out.OriginalWidth, out.OriginalHeight)
	}
}

func TestCaptureResultSizeCap(t *testing.T) {
	// 2000x2000 of random noise: incompressible, lands between the service
	// read cap (32 MB) and nowhere near the downscale output. full_resolution
	// keeps it whole, so the 10 MB result cap must trip.
	big := noisePNG(t, 2000, 2000)
	if len(big) < 10<<20 {
		t.Fatalf("test PNG too small to exercise the cap: %d bytes", len(big))
	}
	runner := &testRunner{fn: captureFn(big)}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	if _, _, err := tools.VMCapture(context.Background(), nil, tools.CaptureInput{VMID: "g", FullResolution: true}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want result-size cap error", err)
	}
}

func TestParallelsCaptureProtocol(t *testing.T) {
	src := capturePNG(t, 2000, 1000)
	runner := &testRunner{fn: captureFn(src)}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	names := toolNames(t, srv)
	if !names["parallels_vm_capture"] {
		t.Fatalf("parallels_vm_capture not listed: %v", names)
	}

	// Fleet mode: vm_id is required; output schema stays portable.
	required, ok := wireInputRequired(t, srv, "parallels_vm_capture")
	if !ok || !required {
		t.Fatalf("fleet mode: vm_id must be required (present=%v required=%v)", ok, required)
	}
	for _, tl := range listTools(t, srv) {
		if tl.Name != "parallels_vm_capture" {
			continue
		}
		if schema, ok := tl.OutputSchema.(map[string]any); ok {
			if bad := nullArrayUnionPaths(schema, "outputSchema"); len(bad) > 0 {
				t.Fatalf("non-portable output schema: %v", bad)
			}
		}
	}

	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "capture-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_capture", Arguments: map[string]any{"vm_id": "guest-x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	if len(res.Content) != 2 {
		t.Fatalf("content len = %d, want 2", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "guest-x") {
		t.Fatalf("content[0] = %+v", res.Content[0])
	}
	img, ok := res.Content[1].(*mcp.ImageContent)
	if !ok {
		t.Fatalf("content[1] = %T, want ImageContent", res.Content[1])
	}
	if img.MIMEType != "image/png" {
		t.Fatalf("mimeType = %q", img.MIMEType)
	}
	if w, h, err := dimensionsOf(img.Data); err != nil || w != 1568 || h != 784 {
		t.Fatalf("image data %dx%d (err=%v), want 1568x784", w, h, err)
	}

	// Structured content carries metadata only — never the image bytes.
	sc, _ := res.StructuredContent.(map[string]any)
	if sc == nil {
		t.Fatal("no structured content")
	}
	for key := range sc {
		if key == "data" || key == "png" || key == "image" {
			t.Fatalf("structured content must not carry the image, found key %q", key)
		}
	}
	if sc["width"] != float64(1568) || sc["height"] != float64(784) {
		t.Fatalf("structured dims = %v x %v", sc["width"], sc["height"])
	}
	if sc["original_width"] != float64(2000) || sc["original_height"] != float64(1000) {
		t.Fatalf("structured native dims = %v x %v", sc["original_width"], sc["original_height"])
	}
}

func TestParallelsCaptureVMModeProtocol(t *testing.T) {
	src := capturePNG(t, 1200, 800)
	runner := &testRunner{fn: captureFn(src)}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testConfig()
	cfg.VMID = vmOneID
	srv := server.New(logger, cfg)

	if required, ok := wireInputRequired(t, srv, "parallels_vm_capture"); !ok || required {
		t.Fatalf("VM mode: vm_id must not be required (present=%v required=%v)", ok, required)
	}

	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "capture-vmmode-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	// No arguments: targets the configured VM, native size (small image).
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_capture", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" || !slices.Equal(args[:2], []string{"capture", vmOneID}) {
		t.Fatalf("prlctl call = %s %v", exe, args)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	if sc["vm_id"] != vmOneID || sc["resized"] != false {
		t.Fatalf("structured = %v", sc)
	}

	// An explicit vm_id must be rejected in VM mode.
	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_capture", Arguments: map[string]any{"vm_id": vmTwoID}})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatalf("explicit vm_id must be rejected in VM mode, got %+v", bad.StructuredContent)
	}
}
