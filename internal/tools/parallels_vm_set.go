package tools

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// descVMSet is the mode-independent part of the parallels_vm_set
// description (powerDescription appends the mode clause).
const descVMSet = "Changes the VM's configuration (prlctl set). Provide only the fields to change (at least one is required); every other setting is left as-is. Hardware fields (cpus, memsize_mb, videosize_mb) require a stopped VM — prlctl rejects the change otherwise and its error is returned. Some changes (for example tpm or efi_boot) only take effect after the VM is fully shut down and started again. Verify the result with parallels_vm_info."

// VMSetInput is the argument schema of parallels_vm_set (spec 027). Every
// configuration field is optional and individually validated; the handler
// rejects a call that sets none of them.
type VMSetInput struct {
	VMID string `json:"vm_id,omitempty" jsonschema:"VM id or name to target: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`

	// Hardware (require a stopped VM unless noted).
	Name           *string `json:"name,omitempty" jsonschema:"new display name for the VM (1-256 printable characters)"`
	Description    *string `json:"description,omitempty" jsonschema:"new description for the VM (1-256 printable characters)"`
	CPUs           *int    `json:"cpus,omitempty" jsonschema:"number of virtual CPUs (1-128); the VM must be stopped"`
	MemSizeMB      *int    `json:"memsize_mb,omitempty" jsonschema:"guest RAM in megabytes (16-131072); the VM must be stopped"`
	VideoSizeMB    *int    `json:"videosize_mb,omitempty" jsonschema:"video card memory in megabytes (2-8192); the VM must be stopped; not supported on macOS VMs"`
	ThreeDAccel    *string `json:"three_d_accelerate,omitempty" jsonschema:"3D acceleration video mode: off, highest or dx9; not supported on macOS VMs"`
	VerticalSync   *bool   `json:"vertical_sync,omitempty" jsonschema:"vertical synchronization of the guest's video output (on/off); not supported on macOS VMs"`
	HighResolution *bool   `json:"high_resolution,omitempty" jsonschema:"high-resolution video mode for retina displays (on/off); not supported on macOS VMs"`

	// Boot and startup.
	EFIBoot          *bool   `json:"efi_boot,omitempty" jsonschema:"boot with UEFI firmware (true) or legacy BIOS (false)"`
	SelectBootDevice *bool   `json:"select_boot_device,omitempty" jsonschema:"allow selecting a boot device at VM startup"`
	Autostart        *string `json:"autostart,omitempty" jsonschema:"VM autostart option: off (manual), open-window (starts when its window opens), start-app (starts when Parallels Desktop starts), start-host (starts when the Mac boots), user-login (starts at user login)"`
	AutostartDelayS  *int    `json:"autostart_delay_s,omitempty" jsonschema:"delay in seconds before the VM autostarts on host boot (0-86400)"`
	Autostop         *string `json:"autostop,omitempty" jsonschema:"what happens to the VM when the Mac shuts down: stop (hard stop), suspend (save state to disk), shutdown (graceful guest shutdown)"`
	StartupView      *string `json:"startup_view,omitempty" jsonschema:"view the VM starts in: same (as last time), window, coherence, fullscreen, modality, headless (no window)"`
	OnShutdown       *string `json:"on_shutdown,omitempty" jsonschema:"what Parallels Desktop does after the VM is shut down: window (VM window stays open), close (VM window closes), quit (Parallels Desktop quits)"`
	OnWindowClose    *string `json:"on_window_close,omitempty" jsonschema:"what happens when the VM window is closed: suspend, shutdown, stop, ask (prompt the user), keep-running"`

	// Optimization.
	FasterVM     *bool `json:"faster_vm,omitempty" jsonschema:"performance mode: true = faster VM, false = faster host"`
	AutoCompress *bool `json:"auto_compress,omitempty" jsonschema:"automatic compression of the virtual disk"`
	NestedVirt   *bool `json:"nested_virt,omitempty" jsonschema:"nested virtualization (run a hypervisor inside the guest)"`

	// Sharing.
	SmartMount      *bool                   `json:"smart_mount,omitempty" jsonschema:"share host volumes with the guest (smart mount)"`
	SharedClipboard *string                 `json:"shared_clipboard,omitempty" jsonschema:"clipboard sharing: on (both directions), host-to-guest, guest-to-host, off; not supported on macOS guests on Apple Silicon"`
	ShfHost         *bool                   `json:"shf_host,omitempty" jsonschema:"enable sharing of user-defined host folders with the guest"`
	FolderAdd       *VMSetFolderAddInput    `json:"shared_folder_add,omitempty" jsonschema:"add a host folder to share with the guest: name (the label shown in the guest, 1-256 printable characters) and path (an existing host path, 1-1024 printable characters) are required; mode is ro or rw (default rw); description is optional; enabled defaults to true"`
	FolderRemove    *VMSetFolderRemoveInput `json:"shared_folder_remove,omitempty" jsonschema:"remove a previously shared host folder, by its name"`
	FolderSet       *VMSetFolderAddInput    `json:"shared_folder_set,omitempty" jsonschema:"modify an existing shared host folder, by its name: same fields as shared_folder_add (name, path, mode, description, enabled); the name must already exist (use shared_folder_add first)"`

	// Security and misc.
	TPM                  *string `json:"tpm,omitempty" jsonschema:"virtual TPM: on, off, crb (CRB interface — required for Windows on ARM) or tis (TIS interface); takes effect after a full shutdown"`
	TimeSync             *bool   `json:"time_sync,omitempty" jsonschema:"synchronize the VM clock with the host"`
	LockOnSuspend        *bool   `json:"lock_on_suspend,omitempty" jsonschema:"always lock the guest OS screen when the VM is suspended"`
	SmartGuard           *bool   `json:"smart_guard,omitempty" jsonschema:"Smart Guard automatic snapshot protection"`
	SgNotifyBeforeCreate *bool   `json:"sg_notify_before_create,omitempty" jsonschema:"ask the user before Smart Guard creates an automatic snapshot"`
	SgIntervalS          *int    `json:"sg_interval_s,omitempty" jsonschema:"Smart Guard: seconds between automatic snapshots (1-2592000)"`
	SgMaxSnapshots       *int    `json:"sg_max_snapshots,omitempty" jsonschema:"Smart Guard: maximum number of automatic snapshots to keep (1-32)"`
	IsolateVM            *bool   `json:"isolate_vm,omitempty" jsonschema:"isolate the VM from the host (block data sharing)"`
}

