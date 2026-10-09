// Package server assembles the MCP server and registers tools.
package server

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/tools"
	"github.com/cjlapao/pdfm-mcp/internal/version"
)

// New builds the MCP server with all tools registered.
func New(logger *slog.Logger) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "pdfm-mcp", Version: version.Version},
		&mcp.ServerOptions{
			Logger:       logger,
			Instructions: "pdfm-mcp server. Use the available tools to interact with pdfm.",
		},
	)

	tools.RegisterGreet(s)
	// Register new tools here.

	return s
}
