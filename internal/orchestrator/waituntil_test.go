package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func untilF64(v float64) *float64 { return &v }
func untilInt(v int) *int         { return &v }

// regWithCapture adds a capture-like action under the real capture name so
// wait-until polling and capture steps share one scripted source.
func regWithCapture() *Registry {
	reg := regWithFakes()
	reg.Register(Action{
		Name: captureActionName, Description: "c", VMScoped: true, ProducesImages: true,
		ValidateArgs: func(json.RawMessage) error { return nil },
	})
	return reg
}

// scriptedCapture registers parallels_vm_capture returning the given PNGs in
// order (the last one repeats). calls counts invocations; argsSeen records the
// raw args of every call.
func scriptedCapture(reg *Registry, imgs [][]byte, calls *int, argsSeen *[]string) {
	regFake(reg, captureActionName, func(_ context.Context, args json.RawMessage) (Outcome, error) {
		idx := *calls
		*calls++
		if argsSeen != nil {
			*argsSeen = append(*argsSeen, string(args))
		}
		if idx >= len(imgs) {
			idx = len(imgs) - 1
		}
		return Outcome{Data: map[string]any{"bytes": float64(len(imgs[idx]))}, Images: []Image{{Data: imgs[idx], MIMEType: "image/png"}}}, nil
	}, true)
}

func untilStep(id string, w WaitSpec) Step { return Step{ID: id, Wait: &w} }

// ---- validation -----------------------------------------------------------

func TestValidateWaitUntilTable(t *testing.T) {
	reg := regWithCapture()
	cases := []struct {
		name  string
		steps []Step
		want  string
	}{
		{"both", []Step{untilStep("w", WaitSpec{Seconds: 1, Until: "settled"})}, "exactly one of seconds or until"},
		{"neither", []Step{untilStep("w", WaitSpec{})}, "exactly one of seconds or until"},
		{"bad until", []Step{untilStep("w", WaitSpec{Until: "sparkly"})}, "must be"},
		{"timeout low", []Step{untilStep("w", WaitSpec{Until: "settled", TimeoutSeconds: 0.1})}, "timeout_seconds"},
		{"timeout high", []Step{untilStep("w", WaitSpec{Until: "settled", TimeoutSeconds: 121})}, "timeout_seconds"},
		{"poll low", []Step{untilStep("w", WaitSpec{Until: "settled", PollMS: 100})}, "poll_ms"},
		{"poll high", []Step{untilStep("w", WaitSpec{Until: "settled", PollMS: 6000})}, "poll_ms"},
		{"stable high", []Step{untilStep("w", WaitSpec{Until: "settled", StablePolls: 6})}, "stable_polls"},
		{"stable negative", []Step{untilStep("w", WaitSpec{Until: "settled", StablePolls: -1})}, "stable_polls"},
		{"dimension low", []Step{untilStep("w", WaitSpec{Until: "settled", MaxDimension: 100})}, "max_dimension"},
		{"dimension high", []Step{untilStep("w", WaitSpec{Until: "settled", MaxDimension: 5000})}, "max_dimension"},
		{"threshold", []Step{untilStep("w", WaitSpec{Until: "settled", ThresholdPercent: untilF64(101)})}, "threshold_percent"},
		{"tolerance", []Step{untilStep("w", WaitSpec{Until: "settled", Tolerance: untilInt(256)})}, "tolerance"},
		{"settled with from", []Step{{ID: "c", Tool: "fake_capture"}, untilStep("w", WaitSpec{Until: "settled", From: "c"})}, "does not take from"},
		{"stable with changed", []Step{untilStep("w", WaitSpec{Until: "changed", StablePolls: 2})}, "only valid with until settled"},
		{"unknown from", []Step{untilStep("w", WaitSpec{Until: "changed", From: "ghost"})}, "unknown step"},
		{"from later", []Step{untilStep("w", WaitSpec{Until: "changed", From: "c"}), {ID: "c", Tool: "fake_capture"}}, "not earlier"},
		{"from not capture", []Step{{ID: "e", Tool: "fake_exec"}, untilStep("w", WaitSpec{Until: "changed", From: "e"})}, "not a capture step"},
		{"until fields with seconds", []Step{untilStep("w", WaitSpec{Seconds: 1, PollMS: 300})}, "only valid with until"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectErrors(t, SequenceInput{Steps: tc.steps}, reg, fleetCtx, tc.want)
		})
	}
}

func TestValidateWaitUntilCaptureMissing(t *testing.T) {
	expectErrors(t, SequenceInput{Steps: []Step{untilStep("w", WaitSpec{Until: "settled"})}}, regWithFakes(), fleetCtx, "not available")
}

