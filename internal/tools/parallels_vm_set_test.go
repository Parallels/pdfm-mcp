package tools_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/server"
	"github.com/Parallels/pdfm-mcp/internal/tools"
)

func strPtr(s string) *string { return &s }

func boolPtr(b bool) *bool { return &b }

func TestVMSetHandlerModes(t *testing.T) {
	cases := []struct {
		name string
		vmID string // configured target
		arg  string // explicit vm_id argument
		want string // expected argv target; "" = call must fail
	}{
		{"vm-mode uses configured target", vmOneID, "", vmOneID},
		{"vm-mode rejects explicit vm_id", vmOneID, vmTwoID, ""},
		{"fleet requires vm_id", "", "", ""},
		{"fleet uses given vm_id", "", "guest-x", "guest-x"},
		{"fleet unquotes quoted name", "", "  \"ubuntu_github_builder\"  ", "ubuntu_github_builder"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, tc.vmID)
			_, out, err := tools.VMSet(context.Background(), nil, tools.VMSetInput{VMID: tc.arg, CPUs: intPtr(8)})
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
			if len(out.Applied) != 1 || out.Applied[0] != "cpus=8" {
				t.Fatalf("applied = %v, want [cpus=8]", out.Applied)
			}
			_, args := runner.lastCall()
			want := []string{"set", tc.want, "--cpus", "8"}
			if !slices.Equal(args, want) {
				t.Fatalf("args = %v, want %v", args, want)
			}
		})
	}
}

func TestVMSetHandlerNoFields(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, _, err := tools.VMSet(context.Background(), nil, tools.VMSetInput{VMID: "guest-x"})
	if err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("err = %v, want the at-least-one error", err)
	}
	if runner.count() != 0 {
		t.Fatalf("prlctl must not run: %d calls", runner.count())
	}
}

func TestVMSetHandlerVideoFields(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, out, err := tools.VMSet(context.Background(), nil, tools.VMSetInput{
		VMID: "guest-x", VerticalSync: boolPtr(true), HighResolution: boolPtr(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Applied) != 2 || out.Applied[0] != "vertical_sync=on" || out.Applied[1] != "high_resolution=off" {
		t.Fatalf("applied = %v, want [vertical_sync=on high_resolution=off]", out.Applied)
	}
	_, args := runner.lastCall()
	want := []string{"set", "guest-x", "--vertical-sync", "on", "--high-resolution", "off"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestVMSetHandlerNewFields(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, out, err := tools.VMSet(context.Background(), nil, tools.VMSetInput{
		VMID: "guest-x", Description: strPtr("qa"), OnShutdown: strPtr("close"),
		LockOnSuspend: boolPtr(true), SgNotifyBeforeCreate: boolPtr(true),
		SgIntervalS: intPtr(600), SgMaxSnapshots: intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantApplied := []string{"description=qa", "on_shutdown=close", "lock_on_suspend=on",
		"sg_notify_before_create=on", "sg_interval_s=600", "sg_max_snapshots=10"}
	if !slices.Equal(out.Applied, wantApplied) {
		t.Fatalf("applied = %v, want %v", out.Applied, wantApplied)
	}
	_, args := runner.lastCall()
	want := []string{"set", "guest-x", "--description", "qa", "--on-shutdown", "close",
		"--lock-on-suspend", "on", "--sg-notify-before-create", "on", "--sg-interval", "600",
		"--sg-max-snapshots", "10"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestVMSetHandlerServiceValidation(t *testing.T) {
	cases := []struct {
		name    string
		in      tools.VMSetInput
		wantErr string
	}{
		{"cpus out of range", tools.VMSetInput{VMID: "g", CPUs: intPtr(0)}, "allowed 1-128"},
		{"mem out of range", tools.VMSetInput{VMID: "g", MemSizeMB: intPtr(131073)}, "allowed 16-131072"},
		{"bad enum", tools.VMSetInput{VMID: "g", Autostart: strPtr("always")}, "invalid autostart value"},
		{"bad on_shutdown", tools.VMSetInput{VMID: "g", OnShutdown: strPtr("reboot")}, "invalid on_shutdown value"},
		{"blank name", tools.VMSetInput{VMID: "g", Name: strPtr(`"  "`)}, "name is empty"},
		{"folder missing path", tools.VMSetInput{VMID: "g", FolderAdd: &tools.VMSetFolderAddInput{Name: "x"}}, "shared_folder_add.path is empty"},
		{"folder set missing name", tools.VMSetInput{VMID: "g", FolderSet: &tools.VMSetFolderAddInput{Path: "/tmp"}}, "shared_folder_set.name is empty"},
		{"sg interval zero", tools.VMSetInput{VMID: "g", SgIntervalS: intPtr(0)}, "allowed 1-2592000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			_, _, err := tools.VMSet(context.Background(), nil, tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if runner.count() != 0 {
				t.Fatalf("prlctl must not run: %d calls", runner.count())
			}
		})
	}
}

func TestParallelsVMSetProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	wireTools := listTools(t, srv)
	var input map[string]any
	for _, tl := range wireTools {
		if tl.Name != "parallels_vm_set" {
			continue
		}
		schema, ok := tl.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("parallels_vm_set input schema = %T, want map", tl.InputSchema)
		}
		input = schema
	}
	if input == nil {
		t.Fatal("parallels_vm_set not listed")
	}

	// Fleet mode: vm_id is the only required argument — the 33 config
	// fields stay optional.
	required, _ := input["required"].([]any)
	req := map[string]bool{}
	for _, r := range required {
		req[r.(string)] = true
	}
	if !req["vm_id"] || len(req) != 1 {
		t.Fatalf("required = %v, want exactly [vm_id]", required)
	}

	// The enum fields carry their closed value sets on the wire, on the
	// non-null anyOf branch (nullable pointer fields are split).
	props, _ := input["properties"].(map[string]any)
	autostart, _ := props["autostart"].(map[string]any)
	anyOf, _ := autostart["anyOf"].([]any)
	var enum []any
	for _, b := range anyOf {
		branch, _ := b.(map[string]any)
		if e, ok := branch["enum"].([]any); ok {
			enum = e
		}
	}
	if len(enum) != 5 {
		t.Fatalf("autostart enum = %v, want 5 values", enum)
	}

	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "vmset-test", Version: "v0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close client: %v", err)
		}
	}()

	// Warm-up already ran one prlctl list during server.New.
	if runner.count() != 1 {
		t.Fatalf("runner calls = %d, want 1 (warm-up)", runner.count())
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_set",
		Arguments: map[string]any{"vm_id": vmOneID, "cpus": float64(8), "nested_virt": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["vm_id"] != vmOneID {
		t.Fatalf("vm_id = %v", got["vm_id"])
	}
	applied, _ := got["applied"].([]any)
	if len(applied) != 2 || applied[0] != "cpus=8" || applied[1] != "nested_virt=on" {
		t.Fatalf("applied = %v, want [cpus=8 nested_virt=on]", applied)
	}
	_, args := runner.lastCall()
	want := []string{"set", vmOneID, "--cpus", "8", "--nested-virt", "on"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}
