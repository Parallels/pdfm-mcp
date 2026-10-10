// Package ocr reads text from screenshots with Apple Vision compiled into
// the binary (cgo, spec 022). It has no knowledge of Parallels or MCP:
// callers hand it PNG bytes and get recognized lines with pixel boxes back.
//
// Privacy: recognized text and pixels are never logged by this package;
// only counts and timings are.
package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits and defaults.
const (
	// MaxLines bounds the number of recognized lines accepted per image.
	MaxLines = 5000
	// MaxTextChars bounds the length of one recognized line (in runes).
	MaxTextChars = 4096
	// MaxOutputBytes caps one recognition's JSON document.
	MaxOutputBytes = 4 << 20
	// MaxInputBytes caps the image handed to Vision.
	MaxInputBytes = 32 << 20
	// DefaultTimeout bounds one recognition run.
	DefaultTimeout = 30 * time.Second
	// MaxQueryLen bounds a find query or regex (bytes).
	MaxQueryLen = 1024
	// maxImageDim is a sanity bound on reported image dimensions.
	maxImageDim = 100000
	// maxLangs and maxLangLen bound the language list.
	maxLangs   = 16
	maxLangLen = 16
)

// Box is a pixel rectangle [x0, y0, x1, y1], origin top-left, x1/y1 exclusive
// edges (x0 <= x1, y0 <= y1).
type Box [4]int

// Intersects reports whether the two boxes overlap with positive area.
func (b Box) Intersects(o Box) bool {
	return b[0] < o[2] && o[0] < b[2] && b[1] < o[3] && o[1] < b[3]
}

// Center returns the integer center point of the box.
func (b Box) Center() (x, y int) {
	return (b[0] + b[2]) / 2, (b[1] + b[3]) / 2
}

// NewBox builds a validated region box from four values.
func NewBox(v []int) (Box, error) {
	if len(v) != 4 {
		return Box{}, fmt.Errorf("box needs exactly 4 integers [x0,y0,x1,y1], got %d", len(v))
	}
	b := Box{v[0], v[1], v[2], v[3]}
	for _, c := range b {
		if c < 0 || c > maxImageDim {
			return Box{}, fmt.Errorf("box coordinate %d out of range (0-%d)", c, maxImageDim)
		}
	}
	if b[0] >= b[2] || b[1] >= b[3] {
		return Box{}, errors.New("box must have x0 < x1 and y0 < y1")
	}
	return b, nil
}

// Line is one recognized line of text.
type Line struct {
	Text       string
	Confidence float64
	Box        Box
}

// Center returns the center point of the line's box.
func (l Line) Center() (x, y int) { return l.Box.Center() }

// Recognition is the result of recognizing one image.
type Recognition struct {
	Width  int
	Height int
	Lines  []Line
}

// Recognizer turns PNG bytes into recognized text lines.
type Recognizer interface {
	Recognize(ctx context.Context, png []byte) (Recognition, error)
}

// wire types for strict decoding of the helper's JSON.
type wireResult struct {
	Width  int        `json:"width"`
	Height int        `json:"height"`
	Lines  []wireLine `json:"lines"`
}

type wireLine struct {
	Text       string   `json:"text"`
	Confidence *float64 `json:"confidence"`
	Box        []int    `json:"box"`
}

