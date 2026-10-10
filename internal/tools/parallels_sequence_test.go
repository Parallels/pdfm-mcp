package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/orchestrator"
	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/tools"
)

// ---- fakes ----------------------------------------------------------------

// seqPNG renders a 640x360 solid-color PNG (real bytes, as prlctl capture
// would produce).
func seqPNG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 640, 360))
	r, g, b, a := c.RGBA()
	rr, gg, bb, aa := byte(r>>8), byte(g>>8), byte(b>>8), byte(a>>8)
	for off := 0; off < len(img.Pix); off += 4 {
		img.Pix[off], img.Pix[off+1], img.Pix[off+2], img.Pix[off+3] = rr, gg, bb, aa
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// seqRunner is a testRunner that scripts capture PNGs and exec results and
// logs every prlctl subcommand that was executed.
type seqRunner struct {
	*testRunner
	mu      sync.Mutex
	opLog   []string
	capture int
	exec    int
	pngs    [][]byte
	stdouts []string
	exits   []int
}

func newSeqRunner(t *testing.T, pngs [][]byte, stdouts []string, exits []int) *seqRunner {
	t.Helper()
	sr := &seqRunner{pngs: pngs, stdouts: stdouts, exits: exits}
	sr.testRunner = &testRunner{
		fn: func(_ string, args []string) (string, error) {
			sr.log(args[0])
			switch args[0] {
			case "list":
				return toolSampleVMsJSON, nil
			case "capture":
				if sr.capture >= len(pngs) {
					return "", errors.New("no scripted PNG left")
				}
				// argv: capture <vm> --file <path>
				if err := os.WriteFile(args[3], pngs[sr.capture], 0o644); err != nil {
					return "", err
				}
				sr.capture++
				return "", nil
			default: // power ops and anything else: success
				return "", nil
			}
		},
		captureFn: func(_ string, args []string) (string, string, int) {
			sr.log(args[0])
			stdout, exit := "", 0
			if sr.exec < len(stdouts) {
				stdout, exit = stdouts[sr.exec], exits[sr.exec]
			}
			sr.exec++
			return stdout, "", exit
		},
	}
	return sr
}

func (s *seqRunner) log(op string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opLog = append(s.opLog, op)
}

func (s *seqRunner) ops() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.opLog...)
}

func (s *seqRunner) ranOp(op string) bool {
	for _, o := range s.ops() {
		if o == op {
			return true
		}
	}
	return false
}

// seqSession brings up a server (fleet or VM mode) with the seq runner and
// an MCP client session.
func seqSession(t *testing.T, sr *seqRunner, vmID string) (*mcp.ClientSession, func()) {
	t.Helper()
	initParallelsForTest(t, sr.testRunner, []string{"prlctl", "prlsrvctl"}, vmID)
	cfg := testConfig()
	if vmID != "" {
		cfg.VMID = vmID
	}
	srv := server.New(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "seq-test", Version: "v0"}, nil)
	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		if cerr := sess.Close(); cerr != nil {
			t.Logf("close: %v", cerr)
		}
		t.Fatal(err)
	}
	cleanup := func() {
		if err := cs.Close(); err != nil {
			t.Logf("close client: %v", err)
		}
		if err := sess.Close(); err != nil {
			t.Logf("close server: %v", err)
		}
	}
	return cs, cleanup
}

// callSequence invokes parallels_vm_sequence and returns the raw result.
func callSequence(t *testing.T, cs *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "parallels_vm_sequence", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res
}

func seqText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

func seqStatus(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res.IsError {
		t.Fatalf("sequence unexpectedly errored: %s", seqText(t, res))
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(seqText(t, res)), &out); err != nil {
		t.Fatalf("report text is not JSON: %v", err)
	}
	return out
}

