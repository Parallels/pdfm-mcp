package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
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
	mu        sync.Mutex
	calls     int
	lastExe   string
	lastArgs  []string
	lastIn    string
	fn        func(exe string, args []string) (string, error)
	captureFn func(exe string, args []string) (stdout, stderr string, exit int)
	startFn   func(ctx context.Context, exe string, args []string) (parallels.Process, error)
}

// Start implements parallels.StreamRunner (async exec jobs).
func (f *testRunner) Start(ctx context.Context, exe string, args ...string) (parallels.Process, error) {
	f.record(exe, args)
	if f.startFn == nil {
		return nil, errors.New("testRunner: no startFn")
	}
	return f.startFn(ctx, exe, args)
}

func (f *testRunner) record(exe string, args []string) {
	f.mu.Lock()
	f.calls++
	f.lastExe = exe
	f.lastArgs = append([]string(nil), args...)
	f.mu.Unlock()
}

func (f *testRunner) Run(_ context.Context, exe string, args ...string) (string, error) {
	f.record(exe, args)
	if f.fn != nil {
		return f.fn(exe, args)
	}
	return toolSampleVMsJSON, nil
}

// RunCapture answers from captureFn when set, else degrades to fn
// (success → exit 0, failure → exit -1 plus the error).
func (f *testRunner) RunCapture(_ context.Context, exe string, args ...string) (string, string, int, error) {
	f.record(exe, args)
	if f.captureFn != nil {
		out, errText, exit := f.captureFn(exe, args)
		return out, errText, exit, nil
	}
	if f.fn != nil {
		out, err := f.fn(exe, args)
		if err != nil {
			return "", err.Error(), -1, err
		}
		return out, "", 0, nil
	}
	return toolSampleVMsJSON, "", 0, nil
}

// RunStdin answers like RunCapture and records the stdin payload.
func (f *testRunner) RunStdin(_ context.Context, exe, stdin string, args ...string) (string, string, int, error) {
	f.mu.Lock()
	f.lastIn = stdin
	f.mu.Unlock()
	return f.RunCapture(context.Background(), exe, args...)
}

func (f *testRunner) lastStdin() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastIn
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

