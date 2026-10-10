package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/imaging"
	"github.com/cjlapao/pdfm-mcp/internal/ocr"
	"github.com/cjlapao/pdfm-mcp/internal/orchestrator"
	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

const descVMObserve = "Reads the text on the VM's screen: the screen's text (the default), what changed since the previous observation, or where a piece of text is. It takes a screenshot, downscales it exactly like parallels_vm_capture (default 1568 px long edge) and runs OCR locally with Apple Vision built into this binary (nothing leaves this machine from this tool). mode text returns the recognized lines plus the joined text; mode changes returns the lines added and removed since the previous observation of this VM (the first call reports every line as added with previous_observed false; every observe call, in any mode, refreshes the baseline); mode find returns the lines matching query (case-insensitive substring, or an RE2 regex) with a center point to aim at. Optional region [x0,y0,x1,y1] (screenshot pixels), limit (default 50, max 500), min_confidence (0-1, default 0.3). All coordinates are [x0,y0,x1,y1] / [x,y] in screenshot pixels, origin top-left; multiply by original_width/width to get native pixels. No image is returned. The OUTPUT text still reaches the model provider: screens can contain secrets, so do not observe a VM showing credentials or confidential data while connected to an external AI service. The VM must be running."

// Observe limits and defaults.
const (
	observeModeText       = "text"
	observeModeChanges    = "changes"
	observeModeFind       = "find"
	defaultMinConfidence  = 0.3
	defaultObserveLimit   = 50
	maxObserveLimit       = 500
	maxObserveTextBytes   = 256 << 10
	observeScalePrecision = 10000
	// Changes baseline store bounds (spec 022).
	observeHistoryMaxVMs = 16
	observeHistoryTTL    = time.Hour
)

// ObserveInput is the argument schema of parallels_vm_observe.
type ObserveInput struct {
	VMID          string  `json:"vm_id,omitempty" jsonschema:"VM id or name to observe"`
	Mode          string  `json:"mode,omitempty" jsonschema:"text (default): all recognized lines; changes: lines added/removed since the previous observation; find: lines matching query"`
	Query         string  `json:"query,omitempty" jsonschema:"find mode only (required): text to look for, case-insensitive substring (max 1024 bytes)"`
	Regex         bool    `json:"regex,omitempty" jsonschema:"find mode only: treat query as an RE2 regular expression (case-sensitive unless it starts with (?i))"`
	Region        []int   `json:"region,omitempty" jsonschema:"only lines intersecting [x0,y0,x1,y1] in screenshot pixels (exactly 4 integers, x0<x1, y0<y1)"`
	MinConfidence float64 `json:"min_confidence,omitempty" jsonschema:"drop lines below this confidence, 0-1 (default 0.3; use 0.001 to keep everything)"`
	Limit         int     `json:"limit,omitempty" jsonschema:"maximum lines or matches returned (1-500, default 50)"`
	MaxDimension  int     `json:"max_dimension,omitempty" jsonschema:"longest edge in pixels the screenshot is downscaled to before OCR (256-4096, default 1568); coordinates refer to this image"`
}

// ObserveItem is one recognized line or match.
type ObserveItem struct {
	Text       string  `json:"text" jsonschema:"recognized text"`
	Confidence float64 `json:"confidence" jsonschema:"recognition confidence 0-1"`
	Box        []int   `json:"box" jsonschema:"[x0,y0,x1,y1] in screenshot pixels, origin top-left"`
	Center     []int   `json:"center" jsonschema:"[x,y] center of box in screenshot pixels"`
}

