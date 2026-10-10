package orchestrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/cjlapao/pdfm-mcp/internal/imaging"
)

func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
func ms(n int) time.Duration          { return time.Duration(n) * time.Millisecond }

func marshalArgs(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

// idPattern is the only accepted shape of a step id.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Budget defaults and hard limits.
const (
	defaultTimeoutSeconds = 120
	defaultMaxImages      = 8
	defaultRetryDelayMS   = 1000

	minSteps, maxSteps          = 1, 50
	minTimeout, maxTimeout      = 1, 900
	maxImagesLimit              = 16
	minWaitSeconds, maxWaitSecs = 0.1, 300
	maxRetryAttempts            = 50
	maxRetryDelayMS             = 60000
	maxRegexLen                 = 1024
)

// Wait-until kinds and limits.
const (
	untilSettled = "settled"
	untilChanged = "changed"

	// captureActionName is the registered action the wait-until polling
	// calls directly; it is not a visible step.
	captureActionName = "parallels_vm_capture"

	defaultUntilTimeoutSeconds = 10
	minUntilTimeoutSeconds     = 0.5
	maxUntilTimeoutSeconds     = 120
	defaultPollMS              = 500
	minPollMS                  = 200
	maxPollMS                  = 5000
	defaultStablePolls         = 2
	minStablePolls             = 1
	maxStablePolls             = 5
	defaultUntilMaxDimension   = 512
)

// Validate checks the whole sequence up front and returns a run-ready plan
// plus ALL validation errors (nothing is executed when the list is
// non-empty).
func Validate(in SequenceInput, reg *Registry, ctx ValidateContext) (*Plan, []error) {
	var errs []error
	add := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf(format, a...))
	}

	timeout := defaultTimeoutSeconds
	if in.TimeoutSeconds != 0 {
		timeout = in.TimeoutSeconds
	}
	if timeout < minTimeout || timeout > maxTimeout {
		add("timeout_seconds %d out of range (%d-%d)", timeout, minTimeout, maxTimeout)
	}
	maxImages := defaultMaxImages
	if in.MaxImages != 0 {
		maxImages = in.MaxImages
	}
	if in.MaxImages < 0 || in.MaxImages > maxImagesLimit {
		add("max_images %d out of range (0-%d)", in.MaxImages, maxImagesLimit)
	}
	onError := "stop"
	if in.OnError != "" {
		onError = in.OnError
	}
	if onError != "stop" && onError != "continue" {
		add("on_error %q must be \"stop\" or \"continue\"", in.OnError)
	}

	if len(in.Steps) < minSteps || len(in.Steps) > maxSteps {
		add("steps must have %d to %d entries (got %d)", minSteps, maxSteps, len(in.Steps))
		return nil, errs
	}

	plan := &Plan{
		VMID:      ctx.Target,
		Timeout:   seconds(float64(timeout)),
		MaxImages: maxImages,
		OnError:   onError,
		Fleet:     ctx.Fleet,
	}

	// Pass 1: assign ids and collect the id set (for forward-reference and
	// duplicate checks).
	ids := make([]string, len(in.Steps))
	idSet := map[string]int{} // id -> first index using it
	usedIDs := map[string]bool{}
	for i, s := range in.Steps {
		id := s.ID
		if id == "" {
			id = fmt.Sprintf("s%d", i+1)
		}
		if !idPattern.MatchString(id) {
			add("step %d (%s): id must match %s", i, id, idPattern.String())
			id = fmt.Sprintf("s%d", i+1) // keep a usable id for later messages
		}
		if first, dup := idSet[id]; dup {
			add("step %d (%s): duplicate id (first used by step %d)", i, id, first)
		} else {
			idSet[id] = i
		}
		usedIDs[id] = true
		ids[i] = id
	}

	var declaredWaitSeconds float64
	var declaredRetryDelayMS int

	for i, s := range in.Steps {
		id := ids[i]
		addStep := func(format string, a ...any) {
			args := append([]any{i, id}, a...)
			errs = append(errs, fmt.Errorf("step %d (%s): "+format, args...))
		}

		kinds := 0
		if s.Tool != "" {
			kinds++
		}
		if s.Wait != nil {
			kinds++
		}
		if len(s.Gate) > 0 {
			kinds++
		}
		if kinds == 0 {
			addStep("no step kind (set exactly one of tool, wait, gate)")
			continue
		}
		if kinds > 1 {
			addStep("at most one of tool, wait, gate (got %d)", kinds)
			continue
		}

		ps := planStep{ID: id, OnFail: "stop"}
		if s.OnFail != "" {
			ps.OnFail = s.OnFail
			if ps.OnFail != "stop" && ps.OnFail != "continue" {
				addStep("on_fail %q must be \"stop\" or \"continue\"", s.OnFail)
			}
		}
		if s.OnError != "" {
			ps.OnError = s.OnError
			if ps.OnError != "stop" && ps.OnError != "continue" {
				addStep("on_error %q must be \"stop\" or \"continue\"", s.OnError)
			}
		} else {
			ps.OnError = onError
		}
		ps.Quiet = s.Quiet

		switch {
		case s.Wait != nil:
			if s.Retry != nil {
				addStep("retry requires a tool step")
				continue
			}
			if (s.Wait.Seconds != 0) == (s.Wait.Until != "") {
				addStep("wait needs exactly one of seconds or until")
				continue
			}
			if s.Wait.Until != "" {
				up, worst, ok := validateUntil(*s.Wait, i, ids, in, reg, ctx, addStep)
				if !ok {
					continue
				}
				ps.Kind = "wait"
				ps.Until = up
				declaredWaitSeconds += worst
			} else {
				if hasUntilOnlyFields(*s.Wait) {
					addStep("from, timeout_seconds, poll_ms, stable_polls, threshold_percent, tolerance and max_dimension are only valid with until")
					continue
				}
				if s.Wait.Seconds < minWaitSeconds || s.Wait.Seconds > maxWaitSecs {
					addStep("wait seconds %v out of range (%v-%v)", s.Wait.Seconds, minWaitSeconds, maxWaitSecs)
					continue
				}
				ps.Kind = "wait"
				ps.Wait = seconds(s.Wait.Seconds)
				declaredWaitSeconds += s.Wait.Seconds
			}

		case s.Tool != "":
			action, ok := reg.Get(s.Tool)
			if !ok {
				addStep("unknown tool %q", s.Tool)
				continue
			}
			ps.Kind = "tool"
			ps.Action = action
			ps.ToolName = s.Tool

			// Build the final args: reject step-level vm_id, then inject
			// the target in fleet mode for VM-scoped tools.
			args, ok := buildArgs(s.Args, action.VMScoped, ctx.Fleet, ctx, func(format string, a ...any) {
				addStep(format, a...)
			})
			if !ok {
				continue
			}
			if verr := action.ValidateArgs(args); verr != nil {
				addStep("%v", verr)
				continue
			}
			ps.Args = args

			if s.Retry != nil && len(s.Expect) == 0 {
				addStep("retry requires expect")
				continue
			}
			if s.Retry != nil {
				if s.Retry.MaxAttempts < 2 || s.Retry.MaxAttempts > maxRetryAttempts {
					addStep("retry.max_attempts %d out of range (2-%d)", s.Retry.MaxAttempts, maxRetryAttempts)
					continue
				}
				delay := s.Retry.DelayMS
				if delay == 0 {
					delay = defaultRetryDelayMS
				}
				if delay < 0 || delay > maxRetryDelayMS {
					addStep("retry.delay_ms %d out of range (0-%d)", delay, maxRetryDelayMS)
					continue
				}
				ps.Retry = &retryPlan{MaxAttempts: s.Retry.MaxAttempts, Delay: ms(delay)}
				declaredRetryDelayMS += delay * (s.Retry.MaxAttempts - 1)
			}

		default: // gate
			if s.Retry != nil {
				addStep("retry requires a tool step")
				continue
			}
			ps.Kind = "gate"
		}

		// Check lists: when (any kind), expect (tool only), gate.
		if len(s.When) > 0 {
			pc, cerrs := validateChecks(s.When, "when", id, i, ids, in, reg, usedIDs, false)
			errs = append(errs, prefixStep(i, id, cerrs)...)
			ps.When = pc
		}
		if len(s.Expect) > 0 {
			if ps.Kind != "tool" {
				addStep("expect requires a tool step")
			} else {
				pc, cerrs := validateChecks(s.Expect, "expect", id, i, ids, in, reg, usedIDs, true)
				errs = append(errs, prefixStep(i, id, cerrs)...)
				ps.Expect = pc
			}
		}
		if ps.Kind == "gate" {
			if len(s.Gate) == 0 {
				addStep("gate needs at least one check")
				continue
			}
			pc, cerrs := validateChecks(s.Gate, "gate", id, i, ids, in, reg, usedIDs, false)
			errs = append(errs, prefixStep(i, id, cerrs)...)
			ps.Gate = pc
		}

		plan.Steps = append(plan.Steps, ps)
	}

	// Budget pre-check: declared waits + retry sleeps must fit the timeout.
	needed := declaredWaitSeconds + float64(declaredRetryDelayMS)/1000
	if needed > float64(timeout) {
		add("declared waits need at least %gs but timeout_seconds is %d", needed, timeout)
	}

	return plan, errs
}

