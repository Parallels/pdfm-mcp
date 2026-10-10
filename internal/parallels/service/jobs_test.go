package service

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// fakeProc is a controllable streaming process. It ends when finish is
// called, when Kill is called, or when its context ends (timeout/shutdown).
type fakeProc struct {
	stdout *OutputBuffer
	stderr *OutputBuffer
	done   chan struct{}
	once   sync.Once

	mu     sync.Mutex
	code   int
	killed bool
}

func newFakeProc(ctx context.Context) *fakeProc {
	p := &fakeProc{
		stdout: NewOutputBuffer(maxJobStdoutBytes),
		stderr: NewOutputBuffer(maxJobStderrBytes),
		done:   make(chan struct{}),
	}
	go func() {
		select {
		case <-ctx.Done():
			p.finish(-1)
		case <-p.done:
		}
	}()
	return p
}

func (p *fakeProc) finish(code int) {
	p.once.Do(func() {
		p.mu.Lock()
		p.code = code
		p.mu.Unlock()
		close(p.done)
	})
}

func (p *fakeProc) Stdout() *OutputBuffer { return p.stdout }

func (p *fakeProc) Stderr() *OutputBuffer { return p.stderr }

func (p *fakeProc) Done() <-chan struct{} { return p.done }

func (p *fakeProc) Result() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code, nil
}

func (p *fakeProc) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.finish(-1)
	return nil
}

func (p *fakeProc) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// procRegistry collects the processes started through a fakeRunner.
type procRegistry struct {
	mu    sync.Mutex
	procs []*fakeProc
	argvs [][]string
}

func (r *procRegistry) start(ctx context.Context, _ string, args []string) (Process, error) {
	p := newFakeProc(ctx)
	r.mu.Lock()
	r.procs = append(r.procs, p)
	r.argvs = append(r.argvs, append([]string(nil), args...))
	r.mu.Unlock()
	return p, nil
}

func (r *procRegistry) get(i int) *fakeProc {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.procs[i]
}

func (r *procRegistry) argv(i int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.argvs[i]
}

// lockedBuffer is a goroutine-safe log sink.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// fakeClock is a goroutine-safe injectable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func jobService(t *testing.T, vmID string, logger *slog.Logger) (*service, *procRegistry) {
	t.Helper()
	reg := &procRegistry{}
	runner := &fakeRunner{startFn: reg.start}
	resetBetween(t)
	ok := mustInit(t,
		WithLogger(logger),
		WithLookPath(fakeLookPath(map[string]string{
			"prlctl":    "/usr/local/bin/prlctl",
			"prlsrvctl": "/usr/local/bin/prlsrvctl",
		})),
		WithRunner(runner),
		WithCacheTTL(30*time.Second),
		WithFallbackDirs(),
		WithVMID(vmID),
	)
	if !ok {
		t.Fatal("service unexpectedly disabled")
	}
	svc, isSvc := GetInstance().(*service)
	if !isSvc {
		t.Fatal("unexpected service type")
	}
	return svc, reg
}

