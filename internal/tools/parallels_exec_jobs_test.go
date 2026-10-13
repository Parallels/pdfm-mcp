package tools_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
	"github.com/Parallels/pdfm-mcp/internal/tools"
)

// jobProc is a controllable fake streaming process.
type jobProc struct {
	stdout *parallels.OutputBuffer
	stderr *parallels.OutputBuffer
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	code   int
}

func newJobProc(ctx context.Context) *jobProc {
	p := &jobProc{
		stdout: parallels.NewOutputBuffer(1 << 20),
		stderr: parallels.NewOutputBuffer(1 << 20),
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

func (p *jobProc) finish(code int) {
	p.once.Do(func() {
		p.mu.Lock()
		p.code = code
		p.mu.Unlock()
		close(p.done)
	})
}

func (p *jobProc) Stdout() *parallels.OutputBuffer { return p.stdout }

func (p *jobProc) Stderr() *parallels.OutputBuffer { return p.stderr }

func (p *jobProc) Done() <-chan struct{} { return p.done }

func (p *jobProc) Result() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code, nil
}

func (p *jobProc) Kill() error {
	p.finish(-1)
	return nil
}

// procSink collects started processes.
type procSink struct {
	mu    sync.Mutex
	procs []*jobProc
}

func (s *procSink) start(ctx context.Context, _ string, _ []string) (parallels.Process, error) {
	p := newJobProc(ctx)
	s.mu.Lock()
	s.procs = append(s.procs, p)
	s.mu.Unlock()
	return p, nil
}

func (s *procSink) last() *jobProc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.procs[len(s.procs)-1]
}

// jobsSession registers the exec + job tools on a fresh server (independent
// of server.New) and returns a connected client session.
func jobsSession(t *testing.T, mode tools.PowerMode) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := mcp.NewServer(&mcp.Implementation{Name: "jobs-test", Version: "v0"}, nil)
	tools.RegisterParallelsVMExec(srv, mode)
	tools.RegisterParallelsVMExecStart(srv, mode)
	tools.RegisterParallelsVMExecResult(srv, mode)
	tools.RegisterParallelsVMExecCancel(srv, mode)

	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sess.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "jobs-client", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	return res, sc
}

func requiredOf(t *testing.T, cs *mcp.ClientSession, name string) []string {
	t.Helper()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range list.Tools {
		if tl.Name != name {
			continue
		}
		schema, ok := tl.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: schema = %T", name, tl.InputSchema)
		}
		var out []string
		raw, _ := schema["required"].([]any)
		for _, r := range raw {
			out = append(out, r.(string))
		}
		return out
	}
	t.Fatalf("%s not listed", name)
	return nil
}

