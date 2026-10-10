package imaging

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// solidPNG renders a w×h solid-color NRGBA image to PNG.
func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownscaleLandscape(t *testing.T) {
	src := solidPNG(t, 3000, 2000)
	res, err := Downscale(src, DefaultMaxDimension)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resized {
		t.Fatal("want resized")
	}
	// Long edge must be <= 1568 and aspect preserved within 1 px.
	if res.Width > DefaultMaxDimension || res.Height > DefaultMaxDimension {
		t.Fatalf("resized too large: %dx%d", res.Width, res.Height)
	}
	if want := 1568; res.Width < want-1 || res.Width > want {
		t.Fatalf("width = %d, want ~%d", res.Width, want)
	}
	if want := 1045; res.Height < want-1 || res.Height > want+1 {
		t.Fatalf("height = %d, want ~%d", res.Height, want)
	}
	if res.OriginalWidth != 3000 || res.OriginalHeight != 2000 {
		t.Fatalf("original = %dx%d", res.OriginalWidth, res.OriginalHeight)
	}
	// Output must be a valid PNG with the reported size.
	w, h, err := Dimensions(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if w != res.Width || h != res.Height {
		t.Fatalf("decoded %dx%d != reported %dx%d", w, h, res.Width, res.Height)
	}
	if len(res.Data) == len(src) {
		t.Fatal("output identical to input; resize did not run")
	}
}

func TestDownscalePortrait(t *testing.T) {
	src := solidPNG(t, 2000, 3000)
	res, err := Downscale(src, DefaultMaxDimension)
	if err != nil {
		t.Fatal(err)
	}
	if res.Height != 1568 {
		t.Fatalf("height = %d, want 1568 (portrait long edge)", res.Height)
	}
	if res.Width != 1045 && res.Width != 1044 && res.Width != 1046 {
		t.Fatalf("width = %d, want ~1045", res.Width)
	}
}

func TestDownscaleSmallInputUntouched(t *testing.T) {
	src := solidPNG(t, 800, 600)
	res, err := Downscale(src, DefaultMaxDimension)
	if err != nil {
		t.Fatal(err)
	}
	if res.Resized {
		t.Fatal("small image must not be resized")
	}
	if !bytes.Equal(res.Data, src) {
		t.Fatal("small image bytes must pass through untouched")
	}
	if res.Width != 800 || res.Height != 600 {
		t.Fatalf("dims = %dx%d", res.Width, res.Height)
	}
}

func TestDownscaleCustomMaxDimension(t *testing.T) {
	src := solidPNG(t, 2000, 1000)
	res, err := Downscale(src, 512)
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 512 || res.Height != 256 {
		t.Fatalf("resized = %dx%d, want 512x256", res.Width, res.Height)
	}
}

func TestDownscaleRejectsOutOfRangeMaxDim(t *testing.T) {
	src := solidPNG(t, 100, 100)
	for _, bad := range []int{0, MinMaxDimension - 1, MaxMaxDimension + 1, 1 << 20} {
		if _, err := Downscale(src, bad); err == nil {
			t.Fatalf("maxDim %d: want error", bad)
		}
	}
}

func TestDownscaleRejectsNonPNG(t *testing.T) {
	if _, err := Downscale([]byte("definitely not a PNG"), DefaultMaxDimension); err == nil {
		t.Fatal("want error for non-PNG input")
	}
}

// headerOnlyPNG crafts a signature + IHDR (huge dimensions) + IEND with
// valid CRCs but no pixel data. DecodeConfig must see the dimensions
// without decoding pixels, and Downscale must reject before a full decode.
func headerOnlyPNG(w, h int) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	writeChunk(&buf, "IHDR", func() []byte {
		d := make([]byte, 13)
		binary.BigEndian.PutUint32(d[0:4], uint32(w))
		binary.BigEndian.PutUint32(d[4:8], uint32(h))
		d[8] = 8  // bit depth
		d[9] = 6  // color type: RGBA
		d[10] = 0 // compression
		d[11] = 0 // filter
		d[12] = 0 // interlace
		return d
	}())
	writeChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func writeChunk(buf *bytes.Buffer, typ string, data []byte) {
	var be [4]byte
	binary.BigEndian.PutUint32(be[:], uint32(len(data)))
	buf.Write(be[:])
	typBytes := []byte(typ)
	buf.Write(typBytes)
	buf.Write(data)
	crc := crc32.ChecksumIEEE(typBytes)
	crc = crc32.Update(crc, crc32.IEEETable, data)
	binary.BigEndian.PutUint32(be[:], crc)
	buf.Write(be[:])
}

func TestDownscaleRejectsHugeHeaderWithoutDecoding(t *testing.T) {
	// 20000x20000 = 400 MP > 100 MP cap. There is no pixel data to decode;
	// the rejection must come from the header check.
	src := headerOnlyPNG(20000, 20000)
	if _, err := Downscale(src, DefaultMaxDimension); err == nil || !strings.Contains(err.Error(), "pixel") {
		t.Fatalf("err = %v, want pixel-limit rejection", err)
	}
	// Dimensions must still report the header size.
	w, h, err := Dimensions(src)
	if err != nil {
		t.Fatal(err)
	}
	if w != 20000 || h != 20000 {
		t.Fatalf("dims = %dx%d", w, h)
	}
}
