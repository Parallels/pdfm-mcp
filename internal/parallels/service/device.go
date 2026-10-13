package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// deviceAddTimeout bounds a single prlctl set --device-add command. A
// plain (pre-allocated) virtual disk does data processing; everything
// else returns immediately.
const deviceAddTimeout = 10 * time.Minute

// deviceTimeout bounds the prlctl set --device-set / --device-del
// commands (no data processing).
const deviceTimeout = 60 * time.Second

// Bounds for device management (spec 028).
const (
	minDeviceSizeMB  = 1
	maxDeviceSizeMB  = 33554432 // 32 TiB
	maxDevicePathLen = 1024
	maxDeviceDomain  = 256
)

var (
	// deviceTypes is the --device-add surface. fdd is deliberately
	// absent: prlctl rejects `--device-add fdd` at the parameter level
	// (live-verified, spec 028 §9); existing fdd devices can still be
	// removed by name via --device-del.
	deviceTypes = map[string]bool{
		"hdd": true, "cdrom": true, "net": true,
		"serial": true, "parallel": true, "sound": true, "usb": true,
	}
	deviceDiskTypes    = map[string]bool{"expand": true, "plain": true}
	deviceIfaces       = map[string]bool{"ide": true, "scsi": true, "sata": true}
	deviceSubtypes     = map[string]bool{"buslogic": true, "lsi-spi": true, "lsi-sas": true}
	deviceNetTypes     = map[string]bool{"shared": true, "bridged": true, "host-only": true}
	deviceAdapterTypes = map[string]bool{"virtio": true, "e1000": true, "e1000e": true, "rtl": true}
	deviceSocketModes  = map[string]bool{"server": true, "client": true}

	// deviceNameRE matches indexed device names as prlctl reports them
	// (hdd0, cdrom0, net0, fdd0, serial0, parallel0, sound0, …). The
	// bare keyword "usb" is additionally accepted for --device-del.
	deviceNameRE = regexp.MustCompile(`^[a-z]{2,8}[0-9]{1,3}$`)

	// macRE matches a colon-separated MAC address (case-insensitive).
	macRE = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)

	// deviceFieldsByType maps a device type to the option fields that
	// apply to it (add; host_device/passthrough included where the
	// docs allow them). A field set with a type outside its map is
	// rejected before exec.
	deviceFieldsByType = map[string]map[string]bool{
		"hdd":      {"image": true, "disk_type": true, "size_mb": true, "split": true, "iface": true, "position": true, "subtype": true, "online_compact": true, "host_device": true, "passthrough": true},
		"cdrom":    {"image": true, "iface": true, "position": true, "subtype": true, "host_device": true, "passthrough": true},
		"net":      {"net_type": true, "net_iface": true, "mac": true, "ip_add": true, "ip_del": true, "dhcp": true, "dhcp6": true, "gw": true, "gw6": true, "nameserver": true, "search_domain": true, "configure": true, "apply_ip_only": true, "adapter_type": true},
		"serial":   {"host_device": true, "output": true, "socket": true, "socket_mode": true},
		"parallel": {"host_device": true, "output": true},
		"sound":    {"sound_output": true, "sound_input": true},
		"usb":      {},
	}

	// deviceSetFieldsByType is the same mapping for --device-set: the
	// docs' modify forms carry no physical-passthrough options, and
	// host_device is only a modify option for ports.
	deviceSetFieldsByType = map[string]map[string]bool{
		"hdd":      {"image": true, "disk_type": true, "size_mb": true, "split": true, "iface": true, "position": true, "subtype": true, "online_compact": true},
		"cdrom":    {"image": true, "iface": true, "position": true, "subtype": true},
		"net":      {"net_type": true, "net_iface": true, "mac": true, "ip_add": true, "ip_del": true, "dhcp": true, "dhcp6": true, "gw": true, "gw6": true, "nameserver": true, "search_domain": true, "configure": true, "apply_ip_only": true, "adapter_type": true},
		"fdd":      {"image": true, "recreate": true},
		"serial":   {"host_device": true, "output": true, "socket": true, "socket_mode": true},
		"parallel": {"host_device": true, "output": true},
		"sound":    {"sound_output": true, "sound_input": true},
		"usb":      {},
	}

	// deviceFieldTypes / deviceSetFieldTypes invert the mappings above
	// (field → the device types it applies to), for precise
	// "does not apply to type=…" error messages.
	deviceFieldTypes    = fieldTypesOf(deviceFieldsByType)
	deviceSetFieldTypes = fieldTypesOf(deviceSetFieldsByType)
)

