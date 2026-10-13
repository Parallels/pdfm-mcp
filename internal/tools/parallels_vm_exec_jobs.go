package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
)

// Argument limits of the async exec tools (mirrored by the service, which
// validates again).
const (
	maxJobTimeoutSeconds = 14400
	maxJobWaitMS         = 30000
)

const (
	descVMExecStart  = "Starts a long-running command (installer, build) inside the guest VM as a background job and returns a job_id immediately; use parallels_vm_exec_result to poll it and parallels_vm_exec_cancel to stop it. Same identity rules as parallels_vm_exec (default: logged-in user; user overrides; elevated=true is PRIVILEGED: guest system account root/SYSTEM, cannot be combined with user; no password support — with user set, prlctl prompts for that user's password on the terminal and the job fails with exit_code 255 unless the account has no password). Command semantics as parallels_vm_exec (program + arguments, no shell). timeout_seconds defaults to 1800, max 14400. At most 4 jobs run at once; finished jobs are kept for 1 hour. Jobs are lost when the server exits."
	descVMExecResult = "Returns the state of a background exec job (running, succeeded, failed, cancelled, timed_out), its exit_code once finished, and the next slice of stdout/stderr starting at stdout_offset/stderr_offset (up to 256 KB per stream per call; pass the returned next offsets to continue). wait_ms (0-30000) long-polls for completion. Output is retained up to 4 MB stdout / 1 MB stderr; *_truncated flags report dropped bytes."
	descVMExecCancel = "Cancels a running background exec job by killing its prlctl process (the guest command may keep running inside the guest). Cancelling a finished job is a no-op that reports its state."
)

const jobScopeFleet = " Jobs are bound to the VM they were started on."
const jobScopeVM = " Only jobs of the VM configured with --vm-id are visible."

func jobScopeNote(mode PowerMode) string {
	if mode == PowerFleet {
		return jobScopeFleet
	}
	return jobScopeVM
}

// ---- parallels_vm_exec_start ------------------------------------------------

// ExecStartInput is the argument schema of parallels_vm_exec_start.
type ExecStartInput struct {
	VMID           string `json:"vm_id,omitempty" jsonschema:"VM id or name to run the command in: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`
	Command        string `json:"command" jsonschema:"guest command line (max 4096 chars): split on unquoted whitespace into program + arguments (single/double quotes group; a backslash escapes a following space or quote, so Windows paths are safe). No shell is started in the guest, so pipes, &&, redirects, globs and $variables are not interpreted — to use them, run a shell explicitly: Linux/macOS sh -c \"...\", Windows cmd /c \"...\" or powershell -NoProfile -Command \"...\""`
	User           string `json:"user,omitempty" jsonschema:"guest user to run as; defaults to the user currently logged in in the guest. Requires that user's password, which this tool cannot provide: prlctl prompts for it on the terminal, so the call fails with exit_code 255 (Unable to authenticate the user) unless the account has no password — the typical outcome on Windows guests. Mutually exclusive with elevated"`
	Elevated       bool   `json:"elevated,omitempty" jsonschema:"PRIVILEGED: run as the guest system account (root on Linux/macOS, SYSTEM on Windows); mutually exclusive with user"`
	ResolvePaths   bool   `json:"resolve_paths,omitempty" jsonschema:"convert host paths in the command to guest paths"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"job timeout in seconds, default 1800, max 14400"`
}

// ExecStartOutput is the structured result of parallels_vm_exec_start.
type ExecStartOutput struct {
	VMID           string `json:"vm_id" jsonschema:"the VM id or name the job runs in"`
	JobID          string `json:"job_id" jsonschema:"job id to pass to parallels_vm_exec_result / parallels_vm_exec_cancel"`
	State          string `json:"state" jsonschema:"always running right after start"`
	Elevated       bool   `json:"elevated" jsonschema:"true when the job runs as the guest system account"`
	TimeoutSeconds int    `json:"timeout_seconds" jsonschema:"the effective job timeout"`
}

