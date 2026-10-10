package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

func iP(n int) *int { return &n }

func repeat(e interfaces.KeyEvent, n int) []interfaces.KeyEvent {
	out := make([]interfaces.KeyEvent, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, e)
	}
	return out
}

func TestSendKeyArgvAndStdin(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	evs := []interfaces.KeyEvent{{Key: iP(36)}}
	if _, err := svc.SendKey(context.Background(), "guest-x", evs); err != nil {
		t.Fatal(err)
	}
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" {
		t.Fatalf("exe = %q", exe)
	}
	want := []string{"send-key-event", "guest-x", "-j"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if got := runner.lastStdin(); got != `[{"key":36}]` {
		t.Fatalf("stdin = %q, want %q", got, `[{"key":36}]`)
	}
}

func TestSendKeyWireFormat(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	evs := []interfaces.KeyEvent{
		{Key: iP(13)},
		{Key: iP(7), Event: "press"},
		{Scancode: iP(7), Event: "release", Delay: iP(100)},
		{Delay: iP(100)},
	}
	if _, err := svc.SendKey(context.Background(), "guest-x", evs); err != nil {
		t.Fatal(err)
	}
	want := `[{"key":13},{"key":7,"event":"press"},{"scancode":7,"event":"release","delay":100},{"delay":100}]`
	if got := runner.lastStdin(); got != want {
		t.Fatalf("stdin = %s, want %s", got, want)
	}
}

func TestSendKeyValidationBeforeExec(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	cases := []struct {
		name   string
		evs    []interfaces.KeyEvent
		substr string
	}{
		{"no events", nil, "at least one"},
		{"key and scancode", []interfaces.KeyEvent{{Key: iP(1), Scancode: iP(2)}}, "mutually exclusive"},
		{"empty event", []interfaces.KeyEvent{{}}, "needs a key, a scancode or a delay"},
		{"key zero", []interfaces.KeyEvent{{Key: iP(0)}}, "out of range 1-255"},
		{"key too big", []interfaces.KeyEvent{{Key: iP(256)}}, "out of range 1-255"},
		{"scancode too big", []interfaces.KeyEvent{{Scancode: iP(256)}}, "out of range 0-255"},
		{"negative scancode", []interfaces.KeyEvent{{Scancode: iP(-1)}}, "out of range 0-255"},
		{"bad event", []interfaces.KeyEvent{{Key: iP(1), Event: "hold"}}, "unknown event"},
		{"negative delay", []interfaces.KeyEvent{{Key: iP(1), Delay: iP(-1)}}, "delay -1 out of range"},
		{"delay too big", []interfaces.KeyEvent{{Key: iP(1), Delay: iP(60001)}}, "delay 60001 out of range"},
		{"total delay too big", repeat(interfaces.KeyEvent{Delay: iP(50001)}, 6), "total delay"},
		{"too many events", make([]interfaces.KeyEvent, maxEvents+1), "event limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.SendKey(context.Background(), "guest-x", tc.evs); err == nil || !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("err = %v, want containing %q", err, tc.substr)
			}
		})
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0 (all rejected before exec)", runner.callCount())
	}
}

func TestSendKeyPrlctlFailureIsAnError(t *testing.T) {
	// Unlike exec, a non-zero prlctl exit is an error: there is no guest
	// exit-code concept for key events.
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "", "PrlDevDisplay_ConnectToVm: PrlJob_Wait: PRL_ERR_IO_STOPPED", 255
	}}
	svc := powerService(t, runner, "")
	if _, err := svc.SendKey(context.Background(), "guest-x", []interfaces.KeyEvent{{Key: iP(36)}}); err == nil || !strings.Contains(err.Error(), "PRL_ERR_IO_STOPPED") {
		t.Fatalf("err = %v, want the prlctl error", err)
	}
}

func TestSendKeyRunnerErrorSurfaces(t *testing.T) {
	runner := &fakeRunner{defErr: errors.New("prlctl send-key-event failed: launch error")}
	svc := powerService(t, runner, "")
	if _, err := svc.SendKey(context.Background(), "guest-x", []interfaces.KeyEvent{{Key: iP(36)}}); err == nil || !strings.Contains(err.Error(), "launch error") {
		t.Fatalf("err = %v, want the runner error", err)
	}
}

func TestSendKeyDisabled(t *testing.T) {
	svc := &service{}
	if _, err := svc.SendKey(context.Background(), "guest-x", []interfaces.KeyEvent{{Key: iP(36)}}); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
}

func TestSendKeyInvalidRef(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	if _, err := svc.SendKey(context.Background(), "  ", []interfaces.KeyEvent{{Key: iP(36)}}); err == nil {
		t.Fatal("want error for invalid ref")
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.callCount())
	}
}

func TestSendKeyAppliesLongDeadline(t *testing.T) {
	runner := &fakeRunner{delay: time.Millisecond}
	svc := powerService(t, runner, "")
	if _, err := svc.SendKey(context.Background(), "guest-x", []interfaces.KeyEvent{{Key: iP(36)}}); err != nil {
		t.Fatal(err)
	}
	deadline, ok := runner.lastDeadline()
	if !ok {
		t.Fatal("no deadline applied")
	}
	rem := time.Until(deadline)
	if rem < 290*time.Second || rem > sendKeyTimeout+time.Millisecond {
		t.Fatalf("remaining deadline %v, want ~%v", rem, sendKeyTimeout)
	}
}

func TestSendKeyCacheUntouched(t *testing.T) {
	runner := &fakeRunner{defaultOut: sampleVMsJSON}
	svc := powerService(t, runner, "")
	ctx := context.Background()
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", runner.callCount())
	}
	if _, err := svc.SendKey(ctx, vmOneID, []interfaces.KeyEvent{{Key: iP(36)}}); err != nil {
		t.Fatal(err)
	}
	// The send-key call itself is runner call #2.
	if runner.callCount() != 2 {
		t.Fatalf("calls = %d, want 2", runner.callCount())
	}
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 2 {
		t.Fatalf("calls = %d, want 2 (send-key must not invalidate the cache)", runner.callCount())
	}
}
