package orchestrator

import (
	"context"
	"encoding/json"
	"image/color"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	colorWhite = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	colorBlack = color.RGBA{R: 0, G: 0, B: 0, A: 255}
)

// ---- fakes ----------------------------------------------------------------

type fakeFn func(ctx context.Context, args json.RawMessage) (Outcome, error)

func regFake(reg *Registry, name string, fn fakeFn, producesImages bool) {
	reg.Register(Action{
		Name:           name,
		VMScoped:       true,
		ProducesImages: producesImages,
		ValidateArgs:   func(json.RawMessage) error { return nil },
		Run:            fn,
	})
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
	// sleepErr returns the error the fake Sleep reports (nil = success).
	sleepErr func(d time.Duration) error
	sleeps   []time.Duration
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestRunner(t *testing.T, reg *Registry) (*Runner, *testClock) {
	t.Helper()
	r := NewRunner(reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clock := &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	r.Now = func() time.Time {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		return clock.now
	}
	r.Sleep = func(_ context.Context, d time.Duration) error {
		clock.advance(d)
		clock.mu.Lock()
		clock.sleeps = append(clock.sleeps, d)
		clock.mu.Unlock()
		if clock.sleepErr != nil {
			return clock.sleepErr(d)
		}
		return nil
	}
	return r, clock
}

func execOut(exit int, stdout string) Outcome {
	return Outcome{Data: map[string]any{"exit_code": float64(exit), "stdout": stdout, "stderr": ""}}
}

// mustPlan builds a plan from a SequenceInput with the fakes registry.
func mustPlan(t *testing.T, in SequenceInput, reg *Registry, ctx ValidateContext) *Plan {
	t.Helper()
	return mustValidate(t, in, reg, ctx)
}

// ---- tests ----------------------------------------------------------------

func TestRunnerHappyPath(t *testing.T) {
	reg := NewRegistry()
	var calls []string
	regFake(reg, "fake_exec", func(_ context.Context, args json.RawMessage) (Outcome, error) {
		var a struct {
			VMID    string `json:"vm_id"`
			Command string `json:"command"`
		}
		_ = json.Unmarshal(args, &a)
		calls = append(calls, a.VMID+"/"+a.Command)
		return execOut(0, "ok"), nil
	}, false)
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{
			{ID: "a", Tool: "fake_exec", Args: map[string]any{"command": "ls"}},
			{Wait: &WaitSpec{Seconds: 2}},
			{ID: "b", Tool: "fake_exec", Args: map[string]any{"command": "pwd"}},
		},
	}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	var prog []string
	r.Progress = func(_, _ int, msg string) { prog = append(prog, msg) }

	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusCompleted || out.StepsRun != 3 {
		t.Fatalf("status=%s steps_run=%d", out.Status, out.StepsRun)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results = %+v", out.Results)
	}
	if out.Results[1].Status != StepOK || out.Results[1].Waited != 2 {
		t.Fatalf("wait result = %+v", out.Results[1])
	}
	// Duration includes the 2 s wait (fake clock).
	if out.DurationMS != 2000 {
		t.Fatalf("duration = %d ms, want 2000", out.DurationMS)
	}
	// Fleet injection reached the handler.
	if len(calls) != 2 || calls[0] != "vm1/ls" || calls[1] != "vm1/pwd" {
		t.Fatalf("calls = %v", calls)
	}
	// Progress called per step, no args in messages.
	if len(prog) != 3 || strings.ContainsAny(prog[0], "{}") || !strings.Contains(prog[0], "fake_exec") {
		t.Fatalf("prog = %v", prog)
	}
	// Output kept in the report.
	if out.Results[0].Output.(map[string]any)["stdout"] != "ok" {
		t.Fatalf("output = %+v", out.Results[0].Output)
	}
}

func TestRunnerOnErrorCodeStopAndContinue(t *testing.T) {
	reg := NewRegistry()
	regFake(reg, "bad", func(context.Context, json.RawMessage) (Outcome, error) {
		return Outcome{}, context.DeadlineExceeded
	}, false)
	regFake(reg, "good", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(0, "fine"), nil
	}, false)

	// Default: stop.
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{
			{ID: "b1", Tool: "bad"},
			{ID: "g1", Tool: "good"},
		},
	}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusFailed || out.StoppedAt == nil || *out.StoppedAt != 0 {
		t.Fatalf("status=%s stopped_at=%v reason=%s", out.Status, out.StoppedAt, out.StopReason)
	}
	if out.Results[0].Status != StepError || !strings.Contains(out.Results[0].Error, "deadline exceeded") {
		t.Fatalf("step0 = %+v", out.Results[0])
	}
	if out.Results[1].Status != StepNotRun {
		t.Fatalf("step1 = %+v", out.Results[1])
	}
	if !strings.Contains(out.StopReason, "b1") {
		t.Fatalf("reason = %q", out.StopReason)
	}

	// on_error: continue.
	plan2 := mustPlan(t, SequenceInput{
		OnError: "continue",
		Steps: []Step{
			{ID: "b1", Tool: "bad"},
			{ID: "g1", Tool: "good"},
		},
	}, reg, fleetCtx)
	report, err = r.Run(context.Background(), plan2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Output.Status != StatusCompletedWithFailures || report.Output.StepsRun != 2 {
		t.Fatalf("status=%s steps_run=%d", report.Output.Status, report.Output.StepsRun)
	}
	if report.Output.Results[1].Status != StepOK {
		t.Fatalf("step1 = %+v", report.Output.Results[1])
	}
}

