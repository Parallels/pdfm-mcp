package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
)

// powerTimeout bounds a single power command. Guest shutdowns (stop,
// restart, reset) can take a while; reads stay on the shorter default cap.
const powerTimeout = 180 * time.Second

// MaxVMRefLength bounds a VM id/name reference. prlctl ids are UUIDs and
// names are short — anything longer is a mistake or an attack.
const MaxVMRefLength = 256

// ValidateVMRef trims and validates a VM id/name reference that will be
// passed to prlctl as a single argv element (never through a shell).
// The same rule applies at startup (--vm-id) and per call (tool arguments).
func ValidateVMRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("VM id or name is empty")
	}
	if len(ref) > MaxVMRefLength {
		return "", fmt.Errorf("invalid VM id or name: longer than %d characters", MaxVMRefLength)
	}
	for _, r := range ref {
		if r == 0 || r < 0x20 || r > 0x7e {
			return "", errors.New("invalid VM id or name: contains non-printable characters")
		}
	}
	return ref, nil
}

func validPowerOp(op interfaces.PowerOp) bool {
	switch op {
	case interfaces.PowerStart, interfaces.PowerResume, interfaces.PowerPause,
		interfaces.PowerSuspend, interfaces.PowerRestart, interfaces.PowerReset,
		interfaces.PowerResetUptime, interfaces.PowerStop:
		return true
	default:
		return false
	}
}

// Power runs `prlctl <op> <vmRef> [--kill] [--drop-state]` on the module's
// runner. It returns prlctl's stdout, trimmed. On success the whole VM list
// cache is invalidated because the VM's state (and uptime) changed.
func (s *service) Power(ctx context.Context, op interfaces.PowerOp, vmRef string, kill, dropState bool) (string, error) {
	if !s.enabled {
		return "", ErrNotEnabled
	}
	if !validPowerOp(op) {
		return "", fmt.Errorf("unknown power operation %q", op)
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return "", err
	}

	// No pre-validation of flag/state combinations (e.g. --kill on a
	// suspended VM): prlctl is the authority and its error is surfaced.
	args := []string{string(op), ref}
	if op == interfaces.PowerStop {
		if kill {
			args = append(args, "--kill")
		}
		if dropState {
			args = append(args, "--drop-state")
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, powerTimeout)
	defer cancel()

	s.logger.Debug("running prlctl power command", "op", string(op), "vm", ref)
	out, err := s.runner.Run(runCtx, s.prlctlPath, args...)
	if err != nil {
		s.logger.Warn("prlctl power command failed", "op", string(op), "vm", ref, "err", err)
		return "", err
	}
	s.logger.Info("prlctl power command completed", "op", string(op), "vm", ref)

	s.cache.invalidate()
	return strings.TrimSpace(out), nil
}
