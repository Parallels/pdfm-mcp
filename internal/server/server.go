// Package server assembles the MCP server and registers tools.
package server

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cjlapao/pdfm-mcp/internal/config"
	"github.com/cjlapao/pdfm-mcp/internal/ocr"
	"github.com/cjlapao/pdfm-mcp/internal/orchestrator"
	"github.com/cjlapao/pdfm-mcp/internal/parallels"
	"github.com/cjlapao/pdfm-mcp/internal/policy"
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

	// The VM allow-list is process-wide state read by the tool handlers;
	// always (re)set it so a previous server's list never leaks.
	vmList := policy.NewVMAllowList(cfg.AllowVMs)
	tools.SetVMAllowList(vmList)

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

		// An invalid hand-built config fails closed: no tools at all.
		pol, err := policy.New(policy.Options{ReadOnly: cfg.ReadOnly, Allow: cfg.AllowTools, Deny: cfg.DenyTools})
		if err != nil {
			logger.Error("invalid tool policy; exposing no tools", "err", err)
			pol, _ = policy.New(policy.Options{Deny: []string{"*"}})
		}
		logPolicy(logger, pol, vmList)

		// Screen text reading: Apple Vision is compiled into the binary
		// (cgo, spec 022), so parallels_vm_observe is always available on
		// macOS.
		observeRec := ocr.NewVision(ocr.WithVisionLogger(logger))

		registerAll(s, logger, pol, mode, toolTable(mode, observeRec), observeRec)

		// Warm the cache at startup: prlctl list -a -i --json (fleet) or
		// prlctl list <id> -a -i --json (VM mode).
		if err := svc.RefreshCache(context.Background()); err != nil {
			logger.Warn("parallels VM cache warm-up failed; tools will retry on demand", "err", err)
		}
	}

	return s
}

// toolEntry is one line of the registration table. Name must equal the
// registered MCP tool name and be present in the policy classification
// table (internal/policy); tests enforce both.
type toolEntry struct {
	Name     string
	Register func(s *mcp.Server)
}

// toolTable lists every tool except parallels_vm_sequence. To add a tool,
// add one line here and a classification in internal/policy.
func toolTable(mode tools.PowerMode, observeRec ocr.Recognizer) []toolEntry {
	table := []toolEntry{
		// Fleet-scoped commands work in both modes.
		{"parallels_list_vms", func(s *mcp.Server) { tools.RegisterParallelsListVms(s) }},
		// Single-VM detail in both modes: the --vm-id target (cached) in
		// VM mode, a specific VM by id or name (live fetch) in fleet mode.
		{"parallels_vm_info", func(s *mcp.Server) { tools.RegisterParallelsVMInfo(s, mode) }},
		{"parallels_vm_start", func(s *mcp.Server) { tools.RegisterParallelsVMStart(s, mode) }},
		{"parallels_vm_resume", func(s *mcp.Server) { tools.RegisterParallelsVMResume(s, mode) }},
		{"parallels_vm_pause", func(s *mcp.Server) { tools.RegisterParallelsVMPause(s, mode) }},
		{"parallels_vm_suspend", func(s *mcp.Server) { tools.RegisterParallelsVMSuspend(s, mode) }},
		{"parallels_vm_restart", func(s *mcp.Server) { tools.RegisterParallelsVMRestart(s, mode) }},
		{"parallels_vm_reset", func(s *mcp.Server) { tools.RegisterParallelsVMReset(s, mode) }},
		{"parallels_vm_reset_uptime", func(s *mcp.Server) { tools.RegisterParallelsVMResetUptime(s, mode) }},
		{"parallels_vm_stop", func(s *mcp.Server) { tools.RegisterParallelsVMStop(s, mode) }},
		// Screen capture (read-only for the VM).
		{"parallels_vm_capture", func(s *mcp.Server) { tools.RegisterParallelsVMCapture(s, mode) }},
		// Guest command execution.
		{"parallels_vm_exec", func(s *mcp.Server) { tools.RegisterParallelsVMExec(s, mode) }},
		// Long-running guest commands as background jobs (spec 018).
		{"parallels_vm_exec_start", func(s *mcp.Server) { tools.RegisterParallelsVMExecStart(s, mode) }},
		{"parallels_vm_exec_result", func(s *mcp.Server) { tools.RegisterParallelsVMExecResult(s, mode) }},
		{"parallels_vm_exec_cancel", func(s *mcp.Server) { tools.RegisterParallelsVMExecCancel(s, mode) }},
		// Keyboard input (single events and typed text).
		{"parallels_vm_send_key", func(s *mcp.Server) { tools.RegisterParallelsVMSendKey(s, mode) }},
		{"parallels_vm_type", func(s *mcp.Server) { tools.RegisterParallelsVMType(s, mode) }},
		// VM deletion (irreversible, confirm-gated).
		{"parallels_vm_delete", func(s *mcp.Server) { tools.RegisterParallelsVMDelete(s, mode) }},
		// VM cloning (source untouched, confirm-free).
		{"parallels_vm_clone", func(s *mcp.Server) { tools.RegisterParallelsVMClone(s, mode) }},
		// VM encryption lifecycle (password via stdin, never logged).
		{"parallels_vm_encrypt", func(s *mcp.Server) { tools.RegisterParallelsVMEncrypt(s, mode) }},
		{"parallels_vm_decrypt", func(s *mcp.Server) { tools.RegisterParallelsVMDecrypt(s, mode) }},
		{"parallels_vm_change_password", func(s *mcp.Server) { tools.RegisterParallelsVMChangePassword(s, mode) }},
		// VM disk-space reclaim (estimate is read-only; real runs mutate
		// the disk file).
		{"parallels_vm_reclaim_disk", func(s *mcp.Server) { tools.RegisterParallelsVMReclaimDisk(s, mode) }},
		// Snapshot management (checkpoints: create / list / delete /
		// revert) in both modes.
		{"parallels_vm_snapshot_create", func(s *mcp.Server) { tools.RegisterParallelsVMSnapshotCreate(s, mode) }},
		{"parallels_vm_snapshot_list", func(s *mcp.Server) { tools.RegisterParallelsVMSnapshotList(s, mode) }},
		{"parallels_vm_snapshot_delete", func(s *mcp.Server) { tools.RegisterParallelsVMSnapshotDelete(s, mode) }},
		{"parallels_vm_snapshot_switch", func(s *mcp.Server) { tools.RegisterParallelsVMSnapshotSwitch(s, mode) }},
	}
	// Screen text reading (Apple Vision built in via cgo, spec 022).
	table = append(table, toolEntry{"parallels_vm_observe", func(s *mcp.Server) {
		tools.RegisterParallelsVMObserve(s, mode, observeRec)
	}})
	return table
}