func startJob(t *testing.T, svc *service, vm string, opts interfaces.ExecOptions, timeout time.Duration) interfaces.ExecJobInfo {
	t.Helper()
	info, err := svc.StartExecJob(context.Background(), vm, "make build", opts, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// waitDone long-polls until the job leaves the running state.
func waitDone(t *testing.T, svc *service, id string) interfaces.ExecJobStatus {
	t.Helper()
	st, err := svc.ExecJobResult(context.Background(), id, interfaces.ExecJobQuery{Wait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if st.State == interfaces.JobRunning {
		t.Fatal("job still running after wait")
	}
	return st
}

func TestExecElevatedArgvShapes(t *testing.T) {
	cases := []struct {
		name string
		opts interfaces.ExecOptions
		want []string
	}{
		{"elevated has no identity flag", interfaces.ExecOptions{Elevated: true}, []string{"exec", "guest-x", "id"}},
		{"elevated with resolve paths", interfaces.ExecOptions{Elevated: true, ResolvePaths: true}, []string{"exec", "guest-x", "--resolve-paths", "id"}},
		{"default unchanged", interfaces.ExecOptions{}, []string{"exec", "guest-x", "--current-user", "id"}},
		{"user unchanged", interfaces.ExecOptions{User: "root"}, []string{"exec", "guest-x", "--user", "root", "id"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{}
			svc := powerService(t, runner, "")
			if _, err := svc.ExecWithOptions(context.Background(), "guest-x", "id", tc.opts); err != nil {
				t.Fatal(err)
			}
			_, args := runner.lastCall()
			if !slices.Equal(args, tc.want) {
				t.Fatalf("args = %v, want %v", args, tc.want)
			}
		})
	}
}

func TestExecElevatedWithUserIsRejected(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	_, err := svc.ExecWithOptions(context.Background(), "guest-x", "id", interfaces.ExecOptions{Elevated: true, User: "root"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v, want mutually exclusive", err)
	}
	if runner.callCount() != 0 {
		t.Fatal("prlctl must not run")
	}
	if _, _, err := svc.CancelExecJob(context.Background(), "nothex"); err == nil {
		t.Fatal("invalid job id must be rejected")
	}
	if _, err := svc.StartExecJob(context.Background(), "guest-x", "id", interfaces.ExecOptions{Elevated: true, User: "root"}, 0); err == nil {
		t.Fatal("start must reject elevated+user")
	}
}

func TestJobLifecyclePartialReadsAndSuccess(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	info := startJob(t, svc, "guest-x", interfaces.ExecOptions{Elevated: true}, 0)
	if len(info.JobID) != 32 || info.TimeoutSeconds != 1800 || !info.Elevated || info.VM != "guest-x" {
		t.Fatalf("info = %+v", info)
	}
	if got := reg.argv(0); !slices.Equal(got, []string{"exec", "guest-x", "make build"}) {
		t.Fatalf("argv = %v", got)
	}

	st, err := svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if st.State != interfaces.JobRunning || st.ExitCode != -1 {
		t.Fatalf("status = %+v", st)
	}

	p := reg.get(0)
	if _, err := p.stdout.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{})
	if st.Stdout != "hello" || st.StdoutNext != 5 {
		t.Fatalf("partial read = %+v", st)
	}
	if _, err := p.stdout.Write([]byte(" world")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.stderr.Write([]byte("warn")); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{StdoutOffset: 5})
	if st.Stdout != " world" || st.StdoutNext != 11 || st.Stderr != "warn" {
		t.Fatalf("incremental read = %+v", st)
	}

	p.finish(0)
	st = waitDone(t, svc, info.JobID)
	if st.State != interfaces.JobSucceeded || st.ExitCode != 0 || st.Stdout != "hello world" {
		t.Fatalf("final = %+v", st)
	}
}

func TestJobFailedExitCode(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	info := startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0)
	reg.get(0).finish(3)
	st := waitDone(t, svc, info.JobID)
	if st.State != interfaces.JobFailed || st.ExitCode != 3 {
		t.Fatalf("status = %+v", st)
	}
	if got := reg.argv(0); !slices.Equal(got, []string{"exec", "guest-x", "--current-user", "make build"}) {
		t.Fatalf("argv = %v", got)
	}
}

func TestJobCancel(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	info := startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0)
	st, was, err := svc.CancelExecJob(context.Background(), info.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !was || st.State != interfaces.JobCancelled {
		t.Fatalf("cancel = %+v was=%v", st, was)
	}
	if !reg.get(0).wasKilled() {
		t.Fatal("process must be killed")
	}
	st, was, err = svc.CancelExecJob(context.Background(), info.JobID)
	if err != nil || was || st.State != interfaces.JobCancelled {
		t.Fatalf("second cancel = %+v was=%v err=%v", st, was, err)
	}
}

func TestJobTimeout(t *testing.T) {
	svc, _ := jobService(t, "", testLogger())
	info := startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 50*time.Millisecond)
	st := waitDone(t, svc, info.JobID)
	if st.State != interfaces.JobTimedOut {
		t.Fatalf("status = %+v", st)
	}
}

func TestJobTimeoutValidation(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	if _, err := svc.StartExecJob(context.Background(), "guest-x", "id", interfaces.ExecOptions{}, MaxJobTimeout+time.Second); err == nil {
		t.Fatal("timeout above the max must be rejected")
	}
	if _, err := svc.StartExecJob(context.Background(), "guest-x", "id", interfaces.ExecOptions{}, -time.Second); err == nil {
		t.Fatal("negative timeout must be rejected")
	}
	if reg.argvs != nil {
		t.Fatal("nothing must start")
	}
}

func TestJobConcurrencyLimit(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	ids := make([]string, 0, maxRunningJobs)
	for i := 0; i < maxRunningJobs; i++ {
		ids = append(ids, startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0).JobID)
	}
	_, err := svc.StartExecJob(context.Background(), "guest-x", "id", interfaces.ExecOptions{}, 0)
	if err == nil || !strings.Contains(err.Error(), "too many running jobs") {
		t.Fatalf("5th job err = %v", err)
	}
	reg.get(0).finish(0)
	waitDone(t, svc, ids[0])
	startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0)
}

