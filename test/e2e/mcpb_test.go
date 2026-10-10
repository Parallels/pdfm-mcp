//go:build e2e

package e2e

import (
	"archive/zip"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpbBuildBinary compiles the server into a temp dir.
func mcpbBuildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pdfm-mcp")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/pdfm-mcp").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// mcpbListTools connects with the given command and returns the tool list.
func mcpbListTools(ctx context.Context, t *testing.T, cmd *exec.Cmd) []*mcp.Tool {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-mcpb", Version: "v0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()
	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Tools
}

// mcpbVMIDRequired reports whether the tool's input schema lists vm_id as
// required, which is how fleet mode differs from VM mode (every tool is listed
// in both modes, so tool presence cannot tell them apart).
func mcpbVMIDRequired(t *testing.T, tool *mcp.Tool) bool {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	for _, r := range schema.Required {
		if r == "vm_id" {
			return true
		}
	}
	return false
}

// TestMCPBManifestToolDrift asserts every tool named in the manifest template
// exists in the live fleet-mode tool list. (The reverse is not required: the
// manifest sets tools_generated=true.)
func TestMCPBManifestToolDrift(t *testing.T) {
	if _, err := exec.LookPath("prlctl"); err != nil {
		t.Skip("prlctl not on PATH; Parallels Desktop not installed")
	}
	raw, err := os.ReadFile("../../packaging/mcpb/manifest.json.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("manifest template is not valid JSON: %v", err)
	}
	if len(manifest.Tools) == 0 {
		t.Fatal("manifest lists no tools")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bin := mcpbBuildBinary(t)
	live := map[string]bool{}
	for _, tl := range mcpbListTools(ctx, t, exec.Command(bin)) {
		live[tl.Name] = true
	}
	for _, mt := range manifest.Tools {
		if !live[mt.Name] {
			t.Errorf("manifest tool %q is not exposed by the server", mt.Name)
		}
	}
}

// TestMCPBBundleHandshake builds a bundle with scripts/build-mcpb.sh from the
// locally built binary, unpacks it, and starts server/pdfm-mcp the way a host
// would when the optional vm_id setting is unset: PDFM_MCP_VM_ID arrives as the
// literal unresolved placeholder. The server must start in fleet mode.
//
// The packer step is skipped (MCPB_SKIP_PACK=1) unless PDFM_MCP_E2E_MCPB_PACK=1
// so the test needs no network or Node. Needs bash, jq and python3.
func TestMCPBBundleHandshake(t *testing.T) {
	if _, err := exec.LookPath("prlctl"); err != nil {
		t.Skip("prlctl not on PATH; Parallels Desktop not installed")
	}
	for _, tool := range []string{"bash", "jq", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	bin := mcpbBuildBinary(t)
	work := t.TempDir()

	iconPath := filepath.Join(work, "icon.png")
	f, err := os.Create(iconPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dist := filepath.Join(work, "dist")
	script, err := filepath.Abs("../../scripts/build-mcpb.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "0.0.0-e2e", bin, dist)
	cmd.Env = append(os.Environ(), "MCPB_ALLOW_THIN=1", "MCPB_ICON="+iconPath)
	if os.Getenv("PDFM_MCP_E2E_MCPB_PACK") != "1" {
		cmd.Env = append(cmd.Env, "MCPB_SKIP_PACK=1")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build-mcpb.sh failed: %v\n%s", err, out)
	}

	// Unpack server/pdfm-mcp, checking the executable bit survived.
	zr, err := zip.OpenReader(filepath.Join(dist, "pdfm-mcp-0.0.0-e2e.mcpb"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	unpacked := filepath.Join(work, "unpacked-server")
	found := false
	for _, zf := range zr.File {
		if zf.Name != "server/pdfm-mcp" {
			continue
		}
		found = true
		if zf.Mode()&0o111 == 0 {
			t.Fatalf("server/pdfm-mcp is not executable in the bundle (mode %v)", zf.Mode())
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(unpacked, data, 0o755); err != nil { //nolint:gosec // test binary must be executable
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("server/pdfm-mcp missing from the bundle")
	}

	srv := exec.Command(unpacked)
	srv.Env = append(os.Environ(),
		"PDFM_MCP_VM_ID=${user_config.vm_id}",
		"PDFM_MCP_LOG_LEVEL=info",
	)
	tools := mcpbListTools(ctx, t, srv)

	// Power tools list vm_id as required in fleet mode only (see
	// internal/tools/parallels_power.go). parallels_vm_info is listed in both
	// modes, so it cannot discriminate.
	var info *mcp.Tool
	for _, tl := range tools {
		if tl.Name == "parallels_vm_start" {
			info = tl
		}
	}
	if info == nil {
		var names []string
		for _, tl := range tools {
			names = append(names, tl.Name)
		}
		t.Fatalf("parallels_vm_start not listed: %s", strings.Join(names, ", "))
	}
	// Fleet mode requires vm_id on VM-scoped tools. If the unresolved
	// placeholder had been taken as a VM name, vm_id would be optional.
	if !mcpbVMIDRequired(t, info) {
		t.Fatal("server started in VM mode: unresolved ${user_config.vm_id} was treated as a VM id (config guard missing?)")
	}
}
