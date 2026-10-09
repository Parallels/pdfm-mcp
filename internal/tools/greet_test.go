package tools_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/tools"
)

func TestGreetHandler(t *testing.T) {
	cases := []struct {
		name    string
		in      tools.GreetInput
		want    string
		wantErr bool
	}{
		{"normal", tools.GreetInput{Name: "Carlos"}, "Hello, Carlos!", false},
		{"shout", tools.GreetInput{Name: "Carlos", Shout: true}, "HELLO, CARLOS!", false},
		{"trims", tools.GreetInput{Name: "  Carlos "}, "Hello, Carlos!", false},
		{"empty", tools.GreetInput{Name: "   "}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := tools.Greet(context.Background(), nil, tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if out.Greeting != tc.want {
				t.Fatalf("greeting = %q, want %q", out.Greeting, tc.want)
			}
		})
	}
}

func TestGreetOverInMemoryTransport(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	clientT, serverT := mcp.NewInMemoryTransports()
	srvSession, err := server.New(logger).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := srvSession.Close(); err != nil {
			t.Logf("server session close: %v", err)
		}
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("client session close: %v", err)
		}
	}()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "Carlos"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["greeting"] != "Hello, Carlos!" {
		t.Fatalf("structured content = %v", res.StructuredContent)
	}
}
