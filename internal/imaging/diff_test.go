package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// flatPNG builds a w x h solid-color PNG.
func flatPNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	p := img.Pix
	r, g, b, a := c.RGBA()
	rr, gg, bb, aa := byte(r>>8), byte(g>>8), byte(b>>8), byte(a>>8)
	for off := 0; off < len(p); off += 4 {
		p[off], p[off+1], p[off+2], p[off+3] = rr, gg, bb, aa
	}
	return encodePNG(t, img)
}

// blockPNG builds a solid image with one filled rectangle of a different
// color at (bx, by) of size bw x bh.
func blockPNG(t *testing.T, w, h, bx, by, bw, bh int, base, block color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	p := img.Pix
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := base
			if x >= bx && x < bx+bw && y >= by && y < by+bh {
				c = block
			}
			r, g, b, a := c.RGBA()
			off := (y*w + x) * 4
			p[off], p[off+1], p[off+2], p[off+3] = byte(r>>8), byte(g>>8), byte(b>>8), byte(a>>8)
		}
	}
	return encodePNG(t, img)
}

func TestDiffIdenticalBytesUnchanged(t *testing.T) {
	a := blockPNG(t, 10, 10, 2, 2, 4, 3, color.White, color.Black)
	res, err := Diff(a, a, DiffOptions{Tolerance: 8, ThresholdPercent: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatalf("identical bytes must be unchanged: %+v", res)
	}
	if res.WidthA != 10 || res.HeightB != 10 {
		t.Fatalf("dims = %+v", res)
	}
}

func TestDiffWithinToleranceUnchanged(t *testing.T) {
	a := flatPNG(t, 20, 20, color.RGBA{R: 100, G: 100, B: 100, A: 255})
	// One pixel off by 5 (within tolerance 8), one channel.
	b := flatPNG(t, 20, 20, color.RGBA{R: 105, G: 100, B: 100, A: 255})
	res, err := Diff(a, b, DiffOptions{Tolerance: 8, ThresholdPercent: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatalf("within tolerance must be unchanged: %+v", res)
	}
	if res.PercentChanged != 0 {
		t.Fatalf("percent = %v, want 0", res.PercentChanged)
	}
}

func TestDiffBlockBeyondTolerance(t *testing.T) {
	// 10x10 base, 4x2 block at (2,3) → 8 changed pixels = 8%.
	a := flatPNG(t, 10, 10, color.White)
	b := blockPNG(t, 10, 10, 2, 3, 4, 2, color.White, color.Black)
	res, err := Diff(a, b, DiffOptions{Tolerance: 8, ThresholdPercent: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatalf("block must be detected: %+v", res)
	}
	if res.PercentChanged != 8.0 {
		t.Fatalf("percent = %v, want 8", res.PercentChanged)
	}
	if res.Bounds == nil || res.Bounds.Min != (image.Point{2, 3}) || res.Bounds.Max != (image.Point{6, 5}) {
		t.Fatalf("bounds = %v, want (2,3)-(6,5)", res.Bounds)
	}
	detail := res.Detail(0.1)
	if !strings.Contains(detail, "8.00%") || !strings.Contains(detail, "(2,3)-(6,5)") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestDiffThresholdBehaviour(t *testing.T) {
	a := flatPNG(t, 10, 10, color.White)
	b := blockPNG(t, 10, 10, 2, 3, 4, 2, color.White, color.Black) // 8%

	// Threshold 20%: 8% < 20% → not changed, but percent still reported.
	res, err := Diff(a, b, DiffOptions{Tolerance: 8, ThresholdPercent: 20})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatalf("8%% < 20%% threshold must be unchanged: %+v", res)
	}
	if res.PercentChanged != 8.0 || res.Bounds == nil {
		t.Fatalf("percent/bounds still expected: %+v", res)
	}

	// Threshold 0: any difference counts.
	res, err = Diff(a, b, DiffOptions{Tolerance: 0, ThresholdPercent: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatalf("threshold 0 must count any difference: %+v", res)
	}

	// High threshold above the real share → unchanged.
	res, err = Diff(a, b, DiffOptions{Tolerance: 8, ThresholdPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatalf("100%% threshold must not be met by 8%%: %+v", res)
	}
}

func TestDiffSizeMismatch(t *testing.T) {
	a := flatPNG(t, 10, 10, color.White)
	b := flatPNG(t, 12, 10, color.White)
	res, err := Diff(a, b, DiffOptions{Tolerance: 8, ThresholdPercent: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.SizeMismatch {
		t.Fatalf("size mismatch must be changed: %+v", res)
	}
	if !strings.Contains(res.Detail(0.1), "10x10 vs 12x10") {
		t.Fatalf("detail = %q", res.Detail(0.1))
	}
}

func TestDiffCorruptPNG(t *testing.T) {
	if _, err := Diff([]byte("not a png"), []byte("also not"), DiffOptions{Tolerance: 8, ThresholdPercent: 0.1}); err == nil || !strings.Contains(err.Error(), "decode PNG a") {
		t.Fatalf("err = %v, want decode error for a", err)
	}
	good := flatPNG(t, 4, 4, color.White)
	if _, err := Diff(good, []byte("broken"), DiffOptions{Tolerance: 8, ThresholdPercent: 0.1}); err == nil || !strings.Contains(err.Error(), "decode PNG b") {
		t.Fatalf("err = %v, want decode error for b", err)
	}
}

func TestDiffBadOptions(t *testing.T) {
	a := flatPNG(t, 4, 4, color.White)
	if _, err := Diff(a, a, DiffOptions{Tolerance: 256, ThresholdPercent: 0.1}); err == nil {
		t.Fatal("want tolerance range error")
	}
	if _, err := Diff(a, a, DiffOptions{Tolerance: 8, ThresholdPercent: 101}); err == nil {
		t.Fatal("want threshold range error")
	}
}

func TestDiffZeroToleranceCountsEverything(t *testing.T) {
	a := flatPNG(t, 20, 20, color.RGBA{R: 100, G: 100, B: 100, A: 255})
	b := flatPNG(t, 20, 20, color.RGBA{R: 105, G: 100, B: 100, A: 255}) // 1 pixel-wide diff on every pixel
	res, err := Diff(a, b, DiffOptions{Tolerance: 0, ThresholdPercent: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.PercentChanged != 100 {
		t.Fatalf("zero tolerance must count the full diff: %+v", res)
	}
}
