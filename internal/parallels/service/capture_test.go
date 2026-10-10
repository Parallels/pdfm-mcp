package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureTestPNG is a small synthetic PNG the fakes hand to the service.
var captureTestPNG = mustSolidPNG(64, 48)

func mustSolidPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 64, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// captureWritingFake parses `capture <vm> --file <path>` out of the argv and
// writes content to the requested path, emulating prlctl.
func captureWritingFake(content []byte) func(exe string, args []string) (string, error) {
	return func(_ string, args []string) (string, error) {
		if len(args) != 4 || args[0] != "capture" || args[2] != "--file" {
			return "", fmt.Errorf("unexpected argv: %v", args)
		}
		if err := os.WriteFile(args[3], content, 0o600); err != nil {
			return "", err
		}
		return "", nil
	}
}

// leftoverCaptureDirs lists any pdfm-mcp-capture-* dirs still in the OS temp
// dir. It must be empty after every Capture call, success or failure.
func leftoverCaptureDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "pdfm-mcp-capture-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestCaptureSuccess(t *testing.T) {
	runner := &fakeRunner{fn: captureWritingFake(captureTestPNG)}
	svc := powerService(t, runner, "")

	res, err := svc.Capture(context.Background(), "guest-x")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res.PNG, captureTestPNG) {
		t.Fatal("returned bytes differ from the file prlctl wrote")
	}
	if res.CapturedAt.IsZero() {
		t.Fatal("CapturedAt not set")
	}

	// argv must be exactly: capture <vm> --file <path inside a temp dir>.
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" {
		t.Fatalf("exe = %q", exe)
	}
	if len(args) != 4 || args[0] != "capture" || args[1] != "guest-x" || args[2] != "--file" {
		t.Fatalf("args = %v", args)
	}
	file := args[3]
	if !strings.HasPrefix(filepath.Dir(file), os.TempDir()) || !strings.Contains(filepath.Base(filepath.Dir(file)), "pdfm-mcp-capture-") {
		t.Fatalf("file = %q, want <TempDir>/pdfm-mcp-capture-*/capture.png", file)
	}
	if filepath.Base(file) != "capture.png" {
		t.Fatalf("file base = %q", filepath.Base(file))
	}

	if left := leftoverCaptureDirs(t); len(left) > 0 {
		t.Fatalf("temp dirs left behind: %v", left)
	}
}

func TestCaptureCleanupOnError(t *testing.T) {
	cancelCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(200*time.Millisecond))
	defer cancel()

	cases := []struct {
		name   string
		runner *fakeRunner
		ctx    context.Context
		want   string
	}{
		{"runner error", &fakeRunner{defErr: errors.New("prlctl capture failed: VM not found")}, context.Background(), "VM not found"},
		{"no file written", &fakeRunner{fn: func(_ string, _ []string) (string, error) { return "", nil }}, context.Background(), "produced no file"},
		{"non-PNG file", &fakeRunner{fn: captureWritingFake([]byte("not a png at all"))}, context.Background(), "not a PNG"},
		{"empty file", &fakeRunner{fn: captureWritingFake(nil)}, context.Background(), "empty"},
		{"oversize file", &fakeRunner{fn: captureWritingFake(append([]byte{0x89, 'P', 'N', 'G'}, make([]byte, maxCaptureBytes+1)...))}, context.Background(), "larger than"},
		{"context cancelled", &fakeRunner{delay: 5 * time.Second}, cancelCtx, "deadline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := powerService(t, tc.runner, "")
			if _, err := svc.Capture(tc.ctx, "guest-x"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
			if left := leftoverCaptureDirs(t); len(left) > 0 {
				t.Fatalf("temp dirs left behind after %s: %v", tc.name, left)
			}
		})
	}
}

func TestCaptureInvalidRef(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	for _, bad := range []string{"", "   ", "bad\x00id", strings.Repeat("a", MaxVMRefLength+1)} {
		if _, err := svc.Capture(context.Background(), bad); err == nil {
			t.Fatalf("ref %q: want error", bad)
		}
	}
	if runner.callCount() != 0 {
		t.Fatalf("prlctl must not run on invalid ref: %d calls", runner.callCount())
	}
}

func TestCaptureDisabled(t *testing.T) {
	resetBetween(t)
	ok := mustInit(t,
		WithLogger(testLogger()),
		WithLookPath(fakeLookPath(map[string]string{})),
		WithRunner(&fakeRunner{}),
		WithFallbackDirs(),
	)
	if ok {
		t.Fatal("service should be disabled without the CLI")
	}
	if _, err := GetInstance().Capture(context.Background(), "guest"); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
}

func TestCaptureDoesNotInvalidateCache(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, args []string) (string, error) {
		if args[0] == "list" {
			return sampleVMsJSON, nil
		}
		return captureWritingFake(captureTestPNG)("", args)
	}}
	svc := powerService(t, runner, "")

	first, err := svc.ListVMs(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Refreshed {
		t.Fatal("first list must refresh")
	}

	if _, err := svc.Capture(context.Background(), vmOneID); err != nil {
		t.Fatal(err)
	}

	second, err := svc.ListVMs(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Refreshed {
		t.Fatal("capture must not invalidate the cache; second list was re-fetched")
	}
}
