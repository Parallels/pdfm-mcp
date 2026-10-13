package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

func TestVMDeviceAddArgv(t *testing.T) {
	cases := []struct {
		name string
		in   interfaces.VMDeviceAddOptions
		want []string
	}{
		{"hdd-min", interfaces.VMDeviceAddOptions{Type: "hdd"}, []string{"set", "guest", "--device-add", "hdd"}},
		{"hdd-full", interfaces.VMDeviceAddOptions{
			Type: "hdd", Image: strPtr("/tmp/data.hdd"), DiskType: strPtr("plain"), SizeMB: intPtr(10240),
			Split: boolPtr(true), Iface: strPtr("sata"), Position: intPtr(2), Subtype: strPtr("lsi-spi"), OnlineCompact: boolPtr(true),
		}, []string{"set", "guest", "--device-add", "hdd", "--iface", "sata", "--image", "/tmp/data.hdd", "--type", "plain", "--size", "10240", "--split", "--online-compact", "on", "--position", "2", "--subtype", "lsi-spi"}},
		{"hdd-physical", interfaces.VMDeviceAddOptions{
			Type: "hdd", HostDevice: strPtr("disk2"), Passthrough: boolPtr(true), Iface: strPtr("scsi"), Position: intPtr(1), Subtype: strPtr("buslogic"),
		}, []string{"set", "guest", "--device-add", "hdd", "--iface", "scsi", "--position", "1", "--subtype", "buslogic", "--device", "disk2", "--passthr", "yes"}},
		{"cdrom-image", interfaces.VMDeviceAddOptions{Type: "cdrom", Image: strPtr("/tmp/a.iso")}, []string{"set", "guest", "--device-add", "cdrom", "--image", "/tmp/a.iso"}},
		{"cdrom-physical", interfaces.VMDeviceAddOptions{Type: "cdrom", HostDevice: strPtr("opt0"), Passthrough: boolPtr(false)}, []string{"set", "guest", "--device-add", "cdrom", "--device", "opt0", "--passthr", "no"}},
		{"net-shared-min", interfaces.VMDeviceAddOptions{Type: "net", NetType: strPtr("shared")}, []string{"set", "guest", "--device-add", "net", "--type", "shared"}},
		{"net-full", interfaces.VMDeviceAddOptions{
			Type: "net", NetType: strPtr("bridged"), NetIface: strPtr("en0"), Mac: strPtr("auto"),
			IPAdd: strPtr("192.168.1.5/24"), IPDel: strPtr("10.0.0.9"), DHCP: boolPtr(true), DHCP6: boolPtr(false),
			GW: strPtr("192.168.1.1"), GW6: strPtr("fe80::1"), Nameserver: strPtr("1.1.1.1"), SearchDomain: strPtr("lab.example"),
			Configure: boolPtr(true), ApplyIPOnly: boolPtr(false), AdapterType: strPtr("e1000"),
		}, []string{"set", "guest", "--device-add", "net", "--type", "bridged", "--iface", "en0", "--mac", "auto", "--ipadd", "192.168.1.5/24", "--ipdel", "10.0.0.9", "--dhcp", "yes", "--dhcp6", "no", "--gw", "192.168.1.1", "--gw6", "fe80::1", "--nameserver", "1.1.1.1", "--searchdomain", "lab.example", "--configure", "yes", "--apply-iponly", "no", "--adapter-type", "e1000"}},
		{"serial-device", interfaces.VMDeviceAddOptions{Type: "serial", HostDevice: strPtr("3")}, []string{"set", "guest", "--device-add", "serial", "--device", "3"}},
		{"serial-output", interfaces.VMDeviceAddOptions{Type: "serial", Output: strPtr("/tmp/serial.out")}, []string{"set", "guest", "--device-add", "serial", "--output", "/tmp/serial.out"}},
		{"serial-socket", interfaces.VMDeviceAddOptions{Type: "serial", Socket: strPtr("vm-serial"), SocketMode: strPtr("server")}, []string{"set", "guest", "--device-add", "serial", "--socket", "vm-serial", "--socket-mode", "server"}},
		{"parallel-output", interfaces.VMDeviceAddOptions{Type: "parallel", Output: strPtr("/tmp/lp.out")}, []string{"set", "guest", "--device-add", "parallel", "--output", "/tmp/lp.out"}},
		{"sound", interfaces.VMDeviceAddOptions{Type: "sound", SoundOutput: strPtr("out1"), SoundInput: strPtr("in1")}, []string{"set", "guest", "--device-add", "sound", "--output", "out1", "--input", "in1"}},
		{"usb", interfaces.VMDeviceAddOptions{Type: "usb"}, []string{"set", "guest", "--device-add", "usb"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{defaultOut: ""}
			svc := setService(t, runner)
			res, err := svc.VMDeviceAdd(context.Background(), "guest", tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if runner.callCount() != 1 {
				t.Fatalf("prlctl ran %d times, want 1", runner.callCount())
			}
			if got := runner.lastArgs; !equalStrings(got, tc.want) {
				t.Fatalf("args = %v, want %v", got, tc.want)
			}
			if len(res.Applied) == 0 {
				t.Fatal("applied is empty")
			}
		})
	}
}

func TestVMDeviceAddRejections(t *testing.T) {
	cases := []struct {
		name   string
		in     interfaces.VMDeviceAddOptions
		substr string
	}{
		{"empty-type", interfaces.VMDeviceAddOptions{Type: ""}, "invalid type"},
		{"bad-type", interfaces.VMDeviceAddOptions{Type: "tape"}, "invalid type"},
		{"size-on-net", interfaces.VMDeviceAddOptions{Type: "net", NetType: strPtr("shared"), SizeMB: intPtr(1)}, "size_mb does not apply to type=net"},
		{"image-on-usb", interfaces.VMDeviceAddOptions{Type: "usb", Image: strPtr("/tmp/a.hdd")}, "image does not apply to type=usb"},
		{"net-missing-nettype", interfaces.VMDeviceAddOptions{Type: "net"}, "net_type is required"},
		{"net-iface-on-shared", interfaces.VMDeviceAddOptions{Type: "net", NetType: strPtr("shared"), NetIface: strPtr("en0")}, "net_iface applies to type=net bridged or host-only"},
		{"fdd-type", interfaces.VMDeviceAddOptions{Type: "fdd"}, "invalid type"},
		{"cdrom-no-media", interfaces.VMDeviceAddOptions{Type: "cdrom"}, "cdrom requires image"},
		{"serial-nothing", interfaces.VMDeviceAddOptions{Type: "serial"}, "requires exactly one of"},
		{"serial-two", interfaces.VMDeviceAddOptions{Type: "serial", Output: strPtr("/tmp/a"), Socket: strPtr("s")}, "takes exactly one"},
		{"parallel-socket", interfaces.VMDeviceAddOptions{Type: "parallel", Socket: strPtr("s")}, "socket does not apply to type=parallel"},
		{"socket-mode-no-socket", interfaces.VMDeviceAddOptions{Type: "serial", Output: strPtr("/tmp/a"), SocketMode: strPtr("server")}, "socket_mode requires socket"},
		{"sound-one-side", interfaces.VMDeviceAddOptions{Type: "sound", SoundOutput: strPtr("out1")}, "requires both sound_output and sound_input"},
		{"hdd-both-images", interfaces.VMDeviceAddOptions{Type: "hdd", Image: strPtr("/tmp/a.hdd"), HostDevice: strPtr("disk2")}, "mutually exclusive"},
		{"hdd-physical-size", interfaces.VMDeviceAddOptions{Type: "hdd", HostDevice: strPtr("disk2"), SizeMB: intPtr(1024)}, "not valid with host_device"},
		{"position-ide", interfaces.VMDeviceAddOptions{Type: "hdd", Iface: strPtr("ide"), Position: intPtr(5)}, "allowed 0-3"},
		{"position-negative", interfaces.VMDeviceAddOptions{Type: "hdd", Position: intPtr(-1)}, "allowed 0-"},
		{"size-zero", interfaces.VMDeviceAddOptions{Type: "hdd", SizeMB: intPtr(0)}, "invalid size_mb"},
		{"size-huge", interfaces.VMDeviceAddOptions{Type: "hdd", SizeMB: intPtr(99999999999)}, "invalid size_mb"},
		{"bad-mac", interfaces.VMDeviceAddOptions{Type: "net", NetType: strPtr("shared"), Mac: strPtr("xx:yy")}, "invalid mac"},
		{"bad-gw", interfaces.VMDeviceAddOptions{Type: "net", NetType: strPtr("shared"), GW: strPtr("300.1.1.1")}, "invalid gw"},
		{"bad-ipadd", interfaces.VMDeviceAddOptions{Type: "net", NetType: strPtr("shared"), IPAdd: strPtr("abc")}, "invalid ip_add"},
		{"bad-disk-type", interfaces.VMDeviceAddOptions{Type: "hdd", DiskType: strPtr("sparse")}, "invalid disk_type"},
		{"bad-iface", interfaces.VMDeviceAddOptions{Type: "hdd", Iface: strPtr("virtio")}, "invalid iface"},
		{"bad-subtype", interfaces.VMDeviceAddOptions{Type: "hdd", Subtype: strPtr("megaide")}, "invalid subtype"},
		{"bad-adapter", interfaces.VMDeviceAddOptions{Type: "net", NetType: strPtr("shared"), AdapterType: strPtr("ne2000")}, "invalid adapter_type"},
		{"bad-socket-mode", interfaces.VMDeviceAddOptions{Type: "serial", Socket: strPtr("s"), SocketMode: strPtr("both")}, "invalid socket_mode"},
		{"nonprintable", interfaces.VMDeviceAddOptions{Type: "hdd", Image: strPtr("/tmp/a\thdd")}, "non-printable"},
		{"empty-image", interfaces.VMDeviceAddOptions{Type: "cdrom", Image: strPtr("   ")}, "image is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{defaultOut: ""}
			svc := setService(t, runner)
			_, err := svc.VMDeviceAdd(context.Background(), "guest", tc.in)
			if err == nil {
				t.Fatal("want error")
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.substr)
			}
			if runner.callCount() != 0 {
				t.Fatalf("prlctl ran %d times, want 0", runner.callCount())
			}
		})
	}
}

