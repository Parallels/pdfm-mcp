package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// ExecInput is the argument schema of parallels_vm_exec.
type ExecInput struct {
	VMID         string `json:"vm_id,omitempty" jsonschema:"VM id or name to run the command in"`
	Command      string `json:"command" jsonschema:"command line to execute in the guest (Linux guests: bash -c), max 4096 characters"`
	User         string `json:"user,omitempty" jsonschema:"guest user to run as; defaults to the user currently logged in inside the guest; mutually exclusive with elevated"`
	Elevated     bool   `json:"elevated,omitempty" jsonschema:"PRIVILEGED: run as the guest system account (root on Linux/macOS, SYSTEM on Windows) instead of the logged-in user; mutually exclusive with user"`
	ResolvePaths bool   `json:"resolve_paths,omitempty" jsonschema:"convert host paths in the command to guest paths"`
}

// ExecOutput is the structured result. A non-zero exit_code is a normal
// result, not an error.
type ExecOutput struct {
	VMID            string `json:"vm_id" jsonschema:"the VM id or name the command ran in"`
	ExitCode        int    `json:"exit_code" jsonschema:"guest command exit code; -1 when no process ran"`
	Stdout          string `json:"stdout" jsonschema:"guest command stdout (truncated to 1 MB)"`
	Stderr          string `json:"stderr" jsonschema:"guest command stderr (truncated to 256 KB)"`
	StdoutTruncated bool   `json:"stdout_truncated" jsonschema:"true when stdout was cut at 1 MB"`
	StderrTruncated bool   `json:"stderr_truncated" jsonschema:"true when stderr was cut at 256 KB"`
}

// VMExec runs a command inside the guest VM and returns its exit code,
// stdout and stderr. Non-zero guest exits come back as normal results;
// tool errors mean prlctl itself failed (unknown/stopped VM, timeout,
// validation).
func VMExec(ctx context.Context, _ *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, ExecOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, ExecOutput{}, err
	}

	res, err := svc.ExecWithOptions(ctx, target, in.Command, parallels.ExecOptions{
		User:         in.User,
		Elevated:     in.Elevated,
		ResolvePaths: in.ResolvePaths,
	})
	if err != nil {
		return nil, ExecOutput{}, err
	}

	// nil result: the SDK auto-generates the TextContent fallback from the
	// structured output.
	return nil, ExecOutput{
		VMID:            target,
		ExitCode:        res.ExitCode,
		Stdout:          res.Stdout,
		Stderr:          res.Stderr,
		StdoutTruncated: res.StdoutTruncated,
		StderrTruncated: res.StderrTruncated,
	}, nil
}

// RegisterParallelsVMExec adds the parallels_vm_exec tool for the given
// mode.
func RegisterParallelsVMExec(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "parallels_vm_exec",
		Description: powerDescription(
			descVMExec,
			mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  powerInputSchema[ExecInput](mode),
		OutputSchema: outputSchemaFor[ExecOutput]("parallels_vm_exec"),
	}, VMExec)
}
