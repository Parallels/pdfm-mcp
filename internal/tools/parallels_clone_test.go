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

func TestCloneHandlerModes(t *testing.T) {
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, tc.vmID)
			_, out, err := tools.VMClone(context.Background(), nil, tools.CloneInput{VMID: tc.arg, Name: "guest-copy"})
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
			if out.VMID != tc.want || out.Name != "guest-copy" {
				t.Fatalf("out = %+v", out)
			}
			_, args := runner.lastCall()
			want := []string{"clone", tc.want, "--name", "guest-copy"}
			if !slices.Equal(args, want) {
				t.Fatalf("args = %v, want %v", args, want)
			}
		})
	}
}

func TestCloneHandlerServiceValidation(t *testing.T) {
	cases := []struct {
		name    string
		in      tools.CloneInput
		wantErr string
	}{
		{"snapshot without linked", tools.CloneInput{VMID: "guest-x", Name: "c", SnapshotID: "snap-1"}, "snapshot_id requires linked"},
		{"bad detach value", tools.CloneInput{VMID: "guest-x", Name: "c", DetachExternalHDD: "maybe"}, "detach_external_hdd"},
		{"whitespace name", tools.CloneInput{VMID: "guest-x", Name: "   "}, "invalid clone name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			_, _, err := tools.VMClone(context.Background(), nil, tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if runner.count() != 0 {
				t.Fatalf("prlctl must not run on validation error: %d calls", runner.count())
			}
		})
	}
}

func TestCloneProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	names := toolNames(t, srv)
	if !names["parallels_vm_clone"] {
		t.Fatalf("parallels_vm_clone not listed: %v", names)
	}
	req := wireRequiredList(t, srv, "parallels_vm_clone")
	if !slices.Contains(req, "name") || !slices.Contains(req, "vm_id") {
		t.Fatalf("fleet required = %v, want name and vm_id", req)
	}
	if len(req) != 2 {
		t.Fatalf("fleet required = %v, want exactly name and vm_id", req)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "clone-test", Version: "v0"}, nil)
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
		Name: "parallels_vm_clone",
		Arguments: map[string]any{
			"vm_id":               "guest-x",
			"name":                " guest-copy ",
			"template":            true,
			"dst":                 "/tmp/dest",
			"regenerate_src_uuid": true,
			"linked":              true,
			"snapshot_id":         "snap-1",
			"detach_external_hdd": "no",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	if sc == nil || sc["vm_id"] != "guest-x" || sc["name"] != "guest-copy" {
		t.Fatalf("structured = %v", sc)
	}
	_, args := runner.lastCall()
	want := []string{
		"clone", "guest-x", "--name", "guest-copy",
		"--template", "--dst", "/tmp/dest", "--regenerate-src-uuid",
		"--linked", "--id", "snap-1", "--detach-external-hdd", "no",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}

	// Missing name is a schema error.
	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_clone",
		Arguments: map[string]any{"vm_id": "guest-x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError || !strings.Contains(flatText(bad), "name") {
		t.Fatalf("missing name must be a schema error: %+v", bad.Content)
	}

	// snapshot_id without linked is a tool error (no exec).
	before := runner.count()
	mism, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_clone",
		Arguments: map[string]any{"vm_id": "guest-x", "name": "c", "snapshot_id": "snap-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mism.IsError || !strings.Contains(flatText(mism), "snapshot_id requires linked") {
		t.Fatalf("snapshot-without-linked must be a tool error: %+v", mism.Content)
	}
	if runner.count() != before {
		t.Fatalf("runner calls went %d -> %d (validation must not exec)", before, runner.count())
	}
}

func TestCloneVMModeProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testConfig()
	cfg.VMID = vmOneID
	srv := server.New(logger, cfg)

	req := wireRequiredList(t, srv, "parallels_vm_clone")
	if !slices.Equal(req, []string{"name"}) {
		t.Fatalf("VM mode required = %v, want [name]", req)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "clone-vmmode-test", Version: "v0"}, nil)
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
		Name:      "parallels_vm_clone",
		Arguments: map[string]any{"name": "copy-one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	_, args := runner.lastCall()
	if !slices.Equal(args, []string{"clone", vmOneID, "--name", "copy-one"}) {
		t.Fatalf("args = %v", args)
	}

	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_clone",
		Arguments: map[string]any{"vm_id": vmTwoID, "name": "c"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatal("explicit vm_id must be rejected in VM mode")
	}
}
