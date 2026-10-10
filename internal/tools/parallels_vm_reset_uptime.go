package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
)

// VMResetUptime resets the VM uptime counter, including the counter start
// date/time (prlctl reset-uptime).
func VMResetUptime(ctx context.Context, _ *mcp.CallToolRequest, in PowerVMInput) (*mcp.CallToolResult, PowerOutput, error) {
	return runPower(ctx, parallels.PowerResetUptime, in.VMID, false, false)
}

// RegisterParallelsVMResetUptime adds the parallels_vm_reset_uptime tool for
// the given mode.
func RegisterParallelsVMResetUptime(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:         "parallels_vm_reset_uptime",
		Description:  powerDescription(descVMResetUptime, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false},
		InputSchema:  powerInputSchema[PowerVMInput](mode),
		OutputSchema: outputSchemaFor[PowerOutput]("parallels_vm_reset_uptime"),
	}, VMResetUptime)
}