// registerAll registers the tools the policy allows, then builds the
// sequence step registry from the same allowed set (before the sequence
// description is generated) so a hidden tool is absent from tools/list, from
// the sequence steps and from the generated description. Hidden tool names
// and reasons (names only) are logged at info level.
func registerAll(s *mcp.Server, logger *slog.Logger, pol *policy.Policy, mode tools.PowerMode, table []toolEntry, observeRec ocr.Recognizer) {
	for _, e := range table {
		if d := pol.Decide(e.Name); !d.Allowed {
			logger.Info("tool hidden by policy", "tool", e.Name, "reason", d.Reason)
			continue
		}
		e.Register(s)
	}

	// VM sequence orchestration: ordered steps (tool / wait / gate) with
	// condition checks, retries and image diffing in a single call. The
	// registry is built from the tools above filtered by the same policy,
	// so a sequence can never run a tool the policy hides (in read-only
	// mode: only read tools such as capture).
	reg := orchestrator.NewRegistry()
	tools.RegisterSteps(reg)
	tools.RegisterObserveStep(reg, observeRec)
	for _, name := range reg.Filter(pol.Allows) {
		logger.Info("sequence step hidden by policy", "tool", name)
	}
	d := pol.Decide(policy.SequenceTool)
	switch {
	case !d.Allowed:
		logger.Info("tool hidden by policy", "tool", policy.SequenceTool, "reason", d.Reason)
	case len(reg.Names()) == 0:
		logger.Info("tool hidden by policy", "tool", policy.SequenceTool, "reason", "no allowed steps")
	default:
		tools.RegisterParallelsVMSequence(s, mode, reg, orchestrator.NewRunner(reg, logger))
	}
}

// logPolicy logs the active restrictions (flag names and counts only).
func logPolicy(logger *slog.Logger, pol *policy.Policy, vms *policy.VMAllowList) {
	if pol.Restricted() {
		logger.Info("tool policy active", "read_only", pol.ReadOnly())
	}
	if un := pol.UnmatchedPatterns(); len(un) > 0 {
		logger.Warn("tool policy entries match no known tool", "entries", un)
	}
	if vms.Active() {
		logger.Info("VM allow-list active", "vms", len(vms.Entries()))
	}
}
