package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
)

// regWithFakes builds a registry with the fake tools used by validation
// tests: exec-like (no images) and capture-like (images).
func regWithFakes() *Registry {
	reg := NewRegistry()
	noop := func(json.RawMessage) error { return nil }
	reg.Register(Action{Name: "fake_exec", Description: "e", VMScoped: true, ValidateArgs: noop})
	reg.Register(Action{Name: "fake_capture", Description: "c", VMScoped: true, ProducesImages: true, ValidateArgs: noop})
	reg.Register(Action{Name: "fake_global", Description: "g", ValidateArgs: noop})
	return reg
}

var fleetCtx = ValidateContext{Target: "vm1", Fleet: true}
var vmCtx = ValidateContext{Target: "vm1", Fleet: false}

func mustValidate(t *testing.T, in SequenceInput, reg *Registry, ctx ValidateContext) *Plan {
	t.Helper()
	plan, errs := Validate(in, reg, ctx)
	if len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	return plan
}

func expectErrors(t *testing.T, in SequenceInput, reg *Registry, ctx ValidateContext, wantSubstr ...string) []error {
	t.Helper()
	_, errs := Validate(in, reg, ctx)
	if len(errs) == 0 {
		t.Fatalf("want errors containing %v, got none", wantSubstr)
	}
	for _, w := range wantSubstr {
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), w) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("want an error containing %q, got %v", w, errs)
		}
	}
	return errs
}

func TestValidateHappyPathFleet(t *testing.T) {
	reg := regWithFakes()
	plan := mustValidate(t, SequenceInput{
		VMID: "vm1",
		Steps: []Step{
			{Tool: "fake_exec", Args: map[string]any{"command": "ls"}},
			{Wait: &WaitSpec{Seconds: 1}},
			{ID: "snap", Tool: "fake_capture"},
		},
	}, reg, fleetCtx)

	if plan.VMID != "vm1" || !plan.Fleet || len(plan.Steps) != 3 {
		t.Fatalf("plan = %+v", plan)
	}
	// Default ids assigned.
	if plan.Steps[0].ID != "s1" || plan.Steps[2].ID != "snap" {
		t.Fatalf("ids = %v", []string{plan.Steps[0].ID, plan.Steps[2].ID})
	}
	// Fleet: vm_id injected into VM-scoped args.
	args := string(plan.Steps[0].Args)
	if !strings.Contains(args, `"vm_id":"vm1"`) || !strings.Contains(args, `"command":"ls"`) {
		t.Fatalf("fleet args = %s", args)
	}
	// Defaults.
	if plan.Timeout.Seconds() != 120 || plan.MaxImages != 8 || plan.OnError != "stop" {
		t.Fatalf("defaults = %+v", plan)
	}
}

func TestValidateVMModeNoInjection(t *testing.T) {
	reg := regWithFakes()
	plan := mustValidate(t, SequenceInput{
		Steps: []Step{{Tool: "fake_exec", Args: map[string]any{"command": "ls"}}},
	}, reg, vmCtx)
	if strings.Contains(string(plan.Steps[0].Args), "vm_id") {
		t.Fatalf("VM mode must not inject vm_id: %s", plan.Steps[0].Args)
	}
}

func TestValidateCountsAndRounds(t *testing.T) {
	reg := regWithFakes()
	expectErrors(t, SequenceInput{Steps: nil}, reg, fleetCtx, "steps must have")
	big := SequenceInput{}
	for i := 0; i < 51; i++ {
		big.Steps = append(big.Steps, Step{Wait: &WaitSpec{Seconds: 1}})
	}
	expectErrors(t, big, reg, fleetCtx, "steps must have")
	expectErrors(t, SequenceInput{Steps: []Step{{Wait: &WaitSpec{Seconds: 1}}}, TimeoutSeconds: -1}, reg, fleetCtx, "timeout_seconds")
	expectErrors(t, SequenceInput{Steps: []Step{{Wait: &WaitSpec{Seconds: 1}}}, TimeoutSeconds: 901}, reg, fleetCtx, "timeout_seconds")
	expectErrors(t, SequenceInput{Steps: []Step{{Wait: &WaitSpec{Seconds: 1}}}, MaxImages: 17}, reg, fleetCtx, "max_images")
	expectErrors(t, SequenceInput{Steps: []Step{{Wait: &WaitSpec{Seconds: 1}}}, OnError: "explode"}, reg, fleetCtx, "on_error")
}

func TestValidateStepKindMutualExclusion(t *testing.T) {
	reg := regWithFakes()
	expectErrors(t, SequenceInput{Steps: []Step{{}}}, reg, fleetCtx, "no step kind")
	expectErrors(t, SequenceInput{Steps: []Step{{Tool: "fake_exec", Wait: &WaitSpec{Seconds: 1}}}}, reg, fleetCtx, "at most one of")
	expectErrors(t, SequenceInput{Steps: []Step{{Tool: "fake_exec", Gate: []Check{{Check: "succeeded", Step: "x"}}}}}, reg, fleetCtx, "at most one of")
}

