package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestExecArgvShapes(t *testing.T) {
	cases := []struct {
		name         string
		command      string
		user         string
		resolvePaths bool
		wantArgs     []string
	}{
		{"default current-user", "ls -la", "", false, []string{"exec", "guest-x", "--current-user", "ls -la"}},
		{"user override", "id", "root", false, []string{"exec", "guest-x", "--user", "root", "id"}},
		{"resolve paths", "cat /host/file", "", true, []string{"exec", "guest-x", "--current-user", "--resolve-paths", "cat /host/file"}},
		{"all options", "run", "builder", true, []string{"exec", "guest-x", "--user", "builder", "--resolve-paths", "run"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{}
			svc := powerService(t, runner, "")
			if _, err := svc.Exec(context.Background(), "guest-x", tc.command, tc.user, tc.resolvePaths); err != nil {
				t.Fatal(err)
			}
			exe, args := runner.lastCall()
			if exe != "/usr/local/bin/prlctl" {
				t.Fatalf("exe = %q", exe)
			}
			if len(args) != len(tc.wantArgs) {
				t.Fatalf("args = %v, want %v", args, tc.wantArgs)
			}
			for i := range args {
				if args[i] != tc.wantArgs[i] {
					t.Fatalf("args = %v, want %v", args, tc.wantArgs)
				}
			}
		})
	}
}

func TestExecPassesThroughResult(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "hello\n", "warn line\n", 0
	}}
	svc := powerService(t, runner, "")
	res, err := svc.Exec(context.Background(), "guest-x", "echo hello", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "hello\n" || res.Stderr != "warn line\n" || res.ExitCode != 0 {
		t.Fatalf("res = %+v", res)
	}
	if res.StdoutTruncated || res.StderrTruncated {
		t.Fatal("no truncation expected")
	}
}

func TestExecNonZeroExitIsNotAnError(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "", "no matches", 1
	}}
	svc := powerService(t, runner, "")
	res, err := svc.Exec(context.Background(), "guest-x", "grep -q x /dev/null", "", false)
	if err != nil {
		t.Fatalf("non-zero exit must not be an error: %v", err)
	}
	if res.ExitCode != 1 || res.Stderr != "no matches" {
		t.Fatalf("res = %+v", res)
	}
}

func TestExecRunnerErrorSurfaces(t *testing.T) {
	runner := &fakeRunner{defErr: errors.New("prlctl exec guest-x failed: PrlDevDisplay_ConnectToVm: PRL_ERR_IO_STOPPED")}
	svc := powerService(t, runner, "")
	if _, err := svc.Exec(context.Background(), "guest-x", "id", "", false); err == nil || !strings.Contains(err.Error(), "PRL_ERR_IO_STOPPED") {
		t.Fatalf("err = %v, want the prlctl error", err)
	}
}

func TestExecContextCancel(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(100*time.Millisecond))
	defer cancel()
	runner := &fakeRunner{delay: 5 * time.Second}
	svc := powerService(t, runner, "")
	if _, err := svc.Exec(ctx, "guest-x", "sleep 10", "", false); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("err = %v, want deadline error", err)
	}
}

func TestExecValidationBeforeExec(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	cases := []struct {
		name     string
		vmRef    string
		command  string
		user     string
		wantPart string
	}{
		{"invalid vmRef", "", "id", "", "VM id or name"},
		{"NUL in vmRef", "bad\x00vm", "id", "", "VM id or name"},
		{"over-long vmRef", strings.Repeat("a", MaxVMRefLength+1), "id", "", "VM id or name"},
		{"empty command", "guest-x", "", "", "empty"},
		{"whitespace command", "guest-x", "   \n\t ", "", "empty"},
		{"NUL in command", "guest-x", "id\x00extra", "", "NUL"},
		{"over-long command", "guest-x", strings.Repeat("a", maxCommandLength+1), "", "limit"},
		{"over-long user", "guest-x", "id", strings.Repeat("u", MaxVMRefLength+1), "VM id or name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Exec(context.Background(), tc.vmRef, tc.command, tc.user, false); err == nil || !strings.Contains(err.Error(), tc.wantPart) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantPart)
			}
		})
	}
	if runner.callCount() != 0 {
		t.Fatalf("prlctl must not run on invalid input: %d calls", runner.callCount())
	}
}

