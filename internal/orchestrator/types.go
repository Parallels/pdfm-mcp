// Package orchestrator runs VM sequences: ordered steps over injected
// actions (tool handlers) with waits, declarative condition checks,
// retries, image diffing and hard budgets. It has no knowledge of
// Parallels or MCP: actions are registered by the tools layer.
package orchestrator

import (
	"encoding/json"
	"regexp"
	"time"
)

// ---- Input (the JSON the AI builds) -------------------------------------

// SequenceInput is the argument schema of parallels_vm_sequence.
type SequenceInput struct {
	VMID           string `json:"vm_id,omitempty" jsonschema:"VM id or name; required in fleet mode, omit in VM mode"`
	Steps          []Step `json:"steps" jsonschema:"ordered steps, 1 to 50"`
	OnError        string `json:"on_error,omitempty" jsonschema:"stop (default) or continue when a step errors"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"total runtime cap in seconds (1-900, default 120)"`
	MaxImages      int    `json:"max_images,omitempty" jsonschema:"max images returned (0-16, default 8); extra images are dropped but still usable by image_changed"`
	ValidateOnly   bool   `json:"validate_only,omitempty" jsonschema:"validate the sequence and return without running anything"`
}

// Step is one sequence step. Exactly one of Tool, Wait, Gate must be set.
type Step struct {
	ID      string         `json:"id,omitempty"    jsonschema:"unique name (a-z, 0-9, _; starts with a letter) used by checks; defaults to s1, s2, ..."`
	Tool    string         `json:"tool,omitempty"    jsonschema:"MCP tool name to run, e.g. parallels_vm_exec; mutually exclusive with wait and gate"`
	Args    map[string]any `json:"args,omitempty"    jsonschema:"that tool's arguments, exactly as for a direct call (without vm_id)"`
	Wait    *WaitSpec      `json:"wait,omitempty"    jsonschema:"pause before the next step; mutually exclusive with tool and gate"`
	Gate    []Check        `json:"gate,omitempty"    jsonschema:"checks that must all pass or the run stops; mutually exclusive with tool and wait"`
	When    []Check        `json:"when,omitempty"    jsonschema:"run this step only if all checks pass, else skip it"`
	Expect  []Check        `json:"expect,omitempty"  jsonschema:"tool steps: checks on this step's own result; on failure see on_fail"`
	OnFail  string         `json:"on_fail,omitempty" jsonschema:"stop (default) or continue when expect or gate fails"`
	Retry   *Retry         `json:"retry,omitempty"   jsonschema:"tool steps with expect: re-run the step until expect passes"`
	OnError string         `json:"on_error,omitempty" jsonschema:"per-step override of the sequence on_error"`
	Quiet   bool           `json:"quiet,omitempty"   jsonschema:"keep the step's output and image out of the report (still available to checks)"`
}

// WaitSpec is a pause step: either a fixed pause (seconds) or a wait until
// the screen condition holds (until). Exactly one of the two is set.
type WaitSpec struct {
	Seconds          float64  `json:"seconds,omitempty"           jsonschema:"fixed pause in seconds (0.1-300); mutually exclusive with until"`
	Until            string   `json:"until,omitempty"             jsonschema:"settled (screen stopped changing) or changed (screen differs from the baseline); polls screenshots; mutually exclusive with seconds"`
	From             string   `json:"from,omitempty"              jsonschema:"until changed: id of an earlier capture step used as baseline (default: a capture taken when the wait starts)"`
	TimeoutSeconds   float64  `json:"timeout_seconds,omitempty"   jsonschema:"until: give up after this many seconds (0.5-120, default 10)"`
	PollMS           int      `json:"poll_ms,omitempty"           jsonschema:"until: delay between screenshots in ms (200-5000, default 500)"`
	StablePolls      int      `json:"stable_polls,omitempty"      jsonschema:"until settled: consecutive unchanged polls required (1-5, default 2)"`
	ThresholdPercent *float64 `json:"threshold_percent,omitempty" jsonschema:"until: percent of pixels that must differ to count as a change (0-100, default 0.1)"`
	Tolerance        *int     `json:"tolerance,omitempty"         jsonschema:"until: per-channel difference ignored as noise (0-255, default 8)"`
	MaxDimension     int      `json:"max_dimension,omitempty"     jsonschema:"until: longest edge of the polled screenshots (256-4096, default 512)"`
}

// Retry re-runs a tool step until its expect checks pass.
type Retry struct {
	MaxAttempts int `json:"max_attempts" jsonschema:"total attempts including the first (2-50)"`
	DelayMS     int `json:"delay_ms,omitempty" jsonschema:"pause between attempts in ms (0-60000, default 1000)"`
}

// Check is one declarative condition. All checks in a list must pass
// (AND); Negate inverts a single check.
type Check struct {
	Check            string   `json:"check"                      jsonschema:"exit_code | matches | contains | image_changed | succeeded"`
	Step             string   `json:"step,omitempty"             jsonschema:"id of the step whose result is checked; in expect it defaults to the step itself"`
	Negate           bool     `json:"negate,omitempty"           jsonschema:"invert the result"`
	Equals           *int     `json:"equals,omitempty"           jsonschema:"exit_code: expected value (default 0)"`
	Field            string   `json:"field,omitempty"            jsonschema:"matches/contains: output field to read (default stdout; also stderr, ...)"`
	Regex            string   `json:"regex,omitempty"            jsonschema:"matches: RE2 regular expression, unanchored, max 1024 chars"`
	Text             string   `json:"text,omitempty"             jsonschema:"contains: substring"`
	From             string   `json:"from,omitempty"             jsonschema:"image_changed: id of the baseline capture step"`
	To               string   `json:"to,omitempty"               jsonschema:"image_changed: id of the later capture step (default: the step itself)"`
	ThresholdPercent *float64 `json:"threshold_percent,omitempty" jsonschema:"image_changed: percent of pixels that must differ (0-100, default 0.1)"`
	Tolerance        *int     `json:"tolerance,omitempty"        jsonschema:"image_changed: per-channel difference ignored as noise (0-255, default 8)"`
}

