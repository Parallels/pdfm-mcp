package tools_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/tools"
)

// powerListFn answers list commands with the sample JSON and power commands
// with an empty output, like the real prlctl.
func powerListFn() func(exe string, args []string) (string, error) {
	return func(_ string, args []string) (string, error) {
		if args[0] == "list" {
			return toolSampleVMsJSON, nil
		}
		return "", nil
	}
}

func vmIDCall(fn func(context.Context, *mcp.CallToolRequest, tools.PowerVMInput) (*mcp.CallToolResult, tools.PowerOutput, error)) func(string) (tools.PowerOutput, error) {
	return func(vmID string) (tools.PowerOutput, error) {
		_, out, err := fn(context.Background(), nil, tools.PowerVMInput{VMID: vmID})
		return out, err
	}
}

var powerInvokers = []struct {
	name string
	op   string
	call func(vmID string) (tools.PowerOutput, error)
}{
	{"start", "start", vmIDCall(tools.VMStart)},
	{"resume", "resume", vmIDCall(tools.VMResume)},
	{"pause", "pause", vmIDCall(tools.VMPause)},
	{"suspend", "suspend", vmIDCall(tools.VMSuspend)},
	{"restart", "restart", vmIDCall(tools.VMRestart)},
	{"reset", "reset", vmIDCall(tools.VMReset)},
	{"reset_uptime", "reset-uptime", vmIDCall(tools.VMResetUptime)},
}

func TestPowerTargetResolution(t *testing.T) {
	for _, tc := range powerInvokers {
		t.Run(tc.name+"/vm-mode/uses-configured-target", func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)
			out, err := tc.call("")
			if err != nil {
				t.Fatal(err)
			}
			if out.VMID != vmOneID {
				t.Fatalf("out.VMID = %q, want %q", out.VMID, vmOneID)
			}
			exe, args := runner.lastCall()
			if exe != "/usr/local/bin/prlctl" {
				t.Fatalf("exe = %q", exe)
			}
			if want := []string{tc.op, vmOneID}; !slices.Equal(args, want) {
				t.Fatalf("args = %v, want %v", args, want)
			}
		})

		t.Run(tc.name+"/vm-mode/explicit-vm-id-rejected", func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)
			if _, err := tc.call(vmTwoID); err == nil || !strings.Contains(err.Error(), "omitted") {
				t.Fatalf("err = %v, want rejection of explicit vm_id", err)
			}
			if runner.count() != 0 {
				t.Fatalf("prlctl must not run: %d calls", runner.count())
			}
		})

		t.Run(tc.name+"/fleet/without-vm-id-rejected", func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			if _, err := tc.call(""); err == nil || !strings.Contains(err.Error(), "required") {
				t.Fatalf("err = %v, want required-vm_id error", err)
			}
			if runner.count() != 0 {
				t.Fatalf("prlctl must not run: %d calls", runner.count())
			}
		})

		t.Run(tc.name+"/fleet/with-vm-id-targets-it", func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			out, err := tc.call("guest-x")
			if err != nil {
				t.Fatal(err)
			}
			if out.VMID != "guest-x" {
				t.Fatalf("out.VMID = %q", out.VMID)
			}
			_, args := runner.lastCall()
			if want := []string{tc.op, "guest-x"}; !slices.Equal(args, want) {
				t.Fatalf("args = %v, want %v", args, want)
			}
		})
	}
}

func TestPowerStopFlags(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	if _, _, err := tools.VMStop(context.Background(), nil, tools.PowerStopInput{VMID: "guest1", Kill: true, DropState: true}); err != nil {
		t.Fatal(err)
	}
	_, args := runner.lastCall()
	if want := []string{"stop", "guest1", "--kill", "--drop-state"}; !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}

	if _, _, err := tools.VMStop(context.Background(), nil, tools.PowerStopInput{VMID: "guest2"}); err != nil {
		t.Fatal(err)
	}
	_, args = runner.lastCall()
	if want := []string{"stop", "guest2"}; !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v (no flags)", args, want)
	}
}