// seqWire converts an mcp schema (any) to a map[string]any tree.
func seqWire(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var seqWhite = color.RGBA{R: 255, G: 255, B: 255, A: 255}
var seqBlack = color.RGBA{R: 0, G: 0, B: 0, A: 255}

// ---- tests ----------------------------------------------------------------

func TestSequenceListedAndModeContract(t *testing.T) {
	sr := newSeqRunner(t, nil, nil, nil)
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var seq *mcp.Tool
	for _, tl := range list.Tools {
		if tl.Name == "parallels_vm_sequence" {
			seq = tl
		}
	}
	if seq == nil {
		t.Fatal("parallels_vm_sequence not listed in fleet mode")
	}
	req := map[string]bool{}
	for _, r := range seqWire(t, seq.InputSchema)["required"].([]any) {
		req[r.(string)] = true
	}
	if !req["vm_id"] || !req["steps"] {
		t.Fatalf("fleet required missing vm_id/steps: %v", req)
	}
	if !strings.Contains(seq.Description, "parallels_vm_exec(command, user?, elevated?, resolve_paths?)") {
		t.Fatalf("description must list step tool signatures: %s", seq.Description)
	}
	if strings.Contains(seq.Description, "vm_id?") {
		t.Fatalf("description must not list vm_id as a step arg: %s", seq.Description)
	}
	if !strings.Contains(seq.Description, "parallels_vm_start()") {
		t.Fatalf("description must show arg-less tools with empty parens: %s", seq.Description)
	}
	if len(seq.Description) >= 3072 {
		t.Fatalf("description is %d bytes, want < 3072", len(seq.Description))
	}

	// Fleet mode: vm_id is required.
	res := callSequence(t, cs, map[string]any{
		"steps": []any{map[string]any{"tool": "parallels_vm_start"}},
	})
	// The input schema (vm_id required in fleet mode) rejects it before the
	// handler runs; the handler keeps the same contract as a backstop.
	if !res.IsError || !strings.Contains(seqText(t, res), "vm_id") {
		t.Fatalf("want vm_id required error, got: %s", seqText(t, res))
	}

	// No prlctl call beyond the cache warm-up (list).
	if ops := sr.ops(); len(ops) != 1 || ops[0] != "list" {
		t.Fatalf("ops = %v, want only the warm-up list", ops)
	}
}

func TestSequenceVMModeContract(t *testing.T) {
	sr := newSeqRunner(t, nil, nil, nil)
	cs, cleanup := seqSession(t, sr, vmOneID)
	defer cleanup()

	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var seq *mcp.Tool
	for _, tl := range list.Tools {
		if tl.Name == "parallels_vm_sequence" {
			seq = tl
		}
	}
	if seq == nil {
		t.Fatal("parallels_vm_sequence not listed in VM mode")
	}
	for _, r := range seqWire(t, seq.InputSchema)["required"].([]any) {
		if r == "vm_id" {
			t.Fatalf("VM mode must not require vm_id")
		}
	}

	// VM mode: explicit vm_id is rejected.
	res := callSequence(t, cs, map[string]any{
		"vm_id": vmTwoID,
		"steps": []any{map[string]any{"tool": "parallels_vm_start"}},
	})
	if !res.IsError || !strings.Contains(seqText(t, res), "vm_id must be omitted") {
		t.Fatalf("want vm_id omitted error, got: %s", seqText(t, res))
	}
}

func TestSequenceMotivatingExampleChanged(t *testing.T) {
	white := seqPNG(t, seqWhite)
	black := seqPNG(t, seqBlack)
	sr := newSeqRunner(t, [][]byte{white, black}, []string{"20 lines of journal"}, []int{0})
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	res := callSequence(t, cs, map[string]any{
		"vm_id": "ubuntu_github_builder",
		"steps": []any{
			map[string]any{"id": "before", "tool": "parallels_vm_capture", "args": map[string]any{"max_dimension": 1024}},
			map[string]any{"wait": map[string]any{"seconds": 3}},
			map[string]any{"id": "after", "tool": "parallels_vm_capture", "args": map[string]any{"max_dimension": 1024},
				"expect": []any{map[string]any{"check": "image_changed", "from": "before"}}},
			map[string]any{"tool": "parallels_vm_exec", "args": map[string]any{"command": "journalctl -n 20 --no-pager"}},
		},
	})
	out := seqStatus(t, res)
	if out["status"] != "completed" {
		t.Fatalf("status = %v, want completed (%s)", out["status"], seqText(t, res))
	}
	if out["steps_run"] != float64(4) {
		t.Fatalf("steps_run = %v", out["steps_run"])
	}
	if out["image_count"] != float64(2) {
		t.Fatalf("image_count = %v", out["image_count"])
	}
	// Two image blocks after the text block, decodable PNGs.
	if len(res.Content) != 3 {
		t.Fatalf("content blocks = %d, want 3", len(res.Content))
	}
	for i := 1; i <= 2; i++ {
		ic, ok := res.Content[i].(*mcp.ImageContent)
		if !ok {
			t.Fatalf("content[%d] = %T, want *mcp.ImageContent", i, res.Content[i])
		}
		if ic.MIMEType != "image/png" {
			t.Fatalf("mime = %q", ic.MIMEType)
		}
		if _, err := png.DecodeConfig(bytes.NewReader(ic.Data)); err != nil {
			t.Fatalf("image %d is not a PNG: %v", i, err)
		}
	}
	// The exec ran and its output is in the report.
	results := out["results"].([]any)
	last := results[3].(map[string]any)
	if last["status"] != "ok" || last["tool"] != "parallels_vm_exec" {
		t.Fatalf("last = %v", last)
	}
	body := last["output"].(map[string]any)
	if body["stdout"] != "20 lines of journal" {
		t.Fatalf("exec output = %v", body)
	}
	// The expect check detail is present on the capture step.
	after := results[2].(map[string]any)
	checks := after["checks"].([]any)
	if len(checks) != 1 || checks[0].(map[string]any)["passed"] != true {
		t.Fatalf("after checks = %v", checks)
	}
	if d := checks[0].(map[string]any)["detail"].(string); !strings.Contains(d, "100.00%") {
		t.Fatalf("detail = %q", d)
	}
	if sr.exec != 1 {
		t.Fatalf("exec ran %d times, want 1", sr.exec)
	}
}

func TestSequenceMotivatingExampleUnchanged(t *testing.T) {
	white := seqPNG(t, seqWhite)
	sr := newSeqRunner(t, [][]byte{white, white}, []string{"should not run"}, []int{0})
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	res := callSequence(t, cs, map[string]any{
		"vm_id": "ubuntu_github_builder",
		"steps": []any{
			map[string]any{"id": "before", "tool": "parallels_vm_capture"},
			map[string]any{"wait": map[string]any{"seconds": 3}},
			map[string]any{"id": "after", "tool": "parallels_vm_capture",
				"expect": []any{map[string]any{"check": "image_changed", "from": "before"}}},
			map[string]any{"tool": "parallels_vm_exec", "args": map[string]any{"command": "journalctl -n 20 --no-pager"}},
		},
	})
	out := seqStatus(t, res)
	if out["status"] != "stopped" {
		t.Fatalf("status = %v, want stopped", out["status"])
	}
	if out["stopped_at"] != float64(2) {
		t.Fatalf("stopped_at = %v, want 2", out["stopped_at"])
	}
	results := out["results"].([]any)
	after := results[2].(map[string]any)
	if after["status"] != "expectation_failed" {
		t.Fatalf("after status = %v", after["status"])
	}
	last := results[3].(map[string]any)
	if last["status"] != "not_run" {
		t.Fatalf("exec status = %v, want not_run", last["status"])
	}
	if sr.ranOp("exec") {
		t.Fatal("exec must not run when the gate fails")
	}
}

func TestSequenceValidateOnly(t *testing.T) {
	sr := newSeqRunner(t, nil, nil, nil)
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	res := callSequence(t, cs, map[string]any{
		"vm_id":         "ubuntu_github_builder",
		"validate_only": true,
		"steps": []any{
			map[string]any{"tool": "parallels_vm_start"},
			map[string]any{"wait": map[string]any{"seconds": 5}},
			map[string]any{"tool": "parallels_vm_exec", "args": map[string]any{"command": "true"}},
		},
	})
	out := seqStatus(t, res)
	if out["status"] != "validated" {
		t.Fatalf("status = %v, want validated", out["status"])
	}
	if out["steps_run"] != float64(0) {
		t.Fatalf("steps_run = %v, want 0", out["steps_run"])
	}
	// Only the cache warm-up may have run.
	if ops := sr.ops(); len(ops) != 1 || ops[0] != "list" {
		t.Fatalf("ops = %v, validate_only must not run steps", ops)
	}
}

func TestSequenceRetryPollsUntilExpect(t *testing.T) {
	sr := newSeqRunner(t, nil, []string{"inactive", "active", "active"}, []int{3, 0, 0})
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	res := callSequence(t, cs, map[string]any{
		"vm_id": "ubuntu_github_builder",
		"steps": []any{
			map[string]any{"id": "ready", "tool": "parallels_vm_exec",
				"args":   map[string]any{"command": "systemctl is-active nginx"},
				"expect": []any{map[string]any{"check": "matches", "field": "stdout", "regex": "^active"}},
				"retry":  map[string]any{"max_attempts": 3, "delay_ms": 100}},
		},
	})
	out := seqStatus(t, res)
	if out["status"] != "completed" {
		t.Fatalf("status = %v: %s", out["status"], seqText(t, res))
	}
	results := out["results"].([]any)
	ready := results[0].(map[string]any)
	if ready["status"] != "ok" || ready["attempts"] != float64(2) {
		t.Fatalf("ready = %v", ready)
	}
	if sr.exec != 2 {
		t.Fatalf("exec ran %d times, want 2", sr.exec)
	}
}

func TestSequenceValidationErrorsReportedTogether(t *testing.T) {
	sr := newSeqRunner(t, nil, nil, nil)
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	res := callSequence(t, cs, map[string]any{
		"vm_id": "ubuntu_github_builder",
		"steps": []any{
			map[string]any{"tool": "parallels_vm_frobnicate"},
			map[string]any{"tool": "parallels_vm_exec", "args": map[string]any{"vm_id": "other", "command": "true"}},
			map[string]any{"id": "w1", "wait": map[string]any{"seconds": 1},
				"when": []any{map[string]any{"check": "succeeded", "step": "later"}}},
			map[string]any{"id": "later", "wait": map[string]any{"seconds": 1}},
		},
	})
	if !res.IsError {
		t.Fatalf("want a validation error, report: %s", seqText(t, res))
	}
	text := seqText(t, res)
	for _, want := range []string{
		"invalid sequence:",
		`unknown tool "parallels_vm_frobnicate"`,
		"must not contain vm_id",
		"not earlier",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("validation message missing %q: %s", want, text)
		}
	}
	// Nothing ran.
	if ops := sr.ops(); len(ops) != 1 || ops[0] != "list" {
		t.Fatalf("ops = %v, validation failure must not run steps", ops)
	}
}

