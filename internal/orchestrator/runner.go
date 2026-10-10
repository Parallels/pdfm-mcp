package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Report and image budgets.
const (
	reportStringCap = 64 << 10 // per-field string cap before the global cap is hit
	reportTotalCap  = 1 << 20  // accumulated report string bytes before the 1 KB rule kicks in
	reportTrimCap   = 1 << 10  // per-field string cap after the global cap is hit
	imagesRetained  = 64 << 20 // max bytes of images kept in run state
	imagesReturned  = 24 << 20 // max bytes of returned image content
	reportJSONCap   = 1 << 20  // hard cap for the final report JSON
)

// ProgressFn reports progress (done, total, message). No args in the message.
type ProgressFn func(done, total int, msg string)

// Runner executes validated plans.
type Runner struct {
	Reg    *Registry
	Logger *slog.Logger
	// Sleep is the context-aware wait used for wait steps and retry delays.
	Sleep func(ctx context.Context, d time.Duration) error
	// Now is the clock; tests fake it.
	Now func() time.Time
	// Progress is called before each step starts (done = steps started so
	// far). May be nil.
	Progress ProgressFn

	locks *vmLocks
}

// NewRunner returns a Runner over the given registry.
func NewRunner(reg *Registry, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{Reg: reg, Logger: logger, Sleep: sleepCtx, Now: time.Now, locks: newVMLocks()}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Run executes the plan. It returns a tool-level error only when the caller
// (client) context is cancelled; timeouts and step failures are reported
// inside the Report.
func (r *Runner) Run(ctx context.Context, plan *Plan) (Report, error) {
	if err := r.locks.acquire(ctx, plan.VMID); err != nil {
		return Report{}, err
	}
	defer r.locks.release(plan.VMID)

	runCtx, cancel := context.WithTimeout(ctx, plan.Timeout)
	defer cancel()

	st := newState()
	start := r.Now()
	report := Report{Output: SequenceOutput{VMID: plan.VMID}}
	results := make([]StepResult, len(plan.Steps))
	trunc := &truncator{}

	stepsRun := 0
	reportedImages := 0
	retainedBytes := int64(0)
	seenImageSteps := map[string]bool{}
	hadFailure := false
	stopKind := "" // "" | "stopped" | "failed" | "timeout"
	stopReason := ""
	stopIndex := -1

	// markRemaining marks all later steps as not run (used after a stop).
	markRemaining := func(from int) {
		for i := from; i < len(plan.Steps); i++ {
			ps := plan.Steps[i]
			if results[i].Status == "" {
				results[i] = StepResult{Index: i, ID: ps.ID, Kind: ps.Kind, Tool: ps.ToolName, Status: StepNotRun, Error: "not run"}
			}
		}
	}

	for i, ps := range plan.Steps {
		if stopKind != "" || runCtx.Err() != nil {
			if stopKind == "" && runCtx.Err() != nil {
				stopKind = "timeout"
				stopReason = fmt.Sprintf("timeout after %s", plan.Timeout)
				stopIndex = i
			}
			results[i] = StepResult{Index: i, ID: ps.ID, Kind: ps.Kind, Tool: ps.ToolName, Status: StepNotRun, Error: "not run"}
			continue
		}

		if r.Progress != nil {
			r.Progress(i, len(plan.Steps), fmt.Sprintf("step %d/%d: %s", i+1, len(plan.Steps), stepKindLabel(ps)))
		}
		stepStart := r.Now()

		// when: skip the step if any check fails. The passed checks are
		// kept on the executed result too, so the report shows why each
		// step ran.
		var whenChecks []CheckResult
		if len(ps.When) > 0 {
			whenChecks = evaluate(PhaseWhen, ps.ID, ps.When, st)
			if !allPassed(whenChecks) {
				r.Logger.Info("step skipped by when", "index", i, "id", ps.ID)
				results[i] = StepResult{Index: i, ID: ps.ID, Kind: ps.Kind, Tool: ps.ToolName, Status: StepSkipped, Checks: whenChecks}
				st.unavailable(ps.ID, StepSkipped)
				continue
			}
		}

		switch ps.Kind {
		case "wait":
			if ps.Until != nil {
				res := r.runWaitUntil(ctx, runCtx, i, ps, st, &retainedBytes, stepStart)
				if res.cancelled {
					return Report{}, ctx.Err()
				}
				if len(whenChecks) > 0 {
					res.result.Checks = append(whenChecks, res.result.Checks...)
				}
				results[i] = res.result
				stepsRun += res.ran
				hadFailure = hadFailure || res.failed
				if res.stop != "" {
					stopKind = res.stop
					stopReason = res.stopReason
					stopIndex = i
					markRemaining(i + 1)
				}
				continue
			}
			if err := r.Sleep(runCtx, ps.Wait); err != nil {
				if ctx.Err() != nil {
					return Report{}, ctx.Err()
				}
				stopKind = "timeout"
				stopReason = fmt.Sprintf("timeout during wait step %s", ps.ID)
				stopIndex = i
				results[i] = StepResult{Index: i, ID: ps.ID, Kind: ps.Kind, Status: StepNotRun, Error: "timeout"}
				markRemaining(i + 1)
				continue
			}
			stepsRun++
			results[i] = StepResult{Index: i, ID: ps.ID, Kind: ps.Kind, Status: StepOK, Checks: whenChecks, Waited: ps.Wait.Seconds(), DurationMS: elapsed(stepStart, r.Now())}

		case "gate":
			checks := evaluate(PhaseGate, ps.ID, ps.Gate, st)
			if !allPassed(checks) {
				hadFailure = true
				results[i] = StepResult{Index: i, ID: ps.ID, Kind: ps.Kind, Status: StepExpectationFailed, Checks: append(whenChecks, checks...), DurationMS: elapsed(stepStart, r.Now())}
				st.unavailable(ps.ID, StepExpectationFailed)
				if ps.OnFail == "stop" {
					stopKind = "stopped"
					stopReason = fmt.Sprintf("step %s: gate check %q failed", ps.ID, failedCheck(checks))
					stopIndex = i
					markRemaining(i + 1)
				}
				r.Logger.Info("gate failed", "index", i, "id", ps.ID, "on_fail", ps.OnFail)
				continue
			}
			stepsRun++
			results[i] = StepResult{Index: i, ID: ps.ID, Kind: ps.Kind, Status: StepOK, Checks: append(whenChecks, checks...), DurationMS: elapsed(stepStart, r.Now())}

		case "tool":
			res := r.runToolStep(ctx, runCtx, i, ps, st, plan.MaxImages, stepStart,
				&stepCounters{trunc: trunc, report: &report, imgCount: &reportedImages, retBytes: &retainedBytes, retSeen: seenImageSteps})
			if res.cancelled {
				return Report{}, ctx.Err()
			}
			if len(whenChecks) > 0 {
				res.result.Checks = append(whenChecks, res.result.Checks...)
			}
			results[i] = res.result
			stepsRun += res.ran
			hadFailure = hadFailure || res.failed
			if res.stop != "" {
				stopKind = res.stop
				stopReason = res.stopReason
				stopIndex = i
				markRemaining(i + 1)
			}
		}
	}

	out := report.Output
	out.Results = results
	out.StepsRun = stepsRun
	out.ImageCount = reportedImages
	out.DurationMS = elapsed(start, r.Now())
	switch stopKind {
	case "":
		out.Status = StatusCompleted
		if hadFailure {
			out.Status = StatusCompletedWithFailures
		}
	case "stopped", "failed", "timeout":
		out.Status = StatusStopped
		if stopKind == "failed" {
			out.Status = StatusFailed
		}
		if stopKind == "timeout" {
			out.Status = StatusTimeout
		}
		if stopIndex >= 0 {
			idx := stopIndex
			out.StoppedAt = &idx
		}
		out.StopReason = stopReason
	}
	fitReportJSON(&out)
	return Report{Output: out, ReturnedImages: report.ReturnedImages}, nil
}

// stepCounters threads the run-level counters into runToolStep.
type stepCounters struct {
	trunc    *truncator
	report   *Report
	imgCount *int
	retBytes *int64
	retSeen  map[string]bool
}

type toolStepResult struct {
	result     StepResult
	ran        int // 1 when at least one attempt executed
	failed     bool
	stop       string // "" | "stopped" | "failed" | "timeout"
	stopReason string
	cancelled  bool
}

func (r *Runner) runToolStep(ctx, runCtx context.Context, i int, ps planStep, st *state, maxImages int, start time.Time, ss *stepCounters) toolStepResult {
	maxAttempts := 1
	if ps.Retry != nil {
		maxAttempts = ps.Retry.MaxAttempts
	}

	var outcome Outcome
	var runErr error
	var expectResults []CheckResult
	attempts := 0
	passed := false

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts = attempt
		outcome, runErr = ps.Action.Run(runCtx, ps.Args)
		if runErr != nil {
			break
		}
		if len(ps.Expect) == 0 {
			passed = true
			break
		}
		// Make the attempt's output/image visible to its own checks.
		img := Image{}
		hasImg := false
		if len(outcome.Images) > 0 {
			img = outcome.Images[0]
			hasImg = true
		}
		st.record(ps.ID, StepOK, outcome.Data, img, hasImg)
		expectResults = evaluate(PhaseExpect, ps.ID, ps.Expect, st)
		if allPassed(expectResults) {
			passed = true
			break
		}
		if attempt < maxAttempts {
			if err := r.Sleep(runCtx, ps.Retry.Delay); err != nil {
				runErr = err // retry sleep cancelled or timed out
				break
			}
		}
	}

	tr := toolStepResult{result: StepResult{Index: i, ID: ps.ID, Kind: "tool", Tool: ps.ToolName, Attempts: attempts, DurationMS: elapsed(start, r.Now())}}
	if attempts > 0 {
		tr.ran = 1
	}

	// Cancellation first, then timeout, then a real tool error.
	if runErr != nil && ctx.Err() != nil {
		tr.cancelled = true
		return tr
	}
	if runErr != nil && runCtx.Err() != nil {
		tr.stop = "timeout"
		tr.stopReason = fmt.Sprintf("timeout during step %s", ps.ID)
		tr.result.Status = StepNotRun
		tr.result.Error = "timeout"
		st.unavailable(ps.ID, StepNotRun)
		return tr
	}
	if runErr != nil {
		tr.failed = true
		tr.result.Status = StepError
		tr.result.Error = runErr.Error()
		st.unavailable(ps.ID, StepError)
		if ps.OnError == "stop" {
			tr.stop = "failed"
			tr.stopReason = fmt.Sprintf("step %s: %s error", ps.ID, ps.ToolName)
		}
		r.Logger.Info("step error", "index", i, "id", ps.ID, "error", runErr.Error(), "on_error", ps.OnError)
		return tr
	}

	img := Image{}
	hasImg := false
	if len(outcome.Images) > 0 {
		img = outcome.Images[0]
		hasImg = true
	}

	if !passed {
		tr.failed = true
		tr.result.Status = StepExpectationFailed
		tr.result.Checks = expectResults
		st.record(ps.ID, StepExpectationFailed, outcome.Data, img, hasImg)
		if ps.OnFail == "stop" {
			tr.stop = "stopped"
			tr.stopReason = fmt.Sprintf("step %s: expect check %q failed", ps.ID, failedCheck(expectResults))
		}
		r.Logger.Info("step expect failed", "index", i, "id", ps.ID, "attempts", attempts)
	} else {
		tr.result.Status = StepOK
		tr.result.Checks = expectResults
		// Steps without expect never recorded in the loop; do it here so
		// later checks can see their output/image. (With expect, the loop
		// already recorded the final attempt.)
		if len(ps.Expect) == 0 {
			st.record(ps.ID, StepOK, outcome.Data, img, hasImg)
		}
	}

	// Output for the report (quiet suppresses it; state keeps the full copy
	// for later checks).
	if !ps.Quiet && outcome.Data != nil {
		tr.result.Output = ss.trunc.truncate(outcome.Data)
	}

	// Image handling: retain for later checks, return if the budgets allow.
	if hasImg {
		if !ss.retSeen[ps.ID] {
			ss.retSeen[ps.ID] = true
			if *ss.retBytes+int64(len(img.Data)) > imagesRetained {
				tr.failed = true
				tr.result.Status = StepError
				tr.result.Error = "image budget exceeded (64 MB retained)"
				st.unavailable(ps.ID, StepError)
				return tr
			}
			*ss.retBytes += int64(len(img.Data))
		}
		if !ps.Quiet && *ss.imgCount < maxImages && ss.retBytesReturned()+int64(len(img.Data)) <= imagesReturned {
			idx := len(ss.report.ReturnedImages)
			ss.report.ReturnedImages = append(ss.report.ReturnedImages, img)
			*ss.imgCount++
			tr.result.ImageIndex = &idx
		} else if !ps.Quiet {
			tr.result.ImageDropped = true
		}
	}

	return tr
}

