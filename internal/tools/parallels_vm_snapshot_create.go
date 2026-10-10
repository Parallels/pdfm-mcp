package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMCreateSnapshotInput is the argument schema for the
// parallels_vm_snapshot_create tool.
type VMCreateSnapshotInput struct {
	VMID        string `json:"vm_id,omitempty" jsonschema:"VM id or name to target"`
	Name        string `json:"name,omitempty" jsonschema:"snapshot name, 1-128 printable characters; omitted = prlctl stores an empty name"`
	Description string `json:"description,omitempty" jsonschema:"snapshot description, up to 512 printable characters"`
	Password    string `json:"password,omitempty" jsonschema:"current encryption password — required only when the VM is encrypted, fed to prlctl over stdin and never logged"`
}

// VMCreateSnapshotOutput is the structured result of the
// parallels_vm_snapshot_create tool.
type VMCreateSnapshotOutput struct {
	SnapshotID string `json:"snapshot_id" jsonschema:"the new snapshot id (with {} braces); empty when it could not be parsed"`
	Output     string `json:"output" jsonschema:"prlctl's trimmed stdout"`
}

// VMCreateSnapshot takes a VM snapshot (prlctl snapshot). Works on
// running and stopped VMs; the VM's state is preserved.
func VMCreateSnapshot(ctx context.Context, _ *mcp.CallToolRequest, in VMCreateSnapshotInput) (*mcp.CallToolResult, VMCreateSnapshotOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMCreateSnapshotOutput{}, err
	}

	id, out, err := svc.CreateSnapshot(ctx, target, in.Name, in.Description, in.Password)
	if err != nil {
		return nil, VMCreateSnapshotOutput{}, err
	}
	return nil, VMCreateSnapshotOutput{SnapshotID: id, Output: out}, nil
}

// RegisterParallelsVMSnapshotCreate adds the parallels_vm_snapshot_create
// tool for the given mode.
func RegisterParallelsVMSnapshotCreate(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name:         "parallels_vm_snapshot_create",
		Description:  powerDescription(descVMSnapshotCreate, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: falsePtr(), IdempotentHint: false, OpenWorldHint: truePtr()},
		InputSchema:  powerInputSchema[VMCreateSnapshotInput](mode),
		OutputSchema: outputSchemaFor[VMCreateSnapshotOutput]("parallels_vm_snapshot_create"),
	}, VMCreateSnapshot)
}