func TestSequenceStepVMIDRejectedInVMMode(t *testing.T) {
	sr := newSeqRunner(t, nil, nil, nil)
	cs, cleanup := seqSession(t, sr, vmOneID)
	defer cleanup()

	res := callSequence(t, cs, map[string]any{
		"steps": []any{
			map[string]any{"tool": "parallels_vm_exec", "args": map[string]any{"vm_id": vmTwoID, "command": "true"}},
		},
	})
	if !res.IsError || !strings.Contains(seqText(t, res), "must not contain vm_id") {
		t.Fatalf("want vm_id rejection in VM mode, got: %s", seqText(t, res))
	}
}

func TestSequenceDriftGuard(t *testing.T) {
	// Every parallels_vm_* tool on the server (except sequence and info)
	// must be available as a step, and vice versa.
	sr := newSeqRunner(t, nil, nil, nil)
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tl := range list.Tools {
		listed[tl.Name] = true
	}

	reg := orchestrator.NewRegistry()
	tools.RegisterSteps(reg)
	stepNames := map[string]bool{}
	for _, n := range reg.Names() {
		stepNames[n] = true
	}

	for name := range listed {
		if !strings.HasPrefix(name, "parallels_vm_") {
			continue
		}
		if name == "parallels_vm_sequence" || name == "parallels_vm_info" {
			continue
		}
		// The async exec tools take and return job ids, which cannot flow
		// between sequence steps (steps have no templating), so they are
		// deliberately not steps (spec 018). parallels_vm_exec itself,
		// including elevated, remains a step.
		if name == "parallels_vm_exec_start" || name == "parallels_vm_exec_result" || name == "parallels_vm_exec_cancel" {
			continue
		}
		// parallels_vm_observe exists only when the OCR helper is present and
		// its step comes from RegisterObserveStep, not RegisterSteps.
		if name == "parallels_vm_observe" {
			continue
		}
		if !stepNames[name] {
			t.Fatalf("tool %s is listed but missing from the sequence registry (drift)", name)
		}
	}
	for name := range stepNames {
		if !listed[name] {
			t.Fatalf("step %s is registered but not listed as a tool (drift)", name)
		}
	}
	if len(stepNames) != 22 {
		t.Fatalf("registry has %d actions, want 22: %v", len(stepNames), reg.Names())
	}
}

