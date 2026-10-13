package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// descVMDeviceSet is the mode-independent part of the
// parallels_vm_device_set description (powerDescription appends the mode
// clause).
const descVMDeviceSet = "Modifies, enables/disables or connects/disconnects an existing VM device (prlctl set --device-set). device_name comes from the Hardware object in parallels_vm_info (hdd0, cdrom0, net0, fdd0, serial0, parallel0, sound0), or the bare keyword usb to toggle USB support. Configuration changes require a stopped VM; connected (hot connect/disconnect) requires a running VM — prlctl rejects the wrong state and its error is returned. Send at most one toggle (enabled or connected, not both) plus any configuration fields that apply to the device type — each field's description states the types it applies to, and the server rejects a mismatch."

// VMDeviceSetInput is the argument schema of parallels_vm_device_set
// (spec 028). DeviceName is required; at least one of Enabled, Connected
// or a configuration field must be set (enforced in the service).
type VMDeviceSetInput struct {
	VMID       string `json:"vm_id,omitempty" jsonschema:"VM id or name to target: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`
	DeviceName string `json:"device_name" jsonschema:"existing device to modify: its name from the Hardware object in parallels_vm_info (hdd0, cdrom0, net0, fdd0, serial0, parallel0, sound0), or the bare keyword usb to toggle USB support"`
	Enabled    *bool  `json:"enabled,omitempty" jsonschema:"enable (true) or disable (false) the device; mutually exclusive with connected"`
	Connected  *bool  `json:"connected,omitempty" jsonschema:"connect (true) or disconnect (false) the device to/from the running VM — the VM must be running; mutually exclusive with enabled"`

	// Configuration (applies to the device type of device_name).
	Image         *string `json:"image,omitempty" jsonschema:"new disk image file (1-1024 printable characters). Applies to hdd (swap/attach a disk image), cdrom (iso, cue, ccd or dmg; omit to leave the tray as-is) or fdd (swap the floppy image) devices"`
	DiskType      *string `json:"disk_type,omitempty" jsonschema:"virtual disk type for hdd devices: expand (grows with data) or plain (fixed size)"`
	SizeMB        *int    `json:"size_mb,omitempty" jsonschema:"virtual disk size in megabytes for hdd devices (1-33554432)"`
	Split         *bool   `json:"split,omitempty" jsonschema:"hdd devices: split the disk image into 2GB pieces (pass true)"`
	Iface         *string `json:"iface,omitempty" jsonschema:"disk interface for hdd or cdrom devices: ide, scsi or sata (for net devices the host interface is net_iface, not this)"`
	Position      *int    `json:"position,omitempty" jsonschema:"disk position for hdd or cdrom devices: 0-3 for ide, 0-6 for scsi (default), 0-5 for sata"`
	Subtype       *string `json:"subtype,omitempty" jsonschema:"SCSI controller subtype for hdd or cdrom devices: buslogic, lsi-spi or lsi-sas"`
	OnlineCompact *bool   `json:"online_compact,omitempty" jsonschema:"hdd devices: online compaction mode (true = on, false = off)"`
	HostDevice    *string `json:"host_device,omitempty" jsonschema:"serial or parallel devices: the host port number to attach (1-1024 printable characters)"`
	Recreate      *bool   `json:"recreate,omitempty" jsonschema:"fdd devices: recreate the image file if it already exists (pass true)"`

	NetType      *string `json:"net_type,omitempty" jsonschema:"net devices: network type — shared (NAT, isolated from the host LAN), bridged (on the host's network) or host-only"`
	NetIface     *string `json:"net_iface,omitempty" jsonschema:"net devices with bridged or host-only: the host network interface to bridge to (e.g. en0)"`
	Mac          *string `json:"mac,omitempty" jsonschema:"net devices: MAC address to assign, or auto to generate one"`
	IPAdd        *string `json:"ip_add,omitempty" jsonschema:"net devices: static IP to add to the guest adapter (IP or IP/mask)"`
	IPDel        *string `json:"ip_del,omitempty" jsonschema:"net devices: static IP to remove from the guest adapter (IP or IP/mask)"`
	DHCP         *bool   `json:"dhcp,omitempty" jsonschema:"net devices: obtain IPv4 settings via DHCP (true = yes)"`
	DHCP6        *bool   `json:"dhcp6,omitempty" jsonschema:"net devices: obtain IPv6 settings via DHCP (true = yes)"`
	GW           *string `json:"gw,omitempty" jsonschema:"net devices: default IPv4 gateway (IP address)"`
	GW6          *string `json:"gw6,omitempty" jsonschema:"net devices: default IPv6 gateway (IP address)"`
	Nameserver   *string `json:"nameserver,omitempty" jsonschema:"net devices: DNS server (IP address)"`
	SearchDomain *string `json:"search_domain,omitempty" jsonschema:"net devices: DNS search domain"`
	Configure    *bool   `json:"configure,omitempty" jsonschema:"net devices: apply the IP settings above to the guest adapter (prlctl sets this automatically when any is given)"`
	ApplyIPOnly  *bool   `json:"apply_ip_only,omitempty" jsonschema:"net devices: ignore the guest's hostname, nameserver and search domain settings"`
	AdapterType  *string `json:"adapter_type,omitempty" jsonschema:"net devices: adapter emulation: virtio, e1000, e1000e or rtl"`

	Output     *string `json:"output,omitempty" jsonschema:"serial or parallel devices: file to capture the port's output (1-1024 printable characters)"`
	Socket     *string `json:"socket,omitempty" jsonschema:"serial devices: host socket to connect the port to"`
	SocketMode *string `json:"socket_mode,omitempty" jsonschema:"serial devices with socket: socket operation mode — server or client"`

	SoundOutput *string `json:"sound_output,omitempty" jsonschema:"sound devices: host audio output device name"`
	SoundInput  *string `json:"sound_input,omitempty" jsonschema:"sound devices: host audio input device name"`
}