// validateChecks checks one list (when/expect/gate). ownerIsSelf is true
// for expect: an empty step/from/to refers to the owner itself.
func validateChecks(
	checks []Check, listName, ownerID string, ownerIndex int,
	ids []string, in SequenceInput, reg *Registry,
	usedIDs map[string]bool, ownerIsSelf bool,
) ([]planCheck, []error) {
	var errs []error
	out := make([]planCheck, 0, len(checks))
	for j, c := range checks {
		cf := func(format string, a ...any) {
			args := append([]any{listName, j}, a...)
			errs = append(errs, fmt.Errorf("%s[%d]: "+format, args...))
		}
		switch c.Check {
		case checkExitCode, checkMatches, checkContains, checkImageChanged, checkSucceeded:
		default:
			cf("unknown check %q", c.Check)
			continue
		}

		// Resolve the referenced step(s); all references must be EARLIER
		// than the referencing step (ownerIsSelf allows the owner).
		refErr := func(ref, kind string) bool {
			if ref == "" {
				if !ownerIsSelf {
					cf("%s requires %q", c.Check, kind)
					return true
				}
				return false
			}
			if !usedIDs[ref] {
				cf("%q references unknown step %q", kind, ref)
				return true
			}
			idx := indexForID(ref, ids)
			if idx >= ownerIndex {
				if !ownerIsSelf || idx != ownerIndex {
					cf("%q references step %q which is not earlier", kind, ref)
					return true
				}
			}
			return false
		}

		pc := planCheck{Spec: c}

		switch c.Check {
		case checkMatches:
			if refErr(c.Step, "step") {
				continue
			}
			if c.Regex == "" {
				cf("matches requires a non-empty regex")
				continue
			}
			if len(c.Regex) > maxRegexLen {
				cf("regex is %d chars, above the %d limit", len(c.Regex), maxRegexLen)
				continue
			}
			re, err := regexp.Compile(c.Regex)
			if err != nil {
				cf("regex does not compile: %v", err)
				continue
			}
			pc.Compiled = re
		case checkContains:
			if refErr(c.Step, "step") {
				continue
			}
			if c.Text == "" {
				cf("contains requires a non-empty text")
				continue
			}
		case checkExitCode:
			if refErr(c.Step, "step") {
				continue
			}
		case checkSucceeded:
			if refErr(c.Step, "step") {
				continue
			}
		case checkImageChanged:
			if refErr(c.From, "from") {
				continue
			}
			to := c.To
			if to == "" {
				if !ownerIsSelf {
					cf("image_changed in %s requires to (there is no owning result)", listName)
					continue
				}
			}
			if refErr(to, "to") {
				continue
			}
			if c.ThresholdPercent != nil && (*c.ThresholdPercent < 0 || *c.ThresholdPercent > 100) {
				cf("threshold_percent %v out of range (0-100)", *c.ThresholdPercent)
				continue
			}
			if c.Tolerance != nil && (*c.Tolerance < 0 || *c.Tolerance > 255) {
				cf("tolerance %d out of range (0-255)", *c.Tolerance)
				continue
			}
			// Both referenced steps must be tool steps that can produce an
			// image.
			prodErr := func(ref string) bool {
				if ref == "" || ref == ownerID {
					// owner: must be a tool step with ProducesImages
					if ownerIndex < len(in.Steps) {
						owner := in.Steps[ownerIndex]
						if owner.Tool == "" {
							cf("image_changed owner %q must be an image-producing tool step", ownerID)
							return true
						}
						if a, ok := reg.Get(owner.Tool); !ok || !a.ProducesImages {
							cf("image_changed owner step uses %q which cannot produce images", owner.Tool)
							return true
						}
					}
					return false
				}
				idx := indexForID(ref, ids)
				st := in.Steps[idx]
				if st.Wait != nil && st.Wait.Until != "" {
					// A wait-until step retains its final poll image.
					return false
				}
				if st.Tool == "" {
					cf("%q references %q which is not a tool step", "image_changed", ref)
					return true
				}
				a, ok := reg.Get(st.Tool)
				if !ok || !a.ProducesImages {
					cf("%q references %q which cannot produce images", "image_changed", ref)
					return true
				}
				return false
			}
			if prodErr(c.From) || prodErr(to) {
				continue
			}
		}
		out = append(out, pc)
	}
	return out, errs
}

