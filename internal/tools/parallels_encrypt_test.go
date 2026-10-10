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

// encTestRunner emulates prlctl's encryption commands: the dry-run Run path
// returns the prerequisite message; the RunStdin-fed real commands (which
// go through RunCapture in the fake) return the success lines.
func encTestRunner() *testRunner {
	return &testRunner{
		fn: func(_ string, args []string) (string, error) {
			switch {
			case args[0] == "encrypt" && len(args) > 2 && args[2] == "--dry-run":
				return "All prerequisites for the virtual machine encryption have been met", nil
			case args[0] == "decrypt" && len(args) > 2 && args[2] == "--dry-run":
				return "All prerequisites for the virtual machine decryption have been met", nil
			}
			return "ok", nil
		},
		captureFn: func(_ string, args []string) (string, string, int) {
			switch args[0] {
			case "encrypt":
				return "The virtual machine has been successfully encrypted.", "", 0
			case "decrypt":
				return "The virtual machine has been successfully decrypted.", "", 0
			case "change-passwd":
				return "The password has been successfully changed.", "", 0
			}
			return "ok", "", 0
		},
	}
}

func wireRequired(t *testing.T, inputSchema any) map[string]bool {
	t.Helper()
	s, _ := inputSchema.(map[string]any)
	if s == nil {
		t.Fatal("tool has no input schema")
	}
	req := map[string]bool{}
	if raw, ok := s["required"].([]any); ok {
		for _, r := range raw {
			req[r.(string)] = true
		}
	}
	return req
}

func TestEncryptFleetProtocol(t *testing.T) {
	runner := encTestRunner()
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())
	wireTools := listTools(t, srv)

	var enc *mcp.Tool
	for _, tl := range wireTools {
		if tl.Name == "parallels_vm_encrypt" {
			enc = tl
		}
	}
	if enc == nil {
		t.Fatal("parallels_vm_encrypt not listed in fleet mode")
	}
	req := wireRequired(t, enc.InputSchema)
	if !req["vm_id"] || len(req) != 1 {
		t.Fatalf("fleet encrypt required = %v, want exactly [vm_id]", req)
	}
	if enc.Annotations == nil || enc.Annotations.ReadOnlyHint ||
		enc.Annotations.DestructiveHint == nil || !*enc.Annotations.DestructiveHint {
		t.Fatalf("encrypt must be destructive and not read-only: %+v", enc.Annotations)
	}

	// Real run: password reaches prlctl over stdin, never argv.
	_, out, err := tools.VMEncrypt(context.Background(), nil, tools.VMEncryptInput{VMID: vmOneID, Password: "pw-42"})
	if err != nil {
		t.Fatal(err)
	}
	if out.DryRun || !strings.Contains(out.Output, "successfully encrypted") {
		t.Fatalf("out = %+v", out)
	}
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" || len(args) != 2 || args[0] != "encrypt" || args[1] != vmOneID {
		t.Fatalf("argv = %q %v", exe, args)
	}
	if runner.lastStdin() != "pw-42\n" {
		t.Fatalf("stdin = %q", runner.lastStdin())
	}

	// Dry run: no password needed, no stdin.
	_, out, err = tools.VMEncrypt(context.Background(), nil, tools.VMEncryptInput{VMID: vmOneID, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.DryRun || !strings.Contains(out.Output, "prerequisites") {
		t.Fatalf("out = %+v", out)
	}
	// Dry run: no password needed. The --dry-run argv proves the Run path
	// (no stdin feeding) — the service test asserts the empty stdin with a
	// fresh fake, since the shared fake retains the previous call's stdin.
	exe, args = runner.lastCall()
	if exe != "/usr/local/bin/prlctl" || len(args) != 3 || args[2] != "--dry-run" {
		t.Fatalf("dry-run argv = %q %v, want encrypt <id> --dry-run", exe, args)
	}

	// Real run without a password: rejected before prlctl.
	before := runner.count()
	if _, _, err := tools.VMEncrypt(context.Background(), nil, tools.VMEncryptInput{VMID: vmOneID}); err == nil || !strings.Contains(err.Error(), "password is required") {
		t.Fatalf("want password required error, got: %v", err)
	}
	if runner.count() != before {
		t.Fatal("prlctl ran for a missing password")
	}
}

func TestDecryptAndChangePasswordFleetProtocol(t *testing.T) {
	runner := encTestRunner()
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	_, out, err := tools.VMDecrypt(context.Background(), nil, tools.VMDecryptInput{VMID: vmOneID, Password: "pw-42"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Output, "successfully decrypted") {
		t.Fatalf("out = %+v", out)
	}
	if runner.lastStdin() != "pw-42\n" {
		t.Fatalf("stdin = %q", runner.lastStdin())
	}

	_, outCP, err := tools.VMChangePassword(context.Background(), nil, tools.VMChangePasswordInput{
		VMID: vmOneID, CurrentPassword: "old", NewPassword: "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outCP.Output, "successfully changed") {
		t.Fatalf("out = %+v", outCP)
	}
	if runner.lastStdin() != "old\nnew\n" {
		t.Fatalf("stdin = %q, want current+new lines", runner.lastStdin())
	}

	// Both passwords are required.
	if _, _, err := tools.VMChangePassword(context.Background(), nil, tools.VMChangePasswordInput{VMID: vmOneID, NewPassword: "new"}); err == nil || !strings.Contains(err.Error(), "current_password is required") {
		t.Fatalf("want current_password required, got: %v", err)
	}
	if _, _, err := tools.VMChangePassword(context.Background(), nil, tools.VMChangePasswordInput{VMID: vmOneID, CurrentPassword: "old"}); err == nil || !strings.Contains(err.Error(), "new_password is required") {
		t.Fatalf("want new_password required, got: %v", err)
	}
}

func TestEncryptionVMModeProtocol(t *testing.T) {
	runner := encTestRunner()
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	// The configured target is used; explicit vm_id is rejected.
	if _, _, err := tools.VMEncrypt(context.Background(), nil, tools.VMEncryptInput{VMID: vmTwoID, Password: "pw"}); err == nil || !strings.Contains(err.Error(), "vm_id must be omitted") {
		t.Fatalf("want vm_id omitted error, got: %v", err)
	}
	_, out, err := tools.VMEncrypt(context.Background(), nil, tools.VMEncryptInput{Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Output, "successfully encrypted") {
		t.Fatalf("out = %+v", out)
	}
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" || args[0] != "encrypt" || args[1] != vmOneID {
		t.Fatalf("argv = %q %v", exe, args)
	}
}