// ObserveOutput is the structured result. It never contains image data.
type ObserveOutput struct {
	VMID             string        `json:"vm_id" jsonschema:"the VM id or name that was observed"`
	Mode             string        `json:"mode" jsonschema:"text, changes or find"`
	Width            int           `json:"width" jsonschema:"screenshot width in pixels that coordinates refer to"`
	Height           int           `json:"height" jsonschema:"screenshot height in pixels that coordinates refer to"`
	OriginalWidth    int           `json:"original_width" jsonschema:"native screen width in pixels"`
	OriginalHeight   int           `json:"original_height" jsonschema:"native screen height in pixels"`
	Scale            float64       `json:"scale" jsonschema:"width / original_width (1 when not downscaled); native = screenshot pixels / scale"`
	Text             string        `json:"text,omitempty" jsonschema:"text mode: recognized lines joined into reading order"`
	Lines            []ObserveItem `json:"lines" jsonschema:"text mode: recognized lines (empty otherwise)"`
	Matches          []ObserveItem `json:"matches" jsonschema:"find mode: matching lines (empty otherwise)"`
	Added            []ObserveItem `json:"added" jsonschema:"changes mode: lines present now but absent in the previous observation"`
	Removed          []ObserveItem `json:"removed" jsonschema:"changes mode: lines present before but absent now"`
	PreviousObserved *bool         `json:"previous_observed,omitempty" jsonschema:"changes mode: whether a previous observation existed for this VM"`
	Count            int           `json:"count" jsonschema:"number of lines, matches or (in changes mode) added+removed entries returned"`
	Truncated        bool          `json:"truncated" jsonschema:"true when more results existed than limit or the text cap allowed"`
}

// observeHistory keeps the last full-screen recognition per VM so changes
// mode can diff against it (spec 022). It is process-wide, so the MCP tool
// and the sequence-step handler share one baseline: "previous observation"
// means the previous observe call of any mode for that VM. Bounded: maxVMs
// entries, TTL-evicted, LRU on overflow.
type observeHistoryStore struct {
	mu     sync.Mutex
	maxVMs int
	ttl    time.Duration
	byVM   map[string][]ocr.Line
	at     map[string]time.Time
	order  []string
}

var observeHistory = &observeHistoryStore{
	maxVMs: observeHistoryMaxVMs,
	ttl:    observeHistoryTTL,
	byVM:   map[string][]ocr.Line{},
	at:     map[string]time.Time{},
}

// get returns the stored baseline for vm.
func (h *observeHistoryStore) get(vm string) ([]ocr.Line, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.evictLocked()
	lines, ok := h.byVM[vm]
	return lines, ok
}