func TestValidateWaitUntilUnusableTarget(t *testing.T) {
	expectErrors(t, SequenceInput{Steps: []Step{untilStep("w", WaitSpec{Until: "settled"})}}, regWithCapture(),
		ValidateContext{Target: "", Fleet: true}, "usable VM target")
}

func TestValidateWaitUntilDefaultsAndInjection(t *testing.T) {
	reg := regWithCapture()
	plan := mustValidate(t, SequenceInput{Steps: []Step{
		{ID: "c", Tool: "fake_capture"},
		untilStep("w", WaitSpec{Until: "changed", From: "c"}),
		untilStep("s", WaitSpec{Until: "settled"}),
	}}, reg, fleetCtx)
	u := plan.Steps[1].Until
	if u == nil || u.From != "c" || u.Kind != untilChanged {
		t.Fatalf("until = %+v", u)
	}
	if u.Timeout.Seconds() != 10 || u.Poll.Milliseconds() != 500 || u.MaxDimension != 512 || u.Tolerance != 8 || u.Threshold != 0.1 {
		t.Fatalf("defaults = %+v", u)
	}
	args := string(u.CaptureArgs)
	if !strings.Contains(args, `"vm_id":"vm1"`) || !strings.Contains(args, `"max_dimension":512`) {
		t.Fatalf("capture args = %s", args)
	}
	if s := plan.Steps[2].Until; s == nil || s.StablePolls != 2 {
		t.Fatalf("settled = %+v", s)
	}
	// VM mode: no vm_id injected.
	plan = mustValidate(t, SequenceInput{Steps: []Step{untilStep("s", WaitSpec{Until: "settled"})}}, reg, vmCtx)
	if strings.Contains(string(plan.Steps[0].Until.CaptureArgs), "vm_id") {
		t.Fatalf("vm mode args = %s", plan.Steps[0].Until.CaptureArgs)
	}
}

func TestValidateWaitUntilBudget(t *testing.T) {
	expectErrors(t, SequenceInput{
		TimeoutSeconds: 60,
		Steps:          []Step{untilStep("w", WaitSpec{Until: "settled", TimeoutSeconds: 100})},
	}, regWithCapture(), fleetCtx, "declared waits need")
	// Worst cases add up with fixed waits.
	expectErrors(t, SequenceInput{
		TimeoutSeconds: 10,
		Steps: []Step{
			untilStep("w", WaitSpec{Until: "settled", TimeoutSeconds: 6}),
			{Wait: &WaitSpec{Seconds: 5}},
		},
	}, regWithCapture(), fleetCtx, "declared waits need")
}

func TestValidateImageChangedMayReferenceWaitUntil(t *testing.T) {
	mustValidate(t, SequenceInput{Steps: []Step{
		{ID: "c1", Tool: "fake_capture"},
		untilStep("w1", WaitSpec{Until: "settled"}),
		{ID: "g", Gate: []Check{{Check: "image_changed", From: "c1", To: "w1"}}},
	}}, regWithCapture(), fleetCtx)
}

// ---- runner ---------------------------------------------------------------

func untilRun(t *testing.T, in SequenceInput, reg *Registry) (Report, *testClock) {
	t.Helper()
	plan := mustPlan(t, in, reg, fleetCtx)
	r, clock := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	return report, clock
}

func resultOutput(t *testing.T, res StepResult) map[string]any {
	t.Helper()
	m, ok := res.Output.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v", res.Output)
	}
	return m
}

func TestRunnerWaitUntilSettled(t *testing.T) {
	w := pngBytes(t, 8, 8, colorWhite)
	b := pngBytes(t, 8, 8, colorBlack)
	reg := NewRegistry()
	calls := 0
	var argsSeen []string
	scriptedCapture(reg, [][]byte{w, b, b, b}, &calls, &argsSeen)
	report, clock := untilRun(t, SequenceInput{Steps: []Step{untilStep("w", WaitSpec{Until: "settled"})}}, reg)
	out := report.Output
	if out.Status != StatusCompleted || out.StepsRun != 1 {
		t.Fatalf("status=%s steps_run=%d", out.Status, out.StepsRun)
	}
	res := out.Results[0]
	o := resultOutput(t, res)
	if res.Status != StepOK || res.Kind != "wait" || o["until"] != "settled" || o["met"] != true || o["polls"] != 4 {
		t.Fatalf("result = %+v output = %v", res, o)
	}
	if calls != 4 || len(clock.sleeps) != 3 {
		t.Fatalf("calls=%d sleeps=%v", calls, clock.sleeps)
	}
	if res.Waited != 1.5 || o["elapsed_ms"] != int64(1500) {
		t.Fatalf("waited=%v elapsed=%v", res.Waited, o["elapsed_ms"])
	}
	if !strings.Contains(argsSeen[0], `"vm_id":"vm1"`) || !strings.Contains(argsSeen[0], `"max_dimension":512`) {
		t.Fatalf("capture args = %s", argsSeen[0])
	}
	// Polling is not a step and never returns images.
	if len(report.ReturnedImages) != 0 || out.ImageCount != 0 || res.ImageIndex != nil {
		t.Fatalf("images leaked: %+v", report)
	}
}

