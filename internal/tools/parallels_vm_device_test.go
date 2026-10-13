package tools_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Parallels/pdfm-mcp/internal/tools"
)

func TestVMDeviceAddHandlerModes(t *testing.T) {
	cases := []struct {
		name string
		vmID string
		arg  string
		want string // expected argv target; "" = call must fail
	}{
		{"vm-mode uses configured target", vmOneID, "", vmOneID},
		{"vm-mode rejects explicit vm_id", vmOneID, vmTwoID, ""},
		{"fleet requires vm_id", "", "", ""},
		{"fleet uses given vm_id", "", "guest-x", "guest-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, tc.vmID)
			_, out, err := tools.VMDeviceAdd(context.Background(), nil, tools.VMDeviceAddInput{VMID: tc.arg, Type: "hdd"})
			if tc.want == "" {
				if err == nil {
					t.Fatal("want error")
				}
				if runner.count() != 0 {
					t.Fatalf("prlctl must not run: %d calls", runner.count())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.VMID != tc.want {
				t.Fatalf("out.VMID = %q, want %q", out.VMID, tc.want)
			}
			if len(out.Applied) != 1 || out.Applied[0] != "type=hdd" {
				t.Fatalf("applied = %v, want [type=hdd]", out.Applied)
			}
			_, args := runner.lastCall()
			want := []string{"set", tc.want, "--device-add", "hdd"}
			if !slices.Equal(args, want) {
				t.Fatalf("args = %v, want %v", args, want)
			}
		})
	}
}

func TestVMDeviceAddHandlerFields(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, out, err := tools.VMDeviceAdd(context.Background(), nil, tools.VMDeviceAddInput{
		VMID: "guest-x", Type: "net", NetType: strPtr("bridged"), NetIface: strPtr("en0"),
		Mac: strPtr("auto"), DHCP: boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantApplied := []string{"type=net", "net_type=bridged", "net_iface=en0", "mac=auto", "dhcp=yes"}
	if !slices.Equal(out.Applied, wantApplied) {
		t.Fatalf("applied = %v, want %v", out.Applied, wantApplied)
	}
	_, args := runner.lastCall()
	want := []string{"set", "guest-x", "--device-add", "net", "--type", "bridged", "--iface", "en0", "--mac", "auto", "--dhcp", "yes"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestVMDeviceAddHandlerRejectsBeforeExec(t *testing.T) {
	cases := []struct {
		name string
		in   tools.VMDeviceAddInput
		want string
	}{
		{"field on wrong type", tools.VMDeviceAddInput{VMID: "guest-x", Type: "net", SizeMB: intPtr(100)}, "does not apply"},
		{"missing type", tools.VMDeviceAddInput{VMID: "guest-x"}, "invalid type"},
		{"net without net_type", tools.VMDeviceAddInput{VMID: "guest-x", Type: "net"}, "net_type is required"},
		{"fdd type unsupported", tools.VMDeviceAddInput{VMID: "guest-x", Type: "fdd"}, "invalid type"},
		{"cdrom without image", tools.VMDeviceAddInput{VMID: "guest-x", Type: "cdrom"}, "cdrom requires image"},
		{"sound one side", tools.VMDeviceAddInput{VMID: "guest-x", Type: "sound", SoundOutput: strPtr("out")}, "requires both"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			_, _, err := tools.VMDeviceAdd(context.Background(), nil, tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if runner.count() != 0 {
				t.Fatalf("prlctl must not run: %d calls", runner.count())
			}
		})
	}
}

func TestVMDeviceSetHandler(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, out, err := tools.VMDeviceSet(context.Background(), nil, tools.VMDeviceSetInput{
		VMID: "guest-x", DeviceName: "hdd0", Enabled: boolPtr(false), SizeMB: intPtr(20480),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantApplied := []string{"device=hdd0", "enabled=false", "size_mb=20480"}
	if !slices.Equal(out.Applied, wantApplied) {
		t.Fatalf("applied = %v, want %v", out.Applied, wantApplied)
	}
	_, args := runner.lastCall()
	want := []string{"set", "guest-x", "--device-set", "hdd0", "--disable", "--size", "20480"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestVMDeviceSetHandlerRejectsBeforeExec(t *testing.T) {
	cases := []struct {
		name string
		in   tools.VMDeviceSetInput
		want string
	}{
		{"both toggles", tools.VMDeviceSetInput{VMID: "guest-x", DeviceName: "hdd0", Enabled: boolPtr(true), Connected: boolPtr(false)}, "mutually exclusive"},
		{"nothing set", tools.VMDeviceSetInput{VMID: "guest-x", DeviceName: "hdd0"}, "at least one"},
		{"bad device name", tools.VMDeviceSetInput{VMID: "guest-x", DeviceName: "disk", Enabled: boolPtr(true)}, "indexed device name"},
		{"usb config field", tools.VMDeviceSetInput{VMID: "guest-x", DeviceName: "usb", SizeMB: intPtr(100)}, "does not apply to usb devices"},
		{"field on wrong type", tools.VMDeviceSetInput{VMID: "guest-x", DeviceName: "net0", SizeMB: intPtr(100)}, "does not apply"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			_, _, err := tools.VMDeviceSet(context.Background(), nil, tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if runner.count() != 0 {
				t.Fatalf("prlctl must not run: %d calls", runner.count())
			}
		})
	}
}

func TestVMDeviceDelHandler(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, out, err := tools.VMDeviceDel(context.Background(), nil, tools.VMDeviceDelInput{
		VMID: "guest-x", DeviceName: "hdd1",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantApplied := []string{"device=hdd1", "image_action=detach_only"}
	if !slices.Equal(out.Applied, wantApplied) {
		t.Fatalf("applied = %v, want %v", out.Applied, wantApplied)
	}
	_, args := runner.lastCall()
	want := []string{"set", "guest-x", "--device-del", "hdd1", "--detach-only"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestVMDeviceDelHandlerConfirmGate(t *testing.T) {
	cases := []struct {
		name    string
		in      tools.VMDeviceDelInput
		wantErr string // "" = success
	}{
		{"destroy with matching confirm", tools.VMDeviceDelInput{VMID: "guest-x", DeviceName: "hdd1", ImageAction: "destroy_image", Confirm: "hdd1"}, ""},
		{"destroy without confirm", tools.VMDeviceDelInput{VMID: "guest-x", DeviceName: "hdd1", ImageAction: "destroy_image"}, "confirm must repeat"},
		{"destroy with wrong confirm", tools.VMDeviceDelInput{VMID: "guest-x", DeviceName: "hdd1", ImageAction: "destroy_image_force", Confirm: "hdd0"}, "confirm must repeat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			_, _, err := tools.VMDeviceDel(context.Background(), nil, tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				_, args := runner.lastCall()
				want := []string{"set", "guest-x", "--device-del", "hdd1", "--destroy-image"}
				if !slices.Equal(args, want) {
					t.Fatalf("args = %v, want %v", args, want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
			if runner.count() != 0 {
				t.Fatalf("prlctl must not run: %d calls", runner.count())
			}
		})
	}
}