// VMSetFolderAddInput mirrors interfaces.VMSharedFolderAdd on the wire.
type VMSetFolderAddInput struct {
	Name        string `json:"name" jsonschema:"label for the shared folder shown in the guest (1-256 printable characters)"`
	Path        string `json:"path" jsonschema:"existing host folder path to share (1-1024 printable characters)"`
	Mode        string `json:"mode,omitempty" jsonschema:"access mode: ro (read-only) or rw (read-write, default)"`
	Description string `json:"description,omitempty" jsonschema:"optional description shown in the guest (max 256 characters)"`
	Enabled     *bool  `json:"enabled,omitempty" jsonschema:"enable (true, default) or disable (false) the share"`
}

// VMSetFolderRemoveInput is the wire form of a shared-folder removal.
type VMSetFolderRemoveInput struct {
	Name string `json:"name" jsonschema:"name of the shared folder to remove"`
}

// VMSetOutput is the structured result of a VMSet call.
type VMSetOutput struct {
	VMID    string   `json:"vm_id" jsonschema:"the VM id or name the command targeted"`
	Output  string   `json:"output,omitempty" jsonschema:"prlctl output (usually empty on success)"`
	Applied []string `json:"applied,omitempty" jsonschema:"the settings that were sent to prlctl, e.g. cpus=8"`
}