func TestExecDisabled(t *testing.T) {
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
	if _, err := GetInstance().Exec(context.Background(), "guest", "id", "", false); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("err = %v, want ErrNotEnabled", err)
	}
}

func TestExecAppliesLongDeadline(t *testing.T) {
	runner := &fakeRunner{}
	svc := powerService(t, runner, "")
	before := time.Now()
	if _, err := svc.Exec(context.Background(), "guest-x", "id", "", false); err != nil {
		t.Fatal(err)
	}
	deadline, ok := runner.lastDeadline()
	if !ok {
		t.Fatal("no deadline on the exec context")
	}
	rem := deadline.Sub(before)
	if rem < 290*time.Second || rem > 300*time.Second+time.Millisecond {
		t.Fatalf("remaining deadline %v, want ~300s", rem)
	}
}

func TestExecTruncatesOutput(t *testing.T) {
	// ASCII: exactly 1 MB+1 → cut to 1 MB, flagged.
	big := strings.Repeat("a", maxExecStdout+1)
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return big, "", 0
	}}
	svc := powerService(t, runner, "")
	res, err := svc.Exec(context.Background(), "guest-x", "cat big", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != maxExecStdout || !res.StdoutTruncated {
		t.Fatalf("stdout len = %d truncated = %v", len(res.Stdout), res.StdoutTruncated)
	}

	// Multi-byte: "€" is 3 bytes; 1 MB is not a multiple of 3, so the cut
	// lands mid-rune and must back off to a boundary. Fresh service: the
	// service binds the runner it was built with.
	runes := strings.Repeat("€", maxExecStdout) // 3 MB of text
	runner2 := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return runes, "", 0
	}}
	svc2 := powerService(t, runner2, "")
	res, err = svc2.Exec(context.Background(), "guest-x", "cat big2", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(res.Stdout) {
		t.Fatal("truncated stdout is not valid UTF-8")
	}
	if len(res.Stdout) > maxExecStdout || !res.StdoutTruncated {
		t.Fatalf("stdout len = %d truncated = %v", len(res.Stdout), res.StdoutTruncated)
	}
	wantRuneCount := maxExecStdout / 3 // 1048575 bytes / 3
	if res.Stdout != strings.Repeat("€", wantRuneCount) {
		t.Fatalf("rune-backoff cut wrong: %d runes", len(res.Stdout)/3)
	}

	// Stderr cap: 256 KB+1.
	runner3 := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "", strings.Repeat("e", maxExecStderr+1), 0
	}}
	svc3 := powerService(t, runner3, "")
	res, err = svc3.Exec(context.Background(), "guest-x", "cat big3", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stderr) != maxExecStderr || !res.StderrTruncated || res.StdoutTruncated {
		t.Fatalf("stderr len = %d truncated = %v stdoutTruncated = %v", len(res.Stderr), res.StderrTruncated, res.StdoutTruncated)
	}
}

func TestExecDoesNotInvalidateCache(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, args []string) (string, error) {
		if args[0] == "list" {
			return sampleVMsJSON, nil
		}
		return "", nil
	}}
	svc := powerService(t, runner, "")

	first, err := svc.ListVMs(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Refreshed {
		t.Fatal("first list must refresh")
	}

	if _, err := svc.Exec(context.Background(), vmOneID, "true", "", false); err != nil {
		t.Fatal(err)
	}

	second, err := svc.ListVMs(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Refreshed {
		t.Fatal("exec must not invalidate the cache; second list was re-fetched")
	}
}
