package service

import (
	"context"
	"strings"
	"time"
)

// deleteTimeout bounds one prlctl delete invocation. Deleting large VM
// images is file-IO bound, so it gets the longer (exec-class) cap rather
// than the power cap.
const deleteTimeout = 300 * time.Second

// Delete permanently removes the VM identified by vmRef (a VM id or name,
// pre-validated) and all its files from the host via `prlctl delete <vm>`.
// The operation is irreversible. On success the whole VM list cache is
// invalidated because the list itself changed.
func (s *service) Delete(ctx context.Context, vmRef string) (string, error) {
	if !s.enabled {
		return "", ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return "", err
	}

	args := []string{"delete", ref}

	runCtx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	s.logger.Debug("running prlctl delete", "vm", ref)
	out, err := s.runner.Run(runCtx, s.prlctlPath, args...)
	if err != nil {
		s.logger.Warn("prlctl delete failed", "vm", ref, "err", err)
		return "", err
	}
	s.logger.Info("prlctl delete completed", "vm", ref)

	s.cache.invalidate()
	return strings.TrimSpace(out), nil
}
