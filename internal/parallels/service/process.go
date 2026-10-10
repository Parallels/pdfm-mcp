package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Retention caps of an async job's output streams. Beyond them the process
// keeps being drained (so it never blocks on a full pipe) but the extra
// bytes are dropped and the stream is flagged truncated.
const (
	maxJobStdoutBytes = 4 << 20 // 4 MB
	maxJobStderrBytes = 1 << 20 // 1 MB
)

// processWaitDelay bounds how long Wait lingers for the output pipes after
// the process exited (a grandchild holding a pipe open must not hang us).
const processWaitDelay = 5 * time.Second

// OutputBuffer is a thread-safe, bounded, append-only byte buffer used as
// the stdout/stderr sink of a streaming Process. It implements io.Writer
// and never fails or blocks: bytes beyond the limit are counted and
// dropped.
type OutputBuffer struct {
	mu      sync.Mutex
	limit   int
	data    []byte
	dropped int64
}

// NewOutputBuffer returns a buffer retaining at most limit bytes.
func NewOutputBuffer(limit int) *OutputBuffer {
	return &OutputBuffer{limit: limit}
}

// Write implements io.Writer. It always reports len(p) written.
func (b *OutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.limit - len(b.data)
	take := 0
	if room > 0 {
		take = min(len(p), room)
		b.data = append(b.data, p[:take]...)
	}
	if take < len(p) {
		b.dropped += int64(len(p) - take)
	}
	return len(p), nil
}

// Len returns the number of retained bytes.
func (b *OutputBuffer) Len() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int64(len(b.data))
}

// Truncated reports whether bytes beyond the retention cap were dropped.
func (b *OutputBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped > 0
}

// Slice returns up to maxBytes retained bytes starting at offset (clamped
// to the retained range), the offset to resume from, and whether retained
// bytes remain beyond it. Unless final is true and the slice reaches the
// end of the buffer, an incomplete trailing UTF-8 sequence is held back
// for the next call, so slices never split a rune. Invalid bytes are
// replaced with U+FFFD in the returned text; offsets always count raw
// bytes.
func (b *OutputBuffer) Slice(offset int64, maxBytes int, final bool) (text string, next int64, hasMore bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := int64(len(b.data))
	if offset < 0 {
		offset = 0
	}
	if offset > n {
		offset = n
	}
	end := offset + int64(maxBytes)
	if end > n {
		end = n
	}
	chunk := b.data[offset:end]
	if !final || end != n {
		chunk = trimIncompleteRune(chunk)
	}
	next = offset + int64(len(chunk))
	return strings.ToValidUTF8(string(chunk), "�"), next, next < n
}

// trimIncompleteRune drops a trailing incomplete UTF-8 sequence.
func trimIncompleteRune(b []byte) []byte {
	for i := 1; i <= utf8.UTFMax && i <= len(b); i++ {
		if utf8.RuneStart(b[len(b)-i]) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			return b
		}
	}
	return b
}

// Process is a running child started by a StreamRunner. Its output is
// readable while it runs.
type Process interface {
	// Stdout / Stderr are the live, bounded output buffers.
	Stdout() *OutputBuffer
	Stderr() *OutputBuffer
	// Done is closed once the process exited and its output was drained.
	Done() <-chan struct{}
	// Result reports the exit code (-1 when killed by a signal or when no
	// code exists) and a non-nil error only for abnormal wait failures. It
	// is only valid after Done is closed.
	Result() (exitCode int, err error)
	// Kill terminates the process. Safe to call more than once and after
	// the process exited.
	Kill() error
}

// StreamRunner is the optional streaming seam of a CommandRunner: it
// starts a child without waiting for it. The implementation must never let
// the child inherit stdout (stdout belongs to the MCP protocol) and must
// kill the child when ctx ends.
type StreamRunner interface {
	Start(ctx context.Context, exe string, args ...string) (Process, error)
}

// Start implements StreamRunner for the production runner.
func (realRunner) Start(ctx context.Context, exe string, args ...string) (Process, error) {
	cmd := exec.CommandContext(ctx, exe, args...) // #nosec G204 -- exe comes from our own detection step, args are fixed flags plus validated identifiers and a bounded command
	p := &realProcess{
		cmd:    cmd,
		stdout: NewOutputBuffer(maxJobStdoutBytes),
		stderr: NewOutputBuffer(maxJobStderrBytes),
		done:   make(chan struct{}),
	}
	cmd.Stdin = nil // /dev/null
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr
	cmd.WaitDelay = processWaitDelay
	if err := cmd.Start(); err != nil {
		// exec errors name the binary, never the arguments.
		return nil, fmt.Errorf("start %s: %w", exe, err)
	}
	go p.wait()
	return p, nil
}

type realProcess struct {
	cmd    *exec.Cmd
	stdout *OutputBuffer
	stderr *OutputBuffer
	done   chan struct{}

	// written before done is closed, read only after.
	exitCode int
	err      error
}

func (p *realProcess) wait() {
	err := p.cmd.Wait()
	var ee *exec.ExitError
	switch {
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
		// ErrWaitDelay: the process exited successfully but a pipe was
		// held open past WaitDelay; the exit itself was clean.
		p.exitCode = 0
	case errors.As(err, &ee):
		p.exitCode = ee.ExitCode()
	default:
		p.exitCode = -1
		p.err = err
	}
	close(p.done)
}

func (p *realProcess) Stdout() *OutputBuffer { return p.stdout }

func (p *realProcess) Stderr() *OutputBuffer { return p.stderr }

func (p *realProcess) Done() <-chan struct{} { return p.done }

func (p *realProcess) Result() (int, error) { return p.exitCode, p.err }

func (p *realProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
