package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// setTimeout bounds a single prlctl set command. prlctl set applies
// configuration and returns immediately (no data processing).
const setTimeout = 60 * time.Second

// Bounds and closed enum sets for the curated prlctl set fields (spec 027).
const (
	minSetCPUs       = 1
	maxSetCPUs       = 128
	minSetMemMB      = 16
	maxSetMemMB      = 131072
	minSetVideoMB    = 2
	maxSetVideoMB    = 8192
	maxSetAutostart  = 86400
	maxSetNameLength = 256
	maxSetPathLength = 1024
	maxSetDescLength = 256
	minSetSgInterval = 1
	maxSetSgInterval = 2592000 // 30 days
	maxSetSgMax      = 32
)

var (
	setThreeDAccelerate = map[string]bool{"off": true, "highest": true, "dx9": true}
	setAutostart        = map[string]bool{"off": true, "open-window": true, "start-app": true, "start-host": true, "user-login": true}
	setAutostop         = map[string]bool{"stop": true, "suspend": true, "shutdown": true}
	setStartupView      = map[string]bool{"same": true, "window": true, "coherence": true, "fullscreen": true, "modality": true, "headless": true}
	setOnShutdown       = map[string]bool{"window": true, "close": true, "quit": true}
	setOnWindowClose    = map[string]bool{"suspend": true, "shutdown": true, "stop": true, "ask": true, "keep-running": true}
	setSharedClipboard  = map[string]bool{"on": true, "host-to-guest": true, "guest-to-host": true, "off": true}
	setTPM              = map[string]bool{"on": true, "off": true, "crb": true, "tis": true}
	setFolderMode       = map[string]bool{"ro": true, "rw": true}
)

// validSetPrintable rejects anything outside printable ASCII — the same
// rule as ValidateVMRef, for values passed to prlctl as single argv
// elements (never through a shell).
func validSetPrintable(s string) error {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return errors.New("contains non-printable characters")
		}
	}
	return nil
}

// validSetEnum checks a closed enum value.
func validSetEnum(field, value string, allowed map[string]bool) error {
	if !allowed[value] {
		return fmt.Errorf("invalid %s value %q", field, value)
	}
	return nil
}

// validSetInt checks an integer range.
func validSetInt(field string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("invalid %s: %d (allowed %d-%d)", field, v, lo, hi)
	}
	return nil
}

// validSetName trims, tolerates one surrounding quote pair (spec 025),
// and bounds a printable-ASCII name/description value.
func validSetName(field, value string, maxLength int) (string, error) {
	v := UnquoteVMRef(value)
	if v == "" {
		return "", fmt.Errorf("%s is empty", field)
	}
	if len(v) > maxLength {
		return "", fmt.Errorf("%s is longer than %d characters", field, maxLength)
	}
	if err := validSetPrintable(v); err != nil {
		return "", fmt.Errorf("invalid %s: %s", field, err)
	}
	return v, nil
}

// checkSharedFolder validates a shared-folder add/set payload and returns
// the cleaned values. field is the error prefix ("shared_folder_add" or
// "shared_folder_set").
func checkSharedFolder(f *interfaces.VMSharedFolderAdd, field string) (name, path, desc string, err error) {
	name, err = validSetName(field+".name", f.Name, maxSetNameLength)
	if err != nil {
		return "", "", "", err
	}
	path = strings.TrimSpace(f.Path)
	if path == "" {
		return "", "", "", fmt.Errorf("%s.path is empty", field)
	}
	if len(path) > maxSetPathLength {
		return "", "", "", fmt.Errorf("%s.path is longer than %d characters", field, maxSetPathLength)
	}
	if err := validSetPrintable(path); err != nil {
		return "", "", "", fmt.Errorf("invalid %s.path: %s", field, err)
	}
	if f.Mode != "" {
		if err := validSetEnum(field+".mode", f.Mode, setFolderMode); err != nil {
			return "", "", "", err
		}
	}
	desc = strings.TrimSpace(f.Description)
	if len(desc) > maxSetDescLength {
		return "", "", "", fmt.Errorf("%s.description is longer than %d characters", field, maxSetDescLength)
	}
	if err := validSetPrintable(desc); err != nil {
		return "", "", "", fmt.Errorf("invalid %s.description: %s", field, err)
	}
	return name, path, desc, nil
}

