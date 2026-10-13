package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }

func setService(t *testing.T, runner *fakeRunner) interfaces.ParallelsService {
	t.Helper()
	return powerService(t, runner, "")
}

func TestVMSetArgvPerField(t *testing.T) {
	cases := []struct {
		name string
		in   interfaces.VMSetOptions
		want []string
	}{
		{"name", interfaces.VMSetOptions{Name: strPtr("My VM")}, []string{"set", "guest", "--name", "My VM"}},
		{"name-unquoted", interfaces.VMSetOptions{Name: strPtr(`  "My VM"  `)}, []string{"set", "guest", "--name", "My VM"}},
		{"description", interfaces.VMSetOptions{Description: strPtr("qa guest")}, []string{"set", "guest", "--description", "qa guest"}},
		{"cpus", interfaces.VMSetOptions{CPUs: intPtr(8)}, []string{"set", "guest", "--cpus", "8"}},
		{"memsize", interfaces.VMSetOptions{MemSizeMB: intPtr(16384)}, []string{"set", "guest", "--memsize", "16384"}},
		{"videosize", interfaces.VMSetOptions{VideoSizeMB: intPtr(512)}, []string{"set", "guest", "--videosize", "512"}},
		{"3d", interfaces.VMSetOptions{ThreeDAccelerate: strPtr("dx9")}, []string{"set", "guest", "--3d-accelerate", "dx9"}},
		{"vertical-sync", interfaces.VMSetOptions{VerticalSync: boolPtr(true)}, []string{"set", "guest", "--vertical-sync", "on"}},
		{"high-resolution", interfaces.VMSetOptions{HighResolution: boolPtr(false)}, []string{"set", "guest", "--high-resolution", "off"}},
		{"efi-boot", interfaces.VMSetOptions{EFIBoot: boolPtr(true)}, []string{"set", "guest", "--efi-boot", "on"}},
		{"efi-boot-off", interfaces.VMSetOptions{EFIBoot: boolPtr(false)}, []string{"set", "guest", "--efi-boot", "off"}},
		{"select-boot-device", interfaces.VMSetOptions{SelectBootDevice: boolPtr(true)}, []string{"set", "guest", "--select-boot-device", "on"}},
		{"autostart", interfaces.VMSetOptions{Autostart: strPtr("start-host")}, []string{"set", "guest", "--autostart", "start-host"}},
		{"autostart-delay", interfaces.VMSetOptions{AutostartDelaySecs: intPtr(30)}, []string{"set", "guest", "--autostart-delay", "30"}},
		{"autostop", interfaces.VMSetOptions{Autostop: strPtr("suspend")}, []string{"set", "guest", "--autostop", "suspend"}},
		{"startup-view", interfaces.VMSetOptions{StartupView: strPtr("headless")}, []string{"set", "guest", "--startup-view", "headless"}},
		{"on-shutdown", interfaces.VMSetOptions{OnShutdown: strPtr("close")}, []string{"set", "guest", "--on-shutdown", "close"}},
		{"on-window-close", interfaces.VMSetOptions{OnWindowClose: strPtr("ask")}, []string{"set", "guest", "--on-window-close", "ask"}},
		{"faster-vm", interfaces.VMSetOptions{FasterVM: boolPtr(true)}, []string{"set", "guest", "--faster-vm", "on"}},
		{"auto-compress", interfaces.VMSetOptions{AutoCompress: boolPtr(false)}, []string{"set", "guest", "--auto-compress", "off"}},
		{"nested-virt", interfaces.VMSetOptions{NestedVirt: boolPtr(true)}, []string{"set", "guest", "--nested-virt", "on"}},
		{"smart-mount", interfaces.VMSetOptions{SmartMount: boolPtr(true)}, []string{"set", "guest", "--smart-mount", "on"}},
		{"shared-clipboard", interfaces.VMSetOptions{SharedClipboard: strPtr("host-to-guest")}, []string{"set", "guest", "--shared-clipboard", "host-to-guest"}},
		{"shf-host", interfaces.VMSetOptions{ShfHost: boolPtr(true)}, []string{"set", "guest", "--shf-host", "on"}},
		{"folder-add-min", interfaces.VMSetOptions{FolderAdd: &interfaces.VMSharedFolderAdd{Name: "code", Path: "/Users/me/code"}}, []string{"set", "guest", "--shf-host-add", "code", "--path", "/Users/me/code", "--enable"}},
		{"folder-add-full", interfaces.VMSetOptions{FolderAdd: &interfaces.VMSharedFolderAdd{Name: "code", Path: "/Users/me/code", Mode: "ro", Description: "src", Enable: boolPtr(false)}}, []string{"set", "guest", "--shf-host-add", "code", "--path", "/Users/me/code", "--mode", "ro", "--shf-description", "src", "--disable"}},
		{"folder-remove", interfaces.VMSetOptions{FolderRemove: strPtr("code")}, []string{"set", "guest", "--shf-host-del", "code"}},
		{"folder-set-min", interfaces.VMSetOptions{FolderSet: &interfaces.VMSharedFolderAdd{Name: "code", Path: "/Users/me/code"}}, []string{"set", "guest", "--shf-host-set", "code", "--path", "/Users/me/code", "--enable"}},
		{"folder-set-full", interfaces.VMSetOptions{FolderSet: &interfaces.VMSharedFolderAdd{Name: "code", Path: "/Users/me/code", Mode: "rw", Description: "src", Enable: boolPtr(false)}}, []string{"set", "guest", "--shf-host-set", "code", "--path", "/Users/me/code", "--mode", "rw", "--shf-description", "src", "--disable"}},
		{"tpm", interfaces.VMSetOptions{TPM: strPtr("crb")}, []string{"set", "guest", "--tpm", "crb"}},
		{"time-sync", interfaces.VMSetOptions{TimeSync: boolPtr(false)}, []string{"set", "guest", "--time-sync", "off"}},
		{"lock-on-suspend", interfaces.VMSetOptions{LockOnSuspend: boolPtr(true)}, []string{"set", "guest", "--lock-on-suspend", "on"}},
		{"smart-guard", interfaces.VMSetOptions{SmartGuard: boolPtr(true)}, []string{"set", "guest", "--smart-guard", "on"}},
		{"sg-notify", interfaces.VMSetOptions{SgNotifyBeforeCreate: boolPtr(true)}, []string{"set", "guest", "--sg-notify-before-create", "on"}},
		{"sg-interval", interfaces.VMSetOptions{SgIntervalSecs: intPtr(600)}, []string{"set", "guest", "--sg-interval", "600"}},
		{"sg-max", interfaces.VMSetOptions{SgMaxSnapshots: intPtr(10)}, []string{"set", "guest", "--sg-max-snapshots", "10"}},
		{"isolate-vm", interfaces.VMSetOptions{IsolateVM: boolPtr(true)}, []string{"set", "guest", "--isolate-vm", "on"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{defaultOut: ""}
			svc := setService(t, runner)
			res, err := svc.VMSet(context.Background(), "guest", tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Applied) != 1 {
				t.Fatalf("applied = %v", res.Applied)
			}
			exe, args := runner.lastCall()
			if exe != "/usr/local/bin/prlctl" {
				t.Fatalf("exe = %q", exe)
			}
			if len(args) != len(tc.want) {
				t.Fatalf("args = %v, want %v", args, tc.want)
			}
			for i := range tc.want {
				if args[i] != tc.want[i] {
					t.Fatalf("args = %v, want %v", args, tc.want)
				}
			}
		})
	}
}

