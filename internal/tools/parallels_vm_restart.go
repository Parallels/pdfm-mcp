package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMRestart gracefully shuts the VM down and starts it again (prlctl restart).
func VMRestart(ctx context.Context, _ *mcp.CallToolRequest, in PowerVMInput) (*mcp.CallToolResult, PowerOutput, error) {
	return runPower(ctx, parallels.PowerRestart, in.VMID, false, false)
}

// RegisterParallelsVMRestart adds the parallels_vm_restart tool for the given mode.
func RegisterParallelsVMRestart(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:        "parallels_vm_restart",
		Description: powerDescription(descVMRestart, mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
		},
		InputSchema:  powerInputSchema[PowerVMInput](mode),
		OutputSchema: outputSchemaFor[PowerOutput]("parallels_vm_restart"),
	}, VMRestart)
}
