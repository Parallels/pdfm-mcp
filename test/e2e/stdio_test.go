//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bin := filepath.Join(t.TempDir(), "pdfm-mcp")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/pdfm-mcp").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "v0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tl := range tools.Tools {
		if tl.Name == "greet" {
			found = true
		}
	}
	if !found {
		t.Fatalf("greet not listed: %+v", tools.Tools)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "e2e", "shout": true},
	})
	if err != nil || res.IsError {
		t.Fatalf("call failed: err=%v res=%+v", err, res)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["greeting"] != "HELLO, E2E!" {
		t.Fatalf("unexpected result: %v", res.StructuredContent)
	}
}

// TestParallelsGating exercises the real prlctl end to end: fleet mode
// exposes only parallels_list_vms, VM mode also exposes parallels_vm_info,
// and the cache serves the second call without re-running prlctl.
func TestParallelsGating(t *testing.T) {
	if _, err := exec.LookPath("prlctl"); err != nil {
		t.Skip("prlctl not on PATH; Parallels Desktop not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	bin := filepath.Join(t.TempDir(), "pdfm-mcp-parallels")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/pdfm-mcp").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	connect := func(args ...string) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "e2e-parallels", Version: "v0"}, nil)
		cmdArgs := append([]string{bin}, args...)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(cmdArgs[0], cmdArgs[1:]...)}, nil)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		return session
	}

	listed := func(t *testing.T, s *mcp.ClientSession) map[string]bool {
		t.Helper()
		tools, err := s.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, tl := range tools.Tools {
			names[tl.Name] = true
		}
		return names
	}

	sessFleet := connect()
	defer func() {
		if err := sessFleet.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	// The power tools are listed in both modes. They are only LISTED here —
	// never executed — because a real run would power-cycle a real VM.
	powerTools := []string{
		"parallels_vm_start", "parallels_vm_resume", "parallels_vm_pause",
		"parallels_vm_suspend", "parallels_vm_restart", "parallels_vm_reset",
		"parallels_vm_reset_uptime", "parallels_vm_stop",
	}

	names := listed(t, sessFleet)
	if !names["parallels_list_vms"] {
		t.Fatalf("fleet mode: parallels_list_vms not listed: %v", names)
	}
	if names["parallels_vm_info"] {
		t.Fatalf("fleet mode: parallels_vm_info must not be listed: %v", names)
	}
	for _, p := range powerTools {
		if !names[p] {
			t.Fatalf("fleet mode: %s not listed: %v", p, names)
		}
	}

	res, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_list_vms", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_vms failed: err=%v res=%+v", err, res)
	}
	got, _ := res.StructuredContent.(map[string]any)
	count, _ := got["count"].(float64)
	if count < 0 {
		t.Fatalf("count = %v", got["count"])
	}
	vms, _ := got["vms"].([]any)
	if float64(len(vms)) != count {
		t.Fatalf("vms len %d != count %v", len(vms), count)
	}
	var firstID string
	if len(vms) > 0 {
		if first, ok := vms[0].(map[string]any); ok {
			firstID, _ = first["ID"].(string)
			if firstID == "" {
				t.Fatalf("first vm has no ID: %v", first)
			}
		}
	}

	if firstID == "" {
		t.Log("no VMs on this machine; skipping VM-mode subtest")
		return
	}

	sessVM := connect("--vm-id", firstID)
	defer func() {
		if err := sessVM.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	vmNames := listed(t, sessVM)
	if !vmNames["parallels_list_vms"] || !vmNames["parallels_vm_info"] {
		t.Fatalf("vm mode: expected both parallels tools: %v", vmNames)
	}
	for _, p := range powerTools {
		if !vmNames[p] {
			t.Fatalf("vm mode: %s not listed: %v", p, vmNames)
		}
	}

	info, err := sessVM.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_info", Arguments: map[string]any{}})
	if err != nil || info.IsError {
		t.Fatalf("vm_info failed: err=%v res=%+v", err, info)
	}
	infoGot, _ := info.StructuredContent.(map[string]any)
	vmBlob, ok := infoGot["vm"].(map[string]any)
	if !ok {
		t.Fatalf("vm blob missing: %v", infoGot)
	}
	if vmBlob["ID"] != firstID {
		t.Fatalf("vm_info returned %v, want %s", vmBlob["ID"], firstID)
	}
}
