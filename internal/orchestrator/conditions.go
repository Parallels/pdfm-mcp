package orchestrator

import (
	"fmt"
	"strings"

	"github.com/Parallels/pdfm-mcp/internal/imaging"
)

// The five check kinds.
const (
	checkExitCode     = "exit_code"
	checkMatches      = "matches"
	checkContains     = "contains"
	checkImageChanged = "image_changed"
	checkSucceeded    = "succeeded"
)

// imageDiffDefaults apply when the check does not set them.
const (
	defaultDiffTolerance        = 8
	defaultDiffThresholdPercent = 0.1
)

// state is the run state conditions evaluate against: finished steps'
// statuses, full (untruncated) outputs and retained images.
type state struct {
	Statuses  map[string]string         // id -> step status
	Outputs   map[string]map[string]any // id -> full output (ok and expectation_failed steps)
	Images    map[string]Image          // id -> retained image
	diffCache map[string]imaging.DiffResult
}

func newState() *state {
	return &state{
		Statuses:  map[string]string{},
		Outputs:   map[string]map[string]any{},
		Images:    map[string]Image{},
		diffCache: map[string]imaging.DiffResult{},
	}
}

// record stores a finished tool step's results.
func (s *state) record(id, status string, data map[string]any, img Image, hasImage bool) {
	s.Statuses[id] = status
	if data != nil {
		s.Outputs[id] = data
	}
	if hasImage {
		s.Images[id] = img
	}
}

// unavailable marks a skipped/not-run step so referencing checks fail.
func (s *state) unavailable(id, status string) {
	s.Statuses[id] = status
}

// evaluate runs all checks (AND) against state and returns one result per
// check, in order. Pure and table-testable: no I/O, no context.
func evaluate(phase, ownerID string, checks []planCheck, st *state) []CheckResult {
	out := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		res := CheckResult{Phase: phase, Check: c.Spec.Check, Passed: true}
		p, detail := evalOne(phase, ownerID, c, st)
		res.Passed = p
		res.Detail = detail
		out = append(out, res)
	}
	return out
}

// allPassed reports whether every check passed (an empty list is vacuously
// true).
func allPassed(checks []CheckResult) bool {
	for _, c := range checks {
		if !c.Passed {
			return false
		}
	}
	return true
}

// failedCheck returns the first failing check's description for stop
// reasons, or "" when all passed.
func failedCheck(checks []CheckResult) string {
	for _, c := range checks {
		if !c.Passed {
			return c.Check
		}
	}
	return ""
}

func evalOne(phase, ownerID string, c planCheck, st *state) (bool, string) {
	// Resolve the referenced step(s).
	switch c.Spec.Check {
	case checkImageChanged:
		return evalImageChanged(phase, ownerID, c, st)
	case checkSucceeded:
		id := c.Spec.Step
		if id == "" {
			id = ownerID
		}
		status, ok := st.Statuses[id]
		if !ok {
			return false, fmt.Sprintf("step %q did not run", id)
		}
		p := status == StepOK
		detail := fmt.Sprintf("step %q is %s", id, status)
		return applyNegate(c.Spec.Negate, p, detail)
	default: // exit_code, matches, contains
		id := c.Spec.Step
		if id == "" {
			id = ownerID
		}
		if status, ok := st.Statuses[id]; !ok || status == StepNotRun || status == StepSkipped {
			return false, fmt.Sprintf("step %q did not run", id)
		}
		data, ok := st.Outputs[id]
		if !ok {
			return false, fmt.Sprintf("step %q did not run", id)
		}
		switch c.Spec.Check {
		case checkExitCode:
			v, ok := data["exit_code"]
			if !ok {
				return false, fmt.Sprintf("field %q missing from step %q", "exit_code", id)
			}
			f, ok := v.(float64)
			if !ok {
				return false, fmt.Sprintf("field %q in step %q is not a number", "exit_code", id)
			}
			want := 0
			if c.Spec.Equals != nil {
				want = *c.Spec.Equals
			}
			p := int(f) == want
			detail := fmt.Sprintf("exit_code %d (expected %d)", int(f), want)
			return applyNegate(c.Spec.Negate, p, detail)
		case checkMatches, checkContains:
			field := c.Spec.Field
			if field == "" {
				field = "stdout"
			}
			v, ok := data[field]
			if !ok {
				return false, fmt.Sprintf("field %q missing from step %q", field, id)
			}
			s, ok := v.(string)
			if !ok {
				return false, fmt.Sprintf("field %q in step %q is not a string", field, id)
			}
			var (
				p      bool
				detail string
			)
			if c.Spec.Check == checkMatches {
				p = c.Compiled.MatchString(s)
				if p {
					detail = fmt.Sprintf("matched in %s (%d chars)", field, len(s))
				} else {
					detail = fmt.Sprintf("no match for %q in %s", c.Spec.Regex, field)
				}
			} else {
				p = strings.Contains(s, c.Spec.Text)
				if p {
					detail = fmt.Sprintf("contains %q in %s", c.Spec.Text, field)
				} else {
					detail = fmt.Sprintf("missing %q in %s", c.Spec.Text, field)
				}
			}
			return applyNegate(c.Spec.Negate, p, detail)
		}
	}
	return false, fmt.Sprintf("unknown check %q", c.Spec.Check)
}

func evalImageChanged(_ string, ownerID string, c planCheck, st *state) (bool, string) {
	from, to := c.Spec.From, c.Spec.To
	if to == "" {
		to = ownerID
	}
	a, okA := st.Images[from]
	b, okB := st.Images[to]
	if !okA {
		return false, fmt.Sprintf("step %q has no image", from)
	}
	if !okB {
		return false, fmt.Sprintf("step %q has no image", to)
	}

	tol := defaultDiffTolerance
	if c.Spec.Tolerance != nil {
		tol = *c.Spec.Tolerance
	}
	thr := defaultDiffThresholdPercent
	if c.Spec.ThresholdPercent != nil {
		thr = *c.Spec.ThresholdPercent
	}

	cacheKey := fmt.Sprintf("%s|%s|%d|%g", from, to, tol, thr)
	res, cached := st.diffCache[cacheKey]
	if !cached {
		var err error
		res, err = imaging.Diff(a.Data, b.Data, imaging.DiffOptions{Tolerance: tol, ThresholdPercent: thr})
		if err != nil {
			return false, fmt.Sprintf("image diff failed: %v", err)
		}
		st.diffCache[cacheKey] = res
	}
	return applyNegate(c.Spec.Negate, res.Changed, res.Detail(thr))
}

// applyNegate inverts the result. Detail keeps the raw evaluation and is
// prefixed when negated so the report always explains the raw fact.
func applyNegate(negate bool, passed bool, detail string) (bool, string) {
	if !negate {
		return passed, detail
	}
	return !passed, "negated: " + detail
}
