package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

const (
	// DefaultJobTimeout is the async job timeout when none is given.
	DefaultJobTimeout = 1800 * time.Second
	// MaxJobTimeout is the largest accepted async job timeout.
	MaxJobTimeout = 14400 * time.Second
	// MaxJobWait is the largest long-poll wait of ExecJobResult.
	MaxJobWait = 30 * time.Second
	// MaxJobReadBytes caps one stream's slice per ExecJobResult call.
	MaxJobReadBytes = 256 << 10

	maxRunningJobs  = 4
	maxRetainedJobs = 32
	jobRetention    = time.Hour
	jobIDBytes      = 16
	// killWait bounds how long cancel/shutdown wait for a killed job.
	killWait = 2 * time.Second
)

var errJobNotFound = errors.New("job not found: unknown id, expired (finished jobs are kept for 1 hour), or it belongs to another VM")

// execJob is one async exec job. The command text is deliberately not a
// field: only the VM ref, identity flags and timestamps are kept.
type execJob struct {
	id       string
	vm       string
	user     string
	elevated bool
	started  time.Time
	proc     Process
	cancel   context.CancelFunc
	finished chan struct{} // closed after the final state is recorded

	mu              sync.Mutex
	state           string
	exitCode        int
	message         string
	finishedAt      time.Time
	cancelRequested bool
}

func (j *execJob) isRunning() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state == interfaces.JobRunning
}

// jobManager owns the async jobs of one service. All jobs run on baseCtx
// (server lifetime), never on a tool request context.
type jobManager struct {
	logger     *slog.Logger
	now        func() time.Time
	baseCtx    context.Context
	baseCancel context.CancelFunc

	mu     sync.Mutex
	jobs   map[string]*execJob
	closed bool
}

func newJobManager(logger *slog.Logger) *jobManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &jobManager{
		logger:     logger,
		now:        time.Now,
		baseCtx:    ctx,
		baseCancel: cancel,
		jobs:       map[string]*execJob{},
	}
}

func newJobID() (string, error) {
	b := make([]byte, jobIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func validateJobID(id string) error {
	if len(id) != jobIDBytes*2 {
		return errors.New("invalid job_id")
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return errors.New("invalid job_id")
		}
	}
	return nil
}

// reapLocked drops finished jobs older than the retention. m.mu is held.
func (m *jobManager) reapLocked(now time.Time) {
	for id, j := range m.jobs {
		j.mu.Lock()
		expired := j.state != interfaces.JobRunning && now.Sub(j.finishedAt) >= jobRetention
		j.mu.Unlock()
		if expired {
			delete(m.jobs, id)
		}
	}
}

// evictOldestFinishedLocked removes the finished job that finished first.
func (m *jobManager) evictOldestFinishedLocked() {
	var oldestID string
	var oldest time.Time
	for id, j := range m.jobs {
		j.mu.Lock()
		fin, at := j.state != interfaces.JobRunning, j.finishedAt
		j.mu.Unlock()
		if fin && (oldestID == "" || at.Before(oldest)) {
			oldestID, oldest = id, at
		}
	}
	if oldestID != "" {
		delete(m.jobs, oldestID)
	}
}

func (m *jobManager) start(sr StreamRunner, exe string, args []string, vm, user string, elevated bool, timeout time.Duration) (*execJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("the server is shutting down: no new jobs can start")
	}
	m.reapLocked(m.now())

	running := 0
	for _, j := range m.jobs {
		if j.isRunning() {
			running++
		}
	}
	if running >= maxRunningJobs {
		return nil, fmt.Errorf("too many running jobs (limit %d): wait for one to finish or cancel it", maxRunningJobs)
	}
	if len(m.jobs) >= maxRetainedJobs {
		m.evictOldestFinishedLocked()
	}
	if len(m.jobs) >= maxRetainedJobs {
		return nil, fmt.Errorf("too many retained jobs (limit %d)", maxRetainedJobs)
	}

	id, err := newJobID()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx, timeout)
	proc, err := sr.Start(ctx, exe, args...)
	if err != nil {
		cancel()
		return nil, err
	}
	j := &execJob{
		id:       id,
		vm:       vm,
		user:     user,
		elevated: elevated,
		started:  m.now(),
		proc:     proc,
		cancel:   cancel,
		finished: make(chan struct{}),
		state:    interfaces.JobRunning,
		exitCode: -1,
	}
	m.jobs[id] = j
	go m.supervise(ctx, j)
	return j, nil
}

