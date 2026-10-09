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