func TestJobRetentionEviction(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	svc.jobs.now = clock.Now
	var first string
	for i := 0; i < maxRetainedJobs+1; i++ {
		info := startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0)
		if i == 0 {
			first = info.JobID
		}
		clock.Advance(time.Second) // distinct finish times: eviction order is deterministic
		reg.get(i).finish(0)
		waitDone(t, svc, info.JobID)
	}
	svc.jobs.mu.Lock()
	n := len(svc.jobs.jobs)
	svc.jobs.mu.Unlock()
	if n != maxRetainedJobs {
		t.Fatalf("retained = %d, want %d", n, maxRetainedJobs)
	}
	if _, err := svc.ExecJobResult(context.Background(), first, interfaces.ExecJobQuery{}); err == nil {
		t.Fatal("oldest finished job must have been evicted")
	}
}

func TestJobExpiryWithInjectedClock(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	svc.jobs.now = clock.Now
	info := startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0)
	reg.get(0).finish(0)
	waitDone(t, svc, info.JobID)

	clock.Advance(59 * time.Minute)
	if _, err := svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{}); err != nil {
		t.Fatalf("job must still be retained: %v", err)
	}
	clock.Advance(2 * time.Minute)
	if _, err := svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{}); err == nil {
		t.Fatal("job must have expired after 1 hour")
	}
}

func TestJobOutputCapsAndOffsets(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	info := startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0)
	total := MaxJobReadBytes + 1000
	if _, err := reg.get(0).stdout.Write(bytes.Repeat([]byte("a"), total)); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{})
	if len(st.Stdout) != MaxJobReadBytes || st.StdoutNext != int64(MaxJobReadBytes) || !st.StdoutHasMore {
		t.Fatalf("first slice: len=%d next=%d more=%v", len(st.Stdout), st.StdoutNext, st.StdoutHasMore)
	}
	st, _ = svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{StdoutOffset: st.StdoutNext})
	if len(st.Stdout) != 1000 || st.StdoutHasMore {
		t.Fatalf("second slice: len=%d more=%v", len(st.Stdout), st.StdoutHasMore)
	}
	st, _ = svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{StdoutOffset: int64(total) + 99})
	if st.Stdout != "" || st.StdoutNext != int64(total) {
		t.Fatalf("offset beyond end: %+v", st)
	}
	if _, err := svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{StdoutOffset: -1}); err == nil {
		t.Fatal("negative offset must be rejected")
	}
	if _, err := svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{Wait: MaxJobWait + time.Second}); err == nil {
		t.Fatal("wait above the max must be rejected")
	}
}

