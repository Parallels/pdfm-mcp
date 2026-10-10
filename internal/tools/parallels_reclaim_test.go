package tools_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/tools"
)

// reclaimTestRunner emulates prlctl reclaim-disk: the Run path (estimate)
// answers with the --estimate --json payload; the RunCapture path (real
// runs, including RunStdin in the fake) answers with the progress line.
func reclaimTestRunner() *testRunner {
	return &testRunner{
		fn: func(_ string, _ []string) (string, error) {
			return `{"diskSpace":{"hardDisks":1204224,"snapshots":1024,"miscellaneous":1081344,"reclaimable":4096}}`, nil
		},
		captureFn: func(_ string, _ []string) (string, string, int) {
			return "Reclaiming disk, please wait...", "", 0
		},
	}
}

func TestReclaimDiskFleetProtocol(t *testing.T) {
	runner := reclaimTestRunner()
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())
	wireTools := listTools(t, srv)

	var tl *mcp.Tool
	for _, t2 := range wireTools {
		if t2.Name == "parallels_vm_reclaim_disk" {
			tl = t2
		}
	}
	if tl == nil {
		t.Fatal("parallels_vm_reclaim_disk not listed in fleet mode")
	}
	req := wireRequired(t, tl.InputSchema)
	if !req["vm_id"] || len(req) != 1 {
		t.Fatalf("fleet reclaim required = %v, want exactly [vm_id]", req)
	}

	// Estimate: parsed byte fields, exact argv, no stdin.
	_, out, err := tools.VMReclaimDisk(context.Background(), nil, tools.VMReclaimDiskInput{VMID: vmOneID, Estimate: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Estimate || out.DiskSpace == nil {
		t.Fatalf("out = %+v", out)
	}
	if out.DiskSpace.HardDisks != 1204224 || out.DiskSpace.Snapshots != 1024 || out.DiskSpace.Miscellaneous != 1081344 || out.DiskSpace.Reclaimable != 4096 {
		t.Fatalf("disk_space = %+v", out.DiskSpace)
	}
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" || len(args) != 4 || args[0] != "reclaim-disk" || args[1] != vmOneID || args[2] != "--estimate" || args[3] != "--json" {
		t.Fatalf("argv = %q %v", exe, args)
	}
	if runner.lastStdin() != "" {
		t.Fatalf("estimate without password fed stdin: %q", runner.lastStdin())
	}

	// Real run with allow_shutdown.
	_, out, err = tools.VMReclaimDisk(context.Background(), nil, tools.VMReclaimDiskInput{VMID: vmOneID, AllowShutdown: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Estimate || out.DiskSpace != nil || !strings.Contains(out.Output, "Reclaiming disk") {
		t.Fatalf("out = %+v", out)
	}
	exe, args = runner.lastCall()
	if exe != "/usr/local/bin/prlctl" || len(args) != 3 || args[2] != "--allow-shutdown" {
		t.Fatalf("argv = %q %v", exe, args)
	}

	// Password reaches prlctl over stdin, never argv.
	_, _, err = tools.VMReclaimDisk(context.Background(), nil, tools.VMReclaimDiskInput{VMID: vmOneID, Estimate: true, Password: "enc-pw-42"})
	if err != nil {
		t.Fatal(err)
	}
	if runner.lastStdin() != "enc-pw-42\n" {
		t.Fatalf("stdin = %q", runner.lastStdin())
	}
	_, args = runner.lastCall()
	for _, a := range args {
		if a == "enc-pw-42" {
			t.Fatalf("password leaked into argv: %v", args)
		}
	}
}

func TestReclaimDiskVMModeProtocol(t *testing.T) {
	runner := reclaimTestRunner()
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	if _, _, err := tools.VMReclaimDisk(context.Background(), nil, tools.VMReclaimDiskInput{VMID: vmTwoID, Estimate: true}); err == nil || !strings.Contains(err.Error(), "vm_id must be omitted") {
		t.Fatalf("want vm_id omitted error, got: %v", err)
	}
	_, out, err := tools.VMReclaimDisk(context.Background(), nil, tools.VMReclaimDiskInput{Estimate: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Estimate || out.DiskSpace == nil {
		t.Fatalf("out = %+v", out)
	}
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" || args[1] != vmOneID {
		t.Fatalf("argv = %q %v", exe, args)
	}
}
