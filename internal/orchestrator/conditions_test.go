package orchestrator

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
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

// stateFixture builds a state with two recorded tool steps and two images.
func stateFixture(t *testing.T, changed bool) *state {
	t.Helper()
	a := pngBytes(t, 10, 10, color.White)
	b := a
	if changed {
		b = pngBytes(t, 10, 10, color.Black)
	}
	st := newState()
	st.record("exec1", StepOK, map[string]any{"exit_code": float64(0), "stdout": "active now", "stderr": ""}, Image{}, false)
	st.record("cap1", StepOK, map[string]any{"width": float64(10)}, Image{Data: a, MIMEType: "image/png"}, true)
	st.record("cap2", StepOK, map[string]any{"width": float64(10)}, Image{Data: b, MIMEType: "image/png"}, true)
	st.unavailable("gone", StepSkipped)
	st.unavailable("never", StepNotRun)
	return st
}

func TestCheckExitCode(t *testing.T) {
	st := stateFixture(t, false)
	cases := []struct {
		name string
		c    Check
		pass bool
		det  string
	}{
		{"default zero", Check{Check: "exit_code"}, true, "exit_code 0 (expected 0)"},
		{"wrong equals", Check{Check: "exit_code", Equals: intPtr(1)}, false, "exit_code 0 (expected 1)"},
		{"negate flip", Check{Check: "exit_code", Negate: true}, false, "negated: exit_code 0 (expected 0)"},
		{"missing step", Check{Check: "exit_code", Step: "gone"}, false, `step "gone" did not run`},
		{"not run step", Check{Check: "exit_code", Step: "never"}, false, `step "never" did not run`},
	}
	for _, tc := range cases {
		res := evaluate(PhaseExpect, "exec1", []planCheck{mkPlanCheck(t, tc.c)}, st)
		if len(res) != 1 || res[0].Passed != tc.pass || res[0].Detail != tc.det {
			t.Fatalf("%s: got %+v, want passed=%v detail=%q", tc.name, res, tc.pass, tc.det)
		}
	}
}

func TestCheckMatches(t *testing.T) {
	st := stateFixture(t, false)
	yes := Check{Check: "matches", Regex: `^active`}
	no := Check{Check: "matches", Regex: `^stopped`}
	field := Check{Check: "matches", Field: "stderr", Regex: `x`}
	missing := Check{Check: "matches", Field: "nope", Regex: `x`}
	neg := Check{Check: "matches", Regex: `^stopped`, Negate: true}
	for _, tc := range []struct {
		c    Check
		pass bool
		det  string
	}{
		{yes, true, "matched in stdout (10 chars)"},
		{no, false, `no match for "^stopped" in stdout`},
		{field, false, `no match for "x" in stderr`}, // stderr exists and is ""
		{missing, false, `field "nope" missing from step "exec1"`},
		{neg, true, "negated: no match for \"^stopped\" in stdout"},
	} {
		res := evaluate(PhaseExpect, "exec1", []planCheck{mkPlanCheck(t, tc.c)}, st)
		if len(res) != 1 || res[0].Passed != tc.pass || res[0].Detail != tc.det {
			t.Fatalf("check %+v: got %+v, want passed=%v detail=%q", tc.c, res, tc.pass, tc.det)
		}
	}
}

func TestCheckContains(t *testing.T) {
	st := stateFixture(t, false)
	for _, tc := range []struct {
		c    Check
		pass bool
		det  string
	}{
		{Check{Check: "contains", Text: "active"}, true, `contains "active" in stdout`},
		{Check{Check: "contains", Text: "zzz"}, false, `missing "zzz" in stdout`},
		{Check{Check: "contains", Text: "active", Negate: true}, false, `negated: contains "active" in stdout`},
	} {
		res := evaluate(PhaseExpect, "exec1", []planCheck{mkPlanCheck(t, tc.c)}, st)
		if len(res) != 1 || res[0].Passed != tc.pass || res[0].Detail != tc.det {
			t.Fatalf("check %+v: got %+v, want passed=%v detail=%q", tc.c, res, tc.pass, tc.det)
		}
	}
}

