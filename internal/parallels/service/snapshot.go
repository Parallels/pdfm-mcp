package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
)

// Bounds for snapshot identifiers and user-supplied labels.
const (
	MaxSnapshotIDLength          = 256
	MaxSnapshotNameLength        = 128
	MaxSnapshotDescriptionLength = 512
)

// snapshotIDRe pulls the new snapshot id out of prlctl's create message:
// "The snapshot with id {uuid} has been successfully created."
var snapshotIDRe = regexp.MustCompile(`snapshot with id (\{[0-9a-fA-F-]+\})`)

// ValidateSnapshotID normalizes and validates a snapshot id. prlctl
// accepts the id with or without the {} braces; it is passed through
// verbatim after trimming.
func ValidateSnapshotID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("snapshot_id is empty")
	}
	if len(id) > MaxSnapshotIDLength {
		return "", fmt.Errorf("invalid snapshot id: longer than %d characters", MaxSnapshotIDLength)
	}
	for _, r := range id {
		if r < 0x20 || r > 0x7e {
			return "", errors.New("invalid snapshot id: contains non-printable characters")
		}
	}
	return id, nil
}

// ValidateSnapshotName validates a snapshot name (spaces allowed,
// trimmed; an empty name is omitted from the command entirely, which
// prlctl stores as "").
func ValidateSnapshotName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name is empty")
	}
	if len(name) > MaxSnapshotNameLength {
		return "", fmt.Errorf("invalid name: longer than %d characters", MaxSnapshotNameLength)
	}
	for _, r := range name {
		if r < 0x20 || r > 0x7e {
			return "", errors.New("invalid name: contains non-printable characters")
		}
	}
	return name, nil
}

// ValidateSnapshotDescription validates a snapshot description (may be
// empty).
func ValidateSnapshotDescription(desc string) (string, error) {
	desc = strings.TrimSpace(desc)
	if len(desc) > MaxSnapshotDescriptionLength {
		return "", fmt.Errorf("invalid description: longer than %d characters", MaxSnapshotDescriptionLength)
	}
	for _, r := range desc {
		if r < 0x20 || r > 0x7e {
			return "", errors.New("invalid description: contains non-printable characters")
		}
	}
	return desc, nil
}

// runSnapshotOp runs a prlctl snapshot command, feeding the password as
// a single stdin line when present (encrypted VMs require it;
// unencrypted VMs ignore stdin). fast selects the runner's default cap
// (list) vs the long disk-op backstop (create/delete/switch move data).
// The password never appears in argv, logs, or the returned error.
func (s *service) runSnapshotOp(ctx context.Context, password string, fast bool, args ...string) (string, string, int, error) {
	runCtx := ctx
	if !fast {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, longDiskOpTimeout)
		defer cancel()
	}

	var out, stderr string
	var exitCode int
	var err error
	if password != "" {
		out, stderr, exitCode, err = s.runner.RunStdin(runCtx, s.prlctlPath, password+"\n", args...)
	} else {
		out, stderr, exitCode, err = s.runner.RunCapture(runCtx, s.prlctlPath, args...)
	}
	if err == nil && exitCode != 0 {
		err = fmt.Errorf("prlctl %s failed: %s", args[0], strings.TrimSpace(stderr))
	}
	return out, stderr, exitCode, err
}

