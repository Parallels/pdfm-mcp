package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeleteArgvAndOutput(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return "  VM removed\n", nil
	}}
	svc := powerService(t, runner, "")
	out, err := svc.Delete(context.Background(), "guest-x")
	if err != nil {
		t.Fatal(err)
	}
	if out != "VM removed" {
		t.Fatalf("out = %q, want trimmed", out)
	}
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" {
		t.Fatalf("exe = %q", exe)
	}
	if len(args) != 2 || args[0] != "delete" || args[1] != "guest-x" {
		t.Fatalf("args = %v, want [delete guest-x]", args)
	}
}

func TestDeleteRunnerErrorSurfaces(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return "", errors.New("prlctl delete guest-x failed: The virtual machine is running")
	}}
	svc := powerService(t, runner, "")
	if _, err := svc.Delete(context.Background(), "guest-x"); err == nil || !strings.Contains(err.Error(), "is running") {
		t.Fatalf("err = %v, want the prlctl error", err)
	}
}

func TestDeleteDisabled(t *testing.T) {
	svc := &service{}
	if _, err := svc.Delete(context.Background(), "guest-x"); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
}

func TestDeleteInvalidRef(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	if _, err := svc.Delete(context.Background(), "  "); err == nil {
		t.Fatal("want error for invalid ref")
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.callCount())
	}
}

func TestDeleteAppliesLongDeadline(t *testing.T) {
	runner := &fakeRunner{delay: time.Millisecond}
	svc := powerService(t, runner, "")
	if _, err := svc.Delete(context.Background(), "guest-x"); err != nil {
		t.Fatal(err)
	}
	deadline, ok := runner.lastDeadline()
	if !ok {
		t.Fatal("no deadline applied")
	}
	rem := time.Until(deadline)
	if rem < 290*time.Second || rem > deleteTimeout+time.Millisecond {
		t.Fatalf("remaining deadline %v, want ~%v", rem, deleteTimeout)
	}
}

// A successful delete must invalidate the cache: the next list call
// re-runs prlctl.
func TestDeleteInvalidatesCache(t *testing.T) {
	runner := &fakeRunner{defaultOut: sampleVMsJSON}
	svc := powerService(t, runner, "")
	ctx := context.Background()

	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", runner.callCount())
	}

	if _, err := svc.Delete(ctx, vmOneID); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 2 {
		t.Fatalf("calls = %d, want 2 (delete ran)", runner.callCount())
	}

	// The cached list is now stale; the next call must fetch again.
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 3 {
		t.Fatalf("calls = %d, want 3 (cache invalidated by delete)", runner.callCount())
	}
}