func TestVMDeviceSetArgv(t *testing.T) {
	cases := []struct {
		name string
		in   interfaces.VMDeviceSetOptions
		want []string
	}{
		{"enable", interfaces.VMDeviceSetOptions{DeviceName: "hdd0", Enabled: boolPtr(true)}, []string{"set", "guest", "--device-set", "hdd0", "--enable"}},
		{"disable", interfaces.VMDeviceSetOptions{DeviceName: "cdrom0", Enabled: boolPtr(false)}, []string{"set", "guest", "--device-set", "cdrom0", "--disable"}},
		{"connect", interfaces.VMDeviceSetOptions{DeviceName: "net0", Connected: boolPtr(true)}, []string{"set", "guest", "--device-set", "net0", "--connect"}},
		{"disconnect", interfaces.VMDeviceSetOptions{DeviceName: "net0", Connected: boolPtr(false)}, []string{"set", "guest", "--device-set", "net0", "--disconnect"}},
		{"hdd-config", interfaces.VMDeviceSetOptions{DeviceName: "hdd0", SizeMB: intPtr(20480)}, []string{"set", "guest", "--device-set", "hdd0", "--size", "20480"}},
		{"disable-plus-config", interfaces.VMDeviceSetOptions{DeviceName: "hdd0", Enabled: boolPtr(false), SizeMB: intPtr(20480)}, []string{"set", "guest", "--device-set", "hdd0", "--disable", "--size", "20480"}},
		{"net-config", interfaces.VMDeviceSetOptions{DeviceName: "net0", NetType: strPtr("bridged"), Mac: strPtr("auto")}, []string{"set", "guest", "--device-set", "net0", "--type", "bridged", "--mac", "auto"}},
		{"cdrom-image", interfaces.VMDeviceSetOptions{DeviceName: "cdrom0", Image: strPtr("/tmp/b.iso")}, []string{"set", "guest", "--device-set", "cdrom0", "--image", "/tmp/b.iso"}},
		{"fdd-recreate", interfaces.VMDeviceSetOptions{DeviceName: "fdd0", Image: strPtr("/tmp/b.img"), Recreate: boolPtr(true)}, []string{"set", "guest", "--device-set", "fdd0", "--image", "/tmp/b.img", "--recreate"}},
		{"serial-output", interfaces.VMDeviceSetOptions{DeviceName: "serial0", Output: strPtr("/tmp/out")}, []string{"set", "guest", "--device-set", "serial0", "--output", "/tmp/out"}},
		{"serial-device", interfaces.VMDeviceSetOptions{DeviceName: "serial0", HostDevice: strPtr("2")}, []string{"set", "guest", "--device-set", "serial0", "--device", "2"}},
		{"sound-config", interfaces.VMDeviceSetOptions{DeviceName: "sound0", SoundOutput: strPtr("out2")}, []string{"set", "guest", "--device-set", "sound0", "--output", "out2"}},
		{"usb-disable", interfaces.VMDeviceSetOptions{DeviceName: "usb", Enabled: boolPtr(false)}, []string{"set", "guest", "--device-set", "usb", "--disable"}},
		{"usb-enable", interfaces.VMDeviceSetOptions{DeviceName: "usb", Enabled: boolPtr(true)}, []string{"set", "guest", "--device-set", "usb", "--enable"}},
		{"usb-connect", interfaces.VMDeviceSetOptions{DeviceName: "usb", Connected: boolPtr(true)}, []string{"set", "guest", "--device-set", "usb", "--connect"}},
		{"unknown-prefix-passes", interfaces.VMDeviceSetOptions{DeviceName: "tape0", Enabled: boolPtr(true)}, []string{"set", "guest", "--device-set", "tape0", "--enable"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{defaultOut: ""}
			svc := setService(t, runner)
			res, err := svc.VMDeviceSet(context.Background(), "guest", tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if runner.callCount() != 1 {
				t.Fatalf("prlctl ran %d times, want 1", runner.callCount())
			}
			if got := runner.lastArgs; !equalStrings(got, tc.want) {
				t.Fatalf("args = %v, want %v", got, tc.want)
			}
			if res.Applied[0] != "device="+tc.in.DeviceName {
				t.Fatalf("applied[0] = %q, want device=%s", res.Applied[0], tc.in.DeviceName)
			}
		})
	}
}

func TestVMDeviceSetRejections(t *testing.T) {
	cases := []struct {
		name   string
		in     interfaces.VMDeviceSetOptions
		substr string
	}{
		{"empty-name", interfaces.VMDeviceSetOptions{DeviceName: ""}, "device_name is empty"},
		{"bad-name", interfaces.VMDeviceSetOptions{DeviceName: "HDD0", Enabled: boolPtr(true)}, "invalid device_name"},
		{"bad-name-no-index", interfaces.VMDeviceSetOptions{DeviceName: "hdd", Enabled: boolPtr(true)}, "invalid device_name"},
		{"usb-config-field", interfaces.VMDeviceSetOptions{DeviceName: "usb", SizeMB: intPtr(1)}, "size_mb does not apply to usb devices"},
		{"both-toggles", interfaces.VMDeviceSetOptions{DeviceName: "hdd0", Enabled: boolPtr(true), Connected: boolPtr(false)}, "mutually exclusive"},
		{"nothing-set", interfaces.VMDeviceSetOptions{DeviceName: "hdd0"}, "at least one"},
		{"size-on-net", interfaces.VMDeviceSetOptions{DeviceName: "net0", SizeMB: intPtr(1)}, "size_mb does not apply to net devices"},
		{"net-type-on-hdd", interfaces.VMDeviceSetOptions{DeviceName: "hdd0", NetType: strPtr("shared")}, "net_type does not apply to hdd devices"},
		{"host-device-on-hdd", interfaces.VMDeviceSetOptions{DeviceName: "hdd0", HostDevice: strPtr("disk2")}, "host_device does not apply to hdd devices"},
		{"recreate-no-image", interfaces.VMDeviceSetOptions{DeviceName: "fdd0", Recreate: boolPtr(true)}, "recreate requires image"},
		{"serial-two", interfaces.VMDeviceSetOptions{DeviceName: "serial0", Output: strPtr("/tmp/a"), Socket: strPtr("s")}, "takes exactly one"},
		{"parallel-two", interfaces.VMDeviceSetOptions{DeviceName: "parallel0", HostDevice: strPtr("1"), Output: strPtr("/tmp/a")}, "exactly one of host_device or output"},
		{"socket-mode-no-socket", interfaces.VMDeviceSetOptions{DeviceName: "serial0", Output: strPtr("/tmp/a"), SocketMode: strPtr("server")}, "socket_mode requires socket"},
		{"net-iface-shared", interfaces.VMDeviceSetOptions{DeviceName: "net0", NetType: strPtr("shared"), NetIface: strPtr("en0")}, "net_iface applies"},
		{"position-ide", interfaces.VMDeviceSetOptions{DeviceName: "cdrom0", Iface: strPtr("ide"), Position: intPtr(5)}, "allowed 0-3"},
		{"bad-mac", interfaces.VMDeviceSetOptions{DeviceName: "net0", Mac: strPtr("zz")}, "invalid mac"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{defaultOut: ""}
			svc := setService(t, runner)
			_, err := svc.VMDeviceSet(context.Background(), "guest", tc.in)
			if err == nil {
				t.Fatal("want error")
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.substr)
			}
			if runner.callCount() != 0 {
				t.Fatalf("prlctl ran %d times, want 0", runner.callCount())
			}
		})
	}
}