func TestSequenceSchemasArePortable(t *testing.T) {
	sr := newSeqRunner(t, nil, nil, nil)
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var seq *mcp.Tool
	for _, tl := range list.Tools {
		if tl.Name == "parallels_vm_sequence" {
			seq = tl
		}
	}
	if seq == nil {
		t.Fatal("parallels_vm_sequence not listed")
	}

	input := seqWire(t, seq.InputSchema)
	if paths := nullArrayUnionPaths(input, "input"); len(paths) > 0 {
		t.Fatalf("input schema has null/array unions: %v", paths)
	}
	props := input["properties"].(map[string]any)
	for _, p := range []string{"vm_id", "steps", "on_error", "timeout_seconds", "max_images", "validate_only"} {
		if _, ok := props[p]; !ok {
			t.Fatalf("input schema missing property %q", p)
		}
	}
	stepsItems := props["steps"].(map[string]any)["items"].(map[string]any)
	stepProps := stepsItems["properties"].(map[string]any)
	for _, p := range []string{"id", "tool", "args", "wait", "gate", "when", "expect", "retry", "on_fail", "on_error", "quiet"} {
		if _, ok := stepProps[p]; !ok {
			t.Fatalf("step schema missing property %q", p)
		}
	}

	// Output schema: no null/array unions either.
	if seq.OutputSchema == nil {
		t.Fatal("sequence has no output schema")
	}
	output := seqWire(t, seq.OutputSchema)
	if paths := nullArrayUnionPaths(output, "output"); len(paths) > 0 {
		t.Fatalf("output schema has null/array unions: %v", paths)
	}
	// The report shape is visible to the AI.
	outProps := output["properties"].(map[string]any)
	for _, p := range []string{"vm_id", "status", "results", "image_count"} {
		if _, ok := outProps[p]; !ok {
			t.Fatalf("output schema missing property %q", p)
		}
	}
}

