package tools

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// descVMDeviceAdd is the mode-independent part of the
// parallels_vm_device_add description (powerDescription appends the mode
// clause).
const descVMDeviceAdd = "Adds a device to the VM (prlctl set --device-add). Pick the type, then provide only the options that apply to it — each field's description states the types it applies to, and the server rejects a mismatch. The VM must be stopped (prlctl rejects otherwise and its error is returned); adding cdrom, serial or sound is not supported on macOS VMs, and prlctl cannot create floppy (fdd) devices. For physical drives (host_device), the host disk name comes from `prlsrvctl disk list` or `prlsrvctl info` on the host. Verify the result with parallels_vm_info."

// VMDeviceAddInput is the argument schema of parallels_vm_device_add
// (spec 028). Type is required; every other field applies only to the
// device type(s) stated in its description and is re-validated in the
// service.
type VMDeviceAddInput struct {
	VMID string `json:"vm_id,omitempty" jsonschema:"VM id or name to target: the VM's UUID (the ID field) or its exact display name (the Name field) as returned by parallels_list_vms; pass the name exactly as shown (spaces are fine, no quotes needed)"`
	Type string `json:"type" jsonschema:"device type to add: hdd (virtual or physical hard drive), cdrom (virtual or physical optical drive), net (network adapter), serial (serial port), parallel (parallel port), sound (sound card) or usb (enables USB support, no other options)"`

	// Virtual and physical disk / optical.
	Image         *string `json:"image,omitempty" jsonschema:"disk image file (1-1024 printable characters). For hdd: optional — when omitted prlctl creates a new harddiskN.hdd next to the VM config. For cdrom: required — an iso, cue, ccd or dmg file (prlctl cannot create an optical drive without media). Applies to type hdd or cdrom only"`
	DiskType      *string `json:"disk_type,omitempty" jsonschema:"virtual disk type for type=hdd: expand (grows with data, default) or plain (fixed size from creation)"`
	SizeMB        *int    `json:"size_mb,omitempty" jsonschema:"virtual disk size in megabytes for type=hdd (1-33554432, i.e. up to 32 TiB)"`
	Split         *bool   `json:"split,omitempty" jsonschema:"hdd: split the disk image into 2GB pieces (pass true)"`
	Iface         *string `json:"iface,omitempty" jsonschema:"disk interface for type=hdd or cdrom: ide, scsi or sata (for type=net the host interface is net_iface, not this)"`
	Position      *int    `json:"position,omitempty" jsonschema:"disk position for type=hdd or cdrom: 0-3 for ide, 0-6 for scsi (default), 0-5 for sata"`
	Subtype       *string `json:"subtype,omitempty" jsonschema:"SCSI controller subtype for type=hdd or cdrom: buslogic, lsi-spi or lsi-sas"`
	OnlineCompact *bool   `json:"online_compact,omitempty" jsonschema:"hdd: online compaction mode (true = on, false = off)"`

	// Physical passthrough and host ports.
	HostDevice  *string `json:"host_device,omitempty" jsonschema:"physical drive passthrough for type=hdd or cdrom: the host disk name as listed by prlsrvctl disk list or prlsrvctl info on the host; or the host port number for type=serial or parallel (1-1024 printable characters)"`
	Passthrough *bool   `json:"passthrough,omitempty" jsonschema:"hdd or cdrom with host_device: enable disk passthrough mode (true)"`

	// Network adapter.
	NetType      *string `json:"net_type,omitempty" jsonschema:"net (required): network type — shared (NAT, isolated from the host LAN), bridged (on the host's network) or host-only"`
	NetIface     *string `json:"net_iface,omitempty" jsonschema:"net with bridged or host-only: the host network interface to bridge to (e.g. en0)"`
	Mac          *string `json:"mac,omitempty" jsonschema:"net: MAC address to assign, or auto to generate one"`
	IPAdd        *string `json:"ip_add,omitempty" jsonschema:"net: static IP to add to the guest adapter (IP or IP/mask)"`
	IPDel        *string `json:"ip_del,omitempty" jsonschema:"net: static IP to remove from the guest adapter (IP or IP/mask)"`
	DHCP         *bool   `json:"dhcp,omitempty" jsonschema:"net: obtain IPv4 settings via DHCP (true = yes)"`
	DHCP6        *bool   `json:"dhcp6,omitempty" jsonschema:"net: obtain IPv6 settings via DHCP (true = yes)"`
	GW           *string `json:"gw,omitempty" jsonschema:"net: default IPv4 gateway (IP address)"`
	GW6          *string `json:"gw6,omitempty" jsonschema:"net: default IPv6 gateway (IP address)"`
	Nameserver   *string `json:"nameserver,omitempty" jsonschema:"net: DNS server (IP address)"`
	SearchDomain *string `json:"search_domain,omitempty" jsonschema:"net: DNS search domain"`
	Configure    *bool   `json:"configure,omitempty" jsonschema:"net: apply the IP settings above to the guest adapter (prlctl sets this automatically when any is given)"`
	ApplyIPOnly  *bool   `json:"apply_ip_only,omitempty" jsonschema:"net: ignore the guest's hostname, nameserver and search domain settings"`
	AdapterType  *string `json:"adapter_type,omitempty" jsonschema:"net: adapter emulation: virtio, e1000, e1000e or rtl"`

	// Serial / parallel ports.
	Output     *string `json:"output,omitempty" jsonschema:"serial or parallel: file to capture the port's output (1-1024 printable characters)"`
	Socket     *string `json:"socket,omitempty" jsonschema:"serial: host socket to connect the port to"`
	SocketMode *string `json:"socket_mode,omitempty" jsonschema:"serial with socket: socket operation mode — server or client"`

	// Sound card.
	SoundOutput *string `json:"sound_output,omitempty" jsonschema:"sound: host audio output device name (required together with sound_input)"`
	SoundInput  *string `json:"sound_input,omitempty" jsonschema:"sound: host audio input device name (required together with sound_output)"`
}

