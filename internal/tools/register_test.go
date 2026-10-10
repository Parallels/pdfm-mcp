package tools

import (
	"os"
	"path/filepath"
	"regexp"
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

// TestVMIDSchemaContract is the spec 025 guard: every INPUT vm_id schema
// (the optional argument of a VM-scoped tool) must carry the shared
// contract text so AI clients see where VM names come from and that names
// with spaces need no quoting. Output fields (echoing the target back)
// keep their own wording and are not matched: input tags use
// `vm_id,omitempty`.
func TestVMIDSchemaContract(t *testing.T) {
	const want = "parallels_list_vms"
	const wantQuotes = "no quotes"
	tagRe := regexp.MustCompile(`json:"vm_id,omitempty" jsonschema:"([^"]*)"`)
	for _, dir := range []string{".", "../orchestrator"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s/%s: %v", dir, name, err)
			}
			src := string(b)
			for _, m := range tagRe.FindAllStringSubmatch(src, -1) {
				desc := m[1]
				if !strings.Contains(desc, want) || !strings.Contains(desc, wantQuotes) {
					t.Errorf("%s/%s: input vm_id schema %q must state that the name comes from %s and needs no quotes (spec 025)", dir, name, desc, want)
				}
			}
		}
	}
}
