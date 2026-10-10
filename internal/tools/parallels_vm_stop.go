package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMStop stops the VM. Without kill the shutdown is graceful (Parallels
// Tools, then ACPI); with kill it is a hard shutdown; drop_state discards a
// suspended state so the VM boots afresh next time (prlctl stop).
func VMStop(ctx context.Context, _ *mcp.CallToolRequest, in PowerStopInput) (*mcp.CallToolResult, PowerOutput, error) {
	return runPower(ctx, parallels.PowerStop, in.VMID, in.Kill, in.DropState)
}

// RegisterParallelsVMStop adds the parallels_vm_stop tool for the given mode.
func RegisterParallelsVMStop(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "parallels_vm_stop",
		Description: powerDescription(
			descVMStop,
			mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
		},
		InputSchema:  powerInputSchema[PowerStopInput](mode),
		OutputSchema: outputSchemaFor[PowerOutput]("parallels_vm_stop"),
	}, VMStop)
}