func TestOutputBufferBoundAndRuneSafety(t *testing.T) {
	b := NewOutputBuffer(10)
	if n, err := b.Write([]byte("0123456789ABCDEF")); err != nil || n != 16 {
		t.Fatalf("write = %d, %v", n, err)
	}
	if b.Len() != 10 || !b.Truncated() {
		t.Fatalf("len=%d truncated=%v", b.Len(), b.Truncated())
	}

	r := NewOutputBuffer(100)
	// "é" is 2 bytes; slice mid-rune must hold the half rune back.
	if _, err := r.Write([]byte("aé")); err != nil {
		t.Fatal(err)
	}
	text, next, more := r.Slice(0, 2, false)
	if text != "a" || next != 1 || !more {
		t.Fatalf("mid-rune slice = %q next=%d more=%v", text, next, more)
	}
	text, next, more = r.Slice(next, 10, true)
	if text != "é" || next != 3 || more {
		t.Fatalf("rest = %q next=%d more=%v", text, next, more)
	}
}

func TestJobVMModeScoping(t *testing.T) {
	svc, reg := jobService(t, "vm-a", testLogger())
	info := startJob(t, svc, "vm-a", interfaces.ExecOptions{}, 0)
	if _, err := svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{}); err != nil {
		t.Fatal(err)
	}
	// Simulate a server targeting another VM than the job's.
	svc.vmID = "vm-b"
	if _, err := svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{}); err == nil {
		t.Fatal("result must not see a job of another VM")
	}
	if _, _, err := svc.CancelExecJob(context.Background(), info.JobID); err == nil {
		t.Fatal("cancel must not see a job of another VM")
	}
	if reg.get(0).wasKilled() {
		t.Fatal("out-of-scope job must not be killed")
	}
}

func TestJobShutdownKillsRunningJobs(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	a := startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0)
	b := startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0)
	svc.jobs.shutdown()
	for i, id := range []string{a.JobID, b.JobID} {
		st := waitDone(t, svc, id)
		if st.State != interfaces.JobCancelled {
			t.Fatalf("job %d state = %s", i, st.State)
		}
		if !reg.get(i).wasKilled() {
			t.Fatalf("job %d not killed", i)
		}
	}
	if _, err := svc.StartExecJob(context.Background(), "guest-x", "id", interfaces.ExecOptions{}, 0); err == nil {
		t.Fatal("no job may start after shutdown")
	}
	Shutdown() // package-level entry point is idempotent
}

func TestJobConcurrentAccessIsRaceFree(t *testing.T) {
	svc, reg := jobService(t, "", testLogger())
	info := startJob(t, svc, "guest-x", interfaces.ExecOptions{}, 0)
	p := reg.get(0)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := svc.ExecJobResult(context.Background(), info.JobID, interfaces.ExecJobQuery{}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		if _, err := p.stdout.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	p.finish(0)
	wg.Wait()
	waitDone(t, svc, info.JobID)
}

func TestJobLogsNeverContainCommandText(t *testing.T) {
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc, reg := jobService(t, "", logger)
	const secret = "SUPER-SECRET-TOKEN-42"
	info, err := svc.StartExecJob(context.Background(), "guest-x", "curl -H 'Authorization: "+secret+"' x", interfaces.ExecOptions{Elevated: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	reg.get(0).finish(1)
	waitDone(t, svc, info.JobID)
	if _, _, err := svc.CancelExecJob(context.Background(), info.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecWithOptions(context.Background(), "guest-x", "echo "+secret, interfaces.ExecOptions{}); err != nil {
		t.Logf("sync exec error (fake runner): %v", err)
	}
	out := logs.String()
	if strings.Contains(out, secret) || strings.Contains(out, "curl") {
		t.Fatalf("logs contain command text:\n%s", out)
	}
	if !strings.Contains(out, info.JobID) {
		t.Fatalf("expected job id in logs:\n%s", out)
	}
}
