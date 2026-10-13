package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
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

// validateCommand bounds the guest command line. The command is tokenized
// into argv elements for prlctl (never a host shell, spec 029), so the
// only host-side risks are NUL bytes (impossible in argv) and unbounded
// size.
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

// splitCommandLine splits a guest command line into argv elements, the
// form prlctl requires (spec 029): the first element is the program, the
// rest its arguments. On a Windows guest a single multi-word element is
// looked up as one executable name and fails (field-reported exit 2), so
// the line is split for EVERY guest OS — uniformly, with no guest-OS
// branching here.
//
// Rules:
//   - unquoted whitespace separates elements;
//   - a single or double quote groups everything up to the matching quote
//     into one element (the quotes are consumed);
//   - a backslash escapes the following character only when it is
//     whitespace or a quote; it is otherwise literal, so Windows paths
//     (C:\Users\...) are preserved; inside double quotes \" and \\ are
//     also literal;
//   - an unterminated quote or invalid UTF-8 is an error.
//
// Nothing is shell-interpreted: |, &&, ;, >, globs and $variables are
// ordinary characters. Shell semantics are the caller's explicit choice:
// sh -c "..." (POSIX guests), cmd /c "..." or powershell -NoProfile
// -Command "..." (Windows guests).
func splitCommandLine(s string) ([]string, error) {
	var (
		out   []string
		cur   strings.Builder
		inArg bool
		quote rune
	)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, errors.New("command contains invalid UTF-8")
		}
		switch {
		case quote != 0:
			switch {
			case r == quote:
				quote = 0 // closing quote: consumed, not written
			case r == '\\' && quote == '"' && i+size < len(s):
				n, nsize := utf8.DecodeRuneInString(s[i+size:])
				if n == '"' || n == '\\' {
					cur.WriteRune(n)
					i += size + nsize
					continue
				}
				cur.WriteRune(r)
			default:
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inArg = true
		case r == '\\':
			if i+size < len(s) {
				n, nsize := utf8.DecodeRuneInString(s[i+size:])
				if isQuoteOrSpace(n) {
					cur.WriteRune(n)
					inArg = true
					i += size + nsize
					continue
				}
			}
			cur.WriteRune(r)
			inArg = true
		case unicode.IsSpace(r):
			if inArg {
				out = append(out, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
		i += size
	}
	if quote != 0 {
		return nil, errors.New("command has an unterminated quote")
	}
	if inArg {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return nil, errors.New("command must not be empty")
	}
	return out, nil
}

// isQuoteOrSpace reports whether r may be escaped by a backslash outside
// quotes (everything else, including \ in Windows paths, stays literal).
func isQuoteOrSpace(r rune) bool {
	return r == '"' || r == '\'' || unicode.IsSpace(r)
}

// buildExecArgs validates the exec inputs and builds the prlctl argv:
// exec <vm> <identity> [--resolve-paths] <elem1> [<elem2> ...]. Options
// come before the command (prlctl exec --help); the command line is split
// into argv elements (splitCommandLine, spec 029) for every guest OS —
// never a host or guest shell. Identity: --current-user by default,
// --user <name> when user is set, and NO identity flag when elevated
// (prlctl then runs the command as the guest's system account; verified
// on a real host per spec 018). elevated and user are mutually exclusive.
func buildExecArgs(vmRef, command string, opts interfaces.ExecOptions) (ref string, args []string, err error) {
	ref, err = ValidateVMRef(vmRef)
	if err != nil {
		return "", nil, err
	}
	cmd, err := validateCommand(command)
	if err != nil {
		return "", nil, err
	}
	elems, err := splitCommandLine(cmd)
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
	args = append(args, elems...)
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
