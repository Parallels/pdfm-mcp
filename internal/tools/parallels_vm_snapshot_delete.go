package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMDeleteSnapshotInput is the argument schema for the
// parallels_vm_snapshot_delete tool.
type VMDeleteSnapshotInput struct {
	VMID       string `json:"vm_id,omitempty" jsonschema:"VM id or name to target"`
	SnapshotID string `json:"snapshot_id" jsonschema:"snapshot id to delete (with or without {} braces)"`
	Children   bool   `json:"children,omitempty" jsonschema:"also delete all child snapshots (prlctl -c); deleting the current snapshot reverts the VM to its parent's state"`
	Password   string `json:"password,omitempty" jsonschema:"current encryption password — required only when the VM is encrypted, fed to prlctl over stdin and never logged"`
}

// VMDeleteSnapshotOutput is the structured result of the
// parallels_vm_snapshot_delete tool.
type VMDeleteSnapshotOutput struct {
	Output string `json:"output" jsonschema:"prlctl's trimmed stdout"`
}

// VMDeleteSnapshot deletes a snapshot (prlctl snapshot-delete). This is
// IRREVERSIBLE: with children it removes the whole subtree.
func VMDeleteSnapshot(ctx context.Context, _ *mcp.CallToolRequest, in VMDeleteSnapshotInput) (*mcp.CallToolResult, VMDeleteSnapshotOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMDeleteSnapshotOutput{}, err
	}

	out, err := svc.DeleteSnapshot(ctx, target, in.SnapshotID, in.Password, in.Children)
	if err != nil {
		return nil, VMDeleteSnapshotOutput{}, err
	}
	return nil, VMDeleteSnapshotOutput{Output: out}, nil
}

// RegisterParallelsVMSnapshotDelete adds the parallels_vm_snapshot_delete
// tool for the given mode.
func RegisterParallelsVMSnapshotDelete(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name:         "parallels_vm_snapshot_delete",
		Description:  powerDescription(descVMSnapshotDelete, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: truePtr(), IdempotentHint: false, OpenWorldHint: truePtr()},
		InputSchema:  powerInputSchema[VMDeleteSnapshotInput](mode),
		OutputSchema: outputSchemaFor[VMDeleteSnapshotOutput]("parallels_vm_snapshot_delete"),
	}, VMDeleteSnapshot)
}
