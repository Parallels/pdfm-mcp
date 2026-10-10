package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/telemetry"
)

// register adds a tool with per-call telemetry instrumentation (spec 024).
// Every tool registration in this package must go through it so no tool can
// bypass telemetry; a grep-guard test (TestNoDirectAddTool) enforces that
// mcp.AddTool does not appear anywhere else in this package.
func register[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(s, t, telemetry.Instrument(t.Name, h))
}