// wireInputRequired reports whether the given tool's wire input schema has
// vm_id in its "required" list.
func wireInputRequired(t *testing.T, srv *mcp.Server, toolName string) (bool, bool) {
	t.Helper()
	for _, tl := range listTools(t, srv) {
		if tl.Name != toolName {
			continue
		}
		schema, ok := tl.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: input schema = %T, want map", toolName, tl.InputSchema)
		}
		required, _ := schema["required"].([]any)
		for _, r := range required {
			if r == "vm_id" {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}

func TestParallelsPowerFleetProtocol(t *testing.T) {
	runner := &testRunner{fn: powerListFn()}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	names := toolNames(t, srv)
	for _, want := range []string{
		"parallels_vm_start", "parallels_vm_resume", "parallels_vm_pause",
		"parallels_vm_suspend", "parallels_vm_restart", "parallels_vm_reset",
		"parallels_vm_reset_uptime", "parallels_vm_stop",
	} {
		if !names[want] {
			t.Fatalf("fleet mode: %s not listed: %v", want, names)
		}
	}

	required, ok := wireInputRequired(t, srv, "parallels_vm_start")
	if !ok || !required {
		t.Fatalf("fleet mode: vm_id must be required in parallels_vm_start input schema (present=%v required=%v)", ok, required)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "power-fleet-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	// Warm-up filled the cache during server.New.
	if runner.count() != 1 {
		t.Fatalf("runner calls = %d, want 1 (warm-up)", runner.count())
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_start", Arguments: map[string]any{"vm_id": "guest-x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["vm_id"] != "guest-x" {
		t.Fatalf("vm_id = %v", got["vm_id"])
	}
	if exe, args := runner.lastCall(); exe != "/usr/local/bin/prlctl" || !slices.Equal(args, []string{"start", "guest-x"}) {
		t.Fatalf("prlctl call = %s %v", exe, args)
	}

	// The power op must have invalidated the cache: the next list re-fetches.
	listRes, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_list_vms", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if listRes.IsError {
		t.Fatalf("list error: %+v", listRes.Content)
	}
	listGot, _ := listRes.StructuredContent.(map[string]any)
	if listGot["source"] != "refreshed" {
		t.Fatalf("source = %v, want refreshed (power op must invalidate the cache)", listGot["source"])
	}
}

func TestParallelsPowerVMModeProtocol(t *testing.T) {
	runner := &testRunner{fn: powerListFn()}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testConfig()
	cfg.VMID = vmOneID
	srv := server.New(logger, cfg)

	names := toolNames(t, srv)
	for _, want := range []string{
		"parallels_vm_start", "parallels_vm_resume", "parallels_vm_pause",
		"parallels_vm_suspend", "parallels_vm_restart", "parallels_vm_reset",
		"parallels_vm_reset_uptime", "parallels_vm_stop",
	} {
		if !names[want] {
			t.Fatalf("VM mode: %s not listed: %v", want, names)
		}
	}

	required, ok := wireInputRequired(t, srv, "parallels_vm_start")
	if !ok || required {
		t.Fatalf("VM mode: vm_id must not be required in parallels_vm_start input schema (present=%v required=%v)", ok, required)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "power-vmmode-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_stop", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	if exe, args := runner.lastCall(); exe != "/usr/local/bin/prlctl" || !slices.Equal(args, []string{"stop", vmOneID}) {
		t.Fatalf("prlctl call = %s %v, want [stop %s]", exe, args, vmOneID)
	}

	// An explicit vm_id must be rejected in VM mode.
	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_stop", Arguments: map[string]any{"vm_id": vmTwoID}})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatalf("explicit vm_id must be rejected in VM mode, got %+v", bad.StructuredContent)
	}
}

// TestPowerFleetVMIDSchemaRejectedOnTheWire checks that a fleet-mode call
// without vm_id is already refused by input-schema validation (required).
func TestPowerFleetVMIDSchemaRejectedOnTheWire(t *testing.T) {
	initParallelsForTest(t, &testRunner{}, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	srv := server.New(slog.New(slog.NewTextHandler(io.Discard, nil)), testConfig())

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
	client := mcp.NewClient(&mcp.Implementation{Name: "power-schema-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	// The SDK validates input against the schema server-side and answers
	// with an error result (the handler never runs).
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_start", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("want validation error for missing vm_id in fleet mode, got %+v", res.StructuredContent)
	}
	found := false
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "vm_id") {
			found = true
		}
	}
	if !found {
		t.Fatalf("validation message does not mention vm_id: %+v", res.Content)
	}
}