func TestRunnerExpectFailStopAndContinue(t *testing.T) {
	reg := NewRegistry()
	regFake(reg, "flaky", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(1, "boom"), nil
	}, false)

	in := SequenceInput{
		Steps: []Step{
			{ID: "f1", Tool: "flaky", Expect: []Check{{Check: "exit_code"}}, OnFail: "stop"},
			{ID: "w1", Wait: &WaitSpec{Seconds: 1}},
		},
	}
	plan := mustPlan(t, in, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusStopped || out.StoppedAt == nil || *out.StoppedAt != 0 {
		t.Fatalf("status=%s stopped_at=%v reason=%s", out.Status, out.StoppedAt, out.StopReason)
	}
	step0 := out.Results[0]
	if step0.Status != StepExpectationFailed || len(step0.Checks) != 1 || step0.Checks[0].Passed {
		t.Fatalf("step0 = %+v", step0)
	}
	if !strings.Contains(step0.Checks[0].Detail, "exit_code 1 (expected 0)") {
		t.Fatalf("detail = %q", step0.Checks[0].Detail)
	}
	if out.Results[1].Status != StepNotRun {
		t.Fatalf("step1 = %+v", out.Results[1])
	}

	// on_fail: continue.
	in.Steps[0].OnFail = "continue"
	plan2 := mustPlan(t, in, reg, fleetCtx)
	report, err = r.Run(context.Background(), plan2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Output.Status != StatusCompletedWithFailures {
		t.Fatalf("status = %s", report.Output.Status)
	}
}

func TestRunnerRetryUntilExpectPasses(t *testing.T) {
	reg := NewRegistry()
	calls := 0
	regFake(reg, "flaky", func(context.Context, json.RawMessage) (Outcome, error) {
		calls++
		if calls < 3 {
			return execOut(1, "not yet"), nil
		}
		return execOut(0, "done"), nil
	}, false)
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{
			{ID: "f1", Tool: "flaky", Expect: []Check{{Check: "exit_code"}}, Retry: &Retry{MaxAttempts: 5, DelayMS: 500}},
		},
	}, reg, fleetCtx)
	r, clock := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusCompleted {
		t.Fatalf("status = %s", out.Status)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if out.Results[0].Attempts != 3 || out.Results[0].Status != StepOK {
		t.Fatalf("step0 = %+v", out.Results[0])
	}
	if len(clock.sleeps) != 2 || clock.sleeps[0] != 500*time.Millisecond {
		t.Fatalf("sleeps = %v", clock.sleeps)
	}
	// Duration reflects the retry sleeps.
	if out.DurationMS != 1000 {
		t.Fatalf("duration = %d ms, want 1000", out.DurationMS)
	}
}

