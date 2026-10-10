package tools_test

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/config"
	"github.com/Parallels/pdfm-mcp/internal/policy"
	"github.com/Parallels/pdfm-mcp/internal/server"
	"github.com/Parallels/pdfm-mcp/internal/tools"
)

// policyServer brings up a server with the given config on a fake runner.
// The VM allow-list global is reset when the test ends.
func policyServer(t *testing.T, runner *testRunner, cfg config.Config) *mcp.Server {
	t.Helper()
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, cfg.VMID)
	t.Cleanup(func() { tools.SetVMAllowList(nil) })
	return server.New(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
}

func policySession(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "policy-test", Version: "v0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("close client: %v", err)
		}
		if err := sess.Close(); err != nil {
			t.Logf("close server: %v", err)
		}
	})
	return cs
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// TestEveryRegisteredToolIsClassified fails when a tool is registered by
// server.New without an entry in the internal/policy classification table.
func TestEveryRegisteredToolIsClassified(t *testing.T) {
	srv := policyServer(t, &testRunner{fn: powerListFn()}, testConfig())
	for name := range toolNames(t, srv) {
		if !policy.Known(name) {
			t.Errorf("tool %q is registered but missing from the classification table in internal/policy/policy.go: add it with the right class (read/input/power/exec/destructive/secret). Unclassified tools are treated as destructive.", name)
		}
	}
}

// TestRegistrationTableNamesMatchToolNames denies each registered tool by
// name and checks exactly that tool disappears: the table names used for
// the policy check must equal the real MCP tool names.
func TestRegistrationTableNamesMatchToolNames(t *testing.T) {
	all := toolNames(t, policyServer(t, &testRunner{fn: powerListFn()}, testConfig()))
	if len(all) < 20 {
		t.Fatalf("unexpectedly few tools: %v", sortedNames(all))
	}
	for name := range all {
		cfg := testConfig()
		cfg.DenyTools = []string{name}
		got := toolNames(t, policyServer(t, &testRunner{fn: powerListFn()}, cfg))
		if got[name] {
			t.Errorf("deny %s: tool still listed", name)
		}
		if len(got) != len(all)-1 {
			t.Errorf("deny %s: %d tools left, want %d", name, len(got), len(all)-1)
		}
	}
}

