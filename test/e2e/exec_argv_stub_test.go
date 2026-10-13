//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestExecArgvSplitOnTheWire is the hermetic wire-level regression guard for
// spec 029. It drives the REAL binary over a real stdio MCP connection against
// a stub prlctl (a bash script on PATH that logs the exact argv it receives;
// no Parallels install needed) and asserts that the guest command line reaches
// prlctl as separate program + argument elements — the exact argv shape the
// Windows exit-2 field report required. A quoted payload must arrive as ONE
// element, a Windows path as ONE element, elevated drops the identity flag,
// and an unterminated quote must be a tool error that never reaches prlctl.
func TestExecArgvSplitOnTheWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	stubDir := t.TempDir()
	prlctl := filepath.Join(stubDir, "prlctl")
	script := `#!/bin/bash
LOG="` + filepath.Join(stubDir, "argv.log") + `"
{
  printf 'CALL'
  for a in "$@"; do printf '\x1f%s' "$a"; done
  printf '\x1e\n'
} >> "$LOG"
case "${1:-}" in
  exec) printf 'stub-exec-output\n' ;;
esac
exit 0
`
	if err := os.WriteFile(prlctl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stubDir, "prlsrvctl"), []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	bin := filepath.Join(t.TempDir(), "pdfm-mcp-argv")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/pdfm-mcp").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	session, err := mcp.NewClient(&mcp.Implementation{Name: "argv-test", Version: "v0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	cases := []struct {
		name     string
		vmID     string
		command  string
		elevated bool
		wantTail []string // expected argv after "exec <vm>"
	}{
		{"windows multi-arg", "windows11-stub", "cmd /c ver", false, []string{"--current-user", "cmd", "/c", "ver"}},
		{"powershell env var", "windows11-stub", `powershell -NoProfile -Command "$env:USERNAME"`, false, []string{"--current-user", "powershell", "-NoProfile", "-Command", "$env:USERNAME"}},
		{"quoted payload one element", "linux-stub", `sh -c "echo a && echo b"`, false, []string{"--current-user", "sh", "-c", "echo a && echo b"}},
		{"windows path single element", "windows11-stub", `C:\Windows\System32\whoami.exe`, false, []string{"--current-user", `C:\Windows\System32\whoami.exe`}},
		{"elevated no identity flag", "windows11-stub", "cmd /c ver", true, []string{"cmd", "/c", "ver"}},
	}

	logPath := filepath.Join(stubDir, "argv.log")
	calls := func() [][]string {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		var out [][]string
		for _, rec := range strings.Split(strings.TrimSuffix(string(data), "\x1e\n"), "\x1e\n") {
			rec = strings.TrimPrefix(rec, "CALL\x1f")
			out = append(out, strings.Split(rec, "\x1f"))
		}
		return out
	}

	for i, tc := range cases {
		args := map[string]any{"vm_id": tc.vmID, "command": tc.command}
		if tc.elevated {
			args["elevated"] = true
		}
		before := calls()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_exec", Arguments: args})
		if err != nil {
			t.Fatalf("%s: call: %v", tc.name, err)
		}
		if res.IsError {
			t.Fatalf("%s: tool error: %+v", tc.name, res.Content)
		}
		after := calls()
		if len(after) != len(before)+1 {
			t.Fatalf("%s: expected exactly one prlctl exec call, before=%d after=%d", tc.name, len(before), len(after))
		}
		got := after[len(after)-1]
		want := append([]string{"exec", tc.vmID}, tc.wantTail...)
		if strings.Join(got, "¦") != strings.Join(want, "¦") {
			t.Fatalf("%s (call %d): argv = %q, want %q", tc.name, i, got, want)
		}
		sc, _ := res.StructuredContent.(map[string]any)
		if sc["exit_code"] != float64(0) || !strings.Contains(sc["stdout"].(string), "stub-exec-output") {
			t.Fatalf("%s: structured = %v", tc.name, sc)
		}
	}

	// Unterminated quote: tool error, and NO prlctl call.
	before := calls()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "parallels_vm_exec",
		Arguments: map[string]any{
			"vm_id":   "windows11-stub",
			"command": `echo "unclosed`,
		},
	})
	if err != nil {
		t.Fatalf("unterminated quote: call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("unterminated quote must be a tool error: %+v", res.Content)
	}
	if after := calls(); len(after) != len(before) {
		t.Fatalf("unterminated quote must not reach prlctl: %d -> %d calls", len(before), len(after))
	}
}
