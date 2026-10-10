// Package imaging provides pure image helpers shared by tools. It has no
// knowledge of Parallels or MCP: functions take PNG bytes and return PNG
// bytes.
package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"

	"golang.org/x/image/draw"
)

// Limits.
const (
	// DefaultMaxDimension is the long-edge target of the default downscale.
	DefaultMaxDimension = 1568
	// MinMaxDimension is the smallest accepted max_dimension.
	MinMaxDimension = 256
	// MaxMaxDimension is the largest accepted max_dimension.
	MaxMaxDimension = 4096
	// maxPixels bounds the decoded INPUT as a decompression-bomb guard.
	maxPixels = 100_000_000
)

// Result describes the (possibly resized) image.
type Result struct {
	Data                          []byte
	Width, Height                 int
	OriginalWidth, OriginalHeight int
	Resized                       bool
}

// Downscale returns src unchanged (same bytes, Resized=false) when its long
// edge is already <= maxDim. Otherwise it decodes, scales to fit maxDim on
// the long edge preserving aspect ratio with CatmullRom, and re-encodes
// PNG. It never upscales.
func Downscale(src []byte, maxDim int) (Result, error) {
	if maxDim < MinMaxDimension || maxDim > MaxMaxDimension {
		return Result{}, fmt.Errorf("max dimension %d out of range (%d-%d)", maxDim, MinMaxDimension, MaxMaxDimension)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return Result{}, fmt.Errorf("decode PNG config: %w", err)
	}
	w, h := cfg.Width, cfg.Height
	if w > 0 && h > 0 && w*h > maxPixels {
		return Result{}, fmt.Errorf("image %dx%d exceeds the %d-pixel input limit", w, h, maxPixels)
	}
	longest := w
	if h > longest {
		longest = h
	}
	if longest <= maxDim {
		return Result{Data: src, Width: w, Height: h, OriginalWidth: w, OriginalHeight: h, Resized: false}, nil
	}

	srcImg, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return Result{}, fmt.Errorf("decode PNG: %w", err)
	}
	scale := float64(maxDim) / float64(longest)
	newW := max(1, int(math.Round(float64(w)*scale)))
	newH := max(1, int(math.Round(float64(h)*scale)))

	dst := image.NewNRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), srcImg, srcImg.Bounds(), draw.Src, nil)

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return Result{}, fmt.Errorf("encode PNG: %w", err)
	}
	return Result{
		Data:           buf.Bytes(),
		Width:          newW,
		Height:         newH,
		OriginalWidth:  w,
		OriginalHeight: h,
		Resized:        true,
	}, nil
}

// Dimensions reads the size from the PNG header without decoding pixels.
func Dimensions(src []byte) (int, int, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return 0, 0, fmt.Errorf("decode PNG config: %w", err)
	}
	return cfg.Width, cfg.Height, nil
}