func TestVMDeviceDelArgv(t *testing.T) {
	cases := []struct {
		name string
		in   interfaces.VMDeviceDelOptions
		want []string
	}{
		{"hdd-default-detach", interfaces.VMDeviceDelOptions{DeviceName: "hdd1"}, []string{"set", "guest", "--device-del", "hdd1", "--detach-only"}},
		{"hdd-explicit-detach", interfaces.VMDeviceDelOptions{DeviceName: "hdd1", ImageAction: "detach_only"}, []string{"set", "guest", "--device-del", "hdd1", "--detach-only"}},
		{"hdd-destroy", interfaces.VMDeviceDelOptions{DeviceName: "hdd1", ImageAction: "destroy_image", Confirm: "hdd1"}, []string{"set", "guest", "--device-del", "hdd1", "--destroy-image"}},
		{"hdd-destroy-force", interfaces.VMDeviceDelOptions{DeviceName: "hdd1", ImageAction: "destroy_image_force", Confirm: "hdd1"}, []string{"set", "guest", "--device-del", "hdd1", "--destroy-image-force"}},
		{"cdrom", interfaces.VMDeviceDelOptions{DeviceName: "cdrom0"}, []string{"set", "guest", "--device-del", "cdrom0"}},
		{"usb", interfaces.VMDeviceDelOptions{DeviceName: "usb"}, []string{"set", "guest", "--device-del", "usb"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{defaultOut: ""}
			svc := setService(t, runner)
			res, err := svc.VMDeviceDel(context.Background(), "guest", tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if runner.callCount() != 1 {
				t.Fatalf("prlctl ran %d times, want 1", runner.callCount())
			}
			if got := runner.lastArgs; !equalStrings(got, tc.want) {
				t.Fatalf("args = %v, want %v", got, tc.want)
			}
			wantAction := "image_action=detach_only"
			if tc.in.ImageAction != "" {
				wantAction = "image_action=" + tc.in.ImageAction
			}
			if res.Applied[1] != wantAction {
				t.Fatalf("applied[1] = %q, want %q", res.Applied[1], wantAction)
			}
		})
	}
}

func TestVMDeviceDelRejections(t *testing.T) {
	cases := []struct {
		name   string
		in     interfaces.VMDeviceDelOptions
		substr string
	}{
		{"empty-name", interfaces.VMDeviceDelOptions{}, "device_name is empty"},
		{"bad-name", interfaces.VMDeviceDelOptions{DeviceName: "HDD 1"}, "invalid device_name"},
		{"destroy-no-confirm", interfaces.VMDeviceDelOptions{DeviceName: "hdd1", ImageAction: "destroy_image"}, "confirm must repeat"},
		{"destroy-wrong-confirm", interfaces.VMDeviceDelOptions{DeviceName: "hdd1", ImageAction: "destroy_image", Confirm: "hdd0"}, "confirm must repeat"},
		{"force-no-confirm", interfaces.VMDeviceDelOptions{DeviceName: "hdd1", ImageAction: "destroy_image_force", Confirm: "  "}, "confirm must repeat"},
		{"bad-action", interfaces.VMDeviceDelOptions{DeviceName: "hdd1", ImageAction: "wipe"}, "invalid image_action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{defaultOut: ""}
			svc := setService(t, runner)
			_, err := svc.VMDeviceDel(context.Background(), "guest", tc.in)
			if err == nil {
				t.Fatal("want error")
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.substr)
			}
			if runner.callCount() != 0 {
				t.Fatalf("prlctl ran %d times, want 0", runner.callCount())
			}
		})
	}
}