// supervise waits for the process and records the final state.
func (m *jobManager) supervise(ctx context.Context, j *execJob) {
	<-j.proc.Done()
	code, perr := j.proc.Result()
	ctxErr := ctx.Err() // before j.cancel(): only a real timeout/shutdown sets it
	j.cancel()
	fin := m.now()

	j.mu.Lock()
	switch {
	case j.cancelRequested:
		j.state = interfaces.JobCancelled
		j.message = "job was cancelled"
	case errors.Is(ctxErr, context.DeadlineExceeded):
		j.state = interfaces.JobTimedOut
		j.message = "job exceeded its timeout and was killed"
	case ctxErr != nil:
		j.state = interfaces.JobCancelled
		j.message = "job was cancelled (server shutdown)"
	case perr != nil:
		j.state = interfaces.JobFailed
		j.message = "process error: " + perr.Error()
	case code == 0:
		j.state = interfaces.JobSucceeded
	default:
		j.state = interfaces.JobFailed
	}
	j.exitCode = code
	j.finishedAt = fin
	state := j.state
	j.mu.Unlock()
	close(j.finished)
	m.logger.Info("exec job finished", "job", j.id, "vm", j.vm, "elevated", j.elevated, "state", state, "exit_code", code)
}

func (m *jobManager) get(id, scopeVM string) (*execJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reapLocked(m.now())
	j, ok := m.jobs[id]
	if !ok || (scopeVM != "" && j.vm != scopeVM) {
		return nil, errJobNotFound
	}
	return j, nil
}

// snapshot builds the status; withOutput adds the stream slices.
func (m *jobManager) snapshot(j *execJob, q interfaces.ExecJobQuery, withOutput bool) interfaces.ExecJobStatus {
	j.mu.Lock()
	state, code, msg, finAt := j.state, j.exitCode, j.message, j.finishedAt
	j.mu.Unlock()
	final := state != interfaces.JobRunning

	end := m.now()
	if final {
		end = finAt
	}
	st := interfaces.ExecJobStatus{
		JobID:     j.id,
		VM:        j.vm,
		State:     state,
		Elevated:  j.elevated,
		User:      j.user,
		ExitCode:  code,
		Message:   msg,
		ElapsedMS: end.Sub(j.started).Milliseconds(),
	}
	if !final {
		st.ExitCode = -1
	}
	if withOutput {
		so, se := j.proc.Stdout(), j.proc.Stderr()
		st.Stdout, st.StdoutNext, st.StdoutHasMore = so.Slice(q.StdoutOffset, MaxJobReadBytes, final)
		st.Stderr, st.StderrNext, st.StderrHasMore = se.Slice(q.StderrOffset, MaxJobReadBytes, final)
		st.StdoutTruncated = so.Truncated()
		st.StderrTruncated = se.Truncated()
	}
	return st
}

// kill marks a running job cancel-requested and kills it. It reports
// whether the job was running.
func (m *jobManager) kill(j *execJob) bool {
	j.mu.Lock()
	running := j.state == interfaces.JobRunning
	if running {
		j.cancelRequested = true
	}
	j.mu.Unlock()
	if running {
		if err := j.proc.Kill(); err != nil {
			m.logger.Warn("killing exec job failed", "job", j.id, "vm", j.vm)
		}
	}
	return running
}

// shutdown kills every running job and refuses new ones. Idempotent.
func (m *jobManager) shutdown() {
	m.mu.Lock()
	m.closed = true
	all := make([]*execJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		all = append(all, j)
	}
	m.mu.Unlock()

	for _, j := range all {
		m.kill(j)
	}
	m.baseCancel()

	deadline := time.NewTimer(killWait)
	defer deadline.Stop()
	for _, j := range all {
		select {
		case <-j.finished:
		case <-deadline.C:
			return
		}
	}
}