func TestRunnerRetryExhausted(t *testing.T) {
	reg := NewRegistry()
	regFake(reg, "always_bad", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(2, "nope"), nil
	}, false)
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{
			{ID: "a1", Tool: "always_bad", Expect: []Check{{Check: "exit_code"}}, Retry: &Retry{MaxAttempts: 2, DelayMS: 500}, OnFail: "continue"},
			{ID: "n1", Wait: &WaitSpec{Seconds: 1}},
		},
	}, reg, fleetCtx)
	r, clock := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusCompletedWithFailures {
		t.Fatalf("status = %s", out.Status)
	}
	if out.Results[0].Status != StepExpectationFailed || out.Results[0].Attempts != 2 {
		t.Fatalf("step0 = %+v", out.Results[0])
	}
	// Last attempt's output is retained in the report.
	if out.Results[0].Output.(map[string]any)["exit_code"] != float64(2) {
		t.Fatalf("output = %+v", out.Results[0].Output)
	}
	// One retry delay (500 ms) plus the final wait step (1 s).
	if len(clock.sleeps) != 2 || clock.sleeps[0] != 500*time.Millisecond || clock.sleeps[1] != time.Second {
		t.Fatalf("sleeps = %v", clock.sleeps)
	}
}

func TestRunnerWhenSkips(t *testing.T) {
	reg := NewRegistry()
	regFake(reg, "exit_one", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(1, ""), nil
	}, false)
	regFake(reg, "runs_tool", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(0, "ran"), nil
	}, false)
	regFake(reg, "skipped_tool", func(context.Context, json.RawMessage) (Outcome, error) {
		t.Error("skipped step must not run")
		return Outcome{}, nil
	}, false)
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{
			{ID: "e1", Tool: "exit_one"},
			{ID: "k1", Tool: "runs_tool", When: []Check{{Check: "exit_code", Step: "e1", Equals: intPtr(1)}}},
			{ID: "k2", Tool: "skipped_tool", When: []Check{{Check: "exit_code", Step: "e1"}}},
		},
	}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	// k1's when passes (exit_code 1 == 1) → runs; k2's fails (1 != 0) → skipped.
	if out.Status != StatusCompleted || out.StepsRun != 2 {
		t.Fatalf("status=%s steps_run=%d", out.Status, out.StepsRun)
	}
	if out.Results[1].Status != StepOK || out.Results[1].Checks[0].Passed != true {
		t.Fatalf("step1 = %+v", out.Results[1])
	}
	if out.Results[2].Status != StepSkipped || out.Results[2].Checks[0].Passed != false {
		t.Fatalf("step2 = %+v", out.Results[2])
	}
}

func TestRunnerGateStops(t *testing.T) {
	reg := NewRegistry()
	regFake(reg, "exit_one", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(1, ""), nil
	}, false)
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{
			{ID: "e1", Tool: "exit_one"},
			{ID: "g1", Gate: []Check{{Check: "exit_code", Step: "e1"}}},
			{ID: "w1", Wait: &WaitSpec{Seconds: 1}},
		},
	}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusStopped || out.StoppedAt == nil || *out.StoppedAt != 1 {
		t.Fatalf("status=%s stopped_at=%v reason=%s", out.Status, out.StoppedAt, out.StopReason)
	}
	if !strings.Contains(out.StopReason, "gate") {
		t.Fatalf("reason = %q", out.StopReason)
	}
	if out.Results[2].Status != StepNotRun {
		t.Fatalf("step2 = %+v", out.Results[2])
	}
}

