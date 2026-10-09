package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMResume resumes a paused VM (prlctl resume).
func VMResume(ctx context.Context, _ *mcp.CallToolRequest, in PowerVMInput) (*mcp.CallToolResult, PowerOutput, error) {
	return runPower(ctx, parallels.PowerResume, in.VMID, false, false)
}

// RegisterParallelsVMResume adds the parallels_vm_resume tool for the given mode.
func RegisterParallelsVMResume(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name:         "parallels_vm_resume",
		Description:  powerDescription("Resumes a paused VM (prlctl resume).", mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false},
		InputSchema:  powerInputSchema[PowerVMInput](mode),
		OutputSchema: outputSchemaFor[PowerOutput]("parallels_vm_resume"),
	}, VMResume)
}