// CreateSnapshot takes a VM snapshot (prlctl snapshot). Works on
// running and stopped VMs; the VM's state is preserved. Returns the new
// snapshot id (parsed from prlctl's message; "" when unparseable) and
// prlctl's trimmed stdout.
func (s *service) CreateSnapshot(ctx context.Context, vmRef, name, description, password string) (string, string, error) {
	if !s.enabled {
		return "", "", ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return "", "", err
	}
	if name != "" {
		name, err = ValidateSnapshotName(name)
		if err != nil {
			return "", "", err
		}
	}
	if description != "" {
		description, err = ValidateSnapshotDescription(description)
		if err != nil {
			return "", "", err
		}
	}
	if password != "" {
		if err := ValidatePassword(password); err != nil {
			return "", "", err
		}
	}

	args := []string{"snapshot", ref}
	if name != "" {
		args = append(args, "-n", name)
	}
	if description != "" {
		args = append(args, "-d", description)
	}

	s.logger.Debug("running prlctl snapshot", "vm", ref, "name", name, "has_password", password != "")
	out, _, _, err := s.runSnapshotOp(ctx, password, false, args...)
	if err != nil {
		s.logger.Warn("prlctl snapshot failed", "vm", ref, "err", err)
		return "", "", err
	}
	out = strings.TrimSpace(out)

	id := ""
	if m := snapshotIDRe.FindStringSubmatch(out); m != nil {
		id = m[1]
	}
	s.logger.Info("prlctl snapshot completed", "vm", ref, "snapshot_id", id)
	return id, out, nil
}

// DeleteSnapshot deletes a snapshot (prlctl snapshot-delete). With
// children, all descendants are deleted too (including the current
// one). Deleting the current snapshot reverts the VM to its parent's
// state. The VM's State does not change, so the cache is kept.
func (s *service) DeleteSnapshot(ctx context.Context, vmRef, snapID, password string, children bool) (string, error) {
	if !s.enabled {
		return "", ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return "", err
	}
	id, err := ValidateSnapshotID(snapID)
	if err != nil {
		return "", err
	}
	if password != "" {
		if err := ValidatePassword(password); err != nil {
			return "", err
		}
	}

	args := []string{"snapshot-delete", ref, "-i", id}
	if children {
		args = append(args, "-c")
	}

	s.logger.Debug("running prlctl snapshot-delete", "vm", ref, "snapshot_id", id, "children", children)
	out, _, _, err := s.runSnapshotOp(ctx, password, false, args...)
	if err != nil {
		s.logger.Warn("prlctl snapshot-delete failed", "vm", ref, "snapshot_id", id, "err", err)
		return "", err
	}
	s.logger.Info("prlctl snapshot-delete completed", "vm", ref, "snapshot_id", id)
	return strings.TrimSpace(out), nil
}

// snapshotRaw is one entry of prlctl's snapshot-list -j array: an object
// whose single key is the snapshot id.
type snapshotRaw struct {
	Name    string `json:"name"`
	Date    string `json:"date"`
	State   string `json:"state"`
	Current bool   `json:"current"`
	Parent  string `json:"parent"`
}

// parseSnapshotSingle parses the labeled output of
// `snapshot-list -i <id>` ("ID: … / Name: … / …").
func parseSnapshotSingle(out string) *interfaces.SnapshotInfo {
	info := &interfaces.SnapshotInfo{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "ID:"):
			info.ID = strings.TrimSpace(strings.TrimPrefix(line, "ID:"))
		case strings.HasPrefix(line, "Name:"):
			info.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		case strings.HasPrefix(line, "Date:"):
			info.Date = strings.TrimSpace(strings.TrimPrefix(line, "Date:"))
		case strings.HasPrefix(line, "State:"):
			info.State = strings.TrimSpace(strings.TrimPrefix(line, "State:"))
		case strings.HasPrefix(line, "Description:"):
			info.Description = strings.TrimSpace(strings.TrimPrefix(line, "Description:"))
		}
	}
	return info
}