func TestReadOnlyListsOnlyReadTools(t *testing.T) {
	all := toolNames(t, policyServer(t, &testRunner{fn: powerListFn()}, testConfig()))

	runner := &testRunner{fn: powerListFn()}
	cfg := testConfig()
	cfg.ReadOnly = true
	srv := policyServer(t, runner, cfg)
	got := toolNames(t, srv)

	for name := range all {
		c := policy.Classify(name)
		want := c == policy.ClassRead || c == policy.ClassSequence
		if got[name] != want {
			t.Errorf("read-only: %s (class %s) listed=%v, want %v", name, c, got[name], want)
		}
	}
	for _, must := range []string{"parallels_list_vms", "parallels_vm_info", "parallels_vm_capture", "parallels_vm_sequence"} {
		if !got[must] {
			t.Errorf("read-only: %s missing: %v", must, sortedNames(got))
		}
	}
	for _, banned := range []string{"parallels_vm_start", "parallels_vm_stop", "parallels_vm_exec", "parallels_vm_type", "parallels_vm_delete", "parallels_vm_encrypt", "parallels_vm_reclaim_disk", "parallels_vm_clone"} {
		if got[banned] {
			t.Errorf("read-only: %s must not be listed", banned)
		}
	}

	// The sequence description lists only allowed steps.
	for _, tl := range listTools(t, srv) {
		if tl.Name != "parallels_vm_sequence" {
			continue
		}
		if !strings.Contains(tl.Description, "parallels_vm_capture(") {
			t.Errorf("sequence description lacks capture step: %s", tl.Description)
		}
		for _, banned := range []string{"parallels_vm_start(", "parallels_vm_exec(", "parallels_vm_delete(", "parallels_vm_type("} {
			if strings.Contains(tl.Description, banned) {
				t.Errorf("sequence description still lists %s", banned)
			}
		}
	}

	// A sequence cannot run a hidden tool and prlctl never sees it.
	cs := policySession(t, srv)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "parallels_vm_sequence",
		Arguments: map[string]any{
			"vm_id": "guest-x",
			"steps": []any{map[string]any{"tool": "parallels_vm_start"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = res // outcome shape is the orchestrator's; what matters is below.
	if _, args := runner.lastCall(); len(args) > 0 && args[0] == "start" {
		t.Fatalf("read-only sequence executed start: %v", args)
	}
}

func TestAllowToolsAndDenyWins(t *testing.T) {
	cfg := testConfig()
	cfg.AllowTools = []string{"parallels_list_vms", "parallels_vm_power_*", "parallels_vm_st*"}
	cfg.DenyTools = []string{"parallels_vm_stop"}
	got := toolNames(t, policyServer(t, &testRunner{fn: powerListFn()}, cfg))
	for _, want := range []string{"parallels_list_vms", "parallels_vm_start"} {
		if !got[want] {
			t.Errorf("%s missing: %v", want, sortedNames(got))
		}
	}
	for _, banned := range []string{"parallels_vm_stop", "parallels_vm_exec", "parallels_vm_sequence", "parallels_vm_info"} {
		if got[banned] {
			t.Errorf("%s must be hidden (deny wins / not allowed)", banned)
		}
	}
}

func TestReadOnlyCannotBeWidenedByAllow(t *testing.T) {
	cfg := testConfig()
	cfg.ReadOnly = true
	cfg.AllowTools = []string{"parallels_vm_*"}
	got := toolNames(t, policyServer(t, &testRunner{fn: powerListFn()}, cfg))
	if got["parallels_vm_start"] || got["parallels_vm_exec"] {
		t.Fatalf("allow list widened read-only: %v", sortedNames(got))
	}
	if !got["parallels_vm_info"] {
		t.Fatalf("parallels_vm_info should remain: %v", sortedNames(got))
	}
}

// --- VM allow-list ---------------------------------------------------

func TestAllowVMsFleetEnforcement(t *testing.T) {
	cfg := testConfig()
	cfg.AllowVMs = []string{"windows11"}
	runner := &testRunner{fn: powerListFn()}
	srv := policyServer(t, runner, cfg)
	cs := policySession(t, srv)
	ctx := context.Background()

	// Allowed by exact name.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_start", Arguments: map[string]any{"vm_id": "windows11"}})
	if err != nil || res.IsError {
		t.Fatalf("allowed name rejected: err=%v res=%+v", err, res)
	}
	// Allowed by the other form (id of an allowed name, resolved via cache).
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_start", Arguments: map[string]any{"vm_id": vmTwoID}})
	if err != nil || res.IsError {
		t.Fatalf("id of allowed VM rejected: err=%v res=%+v", err, res)
	}
	// Not allowed, for every VM-scoped tool shape.
	for _, name := range []string{"parallels_vm_start", "parallels_vm_stop", "parallels_vm_info", "parallels_vm_capture", "parallels_vm_exec"} {
		args := map[string]any{"vm_id": "ubuntu_github_builder"}
		if name == "parallels_vm_exec" {
			args["command"] = "true"
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Errorf("%s: disallowed VM was accepted", name)
			continue
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if !strings.Contains(text, "--allow-vms") {
			t.Errorf("%s: error does not name the rule: %s", name, text)
		}
		for _, leak := range []string{"windows11", vmTwoID, "ubuntu_github_builder"} {
			if strings.Contains(text, leak) {
				t.Errorf("%s: error leaks %q: %s", name, leak, text)
			}
		}
	}
	// Disallowed id too.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_stop", Arguments: map[string]any{"vm_id": vmOneID}})
	if err != nil || !res.IsError {
		t.Fatalf("disallowed id accepted: err=%v res=%+v", err, res)
	}
	// An unknown ref is denied (fail closed).
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_stop", Arguments: map[string]any{"vm_id": "nope"}})
	if err != nil || !res.IsError {
		t.Fatalf("unknown ref accepted: err=%v res=%+v", err, res)
	}
}

func TestAllowVMsListFilterNoLeak(t *testing.T) {
	cfg := testConfig()
	cfg.AllowVMs = []string{vmOneID}
	srv := policyServer(t, &testRunner{fn: powerListFn()}, cfg)
	cs := policySession(t, srv)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "parallels_list_vms", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list: err=%v res=%+v", err, res)
	}
	got, _ := res.StructuredContent.(map[string]any)
	vms, _ := got["vms"].([]any)
	if len(vms) != 1 || got["count"] != float64(1) {
		t.Fatalf("filtered list = %v (count %v), want exactly 1", vms, got["count"])
	}
	if vm, _ := vms[0].(map[string]any); vm["ID"] != vmOneID {
		t.Fatalf("wrong VM kept: %v", vm["ID"])
	}
	raw := res.Content[0].(*mcp.TextContent).Text
	if strings.Contains(raw, "windows11") || strings.Contains(raw, vmTwoID) {
		t.Fatalf("disallowed VM leaked in list output: %s", raw)
	}
}

func TestAllowVMsFleetInfo(t *testing.T) {
	cfg := testConfig()
	cfg.AllowVMs = []string{"ubuntu_github_builder"}
	runner := &testRunner{fn: targetFilteringFn()}
	srv := policyServer(t, runner, cfg)
	cs := policySession(t, srv)
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_info", Arguments: map[string]any{"vm_id": "ubuntu_github_builder"}})
	if err != nil || res.IsError {
		t.Fatalf("allowed info rejected: err=%v res=%+v", err, res)
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_info", Arguments: map[string]any{"vm_id": "windows11"}})
	if err != nil || !res.IsError {
		t.Fatalf("disallowed info accepted: err=%v res=%+v", err, res)
	}
}

func TestAllowVMsVMMode(t *testing.T) {
	cfg := testConfig()
	cfg.VMID = vmOneID
	cfg.AllowVMs = []string{vmOneID}
	runner := &testRunner{fn: targetFilteringFn()}
	srv := policyServer(t, runner, cfg)
	cs := policySession(t, srv)
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_info", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("VM mode info: err=%v res=%+v", err, res)
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_list_vms", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("VM mode list: err=%v res=%+v", err, res)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["count"] != float64(1) {
		t.Fatalf("count = %v, want 1", got["count"])
	}
}

func TestAllowVMsVMModeTargetOutsideListDenied(t *testing.T) {
	// A hand-built config bypassing config.Load: the handler-level check
	// still denies a target that is not on the list.
	cfg := testConfig()
	cfg.VMID = vmOneID
	cfg.AllowVMs = []string{vmTwoID}
	srv := policyServer(t, &testRunner{fn: targetFilteringFn()}, cfg)
	cs := policySession(t, srv)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "parallels_vm_info", Arguments: map[string]any{}})
	if err != nil || !res.IsError {
		t.Fatalf("target outside allow-list accepted: err=%v res=%+v", err, res)
	}
}
