package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMStart starts a stopped VM, or resumes a paused one (prlctl start).
func VMStart(ctx context.Context, _ *mcp.CallToolRequest, in PowerVMInput) (*mcp.CallToolResult, PowerOutput, error) {
	return runPower(ctx, parallels.PowerStart, in.VMID, false, false)
}

// RegisterParallelsVMStart adds the parallels_vm_start tool for the given mode.
func RegisterParallelsVMStart(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name:         "parallels_vm_start",
		Description:  powerDescription(descVMStart, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false},
		InputSchema:  powerInputSchema[PowerVMInput](mode),
		OutputSchema: outputSchemaFor[PowerOutput]("parallels_vm_start"),
	}, VMStart)
}