// set stores the new baseline for vm (LRU).
func (h *observeHistoryStore) set(vm string, lines []ocr.Line) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.evictLocked()
	for i, id := range h.order {
		if id == vm {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
	h.order = append(h.order, vm)
	h.byVM[vm] = lines
	h.at[vm] = time.Now()
}

func (h *observeHistoryStore) evictLocked() {
	now := time.Now()
	for vm, ts := range h.at {
		if now.Sub(ts) > h.ttl {
			delete(h.byVM, vm)
			delete(h.at, vm)
		}
	}
	kept := h.order[:0]
	for _, vm := range h.order {
		if _, ok := h.byVM[vm]; ok {
			kept = append(kept, vm)
		}
	}
	for len(kept) > h.maxVMs {
		delete(h.byVM, kept[0])
		delete(h.at, kept[0])
		kept = kept[1:]
	}
	h.order = kept
}

// diffLines returns the lines added to and removed from cur relative to prev,
// matched by exact line text (a line that moved to another position counts
// as removed plus added, which is the visible change). Input order kept.
func diffLines(prev, cur []ocr.Line) (added, removed []ocr.Line) {
	prevCount := make(map[string]int, len(prev))
	for _, l := range prev {
		prevCount[l.Text]++
	}
	curCount := make(map[string]int, len(cur))
	for _, l := range cur {
		curCount[l.Text]++
	}
	for _, l := range cur {
		if prevCount[l.Text] == 0 {
			added = append(added, l)
		} else {
			prevCount[l.Text]--
		}
	}
	for _, l := range prev {
		if curCount[l.Text] == 0 {
			removed = append(removed, l)
		} else {
			curCount[l.Text]--
		}
	}
	return added, removed
}

type observeParams struct {
	mode   string
	query  string
	opts   ocr.FindOptions
	limit  int
	maxDim int
}

func validateObserve(in ObserveInput) (observeParams, error) {
	p := observeParams{mode: observeModeText, limit: defaultObserveLimit, maxDim: imaging.DefaultMaxDimension}
	switch in.Mode {
	case "", observeModeText:
	case observeModeChanges:
		p.mode = observeModeChanges
	case observeModeFind:
		p.mode = observeModeFind
	default:
		return p, fmt.Errorf("mode %q is invalid (want %s, %s or %s)", in.Mode, observeModeText, observeModeChanges, observeModeFind)
	}
	if p.mode == observeModeFind {
		if in.Query == "" {
			return p, errors.New("query is required in find mode")
		}
		if len(in.Query) > ocr.MaxQueryLen {
			return p, fmt.Errorf("query is longer than %d bytes", ocr.MaxQueryLen)
		}
		p.query = in.Query
		p.opts.Regex = in.Regex
	} else if in.Query != "" || in.Regex {
		return p, errors.New("query and regex are only valid in find mode")
	}
	if len(in.Region) != 0 {
		b, err := ocr.NewBox(in.Region)
		if err != nil {
			return p, fmt.Errorf("region: %w", err)
		}
		p.opts.Region = &b
	}
	if in.MinConfidence < 0 || in.MinConfidence > 1 || math.IsNaN(in.MinConfidence) {
		return p, errors.New("min_confidence must be between 0 and 1")
	}
	p.opts.MinConfidence = defaultMinConfidence
	if in.MinConfidence != 0 {
		p.opts.MinConfidence = in.MinConfidence
	}
	if in.Limit != 0 {
		if in.Limit < 1 || in.Limit > maxObserveLimit {
			return p, fmt.Errorf("limit %d out of range (1-%d)", in.Limit, maxObserveLimit)
		}
		p.limit = in.Limit
	}
	if in.MaxDimension != 0 {
		if in.MaxDimension < imaging.MinMaxDimension || in.MaxDimension > imaging.MaxMaxDimension {
			return p, fmt.Errorf("max_dimension %d out of range (%d-%d)", in.MaxDimension, imaging.MinMaxDimension, imaging.MaxMaxDimension)
		}
		p.maxDim = in.MaxDimension
	}
	return p, nil
}

func toObserveItems(lines []ocr.Line) []ObserveItem {
	items := make([]ObserveItem, 0, len(lines))
	for _, l := range lines {
		cx, cy := l.Center()
		items = append(items, ObserveItem{
			Text:       l.Text,
			Confidence: l.Confidence,
			Box:        []int{l.Box[0], l.Box[1], l.Box[2], l.Box[3]},
			Center:     []int{cx, cy},
		})
	}
	return items
}

// NewVMObserveHandler builds the parallels_vm_observe handler around a
// Recognizer. Logs carry counts and timings only, never recognized text,
// queries or pixels.
func NewVMObserveHandler(rec ocr.Recognizer, logger *slog.Logger) func(context.Context, *mcp.CallToolRequest, ObserveInput) (*mcp.CallToolResult, ObserveOutput, error) {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ObserveInput) (*mcp.CallToolResult, ObserveOutput, error) {
		if rec == nil {
			return nil, ObserveOutput{}, errors.New("OCR is not available on this machine")
		}
		svc := parallels.GetInstance()
		target, err := resolvePowerTarget(svc, in.VMID)
		if err != nil {
			return nil, ObserveOutput{}, err
		}
		p, err := validateObserve(in)
		if err != nil {
			return nil, ObserveOutput{}, err
		}

		start := time.Now()
		res, err := svc.Capture(ctx, target)
		if err != nil {
			return nil, ObserveOutput{}, err
		}
		img, err := imaging.Downscale(res.PNG, p.maxDim)
		if err != nil {
			return nil, ObserveOutput{}, err
		}
		recog, err := rec.Recognize(ctx, img.Data)
		if err != nil {
			return nil, ObserveOutput{}, err
		}
		if recog.Width != img.Width || recog.Height != img.Height {
			return nil, ObserveOutput{}, fmt.Errorf("OCR reported a %dx%d image, expected %dx%d", recog.Width, recog.Height, img.Width, img.Height)
		}

		out := ObserveOutput{
			VMID:           target,
			Mode:           p.mode,
			Width:          img.Width,
			Height:         img.Height,
			OriginalWidth:  img.OriginalWidth,
			OriginalHeight: img.OriginalHeight,
			Scale:          math.Round(float64(img.Width)/float64(img.OriginalWidth)*observeScalePrecision) / observeScalePrecision,
			Lines:          []ObserveItem{},
			Matches:        []ObserveItem{},
			Added:          []ObserveItem{},
			Removed:        []ObserveItem{},
		}

		switch p.mode {
		case observeModeFind:
			found, ferr := ocr.Find(recog.Lines, p.query, p.opts)
			if ferr != nil {
				return nil, ObserveOutput{}, ferr
			}
			if len(found) > p.limit {
				found = found[:p.limit]
				out.Truncated = true
			}
			out.Matches = toObserveItems(found)
			out.Count = len(found)
		case observeModeChanges:
			prev, hadPrev := observeHistory.get(target)
			added, removed := diffLines(prev, recog.Lines)
			addedItems := toObserveItems(ocr.Filter(added, p.opts))
			removedItems := toObserveItems(ocr.Filter(removed, p.opts))
			if len(addedItems)+len(removedItems) > p.limit {
				out.Truncated = true
				if len(addedItems) > p.limit {
					addedItems = addedItems[:p.limit]
					removedItems = removedItems[:0]
				} else {
					removedItems = removedItems[:p.limit-len(addedItems)]
				}
			}
			out.Added = addedItems
			out.Removed = removedItems
			out.Count = len(addedItems) + len(removedItems)
			out.PreviousObserved = &hadPrev
		default:
			kept := ocr.Filter(recog.Lines, p.opts)
			if len(kept) > p.limit {
				kept = kept[:p.limit]
				out.Truncated = true
			}
			out.Lines = toObserveItems(kept)
			out.Count = len(kept)
			text := ocr.Join(kept)
			if len(text) > maxObserveTextBytes {
				text = strings.ToValidUTF8(text[:maxObserveTextBytes], "")
				out.Truncated = true
			}
			out.Text = text
		}

		// Every observation refreshes the changes baseline (full screen,
		// unfiltered).
		observeHistory.set(target, recog.Lines)

		logger.Info("vm observe completed", "vm", target, "mode", p.mode, "recognized", len(recog.Lines), "returned", out.Count, "truncated", out.Truncated, "took", time.Since(start))
		return nil, out, nil
	}
}

