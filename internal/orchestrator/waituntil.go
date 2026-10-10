package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/imaging"
)

// runWaitUntil polls the capture action until the until condition holds or
// the step's timeout elapses. Polling goes through the same registered
// capture action as a visible capture step (vm_id injected as usual) but is
// not a step: poll images are never returned to the client. Only the final
// poll image is kept in run state under the wait step's id, so a later
// image_changed check can reference it.
//
// settled: each poll is compared with the previous one; the condition holds
// after StablePolls consecutive unchanged comparisons (the first poll only
// seeds the comparison).
// changed: the baseline is the image of step From (downscaled to the poll
// size) or, without From, the first capture taken at the start of the wait;
// the condition holds when a poll differs from it.
func (r *Runner) runWaitUntil(ctx, runCtx context.Context, i int, ps planStep, st *state, retBytes *int64, start time.Time) toolStepResult {
	u := ps.Until
	tr := toolStepResult{result: StepResult{Index: i, ID: ps.ID, Kind: "wait"}}

	var (
		polls    int
		lastPct  float64
		met      bool
		runErr   error
		last     Image
		hasLast  bool
		baseline []byte
		haveBase bool
		stable   int
	)

	if u.Kind == untilChanged && u.From != "" {
		src, ok := st.Images[u.From]
		if !ok {
			runErr = fmt.Errorf("baseline step %q has no image", u.From)
		} else if res, err := imaging.Downscale(src.Data, u.MaxDimension); err != nil {
			runErr = fmt.Errorf("baseline step %q: %w", u.From, err)
		} else {
			baseline, haveBase = res.Data, true
		}
	}

	for runErr == nil {
		polls++
		out, err := u.Capture.Run(runCtx, u.CaptureArgs)
		if err != nil {
			runErr = err
			break
		}
		if len(out.Images) == 0 {
			runErr = errors.New("capture returned no image")
			break
		}
		last, hasLast = out.Images[0], true
		data := out.Images[0].Data

		if haveBase {
			res, derr := imaging.Diff(baseline, data, imaging.DiffOptions{Tolerance: u.Tolerance, ThresholdPercent: u.Threshold})
			if derr != nil {
				runErr = derr
				break
			}
			lastPct = res.PercentChanged
			if u.Kind == untilChanged {
				if res.Changed {
					met = true
					break
				}
			} else {
				if res.Changed {
					stable = 0
				} else {
					stable++
				}
				if stable >= u.StablePolls {
					met = true
					break
				}
			}
		}
		if u.Kind == untilSettled || !haveBase {
			baseline, haveBase = data, true
		}

		spent := r.Now().Sub(start)
		if spent >= u.Timeout {
			break
		}
		wait := u.Poll
		if remaining := u.Timeout - spent; remaining < wait {
			wait = remaining
		}
		if err := r.Sleep(runCtx, wait); err != nil {
			runErr = err
			break
		}
	}

	now := r.Now()
	elapsedMS := elapsed(start, now)
	tr.result.DurationMS = elapsedMS
	tr.result.Waited = float64(elapsedMS) / 1000
	if polls > 0 {
		tr.ran = 1
	}

	// Cancellation first, then the sequence timeout, then a real error.
	if runErr != nil && ctx.Err() != nil {
		tr.cancelled = true
		return tr
	}
	if runErr != nil && runCtx.Err() != nil {
		tr.stop = "timeout"
		tr.stopReason = fmt.Sprintf("timeout during wait step %s", ps.ID)
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
			tr.stopReason = fmt.Sprintf("step %s: wait until %s error", ps.ID, u.Kind)
		}
		r.Logger.Info("wait until error", "index", i, "id", ps.ID, "error", runErr.Error(), "on_error", ps.OnError)
		return tr
	}

	data := map[string]any{
		"until":                u.Kind,
		"met":                  met,
		"polls":                polls,
		"elapsed_ms":           elapsedMS,
		"last_changed_percent": lastPct,
	}
	status := StepOK
	if !met {
		status = StepExpectationFailed
	}

	// Keep the final poll image for later image_changed checks. Polls are
	// small (max_dimension, default 512) and only one image is kept per wait
	// step, but it still counts against the retained-image budget.
	hasImg := false
	if hasLast {
		if *retBytes+int64(len(last.Data)) > imagesRetained {
			tr.failed = true
			tr.result.Status = StepError
			tr.result.Error = "image budget exceeded (64 MB retained)"
			st.unavailable(ps.ID, StepError)
			return tr
		}
		*retBytes += int64(len(last.Data))
		hasImg = true
	}
	st.record(ps.ID, status, data, last, hasImg)
	tr.result.Status = status
	if !ps.Quiet {
		tr.result.Output = data
	}

	if !met {
		tr.failed = true
		detail := fmt.Sprintf("until %s not met after %d polls in %dms (last change %.2f%%)", u.Kind, polls, elapsedMS, lastPct)
		tr.result.Checks = []CheckResult{{Phase: PhaseExpect, Check: "wait_until", Passed: false, Detail: detail}}
		if ps.OnFail == "stop" {
			tr.stop = "stopped"
			tr.stopReason = fmt.Sprintf("step %s: wait until %s timed out after %s", ps.ID, u.Kind, u.Timeout)
		}
		r.Logger.Info("wait until not met", "index", i, "id", ps.ID, "polls", polls, "on_fail", ps.OnFail)
	}
	return tr
}
