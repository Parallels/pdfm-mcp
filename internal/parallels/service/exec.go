package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// defaultCommandTimeout bounds a single prlctl/prlsrvctl invocation when the
// caller provides no context deadline. Power commands (stop, restart, reset)
// pass a longer deadline instead — see powerTimeout in power.go.
// It is a variable so tests can shorten it.
var defaultCommandTimeout = 30 * time.Second

// maxOutputBytes bounds how much stdout we are willing to capture from a
// single invocation; a list command on a machine with many VMs can get large,
// but anything beyond this is a bug, not data.
const maxOutputBytes = 10 << 20 // 10 MB

// CommandRunner executes a Parallels CLI binary. The implementation must
// never let a child process inherit stdout: stdout belongs to the MCP
// protocol.
type CommandRunner interface {
	// Run executes exe with args (one element each, never shell-interpolated)
	// and returns its stdout. The returned error carries the child's stderr
	// when the command fails.
	Run(ctx context.Context, exe string, args ...string) (string, error)

	// RunCapture is like Run but also reports stderr and the child's exit
	// code, and preserves stdout when the child exits non-zero. err is
	// non-nil only for launch failures and context expiry — a non-zero exit
	// code is NOT an error here.
	RunCapture(ctx context.Context, exe string, args ...string) (stdout, stderr string, exitCode int, err error)

	// RunStdin is like RunCapture but also feeds stdin to the child.
	RunStdin(ctx context.Context, exe, stdin string, args ...string) (stdout, stderr string, exitCode int, err error)
}

// realRunner is the production CommandRunner.
type realRunner struct{}

// NewCommandRunner returns the production runner.
func NewCommandRunner() CommandRunner { return realRunner{} }

func (realRunner) Run(ctx context.Context, exe string, args ...string) (string, error) {
	// A caller-provided deadline (e.g. the power-operation timeout) is
	// authoritative; the default cap is only a backstop for contexts without
	// one.
	runCtx, cancel := ctx, func() {}
	if _, ok := ctx.Deadline(); !ok {
		runCtx, cancel = context.WithTimeout(ctx, defaultCommandTimeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, exe, args...) // #nosec G204 -- exe comes from our own detection step, args are fixed flags plus a validated VM id/name
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Go reports a context kill as a plain "signal: killed" exit error,
		// so the context itself is the source of truth for that case.
		if cerr := runCtx.Err(); cerr != nil {
			return "", fmt.Errorf("%s %s: %v (command timed out)", exe, joinArgs(args), cerr)
		}
		return "", mapRunError(exe, args, err, &stderr)
	}
	if stdout.Len() > maxOutputBytes {
		return "", fmt.Errorf("output from %s %s exceeds %d bytes", exe, joinArgs(args), maxOutputBytes)
	}
	return stdout.String(), nil
}

// RunCapture implements CommandRunner. It shares Run's deadline policy
// (caller deadline authoritative, else the default cap) but keeps the
// child's stdout and stderr and reports its exit code instead of folding a
// non-zero exit into an error.
func (realRunner) RunCapture(ctx context.Context, exe string, args ...string) (string, string, int, error) {
	runCtx, cancel := ctx, func() {}
	if _, ok := ctx.Deadline(); !ok {
		runCtx, cancel = context.WithTimeout(ctx, defaultCommandTimeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, exe, args...) // #nosec G204 -- exe comes from our own detection step, args are fixed flags plus validated identifiers and a bounded command
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if cerr := runCtx.Err(); cerr != nil {
			// Go reports a context kill as a plain "signal: killed" exit
			// error, so the context itself is the source of truth.
			return "", "", -1, fmt.Errorf("%s %s: %v (command timed out)", exe, joinArgs(args), cerr)
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			// Launch failure (missing binary, permission, ...).
			return "", "", -1, mapRunError(exe, args, err, &stderr)
		}
		// A non-zero guest/host exit is a result, not an error.
		if stdout.Len() > maxOutputBytes || stderr.Len() > maxOutputBytes {
			return "", "", -1, fmt.Errorf("output from %s %s exceeds %d bytes", exe, joinArgs(args), maxOutputBytes)
		}
		return stdout.String(), stderr.String(), ee.ExitCode(), nil
	}
	if stdout.Len() > maxOutputBytes || stderr.Len() > maxOutputBytes {
		return "", "", -1, fmt.Errorf("output from %s %s exceeds %d bytes", exe, joinArgs(args), maxOutputBytes)
	}
	return stdout.String(), stderr.String(), 0, nil
}

// RunStdin implements CommandRunner. It shares RunCapture's deadline policy
// and stream caps, and additionally feeds stdin to the child (the caller is
// responsible for bounding it).
func (realRunner) RunStdin(ctx context.Context, exe, stdin string, args ...string) (string, string, int, error) {
	runCtx, cancel := ctx, func() {}
	if _, ok := ctx.Deadline(); !ok {
		runCtx, cancel = context.WithTimeout(ctx, defaultCommandTimeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, exe, args...) // #nosec G204 -- exe comes from our own detection step, args are fixed flags plus validated identifiers; stdin is validated JSON built by the service
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if cerr := runCtx.Err(); cerr != nil {
			// Go reports a context kill as a plain "signal: killed" exit
			// error, so the context itself is the source of truth.
			return "", "", -1, fmt.Errorf("%s %s: %v (command timed out)", exe, joinArgs(args), cerr)
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			// Launch failure (missing binary, permission, ...).
			return "", "", -1, mapRunError(exe, args, err, &stderr)
		}
		if stdout.Len() > maxOutputBytes || stderr.Len() > maxOutputBytes {
			return "", "", -1, fmt.Errorf("output from %s %s exceeds %d bytes", exe, joinArgs(args), maxOutputBytes)
		}
		return stdout.String(), stderr.String(), ee.ExitCode(), nil
	}
	if stdout.Len() > maxOutputBytes || stderr.Len() > maxOutputBytes {
		return "", "", -1, fmt.Errorf("output from %s %s exceeds %d bytes", exe, joinArgs(args), maxOutputBytes)
	}
	return stdout.String(), stderr.String(), 0, nil
}

func mapRunError(exe string, args []string, err error, stderr *bytes.Buffer) error {
	msg := bytes.TrimSpace(stderr.Bytes())
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if len(msg) > 0 {
		return fmt.Errorf("%s %s failed: %s (%v)", exe, joinArgs(args), msg, err)
	}
	return fmt.Errorf("%s %s failed: %v", exe, joinArgs(args), err)
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
