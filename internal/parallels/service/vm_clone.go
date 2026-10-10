package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// cloneTimeout bounds one prlctl clone invocation. Clone is the only op
// whose duration scales with VM image size: full clones copy all VM
// files, so it gets the longest cap in the module. Linked clones return
// quickly.
const cloneTimeout = 600 * time.Second

// maxClonePathLength bounds the optional --dst directory path.
const maxClonePathLength = 1024

// maxDetachHDDValues are the only accepted --detach-external-hdd values
// ("" means the flag is not passed at all).
var detachHDDValues = map[string]bool{"": true, "yes": true, "no": true}

// Clone creates a copy of the VM identified by vmRef (a VM id or name,
// pre-validated) named req.Name via `prlctl clone <vm> --name <name>
// [options]`. The source VM is never modified. On success the whole VM
// list cache is invalidated because the list gained a VM.
func (s *service) Clone(ctx context.Context, vmRef string, req interfaces.CloneRequest) (string, error) {
	if !s.enabled {
		return "", ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return "", fmt.Errorf("invalid source VM: %w", err)
	}

	// The new name becomes a VM ref, so it gets the same rules.
	name, err := ValidateVMRef(req.Name)
	if err != nil {
		return "", fmt.Errorf("invalid clone name: %w", err)
	}

	dst := strings.TrimSpace(req.Dst)
	if dst != "" {
		if len(dst) > maxClonePathLength {
			return "", fmt.Errorf("dst is longer than %d characters", maxClonePathLength)
		}
		for _, r := range dst {
			if r == 0 || r < 0x20 || r > 0x7e {
				return "", fmt.Errorf("invalid dst: contains non-printable characters")
			}
		}
	}

	snapID := strings.TrimSpace(req.SnapshotID)
	if snapID != "" {
		if len(snapID) > MaxVMRefLength {
			return "", fmt.Errorf("snapshot_id is longer than %d characters", MaxVMRefLength)
		}
		for _, r := range snapID {
			if r == 0 || r < 0x20 || r > 0x7e {
				return "", fmt.Errorf("invalid snapshot_id: contains non-printable characters")
			}
		}
		if !req.Linked {
			return "", fmt.Errorf("snapshot_id requires linked: true (it creates a linked clone based on a snapshot)")
		}
	}

	if !detachHDDValues[req.DetachExternalHDD] {
		return "", fmt.Errorf("detach_external_hdd must be \"yes\" or \"no\" (got %q)", req.DetachExternalHDD)
	}

	args := []string{"clone", ref, "--name", name}
	if req.Template {
		args = append(args, "--template")
	}
	if dst != "" {
		args = append(args, "--dst", dst)
	}
	if req.RegenerateSrcUUID {
		args = append(args, "--regenerate-src-uuid")
	}
	if req.Linked {
		args = append(args, "--linked")
	}
	if snapID != "" {
		args = append(args, "--id", snapID)
	}
	if req.DetachExternalHDD != "" {
		args = append(args, "--detach-external-hdd", req.DetachExternalHDD)
	}

	runCtx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	s.logger.Debug("running prlctl clone", "vm", ref, "name", name)
	out, err := s.runner.Run(runCtx, s.prlctlPath, args...)
	if err != nil {
		s.logger.Warn("prlctl clone failed", "vm", ref, "name", name, "err", err)
		return "", err
	}
	s.logger.Info("prlctl clone completed", "vm", ref, "name", name)

	s.cache.invalidate()
	return strings.TrimSpace(out), nil
}