// buildSetArgs maps opts to the prlctl set argv (after "set <ref>") plus
// a human-readable applied list, in a fixed field order. Every provided
// value is re-validated here: tool arguments are untrusted even though
// the tool schema already bounds them.
func buildSetArgs(ref string, o interfaces.VMSetOptions) (args, applied []string, err error) {
	args = []string{"set", ref}

	boolFlag := func(field, flag string, b *bool) error {
		v := "off"
		if *b {
			v = "on"
		}
		args = append(args, flag, v)
		applied = append(applied, field+"="+v)
		return nil
	}

	if o.Name != nil {
		v, err := validSetName("name", *o.Name, maxSetNameLength)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--name", v)
		applied = append(applied, "name="+v)
	}
	if o.Description != nil {
		v, err := validSetName("description", *o.Description, maxSetDescLength)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--description", v)
		applied = append(applied, "description="+v)
	}
	if o.CPUs != nil {
		if err := validSetInt("cpus", *o.CPUs, minSetCPUs, maxSetCPUs); err != nil {
			return nil, nil, err
		}
		args = append(args, "--cpus", strconv.Itoa(*o.CPUs))
		applied = append(applied, "cpus="+strconv.Itoa(*o.CPUs))
	}
	if o.MemSizeMB != nil {
		if err := validSetInt("memsize_mb", *o.MemSizeMB, minSetMemMB, maxSetMemMB); err != nil {
			return nil, nil, err
		}
		args = append(args, "--memsize", strconv.Itoa(*o.MemSizeMB))
		applied = append(applied, "memsize_mb="+strconv.Itoa(*o.MemSizeMB))
	}
	if o.VideoSizeMB != nil {
		if err := validSetInt("videosize_mb", *o.VideoSizeMB, minSetVideoMB, maxSetVideoMB); err != nil {
			return nil, nil, err
		}
		args = append(args, "--videosize", strconv.Itoa(*o.VideoSizeMB))
		applied = append(applied, "videosize_mb="+strconv.Itoa(*o.VideoSizeMB))
	}
	if o.ThreeDAccelerate != nil {
		if err := validSetEnum("three_d_accelerate", *o.ThreeDAccelerate, setThreeDAccelerate); err != nil {
			return nil, nil, err
		}
		args = append(args, "--3d-accelerate", *o.ThreeDAccelerate)
		applied = append(applied, "three_d_accelerate="+*o.ThreeDAccelerate)
	}
	if o.VerticalSync != nil {
		if err := boolFlag("vertical_sync", "--vertical-sync", o.VerticalSync); err != nil {
			return nil, nil, err
		}
	}
	if o.HighResolution != nil {
		if err := boolFlag("high_resolution", "--high-resolution", o.HighResolution); err != nil {
			return nil, nil, err
		}
	}
	if o.EFIBoot != nil {
		if err := boolFlag("efi_boot", "--efi-boot", o.EFIBoot); err != nil {
			return nil, nil, err
		}
	}
	if o.SelectBootDevice != nil {
		if err := boolFlag("select_boot_device", "--select-boot-device", o.SelectBootDevice); err != nil {
			return nil, nil, err
		}
	}
	if o.Autostart != nil {
		if err := validSetEnum("autostart", *o.Autostart, setAutostart); err != nil {
			return nil, nil, err
		}
		args = append(args, "--autostart", *o.Autostart)
		applied = append(applied, "autostart="+*o.Autostart)
	}
	if o.AutostartDelaySecs != nil {
		if err := validSetInt("autostart_delay_s", *o.AutostartDelaySecs, 0, maxSetAutostart); err != nil {
			return nil, nil, err
		}
		args = append(args, "--autostart-delay", strconv.Itoa(*o.AutostartDelaySecs))
		applied = append(applied, "autostart_delay_s="+strconv.Itoa(*o.AutostartDelaySecs))
	}
	if o.Autostop != nil {
		if err := validSetEnum("autostop", *o.Autostop, setAutostop); err != nil {
			return nil, nil, err
		}
		args = append(args, "--autostop", *o.Autostop)
		applied = append(applied, "autostop="+*o.Autostop)
	}
	if o.StartupView != nil {
		if err := validSetEnum("startup_view", *o.StartupView, setStartupView); err != nil {
			return nil, nil, err
		}
		args = append(args, "--startup-view", *o.StartupView)
		applied = append(applied, "startup_view="+*o.StartupView)
	}
	if o.OnShutdown != nil {
		if err := validSetEnum("on_shutdown", *o.OnShutdown, setOnShutdown); err != nil {
			return nil, nil, err
		}
		args = append(args, "--on-shutdown", *o.OnShutdown)
		applied = append(applied, "on_shutdown="+*o.OnShutdown)
	}
	if o.OnWindowClose != nil {
		if err := validSetEnum("on_window_close", *o.OnWindowClose, setOnWindowClose); err != nil {
			return nil, nil, err
		}
		args = append(args, "--on-window-close", *o.OnWindowClose)
		applied = append(applied, "on_window_close="+*o.OnWindowClose)
	}
	if o.FasterVM != nil {
		if err := boolFlag("faster_vm", "--faster-vm", o.FasterVM); err != nil {
			return nil, nil, err
		}
	}
	if o.AutoCompress != nil {
		if err := boolFlag("auto_compress", "--auto-compress", o.AutoCompress); err != nil {
			return nil, nil, err
		}
	}
	if o.NestedVirt != nil {
		if err := boolFlag("nested_virt", "--nested-virt", o.NestedVirt); err != nil {
			return nil, nil, err
		}
	}
	if o.SmartMount != nil {
		if err := boolFlag("smart_mount", "--smart-mount", o.SmartMount); err != nil {
			return nil, nil, err
		}
	}
	if o.SharedClipboard != nil {
		if err := validSetEnum("shared_clipboard", *o.SharedClipboard, setSharedClipboard); err != nil {
			return nil, nil, err
		}
		args = append(args, "--shared-clipboard", *o.SharedClipboard)
		applied = append(applied, "shared_clipboard="+*o.SharedClipboard)
	}
	if o.ShfHost != nil {
		if err := boolFlag("shf_host", "--shf-host", o.ShfHost); err != nil {
			return nil, nil, err
		}
	}
	folderShare := func(flag, field string, f *interfaces.VMSharedFolderAdd) error {
		name, path, desc, err := checkSharedFolder(f, field)
		if err != nil {
			return err
		}
		args = append(args, flag, name, "--path", path)
		if f.Mode != "" {
			args = append(args, "--mode", f.Mode)
		}
		if desc != "" {
			args = append(args, "--shf-description", desc)
		}
		if f.Enable != nil && !*f.Enable {
			args = append(args, "--disable")
		} else {
			args = append(args, "--enable")
		}
		applied = append(applied, fmt.Sprintf("%s=%s path=%s", field, name, path))
		return nil
	}
	if o.FolderAdd != nil {
		if err := folderShare("--shf-host-add", "shared_folder_add", o.FolderAdd); err != nil {
			return nil, nil, err
		}
	}
	if o.FolderRemove != nil {
		name, err := validSetName("shared_folder_remove.name", *o.FolderRemove, maxSetNameLength)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, "--shf-host-del", name)
		applied = append(applied, "shared_folder_remove="+name)
	}
	if o.FolderSet != nil {
		if err := folderShare("--shf-host-set", "shared_folder_set", o.FolderSet); err != nil {
			return nil, nil, err
		}
	}
	if o.TPM != nil {
		if err := validSetEnum("tpm", *o.TPM, setTPM); err != nil {
			return nil, nil, err
		}
		args = append(args, "--tpm", *o.TPM)
		applied = append(applied, "tpm="+*o.TPM)
	}
	if o.TimeSync != nil {
		if err := boolFlag("time_sync", "--time-sync", o.TimeSync); err != nil {
			return nil, nil, err
		}
	}
	if o.LockOnSuspend != nil {
		if err := boolFlag("lock_on_suspend", "--lock-on-suspend", o.LockOnSuspend); err != nil {
			return nil, nil, err
		}
	}
	if o.SmartGuard != nil {
		if err := boolFlag("smart_guard", "--smart-guard", o.SmartGuard); err != nil {
			return nil, nil, err
		}
	}
	if o.SgNotifyBeforeCreate != nil {
		if err := boolFlag("sg_notify_before_create", "--sg-notify-before-create", o.SgNotifyBeforeCreate); err != nil {
			return nil, nil, err
		}
	}
	if o.SgIntervalSecs != nil {
		if err := validSetInt("sg_interval_s", *o.SgIntervalSecs, minSetSgInterval, maxSetSgInterval); err != nil {
			return nil, nil, err
		}
		args = append(args, "--sg-interval", strconv.Itoa(*o.SgIntervalSecs))
		applied = append(applied, "sg_interval_s="+strconv.Itoa(*o.SgIntervalSecs))
	}
	if o.SgMaxSnapshots != nil {
		if err := validSetInt("sg_max_snapshots", *o.SgMaxSnapshots, 1, maxSetSgMax); err != nil {
			return nil, nil, err
		}
		args = append(args, "--sg-max-snapshots", strconv.Itoa(*o.SgMaxSnapshots))
		applied = append(applied, "sg_max_snapshots="+strconv.Itoa(*o.SgMaxSnapshots))
	}
	if o.IsolateVM != nil {
		if err := boolFlag("isolate_vm", "--isolate-vm", o.IsolateVM); err != nil {
			return nil, nil, err
		}
	}

	if len(applied) == 0 {
		return nil, nil, errors.New("no configuration fields provided (set at least one)")
	}
	return args, applied, nil
}

// VMSet runs `prlctl set <vmRef> <flags>` for the curated option set in
// opts. prlctl is the authority on state/flag preconditions (e.g. hardware
// fields on a running VM) — its error is returned verbatim. On success
// the whole VM list cache is invalidated because cached per-VM fields
// are stale.
func (s *service) VMSet(ctx context.Context, vmRef string, opts interfaces.VMSetOptions) (interfaces.VMSetResult, error) {
	if !s.enabled {
		return interfaces.VMSetResult{}, ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return interfaces.VMSetResult{}, err
	}
	args, applied, err := buildSetArgs(ref, opts)
	if err != nil {
		return interfaces.VMSetResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, setTimeout)
	defer cancel()

	s.logger.Debug("running prlctl set", "vm", ref, "applied", applied)
	out, err := s.runner.Run(runCtx, s.prlctlPath, args...)
	if err != nil {
		s.logger.Warn("prlctl set failed", "vm", ref, "err", err)
		return interfaces.VMSetResult{}, err
	}
	s.logger.Info("prlctl set completed", "vm", ref, "applied", applied)

	s.cache.invalidate()
	return interfaces.VMSetResult{Output: strings.TrimSpace(out), Applied: applied}, nil
}