func TestVMDeviceValidatesRef(t *testing.T) {
	runner := &fakeRunner{defaultOut: ""}
	svc := setService(t, runner)
	if _, err := svc.VMDeviceAdd(context.Background(), "", interfaces.VMDeviceAddOptions{Type: "hdd"}); err == nil {
		t.Fatal("want error for empty ref")
	}
	if _, err := svc.VMDeviceSet(context.Background(), "", interfaces.VMDeviceSetOptions{DeviceName: "hdd0", Enabled: boolPtr(true)}); err == nil {
		t.Fatal("want error for empty ref")
	}
	if _, err := svc.VMDeviceDel(context.Background(), "", interfaces.VMDeviceDelOptions{DeviceName: "hdd1"}); err == nil {
		t.Fatal("want error for empty ref")
	}
	if runner.callCount() != 0 {
		t.Fatalf("prlctl ran %d times, want 0", runner.callCount())
	}
}

func TestVMDeviceFailureSurfaces(t *testing.T) {
	runner := &fakeRunner{defErr: errors.New("The virtual machine could not be found")}
	svc := setService(t, runner)
	if _, err := svc.VMDeviceAdd(context.Background(), "guest", interfaces.VMDeviceAddOptions{Type: "usb"}); err == nil || !strings.Contains(err.Error(), "could not be found") {
		t.Fatalf("err = %v, want the prlctl failure", err)
	}
	if _, err := svc.VMDeviceSet(context.Background(), "guest", interfaces.VMDeviceSetOptions{DeviceName: "hdd0", Enabled: boolPtr(true)}); err == nil {
		t.Fatal("want the prlctl failure")
	}
	if _, err := svc.VMDeviceDel(context.Background(), "guest", interfaces.VMDeviceDelOptions{DeviceName: "hdd1"}); err == nil {
		t.Fatal("want the prlctl failure")
	}
}