// buildArgs returns the final step args: a copy of the user's args with
// vm_id rejected in both modes and injected in fleet mode for VM-scoped
// tools. Errors are reported via the report callback.
func buildArgs(userArgs map[string]any, vmScoped, fleet bool, ctx ValidateContext, report func(format string, a ...any)) (raw []byte, ok bool) {
	if _, has := userArgs["vm_id"]; has {
		report("args must not contain vm_id (the sequence targets one VM; use the top-level vm_id)")
		return nil, false
	}
	final := map[string]any{}
	for k, v := range userArgs {
		final[k] = v
	}
	if vmScoped && fleet {
		final["vm_id"] = ctx.Target
	}
	raw, err := marshalArgs(final)
	if err != nil {
		report("could not encode args: %v", err)
		return nil, false
	}
	return raw, true
}

// prefixStep prefixes a list of errors with "step <n> (<id>): ".
func prefixStep(i int, id string, errs []error) []error {
	out := make([]error, 0, len(errs))
	for _, e := range errs {
		out = append(out, fmt.Errorf("step %d (%s): %v", i, id, e))
	}
	return out
}

func indexForID(id string, ids []string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}

// hasUntilOnlyFields reports whether a fixed-seconds wait carries fields
// that only make sense with until.
func hasUntilOnlyFields(w WaitSpec) bool {
	return w.From != "" || w.TimeoutSeconds != 0 || w.PollMS != 0 || w.StablePolls != 0 ||
		w.ThresholdPercent != nil || w.Tolerance != nil || w.MaxDimension != 0
}