func TestValidateUnknownToolAndArgs(t *testing.T) {
	reg := regWithFakes()
	expectErrors(t, SequenceInput{Steps: []Step{{Tool: "fake_nope"}}}, reg, fleetCtx, "unknown tool")
	// vm_id in args is rejected in BOTH modes.
	expectErrors(t, SequenceInput{Steps: []Step{{Tool: "fake_exec", Args: map[string]any{"vm_id": "other"}}}}, reg, fleetCtx, "must not contain vm_id")
	expectErrors(t, SequenceInput{Steps: []Step{{Tool: "fake_exec", Args: map[string]any{"vm_id": "other"}}}}, reg, vmCtx, "must not contain vm_id")
}

func TestValidateIDs(t *testing.T) {
	reg := regWithFakes()
	expectErrors(t, SequenceInput{Steps: []Step{{ID: "Bad!", Wait: &WaitSpec{Seconds: 1}}}}, reg, fleetCtx, "id must match")
	expectErrors(t, SequenceInput{Steps: []Step{
		{ID: "a", Wait: &WaitSpec{Seconds: 1}},
		{ID: "a", Wait: &WaitSpec{Seconds: 1}},
	}}, reg, fleetCtx, "duplicate id")
	// Forward reference is an error.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Wait: &WaitSpec{Seconds: 1}, When: []Check{{Check: "succeeded", Step: "later"}}},
		{ID: "later", Wait: &WaitSpec{Seconds: 1}},
	}}, reg, fleetCtx, "not earlier")
	// Unknown reference.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Wait: &WaitSpec{Seconds: 1}, When: []Check{{Check: "succeeded", Step: "ghost"}}},
	}}, reg, fleetCtx, `unknown step "ghost"`)
	// Default id may not collide with a user id on another step.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Wait: &WaitSpec{Seconds: 1}},
		{ID: "s1", Wait: &WaitSpec{Seconds: 1}},
	}}, reg, fleetCtx, "duplicate id")
}

func TestValidateCheckKinds(t *testing.T) {
	reg := regWithFakes()
	// Unknown check.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Wait: &WaitSpec{Seconds: 1}, When: []Check{{Check: "sparkles"}}},
	}}, reg, fleetCtx, "unknown check")
	// Bad / overlong regex.
	long := strings.Repeat("a", maxRegexLen+1)
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_exec"},
		{Wait: &WaitSpec{Seconds: 1}, When: []Check{{Check: "matches", Step: "s1", Regex: "("}}},
	}}, reg, fleetCtx, "does not compile")
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_exec"},
		{Wait: &WaitSpec{Seconds: 1}, When: []Check{{Check: "matches", Step: "s1", Regex: long}}},
	}}, reg, fleetCtx, "above the")
	// Empty contains text.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_exec"},
		{Wait: &WaitSpec{Seconds: 1}, When: []Check{{Check: "contains", Step: "s1"}}},
	}}, reg, fleetCtx, "non-empty text")
	// when/gate need explicit step refs (no owning result).
	expectErrors(t, SequenceInput{Steps: []Step{
		{Wait: &WaitSpec{Seconds: 1}, When: []Check{{Check: "exit_code"}}},
	}}, reg, fleetCtx, `requires "step"`)
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_capture"},
		{Gate: []Check{{Check: "image_changed", From: "s1"}}},
	}}, reg, fleetCtx, "requires to")
}

func TestValidateImageChangedRefs(t *testing.T) {
	reg := regWithFakes()
	// from a non-image tool.
	expectErrors(t, SequenceInput{Steps: []Step{
		{ID: "e", Tool: "fake_exec"},
		{Gate: []Check{{Check: "image_changed", From: "e", To: "e"}}},
	}}, reg, fleetCtx, "cannot produce images")
	// to a wait step.
	expectErrors(t, SequenceInput{Steps: []Step{
		{ID: "c1", Tool: "fake_capture"},
		{ID: "w", Wait: &WaitSpec{Seconds: 1}},
		{Gate: []Check{{Check: "image_changed", From: "c1", To: "w"}}},
	}}, reg, fleetCtx, "not a tool step")
	// expect on capture: to defaults to the owner (valid).
	mustValidate(t, SequenceInput{Steps: []Step{
		{ID: "c1", Tool: "fake_capture"},
		{ID: "c2", Tool: "fake_capture", Expect: []Check{{Check: "image_changed", From: "c1"}}},
	}}, reg, fleetCtx)
	// Range errors.
	thr := 101.0
	tol := 256
	expectErrors(t, SequenceInput{Steps: []Step{
		{ID: "c1", Tool: "fake_capture"},
		{ID: "c2", Tool: "fake_capture", Expect: []Check{{Check: "image_changed", From: "c1", ThresholdPercent: &thr}}},
	}}, reg, fleetCtx, "threshold_percent")
	expectErrors(t, SequenceInput{Steps: []Step{
		{ID: "c1", Tool: "fake_capture"},
		{ID: "c2", Tool: "fake_capture", Expect: []Check{{Check: "image_changed", From: "c1", Tolerance: &tol}}},
	}}, reg, fleetCtx, "tolerance")
}

