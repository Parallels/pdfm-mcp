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

func TestDeleteHandlerModes(t *testing.T) {
	cases := []struct {
		name    string
		vmID    string // configured target
		arg     string // explicit vm_id argument
		confirm string
		want    string // expected argv target; "" = call must fail
	}{
		{"vm-mode uses configured target", vmOneID, "", vmOneID, vmOneID},
		{"vm-mode rejects explicit vm_id", vmOneID, vmTwoID, vmTwoID, ""},
		{"fleet requires vm_id", "", "", "x", ""},
		{"fleet uses given vm_id", "", "guest-x", "guest-x", "guest-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, tc.vmID)
			_, out, err := tools.VMDelete(context.Background(), nil, tools.DeleteInput{VMID: tc.arg, Confirm: tc.confirm})
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
			_, args := runner.lastCall()
			if !slices.Equal(args, []string{"delete", tc.want}) {
				t.Fatalf("args = %v", args)
			}
		})
	}
}

func TestDeleteConfirmGate(t *testing.T) {
	cases := []struct {
		name    string
		confirm string
		wPart   string // "" = success
	}{
		{"mismatch rejected", "other-vm", "confirm must repeat"},
		{"mismatch names both", "a", "guest-x"},
		{"empty confirm rejected", "", "confirm must repeat"},
		{"whitespace-only confirm rejected", "   ", "confirm must repeat"},
		{"trimmed confirm accepted", "  guest-x  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			_, _, err := tools.VMDelete(context.Background(), nil, tools.DeleteInput{VMID: "guest-x", Confirm: tc.confirm})
			if tc.wPart == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if runner.count() != 1 {
					t.Fatalf("runner calls = %d, want 1", runner.count())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wPart) {
				t.Fatalf("err = %v, want containing %q", err, tc.wPart)
			}
			if runner.count() != 0 {
				t.Fatalf("prlctl must not run on confirm mismatch: %d calls", runner.count())
			}
		})
	}
}

func TestDeleteProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	names := toolNames(t, srv)
	if !names["parallels_vm_delete"] {
		t.Fatalf("parallels_vm_delete not listed: %v", names)
	}
	req := wireRequiredList(t, srv, "parallels_vm_delete")
	if !slices.Contains(req, "confirm") || !slices.Contains(req, "vm_id") {
		t.Fatalf("fleet required = %v, want confirm and vm_id", req)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "delete-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_delete",
		Arguments: map[string]any{"vm_id": "guest-x", "confirm": "guest-x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	if sc == nil || sc["vm_id"] != "guest-x" {
		t.Fatalf("structured = %v", sc)
	}
	_, args := runner.lastCall()
	if !slices.Equal(args, []string{"delete", "guest-x"}) {
		t.Fatalf("args = %v", args)
	}

	// Missing confirm is a schema error (required field).
	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_delete",
		Arguments: map[string]any{"vm_id": "guest-x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError || !strings.Contains(flatText(bad), "confirm") {
		t.Fatalf("missing confirm must be a schema error: %+v", bad.Content)
	}

	// A confirm mismatch is a tool error and prlctl must not run again
	// (relative count: the startup cache warm-up already used the runner).
	before := runner.count()
	mism, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_delete",
		Arguments: map[string]any{"vm_id": "guest-x", "confirm": "nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mism.IsError || !strings.Contains(flatText(mism), "confirm must repeat") {
		t.Fatalf("mismatch must be a tool error: %+v", mism.Content)
	}
	if runner.count() != before {
		t.Fatalf("runner calls went %d -> %d (mismatch must not exec)", before, runner.count())
	}
}

func TestDeleteVMModeProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testConfig()
	cfg.VMID = vmOneID
	srv := server.New(logger, cfg)

	req := wireRequiredList(t, srv, "parallels_vm_delete")
	if !slices.Equal(req, []string{"confirm"}) {
		t.Fatalf("VM mode required = %v, want [confirm]", req)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "delete-vmmode-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_delete",
		Arguments: map[string]any{"confirm": vmOneID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	_, args := runner.lastCall()
	if !slices.Equal(args, []string{"delete", vmOneID}) {
		t.Fatalf("args = %v", args)
	}

	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_delete",
		Arguments: map[string]any{"vm_id": vmTwoID, "confirm": vmTwoID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatal("explicit vm_id must be rejected in VM mode")
	}
}
