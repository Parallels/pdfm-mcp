package tools_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/policy"
	"github.com/Parallels/pdfm-mcp/internal/server"
	"github.com/Parallels/pdfm-mcp/internal/tools"
)

// rankingSampleVMsJSON is a dedicated sample list for ranking assertions:
// "beta" is the exact-name match for query "beta", "BetaBox" the prefix
// match, "alpha-beta" the substring match, and "gamma" only matches by
// UUID substring.
const rankingSampleVMsJSON = `[
	{"ID": "11111111-1111-1111-1111-111111111111", "Name": "alpha-beta", "State": "running", "OS": "ubuntu",
		"Network": {"ipAddresses": [{"type": "shared", "ip": "10.0.0.3"}]}},
	{"ID": "22222222-2222-2222-2222-222222222222", "Name": "beta", "State": "running", "OS": "ubuntu"},
	{"ID": "33333333-3333-3333-3333-333333333333", "Name": "BetaBox", "State": "stopped", "OS": "windows"},
	{"ID": "4444zzz-4444-4444-4444-444444444123", "Name": "gamma", "State": "stopped", "OS": "macos"}
]`

func rankingListFn() func(string, []string) (string, error) {
	return func(_ string, args []string) (string, error) {
		if args[0] == "list" {
			return rankingSampleVMsJSON, nil
		}
		return "", nil
	}
}

func searchNames(t *testing.T, out tools.SearchVmsOutput) []string {
	t.Helper()
	names := make([]string, 0, len(out.Vms))
	for _, v := range out.Vms {
		names = append(names, v.Name)
	}
	return names
}

func TestSearchVmsHandlerFleet(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	// Name substring (case-insensitive), compact fields, cache semantics.
	_, out, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "UBUNTU"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 1 || len(out.Vms) != 1 || out.Total != 2 {
		t.Fatalf("count=%d total=%d, want 1/2", out.Count, out.Total)
	}
	if v := out.Vms[0]; v.ID != vmOneID || v.Name != "ubuntu_github_builder" || v.State != "stopped" || v.OS != "ubuntu" {
		t.Fatalf("vm = %+v", v)
	}
	if out.Source != "refreshed" || out.FetchedAt == "" {
		t.Fatalf("source=%q fetched_at=%q", out.Source, out.FetchedAt)
	}
	if runner.count() != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.count())
	}

	// Second call: cache hit, no extra prlctl call.
	_, out2, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "ubuntu"})
	if err != nil {
		t.Fatal(err)
	}
	if out2.Source != "cache" || runner.count() != 1 {
		t.Fatalf("source=%q calls=%d, want cache/1", out2.Source, runner.count())
	}

	// UUID substring match.
	_, out3, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "7f7436c5"})
	if err != nil {
		t.Fatal(err)
	}
	if out3.Count != 1 || out3.Vms[0].ID != vmTwoID {
		t.Fatalf("uuid match = %+v", out3.Vms)
	}

	// No match: empty vms, total preserved.
	_, out4, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "zzz"})
	if err != nil {
		t.Fatal(err)
	}
	if out4.Count != 0 || out4.Total != 2 || len(out4.Vms) != 0 {
		t.Fatalf("no-match = %+v, want 0 results / total 2", out4)
	}

	// State filter: "i" matches both names, only one is running.
	_, out5, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "i", State: "RUNNING"})
	if err != nil {
		t.Fatal(err)
	}
	if out5.Count != 1 || out5.Vms[0].ID != vmTwoID {
		t.Fatalf("state filter = %+v, want only running windows11", out5.Vms)
	}

	// Limit clamping: both sample VMs contain "e", keep one.
	_, out6, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "e", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out6.Count != 1 || out6.Total != 2 {
		t.Fatalf("limit = %+v, want 1 result / total 2", out6)
	}

	// Empty / over-long query rejected.
	if _, _, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "   "}); err == nil {
		t.Fatal("want error for blank query")
	}
	if _, _, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: strings.Repeat("x", 201)}); err == nil {
		t.Fatal("want error for over-long query")
	}

	// refresh=true forces exactly one extra prlctl call.
	if _, _, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "e", Refresh: true}); err != nil {
		t.Fatal(err)
	}
	if runner.count() != 2 {
		t.Fatalf("runner calls = %d, want 2 after refresh", runner.count())
	}
}