// observeInputSchema is the portable input schema: region is a plain
// "array" (no null union).
func observeInputSchema(mode PowerMode) *jsonschema.Schema {
	schema := powerInputSchema[ObserveInput](mode)
	normalizeNullArrays(schema)
	return schema
}

// RegisterParallelsVMObserve adds the parallels_vm_observe tool. OCR is
// built into the binary on macOS (spec 022), so registration is
// unconditional; rec is nil only in tests or a broken build.
func RegisterParallelsVMObserve(s *mcp.Server, mode PowerMode, rec ocr.Recognizer) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "parallels_vm_observe",
		Description: powerDescription(descVMObserve, mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: falsePtr(),
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  observeInputSchema(mode),
		OutputSchema: outputSchemaFor[ObserveOutput]("parallels_vm_observe"),
	}, NewVMObserveHandler(rec, slog.Default()))
}

// RegisterObserveStep makes parallels_vm_observe available as a
// parallels_vm_sequence step. Call it next to RegisterSteps.
func RegisterObserveStep(reg *orchestrator.Registry, rec ocr.Recognizer) {
	reg.Register(orchestrator.FromHandler[ObserveInput, ObserveOutput](
		"parallels_vm_observe", descVMObserve, true,
		observeInputSchema(PowerVM), NewVMObserveHandler(rec, slog.Default())))
}
