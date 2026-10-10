package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMSwitchSnapshotInput is the argument schema for the
// parallels_vm_snapshot_switch tool.
type VMSwitchSnapshotInput struct {
	VMID       string `json:"vm_id,omitempty" jsonschema:"VM id or name to target"`
	SnapshotID string `json:"snapshot_id" jsonschema:"snapshot id to revert to (with or without {} braces)"`
	SkipResume bool   `json:"skip_resume,omitempty" jsonschema:"do not start the VM after the revert, even if the snapshot was taken while the VM was running"`
	Password   string `json:"password,omitempty" jsonschema:"current encryption password — required only when the VM is encrypted, fed to prlctl over stdin and never logged"`
}

// VMSwitchSnapshotOutput is the structured result of the
// parallels_vm_snapshot_switch tool.
type VMSwitchSnapshotOutput struct {
	Output string `json:"output" jsonschema:"prlctl's trimmed stdout"`
}

// VMSwitchSnapshot reverts the VM to a snapshot (prlctl snapshot-
// switch). Changes made after the snapshot are lost from the live
// state (the snapshots themselves remain listed).
func VMSwitchSnapshot(ctx context.Context, _ *mcp.CallToolRequest, in VMSwitchSnapshotInput) (*mcp.CallToolResult, VMSwitchSnapshotOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMSwitchSnapshotOutput{}, err
	}

	out, err := svc.SwitchSnapshot(ctx, target, in.SnapshotID, in.Password, in.SkipResume)
	if err != nil {
		return nil, VMSwitchSnapshotOutput{}, err
	}
	return nil, VMSwitchSnapshotOutput{Output: out}, nil
}

// RegisterParallelsVMSnapshotSwitch adds the parallels_vm_snapshot_switch
// tool for the given mode.
func RegisterParallelsVMSnapshotSwitch(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:         "parallels_vm_snapshot_switch",
		Description:  powerDescription(descVMSnapshotSwitch, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: truePtr(), IdempotentHint: false, OpenWorldHint: truePtr()},
		InputSchema:  powerInputSchema[VMSwitchSnapshotInput](mode),
		OutputSchema: outputSchemaFor[VMSwitchSnapshotOutput]("parallels_vm_snapshot_switch"),
	}, VMSwitchSnapshot)
}
