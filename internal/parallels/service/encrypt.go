package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxPasswordLength bounds an encryption password. prlctl reads the
// password as one line from stdin; anything longer is a mistake.
const MaxPasswordLength = 256

// longDiskOpTimeout backstops disk operations that scale with the VM's
// data size (a real encrypt or decrypt, a real reclaim). The MCP request
// context carries NO deadline (verified against the live SDK), so without
// this the runner's 30s default cap would abort legitimate runs that take
// minutes to hours on large VMs. Client cancellation still propagates
// through the request context; an op that truly exceeds the cap is killed
// (a killed encrypt was verified to roll back cleanly).
const longDiskOpTimeout = 4 * time.Hour

// ValidatePassword checks an encryption password before it is fed to
// prlctl over stdin. Unlike ValidateVMRef it does NOT trim: spaces inside
// a password (including at the ends) are significant. A whitespace-only
// password is rejected as a mistake.
func ValidatePassword(pw string) error {
	if strings.TrimSpace(pw) == "" {
		return errors.New("password is empty")
	}
	if len(pw) > MaxPasswordLength {
		return fmt.Errorf("invalid password: longer than %d characters", MaxPasswordLength)
	}
	for _, r := range pw {
		if r < 0x20 || r > 0x7e {
			return errors.New("invalid password: contains non-printable characters")
		}
	}
	return nil
}

// runEncryptOrDecrypt runs `prlctl encrypt|decrypt <ref> [--dry-run]`.
// In dry-run mode prlctl checks preconditions and reads no password;
// otherwise the single password line is fed over stdin (prlctl reads it
// from stdin when not attached to a TTY — verified on Parallels 27.0.1).
// The password never appears in argv, logs, or server-built errors.
func (s *service) runEncryptOrDecrypt(ctx context.Context, verb, ref, password string, dryRun bool) (string, error) {
	if !s.enabled {
		return "", ErrNotEnabled
	}
	ref, err := ValidateVMRef(ref)
	if err != nil {
		return "", err
	}
	if !dryRun {
		if err := ValidatePassword(password); err != nil {
			return "", err
		}
	}

	args := []string{verb, ref}
	if dryRun {
		args = append(args, "--dry-run")
	}

	s.logger.Debug("running prlctl "+verb, "vm", ref, "dry_run", dryRun)
	var out string
	if dryRun {
		// Dry-runs are fast precondition checks: the runner's default cap
		// is the right bound.
		out, err = s.runner.Run(ctx, s.prlctlPath, args...)
	} else {
		var stderr string
		var exitCode int
		// Real runs scale with the VM's data size (minutes to hours on
		// large VMs): the request context has no deadline, so use the
		// long backstop instead of the runner's 30s default.
		runCtx, cancel := context.WithTimeout(ctx, longDiskOpTimeout)
		defer cancel()
		out, stderr, exitCode, err = s.runner.RunStdin(runCtx, s.prlctlPath, password+"\n", args...)
		if err == nil && exitCode != 0 {
			err = fmt.Errorf("%s failed: %s", verb, strings.TrimSpace(stderr))
		}
	}
	if err != nil {
		s.logger.Warn("prlctl "+verb+" failed", "vm", ref, "dry_run", dryRun, "err", err)
		return "", err
	}
	s.logger.Info("prlctl "+verb+" completed", "vm", ref, "dry_run", dryRun)

	// A real run flips Security.Encrypted; a dry-run changes nothing, so
	// only real runs make the cache stale.
	if !dryRun {
		s.cache.invalidate()
	}
	return strings.TrimSpace(out), nil
}

// Encrypt encrypts the VM (and all of its data) behind a password, or
// checks the preconditions with dryRun. The VM must be stopped; prlctl
// rejects anything else.
func (s *service) Encrypt(ctx context.Context, vmRef, password string, dryRun bool) (string, error) {
	return s.runEncryptOrDecrypt(ctx, "encrypt", vmRef, password, dryRun)
}

// Decrypt removes the encryption from the VM (the password is required
// and must be the current one), or checks the preconditions with dryRun.
func (s *service) Decrypt(ctx context.Context, vmRef, password string, dryRun bool) (string, error) {
	return s.runEncryptOrDecrypt(ctx, "decrypt", vmRef, password, dryRun)
}

// ChangePasswd changes the encryption password. prlctl reads two lines
// over stdin — the current password, then the new one — with no
// confirmation prompt (verified on Parallels 27.0.1). The VM must be
// encrypted; prlctl rejects otherwise before reading any password.
func (s *service) ChangePasswd(ctx context.Context, vmRef, currentPassword, newPassword string) (string, error) {
	if !s.enabled {
		return "", ErrNotEnabled
	}
	ref, err := ValidateVMRef(vmRef)
	if err != nil {
		return "", err
	}
	if err := ValidatePassword(currentPassword); err != nil {
		return "", fmt.Errorf("current %w", err)
	}
	if err := ValidatePassword(newPassword); err != nil {
		return "", fmt.Errorf("new %w", err)
	}

	s.logger.Debug("running prlctl change-passwd", "vm", ref)
	out, stderr, exitCode, err := s.runner.RunStdin(ctx, s.prlctlPath, currentPassword+"\n"+newPassword+"\n", "change-passwd", ref)
	if err == nil && exitCode != 0 {
		err = fmt.Errorf("change-passwd failed: %s", strings.TrimSpace(stderr))
	}
	if err != nil {
		s.logger.Warn("prlctl change-passwd failed", "vm", ref, "err", err)
		return "", err
	}
	s.logger.Info("prlctl change-passwd completed", "vm", ref)

	s.cache.invalidate()
	return strings.TrimSpace(out), nil
}
