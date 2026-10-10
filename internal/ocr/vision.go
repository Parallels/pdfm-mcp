package ocr

// Package-internal note: this file compiles the Objective-C Vision wrapper
// (vision.m, spec 022) via cgo. The product is macOS-only, so there is no
// non-darwin fallback.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unsafe"
)

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Vision -framework CoreGraphics -framework ImageIO -framework Foundation
#include <stdlib.h>
char *pdfm_vision_recognize(const unsigned char *data, int len,
                            int fast, const char *lang_csv,
                            char *errbuf, int errbuf_len);
*/
import "C"

const visionErrBufLen = 512

// visionRun performs one recognition; tests replace the native call with a
// canned runner.
type visionRun func(ctx context.Context, png []byte, fast bool, langs string) ([]byte, error)

// Vision is a Recognizer backed by Apple Vision compiled into the binary
// (spec 022): one in-process native call per image, no sidecar process.
//
// A call that hangs inside Vision cannot be killed (it is not a child
// process); the context timeout abandons it and returns an error, which may
// leak that one goroutine. This is the accepted trade-off of in-process OCR.
type Vision struct {
	run     visionRun
	fast    bool
	langs   string
	timeout time.Duration
	logger  *slog.Logger
}

// VisionOption configures NewVision.
type VisionOption func(*Vision)

// WithVisionRunner replaces the native call (tests only).
func WithVisionRunner(r visionRun) VisionOption { return func(v *Vision) { v.run = r } }

// WithVisionTimeout overrides the per-run timeout.
func WithVisionTimeout(d time.Duration) VisionOption { return func(v *Vision) { v.timeout = d } }

// WithVisionLogger sets the logger (default slog.Default()).
func WithVisionLogger(l *slog.Logger) VisionOption { return func(v *Vision) { v.logger = l } }

// WithVisionFast selects the fast (less accurate) recognition level.
func WithVisionFast() VisionOption { return func(v *Vision) { v.fast = true } }

// WithVisionLanguages restricts recognition to the given BCP-47 tags.
// Invalid tags make the first Recognize fail.
func WithVisionLanguages(tags ...string) VisionOption {
	return func(v *Vision) {
		if len(tags) > 0 {
			v.langs = strings.Join(tags, ",")
		}
	}
}

// NewVision builds the built-in Apple Vision recognizer.
func NewVision(opts ...VisionOption) *Vision {
	v := &Vision{run: nativeVisionRun, timeout: DefaultTimeout, logger: slog.Default()}
	for _, o := range opts {
		o(v)
	}
	return v
}

// validateLangs enforces the same bounds the sidecar CLI had.
func validateLangs(csv string) error {
	if csv == "" {
		return nil
	}
	tags := strings.Split(csv, ",")
	if len(tags) > maxLangs {
		return fmt.Errorf("too many OCR languages (max %d)", maxLangs)
	}
	for _, t := range tags {
		if len(t) > maxLangLen || !langRe.MatchString(t) {
			return fmt.Errorf("invalid OCR language tag %q", t)
		}
	}
	return nil
}

// Recognize runs Vision on png (PNG or JPEG bytes).
func (v *Vision) Recognize(ctx context.Context, png []byte) (Recognition, error) {
	if len(png) == 0 {
		return Recognition{}, errors.New("ocr: empty image")
	}
	if len(png) > MaxInputBytes {
		return Recognition{}, fmt.Errorf("ocr: image is larger than %d bytes", MaxInputBytes)
	}
	if err := validateLangs(v.langs); err != nil {
		return Recognition{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	start := time.Now()
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := v.run(runCtx, png, v.fast, v.langs)
		done <- result{out: out, err: err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			if ctxErr := runCtx.Err(); ctxErr != nil {
				return Recognition{}, fmt.Errorf("ocr stopped: %w", ctxErr)
			}
			return Recognition{}, fmt.Errorf("ocr failed: %w", r.err)
		}
		rec, err := Parse(r.out)
		if err != nil {
			return Recognition{}, err
		}
		// Counts and timings only: never the recognized text.
		v.logger.Info("ocr completed", "lines", len(rec.Lines), "image_bytes", len(png), "took", time.Since(start))
		return rec, nil
	case <-runCtx.Done():
		// The native call may still be running; it cannot be killed.
		return Recognition{}, fmt.Errorf("ocr stopped: %w", runCtx.Err())
	}
}

// nativeVisionRun is the real visionRun: one cgo call into Vision. ctx is
// part of the visionRun signature for testability; the native call itself
// cannot be cancelled.
func nativeVisionRun(_ context.Context, png []byte, fast bool, langs string) ([]byte, error) {
	var cdata *C.uchar
	if len(png) > 0 {
		cdata = (*C.uchar)(unsafe.Pointer(&png[0]))
	}
	var cfast C.int
	if fast {
		cfast = 1
	}
	var clang *C.char
	if langs != "" {
		clang = C.CString(langs)
		defer C.free(unsafe.Pointer(clang))
	}
	errbuf := make([]C.char, visionErrBufLen)
	out := C.pdfm_vision_recognize(cdata, C.int(len(png)), cfast, clang, &errbuf[0], C.int(len(errbuf)))
	if out == nil {
		msg := strings.TrimSpace(C.GoString(&errbuf[0]))
		if msg == "" {
			msg = "vision recognition failed"
		}
		return nil, errors.New(msg)
	}
	defer C.free(unsafe.Pointer(out))
	return []byte(C.GoString(out)), nil
}