func TestSearchVmsRanking(t *testing.T) {
	runner := &testRunner{fn: rankingListFn()}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	// Exact name (0) before prefix (2) before substring (3).
	_, out, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"beta", "BetaBox", "alpha-beta"}
	got := searchNames(t, out)
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}

	// The state filter keeps the ranking: "beta" + running drops BetaBox.
	_, out2, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "beta", State: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if got := searchNames(t, out2); len(got) != 2 || got[0] != "beta" || got[1] != "alpha-beta" {
		t.Fatalf("names = %v, want [beta alpha-beta]", got)
	}

	// UUID substring is the weakest match.
	_, out3, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if out3.Count != 1 || out3.Vms[0].Name != "gamma" || out3.Vms[0].ID != "4444zzz-4444-4444-4444-444444444123" {
		t.Fatalf("uuid-substring = %+v, want gamma", out3.Vms)
	}

	// IP summary field.
	_, out4, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "alpha-beta"})
	if err != nil {
		t.Fatal(err)
	}
	if out4.Vms[0].IP != "10.0.0.3" {
		t.Fatalf("ip = %q, want 10.0.0.3", out4.Vms[0].IP)
	}
}

func TestSearchVmsVMMode(t *testing.T) {
	runner := &testRunner{fn: targetFilteringFn()}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmTwoID)

	// VM mode: only the target VM is in the list.
	_, out, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "ubuntu"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 0 || out.Total != 1 {
		t.Fatalf("vm mode = %+v, want 0 results / total 1", out)
	}
	_, out2, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "win"})
	if err != nil {
		t.Fatal(err)
	}
	if out2.Count != 1 || out2.Vms[0].ID != vmTwoID {
		t.Fatalf("vm mode target = %+v, want windows11", out2.Vms)
	}
}

func TestSearchVmsAllowList(t *testing.T) {
	t.Cleanup(func() { tools.SetVMAllowList(nil) })
	tools.SetVMAllowList(policy.NewVMAllowList([]string{vmTwoID}))
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	// "e" matches both sample VMs; only the allowed one may leave.
	_, out, err := tools.SearchVms(context.Background(), nil, tools.SearchVmsInput{Query: "e"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 1 || out.Vms[0].ID != vmTwoID || out.Total != 1 {
		t.Fatalf("allow-list = %+v, want only windows11", out)
	}
}

func TestParallelsSearchVmsProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	wireTools := listTools(t, srv)
	var input map[string]any
	for _, tl := range wireTools {
		if tl.Name != "parallels_search_vms" {
			continue
		}
		schema, ok := tl.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("parallels_search_vms input schema = %T, want map", tl.InputSchema)
		}
		input = schema
	}
	if input == nil {
		t.Fatal("parallels_search_vms not listed")
	}
	required, _ := input["required"].([]any)
	req := map[string]bool{}
	for _, r := range required {
		req[r.(string)] = true
	}
	if !req["query"] || len(req) != 1 {
		t.Fatalf("required = %v, want exactly [query]", required)
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
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "search-test", Version: "v0"}, nil).Connect(ctx, clientT, nil)
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

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_search_vms", Arguments: map[string]any{"query": "ubuntu"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["count"] != float64(1) || got["total"] != float64(2) {
		t.Fatalf("count=%v total=%v, want 1/2", got["count"], got["total"])
	}
	vms, _ := got["vms"].([]any)
	first, _ := vms[0].(map[string]any)
	if first["id"] != vmOneID || first["name"] != "ubuntu_github_builder" || first["state"] != "stopped" {
		t.Fatalf("first vm = %v", first)
	}
	if runner.count() != 1 {
		t.Fatalf("runner calls = %d, want 1 (cache hit)", runner.count())
	}

	// No query on the wire: the schema rejects it.
	res2, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_search_vms", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res2.IsError {
		t.Fatalf("expected validation error for missing query: %+v", res2.StructuredContent)
	}
}
