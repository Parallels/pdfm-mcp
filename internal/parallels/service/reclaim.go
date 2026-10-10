package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
)

// reclaimEstimate is the shape of prlctl's `--estimate --json` output.
type reclaimEstimate struct {
	DiskSpace struct {
		HardDisks     int64 `json:"hardDisks"`
		Snapshots     int64 `json:"snapshots"`
		Miscellaneous int64 `json:"miscellaneous"`
		Reclaimable   int64 `json:"reclaimable"`
	} `json:"diskSpace"`
}

// ReclaimDisk optimizes the VM's disk file (prlctl reclaim-disk) or,
// when estimate is true, only reports the estimated disk-space
// breakdown. Verified live protocol (Parallels 27.0.1):
//   - --estimate is read-only and works on running and stopped VMs;
//   - a real run works on stopped and (on this version) running VMs;
//   - an encrypted VM requires the current password (one stdin line) in
//     both modes; a real run on an encrypted VM fails with a
//     compress-the-hard-disk error — only the estimate is supported;
//   - an unencrypted VM ignores stdin, so piping a password is harmless;
//   - --json only changes the estimate output; a real run prints
//     "Reclaiming disk, please wait…" regardless.
func (s *service) ReclaimDisk(ctx context.Context, vmRef, password string, estimate, allowShutdown bool) (interfaces.ReclaimDiskResult, error) {
	if !s.enabled {
		return interfaces.ReclaimDiskResult{}, ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return interfaces.ReclaimDiskResult{}, err
	}
	if password != "" {
		if err := ValidatePassword(password); err != nil {
			return interfaces.ReclaimDiskResult{}, err
		}
	}

	args := []string{"reclaim-disk", ref}
	if estimate {
		// --json only affects the estimate output; a real run never
		// prints JSON, so it is never sent there.
		args = append(args, "--estimate", "--json")
	} else if allowShutdown {
		args = append(args, "--allow-shutdown")
	}

	s.logger.Debug("running prlctl reclaim-disk", "vm", ref, "estimate", estimate, "allow_shutdown", allowShutdown, "has_password", password != "")

	// An estimate is a fast metadata read: the runner's default cap is
	// the right bound. A real run scales with the VM's disk size (minutes
	// to hours on large VMs) and the request context carries no deadline
	// (verified), so it gets the long backstop instead of the 30s default.
	runCtx := ctx
	if !estimate {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, longDiskOpTimeout)
		defer cancel()
	}

	var out string
	switch {
	case password != "":
		// Encrypted-VM path: the single password line goes over stdin.
		var stderr string
		var exitCode int
		out, stderr, exitCode, err = s.runner.RunStdin(runCtx, s.prlctlPath, password+"\n", args...)
		if err == nil && exitCode != 0 {
			err = fmt.Errorf("reclaim-disk failed: %s", strings.TrimSpace(stderr))
		}
	case estimate:
		out, err = s.runner.Run(runCtx, s.prlctlPath, args...)
	default:
		out, _, _, err = s.runner.RunCapture(runCtx, s.prlctlPath, args...)
	}
	if err != nil {
		s.logger.Warn("prlctl reclaim-disk failed", "vm", ref, "estimate", estimate, "err", err)
		return interfaces.ReclaimDiskResult{}, err
	}
	s.logger.Info("prlctl reclaim-disk completed", "vm", ref, "estimate", estimate)

	result := interfaces.ReclaimDiskResult{Estimate: estimate, Output: strings.TrimSpace(out)}
	if estimate {
		var parsed reclaimEstimate
		if err := json.Unmarshal([]byte(out), &parsed); err != nil {
			// prlctl succeeded but the shape changed: surface the raw
			// output so the agent still has the numbers.
			s.logger.Warn("unexpected reclaim-disk estimate output", "vm", ref, "err", err)
		} else {
			result.DiskSpace = &interfaces.ReclaimDiskSpace{
				HardDisks:     parsed.DiskSpace.HardDisks,
				Snapshots:     parsed.DiskSpace.Snapshots,
				Miscellaneous: parsed.DiskSpace.Miscellaneous,
				Reclaimable:   parsed.DiskSpace.Reclaimable,
			}
		}
	} else {
		// A real run changed the disk file and may have stopped a VM
		// (--allow-shutdown): the cache is stale.
		s.cache.invalidate()
	}
	return result, nil
}