func TestRunnerWaitTimeout(t *testing.T) {
	reg := NewRegistry()
	plan := &Plan{
		VMID:    "vm1",
		Timeout: 50 * time.Millisecond,
		OnError: "stop",
		Steps: []planStep{
			{ID: "w1", Kind: "wait", Wait: 5 * time.Second, OnFail: "stop", OnError: "stop"},
		},
	}
	r, _ := newTestRunner(t, reg)
	// Use the REAL ctx-aware sleep so the 50 ms timeout actually fires.
	r.Sleep = sleepCtx
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusTimeout || !strings.Contains(out.StopReason, "wait step w1") {
		t.Fatalf("status=%s reason=%s", out.Status, out.StopReason)
	}
	if out.Results[0].Status != StepNotRun {
		t.Fatalf("step0 = %+v", out.Results[0])
	}
}

func TestRunnerToolTimeout(t *testing.T) {
	reg := NewRegistry()
	regFake(reg, "slow", func(ctx context.Context, _ json.RawMessage) (Outcome, error) {
		select {
		case <-ctx.Done():
			return Outcome{}, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return execOut(0, "late"), nil
		}
	}, false)
	plan := &Plan{
		VMID:    "vm1",
		Timeout: 50 * time.Millisecond,
		OnError: "stop",
		Steps: []planStep{
			{ID: "s1", Kind: "tool", Action: mustGet(t, reg, "slow"), Args: []byte("{}"), OnFail: "stop", OnError: "stop"},
			{ID: "s2", Kind: "wait", Wait: time.Second, OnFail: "stop", OnError: "stop"},
		},
	}
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusTimeout || out.StoppedAt == nil || *out.StoppedAt != 0 {
		t.Fatalf("status=%s stopped_at=%v reason=%s", out.Status, out.StoppedAt, out.StopReason)
	}
	if out.Results[0].Status != StepNotRun || out.Results[1].Status != StepNotRun {
		t.Fatalf("results = %+v", out.Results)
	}
}

func TestRunnerClientCancellation(t *testing.T) {
	reg := NewRegistry()
	started := make(chan struct{})
	regFake(reg, "blocky", func(ctx context.Context, _ json.RawMessage) (Outcome, error) {
		close(started)
		<-ctx.Done()
		return Outcome{}, ctx.Err()
	}, false)
	plan := &Plan{
		VMID:    "vm1",
		Timeout: time.Minute,
		OnError: "stop",
		Steps: []planStep{
			{ID: "s1", Kind: "tool", Action: mustGet(t, reg, "blocky"), Args: []byte("{}"), OnFail: "stop", OnError: "stop"},
		},
	}
	r, _ := newTestRunner(t, reg)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- func() error { _, err := r.Run(ctx, plan); return err }() }()
	<-started
	cancel()
	select {
	case err := <-errCh:
		if err == nil || err != context.Canceled {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not return")
	}
}

