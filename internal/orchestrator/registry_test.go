package orchestrator

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type demoIn struct {
	Name string `json:"name,omitempty"`
}

type demoOut struct {
	Greeting string `json:"greeting"`
}

func demoHandler(_ context.Context, _ *mcp.CallToolRequest, in demoIn) (*mcp.CallToolResult, demoOut, error) {
	return nil, demoOut{Greeting: "hi " + in.Name}, nil
}

func TestRegistryGetNamesAll(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Action{Name: "a", Description: "A"})
	reg.Register(Action{Name: "b", Description: "B"})

	if _, ok := reg.Get("a"); !ok {
		t.Fatal("Get(a) missing")
	}
	if _, ok := reg.Get("nope"); ok {
		t.Fatal("Get(nope) should miss")
	}
	names := reg.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("Names = %v", names)
	}
	all := reg.All()
	if len(all) != 2 || all[1].Name != "b" {
		t.Fatalf("All = %v", all)
	}
}

func TestRegistryPanics(t *testing.T) {
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("duplicate name must panic")
			}
		}()
		reg := NewRegistry()
		reg.Register(Action{Name: "x"})
		reg.Register(Action{Name: "x"})
	}()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("empty name must panic")
			}
		}()
		NewRegistry().Register(Action{})
	}()
}

func TestFromHandlerBasic(t *testing.T) {
	a := FromHandler[demoIn, demoOut]("demo", "d", true, nil, demoHandler)
	out, err := a.Run(context.Background(), []byte(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Data["greeting"] != "hi x" {
		t.Fatalf("Data = %v", out.Data)
	}
}

func TestFromHandlerStrictArgs(t *testing.T) {
	a := FromHandler[demoIn, demoOut]("demo", "d", true, nil, demoHandler)
	// Unknown field: rejected at ValidateArgs (validation time) and at Run.
	if err := a.ValidateArgs([]byte(`{"name":"x","typo":1}`)); err == nil || !strings.Contains(err.Error(), "invalid arguments for demo") {
		t.Fatalf("ValidateArgs err = %v", err)
	}
	if _, err := a.Run(context.Background(), []byte(`{"name":"x","typo":1}`)); err == nil {
		t.Fatal("Run must reject unknown fields")
	}
	// Empty and null args are the zero value.
	out, err := a.Run(context.Background(), nil)
	if err != nil || out.Data["greeting"] != "hi " {
		t.Fatalf("empty args: out=%v err=%v", out.Data, err)
	}
	if _, err := a.Run(context.Background(), []byte("null")); err != nil {
		t.Fatalf("null args: %v", err)
	}
}

func TestFromHandlerCapturesImages(t *testing.T) {
	img := []byte("png-bytes")
	h := func(_ context.Context, _ *mcp.CallToolRequest, _ demoIn) (*mcp.CallToolResult, demoOut, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "x"},
				&mcp.ImageContent{Data: img, MIMEType: "image/png"},
			},
		}, demoOut{Greeting: "g"}, nil
	}
	a := FromHandler[demoIn, demoOut]("capture", "c", true, nil, h)
	out, err := a.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Images) != 1 || string(out.Images[0].Data) != "png-bytes" || out.Images[0].MIMEType != "image/png" {
		t.Fatalf("Images = %v", out.Images)
	}
	if out.Data["greeting"] != "g" {
		t.Fatalf("Data = %v", out.Data)
	}
}

func TestFromHandlerErrorPassthrough(t *testing.T) {
	h := func(_ context.Context, _ *mcp.CallToolRequest, _ demoIn) (*mcp.CallToolResult, demoOut, error) {
		return nil, demoOut{}, context.Canceled
	}
	a := FromHandler[demoIn, demoOut]("demo", "d", true, nil, h)
	if _, err := a.Run(context.Background(), []byte("{}")); err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// mkPlanCheck builds a planCheck from a spec (compiling the regex).
func mkPlanCheck(t *testing.T, c Check) planCheck {
	t.Helper()
	pc := planCheck{Spec: c}
	if c.Regex != "" {
		var err error
		pc.Compiled, err = regexp.Compile(c.Regex)
		if err != nil {
			t.Fatal(err)
		}
	}
	return pc
}

func TestRegistryRemoveAndFilter(t *testing.T) {
	reg := NewRegistry()
	for _, n := range []string{"a", "b", "c", "d"} {
		reg.Register(Action{Name: n})
	}
	if reg.Remove("nope") {
		t.Fatal("Remove(nope) should report false")
	}
	if !reg.Remove("b") {
		t.Fatal("Remove(b) should report true")
	}
	if _, ok := reg.Get("b"); ok {
		t.Fatal("b still registered")
	}
	removed := reg.Filter(func(n string) bool { return n != "c" })
	if len(removed) != 1 || removed[0] != "c" {
		t.Fatalf("removed = %v, want [c]", removed)
	}
	names := reg.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "d" {
		t.Fatalf("Names = %v, want [a d] in order", names)
	}
	if len(reg.All()) != 2 {
		t.Fatalf("All = %d, want 2", len(reg.All()))
	}
	// A removed name can be registered again.
	reg.Register(Action{Name: "b"})
	if _, ok := reg.Get("b"); !ok {
		t.Fatal("b not re-registered")
	}
}
