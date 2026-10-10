package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
)

// VMListSnapshotItem is one snapshot in a parallels_vm_snapshot_list
// result.
type VMListSnapshotItem struct {
	ID          string `json:"id" jsonschema:"snapshot id with {} braces"`
	Name        string `json:"name" jsonschema:"snapshot name (may be empty)"`
	Date        string `json:"date" jsonschema:"creation date/time, YYYY-MM-DD HH:MM:SS"`
	State       string `json:"state" jsonschema:"VM state when the snapshot was taken: poweron / poweroff"`
	Description string `json:"description,omitempty" jsonschema:"only in single-snapshot mode"`
	Current     bool   `json:"current" jsonschema:"true for the snapshot the VM is currently running from"`
	Parent      string `json:"parent" jsonschema:"parent snapshot id; empty for roots — always empty in single-snapshot mode (prlctl does not report it there)"`
}

// VMListSnapshotsInput is the argument schema for the
// parallels_vm_snapshot_list tool.
type VMListSnapshotsInput struct {
	VMID       string `json:"vm_id,omitempty" jsonschema:"VM id or name to target: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`
	SnapshotID string `json:"snapshot_id,omitempty" jsonschema:"list this single snapshot (braces optional) instead of the whole tree"`
	Tree       bool   `json:"tree,omitempty" jsonschema:"return the ASCII snapshot tree (prlctl -t) as plain text"`
	Password   string `json:"password,omitempty" jsonschema:"current encryption password — required only when the VM is encrypted, fed to prlctl over stdin and never logged"`
}

// VMListSnapshotsOutput is the structured result of the
// parallels_vm_snapshot_list tool.
type VMListSnapshotsOutput struct {
	Snapshots []VMListSnapshotItem `json:"snapshots" jsonschema:"the whole tree (default mode); empty for a VM without snapshots"`
	Snapshot  *VMListSnapshotItem  `json:"snapshot,omitempty" jsonschema:"single-snapshot mode result"`
	NotFound  bool                 `json:"not_found,omitempty" jsonschema:"single-snapshot mode: the id is unknown (prlctl reports nothing for unknown ids)"`
	Output    string               `json:"output,omitempty" jsonschema:"ASCII tree (tree mode) or raw prlctl output when the JSON shape is unrecognized"`
}

// VMListSnapshots lists a VM's snapshots (prlctl snapshot-list): the
// whole tree as JSON by default, one snapshot with snapshot_id, or the
// ASCII tree with tree.
func VMListSnapshots(ctx context.Context, _ *mcp.CallToolRequest, in VMListSnapshotsInput) (*mcp.CallToolResult, VMListSnapshotsOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMListSnapshotsOutput{}, err
	}

	res, err := svc.ListSnapshots(ctx, target, in.SnapshotID, in.Password, in.Tree)
	if err != nil {
		return nil, VMListSnapshotsOutput{}, err
	}

	// Snapshots must be a non-nil array in the output schema (an empty
	// tree serializes as [], never null).
	out := VMListSnapshotsOutput{NotFound: res.NotFound, Output: res.Output, Snapshots: []VMListSnapshotItem{}}
	for _, sn := range res.Snapshots {
		out.Snapshots = append(out.Snapshots, VMListSnapshotItem{
			ID: sn.ID, Name: sn.Name, Date: sn.Date, State: sn.State,
			Current: sn.Current, Parent: sn.Parent,
		})
	}
	if res.Snapshot != nil {
		out.Snapshot = &VMListSnapshotItem{
			ID: res.Snapshot.ID, Name: res.Snapshot.Name, Date: res.Snapshot.Date,
			State: res.Snapshot.State, Description: res.Snapshot.Description,
			Current: res.Snapshot.Current, Parent: res.Snapshot.Parent,
		}
	}
	return nil, out, nil
}

// RegisterParallelsVMSnapshotList adds the parallels_vm_snapshot_list
// tool for the given mode.
func RegisterParallelsVMSnapshotList(s *mcp.Server, mode PowerMode) {
	register(s, &mcp.Tool{
		Name:         "parallels_vm_snapshot_list",
		Description:  powerDescription(descVMSnapshotList, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: truePtr()},
		InputSchema:  powerInputSchema[VMListSnapshotsInput](mode),
		OutputSchema: outputSchemaFor[VMListSnapshotsOutput]("parallels_vm_snapshot_list"),
	}, VMListSnapshots)
}