func TestValidateModifiers(t *testing.T) {
	reg := regWithFakes()
	// expect on a non-tool step.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_exec"},
		{Wait: &WaitSpec{Seconds: 1}, Expect: []Check{{Check: "succeeded", Step: "s1"}}},
	}}, reg, fleetCtx, "expect requires a tool step")
	// retry without expect.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_exec", Retry: &Retry{MaxAttempts: 3}},
	}}, reg, fleetCtx, "retry requires expect")
	// retry on wait / gate.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Wait: &WaitSpec{Seconds: 1}, Retry: &Retry{MaxAttempts: 3}},
	}}, reg, fleetCtx, "retry requires a tool step")
	// Retry ranges.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_exec", Expect: []Check{{Check: "succeeded"}}, Retry: &Retry{MaxAttempts: 1}},
	}}, reg, fleetCtx, "max_attempts")
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_exec", Expect: []Check{{Check: "succeeded"}}, Retry: &Retry{MaxAttempts: 51}},
	}}, reg, fleetCtx, "max_attempts")
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_exec", Expect: []Check{{Check: "succeeded"}}, Retry: &Retry{MaxAttempts: 3, DelayMS: 60001}},
	}}, reg, fleetCtx, "delay_ms")
	// on_fail values.
	expectErrors(t, SequenceInput{Steps: []Step{
		{Tool: "fake_exec", Expect: []Check{{Check: "succeeded"}}, OnFail: "maybe"},
	}}, reg, fleetCtx, "on_fail")
}

func TestValidateWaitRangeAndBudget(t *testing.T) {
	reg := regWithFakes()
	expectErrors(t, SequenceInput{Steps: []Step{{Wait: &WaitSpec{Seconds: 0.01}}}}, reg, fleetCtx, "wait seconds")
	expectErrors(t, SequenceInput{Steps: []Step{{Wait: &WaitSpec{Seconds: 301}}}}, reg, fleetCtx, "wait seconds")
	// Declared waits exceed the timeout.
	expectErrors(t, SequenceInput{
		TimeoutSeconds: 5,
		Steps: []Step{
			{Wait: &WaitSpec{Seconds: 3}},
			{Wait: &WaitSpec{Seconds: 3}},
		},
	}, reg, fleetCtx, "declared waits need")
	// Retry sleep counted in the budget.
	expectErrors(t, SequenceInput{
		TimeoutSeconds: 5,
		Steps: []Step{
			{Tool: "fake_exec", Expect: []Check{{Check: "succeeded"}}, Retry: &Retry{MaxAttempts: 10, DelayMS: 1000}},
		},
	}, reg, fleetCtx, "declared waits need")
	// Fits: 9 sleeps of 1 s in 10 s.
	mustValidate(t, SequenceInput{
		TimeoutSeconds: 10,
		Steps: []Step{
			{Tool: "fake_exec", Expect: []Check{{Check: "succeeded"}}, Retry: &Retry{MaxAttempts: 10, DelayMS: 1000}},
		},
	}, reg, fleetCtx)
}

func TestValidateCollectsAllErrors(t *testing.T) {
	reg := regWithFakes()
	_, errs := Validate(SequenceInput{
		OnError: "nope",
		Steps: []Step{
			{Tool: "fake_nope"},
			{ID: "Bad", Wait: &WaitSpec{Seconds: 999}},
			{Tool: "fake_exec", Args: map[string]any{"vm_id": "x"}},
		},
	}, reg, fleetCtx)
	if len(errs) < 4 {
		t.Fatalf("want >=4 errors, got %v", errs)
	}
	// Every step error carries its "step <n> (<id>):" prefix.
	for _, e := range []string{"step 0 (s1): unknown tool", "step 1 (s2):", "step 2 (s3): args must not contain vm_id"} {
		found := false
		for _, err := range errs {
			if strings.Contains(err.Error(), e) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing error %q in %v", e, errs)
		}
	}
}

func TestValidateGlobalToolNotVMScoped(t *testing.T) {
	reg := regWithFakes()
	plan := mustValidate(t, SequenceInput{
		Steps: []Step{{Tool: "fake_global"}},
	}, reg, fleetCtx)
	// No vm_id injected for non-VM-scoped actions.
	if strings.Contains(string(plan.Steps[0].Args), "vm_id") {
		t.Fatalf("no vm_id injection for global actions: %s", plan.Steps[0].Args)
	}
}
