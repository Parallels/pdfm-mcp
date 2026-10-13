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

// wireRequiredList returns the tool's wire input schema "required" list.
func wireRequiredList(t *testing.T, srv *mcp.Server, toolName string) []string {
	t.Helper()
	for _, tl := range listTools(t, srv) {
		if tl.Name != toolName {
			continue
		}
		schema, ok := tl.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: input schema = %T, want map", toolName, tl.InputSchema)
		}
		var out []string
		raw, _ := schema["required"].([]any) // absent when nothing is required
		for _, r := range raw {
			out = append(out, r.(string))
		}
		return out
	}
	t.Fatalf("%s not listed", toolName)
	return nil
}

// flatText joins all TextContent blocks of a result for assertions.
func flatText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestExecHandlerModes(t *testing.T) {
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
			_, out, err := tools.VMExec(context.Background(), nil, tools.ExecInput{VMID: tc.arg, Command: "id"})
			if tc.want == "" {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.VMID != tc.want {
				t.Fatalf("out.VMID = %q, want %q", out.VMID, tc.want)
			}
			if runner.count() != 1 {
				t.Fatalf("runner calls = %d, want 1", runner.count())
			}
			exe, args := runner.lastCall()
			if exe != "/usr/local/bin/prlctl" || !slices.Equal(args, []string{"exec", tc.want, "--current-user", "id"}) {
				t.Fatalf("prlctl call = %s %v", exe, args)
			}
			if out.ExitCode != 0 {
				t.Fatalf("exit_code = %d, want 0", out.ExitCode)
			}
		})
	}
}

func TestExecNonZeroExitIsNotAnError(t *testing.T) {
	runner := &testRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "partial output", "no matches", 1
	}}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, out, err := tools.VMExec(context.Background(), nil, tools.ExecInput{VMID: "guest-x", Command: "grep -q x /dev/null"})
	if err != nil {
		t.Fatalf("non-zero guest exit must not be a tool error: %v", err)
	}
	if out.ExitCode != 1 || out.Stdout != "partial output" || out.Stderr != "no matches" {
		t.Fatalf("out = %+v", out)
	}
}

func TestExecUserAndResolvePathsReachArgv(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	if _, _, err := tools.VMExec(context.Background(), nil, tools.ExecInput{
		VMID: "guest-x", Command: "cat /host/file", User: "builder", ResolvePaths: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, args := runner.lastCall()
	want := []string{"exec", "guest-x", "--user", "builder", "--resolve-paths", "cat", "/host/file"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestExecCommandValidation(t *testing.T) {
	cases := []struct {
		command  string
		wantPart string
	}{
		{"", "empty"},
		{"   ", "empty"},
		{strings.Repeat("a", 4097), "limit"},
	}
	for _, tc := range cases {
		runner := &testRunner{}
		initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
		if _, _, err := tools.VMExec(context.Background(), nil, tools.ExecInput{VMID: "guest-x", Command: tc.command}); err == nil || !strings.Contains(err.Error(), tc.wantPart) {
			t.Fatalf("command %q: err = %v, want one containing %q", tc.command, err, tc.wantPart)
		}
		if runner.count() != 0 {
			t.Fatalf("prlctl must not run for invalid command: %d calls", runner.count())
		}
	}
}

func TestParallelsExecProtocol(t *testing.T) {
	// The fake makes the "guest command" fail with exit 3: on the wire this
	// must be a NORMAL result with exit_code 3, not an error.
	runner := &testRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "exit three\n", "stderr text", 3
	}}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	names := toolNames(t, srv)
	if !names["parallels_vm_exec"] {
		t.Fatalf("parallels_vm_exec not listed: %v", names)
	}

	// Fleet: vm_id AND command are required.
	requiredList := wireRequiredList(t, srv, "parallels_vm_exec")
	if !slices.Contains(requiredList, "vm_id") || !slices.Contains(requiredList, "command") {
		t.Fatalf("fleet mode: required = %v, want vm_id and command", requiredList)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "exec-test", Version: "v0"}, nil)
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
		Name:      "parallels_vm_exec",
		Arguments: map[string]any{"vm_id": "guest-x", "command": "false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("non-zero guest exit must not be a tool error: %+v", res.Content)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	if sc == nil {
		t.Fatal("no structured content")
	}
	if sc["vm_id"] != "guest-x" || sc["exit_code"] != float64(3) {
		t.Fatalf("structured = %v", sc)
	}
	if sc["stdout"] != "exit three\n" || sc["stderr"] != "stderr text" {
		t.Fatalf("structured = %v", sc)
	}
	if sc["stdout_truncated"] != false || sc["stderr_truncated"] != false {
		t.Fatalf("truncation flags: %v", sc)
	}

	// Missing command must be rejected by the schema (fleet: vm_id too).
	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_exec",
		Arguments: map[string]any{"vm_id": "guest-x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError || !strings.Contains(flatText(bad), "command") {
		t.Fatalf("missing command must be a schema error: %+v", bad.Content)
	}
}

func TestParallelsExecVMModeProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testConfig()
	cfg.VMID = vmOneID
	srv := server.New(logger, cfg)

	requiredList := wireRequiredList(t, srv, "parallels_vm_exec")
	if slices.Contains(requiredList, "vm_id") || !slices.Contains(requiredList, "command") {
		t.Fatalf("VM mode: required = %v, want command but not vm_id", requiredList)
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
	client := mcp.NewClient(&mcp.Implementation{Name: "exec-vmmode-test", Version: "v0"}, nil)
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
		Name:      "parallels_vm_exec",
		Arguments: map[string]any{"command": "uname -a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	_, args := runner.lastCall()
	want := []string{"exec", vmOneID, "--current-user", "uname", "-a"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}

	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_exec",
		Arguments: map[string]any{"vm_id": vmTwoID, "command": "id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatal("explicit vm_id must be rejected in VM mode")
	}
}
