package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/ocr"
	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/tools"
)

const obsSecret = "hunter2-SECRET"

// obsRecognizer is a fake ocr.Recognizer: it reports the dimensions of the
// image it was handed (so coordinates stay consistent) and canned lines.
type obsRecognizer struct {
	mu       sync.Mutex
	lines    []ocr.Line
	err      error
	badSize  bool
	calls    int
	gotW     int
	gotH     int
	gotBytes int
}

func (f *obsRecognizer) Recognize(_ context.Context, png []byte) (ocr.Recognition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.gotBytes = len(png)
	if f.err != nil {
		return ocr.Recognition{}, f.err
	}
	w, h, err := dimensionsOf(png)
	if err != nil {
		return ocr.Recognition{}, err
	}
	f.gotW, f.gotH = w, h
	if f.badSize {
		return ocr.Recognition{Width: w + 1, Height: h}, nil
	}
	return ocr.Recognition{Width: w, Height: h, Lines: f.lines}, nil
}

func obsLines() []ocr.Line {
	return []ocr.Line{
		{Text: "File", Confidence: 0.99, Box: ocr.Box{10, 10, 50, 30}},
		{Text: "Edit", Confidence: 0.98, Box: ocr.Box{60, 11, 100, 31}},
		{Text: "Save changes?", Confidence: 0.95, Box: ocr.Box{200, 200, 400, 230}},
		{Text: "Cancel", Confidence: 0.40, Box: ocr.Box{200, 300, 260, 320}},
		{Text: "OK", Confidence: 0.10, Box: ocr.Box{300, 300, 330, 320}},
		{Text: "password " + obsSecret, Confidence: 0.90, Box: ocr.Box{20, 500, 320, 520}},
	}
}

func obsSetup(t *testing.T, vmID string, rec *obsRecognizer) (*testRunner, func(tools.ObserveInput) (*mcp.CallToolResult, tools.ObserveOutput, error), *bytes.Buffer) {
	t.Helper()
	runner := &testRunner{fn: captureFn(capturePNG(t, 2000, 1000))}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmID)
	var logs bytes.Buffer
	h := tools.NewVMObserveHandler(rec, slog.New(slog.NewTextHandler(&logs, nil)))
	call := func(in tools.ObserveInput) (*mcp.CallToolResult, tools.ObserveOutput, error) {
		return h(context.Background(), nil, in)
	}
	return runner, call, &logs
}

