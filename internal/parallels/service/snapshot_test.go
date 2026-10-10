package service

import (
	"context"
	"strings"
	"testing"
)

// prlctl emits a JSON object keyed by snapshot id (verified on 27.0.1).
const snapTreeJSON = `{
	"{snap-1}": {"name":"alpha","date":"2026-10-09 15:51:15","state":"poweroff","current":false,"parent":""},
	"{snap-2}": {"name":"beta","date":"2026-10-09 16:00:00","state":"poweron","current":true,"parent":"{snap-1}"}
}`

const snapSingleText = `ID: {snap-1}
Name: alpha
Date: 2026-10-09 15:51:15
State: poweroff
Description: hello: world`

func TestCreateSnapshotArgvAndIDParse(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return "Creating the snapshot...\nThe snapshot with id {abcd-1234} has been successfully created.\n", nil
	}}
	svc := powerService(t, runner, "")

	id, out, err := svc.CreateSnapshot(context.Background(), "guest", "my snap", "a description", "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "{abcd-1234}" {
		t.Fatalf("id = %q", id)
	}
	if !strings.Contains(out, "successfully created") {
		t.Fatalf("out = %q", out)
	}
	if len(runner.lastArgs) != 6 || runner.lastArgs[0] != "snapshot" || runner.lastArgs[1] != "guest" ||
		runner.lastArgs[2] != "-n" || runner.lastArgs[3] != "my snap" || runner.lastArgs[4] != "-d" || runner.lastArgs[5] != "a description" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
}

func TestCreateSnapshotBareAndNoIDFallback(t *testing.T) {
	runner := &fakeRunner{fn: func(_ string, _ []string) (string, error) {
		return "something prlctl printed without the id", nil
	}}
	svc := powerService(t, runner, "")

	id, _, err := svc.CreateSnapshot(context.Background(), "guest", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("id = %q, want empty when unparseable", id)
	}
	// Bare: no -n / -d flags at all.
	if len(runner.lastArgs) != 2 {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
}

func TestSnapshotPasswordStdin(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "The snapshot with id {abcd-1234} has been successfully created.", "", 0
	}}
	svc := powerService(t, runner, "")

	if _, _, err := svc.CreateSnapshot(context.Background(), "guest", "x", "", "enc-pw"); err != nil {
		t.Fatal(err)
	}
	if runner.lastIn != "enc-pw\n" {
		t.Fatalf("stdin = %q", runner.lastIn)
	}

	// Invalid password rejected before prlctl runs.
	runner2 := &fakeRunner{}
	svc2 := powerService(t, runner2, "")
	if _, _, err := svc2.CreateSnapshot(context.Background(), "guest", "", "", "pw\x00bad"); err == nil {
		t.Fatal("want validation error")
	}
	if runner2.calls != 0 {
		t.Fatal("prlctl must not run for an invalid password")
	}
}

func TestDeleteSnapshotArgv(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "The snapshot has been successfully deleted.", "", 0
	}}
	svc := powerService(t, runner, "")

	if _, err := svc.DeleteSnapshot(context.Background(), "guest", "{abcd-1234}", "", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 4 || runner.lastArgs[0] != "snapshot-delete" || runner.lastArgs[1] != "guest" || runner.lastArgs[2] != "-i" || runner.lastArgs[3] != "{abcd-1234}" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}

	if _, err := svc.DeleteSnapshot(context.Background(), "guest", "abcd-1234", "", true); err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 5 || runner.lastArgs[4] != "-c" {
		t.Fatalf("argv = %v, want -c appended", runner.lastArgs)
	}

	// Ids pass through verbatim (braces optional, prlctl normalizes).
	if runner.lastArgs[3] != "abcd-1234" {
		t.Fatalf("id altered: %v", runner.lastArgs)
	}

	// Invalid id rejected before prlctl.
	if _, err := svc.DeleteSnapshot(context.Background(), "guest", "bad\x00id", "", false); err == nil {
		t.Fatal("want validation error")
	}
}

func TestDeleteSnapshotNonZeroExitKeepsPasswordOut(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "", "Failed to delete snapshot: Unable to find the snapshot ID.", 255
	}}
	svc := powerService(t, runner, "")

	_, err := svc.DeleteSnapshot(context.Background(), "guest", "{nope}", "pw-zx9", false)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "Unable to find the snapshot ID") {
		t.Fatalf("error lost prlctl's message: %v", err)
	}
	if strings.Contains(err.Error(), "pw-zx9") {
		t.Fatalf("password leaked: %v", err)
	}
}

func TestListSnapshotsTreeJSON(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return snapTreeJSON, "", 0
	}}
	svc := powerService(t, runner, "")

	res, err := svc.ListSnapshots(context.Background(), "guest", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 3 || runner.lastArgs[2] != "--json" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	if len(res.Snapshots) != 2 {
		t.Fatalf("snapshots = %+v", res.Snapshots)
	}
	a, b := res.Snapshots[0], res.Snapshots[1]
	if a.ID != "{snap-1}" || a.Name != "alpha" || a.Date != "2026-10-09 15:51:15" || a.State != "poweroff" || a.Current || a.Parent != "" {
		t.Fatalf("a = %+v", a)
	}
	if b.ID != "{snap-2}" || !b.Current || b.Parent != "{snap-1}" {
		t.Fatalf("b = %+v", b)
	}
}