func TestVMDeviceInvalidatesCache(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, args []string) (string, error) {
		if args[0] == "list" {
			return sampleVMsJSON, nil
		}
		return "", nil
	}}
	svc := setService(t, runner)

	if _, err := svc.ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n := runner.callCount(); n != 1 {
		t.Fatalf("calls = %d, want 1 (initial fetch)", n)
	}

	if _, err := svc.VMDeviceAdd(context.Background(), vmOneID, interfaces.VMDeviceAddOptions{Type: "hdd"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n := runner.callCount(); n != 3 {
		t.Fatalf("calls = %d, want 3 (VMDeviceAdd invalidated the cache)", n)
	}

	if _, err := svc.VMDeviceSet(context.Background(), vmOneID, interfaces.VMDeviceSetOptions{DeviceName: "hdd1", Enabled: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n := runner.callCount(); n != 5 {
		t.Fatalf("calls = %d, want 5 (VMDeviceSet invalidated the cache)", n)
	}

	if _, err := svc.VMDeviceDel(context.Background(), vmOneID, interfaces.VMDeviceDelOptions{DeviceName: "hdd1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n := runner.callCount(); n != 7 {
		t.Fatalf("calls = %d, want 7 (VMDeviceDel invalidated the cache)", n)
	}
}

func TestVMDeviceTrimsOutput(t *testing.T) {
	runner := &fakeRunner{defaultOut: "  configured\n"}
	svc := setService(t, runner)
	res, err := svc.VMDeviceAdd(context.Background(), "guest", interfaces.VMDeviceAddOptions{Type: "usb"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "configured" {
		t.Fatalf("output = %q, want trimmed", res.Output)
	}
}

func TestVMDeviceDisabled(t *testing.T) {
	resetBetween(t)
	mustInit(t,
		WithLogger(testLogger()),
		WithLookPath(fakeLookPath(nil)),
		WithRunner(&fakeRunner{}),
		WithCacheTTL(30*time.Second),
		WithFallbackDirs(),
		WithVMID(""),
	)
	svc := GetInstance()
	if _, err := svc.VMDeviceAdd(context.Background(), "guest", interfaces.VMDeviceAddOptions{Type: "hdd"}); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
	if _, err := svc.VMDeviceSet(context.Background(), "guest", interfaces.VMDeviceSetOptions{DeviceName: "hdd0", Enabled: boolPtr(true)}); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
	if _, err := svc.VMDeviceDel(context.Background(), "guest", interfaces.VMDeviceDelOptions{DeviceName: "hdd1"}); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