// ListSnapshots lists a VM's snapshots (prlctl snapshot-list). Empty
// snapshotID: the whole tree as JSON (empty tree → empty slice — prlctl
// prints nothing). Non-empty snapshotID: single-snapshot mode (NotFound
// when the id is unknown: prlctl prints nothing and exits 0 there).
// tree: the ASCII tree. The VM's State never changes: the cache is
// kept.
func (s *service) ListSnapshots(ctx context.Context, vmRef, snapshotID, password string, tree bool) (interfaces.SnapshotListResult, error) {
	if !s.enabled {
		return interfaces.SnapshotListResult{}, ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return interfaces.SnapshotListResult{}, err
	}
	if snapshotID != "" {
		snapshotID, err = ValidateSnapshotID(snapshotID)
		if err != nil {
			return interfaces.SnapshotListResult{}, err
		}
	}
	if password != "" {
		if err := ValidatePassword(password); err != nil {
			return interfaces.SnapshotListResult{}, err
		}
	}

	args := []string{"snapshot-list", ref}
	switch {
	case snapshotID != "":
		// -j is ignored by prlctl in single-snapshot mode (verified).
		args = append(args, "-i", snapshotID)
	case tree:
		args = append(args, "-t")
	default:
		args = append(args, "--json")
	}

	s.logger.Debug("running prlctl snapshot-list", "vm", ref, "snapshot_id", snapshotID, "tree", tree)
	out, _, _, err := s.runSnapshotOp(ctx, password, true, args...)
	if err != nil {
		s.logger.Warn("prlctl snapshot-list failed", "vm", ref, "err", err)
		return interfaces.SnapshotListResult{}, err
	}
	out = strings.TrimSpace(out)

	result := interfaces.SnapshotListResult{}
	switch {
	case snapshotID != "":
		if out == "" {
			// Silent success on an unknown id (verified live).
			result.NotFound = true
			return result, nil
		}
		result.Snapshot = parseSnapshotSingle(out)
	case tree:
		result.Output = out
	default:
		if out == "" {
			return result, nil // empty tree
		}
		// prlctl emits a JSON OBJECT keyed by snapshot id (verified on
		// 27.0.1); an array of single-key objects is accepted too, should a
		// version change the shape.
		var raw map[string]snapshotRaw
		if err := json.Unmarshal([]byte(out), &raw); err != nil {
			var arr []map[string]snapshotRaw
			if err2 := json.Unmarshal([]byte(out), &arr); err2 != nil {
				// Shape unrecognized: keep the raw JSON so the agent still
				// has the data.
				s.logger.Warn("unexpected snapshot-list output shape", "vm", ref, "err", err)
				result.Output = out
				return result, nil
			}
			raw = make(map[string]snapshotRaw, len(arr))
			for _, obj := range arr {
				for id, info := range obj {
					raw[id] = info
				}
			}
		}
		// Map iteration order is random; sort by creation date (then id) so
		// the agent-facing list is deterministic.
		ids := make([]string, 0, len(raw))
		for id := range raw {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			di, dj := raw[ids[i]].Date, raw[ids[j]].Date
			if di != dj {
				return di < dj
			}
			return ids[i] < ids[j]
		})
		for _, id := range ids {
			info := raw[id]
			result.Snapshots = append(result.Snapshots, interfaces.SnapshotInfo{
				ID:      id,
				Name:    info.Name,
				Date:    info.Date,
				State:   info.State,
				Current: info.Current,
				Parent:  info.Parent,
			})
		}
	}
	return result, nil
}

// SwitchSnapshot reverts the VM to a snapshot (prlctl snapshot-
// switch). Without skipResume a "poweron" snapshot starts the VM
// afterwards. The VM's State can change: the cache is invalidated.
func (s *service) SwitchSnapshot(ctx context.Context, vmRef, snapID, password string, skipResume bool) (string, error) {
	if !s.enabled {
		return "", ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return "", err
	}
	id, err := ValidateSnapshotID(snapID)
	if err != nil {
		return "", err
	}
	if password != "" {
		if err := ValidatePassword(password); err != nil {
			return "", err
		}
	}

	args := []string{"snapshot-switch", ref, "-i", id}
	if skipResume {
		args = append(args, "--skip-resume")
	}

	s.logger.Debug("running prlctl snapshot-switch", "vm", ref, "snapshot_id", id, "skip_resume", skipResume)
	out, _, _, err := s.runSnapshotOp(ctx, password, false, args...)
	if err != nil {
		s.logger.Warn("prlctl snapshot-switch failed", "vm", ref, "snapshot_id", id, "err", err)
		return "", err
	}
	s.logger.Info("prlctl snapshot-switch completed", "vm", ref, "snapshot_id", id)

	s.cache.invalidate()
	return strings.TrimSpace(out), nil
}