func TestExecElevatedReachesArgvWithoutIdentityFlags(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	if _, _, err := tools.VMExec(context.Background(), nil, tools.ExecInput{
		VMID: "guest-x", Command: "whoami", Elevated: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, args := runner.lastCall()
	if want := []string{"exec", "guest-x", "whoami"}; !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}

	if _, _, err := tools.VMExec(context.Background(), nil, tools.ExecInput{
		VMID: "guest-x", Command: "cat /host/f", Elevated: true, ResolvePaths: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, args = runner.lastCall()
	if want := []string{"exec", "guest-x", "--resolve-paths", "cat", "/host/f"}; !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestExecElevatedWithUserIsValidationError(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, _, err := tools.VMExec(context.Background(), nil, tools.ExecInput{
		VMID: "guest-x", Command: "id", Elevated: true, User: "root",
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v", err)
	}
	if runner.count() != 0 {
		t.Fatal("prlctl must not run")
	}
}

func TestExecJobsProtocolFleet(t *testing.T) {
	sink := &procSink{}
	runner := &testRunner{startFn: sink.start}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	cs := jobsSession(t, tools.PowerFleet)

	if req := requiredOf(t, cs, "parallels_vm_exec_start"); !slices.Contains(req, "vm_id") || !slices.Contains(req, "command") {
		t.Fatalf("start required = %v", req)
	}
	if req := requiredOf(t, cs, "parallels_vm_exec_result"); slices.Contains(req, "vm_id") || !slices.Contains(req, "job_id") {
		t.Fatalf("result required = %v", req)
	}
	if req := requiredOf(t, cs, "parallels_vm_exec_cancel"); slices.Contains(req, "vm_id") || !slices.Contains(req, "job_id") {
		t.Fatalf("cancel required = %v", req)
	}

	// Descriptions and annotations flag the privileged mode.
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range list.Tools {
		switch tl.Name {
		case "parallels_vm_exec", "parallels_vm_exec_start":
			if !strings.Contains(tl.Description, "PRIVILEGED") {
				t.Fatalf("%s description must say PRIVILEGED: %s", tl.Name, tl.Description)
			}
			if tl.Annotations == nil || tl.Annotations.ReadOnlyHint || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint {
				t.Fatalf("%s annotations = %+v", tl.Name, tl.Annotations)
			}
		case "parallels_vm_exec_result":
			if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
				t.Fatalf("result must be read-only: %+v", tl.Annotations)
			}
		}
	}

	res, sc := callTool(t, cs, "parallels_vm_exec_start", map[string]any{
		"vm_id": "guest-x", "command": "make build", "elevated": true,
	})
	if res.IsError || sc == nil {
		t.Fatalf("start failed: %+v", res.Content)
	}
	jobID, _ := sc["job_id"].(string)
	if len(jobID) != 32 || sc["state"] != "running" || sc["elevated"] != true || sc["timeout_seconds"] != float64(1800) || sc["vm_id"] != "guest-x" {
		t.Fatalf("start = %v", sc)
	}
	_, args := runner.lastCall()
	if want := []string{"exec", "guest-x", "make", "build"}; !slices.Equal(args, want) {
		t.Fatalf("argv = %v, want %v", args, want)
	}

	p := sink.last()
	if _, err := p.stdout.Write([]byte("compiling")); err != nil {
		t.Fatal(err)
	}
	res, sc = callTool(t, cs, "parallels_vm_exec_result", map[string]any{"job_id": jobID})
	if res.IsError || sc["state"] != "running" || sc["stdout"] != "compiling" || sc["stdout_next"] != float64(9) || sc["exit_code"] != float64(-1) {
		t.Fatalf("running result = %v (%+v)", sc, res.Content)
	}

	p.finish(0)
	res, sc = callTool(t, cs, "parallels_vm_exec_result", map[string]any{
		"job_id": jobID, "wait_ms": 3000, "stdout_offset": 9,
	})
	if res.IsError || sc["state"] != "succeeded" || sc["exit_code"] != float64(0) || sc["stdout"] != "" {
		t.Fatalf("final result = %v (%+v)", sc, res.Content)
	}

	// Cancel of a finished job is a no-op.
	res, sc = callTool(t, cs, "parallels_vm_exec_cancel", map[string]any{"job_id": jobID})
	if res.IsError || sc["was_running"] != false || sc["state"] != "succeeded" {
		t.Fatalf("cancel finished = %v", sc)
	}

	// Cancel of a running job kills it.
	_, sc = callTool(t, cs, "parallels_vm_exec_start", map[string]any{"vm_id": "guest-x", "command": "sleep 999"})
	job2, _ := sc["job_id"].(string)
	res, sc = callTool(t, cs, "parallels_vm_exec_cancel", map[string]any{"job_id": job2})
	if res.IsError || sc["was_running"] != true || sc["state"] != "cancelled" {
		t.Fatalf("cancel running = %v (%+v)", sc, res.Content)
	}

	// Validation and unknown ids are tool errors.
	for name, args := range map[string]map[string]any{
		"parallels_vm_exec_result": {"job_id": jobID, "wait_ms": 30001},
		"parallels_vm_exec_start":  {"vm_id": "guest-x", "command": "id", "timeout_seconds": 14401},
	} {
		bad, _ := callTool(t, cs, name, args)
		if !bad.IsError {
			t.Fatalf("%s %v must be an error", name, args)
		}
	}
	bad, _ := callTool(t, cs, "parallels_vm_exec_result", map[string]any{"job_id": "00000000000000000000000000000000"})
	if !bad.IsError {
		t.Fatal("unknown job id must be an error")
	}
	bad, _ = callTool(t, cs, "parallels_vm_exec_start", map[string]any{"vm_id": "guest-x", "command": "id", "elevated": true, "user": "root"})
	if !bad.IsError {
		t.Fatal("elevated+user must be an error")
	}
}

func TestExecJobsProtocolVMMode(t *testing.T) {
	sink := &procSink{}
	runner := &testRunner{startFn: sink.start}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)
	cs := jobsSession(t, tools.PowerVM)

	if req := requiredOf(t, cs, "parallels_vm_exec_start"); slices.Contains(req, "vm_id") || !slices.Contains(req, "command") {
		t.Fatalf("VM mode start required = %v", req)
	}

	bad, _ := callTool(t, cs, "parallels_vm_exec_start", map[string]any{"vm_id": vmTwoID, "command": "id"})
	if !bad.IsError {
		t.Fatal("explicit vm_id must be rejected in VM mode")
	}

	res, sc := callTool(t, cs, "parallels_vm_exec_start", map[string]any{"command": "make", "timeout_seconds": 60})
	if res.IsError {
		t.Fatalf("start failed: %+v", res.Content)
	}
	if sc["vm_id"] != vmOneID || sc["timeout_seconds"] != float64(60) {
		t.Fatalf("start = %v", sc)
	}
	_, args := runner.lastCall()
	if want := []string{"exec", vmOneID, "--current-user", "make"}; !slices.Equal(args, want) {
		t.Fatalf("argv = %v, want %v", args, want)
	}
	jobID, _ := sc["job_id"].(string)

	sink.last().finish(2)
	_, sc = callTool(t, cs, "parallels_vm_exec_result", map[string]any{"job_id": jobID, "wait_ms": 3000})
	if sc["state"] != "failed" || sc["exit_code"] != float64(2) || sc["vm_id"] != vmOneID {
		t.Fatalf("result = %v", sc)
	}
}

func TestExecJobsDeadlineDoesNotBelongToRequest(t *testing.T) {
	// A job must survive the end of the tool call that started it.
	sink := &procSink{}
	runner := &testRunner{startFn: sink.start}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx, cancel := context.WithCancel(context.Background())
	_, out, err := tools.VMExecStart(ctx, nil, tools.ExecStartInput{VMID: "guest-x", Command: "make"})
	if err != nil {
		t.Fatal(err)
	}
	cancel() // the request context ends here
	time.Sleep(50 * time.Millisecond)

	_, res, err := tools.VMExecResult(context.Background(), nil, tools.ExecResultInput{JobID: out.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "running" {
		t.Fatalf("state = %s, job must outlive the request context", res.State)
	}
	parallels.Shutdown()
}