// ---- service methods -------------------------------------------------------

func (s *service) StartExecJob(_ context.Context, vmRef, command string, opts interfaces.ExecOptions, timeout time.Duration) (interfaces.ExecJobInfo, error) {
	if !s.enabled {
		return interfaces.ExecJobInfo{}, ErrNotEnabled
	}
	ref, args, err := buildExecArgs(vmRef, command, opts)
	if err != nil {
		return interfaces.ExecJobInfo{}, err
	}
	if timeout == 0 {
		timeout = DefaultJobTimeout
	}
	if timeout < 0 || timeout > MaxJobTimeout {
		return interfaces.ExecJobInfo{}, fmt.Errorf("timeout must be between 1 second and %d seconds", int(MaxJobTimeout/time.Second))
	}
	sr, ok := s.runner.(StreamRunner)
	if !ok {
		return interfaces.ExecJobInfo{}, errors.New("the command runner does not support background jobs")
	}
	j, err := s.jobs.start(sr, s.prlctlPath, args, ref, opts.User, opts.Elevated, timeout)
	if err != nil {
		// never log err/args: they may carry the command line
		s.logger.Warn("exec job start failed", "vm", ref)
		return interfaces.ExecJobInfo{}, err
	}
	s.logger.Info("exec job started", "job", j.id, "vm", ref, "elevated", opts.Elevated, "timeout", timeout)
	return interfaces.ExecJobInfo{
		JobID:          j.id,
		VM:             ref,
		Elevated:       opts.Elevated,
		TimeoutSeconds: int(timeout / time.Second),
		StartedAt:      j.started,
	}, nil
}

func (s *service) ExecJobResult(ctx context.Context, jobID string, q interfaces.ExecJobQuery) (interfaces.ExecJobStatus, error) {
	if !s.enabled {
		return interfaces.ExecJobStatus{}, ErrNotEnabled
	}
	if err := validateJobID(jobID); err != nil {
		return interfaces.ExecJobStatus{}, err
	}
	if q.Wait < 0 || q.Wait > MaxJobWait {
		return interfaces.ExecJobStatus{}, fmt.Errorf("wait must be between 0 and %d ms", int(MaxJobWait/time.Millisecond))
	}
	if q.StdoutOffset < 0 || q.StderrOffset < 0 {
		return interfaces.ExecJobStatus{}, errors.New("offsets must not be negative")
	}
	j, err := s.jobs.get(jobID, s.vmID)
	if err != nil {
		return interfaces.ExecJobStatus{}, err
	}
	if q.Wait > 0 && j.isRunning() {
		t := time.NewTimer(q.Wait)
		defer t.Stop()
		select {
		case <-j.finished:
		case <-t.C:
		case <-ctx.Done():
			return interfaces.ExecJobStatus{}, ctx.Err()
		}
	}
	return s.jobs.snapshot(j, q, true), nil
}

func (s *service) CancelExecJob(ctx context.Context, jobID string) (interfaces.ExecJobStatus, bool, error) {
	if !s.enabled {
		return interfaces.ExecJobStatus{}, false, ErrNotEnabled
	}
	if err := validateJobID(jobID); err != nil {
		return interfaces.ExecJobStatus{}, false, err
	}
	j, err := s.jobs.get(jobID, s.vmID)
	if err != nil {
		return interfaces.ExecJobStatus{}, false, err
	}
	was := s.jobs.kill(j)
	if was {
		t := time.NewTimer(killWait)
		defer t.Stop()
		select {
		case <-j.finished:
		case <-t.C:
		case <-ctx.Done():
		}
		s.logger.Info("exec job cancel requested", "job", j.id, "vm", j.vm)
	}
	return s.jobs.snapshot(j, interfaces.ExecJobQuery{}, false), was, nil
}

// Shutdown kills all running async jobs of the singleton (no-op when it
// was never initialized). Call it on server exit.
func Shutdown() {
	if instance != nil {
		instance.jobs.shutdown()
	}
}