// fieldTypesOf inverts a type→fields map into field→sorted types.
func fieldTypesOf(m map[string]map[string]bool) map[string][]string {
	out := map[string][]string{}
	for t, fields := range m {
		for f := range fields {
			out[f] = append(out[f], t)
		}
	}
	for f := range out {
		sort.Strings(out[f])
	}
	return out
}

// deviceTypeFromName maps a device-name prefix to its device type
// (hdd0 → hdd, cdrom0 → cdrom, …). ok is false for names with no known
// prefix (including the bare keyword "usb"); those pass through to
// prlctl without a cross-field check.
func deviceTypeFromName(name string) (prefix string, ok bool) {
	// "usb" is a keyword, not an indexed prefix; it is recognized so its
	// (empty) configuration-field table applies to --device-set usb.
	for _, t := range []string{"hdd", "cdrom", "net", "fdd", "serial", "parallel", "sound", "usb"} {
		if strings.HasPrefix(name, t) {
			return t, true
		}
	}
	return "", false
}

// validDeviceString trims and bounds a printable-ASCII string field.
func validDeviceString(field, value string, maxLen int) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", fmt.Errorf("%s is empty", field)
	}
	if len(v) > maxLen {
		return "", fmt.Errorf("%s is longer than %d characters", field, maxLen)
	}
	if err := validSetPrintable(v); err != nil {
		return "", fmt.Errorf("invalid %s: %s", field, err)
	}
	return v, nil
}

// validDeviceIP validates a strict IPv4/IPv6 address.
func validDeviceIP(field, value string) (string, error) {
	v := strings.TrimSpace(value)
	if net.ParseIP(v) == nil {
		return "", fmt.Errorf("invalid %s: %q is not a valid IP address", field, value)
	}
	return v, nil
}

// validDeviceIPOrMask validates an IP address or a CIDR (addr/mask).
func validDeviceIPOrMask(field, value string) (string, error) {
	v := strings.TrimSpace(value)
	if strings.Contains(v, "/") {
		if _, _, err := net.ParseCIDR(v); err != nil {
			return "", fmt.Errorf("invalid %s: %q is not a valid IP address or CIDR", field, value)
		}
		return v, nil
	}
	return validDeviceIP(field, v)
}

// validDeviceMac validates "auto" or a colon-separated MAC address.
func validDeviceMac(field, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "auto" {
		return v, nil
	}
	if !macRE.MatchString(v) {
		return "", fmt.Errorf("invalid %s: %q must be \"auto\" or a MAC address like aa:bb:cc:dd:ee:ff", field, value)
	}
	return v, nil
}

// devicePositionLimit is the maximum device position for an interface
// type (scsi — the default — allows the widest range).
func devicePositionLimit(iface string) int {
	switch iface {
	case "ide":
		return 3
	case "sata":
		return 5
	default:
		return 6
	}
}

// validDevicePosition validates a device position against the interface
// ("" = the scsi range applies).
func validDevicePosition(v int, iface string) error {
	lim := devicePositionLimit(iface)
	if v < 0 || v > lim {
		return fmt.Errorf("invalid position %d: allowed 0-%d for interface %s", v, lim, iface)
	}
	return nil
}

// validDeviceName validates a device name (indexed pattern; the bare
// keyword "usb" is allowed for del only, per allowUSB).
func validDeviceName(field, value string, allowUSB bool) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", fmt.Errorf("%s is empty", field)
	}
	if v == "usb" {
		if !allowUSB {
			return "", fmt.Errorf("%s must be an indexed device name such as hdd0 or net0 (see the Hardware object in parallels_vm_info)", field)
		}
		return v, nil
	}
	if !deviceNameRE.MatchString(v) {
		return "", fmt.Errorf("invalid %s %q: expected an indexed device name such as hdd0, cdrom0 or net0 (see the Hardware object in parallels_vm_info)", field, value)
	}
	return v, nil
}

