package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels/interfaces"
)

// powerService initializes the singleton with a fake runner/PATH and the
// given target VM ("" = fleet mode).
func powerService(t *testing.T, runner *fakeRunner, vmID string) interfaces.ParallelsService {
	t.Helper()
	resetBetween(t)
	ok := mustInit(t,
		WithLogger(testLogger()),
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
	return GetInstance()
}

func TestPowerArgvPerOp(t *testing.T) {
	cases := []struct {
		op   interfaces.PowerOp
		want []string
	}{
		{interfaces.PowerStart, []string{"start", "guest"}},
		{interfaces.PowerResume, []string{"resume", "guest"}},
		{interfaces.PowerPause, []string{"pause", "guest"}},
		{interfaces.PowerSuspend, []string{"suspend", "guest"}},
		{interfaces.PowerRestart, []string{"restart", "guest"}},
		{interfaces.PowerReset, []string{"reset", "guest"}},
		{interfaces.PowerResetUptime, []string{"reset-uptime", "guest"}},
		{interfaces.PowerStop, []string{"stop", "guest"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.op), func(t *testing.T) {
			runner := &fakeRunner{defaultOut: "  done\n"}
			svc := powerService(t, runner, "")
			out, err := svc.Power(context.Background(), tc.op, "guest", false, false)
			if err != nil {
				t.Fatal(err)
			}
			if out != "done" {
				t.Fatalf("out = %q, want trimmed %q", out, "done")
			}
			exe, args := runner.lastCall()
			if exe != "/usr/local/bin/prlctl" {
				t.Fatalf("exe = %q", exe)
			}
			if !sameStrings(args, tc.want) {
				t.Fatalf("args = %v, want %v", args, tc.want)
			}
		})
	}
}

func TestPowerStopFlags(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	if _, err := svc.Power(context.Background(), interfaces.PowerStop, "guest", true, true); err != nil {
		t.Fatal(err)
	}
	_, args := runner.lastCall()
	if want := []string{"stop", "guest", "--kill", "--drop-state"}; !sameStrings(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestPowerUnknownOp(t *testing.T) {
	svc := powerService(t, &fakeRunner{}, "")
	if _, err := svc.Power(context.Background(), interfaces.PowerOp("exec"), "guest", false, false); err == nil || !strings.Contains(err.Error(), "unknown power operation") {
		t.Fatalf("err = %v, want unknown power operation", err)
	}
}

// TestUnquoteVMRef pins the LLM shell-quoting tolerance (spec 025): one
// pair of surrounding double quotes is stripped, nothing more.
func TestUnquoteVMRef(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "My VM", want: "My VM"},             // plain: unchanged
		{in: "  My VM  ", want: "My VM"},         // trimmed
		{in: `"My VM"`, want: "My VM"},           // one pair of quotes: stripped
		{in: `  "My  VM"  `, want: "My  VM"},     // surrounding + inner spaces
		{in: `""a""`, want: `"a"`},               // exactly one outer pair
		{in: `"`, want: `"`},                     // a lone quote: unchanged
		{in: `"unbalanced`, want: `"unbalanced`}, // unbalanced: unchanged
		{in: `a"b`, want: `a"b`},                 // interior quote: unchanged
		{in: `""`, want: ""},                     // quotes around nothing
		{in: "", want: ""},
	}
	for _, c := range cases {
		if got := UnquoteVMRef(c.in); got != c.want {
			t.Errorf("UnquoteVMRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestValidateVMRef pins the id/name reference contract (spec 025): plain
// names with spaces are valid as-is, and one pair of surrounding double
// quotes (an LLM shell-quoting habit) is tolerated and stripped.
func TestValidateVMRef(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		isErr  bool
		errSub string
	}{
		{in: "My-VM-UUID", want: "My-VM-UUID"},
		{in: "  My VM  ", want: "My VM"},                   // spaces fine, trimmed
		{in: `"My VM"`, want: "My VM"},                     // one pair of quotes: stripped
		{in: `  "My  VM"  `, want: "My  VM"},               // quotes + surrounding/inner spaces
		{in: `""`, isErr: true, errSub: "empty"},           // quotes around nothing
		{in: `"`, isErr: false, want: `"`},                 // a lone printable char: passed through, prlctl will not find it
		{in: `"balanced`, isErr: false, want: `"balanced`}, // unbalanced: no stripping
		{in: `"a"b"`, want: `a"b`},                         // exactly one outer pair stripped
		{in: "bad\x00name", isErr: true, errSub: "non-printable"},
		{in: strings.Repeat("a", MaxVMRefLength+1), isErr: true, errSub: "longer than"},
		{in: "   ", isErr: true, errSub: "empty"},
	}
	for _, c := range cases {
		got, err := ValidateVMRef(c.in)
		if c.isErr {
			if err == nil || (c.errSub != "" && !strings.Contains(err.Error(), c.errSub)) {
				t.Errorf("ValidateVMRef(%q) err = %v, want containing %q", c.in, err, c.errSub)
			}
			continue
		}
		if err != nil {
			t.Errorf("ValidateVMRef(%q) unexpected err: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ValidateVMRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPowerValidatesRef(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	if _, err := svc.Power(context.Background(), interfaces.PowerStart, "   ", false, false); err == nil {
		t.Fatal("want error for empty ref")
	}
	if _, err := svc.Power(context.Background(), interfaces.PowerStart, "bad\x00id", false, false); err == nil {
		t.Fatal("want error for NUL byte")
	}
	if _, err := svc.Power(context.Background(), interfaces.PowerStart, strings.Repeat("a", MaxVMRefLength+1), false, false); err == nil {
		t.Fatal("want error for over-long ref")
	}
	if runner.callCount() != 0 {
		t.Fatalf("prlctl must not run on invalid ref: %d calls", runner.callCount())
	}
}

func TestPowerFailureSurfaces(t *testing.T) {
	runner := &fakeRunner{defErr: errors.New("prlctl start guest failed: VM not found")}
	svc := powerService(t, runner, "")
	out, err := svc.Power(context.Background(), interfaces.PowerStart, "guest", false, false)
	if err == nil || !strings.Contains(err.Error(), "VM not found") {
		t.Fatalf("err = %v, want prlctl error surfaced", err)
	}
	if out != "" {
		t.Fatalf("out = %q, want empty on failure", out)
	}
}

func TestPowerInvalidatesCache(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, args []string) (string, error) {
		if args[0] == "list" {
			return sampleVMsJSON, nil
		}
		return "", nil
	}}
	svc := powerService(t, runner, "")

	if _, err := svc.ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n := runner.callCount(); n != 1 {
		t.Fatalf("calls = %d, want 1 (initial fetch)", n)
	}

	if _, err := svc.Power(context.Background(), interfaces.PowerStart, vmOneID, false, false); err != nil {
		t.Fatal(err)
	}

	// The power op must have marked the cache stale: the next list re-runs.
	if _, err := svc.ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n := runner.callCount(); n != 3 {
		t.Fatalf("calls = %d, want 3 (power invalidated the cache)", n)
	}
}

func TestPowerAppliesLongDeadline(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	if _, err := svc.Power(context.Background(), interfaces.PowerStart, "guest", false, false); err != nil {
		t.Fatal(err)
	}
	deadline, ok := runner.lastDeadline()
	if !ok {
		t.Fatal("power command must run with a bounded context")
	}
	// The power cap (180s) must apply, not the 30s read default.
	if rem := time.Until(deadline); rem < 100*time.Second {
		t.Fatalf("deadline too close: %s (want ~%s)", rem, powerTimeout)
	}
}

func TestPowerDisabled(t *testing.T) {
	resetBetween(t)
	ok := mustInit(t,
		WithLogger(testLogger()),
		WithLookPath(fakeLookPath(map[string]string{})),
		WithRunner(&fakeRunner{}),
		WithFallbackDirs(),
	)
	if ok {
		t.Fatal("service should be disabled without the CLI")
	}
	if _, err := GetInstance().Power(context.Background(), interfaces.PowerStart, "guest", false, false); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