func TestObserveTextMode(t *testing.T) {
	rec := &obsRecognizer{lines: obsLines()}
	runner, call, logs := obsSetup(t, "", rec)

	res, out, err := call(tools.ObserveInput{VMID: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatalf("handler must leave the result to the SDK (no image content), got %+v", res)
	}
	if runner.count() != 1 || rec.calls != 1 {
		t.Fatalf("capture calls %d, ocr calls %d", runner.count(), rec.calls)
	}
	// Same defaults as parallels_vm_capture: 2000x1000 -> 1568x784.
	if rec.gotW != 1568 || rec.gotH != 784 {
		t.Fatalf("OCR ran on %dx%d, want 1568x784", rec.gotW, rec.gotH)
	}
	if out.Width != 1568 || out.Height != 784 || out.OriginalWidth != 2000 || out.OriginalHeight != 1000 {
		t.Fatalf("dims = %+v", out)
	}
	if out.Scale != 0.784 {
		t.Fatalf("scale = %v, want 0.784", out.Scale)
	}
	if out.Mode != "text" || out.VMID != "g" {
		t.Fatalf("out = %+v", out)
	}
	// Default min_confidence 0.3 drops "OK" (0.10).
	if out.Count != 5 || len(out.Lines) != 5 || out.Matches == nil || len(out.Matches) != 0 {
		t.Fatalf("count %d lines %d matches %#v", out.Count, len(out.Lines), out.Matches)
	}
	if !strings.HasPrefix(out.Text, "File Edit\nSave changes?\nCancel") || strings.Contains(out.Text, "OK") {
		t.Fatalf("text = %q", out.Text)
	}
	if out.Truncated {
		t.Fatal("not truncated")
	}
	first := out.Lines[0]
	if first.Text != "File" || len(first.Box) != 4 || first.Center[0] != 30 || first.Center[1] != 20 {
		t.Fatalf("first = %+v", first)
	}
	// Logs must carry no recognized text.
	if strings.Contains(logs.String(), obsSecret) || strings.Contains(logs.String(), "Save changes") {
		t.Fatalf("logs leak recognized text: %s", logs.String())
	}
}

func TestObserveFindMode(t *testing.T) {
	rec := &obsRecognizer{lines: obsLines()}
	_, call, logs := obsSetup(t, "", rec)

	_, out, err := call(tools.ObserveInput{VMID: "g", Mode: "find", Query: "SAVE"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "find" || out.Count != 1 || len(out.Matches) != 1 || out.Text != "" || out.Lines == nil || len(out.Lines) != 0 {
		t.Fatalf("out = %+v", out)
	}
	m := out.Matches[0]
	if m.Text != "Save changes?" || m.Center[0] != 300 || m.Center[1] != 215 {
		t.Fatalf("match = %+v", m)
	}

	// Regex, region, nothing found, and min_confidence.
	_, out, err = call(tools.ObserveInput{VMID: "g", Mode: "find", Query: `^(File|Edit)$`, Regex: true})
	if err != nil || out.Count != 2 {
		t.Fatalf("regex: %+v err %v", out, err)
	}
	_, out, err = call(tools.ObserveInput{VMID: "g", Mode: "find", Query: "e", Region: []int{0, 0, 120, 40}})
	if err != nil || out.Count != 2 {
		t.Fatalf("region: %+v err %v", out, err)
	}
	_, out, err = call(tools.ObserveInput{VMID: "g", Mode: "find", Query: "nothing-here"})
	if err != nil || out.Count != 0 || out.Matches == nil {
		t.Fatalf("no match: %+v err %v", out, err)
	}
	_, out, err = call(tools.ObserveInput{VMID: "g", Mode: "find", Query: "ok"})
	if err != nil || out.Count != 0 {
		t.Fatalf("OK is below the default confidence: %+v err %v", out, err)
	}
	_, out, err = call(tools.ObserveInput{VMID: "g", Mode: "find", Query: "ok", MinConfidence: 0.05})
	if err != nil || out.Count != 1 {
		t.Fatalf("lowered confidence: %+v err %v", out, err)
	}
	if strings.Contains(logs.String(), obsSecret) || strings.Contains(logs.String(), "SAVE") {
		t.Fatalf("logs leak query or text: %s", logs.String())
	}
}

func TestObserveLimitTruncates(t *testing.T) {
	rec := &obsRecognizer{lines: obsLines()}
	_, call, _ := obsSetup(t, "", rec)
	_, out, err := call(tools.ObserveInput{VMID: "g", Limit: 2})
	if err != nil || out.Count != 2 || !out.Truncated {
		t.Fatalf("text limit: %+v err %v", out, err)
	}
	_, out, err = call(tools.ObserveInput{VMID: "g", Mode: "find", Query: "e", Limit: 1})
	if err != nil || out.Count != 1 || !out.Truncated {
		t.Fatalf("find limit: %+v err %v", out, err)
	}
}

func TestObserveMaxDimensionHonoured(t *testing.T) {
	rec := &obsRecognizer{lines: obsLines()}
	_, call, _ := obsSetup(t, "", rec)
	_, out, err := call(tools.ObserveInput{VMID: "g", MaxDimension: 512})
	if err != nil {
		t.Fatal(err)
	}
	if out.Width != 512 || out.Height != 256 || out.Scale != 0.256 {
		t.Fatalf("out = %+v", out)
	}
}

func TestObserveModes(t *testing.T) {
	cases := []struct {
		name string
		vmID string
		arg  string
		want string
	}{
		{"vm-mode uses configured target", vmOneID, "", vmOneID},
		{"vm-mode rejects explicit vm_id", vmOneID, vmTwoID, ""},
		{"fleet requires vm_id", "", "", ""},
		{"fleet uses given vm_id", "", "guest-x", "guest-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &obsRecognizer{lines: obsLines()}
			runner, call, _ := obsSetup(t, tc.vmID, rec)
			_, out, err := call(tools.ObserveInput{VMID: tc.arg})
			if tc.want == "" {
				if err == nil {
					t.Fatal("want error")
				}
				if runner.count() != 0 {
					t.Fatal("prlctl must not run on a mode error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.VMID != tc.want {
				t.Fatalf("vm_id = %q, want %q", out.VMID, tc.want)
			}
			_, args := runner.lastCall()
			if len(args) < 2 || args[0] != "capture" || args[1] != tc.want {
				t.Fatalf("argv = %v", args)
			}
		})
	}
}

func TestObserveValidation(t *testing.T) {
	cases := []struct {
		name string
		in   tools.ObserveInput
	}{
		{"bad mode", tools.ObserveInput{Mode: "ocr"}},
		{"find without query", tools.ObserveInput{Mode: "find"}},
		{"query in text mode", tools.ObserveInput{Query: "x"}},
		{"regex in text mode", tools.ObserveInput{Regex: true}},
		{"query too long", tools.ObserveInput{Mode: "find", Query: strings.Repeat("a", 1025)}},
		{"region 3 values", tools.ObserveInput{Region: []int{0, 0, 5}}},
		{"region 5 values", tools.ObserveInput{Region: []int{0, 0, 5, 5, 5}}},
		{"region inverted", tools.ObserveInput{Region: []int{10, 0, 5, 5}}},
		{"region negative", tools.ObserveInput{Region: []int{-1, 0, 5, 5}}},
		{"confidence above 1", tools.ObserveInput{MinConfidence: 1.1}},
		{"confidence negative", tools.ObserveInput{MinConfidence: -0.1}},
		{"limit negative", tools.ObserveInput{Limit: -1}},
		{"limit too big", tools.ObserveInput{Limit: 501}},
		{"max_dimension low", tools.ObserveInput{MaxDimension: 255}},
		{"max_dimension high", tools.ObserveInput{MaxDimension: 4097}},
		{"invalid regex", tools.ObserveInput{Mode: "find", Query: "(", Regex: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &obsRecognizer{lines: obsLines()}
			runner, call, _ := obsSetup(t, "", rec)
			tc.in.VMID = "g"
			if _, _, err := call(tc.in); err == nil {
				t.Fatal("want error")
			}
			if tc.name != "invalid regex" && runner.count() != 0 {
				t.Fatal("prlctl must not run for invalid input")
			}
		})
	}
}

func TestObserveFailures(t *testing.T) {
	// Recognizer error is surfaced.
	rec := &obsRecognizer{err: errors.New("helper exploded")}
	_, call, _ := obsSetup(t, "", rec)
	if _, _, err := call(tools.ObserveInput{VMID: "g"}); err == nil || !strings.Contains(err.Error(), "helper exploded") {
		t.Fatalf("err = %v", err)
	}
	// Dimension mismatch from the helper is rejected.
	rec = &obsRecognizer{badSize: true}
	_, call, _ = obsSetup(t, "", rec)
	if _, _, err := call(tools.ObserveInput{VMID: "g"}); err == nil {
		t.Fatal("size mismatch must fail")
	}
	// Capture failure: the recognizer is never called.
	rec = &obsRecognizer{}
	runner := &testRunner{fn: func(string, []string) (string, error) { return "", errors.New("vm is stopped") }}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	h := tools.NewVMObserveHandler(rec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, _, err := h(context.Background(), nil, tools.ObserveInput{VMID: "g"}); err == nil {
		t.Fatal("capture failure must propagate")
	}
	if rec.calls != 0 {
		t.Fatal("recognizer must not run without a screenshot")
	}
	// Nil recognizer.
	nilH := tools.NewVMObserveHandler(nil, nil)
	if _, _, err := nilH(context.Background(), nil, tools.ObserveInput{VMID: "g"}); err == nil {
		t.Fatal("nil recognizer must fail")
	}
}

func obsConnect(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sess.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "observe-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	return cs
}

func TestObserveProtocol(t *testing.T) {
	rec := &obsRecognizer{lines: obsLines()}
	runner := &testRunner{fn: captureFn(capturePNG(t, 2000, 1000))}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	srv := mcp.NewServer(&mcp.Implementation{Name: "observe", Version: "v0"}, nil)
	tools.RegisterParallelsVMObserve(srv, tools.PowerFleet, rec)

	names := toolNames(t, srv)
	if !names["parallels_vm_observe"] {
		t.Fatalf("parallels_vm_observe not listed: %v", names)
	}
	if required, ok := wireInputRequired(t, srv, "parallels_vm_observe"); !ok || !required {
		t.Fatalf("fleet mode: vm_id must be required (present=%v required=%v)", ok, required)
	}
	for _, tl := range listTools(t, srv) {
		if tl.Name != "parallels_vm_observe" {
			continue
		}
		if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
			t.Fatalf("annotations = %+v, want read-only", tl.Annotations)
		}
		if tl.Annotations.DestructiveHint == nil || *tl.Annotations.DestructiveHint {
			t.Fatal("destructive hint must be false")
		}
		for label, sch := range map[string]any{"input": tl.InputSchema, "output": tl.OutputSchema} {
			m, ok := sch.(map[string]any)
			if !ok {
				t.Fatalf("%s schema = %T", label, sch)
			}
			if bad := nullArrayUnionPaths(m, label+"Schema"); len(bad) > 0 {
				t.Fatalf("non-portable %s schema: %v", label, bad)
			}
		}
		for _, want := range []string{"screenshot pixels", "original_width", "Apple Vision", "secrets"} {
			if !strings.Contains(tl.Description, want) {
				t.Fatalf("description lacks %q", want)
			}
		}
	}

	cs := obsConnect(t, srv)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "parallels_vm_observe",
		Arguments: map[string]any{"vm_id": "guest-x", "mode": "find", "query": "save", "region": []int{0, 100, 1568, 300}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	if sc == nil {
		t.Fatal("no structured content")
	}
	if sc["count"] != float64(1) || sc["width"] != float64(1568) || sc["original_width"] != float64(2000) {
		t.Fatalf("structured = %v", sc)
	}
	// No image anywhere in the result.
	for _, c := range res.Content {
		if _, ok := c.(*mcp.ImageContent); ok {
			t.Fatal("observe must not return image content")
		}
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("iVBOR")) {
		t.Fatal("result contains base64 PNG data")
	}

	// Invalid arguments come back as a tool error.
	bad, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "parallels_vm_observe", Arguments: map[string]any{"vm_id": "guest-x", "mode": "find"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatal("find without query must be a tool error")
	}
}

func TestObserveVMModeProtocol(t *testing.T) {
	rec := &obsRecognizer{lines: obsLines()}
	runner := &testRunner{fn: captureFn(capturePNG(t, 1200, 800))}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)
	srv := mcp.NewServer(&mcp.Implementation{Name: "observe", Version: "v0"}, nil)
	tools.RegisterParallelsVMObserve(srv, tools.PowerVM, rec)

	if required, ok := wireInputRequired(t, srv, "parallels_vm_observe"); !ok || required {
		t.Fatalf("VM mode: vm_id must not be required (present=%v required=%v)", ok, required)
	}
	cs := obsConnect(t, srv)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "parallels_vm_observe", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("call: err %v res %+v", err, res)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	if sc["vm_id"] != vmOneID || sc["scale"] != float64(1) {
		t.Fatalf("structured = %v", sc)
	}
	bad, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "parallels_vm_observe", Arguments: map[string]any{"vm_id": vmTwoID}})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatal("explicit vm_id must be rejected in VM mode")
	}
}

// OCR is built into the binary (spec 022): the full server always lists
// parallels_vm_observe, with no environment or file preconditions.
func TestObserveAlwaysRegistered(t *testing.T) {
	runner := &testRunner{fn: captureFn(capturePNG(t, 100, 100))}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	srv := server.New(slog.New(slog.NewTextHandler(io.Discard, nil)), testConfig())
	if !toolNames(t, srv)["parallels_vm_observe"] {
		t.Fatal("parallels_vm_observe must be listed (OCR is built in)")
	}
}

// obsChangeRecognizer is an obsRecognizer whose lines the test can change
// between calls (simulating screen changes).
func obsChangeRecognizer(t *testing.T, vmID string) (*obsRecognizer, func(tools.ObserveInput) (*mcp.CallToolResult, tools.ObserveOutput, error)) {
	t.Helper()
	rec := &obsRecognizer{}
	_, call, _ := obsSetup(t, "", rec)
	return rec, func(in tools.ObserveInput) (*mcp.CallToolResult, tools.ObserveOutput, error) {
		in.VMID = vmID
		return call(in)
	}
}

func TestObserveChangesMode(t *testing.T) {
	rec, call := obsChangeRecognizer(t, "chg-1")

	// First observation: everything is "added", no previous baseline.
	rec.lines = obsLines()
	_, out, err := call(tools.ObserveInput{Mode: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	if out.PreviousObserved == nil || *out.PreviousObserved {
		t.Fatalf("previous_observed must be false on the first call: %+v", out)
	}
	// "OK" (0.10) is below the default confidence and stays out of the diff.
	if out.Count != 5 || len(out.Added) != 5 || len(out.Removed) != 0 || out.Text != "" || len(out.Lines) != 0 {
		t.Fatalf("out = %+v", out)
	}

	// Screen changes: one line edited, one line gone.
	rec.lines = []ocr.Line{
		{Text: "File", Confidence: 0.99, Box: ocr.Box{10, 10, 50, 30}},
		{Text: "Edit", Confidence: 0.98, Box: ocr.Box{60, 11, 100, 31}},
		{Text: "Save now?", Confidence: 0.95, Box: ocr.Box{200, 200, 400, 230}},
		{Text: "Cancel", Confidence: 0.40, Box: ocr.Box{200, 300, 260, 320}},
		{Text: "password " + obsSecret, Confidence: 0.90, Box: ocr.Box{20, 500, 320, 520}},
	}
	_, out, err = call(tools.ObserveInput{Mode: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	if out.PreviousObserved == nil || !*out.PreviousObserved {
		t.Fatal("previous_observed must be true now")
	}
	if out.Count != 2 || len(out.Added) != 1 || len(out.Removed) != 1 {
		t.Fatalf("out = %+v", out)
	}
	if out.Added[0].Text != "Save now?" || out.Removed[0].Text != "Save changes?" {
		t.Fatalf("added=%v removed=%v", out.Added, out.Removed)
	}

	// No change: empty diff.
	_, out, err = call(tools.ObserveInput{Mode: "changes"})
	if err != nil || out.Count != 0 || len(out.Added) != 0 || len(out.Removed) != 0 {
		t.Fatalf("unchanged: %+v err %v", out, err)
	}

	// A text-mode call refreshes the baseline for the next changes call.
	rec.lines = append([]ocr.Line{}, obsLines()...)
	_, out, err = call(tools.ObserveInput{Mode: "text"})
	if err != nil || out.Count != 5 {
		t.Fatalf("text: %+v err %v", out, err)
	}
	rec.lines = nil
	_, out, err = call(tools.ObserveInput{Mode: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 5 || len(out.Removed) != 5 || len(out.Added) != 0 {
		t.Fatalf("after blank screen: %+v", out)
	}

	// A find-mode call also refreshes the baseline.
	rec.lines = obsLines()
	_, _, err = call(tools.ObserveInput{Mode: "find", Query: "file"})
	if err != nil {
		t.Fatal(err)
	}
	rec.lines = []ocr.Line{{Text: "File", Confidence: 0.99, Box: ocr.Box{10, 10, 50, 30}}}
	_, out, err = call(tools.ObserveInput{Mode: "changes"})
	// Baseline was the 6 obsLines; only File remains. Removed = Edit,
	// Save changes?, Cancel, password ("OK" filtered by confidence).
	if err != nil || out.Count != 4 || len(out.Removed) != 4 || len(out.Added) != 0 {
		t.Fatalf("baseline from find: %+v err %v", out, err)
	}
}

func TestObserveChangesLimitAndRegion(t *testing.T) {
	new4 := func() []ocr.Line {
		return []ocr.Line{
			{Text: "One", Confidence: 0.99, Box: ocr.Box{10, 10, 50, 30}},
			{Text: "Two", Confidence: 0.99, Box: ocr.Box{60, 10, 100, 30}},
			{Text: "Three", Confidence: 0.99, Box: ocr.Box{10, 60, 50, 80}},
			{Text: "Four", Confidence: 0.99, Box: ocr.Box{60, 60, 100, 80}},
		}
	}
	rec, call := obsChangeRecognizer(t, "chg-2")
	rec.lines = obsLines()
	if _, _, err := call(tools.ObserveInput{Mode: "changes"}); err != nil {
		t.Fatal(err)
	}
	// Every call refreshes the baseline, so reset the screen per sub-case.
	// Diff vs obsLines: 4 added + 5 removed ("OK" filtered) = 9 candidates.
	rec.lines = new4()
	_, out, err := call(tools.ObserveInput{Mode: "changes", Limit: 2})
	if err != nil || !out.Truncated || out.Count != 2 || len(out.Added) != 2 || len(out.Removed) != 0 {
		t.Fatalf("limit: %+v err %v", out, err)
	}
	// Baseline is now new4; flipping back to obsLines gives the mirror diff:
	// 5 added (File, Edit, Save changes?, Cancel, password) + 4 removed.
	rec.lines = obsLines()
	_, out, err = call(tools.ObserveInput{Mode: "changes", Limit: 6})
	if err != nil || !out.Truncated || out.Count != 6 || len(out.Added) != 5 || len(out.Removed) != 1 {
		t.Fatalf("limit split: %+v err %v", out, err)
	}
	rec.lines = new4()
	_, out, err = call(tools.ObserveInput{Mode: "changes", Region: []int{0, 0, 1568, 40}, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 4 || len(out.Added) != 2 || len(out.Removed) != 2 {
		t.Fatalf("region: %+v", out)
	}
	for _, it := range append(append([]tools.ObserveItem{}, out.Added...), out.Removed...) {
		if it.Box[1] >= 40 || it.Box[3] <= 0 {
			t.Fatalf("diff entry outside region: %+v", it)
		}
		if strings.Contains(it.Text, obsSecret) {
			t.Fatalf("secret line leaked into region diff: %+v", it)
		}
	}
}

// The changes baseline store is capped: after 17 distinct VMs, the first
// VM's baseline is gone (previous_observed false again).
func TestObserveHistoryCap(t *testing.T) {
	for i := 0; i < 17; i++ {
		rec := &obsRecognizer{lines: []ocr.Line{{Text: fmt.Sprintf("line-%d", i), Confidence: 1, Box: ocr.Box{0, 0, 1, 1}}}}
		_, call, _ := obsSetup(t, "", rec)
		if _, _, err := call(tools.ObserveInput{VMID: fmt.Sprintf("cap-%d", i), Mode: "changes"}); err != nil {
			t.Fatal(err)
		}
	}
	// cap-0 was evicted; a fresh observation has no previous baseline.
	rec := &obsRecognizer{lines: []ocr.Line{{Text: "line-0", Confidence: 1, Box: ocr.Box{0, 0, 1, 1}}}}
	_, call, _ := obsSetup(t, "", rec)
	_, out, err := call(tools.ObserveInput{VMID: "cap-0", Mode: "changes"})
	if err != nil {
		t.Fatal(err)
	}
	if out.PreviousObserved == nil || *out.PreviousObserved {
		t.Fatalf("first VM must have been evicted: %+v", out)
	}
}
