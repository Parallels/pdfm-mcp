package service

import (
	"context"
	"strings"
	"testing"
)

const reclaimEstimateJSON = `{"diskSpace":{"hardDisks":1204224,"snapshots":1024,"miscellaneous":1081344,"reclaimable":4096}}`

func TestReclaimDiskEstimateArgvAndParse(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return reclaimEstimateJSON, nil
	}}
	svc := powerService(t, runner, "")

	res, err := svc.ReclaimDisk(context.Background(), "guest", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 4 || runner.lastArgs[0] != "reclaim-disk" || runner.lastArgs[1] != "guest" || runner.lastArgs[2] != "--estimate" || runner.lastArgs[3] != "--json" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	if runner.lastIn != "" {
		t.Fatalf("estimate without a password must not feed stdin: %q", runner.lastIn)
	}
	if !res.Estimate {
		t.Fatal("Estimate = false, want true")
	}
	if res.DiskSpace == nil || res.DiskSpace.HardDisks != 1204224 || res.DiskSpace.Snapshots != 1024 || res.DiskSpace.Miscellaneous != 1081344 || res.DiskSpace.Reclaimable != 4096 {
		t.Fatalf("DiskSpace = %+v", res.DiskSpace)
	}
	if res.Output != reclaimEstimateJSON {
		t.Fatalf("Output = %q", res.Output)
	}
}

func TestReclaimDiskRealRunArgv(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "  Reclaiming disk, please wait...\n", "", 0
	}}
	svc := powerService(t, runner, "")

	res, err := svc.ReclaimDisk(context.Background(), "guest", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 2 || runner.lastArgs[0] != "reclaim-disk" || runner.lastArgs[1] != "guest" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	if res.Estimate || res.DiskSpace != nil {
		t.Fatalf("real run must not parse an estimate: %+v", res)
	}
	if res.Output != "Reclaiming disk, please wait..." {
		t.Fatalf("Output = %q", res.Output)
	}
}

func TestReclaimDiskAllowShutdownArgv(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "Reclaiming disk, please wait...", "", 0
	}}
	svc := powerService(t, runner, "")

	if _, err := svc.ReclaimDisk(context.Background(), "guest", "", false, true); err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 3 || runner.lastArgs[2] != "--allow-shutdown" {
		t.Fatalf("argv = %v, want reclaim-disk guest --allow-shutdown", runner.lastArgs)
	}

	// allow_shutdown is a real-run flag: an estimate never carries it.
	runner.fn = func(_ string, _ []string) (string, error) { return reclaimEstimateJSON, nil }
	if _, err := svc.ReclaimDisk(context.Background(), "guest", "", true, true); err != nil {
		t.Fatal(err)
	}
	for _, a := range runner.lastArgs {
		if a == "--allow-shutdown" {
			t.Fatalf("estimate must not carry --allow-shutdown: %v", runner.lastArgs)
		}
	}
}

func TestReclaimDiskPasswordStdin(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "Reclaiming disk, please wait...", "", 0
	}}
	svc := powerService(t, runner, "")

	if _, err := svc.ReclaimDisk(context.Background(), "guest", "enc-pw", false, false); err != nil {
		t.Fatal(err)
	}
	if runner.lastIn != "enc-pw\n" {
		t.Fatalf("stdin = %q, want the single password line", runner.lastIn)
	}

	// An invalid password is rejected before prlctl runs.
	runner2 := &fakeRunner{}
	svc2 := powerService(t, runner2, "")
	if _, err := svc2.ReclaimDisk(context.Background(), "guest", "pw\x00bad", true, false); err == nil {
		t.Fatal("want validation error")
	}
	if runner2.calls != 0 {
		t.Fatal("prlctl must not run for an invalid password")
	}
}

func TestReclaimDiskNonZeroExitKeepsPasswordOut(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "", "An error occurred while compressing the hard disk of \"guest\".", 255
	}}
	svc := powerService(t, runner, "")

	_, err := svc.ReclaimDisk(context.Background(), "guest", "pw-zx9-invalid", false, false)
	if err == nil {
		t.Fatal("want error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "compressing the hard disk") {
		t.Fatalf("error lost prlctl's message: %v", err)
	}
	if strings.Contains(err.Error(), "pw-zx9-invalid") {
		t.Fatalf("password leaked into the error: %v", err)
	}
}

func TestReclaimDiskCacheInvalidation(t *testing.T) {
	var listRuns int
	runner := &fakeRunner{}
	runner.fn = func(_ string, args []string) (string, error) {
		if args[0] == "list" {
			listRuns++
			return "[]", nil
		}
		return reclaimEstimateJSON, nil
	}
	runner.captureFn = func(_ string, _ []string) (string, string, int) {
		return "Reclaiming disk, please wait...", "", 0
	}
	svc := powerService(t, runner, "")
	ctx := context.Background()

	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if listRuns != 1 {
		t.Fatalf("list runs = %d, want 1", listRuns)
	}

	// A real run invalidates.
	if _, err := svc.ReclaimDisk(ctx, "guest", "", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if listRuns != 2 {
		t.Fatalf("list runs after real reclaim = %d, want 2", listRuns)
	}

	// An estimate changes nothing: no extra re-fetch.
	if _, err := svc.ReclaimDisk(ctx, "guest", "", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if listRuns != 2 {
		t.Fatalf("list runs after estimate = %d, want 2 (estimate must not invalidate)", listRuns)
	}
}