func TestVMSetCombinedFieldsOneCall(t *testing.T) {
	runner := &fakeRunner{defaultOut: ""}
	svc := setService(t, runner)
	res, err := svc.VMSet(context.Background(), "guest", interfaces.VMSetOptions{
		Description:      strPtr("qa guest"),
		CPUs:             intPtr(8),
		MemSizeMB:        intPtr(16384),
		ThreeDAccelerate: strPtr("highest"),
		VerticalSync:     boolPtr(true),
		HighResolution:   boolPtr(false),
		OnShutdown:       strPtr("close"),
		NestedVirt:       boolPtr(true),
		SharedClipboard:  strPtr("off"),
		SgIntervalSecs:   intPtr(600),
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", runner.callCount())
	}
	_, args := runner.lastCall()
	want := []string{"set", "guest", "--description", "qa guest", "--cpus", "8", "--memsize", "16384",
		"--3d-accelerate", "highest", "--vertical-sync", "on", "--high-resolution", "off",
		"--on-shutdown", "close", "--nested-virt", "on", "--shared-clipboard", "off",
		"--sg-interval", "600"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %v, want %v", args, want)
		}
	}
	if len(res.Applied) != 10 {
		t.Fatalf("applied = %v, want 10 entries", res.Applied)
	}
}

func TestVMSetRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name   string
		in     interfaces.VMSetOptions
		substr string
	}{
		{"no-fields", interfaces.VMSetOptions{}, "at least one"},
		{"cpus-low", interfaces.VMSetOptions{CPUs: intPtr(0)}, "allowed 1-128"},
		{"cpus-high", interfaces.VMSetOptions{CPUs: intPtr(129)}, "allowed 1-128"},
		{"mem-low", interfaces.VMSetOptions{MemSizeMB: intPtr(15)}, "allowed 16-131072"},
		{"mem-high", interfaces.VMSetOptions{MemSizeMB: intPtr(131073)}, "allowed 16-131072"},
		{"video-low", interfaces.VMSetOptions{VideoSizeMB: intPtr(1)}, "allowed 2-8192"},
		{"video-high", interfaces.VMSetOptions{VideoSizeMB: intPtr(8193)}, "allowed 2-8192"},
		{"delay-high", interfaces.VMSetOptions{AutostartDelaySecs: intPtr(86401)}, "allowed 0-86400"},
		{"delay-negative", interfaces.VMSetOptions{AutostartDelaySecs: intPtr(-1)}, "allowed 0-86400"},
		{"bad-3d", interfaces.VMSetOptions{ThreeDAccelerate: strPtr("on")}, `invalid three_d_accelerate value "on"`},
		{"bad-autostart", interfaces.VMSetOptions{Autostart: strPtr("always")}, `invalid autostart value "always"`},
		{"bad-autostop", interfaces.VMSetOptions{Autostop: strPtr("quit")}, `invalid autostop value "quit"`},
		{"bad-view", interfaces.VMSetOptions{StartupView: strPtr("tile")}, `invalid startup_view value "tile"`},
		{"bad-shutdown", interfaces.VMSetOptions{OnShutdown: strPtr("reboot")}, `invalid on_shutdown value "reboot"`},
		{"bad-close", interfaces.VMSetOptions{OnWindowClose: strPtr("quit")}, `invalid on_window_close value "quit"`},
		{"bad-clipboard", interfaces.VMSetOptions{SharedClipboard: strPtr("both")}, `invalid shared_clipboard value "both"`},
		{"bad-tpm", interfaces.VMSetOptions{TPM: strPtr("v2")}, `invalid tpm value "v2"`},
		{"name-empty", interfaces.VMSetOptions{Name: strPtr(`  " "  `)}, "name is empty"},
		{"name-long", interfaces.VMSetOptions{Name: strPtr(strings.Repeat("x", 257))}, "longer than 256"},
		{"name-nonprintable", interfaces.VMSetOptions{Name: strPtr("bad\nname")}, "non-printable"},
		{"description-empty", interfaces.VMSetOptions{Description: strPtr("   ")}, "description is empty"},
		{"description-long", interfaces.VMSetOptions{Description: strPtr(strings.Repeat("d", 257))}, "longer than 256"},
		{"description-nonprintable", interfaces.VMSetOptions{Description: strPtr("bad\ndesc")}, "non-printable"},
		{"folder-empty-name", interfaces.VMSetOptions{FolderAdd: &interfaces.VMSharedFolderAdd{Name: "", Path: "/tmp"}}, "shared_folder_add.name is empty"},
		{"folder-empty-path", interfaces.VMSetOptions{FolderAdd: &interfaces.VMSharedFolderAdd{Name: "x", Path: "   "}}, "shared_folder_add.path is empty"},
		{"folder-long-path", interfaces.VMSetOptions{FolderAdd: &interfaces.VMSharedFolderAdd{Name: "x", Path: strings.Repeat("p", 1025)}}, "longer than 1024"},
		{"folder-bad-mode", interfaces.VMSetOptions{FolderAdd: &interfaces.VMSharedFolderAdd{Name: "x", Path: "/tmp", Mode: "write"}}, `invalid shared_folder_add.mode value "write"`},
		{"folder-long-desc", interfaces.VMSetOptions{FolderAdd: &interfaces.VMSharedFolderAdd{Name: "x", Path: "/tmp", Description: strings.Repeat("d", 257)}}, "longer than 256"},
		{"remove-empty", interfaces.VMSetOptions{FolderRemove: strPtr("")}, "shared_folder_remove.name is empty"},
		{"folder-set-empty-name", interfaces.VMSetOptions{FolderSet: &interfaces.VMSharedFolderAdd{Name: "", Path: "/tmp"}}, "shared_folder_set.name is empty"},
		{"folder-set-empty-path", interfaces.VMSetOptions{FolderSet: &interfaces.VMSharedFolderAdd{Name: "x", Path: "  "}}, "shared_folder_set.path is empty"},
		{"sg-interval-zero", interfaces.VMSetOptions{SgIntervalSecs: intPtr(0)}, "allowed 1-2592000"},
		{"sg-interval-high", interfaces.VMSetOptions{SgIntervalSecs: intPtr(2592001)}, "allowed 1-2592000"},
		{"sg-max-zero", interfaces.VMSetOptions{SgMaxSnapshots: intPtr(0)}, "allowed 1-32"},
		{"sg-max-high", interfaces.VMSetOptions{SgMaxSnapshots: intPtr(33)}, "allowed 1-32"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{defaultOut: ""}
			svc := setService(t, runner)
			_, err := svc.VMSet(context.Background(), "guest", tc.in)
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

func TestVMSetValidatesRef(t *testing.T) {
	runner := &fakeRunner{defaultOut: ""}
	svc := setService(t, runner)
	if _, err := svc.VMSet(context.Background(), "", interfaces.VMSetOptions{CPUs: intPtr(4)}); err == nil {
		t.Fatal("want error for empty ref")
	}
	if runner.callCount() != 0 {
		t.Fatalf("prlctl ran %d times, want 0", runner.callCount())
	}
}

func TestVMSetFailureSurfaces(t *testing.T) {
	runner := &fakeRunner{defErr: errors.New("The virtual machine could not be found")}
	svc := setService(t, runner)
	_, err := svc.VMSet(context.Background(), "guest", interfaces.VMSetOptions{CPUs: intPtr(4)})
	if err == nil || !strings.Contains(err.Error(), "could not be found") {
		t.Fatalf("err = %v, want the prlctl failure", err)
	}
}

func TestVMSetInvalidatesCache(t *testing.T) {
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

	if _, err := svc.VMSet(context.Background(), vmOneID, interfaces.VMSetOptions{CPUs: intPtr(4)}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n := runner.callCount(); n != 3 {
		t.Fatalf("calls = %d, want 3 (VMSet invalidated the cache)", n)
	}
}

func TestVMSetTrimsOutput(t *testing.T) {
	runner := &fakeRunner{defaultOut: "  configured\n"}
	svc := setService(t, runner)
	res, err := svc.VMSet(context.Background(), "guest", interfaces.VMSetOptions{TimeSync: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "configured" {
		t.Fatalf("output = %q, want trimmed", res.Output)
	}
}

func TestVMSetDisabled(t *testing.T) {
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
	if _, err := svc.VMSet(context.Background(), "guest", interfaces.VMSetOptions{CPUs: intPtr(4)}); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
}