func TestCheckSucceeded(t *testing.T) {
	st := stateFixture(t, false)
	for _, tc := range []struct {
		c    Check
		pass bool
		det  string
	}{
		{Check{Check: "succeeded", Step: "exec1"}, true, `step "exec1" is ok`},
		{Check{Check: "succeeded", Step: "gone"}, false, `step "gone" is skipped`},
		{Check{Check: "succeeded", Step: "never"}, false, `step "never" is not_run`},
		{Check{Check: "succeeded", Step: "exec1", Negate: true}, false, `negated: step "exec1" is ok`},
	} {
		res := evaluate(PhaseWhen, "exec1", []planCheck{mkPlanCheck(t, tc.c)}, st)
		if len(res) != 1 || res[0].Passed != tc.pass || res[0].Detail != tc.det {
			t.Fatalf("check %+v: got %+v, want passed=%v detail=%q", tc.c, res, tc.pass, tc.det)
		}
	}
}

func TestCheckImageChanged(t *testing.T) {
	// Identical images: unchanged by default (threshold 0.1).
	st := stateFixture(t, false)
	res := evaluate(PhaseGate, "cap2", []planCheck{mkPlanCheck(t, Check{Check: "image_changed", From: "cap1", To: "cap2"})}, st)
	if res[0].Passed || !strings.HasPrefix(res[0].Detail, "0.00%") {
		t.Fatalf("identical: %+v", res)
	}

	// Changed images: passed.
	st2 := stateFixture(t, true)
	res = evaluate(PhaseGate, "cap2", []planCheck{mkPlanCheck(t, Check{Check: "image_changed", From: "cap1", To: "cap2"})}, st2)
	if !res[0].Passed || !strings.Contains(res[0].Detail, "100.00%") {
		t.Fatalf("changed: %+v", res)
	}

	// Negate flips both.
	res = evaluate(PhaseGate, "cap2", []planCheck{mkPlanCheck(t, Check{Check: "image_changed", From: "cap1", To: "cap2", Negate: true})}, st)
	if !res[0].Passed || !strings.HasPrefix(res[0].Detail, "negated:") {
		t.Fatalf("negate identical: %+v", res)
	}

	// Missing image fails even negated (unavailable input rule).
	st3 := newState()
	st3.record("exec1", StepOK, map[string]any{}, Image{}, false)
	res = evaluate(PhaseGate, "exec1", []planCheck{mkPlanCheck(t, Check{Check: "image_changed", From: "cap1", To: "cap2", Negate: true})}, st3)
	if res[0].Passed || res[0].Detail != `step "cap1" has no image` {
		t.Fatalf("missing image negated: %+v", res)
	}

	// Caching: two evaluations → one diff computation.
	st4 := stateFixture(t, true)
	ck := mkPlanCheck(t, Check{Check: "image_changed", From: "cap1", To: "cap2"})
	evaluate(PhaseGate, "cap2", []planCheck{ck}, st4)
	evaluate(PhaseGate, "cap2", []planCheck{ck}, st4)
	if len(st4.diffCache) != 1 {
		t.Fatalf("diffCache = %d entries, want 1", len(st4.diffCache))
	}
	// A different threshold is a different cache entry.
	thr := 50.0
	evaluate(PhaseGate, "cap2", []planCheck{mkPlanCheck(t, Check{Check: "image_changed", From: "cap1", To: "cap2", ThresholdPercent: &thr})}, st4)
	if len(st4.diffCache) != 2 {
		t.Fatalf("diffCache = %d entries, want 2", len(st4.diffCache))
	}
}

func TestAllPassedAndFailedCheck(t *testing.T) {
	if !allPassed(nil) || !allPassed([]CheckResult{{Passed: true}}) {
		t.Fatal("allPassed positive cases")
	}
	if allPassed([]CheckResult{{Passed: true}, {Passed: false}}) {
		t.Fatal("allPassed must catch a failure")
	}
	if failedCheck([]CheckResult{{Passed: true}, {Passed: false, Check: "matches"}}) != "matches" {
		t.Fatal("failedCheck must return the failing kind")
	}
	if failedCheck([]CheckResult{{Passed: true}}) != "" {
		t.Fatal("failedCheck on all-pass must be empty")
	}
}
