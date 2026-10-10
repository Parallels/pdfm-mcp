package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	"golang.org/x/image/draw"
)

// DiffOptions tunes a per-pixel comparison.
type DiffOptions struct {
	// Tolerance is the per-channel difference (0-255) ignored as noise.
	// Zero means every difference counts.
	Tolerance int
	// ThresholdPercent is the percent of pixels that must differ for the
	// images to count as changed (0-100). Zero means any difference counts.
	ThresholdPercent float64
}

// DiffResult is the outcome of a Diff call.
type DiffResult struct {
	// Changed is true when the images count as different (threshold reached
	// or size mismatch).
	Changed bool
	// PercentChanged is the share of differing pixels in [0,100]. It is 0
	// when SizeMismatch is set (the pixel share is meaningless).
	PercentChanged float64
	// Bounds is the bounding box of the differing pixels; nil when there is
	// no change.
	Bounds *image.Rectangle
	// SizeMismatch is true when the two images have different dimensions.
	SizeMismatch    bool
	WidthA, HeightA int
	WidthB, HeightB int
}

// Diff compares two PNG images pixel by pixel. Each channel (R, G, B, A)
// must differ by more than opts.Tolerance for a pixel to count as changed.
// The images count as changed when the share of changed pixels is at least
// opts.ThresholdPercent, or when their dimensions differ.
func Diff(a, b []byte, opts DiffOptions) (DiffResult, error) {
	if opts.Tolerance < 0 || opts.Tolerance > 255 {
		return DiffResult{}, fmt.Errorf("tolerance %d out of range (0-255)", opts.Tolerance)
	}
	if opts.ThresholdPercent < 0 || opts.ThresholdPercent > 100 {
		return DiffResult{}, fmt.Errorf("threshold_percent %v out of range (0-100)", opts.ThresholdPercent)
	}
	cfgA, err := png.DecodeConfig(bytes.NewReader(a))
	if err != nil {
		return DiffResult{}, fmt.Errorf("decode PNG a: %w", err)
	}
	cfgB, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return DiffResult{}, fmt.Errorf("decode PNG b: %w", err)
	}
	res := DiffResult{
		WidthA: cfgA.Width, HeightA: cfgA.Height,
		WidthB: cfgB.Width, HeightB: cfgB.Height,
	}
	// Decompression-bomb guard before decoding pixels.
	if cfgA.Width > 0 && cfgA.Height > 0 && cfgA.Width*cfgA.Height > maxPixels {
		return DiffResult{}, fmt.Errorf("image a %dx%d exceeds the %d-pixel limit", cfgA.Width, cfgA.Height, maxPixels)
	}
	if cfgB.Width > 0 && cfgB.Height > 0 && cfgB.Width*cfgB.Height > maxPixels {
		return DiffResult{}, fmt.Errorf("image b %dx%d exceeds the %d-pixel limit", cfgB.Width, cfgB.Height, maxPixels)
	}

	if cfgA.Width != cfgB.Width || cfgA.Height != cfgB.Height {
		res.SizeMismatch = true
		res.Changed = true
		return res, nil
	}

	// Fast path: identical bytes are identical pixels.
	if bytes.Equal(a, b) {
		return res, nil
	}

	ia, err := decodeNRGBA(a)
	if err != nil {
		return DiffResult{}, fmt.Errorf("decode PNG a: %w", err)
	}
	ib, err := decodeNRGBA(b)
	if err != nil {
		return DiffResult{}, fmt.Errorf("decode PNG b: %w", err)
	}

	w, h := cfgA.Width, cfgA.Height
	total := w * h
	changed := 0
	// Bounds accumulate as [minX, minY, maxX, maxY].
	minX, minY := w, h
	maxX, maxY := -1, -1
	for y := 0; y < h; y++ {
		rowA := ia.Pix[y*ia.Stride : (y+1)*ia.Stride]
		rowB := ib.Pix[y*ib.Stride : (y+1)*ib.Stride]
		for x := 0; x < w; x++ {
			off := x * 4
			if diff := abs4(int(rowA[off]) - int(rowB[off])); diff > opts.Tolerance {
				changed++
				addBounds(&minX, &minY, &maxX, &maxY, x, y)
				continue
			}
			if diff := abs4(int(rowA[off+1]) - int(rowB[off+1])); diff > opts.Tolerance {
				changed++
				addBounds(&minX, &minY, &maxX, &maxY, x, y)
				continue
			}
			if diff := abs4(int(rowA[off+2]) - int(rowB[off+2])); diff > opts.Tolerance {
				changed++
				addBounds(&minX, &minY, &maxX, &maxY, x, y)
				continue
			}
			if diff := abs4(int(rowA[off+3]) - int(rowB[off+3])); diff > opts.Tolerance {
				changed++
				addBounds(&minX, &minY, &maxX, &maxY, x, y)
			}
		}
	}

	res.PercentChanged = float64(changed) / float64(total) * 100
	if changed > 0 {
		bounds := image.Rect(minX, minY, maxX+1, maxY+1)
		res.Bounds = &bounds
		// A zero threshold means "any difference counts"; with a positive
		// threshold the share must reach it. No changed pixels is never a
		// change, even at threshold 0.
		res.Changed = res.PercentChanged >= opts.ThresholdPercent
	}
	return res, nil
}

// Detail renders a human/model-readable summary of the diff.
func (r DiffResult) Detail(threshold float64) string {
	if r.SizeMismatch {
		return fmt.Sprintf("dimensions differ (%dx%d vs %dx%d)", r.WidthA, r.HeightA, r.WidthB, r.HeightB)
	}
	detail := fmt.Sprintf("%.2f%% of pixels changed (threshold %g%%)", r.PercentChanged, threshold)
	if r.Bounds != nil {
		detail += fmt.Sprintf(", bounds (%d,%d)-(%d,%d)", r.Bounds.Min.X, r.Bounds.Min.Y, r.Bounds.Max.X, r.Bounds.Max.Y)
	}
	return detail
}

func decodeNRGBA(src []byte) (*image.NRGBA, error) {
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	dst := image.NewNRGBA(b)
	draw.Draw(dst, b, img, b.Min, draw.Src)
	return dst, nil
}

func abs4(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func addBounds(minX, minY, maxX, maxY *int, x, y int) {
	if x < *minX {
		*minX = x
	}
	if y < *minY {
		*minY = y
	}
	if x > *maxX {
		*maxX = x
	}
	if y > *maxY {
		*maxY = y
	}
}
