package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/config"
	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/tools"
)

const (
	vmOneID = "90f90e2b-e19f-4429-86b3-b5146bc860d4"
	vmTwoID = "7f7436c5-af65-4533-b4cf-13496a71fb7e"
)

// toolSampleVMsJSON mirrors the shape of `prlctl list -a -i --json`.
const toolSampleVMsJSON = `[
	{
		"ID": "90f90e2b-e19f-4429-86b3-b5146bc860d4",
		"Name": "ubuntu_github_builder",
		"Type": "VM",
		"State": "stopped",
		"OS": "ubuntu",
		"Uptime": "2104507",
		"Home path": "/Volumes/local_storage_m2/Parallels/ubuntu_github_builder.pvm/config.pvs",
		"Home": "/Volumes/local_storage_m2/Parallels/ubuntu_github_builder.pvm/",
		"GuestTools": { "state": "outdated", "version": "20.3.0-55895" },
		"Hardware": {
			"cpu": { "cpus": 2, "type": "arm" },
			"memory": { "size": "4096Mb" },
			"net0": { "enabled": true, "type": "shared", "mac": "001C42F101D1", "card": "virtio" }
		}
	},
	{
		"ID": "7f7436c5-af65-4533-b4cf-13496a71fb7e",
		"Name": "windows11",
		"Type": "VM",
		"State": "running",
		"OS": "windows",
		"Hardware": { "cpu": { "cpus": 4 }, "memory": { "size": "8192Mb" } }
	}
]`

// testRunner is a fake CommandRunner that records calls.
type testRunner struct {
	mu       sync.Mutex
	calls    int
	lastExe  string
	lastArgs []string
	fn       func(exe string, args []string) (string, error)
}

func (f *testRunner) Run(_ context.Context, exe string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.lastExe = exe
	f.lastArgs = append([]string(nil), args...)
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(exe, args)
	}
	return toolSampleVMsJSON, nil
}

func (f *testRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *testRunner) lastCall() (string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastExe, append([]string(nil), f.lastArgs...)
}

// targetFilteringFn emulates `prlctl list <target> -a -i --json`: only the
// sample VMs matching the target (by ID or name) come back.
func targetFilteringFn() func(exe string, args []string) (string, error) {
	var all []map[string]any
	if err := json.Unmarshal([]byte(toolSampleVMsJSON), &all); err != nil {
		panic(err)
	}
	return func(_ string, args []string) (string, error) {
		target := ""
		if len(args) > 1 && args[0] == "list" {
			target = args[1]
		}
		if target == "" {
			return toolSampleVMsJSON, nil
		}
		matched := make([]map[string]any, 0, 1)
		for _, vm := range all {
			if vm["ID"] == target || vm["Name"] == target {
				matched = append(matched, vm)
			}
		}
		out, err := json.Marshal(matched)
		return string(out), err
	}
}

func lookPathFound(names ...string) func(string) (string, error) {
	found := map[string]string{}
	for _, n := range names {
		found[n] = "/usr/local/bin/" + n
	}
	return func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
}

// testConfig is the minimal config for in-memory protocol tests.
func testConfig() config.Config {
	return config.Config{LogLevel: slog.LevelInfo, VMCacheTTL: 30 * time.Second}
}

// initParallelsForTest installs the module singleton with a fake runner and
// a fake PATH. found==nil disables the module (binaries "not found").
func initParallelsForTest(t *testing.T, runner *testRunner, found []string, vmID string) {
	t.Helper()
	parallels.Reset()
	t.Cleanup(parallels.Reset)

	_, ok := parallels.Initialize(
		parallels.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		parallels.WithRunner(runner),
		parallels.WithLookPath(lookPathFound(found...)),
		parallels.WithVMID(vmID),
		parallels.WithCacheTTL(30*time.Second),
		parallels.WithFallbackDirs(), // never consult the real /usr/local/bin
	)
	if len(found) > 0 && !ok {
		t.Fatal("parallels module unexpectedly disabled")
	}
}

func listTools(t *testing.T, s *mcp.Server) []*mcp.Tool {
	t.Helper()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "toolnames", Version: "v0"}, nil)
	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := s.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return list.Tools
}

func toolNames(t *testing.T, s *mcp.Server) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, tl := range listTools(t, s) {
		names[tl.Name] = true
	}
	return names
}

