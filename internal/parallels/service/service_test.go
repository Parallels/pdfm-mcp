package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sampleVMsJSON is two VMs in the shape of `prlctl list -a -i --json`
// (Parallels 27 sample, trimmed).
const sampleVMsJSON = `[
	{
		"ID": "90f90e2b-e19f-4429-86b3-b5146bc860d4",
		"Name": "ubuntu_github_builder",
		"Description": "",
		"Type": "VM",
		"State": "stopped",
		"OS": "ubuntu",
		"Template": "no",
		"Uptime": "2104507",
		"Home path": "/Volumes/local_storage_m2/Parallels/ubuntu_github_builder.pvm/config.pvs",
		"Home": "/Volumes/local_storage_m2/Parallels/ubuntu_github_builder.pvm/",
		"Restore Image": "",
		"GuestTools": { "state": "outdated", "version": "20.3.0-55895" },
		"Startup and Shutdown": { "Autostart": "user-login", "Autostart delay": 5, "Autostop": "suspend" },
		"Security": { "Encrypted": "no", "Protected": "no", "Archived": "no", "Packed": "no" },
		"Network": { "Conditioned": "off", "ipAddresses": [] },
		"Hardware": {
			"cpu": { "cpus": 2, "auto": "off", "VT-x": true, "hotplug": false, "accl": "high", "mode": "64", "type": "arm" },
			"memory": { "size": "4096Mb", "auto": "off" },
			"video": { "adapter-type": "virtio", "size": "0Mb", "3d-acceleration": "highest" },
			"hdd0": { "enabled": true, "port": "sata:0", "image": "/Volumes/local_storage_m2/Parallels/ubuntu_github_builder.pvm/Ubuntu 24.04 BaseImage-0.hdd", "type": "expanded", "size": "65536Mb", "online-compact": "on" },
			"net0": { "enabled": true, "type": "shared", "mac": "001C42F101D1", "card": "virtio" },
			"sound0": { "enabled": true, "output": "Default", "mixer": "Default" },
			"usb": { "enabled": true }
		}
	},
	{
		"ID": "7f7436c5-af65-4533-b4cf-13496a71fb7e",
		"Name": "windows11",
		"Type": "VM",
		"State": "running",
		"OS": "windows",
		"Uptime": "3600",
		"Home path": "/Volumes/other/windows11.pvm/config.pvs",
		"Home": "/Volumes/other/windows11.pvm/",
		"Hardware": {
			"cpu": { "cpus": 4, "type": "arm" },
			"memory": { "size": "8192Mb" }
		}
	}
]`

const vmOneID = "90f90e2b-e19f-4429-86b3-b5146bc860d4"
const vmTwoID = "7f7436c5-af65-4533-b4cf-13496a71fb7e"

// fakeRunner records calls and answers from fn (or defaultOut).
type fakeRunner struct {
	mu         sync.Mutex
	calls      int
	lastExe    string
	lastArgs   []string
	fn         func(exe string, args []string) (string, error)
	defaultOut string
	defErr     error
	delay      time.Duration
}

func (f *fakeRunner) Run(ctx context.Context, exe string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.lastExe = exe
	f.lastArgs = append([]string(nil), args...)
	f.mu.Unlock()

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if f.fn != nil {
		return f.fn(exe, args)
	}
	return f.defaultOut, f.defErr
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeRunner) lastCall() (string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastExe, f.lastArgs
}

// targetFilteringFake emulates `prlctl list <target> -a -i --json`: it
// returns only the sample VMs matching the target (by ID or name).
func targetFilteringFake() func(exe string, args []string) (string, error) {
	var all []map[string]any
	if err := json.Unmarshal([]byte(sampleVMsJSON), &all); err != nil {
		panic(err)
	}
	return func(_ string, args []string) (string, error) {
		target := ""
		if len(args) > 1 && args[0] == "list" {
			target = args[1]
		}
		if target == "" {
			return sampleVMsJSON, nil
		}
		matched := make([]map[string]any, 0, 1)
		for _, vm := range all {
			if vm["ID"] == target || vm["Name"] == target {
				matched = append(matched, vm)
			}
		}
		out, err := json.Marshal(matched)
		return string(out), err
	}
}

