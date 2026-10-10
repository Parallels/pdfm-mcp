package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
)

func TestCloneDefaultArgv(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) { return "", nil }}
	svc := powerService(t, runner, "")
	if _, err := svc.Clone(context.Background(), "guest-x", interfaces.CloneRequest{Name: "guest-copy"}); err != nil {
		t.Fatal(err)
	}
	_, args := runner.lastCall()
	want := []string{"clone", "guest-x", "--name", "guest-copy"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestCloneAllOptionsArgv(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) { return "", nil }}
	svc := powerService(t, runner, "")
	_, err := svc.Clone(context.Background(), "guest-x", interfaces.CloneRequest{
		Name:              "guest-copy",
		Template:          true,
		Dst:               " /tmp/dest ",
		RegenerateSrcUUID: true,
		Linked:            true,
		SnapshotID:        "  snap-1  ",
		DetachExternalHDD: "yes",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, args := runner.lastCall()
	want := []string{
		"clone", "guest-x", "--name", "guest-copy",
		"--template",
		"--dst", "/tmp/dest",
		"--regenerate-src-uuid",
		"--linked",
		"--id", "snap-1",
		"--detach-external-hdd", "yes",
	}
	if strings.Join(args, "|") != strings.Join(want, "|") {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestCloneValidation(t *testing.T) {
	cases := []struct {
		name    string
		ref     string
		req     interfaces.CloneRequest
		wantErr string
	}{
		{"empty name", "guest-x", interfaces.CloneRequest{}, "invalid clone name"},
		{"whitespace name", "guest-x", interfaces.CloneRequest{Name: "   "}, "invalid clone name"},
		{"non-printable name", "guest-x", interfaces.CloneRequest{Name: "bad\x00name"}, "invalid clone name"},
		{"long dst", "guest-x", interfaces.CloneRequest{Name: "c", Dst: strings.Repeat("a", maxClonePathLength+1)}, "dst is longer"},
		{"non-printable dst", "guest-x", interfaces.CloneRequest{Name: "c", Dst: "/a\x7fb"}, "invalid dst"},
		{"snapshot without linked", "guest-x", interfaces.CloneRequest{Name: "c", SnapshotID: "snap-1"}, "snapshot_id requires linked"},
		{"long snapshot id", "guest-x", interfaces.CloneRequest{Name: "c", Linked: true, SnapshotID: strings.Repeat("a", MaxVMRefLength+1)}, "snapshot_id is longer"},
		{"bad detach value", "guest-x", interfaces.CloneRequest{Name: "c", DetachExternalHDD: "maybe"}, "detach_external_hdd"},
		{"invalid source ref", "  ", interfaces.CloneRequest{Name: "c"}, "invalid source VM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{}
			svc := powerService(t, runner, "")
			if _, err := svc.Clone(context.Background(), tc.ref, tc.req); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if runner.callCount() != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.callCount())
			}
		})
	}
}

func TestCloneSuccessInvalidatesCache(t *testing.T) {
	runner := &fakeRunner{defaultOut: sampleVMsJSON}
	svc := powerService(t, runner, "")
	ctx := context.Background()

	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", runner.callCount())
	}

	if _, err := svc.Clone(ctx, vmOneID, interfaces.CloneRequest{Name: "copy-one"}); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 2 {
		t.Fatalf("calls = %d, want 2 (clone ran)", runner.callCount())
	}

	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 3 {
		t.Fatalf("calls = %d, want 3 (cache invalidated by clone)", runner.callCount())
	}
}

func TestCloneRunnerErrorSurfaces(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return "", errors.New("prlctl clone guest-x failed: A virtual machine with the same name already exists")
	}}
	svc := powerService(t, runner, "")
	if _, err := svc.Clone(context.Background(), "guest-x", interfaces.CloneRequest{Name: "taken"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want the prlctl error", err)
	}
}

func TestCloneDisabled(t *testing.T) {
	svc := &service{}
	if _, err := svc.Clone(context.Background(), "guest-x", interfaces.CloneRequest{Name: "c"}); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
}

func TestCloneAppliesLongDeadline(t *testing.T) {
	runner := &fakeRunner{delay: time.Millisecond}
	svc := powerService(t, runner, "")
	if _, err := svc.Clone(context.Background(), "guest-x", interfaces.CloneRequest{Name: "c"}); err != nil {
		t.Fatal(err)
	}
	deadline, ok := runner.lastDeadline()
	if !ok {
		t.Fatal("no deadline applied")
	}
	rem := time.Until(deadline)
	if rem < 590*time.Second || rem > cloneTimeout+time.Millisecond {
		t.Fatalf("remaining deadline %v, want ~%v", rem, cloneTimeout)
	}
}