func TestRunnerWaitUntilSettledTimeout(t *testing.T) {
	w := pngBytes(t, 8, 8, colorWhite)
	b := pngBytes(t, 8, 8, colorBlack)
	reg := NewRegistry()
	calls := 0
	scriptedCapture(reg, [][]byte{w, b, w, b, w, b, w, b, w, b, w, b, w, b}, &calls, nil)
	regFake(reg, "fake_exec", func(context.Context, json.RawMessage) (Outcome, error) { return execOut(0, "x"), nil }, false)
	report, _ := untilRun(t, SequenceInput{Steps: []Step{
		untilStep("w", WaitSpec{Until: "settled", TimeoutSeconds: 1, PollMS: 200}),
		{ID: "after", Tool: "fake_exec"},
	}}, reg)
	out := report.Output
	if out.Status != StatusStopped || out.StoppedAt == nil || *out.StoppedAt != 0 {
		t.Fatalf("status=%s stopped_at=%v", out.Status, out.StoppedAt)
	}
	res := out.Results[0]
	o := resultOutput(t, res)
	if res.Status != StepExpectationFailed || o["met"] != false || o["polls"] != 6 {
		t.Fatalf("result = %+v output = %v", res, o)
	}
	if len(res.Checks) != 1 || res.Checks[0].Passed || res.Checks[0].Check != "wait_until" {
		t.Fatalf("checks = %+v", res.Checks)
	}
	if out.Results[1].Status != StepNotRun {
		t.Fatalf("after = %+v", out.Results[1])
	}
}

func TestRunnerWaitUntilOnFailContinue(t *testing.T) {
	w := pngBytes(t, 8, 8, colorWhite)
	b := pngBytes(t, 8, 8, colorBlack)
	reg := NewRegistry()
	calls := 0
	scriptedCapture(reg, [][]byte{w, b, w, b, w, b, w, b}, &calls, nil)
	ran := 0
	regFake(reg, "fake_exec", func(context.Context, json.RawMessage) (Outcome, error) {
		ran++
		return execOut(0, "x"), nil
	}, false)
	w1 := WaitSpec{Until: "settled", TimeoutSeconds: 1, PollMS: 200}
	report, _ := untilRun(t, SequenceInput{Steps: []Step{
		{ID: "w", Wait: &w1, OnFail: "continue"},
		{ID: "after", Tool: "fake_exec"},
	}}, reg)
	if report.Output.Status != StatusCompletedWithFailures || ran != 1 {
		t.Fatalf("status=%s ran=%d", report.Output.Status, ran)
	}
	if report.Output.Results[0].Status != StepExpectationFailed {
		t.Fatalf("wait = %+v", report.Output.Results[0])
	}
}

func TestRunnerWaitUntilChangedFromStartBaseline(t *testing.T) {
	w := pngBytes(t, 8, 8, colorWhite)
	b := pngBytes(t, 8, 8, colorBlack)
	reg := NewRegistry()
	calls := 0
	scriptedCapture(reg, [][]byte{w, w, b}, &calls, nil)
	report, _ := untilRun(t, SequenceInput{Steps: []Step{untilStep("w", WaitSpec{Until: "changed"})}}, reg)
	res := report.Output.Results[0]
	o := resultOutput(t, res)
	if res.Status != StepOK || o["met"] != true || o["polls"] != 3 || calls != 3 {
		t.Fatalf("result = %+v output = %v calls=%d", res, o, calls)
	}
	if pct, _ := o["last_changed_percent"].(float64); pct != 100 {
		t.Fatalf("last_changed_percent = %v", o["last_changed_percent"])
	}
}

func TestRunnerWaitUntilChangedFromStep(t *testing.T) {
	w := pngBytes(t, 8, 8, colorWhite)
	b := pngBytes(t, 8, 8, colorBlack)
	reg := NewRegistry()
	calls := 0
	// Call 1 is the visible capture step; calls 2+ are polls.
	scriptedCapture(reg, [][]byte{w, w, b}, &calls, nil)
	report, _ := untilRun(t, SequenceInput{Steps: []Step{
		{ID: "c1", Tool: captureActionName},
		untilStep("w", WaitSpec{Until: "changed", From: "c1"}),
		{ID: "g", Gate: []Check{{Check: "image_changed", From: "c1", To: "w"}}},
	}}, reg)
	out := report.Output
	if out.Status != StatusCompleted {
		t.Fatalf("status=%s reason=%s results=%+v", out.Status, out.StopReason, out.Results)
	}
	o := resultOutput(t, out.Results[1])
	if o["met"] != true || o["polls"] != 2 || calls != 3 {
		t.Fatalf("output = %v calls=%d", o, calls)
	}
	// Only the visible capture step returned an image; the gate could still
	// diff against the wait step's retained final poll.
	if len(report.ReturnedImages) != 1 || out.Results[2].Status != StepOK {
		t.Fatalf("images=%d gate=%+v", len(report.ReturnedImages), out.Results[2])
	}
}