// Parse strictly decodes and validates a recognition document: one JSON
// document, no unknown fields, bounded line count and text length, boxes
// inside the image. Lines are returned sorted top-to-bottom, then
// left-to-right. Errors never quote recognized text.
func Parse(data []byte) (Recognition, error) {
	if len(data) > MaxOutputBytes {
		return Recognition{}, fmt.Errorf("ocr output exceeds %d bytes", MaxOutputBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w wireResult
	if err := dec.Decode(&w); err != nil {
		return Recognition{}, fmt.Errorf("decode ocr output: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Recognition{}, errors.New("decode ocr output: trailing data after JSON document")
	}
	if w.Width < 1 || w.Width > maxImageDim || w.Height < 1 || w.Height > maxImageDim {
		return Recognition{}, fmt.Errorf("ocr output has invalid image size %dx%d", w.Width, w.Height)
	}
	if len(w.Lines) > MaxLines {
		return Recognition{}, fmt.Errorf("ocr output has %d lines, above the %d limit", len(w.Lines), MaxLines)
	}
	lines := make([]Line, 0, len(w.Lines))
	for i, wl := range w.Lines {
		if utf8.RuneCountInString(wl.Text) > MaxTextChars {
			return Recognition{}, fmt.Errorf("ocr line %d is longer than %d characters", i, MaxTextChars)
		}
		if !utf8.ValidString(wl.Text) {
			return Recognition{}, fmt.Errorf("ocr line %d is not valid UTF-8", i)
		}
		if wl.Confidence == nil || *wl.Confidence < 0 || *wl.Confidence > 1 {
			return Recognition{}, fmt.Errorf("ocr line %d has a missing or out-of-range confidence", i)
		}
		if len(wl.Box) != 4 {
			return Recognition{}, fmt.Errorf("ocr line %d box needs 4 integers", i)
		}
		b := Box{wl.Box[0], wl.Box[1], wl.Box[2], wl.Box[3]}
		if b[0] < 0 || b[1] < 0 || b[0] > b[2] || b[1] > b[3] || b[2] > w.Width || b[3] > w.Height {
			return Recognition{}, fmt.Errorf("ocr line %d box is outside the %dx%d image", i, w.Width, w.Height)
		}
		lines = append(lines, Line{Text: wl.Text, Confidence: *wl.Confidence, Box: b})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].Box[1] != lines[j].Box[1] {
			return lines[i].Box[1] < lines[j].Box[1]
		}
		return lines[i].Box[0] < lines[j].Box[0]
	})
	return Recognition{Width: w.Width, Height: w.Height, Lines: lines}, nil
}

// Join reconstructs readable text: lines are grouped into rows (a line
// joins the current row when its vertical center falls inside the row's
// first line), rows run top to bottom separated by newlines, and the lines
// of a row run left to right separated by a space.
func Join(lines []Line) string {
	if len(lines) == 0 {
		return ""
	}
	sorted := make([]Line, len(lines))
	copy(sorted, lines)
	sort.SliceStable(sorted, func(i, j int) bool {
		_, ci := sorted[i].Center()
		_, cj := sorted[j].Center()
		if ci != cj {
			return ci < cj
		}
		return sorted[i].Box[0] < sorted[j].Box[0]
	})

	var rows [][]Line
	var rowBox Box
	for _, l := range sorted {
		_, cy := l.Center()
		if len(rows) > 0 && cy >= rowBox[1] && cy <= rowBox[3] {
			rows[len(rows)-1] = append(rows[len(rows)-1], l)
			continue
		}
		rows = append(rows, []Line{l})
		rowBox = l.Box
	}

	var sb strings.Builder
	for i, row := range rows {
		sort.SliceStable(row, func(a, b int) bool { return row[a].Box[0] < row[b].Box[0] })
		if i > 0 {
			sb.WriteByte('\n')
		}
		for j, l := range row {
			if j > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(l.Text)
		}
	}
	return sb.String()
}

// FindOptions control Find and Filter.
type FindOptions struct {
	// Regex treats the query as an RE2 regular expression (case-sensitive
	// unless it starts with (?i)); otherwise a case-insensitive substring.
	Regex bool
	// MinConfidence drops lines below this confidence (0 keeps all).
	MinConfidence float64
	// Region, when non-nil, keeps only lines whose box intersects it.
	Region *Box
	// Nth, when > 0, returns only the nth (1-based) match.
	Nth int
}

// Filter applies the confidence and region options to lines and returns a
// new slice in the original order.
func Filter(lines []Line, opts FindOptions) []Line {
	out := make([]Line, 0, len(lines))
	for _, l := range lines {
		if l.Confidence < opts.MinConfidence {
			continue
		}
		if opts.Region != nil && !l.Box.Intersects(*opts.Region) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// Find returns the lines matching query, in input order. The query must be
// non-empty and at most MaxQueryLen bytes; a regex must compile under RE2
// (linear time, so no catastrophic backtracking). Nth selects one match.
func Find(lines []Line, query string, opts FindOptions) ([]Line, error) {
	if query == "" {
		return nil, errors.New("query must not be empty")
	}
	if len(query) > MaxQueryLen {
		return nil, fmt.Errorf("query is longer than %d bytes", MaxQueryLen)
	}
	if opts.Nth < 0 {
		return nil, errors.New("nth must not be negative")
	}
	var match func(string) bool
	if opts.Regex {
		re, err := regexp.Compile(query)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		match = re.MatchString
	} else {
		needle := strings.ToLower(query)
		match = func(s string) bool { return strings.Contains(strings.ToLower(s), needle) }
	}

	var out []Line
	for _, l := range Filter(lines, opts) {
		if !match(l.Text) {
			continue
		}
		out = append(out, l)
	}
	if opts.Nth > 0 {
		if opts.Nth > len(out) {
			return []Line{}, nil
		}
		return []Line{out[opts.Nth-1]}, nil
	}
	if out == nil {
		out = []Line{}
	}
	return out, nil
}

var langRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