// buildDeviceAddArgs maps opts to the prlctl set --device-add argv
// (after "set <ref>") plus a human-readable applied list, in a fixed
// field order. Every provided value is re-validated here: tool arguments
// are untrusted even though the tool schema already bounds them.
func buildDeviceAddArgs(ref string, o interfaces.VMDeviceAddOptions) (args, applied []string, err error) {
	t := strings.TrimSpace(o.Type)
	if !deviceTypes[t] {
		return nil, nil, fmt.Errorf("invalid type %q (allowed: hdd, cdrom, net, serial, parallel, sound, usb)", o.Type)
	}
	args = []string{"set", ref, "--device-add", t}
	applied = append(applied, "type="+t)

	allowed := deviceFieldsByType[t]
	only := func(field string) error {
		if !allowed[field] {
			return fmt.Errorf("%s does not apply to type=%s (applies to: %s)", field, t, strings.Join(deviceFieldTypes[field], ", "))
		}
		return nil
	}

	// Stray fields: anything set that does not belong to this device
	// type is rejected up front (the per-type cases below only inspect
	// their own fields, so without this check they would be silently
	// dropped).
	strays := map[string]bool{
		"image": o.Image != nil, "disk_type": o.DiskType != nil, "size_mb": o.SizeMB != nil,
		"split": o.Split != nil, "iface": o.Iface != nil, "position": o.Position != nil,
		"subtype": o.Subtype != nil, "online_compact": o.OnlineCompact != nil,
		"host_device": o.HostDevice != nil, "passthrough": o.Passthrough != nil,
		"net_type": o.NetType != nil, "net_iface": o.NetIface != nil, "mac": o.Mac != nil,
		"ip_add": o.IPAdd != nil, "ip_del": o.IPDel != nil, "dhcp": o.DHCP != nil,
		"dhcp6": o.DHCP6 != nil, "gw": o.GW != nil, "gw6": o.GW6 != nil,
		"nameserver": o.Nameserver != nil, "search_domain": o.SearchDomain != nil,
		"configure": o.Configure != nil, "apply_ip_only": o.ApplyIPOnly != nil,
		"adapter_type": o.AdapterType != nil,
		"output":       o.Output != nil, "socket": o.Socket != nil, "socket_mode": o.SocketMode != nil,
		"sound_output": o.SoundOutput != nil, "sound_input": o.SoundInput != nil,
	}
	for field, set := range strays {
		if set {
			if err := only(field); err != nil {
				return nil, nil, err
			}
		}
	}

	boolArg := func(field, flag string, b *bool, yes, no string) error {
		if err := only(field); err != nil {
			return err
		}
		v := no
		if *b {
			v = yes
		}
		args = append(args, flag, v)
		applied = append(applied, field+"="+v)
		return nil
	}

	var iface string
	if o.Iface != nil {
		if err := only("iface"); err != nil {
			return nil, nil, err
		}
		if !deviceIfaces[*o.Iface] {
			return nil, nil, fmt.Errorf("invalid iface %q (allowed: ide, scsi, sata)", *o.Iface)
		}
		iface = *o.Iface
		args = append(args, "--iface", iface)
		applied = append(applied, "iface="+iface)
	}

	switch t {
	case "hdd", "cdrom":
		if o.Image != nil {
			if err := only("image"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceString("image", *o.Image, maxDevicePathLen)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--image", v)
			applied = append(applied, "image="+v)
		}
		if t == "hdd" {
			if o.DiskType != nil {
				if err := only("disk_type"); err != nil {
					return nil, nil, err
				}
				if !deviceDiskTypes[*o.DiskType] {
					return nil, nil, fmt.Errorf("invalid disk_type %q (allowed: expand, plain)", *o.DiskType)
				}
				args = append(args, "--type", *o.DiskType)
				applied = append(applied, "disk_type="+*o.DiskType)
			}
			if o.SizeMB != nil {
				if err := only("size_mb"); err != nil {
					return nil, nil, err
				}
				if err := validSetInt("size_mb", *o.SizeMB, minDeviceSizeMB, maxDeviceSizeMB); err != nil {
					return nil, nil, err
				}
				args = append(args, "--size", strconv.Itoa(*o.SizeMB))
				applied = append(applied, "size_mb="+strconv.Itoa(*o.SizeMB))
			}
			if o.Split != nil && *o.Split {
				if err := only("split"); err != nil {
					return nil, nil, err
				}
				args = append(args, "--split")
				applied = append(applied, "split=true")
			}
			if o.OnlineCompact != nil {
				if err := boolArg("online_compact", "--online-compact", o.OnlineCompact, "on", "off"); err != nil {
					return nil, nil, err
				}
			}
		}
		if o.Position != nil {
			if err := only("position"); err != nil {
				return nil, nil, err
			}
			if err := validDevicePosition(*o.Position, iface); err != nil {
				return nil, nil, err
			}
			args = append(args, "--position", strconv.Itoa(*o.Position))
			applied = append(applied, "position="+strconv.Itoa(*o.Position))
		}
		if o.Subtype != nil {
			if err := only("subtype"); err != nil {
				return nil, nil, err
			}
			if !deviceSubtypes[*o.Subtype] {
				return nil, nil, fmt.Errorf("invalid subtype %q (allowed: buslogic, lsi-spi, lsi-sas)", *o.Subtype)
			}
			args = append(args, "--subtype", *o.Subtype)
			applied = append(applied, "subtype="+*o.Subtype)
		}
		if o.HostDevice != nil {
			if err := only("host_device"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceString("host_device", *o.HostDevice, maxDevicePathLen)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--device", v)
			applied = append(applied, "host_device="+v)
		}
		if o.Passthrough != nil {
			if err := boolArg("passthrough", "--passthr", o.Passthrough, "yes", "no"); err != nil {
				return nil, nil, err
			}
		}
		// Virtual and physical drive configuration are mutually
		// exclusive; the virtual-disk fields never accompany a
		// physical passthrough.
		if o.HostDevice != nil {
			if o.Image != nil {
				return nil, nil, errors.New("image and host_device are mutually exclusive (virtual or physical drive, not both)")
			}
			for _, bad := range []struct {
				field string
				set   bool
			}{
				{"disk_type", o.DiskType != nil},
				{"size_mb", o.SizeMB != nil},
				{"split", o.Split != nil},
				{"online_compact", o.OnlineCompact != nil},
			} {
				if bad.set {
					return nil, nil, fmt.Errorf("%s is not valid with host_device (physical drive)", bad.field)
				}
			}
		}
		// prlctl refuses to create an optical drive without media:
		// a cdrom must be added with a virtual image or a physical
		// host device (live-verified, spec 028 §9).
		if t == "cdrom" && o.Image == nil && o.HostDevice == nil {
			return nil, nil, errors.New("cdrom requires image (an iso, cue, ccd or dmg file) or host_device (a physical drive)")
		}
	case "net":
		if o.NetType != nil {
			if err := only("net_type"); err != nil {
				return nil, nil, err
			}
			if !deviceNetTypes[*o.NetType] {
				return nil, nil, fmt.Errorf("invalid net_type %q (allowed: shared, bridged, host-only)", *o.NetType)
			}
			args = append(args, "--type", *o.NetType)
			applied = append(applied, "net_type="+*o.NetType)
		}
		if o.NetIface != nil {
			if err := only("net_iface"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceString("net_iface", *o.NetIface, maxDevicePathLen)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--iface", v)
			applied = append(applied, "net_iface="+v)
		}
		if o.Mac != nil {
			if err := only("mac"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceMac("mac", *o.Mac)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--mac", v)
			applied = append(applied, "mac="+v)
		}
		if o.IPAdd != nil {
			if err := only("ip_add"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceIPOrMask("ip_add", *o.IPAdd)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--ipadd", v)
			applied = append(applied, "ip_add="+v)
		}
		if o.IPDel != nil {
			if err := only("ip_del"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceIPOrMask("ip_del", *o.IPDel)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--ipdel", v)
			applied = append(applied, "ip_del="+v)
		}
		if o.DHCP != nil {
			if err := boolArg("dhcp", "--dhcp", o.DHCP, "yes", "no"); err != nil {
				return nil, nil, err
			}
		}
		if o.DHCP6 != nil {
			if err := boolArg("dhcp6", "--dhcp6", o.DHCP6, "yes", "no"); err != nil {
				return nil, nil, err
			}
		}
		if o.GW != nil {
			if err := only("gw"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceIP("gw", *o.GW)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--gw", v)
			applied = append(applied, "gw="+v)
		}
		if o.GW6 != nil {
			if err := only("gw6"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceIP("gw6", *o.GW6)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--gw6", v)
			applied = append(applied, "gw6="+v)
		}
		if o.Nameserver != nil {
			if err := only("nameserver"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceIP("nameserver", *o.Nameserver)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--nameserver", v)
			applied = append(applied, "nameserver="+v)
		}
		if o.SearchDomain != nil {
			if err := only("search_domain"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceString("search_domain", *o.SearchDomain, maxDeviceDomain)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--searchdomain", v)
			applied = append(applied, "search_domain="+v)
		}
		if o.Configure != nil {
			if err := boolArg("configure", "--configure", o.Configure, "yes", "no"); err != nil {
				return nil, nil, err
			}
		}
		if o.ApplyIPOnly != nil {
			if err := boolArg("apply_ip_only", "--apply-iponly", o.ApplyIPOnly, "yes", "no"); err != nil {
				return nil, nil, err
			}
		}
		if o.AdapterType != nil {
			if err := only("adapter_type"); err != nil {
				return nil, nil, err
			}
			if !deviceAdapterTypes[*o.AdapterType] {
				return nil, nil, fmt.Errorf("invalid adapter_type %q (allowed: virtio, e1000, e1000e, rtl)", *o.AdapterType)
			}
			args = append(args, "--adapter-type", *o.AdapterType)
			applied = append(applied, "adapter_type="+*o.AdapterType)
		}
		if o.NetType == nil {
			return nil, nil, errors.New("net_type is required for type=net (shared, bridged or host-only)")
		}
		if o.NetIface != nil && (o.NetType == nil || *o.NetType == "shared") {
			return nil, nil, errors.New("net_iface applies to type=net bridged or host-only only")
		}
	case "serial", "parallel":
		if o.HostDevice != nil {
			if err := only("host_device"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceString("host_device", *o.HostDevice, maxDevicePathLen)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--device", v)
			applied = append(applied, "host_device="+v)
		}
		if o.Output != nil {
			if err := only("output"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceString("output", *o.Output, maxDevicePathLen)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--output", v)
			applied = append(applied, "output="+v)
		}
		if t == "serial" {
			if o.Socket != nil {
				if err := only("socket"); err != nil {
					return nil, nil, err
				}
				v, err := validDeviceString("socket", *o.Socket, maxDevicePathLen)
				if err != nil {
					return nil, nil, err
				}
				args = append(args, "--socket", v)
				applied = append(applied, "socket="+v)
			}
			if o.SocketMode != nil {
				if err := only("socket_mode"); err != nil {
					return nil, nil, err
				}
				if !deviceSocketModes[*o.SocketMode] {
					return nil, nil, fmt.Errorf("invalid socket_mode %q (allowed: server, client)", *o.SocketMode)
				}
				args = append(args, "--socket-mode", *o.SocketMode)
				applied = append(applied, "socket_mode="+*o.SocketMode)
			}
		}
		count := 0
		for _, set := range []bool{o.HostDevice != nil, o.Output != nil, o.Socket != nil} {
			if set {
				count++
			}
		}
		want := "host_device, output or socket"
		if t == "parallel" {
			want = "host_device or output"
		}
		if count == 0 {
			return nil, nil, fmt.Errorf("type=%s requires exactly one of %s", t, want)
		}
		if count > 1 {
			return nil, nil, fmt.Errorf("type=%s takes exactly one of %s (got %d)", t, want, count)
		}
		if o.SocketMode != nil && o.Socket == nil {
			return nil, nil, errors.New("socket_mode requires socket")
		}
	case "sound":
		if o.SoundOutput != nil {
			if err := only("sound_output"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceString("sound_output", *o.SoundOutput, maxDevicePathLen)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--output", v)
			applied = append(applied, "sound_output="+v)
		}
		if o.SoundInput != nil {
			if err := only("sound_input"); err != nil {
				return nil, nil, err
			}
			v, err := validDeviceString("sound_input", *o.SoundInput, maxDevicePathLen)
			if err != nil {
				return nil, nil, err
			}
			args = append(args, "--input", v)
			applied = append(applied, "sound_input="+v)
		}
		if o.SoundOutput == nil || o.SoundInput == nil {
			return nil, nil, errors.New("type=sound requires both sound_output and sound_input")
		}
	case "usb":
		// no options
	}

	return args, applied, nil
}

// buildDeviceSetArgs maps opts to the prlctl set --device-set argv
// (after "set <ref>") plus a human-readable applied list, in a fixed
// field order. Validation is as in buildDeviceAddArgs, with the device-
// name prefix identifying the device type for the cross-field check.
func buildDeviceSetArgs(ref string, o interfaces.VMDeviceSetOptions) (args, applied []string, err error) {
	// Bare "usb" is a valid --device-set target for the enable/disable
	// and connect/disconnect toggles (live-verified, spec 028 §9); it has
	// no configuration fields.
	name, err := validDeviceName("device_name", o.DeviceName, true)
	if err != nil {
		return nil, nil, err
	}
	args = []string{"set", ref, "--device-set", name}
	applied = append(applied, "device="+name)

	if o.Enabled != nil && o.Connected != nil {
		return nil, nil, errors.New("enabled and connected are mutually exclusive (send one toggle per call)")
	}

	prefix, known := deviceTypeFromName(name)
	var allowed map[string]bool
	if known {
		allowed = deviceSetFieldsByType[prefix]
	}
	only := func(field string) error {
		if known && !allowed[field] {
			return fmt.Errorf("%s does not apply to %s devices (applies to: %s; got %s)", field, prefix, strings.Join(deviceSetFieldTypes[field], ", "), name)
		}
		return nil
	}
	boolArg := func(field, flag string, b *bool, yes, no string) error {
		if err := only(field); err != nil {
			return err
		}
		v := no
		if *b {
			v = yes
		}
		args = append(args, flag, v)
		applied = append(applied, field+"="+v)
		return nil
	}

	if o.Enabled != nil {
		if *o.Enabled {
			args = append(args, "--enable")
			applied = append(applied, "enabled=true")
		} else {
			args = append(args, "--disable")
			applied = append(applied, "enabled=false")
		}
	}
	if o.Connected != nil {
		if *o.Connected {
			args = append(args, "--connect")
			applied = append(applied, "connected=true")
		} else {
			args = append(args, "--disconnect")
			applied = append(applied, "connected=false")
		}
	}

	var iface string
	if o.Iface != nil {
		if err := only("iface"); err != nil {
			return nil, nil, err
		}
		if !deviceIfaces[*o.Iface] {
			return nil, nil, fmt.Errorf("invalid iface %q (allowed: ide, scsi, sata)", *o.Iface)
		}
		iface = *o.Iface
		args = append(args, "--iface", iface)
		applied = append(applied, "iface="+iface)
	}

	if o.Image != nil {
		if err := only("image"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceString("image", *o.Image, maxDevicePathLen)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--image", v)
		applied = append(applied, "image="+v)
	}
	if o.DiskType != nil {
		if err := only("disk_type"); err != nil {
			return nil, nil, err
		}
		if !deviceDiskTypes[*o.DiskType] {
			return nil, nil, fmt.Errorf("invalid disk_type %q (allowed: expand, plain)", *o.DiskType)
		}
		args = append(args, "--type", *o.DiskType)
		applied = append(applied, "disk_type="+*o.DiskType)
	}
	if o.SizeMB != nil {
		if err := only("size_mb"); err != nil {
			return nil, nil, err
		}
		if err := validSetInt("size_mb", *o.SizeMB, minDeviceSizeMB, maxDeviceSizeMB); err != nil {
			return nil, nil, err
		}
		args = append(args, "--size", strconv.Itoa(*o.SizeMB))
		applied = append(applied, "size_mb="+strconv.Itoa(*o.SizeMB))
	}
	if o.Split != nil && *o.Split {
		if err := only("split"); err != nil {
			return nil, nil, err
		}
		args = append(args, "--split")
		applied = append(applied, "split=true")
	}
	if o.Position != nil {
		if err := only("position"); err != nil {
			return nil, nil, err
		}
		if err := validDevicePosition(*o.Position, iface); err != nil {
			return nil, nil, err
		}
		args = append(args, "--position", strconv.Itoa(*o.Position))
		applied = append(applied, "position="+strconv.Itoa(*o.Position))
	}
	if o.Subtype != nil {
		if err := only("subtype"); err != nil {
			return nil, nil, err
		}
		if !deviceSubtypes[*o.Subtype] {
			return nil, nil, fmt.Errorf("invalid subtype %q (allowed: buslogic, lsi-spi, lsi-sas)", *o.Subtype)
		}
		args = append(args, "--subtype", *o.Subtype)
		applied = append(applied, "subtype="+*o.Subtype)
	}
	if o.OnlineCompact != nil {
		if err := boolArg("online_compact", "--online-compact", o.OnlineCompact, "on", "off"); err != nil {
			return nil, nil, err
		}
	}
	if o.HostDevice != nil {
		if err := only("host_device"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceString("host_device", *o.HostDevice, maxDevicePathLen)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--device", v)
		applied = append(applied, "host_device="+v)
	}
	if o.Recreate != nil && *o.Recreate {
		if err := only("recreate"); err != nil {
			return nil, nil, err
		}
		args = append(args, "--recreate")
		applied = append(applied, "recreate=true")
	}
	if o.NetType != nil {
		if err := only("net_type"); err != nil {
			return nil, nil, err
		}
		if !deviceNetTypes[*o.NetType] {
			return nil, nil, fmt.Errorf("invalid net_type %q (allowed: shared, bridged, host-only)", *o.NetType)
		}
		args = append(args, "--type", *o.NetType)
		applied = append(applied, "net_type="+*o.NetType)
	}
	if o.NetIface != nil {
		if err := only("net_iface"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceString("net_iface", *o.NetIface, maxDevicePathLen)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--iface", v)
		applied = append(applied, "net_iface="+v)
	}
	if o.Mac != nil {
		if err := only("mac"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceMac("mac", *o.Mac)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--mac", v)
		applied = append(applied, "mac="+v)
	}
	if o.IPAdd != nil {
		if err := only("ip_add"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceIPOrMask("ip_add", *o.IPAdd)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--ipadd", v)
		applied = append(applied, "ip_add="+v)
	}
	if o.IPDel != nil {
		if err := only("ip_del"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceIPOrMask("ip_del", *o.IPDel)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--ipdel", v)
		applied = append(applied, "ip_del="+v)
	}
	if o.DHCP != nil {
		if err := boolArg("dhcp", "--dhcp", o.DHCP, "yes", "no"); err != nil {
			return nil, nil, err
		}
	}
	if o.DHCP6 != nil {
		if err := boolArg("dhcp6", "--dhcp6", o.DHCP6, "yes", "no"); err != nil {
			return nil, nil, err
		}
	}
	if o.GW != nil {
		if err := only("gw"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceIP("gw", *o.GW)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--gw", v)
		applied = append(applied, "gw="+v)
	}
	if o.GW6 != nil {
		if err := only("gw6"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceIP("gw6", *o.GW6)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--gw6", v)
		applied = append(applied, "gw6="+v)
	}
	if o.Nameserver != nil {
		if err := only("nameserver"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceIP("nameserver", *o.Nameserver)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--nameserver", v)
		applied = append(applied, "nameserver="+v)
	}
	if o.SearchDomain != nil {
		if err := only("search_domain"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceString("search_domain", *o.SearchDomain, maxDeviceDomain)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--searchdomain", v)
		applied = append(applied, "search_domain="+v)
	}
	if o.Configure != nil {
		if err := boolArg("configure", "--configure", o.Configure, "yes", "no"); err != nil {
			return nil, nil, err
		}
	}
	if o.ApplyIPOnly != nil {
		if err := boolArg("apply_ip_only", "--apply-iponly", o.ApplyIPOnly, "yes", "no"); err != nil {
			return nil, nil, err
		}
	}
	if o.AdapterType != nil {
		if err := only("adapter_type"); err != nil {
			return nil, nil, err
		}
		if !deviceAdapterTypes[*o.AdapterType] {
			return nil, nil, fmt.Errorf("invalid adapter_type %q (allowed: virtio, e1000, e1000e, rtl)", *o.AdapterType)
		}
		args = append(args, "--adapter-type", *o.AdapterType)
		applied = append(applied, "adapter_type="+*o.AdapterType)
	}
	if o.Output != nil {
		if err := only("output"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceString("output", *o.Output, maxDevicePathLen)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--output", v)
		applied = append(applied, "output="+v)
	}
	if o.Socket != nil {
		if err := only("socket"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceString("socket", *o.Socket, maxDevicePathLen)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--socket", v)
		applied = append(applied, "socket="+v)
	}
	if o.SocketMode != nil {
		if err := only("socket_mode"); err != nil {
			return nil, nil, err
		}
		if !deviceSocketModes[*o.SocketMode] {
			return nil, nil, fmt.Errorf("invalid socket_mode %q (allowed: server, client)", *o.SocketMode)
		}
		args = append(args, "--socket-mode", *o.SocketMode)
		applied = append(applied, "socket_mode="+*o.SocketMode)
	}
	if o.SoundOutput != nil {
		if err := only("sound_output"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceString("sound_output", *o.SoundOutput, maxDevicePathLen)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--output", v)
		applied = append(applied, "sound_output="+v)
	}
	if o.SoundInput != nil {
		if err := only("sound_input"); err != nil {
			return nil, nil, err
		}
		v, err := validDeviceString("sound_input", *o.SoundInput, maxDevicePathLen)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--input", v)
		applied = append(applied, "sound_input="+v)
	}

	// Per-type rules.
	switch {
	case known && prefix == "net" && o.NetIface != nil && (o.NetType == nil || *o.NetType == "shared"):
		return nil, nil, errors.New("net_iface applies to net devices with net_type bridged or host-only")
	case known && prefix == "fdd" && o.Recreate != nil && o.Image == nil:
		return nil, nil, errors.New("recreate requires image on fdd devices")
	case known && prefix == "serial":
		count := 0
		for _, set := range []bool{o.HostDevice != nil, o.Output != nil, o.Socket != nil} {
			if set {
				count++
			}
		}
		if count > 1 {
			return nil, nil, fmt.Errorf("serial device takes exactly one of host_device, output or socket (got %d)", count)
		}
		if o.SocketMode != nil && o.Socket == nil {
			return nil, nil, errors.New("socket_mode requires socket")
		}
	case known && prefix == "parallel":
		if o.HostDevice != nil && o.Output != nil {
			return nil, nil, errors.New("parallel device takes exactly one of host_device or output")
		}
	}

	if len(applied) == 1 {
		return nil, nil, errors.New("set at least one of enabled, connected or a configuration field")
	}
	return args, applied, nil
}

// buildDeviceDelArgs maps opts to the prlctl set --device-del argv
// (after "set <ref>") plus a human-readable applied list. The destroy
// image actions require the confirm gate (Confirm equals DeviceName).
func buildDeviceDelArgs(ref string, o interfaces.VMDeviceDelOptions) (args, applied []string, err error) {
	name, err := validDeviceName("device_name", o.DeviceName, true)
	if err != nil {
		return nil, nil, err
	}
	args = []string{"set", ref, "--device-del", name}
	applied = append(applied, "device="+name)

	action := strings.TrimSpace(o.ImageAction)
	if action == "" {
		action = "detach_only"
	}
	switch action {
	case "detach_only":
		if prefix, ok := deviceTypeFromName(name); ok && prefix == "hdd" {
			args = append(args, "--detach-only")
		}
	case "destroy_image":
		if strings.TrimSpace(o.Confirm) != name {
			return nil, nil, fmt.Errorf("confirm must repeat device_name %q exactly to destroy the disk image", name)
		}
		args = append(args, "--destroy-image")
	case "destroy_image_force":
		if strings.TrimSpace(o.Confirm) != name {
			return nil, nil, fmt.Errorf("confirm must repeat device_name %q exactly to destroy the disk image (including from all snapshots)", name)
		}
		args = append(args, "--destroy-image-force")
	default:
		return nil, nil, fmt.Errorf("invalid image_action %q (allowed: detach_only, destroy_image, destroy_image_force)", o.ImageAction)
	}
	applied = append(applied, "image_action="+action)
	return args, applied, nil
}

// VMDeviceAdd adds a device to the target VM (prlctl set --device-add).
// prlctl is the authority on state/VM-type preconditions (stopped VM,
// macOS-VM unsupported adds) — its error is returned verbatim. On
// success the VM list cache is invalidated.
func (s *service) VMDeviceAdd(ctx context.Context, vmRef string, opts interfaces.VMDeviceAddOptions) (interfaces.VMDeviceResult, error) {
	if !s.enabled {
		return interfaces.VMDeviceResult{}, ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return interfaces.VMDeviceResult{}, err
	}
	args, applied, err := buildDeviceAddArgs(ref, opts)
	if err != nil {
		return interfaces.VMDeviceResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, deviceAddTimeout)
	defer cancel()

	s.logger.Debug("running prlctl set --device-add", "vm", ref, "applied", applied)
	out, err := s.runner.Run(runCtx, s.prlctlPath, args...)
	if err != nil {
		s.logger.Warn("prlctl set --device-add failed", "vm", ref, "err", err)
		return interfaces.VMDeviceResult{}, err
	}
	s.logger.Info("prlctl set --device-add completed", "vm", ref, "applied", applied)

	s.cache.invalidate()
	return interfaces.VMDeviceResult{Output: strings.TrimSpace(out), Applied: applied}, nil
}

// VMDeviceSet modifies or toggles an existing device of the target VM
// (prlctl set --device-set). prlctl is the authority on state
// preconditions (config changes need a stopped VM, connect/disconnect a
// running one) — its error is returned verbatim. On success the VM list
// cache is invalidated.
func (s *service) VMDeviceSet(ctx context.Context, vmRef string, opts interfaces.VMDeviceSetOptions) (interfaces.VMDeviceResult, error) {
	if !s.enabled {
		return interfaces.VMDeviceResult{}, ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return interfaces.VMDeviceResult{}, err
	}
	args, applied, err := buildDeviceSetArgs(ref, opts)
	if err != nil {
		return interfaces.VMDeviceResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, deviceTimeout)
	defer cancel()

	s.logger.Debug("running prlctl set --device-set", "vm", ref, "applied", applied)
	out, err := s.runner.Run(runCtx, s.prlctlPath, args...)
	if err != nil {
		s.logger.Warn("prlctl set --device-set failed", "vm", ref, "err", err)
		return interfaces.VMDeviceResult{}, err
	}
	s.logger.Info("prlctl set --device-set completed", "vm", ref, "applied", applied)

	s.cache.invalidate()
	return interfaces.VMDeviceResult{Output: strings.TrimSpace(out), Applied: applied}, nil
}

// VMDeviceDel removes a device from the target VM (prlctl set
// --device-del). prlctl is the authority on state preconditions — its
// error is returned verbatim. On success the VM list cache is
// invalidated.
func (s *service) VMDeviceDel(ctx context.Context, vmRef string, opts interfaces.VMDeviceDelOptions) (interfaces.VMDeviceResult, error) {
	if !s.enabled {
		return interfaces.VMDeviceResult{}, ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return interfaces.VMDeviceResult{}, err
	}
	args, applied, err := buildDeviceDelArgs(ref, opts)
	if err != nil {
		return interfaces.VMDeviceResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, deviceTimeout)
	defer cancel()

	s.logger.Debug("running prlctl set --device-del", "vm", ref, "applied", applied)
	out, err := s.runner.Run(runCtx, s.prlctlPath, args...)
	if err != nil {
		s.logger.Warn("prlctl set --device-del failed", "vm", ref, "err", err)
		return interfaces.VMDeviceResult{}, err
	}
	s.logger.Info("prlctl set --device-del completed", "vm", ref, "applied", applied)

	s.cache.invalidate()
	return interfaces.VMDeviceResult{Output: strings.TrimSpace(out), Applied: applied}, nil
}