func TestRunnerWaitUntilQuietAndWhen(t *testing.T) {
	w := pngBytes(t, 8, 8, colorWhite)
	reg := NewRegistry()
	calls := 0
	scriptedCapture(reg, [][]byte{w}, &calls, nil)
	ws := WaitSpec{Until: "settled"}
	report, _ := untilRun(t, SequenceInput{Steps: []Step{
		{ID: "q", Wait: &ws, Quiet: true},
		{ID: "skipme", Wait: &ws, When: []Check{{Check: "succeeded", Step: "q", Negate: true}}},
	}}, reg)
	out := report.Output
	if out.Results[0].Status != StepOK || out.Results[0].Output != nil {
		t.Fatalf("quiet result = %+v", out.Results[0])
	}
	if out.Results[1].Status != StepSkipped || calls != 3 {
		t.Fatalf("when result = %+v calls=%d", out.Results[1], calls)
	}
}

func TestRunnerWaitUntilCaptureError(t *testing.T) {
	for _, tc := range []struct {
		onError string
		status  string
		ran     int
	}{
		{"stop", StatusFailed, 0},
		{"continue", StatusCompletedWithFailures, 1},
	} {
		t.Run(tc.onError, func(t *testing.T) {
			reg := NewRegistry()
			regFake(reg, captureActionName, func(context.Context, json.RawMessage) (Outcome, error) {
				return Outcome{}, errors.New("prlctl exploded")
			}, true)
			ran := 0
			regFake(reg, "fake_exec", func(context.Context, json.RawMessage) (Outcome, error) {
				ran++
				return execOut(0, "x"), nil
			}, false)
			report, _ := untilRun(t, SequenceInput{OnError: tc.onError, Steps: []Step{
				untilStep("w", WaitSpec{Until: "settled"}),
				{ID: "after", Tool: "fake_exec"},
			}}, reg)
			if report.Output.Status != tc.status || ran != tc.ran {
				t.Fatalf("status=%s ran=%d", report.Output.Status, ran)
			}
			res := report.Output.Results[0]
			if res.Status != StepError || !strings.Contains(res.Error, "prlctl exploded") {
				t.Fatalf("result = %+v", res)
			}
		})
	}
}

func TestRunnerWaitUntilBaselineMissing(t *testing.T) {
	// The referenced capture step errored (continue), so it has no image.
	w := pngBytes(t, 8, 8, colorWhite)
	reg := NewRegistry()
	n := 0
	regFake(reg, captureActionName, func(context.Context, json.RawMessage) (Outcome, error) {
		n++
		if n == 1 {
			return Outcome{}, errors.New("boom")
		}
		return Outcome{Images: []Image{{Data: w, MIMEType: "image/png"}}}, nil
	}, true)
	report, _ := untilRun(t, SequenceInput{OnError: "continue", Steps: []Step{
		{ID: "c1", Tool: captureActionName},
		untilStep("w", WaitSpec{Until: "changed", From: "c1"}),
	}}, reg)
	res := report.Output.Results[1]
	if res.Status != StepError || !strings.Contains(res.Error, "no image") {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunnerWaitUntilCancellation(t *testing.T) {
	w := pngBytes(t, 8, 8, colorWhite)
	reg := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	regFake(reg, captureActionName, func(c context.Context, _ json.RawMessage) (Outcome, error) {
		calls++
		if calls == 2 {
			cancel()
			return Outcome{}, c.Err()
		}
		return Outcome{Images: []Image{{Data: w, MIMEType: "image/png"}}}, nil
	}, true)
	plan := mustPlan(t, SequenceInput{Steps: []Step{untilStep("w", WaitSpec{Until: "settled", StablePolls: 5})}}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	_, err := r.Run(ctx, plan)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRunnerWaitUntilSleepCancelled(t *testing.T) {
	w := pngBytes(t, 8, 8, colorWhite)
	reg := NewRegistry()
	calls := 0
	scriptedCapture(reg, [][]byte{w}, &calls, nil)
	plan := mustPlan(t, SequenceInput{Steps: []Step{untilStep("w", WaitSpec{Until: "settled", StablePolls: 5})}}, reg, fleetCtx)
	r, clock := newTestRunner(t, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock.sleepErr = func(time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err := r.Run(ctx, plan)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