// ---- Output (the report) --------------------------------------------------

// Step and sequence statuses.
const (
	StepOK                = "ok"
	StepError             = "error"
	StepSkipped           = "skipped"
	StepExpectationFailed = "expectation_failed"
	StepNotRun            = "not_run"

	StatusCompleted             = "completed"
	StatusCompletedWithFailures = "completed_with_failures"
	StatusStopped               = "stopped"
	StatusFailed                = "failed"
	StatusTimeout               = "timeout"
	StatusValidated             = "validated"
)

// Check phases in the report.
const (
	PhaseWhen   = "when"
	PhaseExpect = "expect"
	PhaseGate   = "gate"
)

// SequenceOutput is the structured result of parallels_vm_sequence.
type SequenceOutput struct {
	VMID       string       `json:"vm_id"`
	Status     string       `json:"status"`
	StoppedAt  *int         `json:"stopped_at,omitempty"   jsonschema:"0-based index of the step that stopped or failed the run"`
	StopReason string       `json:"stop_reason,omitempty"`
	DurationMS int64        `json:"duration_ms"`
	StepsRun   int          `json:"steps_run"`
	ImageCount int          `json:"image_count" jsonschema:"number of image blocks that follow the text block"`
	Results    []StepResult `json:"results"`
}

// StepResult is one declared step's outcome, in declaration order.
type StepResult struct {
	Index        int           `json:"index"`
	ID           string        `json:"id"`
	Kind         string        `json:"kind"   jsonschema:"tool | wait | gate"`
	Tool         string        `json:"tool,omitempty" jsonschema:"the tool that produced output (discriminates the output shape)"`
	Status       string        `json:"status"`
	Attempts     int           `json:"attempts,omitempty"`
	DurationMS   int64         `json:"duration_ms,omitempty"`
	Output       any           `json:"output,omitempty" jsonschema:"the tool's own structured output; its shape depends on tool"`
	ImageIndex   *int          `json:"image_index,omitempty"`
	ImageDropped bool          `json:"image_dropped,omitempty"`
	Checks       []CheckResult `json:"checks,omitempty"`
	Error        string        `json:"error,omitempty"`
	Waited       float64       `json:"waited_seconds,omitempty"`
}

// CheckResult is one evaluated check with its detail.
type CheckResult struct {
	Phase  string `json:"phase" jsonschema:"when | expect | gate"`
	Check  string `json:"check"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// Image is one carried image (PNG bytes from a capture step).
type Image struct {
	Data     []byte
	MIMEType string
}

// Report is the runner's full result: the JSON-serializable report plus the
// returned images (delivered as image content blocks after the text block).
type Report struct {
	Output         SequenceOutput
	ReturnedImages []Image
}

// intPtr returns a pointer to v (for the report's nullable int fields).
func intPtr(v int) *int { return &v }

// ---- Plan (validated, ready to run) ---------------------------------------

// ValidateContext carries the mode-resolved target.
type ValidateContext struct {
	Target string // the resolved VM id or name (never empty)
	Fleet  bool   // true: the runner injects vm_id into step args
}

// planCheck is a validated check with its compiled regex (matches only).
// The field is named Spec because Check.Check (the kind) would be
// confusingly shadowed by the type name.
type planCheck struct {
	Spec     Check
	Compiled *regexp.Regexp
}

// Plan is a validated sequence ready to run.
type Plan struct {
	VMID      string
	Steps     []planStep
	Timeout   time.Duration
	MaxImages int
	OnError   string // "stop" | "continue"
	Fleet     bool
}

// planStep is one validated step.
type planStep struct {
	ID     string
	Kind   string // "tool" | "wait" | "gate"
	Action Action
	Args   json.RawMessage // final args (vm_id injected in fleet mode)
	Wait   time.Duration
	// Until is non-nil for a wait-until step (Wait is then zero).
	Until *untilPlan
	// When / Expect / Gate are the validated checks (compiled regexes).
	When   []planCheck
	Expect []planCheck
	Gate   []planCheck
	// Retry is nil when the step does not retry.
	Retry    *retryPlan
	OnFail   string // "stop" | "continue"
	OnError  string // "stop" | "continue" (effective after override)
	Quiet    bool
	ToolName string
}

// untilPlan is a validated wait-until step.
type untilPlan struct {
	Kind         string // untilSettled | untilChanged
	From         string // changed: baseline capture step id ("" = capture at start)
	Timeout      time.Duration
	Poll         time.Duration
	StablePolls  int
	Threshold    float64
	Tolerance    int
	MaxDimension int
	Capture      Action          // the registered capture action (not a visible step)
	CaptureArgs  json.RawMessage // {"max_dimension": N} plus vm_id in fleet mode
}

type retryPlan struct {
	MaxAttempts int
	Delay       time.Duration
}