// nullArrayUnionPaths walks a wire schema (map[string]any tree as seen by
// MCP clients) and returns the paths of every node whose "type" is a union
// containing both "null" and "array". Such unions are legal JSON Schema but
// less portable (single-type dialects like OpenAPI/Gemini may reject them).
func nullArrayUnionPaths(node any, path string) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		if t, ok := v["type"]; ok {
			if arr, ok := t.([]any); ok {
				hasNull, hasArray := false, false
				for _, x := range arr {
					if s, ok := x.(string); ok {
						if s == "null" {
							hasNull = true
						}
						if s == "array" {
							hasArray = true
						}
					}
				}
				if hasNull && hasArray {
					out = append(out, path)
				}
			}
		}
		for k, val := range v {
			out = append(out, nullArrayUnionPaths(val, path+"."+k)...)
		}
	case []any:
		for i, item := range v {
			out = append(out, nullArrayUnionPaths(item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return out
}

func TestListVmsHandlerFleet(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	_, out, err := tools.ListVms(context.Background(), nil, tools.ListVmsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 2 || len(out.Vms) != 2 {
		t.Fatalf("count = %d, len = %d, want 2", out.Count, len(out.Vms))
	}
	if out.Vms[0].ID != vmOneID || out.Vms[0].Name != "ubuntu_github_builder" || out.Vms[0].Hardware.CPU.Cpus != 2 {
		t.Fatalf("bad first VM: %+v", out.Vms[0])
	}
	if out.Source != "refreshed" {
		t.Fatalf("source = %q, want refreshed", out.Source)
	}
	if out.FetchedAt == "" {
		t.Fatal("fetched_at empty")
	}

	// Second call: served from the in-memory cache.
	_, out2, err := tools.ListVms(context.Background(), nil, tools.ListVmsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out2.Source != "cache" {
		t.Fatalf("source = %q, want cache", out2.Source)
	}
	if runner.count() != 1 {
		t.Fatalf("runner calls = %d, want 1 (cache hit)", runner.count())
	}

	// refresh=true forces a re-run.
	if _, _, err := tools.ListVms(context.Background(), nil, tools.ListVmsInput{Refresh: true}); err != nil {
		t.Fatal(err)
	}
	if runner.count() != 2 {
		t.Fatalf("runner calls = %d, want 2 after forced refresh", runner.count())
	}
}

func TestListVmsHandlerVMMode(t *testing.T) {
	runner := &testRunner{fn: targetFilteringFn()}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmTwoID)

	_, out, err := tools.ListVms(context.Background(), nil, tools.ListVmsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 1 || out.Vms[0].ID != vmTwoID {
		t.Fatalf("got %+v, want only %s", out.Vms, vmTwoID)
	}
}

func TestVMInfoHandler(t *testing.T) {
	runner := &testRunner{fn: targetFilteringFn()}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	_, out, err := tools.VMInfo(context.Background(), nil, tools.VMInfoInput{})
	if err != nil {
		t.Fatal(err)
	}
	blobBytes, err := json.Marshal(out.VM)
	if err != nil {
		t.Fatal(err)
	}
	var blob map[string]any
	if err := json.Unmarshal(blobBytes, &blob); err != nil {
		t.Fatal(err)
	}
	if blob["ID"] != vmOneID || blob["Name"] != "ubuntu_github_builder" {
		t.Fatalf("blob = %v", blob)
	}
	// The raw blob must preserve fields the typed model does not carry.
	if _, ok := blob["Uptime"]; !ok {
		t.Fatal("raw blob lost the Uptime field")
	}
}

func TestVMInfoHandlerFleetModeError(t *testing.T) {
	initParallelsForTest(t, &testRunner{}, []string{"prlctl", "prlsrvctl"}, "")
	if _, _, err := tools.VMInfo(context.Background(), nil, tools.VMInfoInput{}); err == nil {
		t.Fatal("want error: no target VM in fleet mode")
	}
}

func TestParallelsFleetProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	wireTools := listTools(t, srv)
	names := map[string]bool{}
	for _, tl := range wireTools {
		names[tl.Name] = true
	}
	if !names["parallels_list_vms"] {
		t.Fatalf("parallels_list_vms not listed: %v", names)
	}
	if names["parallels_vm_info"] {
		t.Fatalf("parallels_vm_info must not be listed in fleet mode: %v", names)
	}
	if !names["greet"] {
		t.Fatalf("greet missing: %v", names)
	}

	// The wire output schema must be portable: no ["null","array"] type
	// unions, and vms / ipAddresses must be plain "array" types.
	var listVmsSchema map[string]any
	for _, tl := range wireTools {
		if tl.Name == "parallels_list_vms" {
			listVmsSchema, _ = tl.OutputSchema.(map[string]any)
		}
	}
	if listVmsSchema == nil {
		t.Fatal("parallels_list_vms has no outputSchema on the wire")
	}
	if bad := nullArrayUnionPaths(listVmsSchema, "outputSchema"); len(bad) > 0 {
		t.Fatalf("non-portable output schema (null/array unions at): %v", bad)
	}
	vmsSchema, _ := listVmsSchema["properties"].(map[string]any)["vms"].(map[string]any)
	if vmsSchema == nil || vmsSchema["type"] != "array" {
		t.Fatalf("vms type = %v, want \"array\"", vmsSchema["type"])
	}
	ipSchema, _ := vmsSchema["items"].(map[string]any)["properties"].(map[string]any)["Network"].(map[string]any)["properties"].(map[string]any)["ipAddresses"].(map[string]any)
	if ipSchema == nil || ipSchema["type"] != "array" {
		t.Fatalf("ipAddresses type = %v, want \"array\"", ipSchema["type"])
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
	client := mcp.NewClient(&mcp.Implementation{Name: "fleet-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	// Warm-up already ran one prlctl list during server.New.
	if runner.count() != 1 {
		t.Fatalf("runner calls = %d, want 1 (warm-up)", runner.count())
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_list_vms", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["count"] != float64(2) {
		t.Fatalf("count = %v", got["count"])
	}
	if got["source"] != "cache" {
		t.Fatalf("source = %v (warm-up should have filled the cache)", got["source"])
	}
	vms, _ := got["vms"].([]any)
	first, _ := vms[0].(map[string]any)
	if first["Name"] != "ubuntu_github_builder" {
		t.Fatalf("first vm = %v", first)
	}
	if runner.count() != 1 {
		t.Fatalf("runner calls = %d, want 1 (cache hit)", runner.count())
	}
}

func TestParallelsVMModeProtocol(t *testing.T) {
	runner := &testRunner{fn: targetFilteringFn()}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testConfig()
	cfg.VMID = vmOneID
	srv := server.New(logger, cfg)

	names := toolNames(t, srv)
	if !names["parallels_list_vms"] || !names["parallels_vm_info"] {
		t.Fatalf("expected both parallels tools in VM mode: %v", names)
	}
	for _, tl := range listTools(t, srv) {
		if schema, ok := tl.OutputSchema.(map[string]any); ok {
			if bad := nullArrayUnionPaths(schema, tl.Name); len(bad) > 0 {
				t.Fatalf("%s: non-portable output schema (null/array unions at): %v", tl.Name, bad)
			}
		}
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
	client := mcp.NewClient(&mcp.Implementation{Name: "vmmode-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_info", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	got, _ := res.StructuredContent.(map[string]any)
	vmBlob, ok := got["vm"].(map[string]any)
	if !ok || vmBlob["ID"] != vmOneID {
		t.Fatalf("vm blob = %v", got["vm"])
	}

	listRes, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_list_vms", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if listRes.IsError {
		t.Fatalf("tool error: %+v", listRes.Content)
	}
	listGot, _ := listRes.StructuredContent.(map[string]any)
	if listGot["count"] != float64(1) {
		t.Fatalf("count = %v, want 1 (only the target VM)", listGot["count"])
	}
}

func TestParallelsDisabledProtocol(t *testing.T) {
	initParallelsForTest(t, &testRunner{}, nil, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	names := toolNames(t, srv)
	if names["parallels_list_vms"] || names["parallels_vm_info"] {
		t.Fatalf("parallels tools must not be exposed without the CLI: %v", names)
	}
	if !names["greet"] {
		t.Fatalf("greet missing: %v", names)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "disabled-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "greet", Arguments: map[string]any{"name": "x"}})
	if err != nil || res.IsError {
		t.Fatalf("greet should keep working: err=%v res=%+v", err, res)
	}
}
