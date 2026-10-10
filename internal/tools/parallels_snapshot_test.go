package tools_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/server"
	"github.com/Parallels/pdfm-mcp/internal/tools"
)

const snapTreeJSONWire = `{
	"{snap-1}": {"name":"alpha","date":"2026-10-09 15:51:15","state":"poweroff","current":false,"parent":""},
	"{snap-2}": {"name":"beta","date":"2026-10-09 16:00:00","state":"poweron","current":true,"parent":"{snap-1}"}
}`

// snapTestRunner emulates prlctl's snapshot commands; set captureFn per
// case when a different output is needed (it defaults to the tree JSON
// for snapshot-list, the create message for snapshot, and the plain
// success lines elsewhere).
func snapCaptureFn(_ string, args []string) (string, string, int) {
	switch args[0] {
	case "snapshot":
		return "Creating the snapshot...\nThe snapshot with id {abcd-1234} has been successfully created.", "", 0
	case "snapshot-list":
		return snapTreeJSONWire, "", 0
	case "snapshot-delete":
		return "The snapshot has been successfully deleted.", "", 0
	case "snapshot-switch":
		return "The VM has been successfully switched.", "", 0
	}
	return "ok", "", 0
}

func snapTestRunner() *testRunner {
	return &testRunner{captureFn: snapCaptureFn}
}

func TestSnapshotToolsFleetProtocol(t *testing.T) {
	runner := snapTestRunner()
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())
	wireTools := listTools(t, srv)

	want := map[string][]string{
		"parallels_vm_snapshot_create": {"vm_id"},
		"parallels_vm_snapshot_list":   {"vm_id"},
		"parallels_vm_snapshot_delete": {"vm_id", "snapshot_id"},
		"parallels_vm_snapshot_switch": {"vm_id", "snapshot_id"},
	}
	for name, wantReq := range want {
		var tl *mcp.Tool
		for _, t2 := range wireTools {
			if t2.Name == name {
				tl = t2
			}
		}
		if tl == nil {
			t.Fatalf("%s not listed in fleet mode", name)
		}
		req := wireRequired(t, tl.InputSchema)
		if len(req) != len(wantReq) {
			t.Fatalf("%s required = %v, want %v", name, req, wantReq)
		}
		for _, r := range wantReq {
			if !req[r] {
				t.Fatalf("%s required = %v, want %v", name, req, wantReq)
			}
		}
	}

	// Annotations: delete/switch destructive, list read-only.
	byName := map[string]*mcp.Tool{}
	for _, t2 := range wireTools {
		byName[t2.Name] = t2
	}
	for _, name := range []string{"parallels_vm_snapshot_delete", "parallels_vm_snapshot_switch"} {
		a := byName[name].Annotations
		if a == nil || a.DestructiveHint == nil || !*a.DestructiveHint || a.ReadOnlyHint {
			t.Fatalf("%s must be destructive and not read-only: %+v", name, a)
		}
	}
	l := byName["parallels_vm_snapshot_list"].Annotations
	if l == nil || !l.ReadOnlyHint {
		t.Fatalf("snapshot_list must be read-only: %+v", l)
	}

	// Create: parsed id + output.
	_, outC, err := tools.VMCreateSnapshot(context.Background(), nil, tools.VMCreateSnapshotInput{VMID: vmOneID, Name: "my snap"})
	if err != nil {
		t.Fatal(err)
	}
	if outC.SnapshotID != "{abcd-1234}" || !strings.Contains(outC.Output, "successfully created") {
		t.Fatalf("out = %+v", outC)
	}
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" || len(args) != 4 || args[0] != "snapshot" || args[2] != "-n" || args[3] != "my snap" {
		t.Fatalf("argv = %q %v", exe, args)
	}

	// List (default): parsed tree.
	_, outL, err := tools.VMListSnapshots(context.Background(), nil, tools.VMListSnapshotsInput{VMID: vmOneID})
	if err != nil {
		t.Fatal(err)
	}
	if len(outL.Snapshots) != 2 || outL.Snapshots[0].ID != "{snap-1}" || !outL.Snapshots[1].Current || outL.Snapshots[1].Parent != "{snap-1}" {
		t.Fatalf("out = %+v", outL)
	}
	if _, args := runner.lastCall(); len(args) != 3 || args[2] != "--json" {
		t.Fatalf("argv = %v", args)
	}

	// Single mode: parsed fields incl. description with a colon.
	runner.captureFn = func(_ string, _ []string) (string, string, int) {
		return "ID: {snap-1}\nName: alpha\nDate: 2026-10-09 15:51:15\nState: poweroff\nDescription: hello: world", "", 0
	}
	_, outLS, err := tools.VMListSnapshots(context.Background(), nil, tools.VMListSnapshotsInput{VMID: vmOneID, SnapshotID: "{snap-1}"})
	if err != nil {
		t.Fatal(err)
	}
	if outLS.Snapshot == nil || outLS.Snapshot.Description != "hello: world" || outLS.Snapshot.ID != "{snap-1}" {
		t.Fatalf("out = %+v", outLS)
	}

	// Unknown id: clean not_found, not an error.
	runner.captureFn = func(_ string, _ []string) (string, string, int) { return "", "", 0 }
	_, outNF, err := tools.VMListSnapshots(context.Background(), nil, tools.VMListSnapshotsInput{VMID: vmOneID, SnapshotID: "{nope}"})
	if err != nil {
		t.Fatal(err)
	}
	if !outNF.NotFound || outNF.Snapshot != nil {
		t.Fatalf("out = %+v", outNF)
	}

	// Delete with children; switch with skip_resume (restore the default
	// answers on the SAME runner the service holds).
	runner.captureFn = snapCaptureFn
	_, outD, err := tools.VMDeleteSnapshot(context.Background(), nil, tools.VMDeleteSnapshotInput{VMID: vmOneID, SnapshotID: "{snap-1}", Children: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outD.Output, "successfully deleted") {
		t.Fatalf("out = %+v", outD)
	}
	if _, args := runner.lastCall(); len(args) != 5 || args[4] != "-c" {
		t.Fatalf("argv = %v", args)
	}

	_, outS, err := tools.VMSwitchSnapshot(context.Background(), nil, tools.VMSwitchSnapshotInput{VMID: vmOneID, SnapshotID: "{snap-1}", SkipResume: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outS.Output, "successfully switched") {
		t.Fatalf("out = %+v", outS)
	}
	if _, args := runner.lastCall(); len(args) != 5 || args[4] != "--skip-resume" {
		t.Fatalf("argv = %v", args)
	}
}

func TestSnapshotToolsVMModeAndPassword(t *testing.T) {
	runner := snapTestRunner()
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	// Explicit vm_id rejected in VM mode.
	if _, _, err := tools.VMListSnapshots(context.Background(), nil, tools.VMListSnapshotsInput{VMID: vmTwoID}); err == nil || !strings.Contains(err.Error(), "vm_id must be omitted") {
		t.Fatalf("want vm_id omitted error, got: %v", err)
	}

	// Password reaches prlctl over stdin, never argv.
	_, _, err := tools.VMCreateSnapshot(context.Background(), nil, tools.VMCreateSnapshotInput{Password: "enc-pw-7"})
	if err != nil {
		t.Fatal(err)
	}
	if runner.lastStdin() != "enc-pw-7\n" {
		t.Fatalf("stdin = %q", runner.lastStdin())
	}
	_, args := runner.lastCall()
	if len(args) != 2 {
		t.Fatalf("argv = %v", args)
	}
	for _, a := range args {
		if a == "enc-pw-7" {
			t.Fatalf("password leaked into argv: %v", args)
		}
	}
}