func TestSequenceWaitUntilSettled(t *testing.T) {
	white := seqPNG(t, seqWhite)
	black := seqPNG(t, seqBlack)
	sr := newSeqRunner(t, [][]byte{white, black, black, black}, nil, nil)
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	res := callSequence(t, cs, map[string]any{
		"vm_id": "ubuntu_github_builder",
		"steps": []any{
			map[string]any{"id": "settle", "wait": map[string]any{"until": "settled", "poll_ms": 200, "timeout_seconds": 5}},
		},
	})
	out := seqStatus(t, res)
	if out["status"] != "completed" {
		t.Fatalf("status = %v (%s)", out["status"], seqText(t, res))
	}
	// Polling never returns image blocks.
	if len(res.Content) != 1 || out["image_count"] != float64(0) {
		t.Fatalf("content blocks = %d image_count = %v", len(res.Content), out["image_count"])
	}
	step := out["results"].([]any)[0].(map[string]any)
	body := step["output"].(map[string]any)
	if step["kind"] != "wait" || body["until"] != "settled" || body["met"] != true || body["polls"] != float64(4) {
		t.Fatalf("step = %v", step)
	}
	if sr.capture != 4 {
		t.Fatalf("capture ran %d times, want 4", sr.capture)
	}
}

func TestSequenceWaitUntilValidation(t *testing.T) {
	sr := newSeqRunner(t, nil, nil, nil)
	cs, cleanup := seqSession(t, sr, "")
	defer cleanup()

	res := callSequence(t, cs, map[string]any{
		"vm_id": "ubuntu_github_builder",
		"steps": []any{
			map[string]any{"wait": map[string]any{"seconds": 1, "until": "settled"}},
		},
	})
	if !res.IsError || !strings.Contains(seqText(t, res), "exactly one of seconds or until") {
		t.Fatalf("want validation error, got: %s", seqText(t, res))
	}
	if sr.ranOp("capture") {
		t.Fatal("nothing may run on a validation error")
	}
}
