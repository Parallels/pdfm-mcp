// Package server assembles the MCP server and registers tools.
package server

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/config"
	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/tools"
	"github.com/cjlapao/pdfm-mcp/internal/version"
)

// New builds the MCP server with all tools registered.
//
// The Parallels Desktop module is initialized here: when prlctl and
// prlsrvctl are both found, the parallels tools are registered (VM-scoped
// tools only when cfg.VMID targets a specific VM) and the VM list cache is
// warmed. A warm-up failure never fails startup; the tools retry on demand.
func New(logger *slog.Logger, cfg config.Config) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "pdfm-mcp", Version: version.Version},
		&mcp.ServerOptions{
			Logger:       logger,
			Instructions: "pdfm-mcp server. Use the available tools to interact with pdfm.",
		},
	)

	tools.RegisterGreet(s)

	if svc, ok := parallels.Initialize(
		parallels.WithVMID(cfg.VMID),
		parallels.WithLogger(logger),
		parallels.WithCacheTTL(cfg.VMCacheTTL),
	); ok {
		// Power tools are available in both modes; the mode only shapes the
		// target contract (fleet: vm_id required, VM: configured target).
		mode := tools.PowerFleet
		if cfg.VMID != "" {
			mode = tools.PowerVM
		}
		// Fleet-scoped commands work in both modes.
		tools.RegisterParallelsListVms(s)
		// VM-scoped commands are only available when a target VM is configured.
		if cfg.VMID != "" {
			tools.RegisterParallelsVMInfo(s)
		}
		tools.RegisterParallelsVMStart(s, mode)
		tools.RegisterParallelsVMResume(s, mode)
		tools.RegisterParallelsVMPause(s, mode)
		tools.RegisterParallelsVMSuspend(s, mode)
		tools.RegisterParallelsVMRestart(s, mode)
		tools.RegisterParallelsVMReset(s, mode)
		tools.RegisterParallelsVMResetUptime(s, mode)
		tools.RegisterParallelsVMStop(s, mode)

		// Warm the cache at startup: prlctl list -a -i --json (fleet) or
		// prlctl list <id> -a -i --json (VM mode).
		if err := svc.RefreshCache(context.Background()); err != nil {
			logger.Warn("parallels VM cache warm-up failed; tools will retry on demand", "err", err)
		}
	}

	return s
}