func fakeLookPath(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// resetBetween isolates the singleton per test.
func resetBetween(t *testing.T) {
	t.Helper()
	Reset()
	t.Cleanup(Reset)
}

func mustInit(t *testing.T, opts ...Option) bool {
	t.Helper()
	_, ok := Initialize(opts...)
	return ok
}

func TestInitializeDetection(t *testing.T) {
	cases := []struct {
		name   string
		found  map[string]string
		fbDir  string // dir containing the binaries for the fallback check
		wantOK bool
	}{
		{"both on PATH", map[string]string{"prlctl": "/usr/local/bin/prlctl", "prlsrvctl": "/usr/local/bin/prlsrvctl"}, "", true},
		{"prlctl missing", map[string]string{"prlsrvctl": "/usr/local/bin/prlsrvctl"}, "", false},
		{"prlsrvctl missing", map[string]string{"prlctl": "/usr/local/bin/prlctl"}, "", false},
		{"none on PATH, none in fallback", map[string]string{}, "empty-dir", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetBetween(t)
			var fb []string
			if tc.fbDir != "" {
				fb = []string{tc.fbDir}
			}
			ok := mustInit(t, WithLogger(testLogger()), WithLookPath(fakeLookPath(tc.found)), WithFallbackDirs(fb...))
			if ok != tc.wantOK {
				t.Fatalf("enabled = %v, want %v", ok, tc.wantOK)
			}
			if ok {
				svc := GetInstance()
				if !svc.Enabled() || svc.PRLCtlPath() == "" || svc.PRLSrvCtlPath() == "" {
					t.Fatalf("bad paths: %+v", svc)
				}
			}
		})
	}
}

func TestInitializeFallbackDir(t *testing.T) {
	resetBetween(t)
	dir := t.TempDir()
	for _, name := range []string{"prlctl", "prlsrvctl"} {
		if err := writeFile(filepath.Join(dir, name), "#!/bin/sh\n"); err != nil {
			t.Fatal(err)
		}
	}

	ok := mustInit(t, WithLogger(testLogger()), WithLookPath(fakeLookPath(nil)), WithFallbackDirs(dir))
	if !ok {
		t.Fatal("expected module enabled via fallback dir")
	}
	if got := GetInstance().PRLCtlPath(); got != filepath.Join(dir, "prlctl") {
		t.Fatalf("PRLCtlPath = %q", got)
	}
}

func TestListVMsParsesCache(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: sampleVMsJSON}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound()))) {
		t.Fatal("module not enabled")
	}

	res, err := GetInstance().ListVMs(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	vms, fetchedAt := res.VMs, res.FetchedAt
	if len(vms) != 2 {
		t.Fatalf("got %d VMs, want 2", len(vms))
	}
	if vms[0].ID != vmOneID || vms[0].Name != "ubuntu_github_builder" || vms[0].State != "stopped" {
		t.Fatalf("bad first VM: %+v", vms[0])
	}
	if vms[0].Uptime != "2104507" || vms[0].HomePath == "" || vms[0].GuestTools.State != "outdated" {
		t.Fatalf("bad first VM fields: %+v", vms[0])
	}
	if vms[0].Hardware.CPU.Cpus != 2 || vms[0].Hardware.Memory.Size != "4096Mb" || vms[0].Hardware.Net0.MAC != "001C42F101D1" {
		t.Fatalf("bad hardware: %+v", vms[0].Hardware)
	}
	if vms[1].Name != "windows11" || vms[1].State != "running" || vms[1].Hardware.CPU.Cpus != 4 {
		t.Fatalf("bad second VM: %+v", vms[1])
	}
	if fetchedAt.IsZero() {
		t.Fatal("fetchedAt is zero")
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.callCount())
	}
	exe, args := runner.lastCall()
	if exe != "/usr/local/bin/prlctl" {
		t.Fatalf("exe = %q", exe)
	}
	want := []string{"list", "-a", "-i", "--json"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}

	// Second call is served from the cache.
	if _, err := GetInstance().ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner calls = %d, want 1 (cache hit)", runner.callCount())
	}
}

func TestListVMsVMModeTargetsSingleVM(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{fn: targetFilteringFake()}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound())), WithVMID(vmTwoID)) {
		t.Fatal("module not enabled")
	}

	res, err := GetInstance().ListVMs(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	vms := res.VMs
	if len(vms) != 1 || vms[0].ID != vmTwoID {
		t.Fatalf("got %v, want only %s", vms, vmTwoID)
	}
	if _, args := runner.lastCall(); strings.Join(args, " ") != "list "+vmTwoID+" -a -i --json" {
		t.Fatalf("args = %v", args)
	}
}

func TestListVMsVMModeByNameResolvesID(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{fn: targetFilteringFake()}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound())), WithVMID("ubuntu_github_builder")) {
		t.Fatal("module not enabled")
	}

	res, err := GetInstance().ListVMs(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	vms := res.VMs
	if len(vms) != 1 || vms[0].ID != vmOneID {
		t.Fatalf("got %v, want only %s", vms, vmOneID)
	}

	rawRes, err := GetInstance().VMRaw(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	raw := rawRes.Blob
	var blob map[string]any
	if err := json.Unmarshal(raw, &blob); err != nil {
		t.Fatal(err)
	}
	if blob["ID"] != vmOneID || blob["Name"] != "ubuntu_github_builder" {
		t.Fatalf("raw blob mismatch: %v", blob)
	}
}

func TestListVMsVMModeUnknown(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: "[]"}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound())), WithVMID("nope-123")) {
		t.Fatal("module not enabled")
	}
	_, err := GetInstance().ListVMs(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestListVMsEmptyListIsNotAnError(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: "[]"}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound()))) {
		t.Fatal("module not enabled")
	}
	res, err := GetInstance().ListVMs(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	vms := res.VMs
	if len(vms) != 0 {
		t.Fatalf("got %d VMs, want 0", len(vms))
	}
}