func toVMDeviceSetOptions(in VMDeviceSetInput) interfaces.VMDeviceSetOptions {
	var o interfaces.VMDeviceSetOptions
	o.DeviceName = in.DeviceName
	o.Enabled = in.Enabled
	o.Connected = in.Connected
	o.Image = in.Image
	o.DiskType = in.DiskType
	o.SizeMB = in.SizeMB
	o.Split = in.Split
	o.Iface = in.Iface
	o.Position = in.Position
	o.Subtype = in.Subtype
	o.OnlineCompact = in.OnlineCompact
	o.HostDevice = in.HostDevice
	o.Recreate = in.Recreate
	o.NetType = in.NetType
	o.NetIface = in.NetIface
	o.Mac = in.Mac
	o.IPAdd = in.IPAdd
	o.IPDel = in.IPDel
	o.DHCP = in.DHCP
	o.DHCP6 = in.DHCP6
	o.GW = in.GW
	o.GW6 = in.GW6
	o.Nameserver = in.Nameserver
	o.SearchDomain = in.SearchDomain
	o.Configure = in.Configure
	o.ApplyIPOnly = in.ApplyIPOnly
	o.AdapterType = in.AdapterType
	o.Output = in.Output
	o.Socket = in.Socket
	o.SocketMode = in.SocketMode
	o.SoundOutput = in.SoundOutput
	o.SoundInput = in.SoundInput
	return o
}

// VMDeviceSet modifies or toggles an existing device of the target VM
// (prlctl set --device-set).
func VMDeviceSet(ctx context.Context, _ *mcp.CallToolRequest, in VMDeviceSetInput) (*mcp.CallToolResult, VMDeviceOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMDeviceOutput{}, err
	}
	res, err := svc.VMDeviceSet(ctx, target, toVMDeviceSetOptions(in))
	if err != nil {
		return nil, VMDeviceOutput{}, err
	}
	return nil, VMDeviceOutput{VMID: target, Output: res.Output, Applied: res.Applied}, nil
}

// deviceSetEnums is the closed value set of parallels_vm_device_set's
// enum fields (spec 028).
var deviceSetEnums = map[string][]any{
	"disk_type":    {"expand", "plain"},
	"iface":        {"ide", "scsi", "sata"},
	"subtype":      {"buslogic", "lsi-spi", "lsi-sas"},
	"net_type":     {"shared", "bridged", "host-only"},
	"adapter_type": {"virtio", "e1000", "e1000e", "rtl"},
	"socket_mode":  {"server", "client"},
}

// RegisterParallelsVMDeviceSet adds the parallels_vm_device_set tool for
// the given mode.
func RegisterParallelsVMDeviceSet(s *mcp.Server, mode PowerMode) {
	schema := powerInputSchema[VMDeviceSetInput](mode)
	applyDeviceEnums(schema, "", deviceSetEnums)
	register(s, &mcp.Tool{
		Name:         "parallels_vm_device_set",
		Description:  powerDescription(descVMDeviceSet, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false},
		InputSchema:  schema,
		OutputSchema: outputSchemaFor[VMDeviceOutput]("parallels_vm_device_set"),
	}, VMDeviceSet)
}