// VMExecStart starts a background exec job.
func VMExecStart(ctx context.Context, _ *mcp.CallToolRequest, in ExecStartInput) (*mcp.CallToolResult, ExecStartOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, ExecStartOutput{}, err
	}
	if in.TimeoutSeconds < 0 || in.TimeoutSeconds > maxJobTimeoutSeconds {
		return nil, ExecStartOutput{}, fmt.Errorf("timeout_seconds must be between 1 and %d", maxJobTimeoutSeconds)
	}
	info, err := svc.StartExecJob(ctx, target, in.Command, parallels.ExecOptions{
		User:         in.User,
		Elevated:     in.Elevated,
		ResolvePaths: in.ResolvePaths,
	}, time.Duration(in.TimeoutSeconds)*time.Second)
	if err != nil {
		return nil, ExecStartOutput{}, err
	}
	return nil, ExecStartOutput{
		VMID:           info.VM,
		JobID:          info.JobID,
		State:          "running",
		Elevated:       info.Elevated,
		TimeoutSeconds: info.TimeoutSeconds,
	}, nil
}

// RegisterParallelsVMExecStart adds the parallels_vm_exec_start tool.
func RegisterParallelsVMExecStart(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:        "parallels_vm_exec_start",
		Description: powerDescription(descVMExecStart, mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  powerInputSchema[ExecStartInput](mode),
		OutputSchema: outputSchemaFor[ExecStartOutput]("parallels_vm_exec_start"),
	}, VMExecStart)
}

// ---- parallels_vm_exec_result -----------------------------------------------

// ExecResultInput is the argument schema of parallels_vm_exec_result.
type ExecResultInput struct {
	JobID        string `json:"job_id" jsonschema:"job id returned by parallels_vm_exec_start"`
	WaitMS       int    `json:"wait_ms,omitempty" jsonschema:"long-poll up to this many milliseconds for the job to finish (0-30000, default 0)"`
	StdoutOffset int64  `json:"stdout_offset,omitempty" jsonschema:"byte offset in stdout to read from (use stdout_next of the previous call)"`
	StderrOffset int64  `json:"stderr_offset,omitempty" jsonschema:"byte offset in stderr to read from (use stderr_next of the previous call)"`
}

// ExecResultOutput is the structured result of parallels_vm_exec_result.
type ExecResultOutput struct {
	VMID            string `json:"vm_id" jsonschema:"the VM the job runs in"`
	JobID           string `json:"job_id" jsonschema:"the job id"`
	State           string `json:"state" jsonschema:"running, succeeded, failed, cancelled or timed_out"`
	Elevated        bool   `json:"elevated" jsonschema:"true when the job runs as the guest system account"`
	ExitCode        int    `json:"exit_code" jsonschema:"guest command exit code once finished; -1 while running or when no exit code exists"`
	Message         string `json:"message" jsonschema:"short explanation for non-success end states, empty otherwise"`
	ElapsedMS       int64  `json:"elapsed_ms" jsonschema:"milliseconds since the job started (until it finished)"`
	Stdout          string `json:"stdout" jsonschema:"stdout slice starting at the requested offset (max 256 KB)"`
	Stderr          string `json:"stderr" jsonschema:"stderr slice starting at the requested offset (max 256 KB)"`
	StdoutNext      int64  `json:"stdout_next" jsonschema:"offset to pass as stdout_offset next time"`
	StderrNext      int64  `json:"stderr_next" jsonschema:"offset to pass as stderr_offset next time"`
	StdoutHasMore   bool   `json:"stdout_has_more" jsonschema:"true when more retained stdout exists beyond stdout_next (read again)"`
	StderrHasMore   bool   `json:"stderr_has_more" jsonschema:"true when more retained stderr exists beyond stderr_next (read again)"`
	StdoutTruncated bool   `json:"stdout_truncated" jsonschema:"true when stdout beyond the 4 MB retention cap was dropped"`
	StderrTruncated bool   `json:"stderr_truncated" jsonschema:"true when stderr beyond the 1 MB retention cap was dropped"`
}

