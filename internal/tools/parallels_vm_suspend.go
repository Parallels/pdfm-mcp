package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
)

// VMSuspend suspends a running VM, keeping its state on disk (prlctl suspend).
func VMSuspend(ctx context.Context, _ *mcp.CallToolRequest, in PowerVMInput) (*mcp.CallToolResult, PowerOutput, error) {
	return runPower(ctx, parallels.PowerSuspend, in.VMID, false, false)
}

// RegisterParallelsVMSuspend adds the parallels_vm_suspend tool for the given mode.
func RegisterParallelsVMSuspend(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:         "parallels_vm_suspend",
		Description:  powerDescription(descVMSuspend, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false},
		InputSchema:  powerInputSchema[PowerVMInput](mode),
		OutputSchema: outputSchemaFor[PowerOutput]("parallels_vm_suspend"),
	}, VMSuspend)
}