// validateUntil validates a wait-until spec for the step at index i and
// returns its plan plus the worst-case duration in seconds (the timeout,
// which counts toward the sequence budget). Errors go through report; ok is
// false when any was reported.
func validateUntil(
	w WaitSpec, i int, ids []string, in SequenceInput, reg *Registry,
	ctx ValidateContext, report func(format string, a ...any),
) (up *untilPlan, worst float64, ok bool) {
	ok = true
	fail := func(format string, a ...any) {
		ok = false
		report(format, a...)
	}

	if w.Until != untilSettled && w.Until != untilChanged {
		fail("wait until %q must be %q or %q", w.Until, untilSettled, untilChanged)
		return nil, 0, false
	}
	if w.Until == untilSettled && w.From != "" {
		fail("wait until settled does not take from")
	}
	if w.Until == untilChanged && w.StablePolls != 0 {
		fail("stable_polls is only valid with until settled")
	}

	timeout := float64(defaultUntilTimeoutSeconds)
	if w.TimeoutSeconds != 0 {
		timeout = w.TimeoutSeconds
	}
	if timeout < minUntilTimeoutSeconds || timeout > maxUntilTimeoutSeconds {
		fail("wait timeout_seconds %v out of range (%v-%v)", timeout, minUntilTimeoutSeconds, maxUntilTimeoutSeconds)
	}
	poll := defaultPollMS
	if w.PollMS != 0 {
		poll = w.PollMS
	}
	if poll < minPollMS || poll > maxPollMS {
		fail("wait poll_ms %d out of range (%d-%d)", poll, minPollMS, maxPollMS)
	}
	stable := defaultStablePolls
	if w.StablePolls != 0 {
		stable = w.StablePolls
	}
	if stable < minStablePolls || stable > maxStablePolls {
		fail("wait stable_polls %d out of range (%d-%d)", stable, minStablePolls, maxStablePolls)
	}
	maxDim := defaultUntilMaxDimension
	if w.MaxDimension != 0 {
		maxDim = w.MaxDimension
	}
	if maxDim < imaging.MinMaxDimension || maxDim > imaging.MaxMaxDimension {
		fail("wait max_dimension %d out of range (%d-%d)", maxDim, imaging.MinMaxDimension, imaging.MaxMaxDimension)
	}
	threshold := defaultDiffThresholdPercent
	if w.ThresholdPercent != nil {
		threshold = *w.ThresholdPercent
	}
	if threshold < 0 || threshold > 100 {
		fail("wait threshold_percent %v out of range (0-100)", threshold)
	}
	tolerance := defaultDiffTolerance
	if w.Tolerance != nil {
		tolerance = *w.Tolerance
	}
	if tolerance < 0 || tolerance > 255 {
		fail("wait tolerance %d out of range (0-255)", tolerance)
	}

	// The baseline must be an EARLIER step that holds an image.
	if w.From != "" && w.Until == untilChanged {
		idx := indexForID(w.From, ids)
		switch {
		case idx < 0:
			fail("from references unknown step %q", w.From)
		case idx >= i:
			fail("from references step %q which is not earlier", w.From)
		default:
			st := in.Steps[idx]
			isWaitUntil := st.Wait != nil && st.Wait.Until != ""
			a, found := reg.Get(st.Tool)
			if !isWaitUntil && (st.Tool == "" || !found || !a.ProducesImages) {
				fail("from references %q which is not a capture step", w.From)
			}
		}
	}

	// Polling calls the registered capture action directly.
	capture, found := reg.Get(captureActionName)
	if !found {
		fail("wait until needs the %s tool, which is not available (disabled by policy?)", captureActionName)
		return nil, 0, false
	}
	if ctx.Target == "" {
		fail("wait until needs a usable VM target")
		return nil, 0, false
	}
	if !ok {
		return nil, 0, false
	}
	args, argsOK := buildArgs(map[string]any{"max_dimension": maxDim}, capture.VMScoped, ctx.Fleet, ctx, report)
	if !argsOK {
		return nil, 0, false
	}
	if verr := capture.ValidateArgs(args); verr != nil {
		fail("%v", verr)
		return nil, 0, false
	}

	from := ""
	if w.Until == untilChanged {
		from = w.From
	}
	return &untilPlan{
		Kind:         w.Until,
		From:         from,
		Timeout:      seconds(timeout),
		Poll:         ms(poll),
		StablePolls:  stable,
		Threshold:    threshold,
		Tolerance:    tolerance,
		MaxDimension: maxDim,
		Capture:      capture,
		CaptureArgs:  args,
	}, timeout, true
}
