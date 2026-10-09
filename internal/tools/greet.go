// Package tools contains MCP tool implementations. One file per tool.
package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GreetInput is the argument schema for the greet tool.
// The jsonschema tag becomes the argument description shown to the model.
type GreetInput struct {
	Name  string `json:"name" jsonschema:"the name of the person to greet"`
	Shout bool   `json:"shout,omitempty" jsonschema:"return the greeting in upper case"`
}

// GreetOutput is the structured result of the greet tool.
type GreetOutput struct {
	Greeting string `json:"greeting" jsonschema:"the greeting message"`
}

// Greet is the handler. Keep handlers thin: validate, call domain code, map result.
func Greet(_ context.Context, _ *mcp.CallToolRequest, in GreetInput) (*mcp.CallToolResult, GreetOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		// Returning an error yields a tool result with isError=true.
		return nil, GreetOutput{}, errEmptyName
	}

	msg := "Hello, " + name + "!"
	if in.Shout {
		msg = strings.ToUpper(msg)
	}
	// Returning a nil result lets the SDK populate both structured content
	// and a text content block from the output value.
	return nil, GreetOutput{Greeting: msg}, nil
}

// RegisterGreet adds the greet tool to the server.
func RegisterGreet(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "greet",
		Description: "Greets a person by name. Reference tool used to verify the MCP plumbing.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, Greet)
}
