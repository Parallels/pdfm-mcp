package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/parallels"
)

// VMReclaimDiskInput is the argument schema for the
// parallels_vm_reclaim_disk tool.
type VMReclaimDiskInput struct {
	VMID          string `json:"vm_id,omitempty" jsonschema:"VM id or name to target"`
	Estimate      bool   `json:"estimate,omitempty" jsonschema:"only report the estimated disk-space breakdown (bytes) without reclaiming; read-only, works on running and stopped VMs"`
	AllowShutdown bool   `json:"allow_shutdown,omitempty" jsonschema:"for a real run on a running VM: shut the VM down before reclaiming (parllels 27.0.1 often reclaims while the VM stays running, so this is rarely needed)"`
	Password      string `json:"password,omitempty" jsonschema:"current encryption password — required only when the VM is encrypted, 1-256 printable characters, fed to prlctl over stdin and never logged"`
}

// VMReclaimDiskSpace is the parsed estimate report (bytes).
type VMReclaimDiskSpace struct {
	HardDisks     int64 `json:"hard_disks" jsonschema:"hard disk data, bytes"`
	Snapshots     int64 `json:"snapshots" jsonschema:"snapshot data, bytes"`
	Miscellaneous int64 `json:"miscellaneous" jsonschema:"other VM files, bytes"`
	Reclaimable   int64 `json:"reclaimable" jsonschema:"space a real run could free, bytes"`
}

// VMReclaimDiskOutput is the structured result of the
// parallels_vm_reclaim_disk tool.
type VMReclaimDiskOutput struct {
	Estimate  bool                `json:"estimate" jsonschema:"true when only the estimate was reported"`
	DiskSpace *VMReclaimDiskSpace `json:"disk_space,omitempty" jsonschema:"estimated disk-space breakdown, present for estimate runs"`
	Output    string              `json:"output" jsonschema:"prlctl's trimmed stdout"`
}

// VMReclaimDisk optimizes a VM's disk file to free host space
// (prlctl reclaim-disk), or reports the estimate only.
func VMReclaimDisk(ctx context.Context, _ *mcp.CallToolRequest, in VMReclaimDiskInput) (*mcp.CallToolResult, VMReclaimDiskOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMReclaimDiskOutput{}, err
	}

	res, err := svc.ReclaimDisk(ctx, target, in.Password, in.Estimate, in.AllowShutdown)
	if err != nil {
		return nil, VMReclaimDiskOutput{}, err
	}

	var space *VMReclaimDiskSpace
	if res.DiskSpace != nil {
		space = &VMReclaimDiskSpace{
			HardDisks:     res.DiskSpace.HardDisks,
			Snapshots:     res.DiskSpace.Snapshots,
			Miscellaneous: res.DiskSpace.Miscellaneous,
			Reclaimable:   res.DiskSpace.Reclaimable,
		}
	}
	return nil, VMReclaimDiskOutput{Estimate: res.Estimate, DiskSpace: space, Output: res.Output}, nil
}

// RegisterParallelsVMReclaimDisk adds the parallels_vm_reclaim_disk tool
// for the given mode.
func RegisterParallelsVMReclaimDisk(s *mcp.Server, mode PowerMode) {
	mcp.AddTool(s, &mcp.Tool{
		Name:         "parallels_vm_reclaim_disk",
		Description:  powerDescription(descVMReclaimDisk, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: falsePtr(), IdempotentHint: true, OpenWorldHint: truePtr()},
		InputSchema:  powerInputSchema[VMReclaimDiskInput](mode),
		OutputSchema: outputSchemaFor[VMReclaimDiskOutput]("parallels_vm_reclaim_disk"),
	}, VMReclaimDisk)
}