func TestListVMsMalformedOutput(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: "definitely not json"}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound()))) {
		t.Fatal("module not enabled")
	}
	_, err := GetInstance().ListVMs(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "unparseable") {
		t.Fatalf("err = %v, want unparseable", err)
	}
}

func TestListVMsMissingIDEntry(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: `[{"Name": "x"}]`}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound()))) {
		t.Fatal("module not enabled")
	}
	_, err := GetInstance().ListVMs(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "missing its ID") {
		t.Fatalf("err = %v, want missing ID", err)
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: sampleVMsJSON}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound())), WithCacheTTL(60*time.Millisecond)) {
		t.Fatal("module not enabled")
	}

	if _, err := GetInstance().ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := GetInstance().ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("calls = %d, want 1 (within TTL)", runner.callCount())
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := GetInstance().ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 2 {
		t.Fatalf("calls = %d, want 2 (TTL expired)", runner.callCount())
	}
}

func TestRefreshForce(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: sampleVMsJSON}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound())), WithCacheTTL(time.Hour)) {
		t.Fatal("module not enabled")
	}
	if _, err := GetInstance().ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := GetInstance().ListVMs(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 2 {
		t.Fatalf("calls = %d, want 2 (force refresh)", runner.callCount())
	}
}

func TestRefreshSingleFlight(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: sampleVMsJSON, delay: 150 * time.Millisecond}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound())), WithCacheTTL(time.Millisecond)) {
		t.Fatal("module not enabled")
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = GetInstance().ListVMs(context.Background(), false)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if got := runner.callCount(); got != 1 {
		t.Fatalf("calls = %d, want 1 (single-flight)", got)
	}
}

func TestFetchErrorRetainsStaleCache(t *testing.T) {
	resetBetween(t)
	var failNow atomic.Bool
	runner := &fakeRunner{fn: func(string, []string) (string, error) {
		if failNow.Load() {
			return "", errors.New("prlctl exploded")
		}
		return sampleVMsJSON, nil
	}}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound())), WithCacheTTL(time.Millisecond)) {
		t.Fatal("module not enabled")
	}

	if _, err := GetInstance().ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // let the 1ms TTL expire
	failNow.Store(true)

	// Stale (TTL=0) + broken fetch: the error is surfaced…
	if _, err := GetInstance().ListVMs(context.Background(), false); err == nil {
		t.Fatal("want error from broken fetch")
	}

	// …and once the fetch recovers, the list is served again.
	failNow.Store(false)
	time.Sleep(2 * time.Millisecond)
	res, err := GetInstance().ListVMs(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	vms := res.VMs
	if len(vms) != 2 {
		t.Fatalf("got %d VMs after recovery, want 2", len(vms))
	}
}

func TestVMRawRequiresVMMode(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: sampleVMsJSON}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound()))) {
		t.Fatal("module not enabled")
	}
	if _, err := GetInstance().VMRaw(context.Background(), false); err == nil {
		t.Fatal("want error in fleet mode")
	}
}

func TestVMRawUnknownAfterCache(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: sampleVMsJSON}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound())), WithVMID(vmOneID), WithCacheTTL(time.Hour)) {
		t.Fatal("module not enabled")
	}
	if _, err := GetInstance().ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	// Bypass the cache freshness window and ask for an id that is not cached.
	// (In VM mode the target is fixed; this exercises the cache path directly.)
	c := newVMCache(time.Hour)
	c.store(nil, map[string]json.RawMessage{})
	if _, _, _, err := c.getRaw(context.Background(), "ghost", func(context.Context) error { return nil }, false); err == nil {
		t.Fatal("want not-found error for missing id")
	}
}

func TestInvalidateMarksStale(t *testing.T) {
	resetBetween(t)
	runner := &fakeRunner{defaultOut: sampleVMsJSON}
	if !mustInit(t, WithLogger(testLogger()), WithRunner(runner), WithLookPath(fakeLookPath(bothFound())), WithCacheTTL(time.Hour)) {
		t.Fatal("module not enabled")
	}
	if _, err := GetInstance().ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	GetInstance().InvalidateCache()
	if _, err := GetInstance().ListVMs(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 2 {
		t.Fatalf("calls = %d, want 2 after invalidation", runner.callCount())
	}
}

func bothFound() map[string]string {
	return map[string]string{
		"prlctl":    "/usr/local/bin/prlctl",
		"prlsrvctl": "/usr/local/bin/prlsrvctl",
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o755)
}
