package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// DeleteInput is the argument schema of parallels_vm_delete. confirm is
// required in both modes and must repeat the resolved VM ref exactly —
// the tool's irreversible-operation safety gate.
type DeleteInput struct {
	VMID    string `json:"vm_id,omitempty" jsonschema:"VM id or name to delete; the VM and all its files are removed permanently"`
	Confirm string `json:"confirm" jsonschema:"safety gate: must be exactly the same VM id or name as vm_id (or the configured target in VM mode)"`
}

// DeleteOutput is the structured result.
type DeleteOutput struct {
	VMID   string `json:"vm_id" jsonschema:"the VM id or name that was deleted"`
	Output string `json:"output" jsonschema:"prlctl stdout (usually empty)"`
}

// VMDelete permanently deletes the target VM and all its files. The
// confirm argument must repeat the resolved VM ref exactly; on mismatch
// nothing is executed.
func VMDelete(ctx context.Context, _ *mcp.CallToolRequest, in DeleteInput) (*mcp.CallToolResult, DeleteOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, DeleteOutput{}, err
	}

	if confirm := strings.TrimSpace(in.Confirm); confirm != target {
		return nil, DeleteOutput{}, fmt.Errorf("confirm must repeat the VM id or name exactly (got %q, want %q)", in.Confirm, target)
	}

	out, err := svc.Delete(ctx, target)
	if err != nil {
		return nil, DeleteOutput{}, err
	}
	return nil, DeleteOutput{VMID: target, Output: out}, nil
}

// RegisterParallelsVMDelete adds the parallels_vm_delete tool for the
// given mode.
func RegisterParallelsVMDelete(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "parallels_vm_delete",
		Description: powerDescription(
			descVMDelete,
			mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  powerInputSchema[DeleteInput](mode),
		OutputSchema: outputSchemaFor[DeleteOutput]("parallels_vm_delete"),
	}, VMDelete)
}