func toVMSetOptions(in VMSetInput) interfaces.VMSetOptions {
	var o interfaces.VMSetOptions
	o.Name = in.Name
	o.Description = in.Description
	o.CPUs = in.CPUs
	o.MemSizeMB = in.MemSizeMB
	o.VideoSizeMB = in.VideoSizeMB
	o.ThreeDAccelerate = in.ThreeDAccel
	o.VerticalSync = in.VerticalSync
	o.HighResolution = in.HighResolution
	o.EFIBoot = in.EFIBoot
	o.SelectBootDevice = in.SelectBootDevice
	o.Autostart = in.Autostart
	o.AutostartDelaySecs = in.AutostartDelayS
	o.Autostop = in.Autostop
	o.StartupView = in.StartupView
	o.OnShutdown = in.OnShutdown
	o.OnWindowClose = in.OnWindowClose
	o.FasterVM = in.FasterVM
	o.AutoCompress = in.AutoCompress
	o.NestedVirt = in.NestedVirt
	o.SmartMount = in.SmartMount
	o.SharedClipboard = in.SharedClipboard
	o.ShfHost = in.ShfHost
	if in.FolderAdd != nil {
		o.FolderAdd = &interfaces.VMSharedFolderAdd{
			Name:        in.FolderAdd.Name,
			Path:        in.FolderAdd.Path,
			Mode:        in.FolderAdd.Mode,
			Description: in.FolderAdd.Description,
			Enable:      in.FolderAdd.Enabled,
		}
	}
	if in.FolderRemove != nil {
		name := in.FolderRemove.Name
		o.FolderRemove = &name
	}
	if in.FolderSet != nil {
		o.FolderSet = &interfaces.VMSharedFolderAdd{
			Name:        in.FolderSet.Name,
			Path:        in.FolderSet.Path,
			Mode:        in.FolderSet.Mode,
			Description: in.FolderSet.Description,
			Enable:      in.FolderSet.Enabled,
		}
	}
	o.TPM = in.TPM
	o.TimeSync = in.TimeSync
	o.LockOnSuspend = in.LockOnSuspend
	o.SmartGuard = in.SmartGuard
	o.SgNotifyBeforeCreate = in.SgNotifyBeforeCreate
	o.SgIntervalSecs = in.SgIntervalS
	o.SgMaxSnapshots = in.SgMaxSnapshots
	o.IsolateVM = in.IsolateVM
	return o
}

// VMSet changes the target VM's configuration (prlctl set).
func VMSet(ctx context.Context, _ *mcp.CallToolRequest, in VMSetInput) (*mcp.CallToolResult, VMSetOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMSetOutput{}, err
	}
	res, err := svc.VMSet(ctx, target, toVMSetOptions(in))
	if err != nil {
		return nil, VMSetOutput{}, err
	}
	return nil, VMSetOutput{VMID: target, Output: res.Output, Applied: res.Applied}, nil
}

// setEnums is the closed value set of parallels_vm_set's enum fields
// (spec 027), injected into the wire schema so the values are
// machine-readable, not just prose in the description.
var setEnums = map[string][]any{
	"three_d_accelerate": {"off", "highest", "dx9"},
	"autostart":          {"off", "open-window", "start-app", "start-host", "user-login"},
	"autostop":           {"stop", "suspend", "shutdown"},
	"startup_view":       {"same", "window", "coherence", "fullscreen", "modality", "headless"},
	"on_shutdown":        {"window", "close", "quit"},
	"on_window_close":    {"suspend", "shutdown", "stop", "ask", "keep-running"},
	"shared_clipboard":   {"on", "host-to-guest", "guest-to-host", "off"},
	"tpm":                {"on", "off", "crb", "tis"},
	"mode":               {"ro", "rw"}, // inside shared_folder_add
}

// applySetEnums injects the closed enum sets into the inferred input
// schema, recursing into nested objects (shared_folder_add.mode). A
// nullable pointer field was split into anyOf branches by
// splitNullableUnions: the enum goes on the non-null branch.
func applySetEnums(s *jsonschema.Schema, name string) {
	if s == nil {
		return
	}
	for n, p := range s.Properties {
		applySetEnums(p, n)
	}
	values, ok := setEnums[name]
	if !ok || s.Enum != nil {
		return
	}
	if len(s.AnyOf) == 2 {
		for _, b := range s.AnyOf {
			if b.Type != "null" {
				b.Enum = values
				return
			}
		}
		return
	}
	if s.Type != "null" {
		s.Enum = values
	}
}

// RegisterParallelsVMSet adds the parallels_vm_set tool for the given mode.
func RegisterParallelsVMSet(s *mcp.Server, mode PowerMode) {
	schema := powerInputSchema[VMSetInput](mode)
	applySetEnums(schema, "")
	register(s, &mcp.Tool{
		Name:         "parallels_vm_set",
		Description:  powerDescription(descVMSet, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false},
		InputSchema:  schema,
		OutputSchema: outputSchemaFor[VMSetOutput]("parallels_vm_set"),
	}, VMSet)
}