func TestListSnapshotsArrayShapeAndSort(t *testing.T) {
	// Array-of-single-key-objects variant + unsorted input: the result
	// must be sorted by date, then id.
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return `[
			{"{b}": {"name":"b","date":"2026-10-09 16:00:00","state":"poweron","current":true,"parent":"{a}"}},
			{"{a}": {"name":"a","date":"2026-10-09 15:00:00","state":"poweroff","current":false,"parent":""}}
		]`, "", 0
	}}
	svc := powerService(t, runner, "")

	res, err := svc.ListSnapshots(context.Background(), "guest", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Snapshots) != 2 || res.Snapshots[0].ID != "{a}" || res.Snapshots[1].ID != "{b}" {
		t.Fatalf("order = %+v, want {a} (15:00) before {b} (16:00)", res.Snapshots)
	}
}

func TestListSnapshotsEmptyTree(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "", "", 0
	}}
	svc := powerService(t, runner, "")

	res, err := svc.ListSnapshots(context.Background(), "guest", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Snapshots) != 0 {
		t.Fatalf("snapshots = %+v, want empty (prlctl prints nothing for an empty tree)", res.Snapshots)
	}
}

func TestListSnapshotsSingleMode(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return snapSingleText, "", 0
	}}
	svc := powerService(t, runner, "")

	res, err := svc.ListSnapshots(context.Background(), "guest", "{snap-1}", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 4 || runner.lastArgs[2] != "-i" || runner.lastArgs[3] != "{snap-1}" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	if res.Snapshot == nil {
		t.Fatal("snapshot = nil")
	}
	s := res.Snapshot
	if s.ID != "{snap-1}" || s.Name != "alpha" || s.State != "poweroff" || s.Description != "hello: world" {
		t.Fatalf("snapshot = %+v", s)
	}

	// Unknown id: prlctl prints nothing and exits 0 → NotFound, no error.
	runner.captureFn = func(_ string, _ []string) (string, string, int) { return "", "", 0 }
	res, err = svc.ListSnapshots(context.Background(), "guest", "{nope}", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NotFound || res.Snapshot != nil {
		t.Fatalf("res = %+v", res)
	}
}

func TestListSnapshotsTreeMode(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "_{snap-1}*{snap-2}", "", 0
	}}
	svc := powerService(t, runner, "")

	res, err := svc.ListSnapshots(context.Background(), "guest", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 3 || runner.lastArgs[2] != "-t" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	if res.Output != "_{snap-1}*{snap-2}" {
		t.Fatalf("output = %q", res.Output)
	}
}

func TestListSnapshotsUnknownShapeKeepsRaw(t *testing.T) {
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return `{"unexpected":"shape"}`, "", 0
	}}
	svc := powerService(t, runner, "")

	res, err := svc.ListSnapshots(context.Background(), "guest", "", "", false)
	if err != nil {
		t.Fatalf("shape change is not an error: %v", err)
	}
	if res.Output != `{"unexpected":"shape"}` {
		t.Fatalf("raw output lost: %+v", res)
	}
}

func TestSwitchSnapshotArgvAndCache(t *testing.T) {
	var listRuns int
	runner := &fakeRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "The VM has been successfully switched.", "", 0
	}}
	runner.fn = func(_ string, args []string) (string, error) {
		if args[0] == "list" {
			listRuns++
		}
		return "[]", nil
	}
	svc := powerService(t, runner, "")
	ctx := context.Background()

	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.SwitchSnapshot(ctx, "guest", "{snap-1}", "", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 4 || runner.lastArgs[0] != "snapshot-switch" || runner.lastArgs[2] != "-i" {
		t.Fatalf("argv = %v", runner.lastArgs)
	}
	if _, err := svc.SwitchSnapshot(ctx, "guest", "{snap-1}", "", true); err != nil {
		t.Fatal(err)
	}
	if len(runner.lastArgs) != 5 || runner.lastArgs[4] != "--skip-resume" {
		t.Fatalf("argv = %v, want --skip-resume appended", runner.lastArgs)
	}

	// Switch flips the VM's State: the next read must re-fetch.
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if listRuns != 2 {
		t.Fatalf("list runs after switch = %d, want 2", listRuns)
	}

	// Create and list do not invalidate.
	runner.fn = func(_ string, _ []string) (string, error) {
		return "The snapshot with id {x} has been successfully created.", nil
	}
	if _, _, err := svc.CreateSnapshot(ctx, "guest", "", "", ""); err != nil {
		t.Fatal(err)
	}
	runner.fn = func(_ string, args []string) (string, error) {
		if args[0] == "list" {
			listRuns++
		}
		return snapTreeJSON, nil
	}
	if _, err := svc.ListSnapshots(ctx, "guest", "", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListVMs(ctx, false); err != nil {
		t.Fatal(err)
	}
	if listRuns != 2 {
		t.Fatalf("list runs after create/list = %d, want 2 (no invalidation)", listRuns)
	}
}
