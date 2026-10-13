package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// descVMDeviceDel is the mode-independent part of the
// parallels_vm_device_del description (powerDescription appends the mode
// clause).
const descVMDeviceDel = "Removes a device from the VM (prlctl set --device-del). device_name comes from the Hardware object in parallels_vm_info, or the bare keyword usb to remove USB support. The VM must be stopped (prlctl rejects otherwise and its error is returned). For virtual hard drives the disk image is detached and preserved by default (detach_only); destroy_image and destroy_image_force permanently delete the image (the latter also from all snapshots) and require confirm to repeat device_name exactly."

// VMDeviceDelInput is the argument schema of parallels_vm_device_del
// (spec 028). DeviceName is required; ImageAction defaults to
// detach_only; Confirm gates the two destroy modes (spec-008 pattern).
type VMDeviceDelInput struct {
	VMID        string `json:"vm_id,omitempty" jsonschema:"VM id or name to target: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`
	DeviceName  string `json:"device_name" jsonschema:"device to remove: its name from the Hardware object in parallels_vm_info (hdd0, cdrom0, net0, fdd0, serial0, parallel0, sound0), or the bare keyword usb to remove USB support"`
	ImageAction string `json:"image_action,omitempty" jsonschema:"for virtual hard drives only, what happens to the disk image: detach_only (default; the image file is preserved), destroy_image (the image is permanently deleted) or destroy_image_force (the image is permanently deleted, including from all snapshots)"`
	Confirm     string `json:"confirm,omitempty" jsonschema:"safety gate: when image_action is destroy_image or destroy_image_force, must be exactly the same device name as device_name"`
}

func toVMDeviceDelOptions(in VMDeviceDelInput) interfaces.VMDeviceDelOptions {
	return interfaces.VMDeviceDelOptions{
		DeviceName:  in.DeviceName,
		ImageAction: in.ImageAction,
		Confirm:     in.Confirm,
	}
}

// VMDeviceDel removes a device from the target VM (prlctl set
// --device-del).
func VMDeviceDel(ctx context.Context, _ *mcp.CallToolRequest, in VMDeviceDelInput) (*mcp.CallToolResult, VMDeviceOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMDeviceOutput{}, err
	}
	res, err := svc.VMDeviceDel(ctx, target, toVMDeviceDelOptions(in))
	if err != nil {
		return nil, VMDeviceOutput{}, err
	}
	return nil, VMDeviceOutput{VMID: target, Output: res.Output, Applied: res.Applied}, nil
}

// deviceDelEnums is the closed value set of parallels_vm_device_del's
// enum fields (spec 028).
var deviceDelEnums = map[string][]any{
	"image_action": {"detach_only", "destroy_image", "destroy_image_force"},
}

// RegisterParallelsVMDeviceDel adds the parallels_vm_device_del tool for
// the given mode.
func RegisterParallelsVMDeviceDel(s *mcp.Server, mode PowerMode) {
	schema := powerInputSchema[VMDeviceDelInput](mode)
	applyDeviceEnums(schema, "", deviceDelEnums)
	register(s, &mcp.Tool{
		Name:        "parallels_vm_device_del",
		Description: powerDescription(descVMDeviceDel, mode),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: truePtr(),
			OpenWorldHint:   falsePtr(),
		},
		InputSchema:  schema,
		OutputSchema: outputSchemaFor[VMDeviceOutput]("parallels_vm_device_del"),
	}, VMDeviceDel)
}