func TestRunnerImageBudgetsAndDiff(t *testing.T) {
	a := pngBytes(t, 8, 8, colorWhite)
	b := pngBytes(t, 8, 8, colorBlack)
	reg := NewRegistry()
	seq := 0
	regFake(reg, "fake_capture", func(context.Context, json.RawMessage) (Outcome, error) {
		seq++
		data := a
		if seq == 2 {
			data = b
		}
		return Outcome{Data: map[string]any{"bytes": float64(len(data))}, Images: []Image{{Data: data, MIMEType: "image/png"}}}, nil
	}, true)
	plan := mustPlan(t, SequenceInput{
		MaxImages: 1,
		Steps: []Step{
			{ID: "c1", Tool: "fake_capture"},
			{ID: "c2", Tool: "fake_capture"},
			{ID: "g1", Gate: []Check{{Check: "image_changed", From: "c1", To: "c2"}}},
		},
	}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusCompleted {
		t.Fatalf("status=%s reason=%s", out.Status, out.StopReason)
	}
	if out.ImageCount != 1 || len(report.ReturnedImages) != 1 {
		t.Fatalf("image_count=%d returned=%d", out.ImageCount, len(report.ReturnedImages))
	}
	if out.Results[0].ImageIndex == nil || *out.Results[0].ImageIndex != 0 {
		t.Fatalf("c1 image_index = %v", out.Results[0].ImageIndex)
	}
	if !out.Results[1].ImageDropped {
		t.Fatalf("c2 should be dropped: %+v", out.Results[1])
	}
	// The dropped image was still usable by the gate (diff passed).
	if out.Results[2].Status != StepOK {
		t.Fatalf("gate = %+v", out.Results[2])
	}
}

func TestRunnerQuietStep(t *testing.T) {
	a := pngBytes(t, 8, 8, colorWhite)
	reg := NewRegistry()
	seq := 0
	regFake(reg, "fake_capture", func(context.Context, json.RawMessage) (Outcome, error) {
		seq++
		data := a
		if seq == 2 {
			data = pngBytes(t, 8, 8, colorBlack)
		}
		return Outcome{Data: map[string]any{"note": "secret-stdout"}, Images: []Image{{Data: data, MIMEType: "image/png"}}}, nil
	}, true)
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{
			{ID: "c1", Tool: "fake_capture", Quiet: true},
			{ID: "c2", Tool: "fake_capture", Quiet: true, Expect: []Check{{Check: "image_changed", From: "c1"}}},
		},
	}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	if out.Status != StatusCompleted {
		t.Fatalf("status=%s reason=%s", out.Status, out.StopReason)
	}
	if out.Results[0].Output != nil || out.Results[0].ImageIndex != nil {
		t.Fatalf("quiet c1 must hide output/image: %+v", out.Results[0])
	}
	if out.Results[1].Output != nil || out.Results[1].ImageIndex != nil {
		t.Fatalf("quiet c2 must hide output/image: %+v", out.Results[1])
	}
	// The expect on the quiet step still saw the image (diff changed → pass).
	if out.Results[1].Checks[0].Passed != true {
		t.Fatalf("quiet expect = %+v", out.Results[1].Checks)
	}
}

func TestRunnerTruncation(t *testing.T) {
	big := strings.Repeat("x", 70*1024)
	reg := NewRegistry()
	regFake(reg, "chatty", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(0, big), nil
	}, false)
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{{ID: "c1", Tool: "chatty"}},
	}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	m := out.Results[0].Output.(map[string]any)
	s, _ := m["stdout"].(string)
	if len(s) != reportStringCap {
		t.Fatalf("stdout len = %d, want %d", len(s), reportStringCap)
	}
	if m["stdout_truncated_in_report"] != true {
		t.Fatalf("marker missing: %+v", m)
	}
}

func TestTruncatorGlobalCap(t *testing.T) {
	// 16 outputs of 64 KB accumulate just under 1 MB; the 17th must be cut
	// to the 1 KB cap.
	// 70 KB: strictly longer than the 64 KB cap, so the marker appears.
	big := strings.Repeat("y", 64*1024+1)
	tr := &truncator{}
	// 16 stored strings of 64 KB reach exactly 1 MB; the 17th is still
	// capped at 64 KB (the total only EXCEEDS 1 MB once it is stored), so
	// the 1 KB cap applies from the 18th onward.
	for i := 0; i < 17; i++ {
		m := tr.truncate(map[string]any{"stdout": big}).(map[string]any)
		if len(m["stdout"].(string)) != reportStringCap {
			t.Fatalf("output %d stdout len = %d, want %d", i, len(m["stdout"].(string)), reportStringCap)
		}
		if m["stdout_truncated_in_report"] != true {
			t.Fatalf("output %d missing marker", i)
		}
	}
	m := tr.truncate(map[string]any{"stdout": big}).(map[string]any)
	if len(m["stdout"].(string)) != reportTrimCap {
		t.Fatalf("18th stdout len = %d, want %d", len(m["stdout"].(string)), reportTrimCap)
	}
	// Small strings are untouched and carry no marker.
	small := tr.truncate(map[string]any{"s": "hi", "n": float64(1)}).(map[string]any)
	if small["s"] != "hi" || small["n"] != float64(1) {
		t.Fatalf("small = %+v", small)
	}
	if _, has := small["s_truncated_in_report"]; has {
		t.Fatal("small string must not carry a marker")
	}
}