func (c *stepCounters) retBytesReturned() int64 {
	// Returned bytes are derived from the report itself.
	var n int64
	for _, im := range c.report.ReturnedImages {
		n += int64(len(im.Data))
	}
	return n
}

func stepKindLabel(ps planStep) string {
	switch ps.Kind {
	case "wait":
		return "wait"
	case "gate":
		return "gate"
	default:
		return ps.ToolName
	}
}

func elapsed(a, b time.Time) int64 { return (b.UnixNano() - a.UnixNano()) / int64(time.Millisecond) }

// ---- Report truncation ----------------------------------------------------

type truncator struct {
	total int
}

// truncate returns a copy of the output map with string fields cut per the
// report budgets.
func (t *truncator) truncate(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m)+2)
	for k, val := range m {
		s, isStr := val.(string)
		if !isStr {
			out[k] = val
			continue
		}
		limit := reportStringCap
		if t.total > reportTotalCap {
			limit = reportTrimCap
		}
		if len(s) > limit {
			out[k] = cutRune(s, limit)
			out[k+"_truncated_in_report"] = true
			t.total += limit
		} else {
			out[k] = s
			t.total += len(s)
		}
	}
	return out
}

func cutRune(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// fitReportJSON enforces the final report JSON cap by further trimming
// strings (1 KB, then progressively smaller) until the report fits.
func fitReportJSON(out *SequenceOutput) {
	for _, limit := range []int{1024, 256, 64} {
		raw, err := json.Marshal(out)
		if err == nil && len(raw) <= reportJSONCap {
			return
		}
		trimStringsInResults(out, limit)
	}
}

func trimStringsInResults(out *SequenceOutput, limit int) {
	for i := range out.Results {
		res := &out.Results[i]
		m, ok := res.Output.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range m {
			if s, isStr := v.(string); isStr && len(s) > limit {
				m[k] = cutRune(s, limit)
			}
		}
	}
}
