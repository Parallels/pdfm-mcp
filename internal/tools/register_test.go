package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoDirectAddTool is the spec 024 guard: every tool must be registered
// through register() so the per-call telemetry wrapper is applied. A direct
// mcp.AddTool call anywhere else in this package (except register.go
// itself) fails the build's test run.
func TestNoDirectAddTool(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read tools dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "register.go" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(b), "mcp.AddTool(") {
			t.Errorf("%s calls mcp.AddTool directly; use register() so the tool is telemetry-instrumented (spec 024)", name)
		}
	}
}
