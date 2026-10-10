// Command pdfm-mcp runs the pdfm MCP server, speaking JSON-RPC 2.0 over stdio.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/config"
	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/server"
	"github.com/cjlapao/pdfm-mcp/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load(os.Args[1:], os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		return 2
	}
	if cfg.ShowVersion {
		// --version is the one allowed stdout write: it never runs in server mode.
		_, _ = fmt.Fprintln(os.Stdout, version.String()) //nolint:forbidigo
		return 0
	}

	logger, closeLog := newLogger(cfg)
	defer closeLog()
	slog.SetDefault(logger)
	logger.Info("starting", "version", version.Version, "commit", version.Commit)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Kill any background exec jobs (spec 018) when the server exits.
	defer parallels.Shutdown()

	if err := server.New(logger, cfg).Run(ctx, &mcp.StdioTransport{}); err != nil {
		logger.Error("server exited with error", "err", err)
		return 1
	}
	logger.Info("server stopped")
	return 0
}

func newLogger(cfg config.Config) (*slog.Logger, func()) {
	var w io.Writer = os.Stderr
	closer := func() {}
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot open log file:", err)
		} else {
			w = io.MultiWriter(os.Stderr, f)
			closer = func() { _ = f.Close() }
		}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: cfg.LogLevel})), closer
}