// VMExecResult reads the state and output of a background exec job.
func VMExecResult(ctx context.Context, _ *mcp.CallToolRequest, in ExecResultInput) (*mcp.CallToolResult, ExecResultOutput, error) {
	svc := parallels.GetInstance()
	if in.WaitMS < 0 || in.WaitMS > maxJobWaitMS {
		return nil, ExecResultOutput{}, fmt.Errorf("wait_ms must be between 0 and %d", maxJobWaitMS)
	}
	if in.StdoutOffset < 0 || in.StderrOffset < 0 {
		return nil, ExecResultOutput{}, errors.New("offsets must not be negative")
	}
	st, err := svc.ExecJobResult(ctx, in.JobID, parallels.ExecJobQuery{
		Wait:         time.Duration(in.WaitMS) * time.Millisecond,
		StdoutOffset: in.StdoutOffset,
		StderrOffset: in.StderrOffset,
	})
	if err != nil {
		return nil, ExecResultOutput{}, err
	}
	return nil, ExecResultOutput{
		VMID:            st.VM,
		JobID:           st.JobID,
		State:           st.State,
		Elevated:        st.Elevated,
		ExitCode:        st.ExitCode,
		Message:         st.Message,
		ElapsedMS:       st.ElapsedMS,
		Stdout:          st.Stdout,
		Stderr:          st.Stderr,
		StdoutNext:      st.StdoutNext,
		StderrNext:      st.StderrNext,
		StdoutHasMore:   st.StdoutHasMore,
		StderrHasMore:   st.StderrHasMore,
		StdoutTruncated: st.StdoutTruncated,
		StderrTruncated: st.StderrTruncated,
	}, nil
}

// RegisterParallelsVMExecResult adds the parallels_vm_exec_result tool.
func RegisterParallelsVMExecResult(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:        "parallels_vm_exec_result",
		Description: descVMExecResult + jobScopeNote(mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: falsePtr(),
		},
		InputSchema:  jobInputSchema[ExecResultInput]("parallels_vm_exec_result"),
		OutputSchema: outputSchemaFor[ExecResultOutput]("parallels_vm_exec_result"),
	}, VMExecResult)
}

// ---- parallels_vm_exec_cancel -----------------------------------------------

// ExecCancelInput is the argument schema of parallels_vm_exec_cancel.
type ExecCancelInput struct {
	JobID string `json:"job_id" jsonschema:"job id returned by parallels_vm_exec_start"`
}

// ExecCancelOutput is the structured result of parallels_vm_exec_cancel.
type ExecCancelOutput struct {
	VMID       string `json:"vm_id" jsonschema:"the VM the job ran in"`
	JobID      string `json:"job_id" jsonschema:"the job id"`
	State      string `json:"state" jsonschema:"job state after the cancel (cancelled when it was running)"`
	WasRunning bool   `json:"was_running" jsonschema:"true when the job was still running and was killed by this call"`
	ExitCode   int    `json:"exit_code" jsonschema:"exit code when finished; -1 otherwise"`
}

// VMExecCancel cancels a running background exec job.
func VMExecCancel(ctx context.Context, _ *mcp.CallToolRequest, in ExecCancelInput) (*mcp.CallToolResult, ExecCancelOutput, error) {
	svc := parallels.GetInstance()
	st, was, err := svc.CancelExecJob(ctx, in.JobID)
	if err != nil {
		return nil, ExecCancelOutput{}, err
	}
	return nil, ExecCancelOutput{
		VMID:       st.VM,
		JobID:      st.JobID,
		State:      st.State,
		WasRunning: was,
		ExitCode:   st.ExitCode,
	}, nil
}

// RegisterParallelsVMExecCancel adds the parallels_vm_exec_cancel tool.
func RegisterParallelsVMExecCancel(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:        "parallels_vm_exec_cancel",
		Description: descVMExecCancel + jobScopeNote(mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
			IdempotentHint:  true,
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  jobInputSchema[ExecCancelInput]("parallels_vm_exec_cancel"),
		OutputSchema: outputSchemaFor[ExecCancelOutput]("parallels_vm_exec_cancel"),
	}, VMExecCancel)
}

// jobInputSchema infers the input schema of a job tool. Unlike the power
// tools it never requires vm_id: a job id already identifies its VM.
func jobInputSchema[T any](name string) *jsonschema.Schema {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{})
	if err != nil {
		panic("tools: input schema for " + name + ": " + err.Error())
	}
	return schema
}
