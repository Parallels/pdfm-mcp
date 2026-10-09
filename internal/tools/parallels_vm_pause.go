package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMPause pauses a running VM (prlctl pause).
func VMPause(ctx context.Context, _ *mcp.CallToolRequest, in PowerVMInput) (*mcp.CallToolResult, PowerOutput, error) {
	return runPower(ctx, parallels.PowerPause, in.VMID, false, false)
}

// RegisterParallelsVMPause adds the parallels_vm_pause tool for the given mode.
func RegisterParallelsVMPause(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name:         "parallels_vm_pause",
		Description:  powerDescription("Pauses a running VM (prlctl pause).", mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false},
		InputSchema:  powerInputSchema[PowerVMInput](mode),
		OutputSchema: outputSchemaFor[PowerOutput]("parallels_vm_pause"),
	}, VMPause)
}