func TestRunnerTruncationWithinGlobalCap(t *testing.T) {
	// 15 outputs of 64 KB stay under the 1 MB JSON cap, so no further
	// trimming happens.
	big := strings.Repeat("y", 64*1024)
	reg := NewRegistry()
	regFake(reg, "chatty", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(0, big), nil
	}, false)
	steps := make([]Step, 0, 15)
	for i := 0; i < 15; i++ {
		steps = append(steps, Step{ID: "c" + string(rune('a'+i)), Tool: "chatty"})
	}
	plan := mustPlan(t, SequenceInput{Steps: steps}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	out := report.Output
	for i, res := range out.Results {
		if got := len(res.Output.(map[string]any)["stdout"].(string)); got != reportStringCap {
			t.Fatalf("step %d stdout len = %d, want %d", i, got, reportStringCap)
		}
	}
	if n := len(mustMarshal(t, out)); n > reportJSONCap {
		t.Fatalf("report JSON = %d bytes, want <= %d", n, reportJSONCap)
	}
}

func TestRunnerFinalJSONHardCap(t *testing.T) {
	// 20 outputs of 64 KB overshoot 1 MB: the safety valve must bring the
	// final JSON under the cap.
	big := strings.Repeat("z", 64*1024)
	reg := NewRegistry()
	regFake(reg, "chatty", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(0, big), nil
	}, false)
	steps := make([]Step, 0, 20)
	for i := 0; i < 20; i++ {
		steps = append(steps, Step{ID: "c" + string(rune('a'+i)), Tool: "chatty"})
	}
	plan := mustPlan(t, SequenceInput{Steps: steps}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(mustMarshal(t, report.Output)); n > reportJSONCap {
		t.Fatalf("report JSON = %d bytes, want <= %d", n, reportJSONCap)
	}
}

func TestRunnerSequenceLockSerializes(t *testing.T) {
	reg := NewRegistry()
	var mu sync.Mutex
	inside := 0
	maxInside := 0
	entered := make(chan struct{}, 2)
	regFake(reg, "fake_exec", func(context.Context, json.RawMessage) (Outcome, error) {
		mu.Lock()
		inside++
		if inside > maxInside {
			maxInside = inside
		}
		mu.Unlock()
		entered <- struct{}{}
		time.Sleep(30 * time.Millisecond) // real sleep inside the step
		mu.Lock()
		inside--
		mu.Unlock()
		return execOut(0, "ok"), nil
	}, false)
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{{ID: "e1", Tool: "fake_exec"}},
	}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Run(context.Background(), plan); err != nil {
				t.Error(err)
			}
		}()
	}
	<-entered
	<-entered
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("maxInside = %d, want 1 (per-VM lock)", maxInside)
	}
}

func TestRunnerSkippedStepNotCountedAndLocksReleased(t *testing.T) {
	reg := NewRegistry()
	regFake(reg, "fake_exec", func(context.Context, json.RawMessage) (Outcome, error) {
		return execOut(7, ""), nil
	}, false)
	plan := mustPlan(t, SequenceInput{
		Steps: []Step{
			{ID: "e1", Tool: "fake_exec"},
			{ID: "s1", Tool: "fake_exec", When: []Check{{Check: "exit_code", Step: "e1"}}},
		},
	}, reg, fleetCtx)
	r, _ := newTestRunner(t, reg)
	report, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Output.StepsRun != 1 {
		t.Fatalf("steps_run = %d, want 1", report.Output.StepsRun)
	}
	// Lock released: another sequence on the same VM runs immediately.
	report2, err := r.Run(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	_ = report2
}

func mustGet(t *testing.T, reg *Registry, name string) Action {
	t.Helper()
	a, ok := reg.Get(name)
	if !ok {
		t.Fatal("missing action " + name)
	}
	return a
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
