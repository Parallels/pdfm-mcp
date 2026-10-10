package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

const (
	// execTimeout bounds one guest command. Long jobs should run in the
	// background inside the guest instead.
	execTimeout = 300 * time.Second
	// maxCommandLength bounds the command line.
	maxCommandLength = 4096
	// maxExecStdout / maxExecStderr cap the tool result (the runner has a
	// separate 10 MB hard cap for memory protection).
	maxExecStdout = 1 << 20   // 1 MB
	maxExecStderr = 256 << 10 // 256 KB
)

// validateCommand bounds the guest command line. The command is a single
// argv element for prlctl (never a host shell), so the only host-side risks
// are NUL bytes (impossible in argv) and unbounded size.
func validateCommand(cmd string) (string, error) {
	if strings.TrimSpace(cmd) == "" {
		return "", errors.New("command must not be empty")
	}
	if strings.ContainsRune(cmd, '\x00') {
		return "", errors.New("command must not contain NUL bytes")
	}
	if len(cmd) > maxCommandLength {
		return "", fmt.Errorf("command is %d bytes, above the %d byte limit", len(cmd), maxCommandLength)
	}
	return cmd, nil
}

// truncateRuneSafe cuts s to at most n bytes, backing off to a UTF-8 rune
// boundary. It reports whether anything was cut.
func truncateRuneSafe(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// buildExecArgs validates the exec inputs and builds the prlctl argv:
// exec <vm> <identity> [--resolve-paths] <command>. Options come before
// the command (prlctl exec --help); the command is ONE element — no host
// shell. Identity: --current-user by default, --user <name> when user is
// set, and NO identity flag when elevated (prlctl then runs the command as
// the guest's system account; verified on a real host per spec 018).
// elevated and user are mutually exclusive.
func buildExecArgs(vmRef, command string, opts interfaces.ExecOptions) (ref string, args []string, err error) {
	ref, err = ValidateVMRef(vmRef)
	if err != nil {
		return "", nil, err
	}
	cmd, err := validateCommand(command)
	if err != nil {
		return "", nil, err
	}
	user := opts.User
	if opts.Elevated && strings.TrimSpace(user) != "" {
		return "", nil, errors.New("elevated and user are mutually exclusive")
	}
	if user != "" {
		if user, err = ValidateVMRef(user); err != nil {
			return "", nil, err
		}
	}

	args = []string{"exec", ref}
	switch {
	case opts.Elevated:
		// no identity flag: system account
	case user == "":
		args = append(args, "--current-user")
	default:
		args = append(args, "--user", user)
	}
	if opts.ResolvePaths {
		args = append(args, "--resolve-paths")
	}
	args = append(args, cmd)
	return ref, args, nil
}

// Exec runs command inside the VM. Identity defaults to --current-user;
// user overrides it via --user. Non-zero guest exit codes are returned as
// results (err == nil); err is non-nil only when prlctl itself failed.
func (s *service) Exec(ctx context.Context, vmRef, command string, user string, resolvePaths bool) (interfaces.ExecResult, error) {
	return s.ExecWithOptions(ctx, vmRef, command, interfaces.ExecOptions{User: user, ResolvePaths: resolvePaths})
}

// ExecWithOptions is Exec with the full option set, including Elevated.
func (s *service) ExecWithOptions(ctx context.Context, vmRef, command string, opts interfaces.ExecOptions) (interfaces.ExecResult, error) {
	if !s.enabled {
		return interfaces.ExecResult{}, ErrNotEnabled
	}
	ref, args, err := buildExecArgs(vmRef, command, opts)
	if err != nil {
		return interfaces.ExecResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	start := time.Now()
	stdout, stderr, exitCode, err := s.runner.RunCapture(runCtx, s.prlctlPath, args...)
	if err != nil {
		// The error string contains the command via joinArgs — it may hold
		// inline secrets, so log the VM ref only.
		s.logger.Warn("prlctl exec failed", "vm", ref)
		return interfaces.ExecResult{}, err
	}

	stdout, stdoutCut := truncateRuneSafe(stdout, maxExecStdout)
	stderr, stderrCut := truncateRuneSafe(stderr, maxExecStderr)
	s.logger.Info("prlctl exec completed", "vm", ref, "elevated", opts.Elevated, "exit_code", exitCode, "stdout_bytes", len(stdout), "stderr_bytes", len(stderr), "took", time.Since(start))
	return interfaces.ExecResult{
		Stdout:          stdout,
		Stderr:          stderr,
		ExitCode:        exitCode,
		StdoutTruncated: stdoutCut,
		StderrTruncated: stderrCut,
	}, nil
}
