package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMReset performs a hard shutdown of the VM and starts it again (prlctl reset).
func VMReset(ctx context.Context, _ *mcp.CallToolRequest, in PowerVMInput) (*mcp.CallToolResult, PowerOutput, error) {
	return runPower(ctx, parallels.PowerReset, in.VMID, false, false)
}

// RegisterParallelsVMReset adds the parallels_vm_reset tool for the given mode.
func RegisterParallelsVMReset(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "parallels_vm_reset",
		Description: powerDescription("Performs a hard shutdown of the VM and starts it again (prlctl reset).", mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
		},
		InputSchema:  powerInputSchema[PowerVMInput](mode),
		OutputSchema: outputSchemaFor[PowerOutput]("parallels_vm_reset"),
	}, VMReset)
}