// multiTypePaths walks a wire schema (map[string]any tree as seen by MCP
// clients) and returns the paths of every node whose "type" is an array of
// types. Single-type dialects (e.g. the OpenAPI subset used for Gemini
// function declarations) may reject such nodes or drop the constraint, so
// registered tool schemas must not contain them: pointer fields are split
// into single-type anyOf branches instead (splitNullableUnions).
func multiTypePaths(node any, path string) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		if t, ok := v["type"]; ok {
			if _, ok := t.([]any); ok {
				out = append(out, path)
			}
		}
		for k, val := range v {
			out = append(out, multiTypePaths(val, path+"."+k)...)
		}
	case []any:
		for i, item := range v {
			out = append(out, multiTypePaths(item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return out
}

// wireMetadataKeys are schema keywords that are metadata, not validation:
// a node carrying only these constrains nothing and is flagged below.
var wireMetadataKeys = map[string]bool{
	"$id": true, "$schema": true, "$comment": true, "$anchor": true,
	"title": true, "description": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

// bareNodePaths walks a wire schema (map[string]any tree as seen by MCP
// clients) and returns the paths of every node that carries no validation
// keyword at all (the object-literal spelling of a bare true). Such nodes
// accept any value, so some MCP clients reject them or drop the field;
// pinBareNodes keeps registered schemas free of them.
func bareNodePaths(node any, path string) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		validation := false
		for k := range v {
			if !wireMetadataKeys[k] {
				validation = true
				break
			}
		}
		if !validation {
			out = append(out, path)
		}
		for k, val := range v {
			out = append(out, bareNodePaths(val, path+"."+k)...)
		}
	case []any:
		for i, item := range v {
			out = append(out, bareNodePaths(item, fmt.Sprintf("%s[%d]", path, i))...)
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
	// Fleet mode without a vm_id argument: the mode contract rejects it
	// before anything runs.
	if _, _, err := tools.VMInfo(context.Background(), nil, tools.VMInfoInput{}); err == nil || !strings.Contains(err.Error(), "vm_id is required") {
		t.Fatalf("want vm_id required error, got: %v", err)
	}
}

// TestVMInfoHandlerFleetLive fetches one VM by id and by name through the
// live single-VM command (prlctl list <ref> -a -i --json) in fleet mode.
func TestVMInfoHandlerFleetLive(t *testing.T) {
	runner := &testRunner{fn: targetFilteringFn()}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	// By id.
	_, out, err := tools.VMInfo(context.Background(), nil, tools.VMInfoInput{VMID: vmOneID})
	if err != nil {
		t.Fatal(err)
	}
	if out.Source != "live" {
		t.Fatalf("source = %q, want live", out.Source)
	}
	blob := blobMap(t, out.VM)
	if blob["ID"] != vmOneID {
		t.Fatalf("blob ID = %v", blob["ID"])
	}
	// The exact single-VM command was used.
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" || len(args) != 5 || args[0] != "list" || args[1] != vmOneID ||
		args[2] != "-a" || args[3] != "-i" || args[4] != "--json" {
		t.Fatalf("argv = %q %v, want list <id> -a -i --json", exe, args)
	}

	// By name.
	_, out, err = tools.VMInfo(context.Background(), nil, tools.VMInfoInput{VMID: "ubuntu_github_builder"})
	if err != nil {
		t.Fatal(err)
	}
	blob = blobMap(t, out.VM)
	if blob["ID"] != vmOneID || blob["Name"] != "ubuntu_github_builder" {
		t.Fatalf("blob = %v", blob)
	}

	// Unknown ref: prlctl returns no results.
	if _, _, err := tools.VMInfo(context.Background(), nil, tools.VMInfoInput{VMID: "no-such-vm-xyz"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not found error, got: %v", err)
	}

	// Invalid ref (non-printable character) is rejected before any run.
	if _, _, err := tools.VMInfo(context.Background(), nil, tools.VMInfoInput{VMID: "bad\x00ref"}); err == nil || !strings.Contains(err.Error(), "invalid VM id or name") {
		t.Fatalf("want invalid ref error, got: %v", err)
	}
}

// TestVMInfoHandlerVMModeRejectsExplicitVMID keeps the VM-mode contract:
// the configured target is used, an explicit vm_id is rejected.
func TestVMInfoHandlerVMModeRejectsExplicitVMID(t *testing.T) {
	runner := &testRunner{fn: targetFilteringFn()}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	if _, _, err := tools.VMInfo(context.Background(), nil, tools.VMInfoInput{VMID: vmTwoID}); err == nil || !strings.Contains(err.Error(), "vm_id must be omitted") {
		t.Fatalf("want vm_id omitted error, got: %v", err)
	}
	// Without vm_id the configured target is still served from the cache.
	_, out, err := tools.VMInfo(context.Background(), nil, tools.VMInfoInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Source != "cache" && out.Source != "refreshed" {
		t.Fatalf("source = %q, want cache/refreshed", out.Source)
	}
	if blobMap(t, out.VM)["ID"] != vmOneID {
		t.Fatal("wrong VM served in VM mode")
	}
}

func blobMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
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
	// parallels_vm_info is listed in fleet mode too: it fetches a specific
	// VM by id or name with prlctl list <vm_id> -a -i --json.
	var infoInput map[string]any
	for _, tl := range wireTools {
		if tl.Name == "parallels_vm_info" {
			infoInput, _ = tl.InputSchema.(map[string]any)
		}
	}
	if infoInput == nil {
		t.Fatalf("parallels_vm_info not listed in fleet mode: %v", names)
	}
	req := map[string]bool{}
	for _, r := range infoInput["required"].([]any) {
		req[r.(string)] = true
	}
	if !req["vm_id"] || len(req) != 1 {
		t.Fatalf("fleet parallels_vm_info required = %v, want exactly [vm_id]", infoInput["required"])
	}

	// Every wire schema (input and output) must be portable: no node
	// carries a multi-type "type" array (the generator emits
	// ["null", T] for Go pointer fields; splitNullableUnions rewrites
	// those into single-type anyOf branches).
	for _, tl := range wireTools {
		for _, kind := range []string{"inputSchema", "outputSchema"} {
			var sc any
			if kind == "inputSchema" {
				sc = tl.InputSchema
			} else {
				sc = tl.OutputSchema
			}
			if sc == nil {
				continue
			}
			if bad := multiTypePaths(sc, tl.Name+"."+kind); len(bad) > 0 {
				t.Fatalf("%s: non-portable %s (multi-type nodes at): %v", tl.Name, kind, bad)
			}
			if bad := bareNodePaths(sc, tl.Name+"."+kind); len(bad) > 0 {
				t.Fatalf("%s: non-portable %s (keyword-less nodes at): %v", tl.Name, kind, bad)
			}
		}
	}

	// The Go `any` fields are declared as JSON objects on the wire: the
	// sequence step's arguments and output, and vm_info's prlctl object.
	var seqInput, seqOutput, infoOutput map[string]any
	for _, tl := range wireTools {
		switch tl.Name {
		case "parallels_vm_sequence":
			seqInput, _ = tl.InputSchema.(map[string]any)
			seqOutput, _ = tl.OutputSchema.(map[string]any)
		case "parallels_vm_info":
			infoOutput, _ = tl.OutputSchema.(map[string]any)
		}
	}
	argsNode, _ := seqInput["properties"].(map[string]any)["steps"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["args"].(map[string]any)
	if argsNode == nil || argsNode["type"] != "object" {
		t.Fatalf("sequence args node = %v, want type object", argsNode)
	}
	if _, has := argsNode["additionalProperties"]; has {
		t.Fatalf("sequence args must not carry additionalProperties: %v", argsNode)
	}
	stepOutNode, _ := seqOutput["properties"].(map[string]any)["results"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["output"].(map[string]any)
	if stepOutNode == nil || stepOutNode["type"] != "object" {
		t.Fatalf("sequence step output node = %v, want type object", stepOutNode)
	}
	vmNode, _ := infoOutput["properties"].(map[string]any)["vm"].(map[string]any)
	if vmNode == nil || vmNode["type"] != "object" {
		t.Fatalf("vm_info vm node = %v, want type object", vmNode)
	}

	// delay_ms is the one nullable input field: the wire form must be the
	// anyOf split — two single-type branches, no raw "type" array — with
	// the description preserved.
	var typeInput map[string]any
	for _, tl := range wireTools {
		if tl.Name == "parallels_vm_type" {
			typeInput, _ = tl.InputSchema.(map[string]any)
		}
	}
	if typeInput == nil {
		t.Fatal("parallels_vm_type not listed")
	}
	dm, _ := typeInput["properties"].(map[string]any)["delay_ms"].(map[string]any)
	if dm == nil {
		t.Fatal("delay_ms missing from the vm_type input schema")
	}
	if _, has := dm["type"]; has {
		t.Fatalf("delay_ms carries a raw type: %v", dm)
	}
	if dm["description"] == "" {
		t.Fatalf("delay_ms lost its description: %v", dm)
	}
	branches, _ := dm["anyOf"].([]any)
	if len(branches) != 2 {
		t.Fatalf("delay_ms anyOf = %v, want 2 branches", dm)
	}
	branchTypes := map[string]bool{}
	for _, b := range branches {
		bm, _ := b.(map[string]any)
		typ, _ := bm["type"].(string)
		if typ == "" {
			t.Fatalf("anyOf branch without a single type: %v", b)
		}
		branchTypes[typ] = true
	}
	if !branchTypes["null"] || !branchTypes["integer"] || len(branchTypes) != 2 {
		t.Fatalf("delay_ms anyOf types = %v, want exactly {null integer}", branchTypes)
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

	// No binaries: the server still starts and speaks MCP, but exposes
	// no tools at all.
	names := toolNames(t, srv)
	if len(names) != 0 {
		t.Fatalf("no tools may be exposed without the CLI: %v", names)
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
	tls, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tls.Tools) != 0 {
		var listed []string
		for _, tl := range tls.Tools {
			listed = append(listed, tl.Name)
		}
		t.Fatalf("client sees tools in disabled mode: %v", listed)
	}
}
