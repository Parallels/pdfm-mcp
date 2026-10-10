package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
)

// CloneInput is the argument schema of parallels_vm_clone. Name is
// required in both modes; all options map 1:1 onto prlctl clone flags.
type CloneInput struct {
	VMID              string `json:"vm_id,omitempty" jsonschema:"VM id or name of the source VM to clone"`
	Name              string `json:"name" jsonschema:"name of the new VM (required)"`
	Template          bool   `json:"template,omitempty" jsonschema:"make the clone a template"`
	Dst               string `json:"dst,omitempty" jsonschema:"directory where the clone files are stored (prlctl default if omitted)"`
	RegenerateSrcUUID bool   `json:"regenerate_src_uuid,omitempty" jsonschema:"regenerate the source ID (SMBIOS product ID also changes)"`
	Linked            bool   `json:"linked,omitempty" jsonschema:"create a linked clone (smaller and faster)"`
	SnapshotID        string `json:"snapshot_id,omitempty" jsonschema:"snapid for a snapshot-based linked clone; requires linked"`
	DetachExternalHDD string `json:"detach_external_hdd,omitempty" jsonschema:"yes|no: what to do with hard disks located outside the source VM (they are never copied)"`
}

// CloneOutput is the structured result.
type CloneOutput struct {
	VMID   string `json:"vm_id" jsonschema:"the source VM id or name"`
	Name   string `json:"name" jsonschema:"the name of the created clone"`
	Output string `json:"output" jsonschema:"prlctl stdout (usually empty)"`
}

// VMClone creates a copy of the target VM named in.Name. The source VM
// is never modified.
func VMClone(ctx context.Context, _ *mcp.CallToolRequest, in CloneInput) (*mcp.CallToolResult, CloneOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, CloneOutput{}, err
	}

	req := interfaces.CloneRequest{
		Name:              strings.TrimSpace(in.Name),
		Template:          in.Template,
		Dst:               strings.TrimSpace(in.Dst),
		RegenerateSrcUUID: in.RegenerateSrcUUID,
		Linked:            in.Linked,
		SnapshotID:        strings.TrimSpace(in.SnapshotID),
		DetachExternalHDD: in.DetachExternalHDD,
	}

	out, err := svc.Clone(ctx, target, req)
	if err != nil {
		return nil, CloneOutput{}, err
	}
	return nil, CloneOutput{VMID: target, Name: req.Name, Output: out}, nil
}

// RegisterParallelsVMClone adds the parallels_vm_clone tool for the given
// mode.
func RegisterParallelsVMClone(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "parallels_vm_clone",
		Description: powerDescription(
			descVMClone,
			mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: falsePtr(),
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  powerInputSchema[CloneInput](mode),
		OutputSchema: outputSchemaFor[CloneOutput]("parallels_vm_clone"),
	}, VMClone)
}