// VMDeviceOutput is the structured result of a device operation call.
type VMDeviceOutput struct {
	VMID    string   `json:"vm_id" jsonschema:"the VM id or name the command targeted"`
	Output  string   `json:"output,omitempty" jsonschema:"prlctl output (usually empty on success)"`
	Applied []string `json:"applied,omitempty" jsonschema:"the settings that were sent to prlctl"`
}

func toVMDeviceAddOptions(in VMDeviceAddInput) interfaces.VMDeviceAddOptions {
	var o interfaces.VMDeviceAddOptions
	o.Type = in.Type
	o.Image = in.Image
	o.DiskType = in.DiskType
	o.SizeMB = in.SizeMB
	o.Split = in.Split
	o.Iface = in.Iface
	o.Position = in.Position
	o.Subtype = in.Subtype
	o.OnlineCompact = in.OnlineCompact
	o.HostDevice = in.HostDevice
	o.Passthrough = in.Passthrough
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

// VMDeviceAdd adds a device to the target VM (prlctl set --device-add).
func VMDeviceAdd(ctx context.Context, _ *mcp.CallToolRequest, in VMDeviceAddInput) (*mcp.CallToolResult, VMDeviceOutput, error) {
	svc := parallels.GetInstance()
	target, err := resolvePowerTarget(svc, in.VMID)
	if err != nil {
		return nil, VMDeviceOutput{}, err
	}
	res, err := svc.VMDeviceAdd(ctx, target, toVMDeviceAddOptions(in))
	if err != nil {
		return nil, VMDeviceOutput{}, err
	}
	return nil, VMDeviceOutput{VMID: target, Output: res.Output, Applied: res.Applied}, nil
}

// deviceAddEnums is the closed value set of parallels_vm_device_add's
// enum fields (spec 028), injected into the wire schema so the values
// are machine-readable, not just prose in the description.
var deviceAddEnums = map[string][]any{
	"type":         {"hdd", "cdrom", "net", "serial", "parallel", "sound", "usb"},
	"disk_type":    {"expand", "plain"},
	"iface":        {"ide", "scsi", "sata"},
	"subtype":      {"buslogic", "lsi-spi", "lsi-sas"},
	"net_type":     {"shared", "bridged", "host-only"},
	"adapter_type": {"virtio", "e1000", "e1000e", "rtl"},
	"socket_mode":  {"server", "client"},
}

// applyDeviceEnums injects the closed enum sets into the inferred input
// schema, recursing into nested properties. Same mechanics as
// applySetEnums: a nullable pointer field was split into anyOf branches
// by splitNullableUnions, so the enum goes on the non-null branch.
func applyDeviceEnums(s *jsonschema.Schema, name string, enums map[string][]any) {
	if s == nil {
		return
	}
	for n, p := range s.Properties {
		applyDeviceEnums(p, n, enums)
	}
	values, ok := enums[name]
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

// RegisterParallelsVMDeviceAdd adds the parallels_vm_device_add tool for
// the given mode.
func RegisterParallelsVMDeviceAdd(s *mcp.Server, mode PowerMode) {
	schema := powerInputSchema[VMDeviceAddInput](mode)
	applyDeviceEnums(schema, "", deviceAddEnums)
	register(s, &mcp.Tool{
		Name:         "parallels_vm_device_add",
		Description:  powerDescription(descVMDeviceAdd, mode),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: false},
		InputSchema:  schema,
		OutputSchema: outputSchemaFor[VMDeviceOutput]("parallels_vm_device_add"),
	}, VMDeviceAdd)
}
