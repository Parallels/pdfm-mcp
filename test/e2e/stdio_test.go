//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// TestStdioEndToEnd builds the binary and speaks real JSON-RPC over stdio:
// connect, list tools, and verify the Parallels module gating matches the
// machine (no tools at all without prlctl/prlsrvctl, the list tool present
// when both are installed).
func TestStdioEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bin := filepath.Join(t.TempDir(), "pdfm-mcp")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/pdfm-mcp").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "v0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin)}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	if _, err := exec.LookPath("prlctl"); err != nil {
		// Without Parallels the server still starts, speaks MCP, and
		// exposes no tools.
		if len(names) != 0 {
			t.Fatalf("expected no tools without prlctl: %v", names)
		}
		return
	}
	if !names["parallels_list_vms"] {
		t.Fatalf("parallels_list_vms not listed: %v", names)
	}
}

// TestParallelsGating exercises the real prlctl end to end: both modes
// expose the read tools (fleet mode's parallels_vm_info targets a specific
// VM by id, fetched live), VM mode adds the no-argument cache-backed
// behaviour, and the cache serves the second call without re-running prlctl.
func TestParallelsGating(t *testing.T) {
	if _, err := exec.LookPath("prlctl"); err != nil {
		t.Skip("prlctl not on PATH; Parallels Desktop not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	bin := filepath.Join(t.TempDir(), "pdfm-mcp-parallels")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/pdfm-mcp").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	connect := func(args ...string) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "e2e-parallels", Version: "v0"}, nil)
		cmdArgs := append([]string{bin}, args...)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(cmdArgs[0], cmdArgs[1:]...)}, nil)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		return session
	}

	listed := func(t *testing.T, s *mcp.ClientSession) map[string]bool {
		t.Helper()
		tools, err := s.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, tl := range tools.Tools {
			names[tl.Name] = true
		}
		return names
	}

	sessFleet := connect()
	defer func() {
		if err := sessFleet.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	// The power tools are listed in both modes. They are only LISTED here —
	// never executed — because a real run would power-cycle a real VM.
	powerTools := []string{
		"parallels_vm_start", "parallels_vm_resume", "parallels_vm_pause",
		"parallels_vm_suspend", "parallels_vm_restart", "parallels_vm_reset",
		"parallels_vm_reset_uptime", "parallels_vm_stop",
	}

	names := listed(t, sessFleet)
	if !names["parallels_list_vms"] {
		t.Fatalf("fleet mode: parallels_list_vms not listed: %v", names)
	}
	// parallels_vm_info is listed in fleet mode too (a specific VM by id or
	// name, fetched live) — asserted and executed further down.
	if !names["parallels_vm_info"] {
		t.Fatalf("fleet mode: parallels_vm_info not listed: %v", names)
	}
	for _, p := range powerTools {
		if !names[p] {
			t.Fatalf("fleet mode: %s not listed: %v", p, names)
		}
	}
	// parallels_vm_capture is listed too. It is only executed when
	// PDFM_MCP_E2E_CAPTURE_VM names a running VM — never default-on.
	if !names["parallels_vm_capture"] {
		t.Fatalf("fleet mode: parallels_vm_capture not listed: %v", names)
	}
	// parallels_vm_exec is listed too; opt-in execution below (the only safe
	// commands are echo/exit).
	if !names["parallels_vm_exec"] {
		t.Fatalf("fleet mode: parallels_vm_exec not listed: %v", names)
	}
	// Keyboard input is listed too; opt-in below (a single no-op Escape key
	// only).
	if !names["parallels_vm_send_key"] || !names["parallels_vm_type"] {
		t.Fatalf("fleet mode: keyboard tools not listed: %v", names)
	}
	// Deletion is listed but NEVER executed here — there is deliberately no
	// opt-in env, because any real run would delete a real VM.
	if !names["parallels_vm_delete"] {
		t.Fatalf("fleet mode: parallels_vm_delete not listed: %v", names)
	}
	// Cloning is listed too; also never executed (a real clone
	// permanently creates a VM that needs a separate delete to clean up).
	if !names["parallels_vm_clone"] {
		t.Fatalf("fleet mode: parallels_vm_clone not listed: %v", names)
	}
	// The encryption lifecycle is listed too. Only the non-mutating
	// dry-run precondition checks execute below (default-on); real
	// encrypt/decrypt/change-password never run here — they are
	// secret-bearing state mutations (manual QA only).
	for _, p := range []string{"parallels_vm_encrypt", "parallels_vm_decrypt", "parallels_vm_change_encryption_password"} {
		if !names[p] {
			t.Fatalf("fleet mode: %s not listed: %v", p, names)
		}
	}
	// Reclaim is listed too; the non-mutating ESTIMATE runs below
	// (default-on), a real reclaim never runs in e2e.
	if !names["parallels_vm_reclaim_disk"] {
		t.Fatalf("fleet mode: parallels_vm_reclaim_disk not listed: %v", names)
	}
	// Sequence orchestration is listed in both modes. Only the opt-in
	// read-only capture sequence below ever executes a real step.
	if !names["parallels_vm_sequence"] {
		t.Fatalf("fleet mode: parallels_vm_sequence not listed: %v", names)
	}

	// Background exec jobs, snapshots and screen text reading are listed
	// (default policy: no read-only/allow/deny flags). They are only LISTED
	// here, never run. OCR is built into the binary on macOS (spec 022),
	// so parallels_vm_observe is always present.
	for _, p := range []string{
		"parallels_vm_exec_start", "parallels_vm_exec_result", "parallels_vm_exec_cancel",
		"parallels_vm_snapshot_create", "parallels_vm_snapshot_list",
		"parallels_vm_snapshot_delete", "parallels_vm_snapshot_switch",
		"parallels_vm_observe",
	} {
		if !names[p] {
			t.Fatalf("fleet mode: %s not listed: %v", p, names)
		}
	}

	res, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_list_vms", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_vms failed: err=%v res=%+v", err, res)
	}
	got, _ := res.StructuredContent.(map[string]any)
	count, _ := got["count"].(float64)
	if count < 0 {
		t.Fatalf("count = %v", got["count"])
	}
	vms, _ := got["vms"].([]any)
	if float64(len(vms)) != count {
		t.Fatalf("vms len %d != count %v", len(vms), count)
	}
	var firstID string
	if len(vms) > 0 {
		if first, ok := vms[0].(map[string]any); ok {
			firstID, _ = first["ID"].(string)
			if firstID == "" {
				t.Fatalf("first vm has no ID: %v", first)
			}
		}
	}

	// Fleet-mode parallels_vm_info: a read-only live fetch of one VM by id
	// (prlctl list <id> -a -i --json). Safe default-on; never mutating.
	if firstID != "" {
		infoFleet, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_info",
			Arguments: map[string]any{"vm_id": firstID},
		})
		if err != nil || infoFleet.IsError {
			t.Fatalf("fleet vm_info failed: err=%v res=%+v", err, infoFleet)
		}
		infoFleetGot, _ := infoFleet.StructuredContent.(map[string]any)
		if infoFleetGot["source"] != "live" {
			t.Fatalf("fleet vm_info source = %v, want live: %v", infoFleetGot["source"], infoFleetGot)
		}
		if vmBlob, ok := infoFleetGot["vm"].(map[string]any); !ok || vmBlob["ID"] != firstID {
			t.Fatalf("fleet vm_info returned wrong VM: %v", infoFleetGot["vm"])
		}
		// Unknown VM: a clean tool error, not a crash.
		infoMissing, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_info",
			Arguments: map[string]any{"vm_id": "no-such-vm-xyz-e2e"},
		})
		if err != nil || !infoMissing.IsError {
			t.Fatalf("fleet vm_info unknown VM must be a tool error: err=%v res=%+v", err, infoMissing)
		}
	}

	if firstID == "" {
		t.Log("no VMs on this machine; skipping VM-mode subtest")
		return
	}

	// Encryption dry-runs are non-mutating precondition checks (prlctl
	// reads no password in dry-run): safe to run default-on against any
	// stopped VM, with the outcome determined by its Security.Encrypted
	// flag. No password is ever sent.
	var stoppedVM map[string]any
	for _, v := range vms {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if vm["State"] == "stopped" {
			stoppedVM = vm
			break
		}
	}
	if stoppedVM == nil {
		t.Log("no stopped VM found; skipping encryption dry-run checks")
	} else {
		sec, _ := stoppedVM["Security"].(map[string]any)
		encrypted, _ := sec["Encrypted"].(string)
		if encrypted != "yes" && encrypted != "no" {
			t.Fatalf("unexpected Security.Encrypted %q on %v", encrypted, stoppedVM["Name"])
		}
		encID, _ := stoppedVM["ID"].(string)
		encRes, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_encrypt",
			Arguments: map[string]any{"vm_id": encID, "dry_run": true},
		})
		if err != nil {
			t.Fatalf("encrypt dry-run transport failed: %v", err)
		}
		decRes, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_decrypt",
			Arguments: map[string]any{"vm_id": encID, "dry_run": true},
		})
		if err != nil {
			t.Fatalf("decrypt dry-run transport failed: %v", err)
		}
		switch encrypted {
		case "no":
			// Unencrypted: encrypt dry-run reports the prerequisites met,
			// decrypt dry-run is a clean tool error.
			if encRes.IsError {
				t.Fatalf("encrypt dry-run on unencrypted VM must succeed: %+v", encRes.Content)
			}
			if !decRes.IsError {
				t.Fatalf("decrypt dry-run on unencrypted VM must be a tool error: %+v", decRes)
			}
		case "yes":
			// Encrypted: the dry-run preconditions require the current
			// password to authorise the check. E2e never sends passwords,
			// so both must fail as clean tool errors (no crash).
			if !encRes.IsError || !decRes.IsError {
				t.Fatalf("dry-runs on an encrypted VM without a password must be tool errors: enc=%+v dec=%+v", encRes, decRes)
			}
		}
	}

	// The reclaim estimate is read-only (works on running and stopped
	// VMs): safe to run default-on against any UNENCRYPTED VM. An
	// encrypted VM would need its password, which e2e never sends.
	var reclaimVM map[string]any
	for _, v := range vms {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		sec, _ := vm["Security"].(map[string]any)
		if sec["Encrypted"] != "yes" {
			reclaimVM = vm
			break
		}
	}
	if reclaimVM == nil {
		t.Log("all VMs encrypted; skipping reclaim estimate check")
	} else {
		recID, _ := reclaimVM["ID"].(string)
		recRes, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_reclaim_disk",
			Arguments: map[string]any{"vm_id": recID, "estimate": true},
		})
		if err != nil || recRes.IsError {
			t.Fatalf("reclaim estimate failed: err=%v res=%+v", err, recRes)
		}
		recSc, _ := recRes.StructuredContent.(map[string]any)
		if recSc["estimate"] != true {
			t.Fatalf("reclaim estimate output = %v", recSc)
		}
		if space, ok := recSc["disk_space"].(map[string]any); !ok {
			t.Fatalf("reclaim estimate missing disk_space: %v", recSc)
		} else {
			for _, k := range []string{"hard_disks", "snapshots", "miscellaneous", "reclaimable"} {
				if n, ok := space[k].(float64); !ok || n < 0 {
					t.Fatalf("disk_space.%s = %v", k, space[k])
				}
			}
		}
	}

	// The snapshot list is read-only (and the only snapshot op e2e ever
	// runs): safe default-on against any UNENCRYPTED VM — an encrypted
	// VM would need its password, which e2e never sends. Mutating
	// snapshot ops (create/delete/switch) are listed above, never run.
	if reclaimVM == nil {
		t.Log("all VMs encrypted; skipping snapshot list check")
	} else {
		snapID, _ := reclaimVM["ID"].(string)
		snapRes, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_snapshot_list",
			Arguments: map[string]any{"vm_id": snapID},
		})
		if err != nil || snapRes.IsError {
			t.Fatalf("snapshot list failed: err=%v res=%+v", err, snapRes)
		}
		snapSc, _ := snapRes.StructuredContent.(map[string]any)
		snaps, ok := snapSc["snapshots"].([]any)
		if !ok {
			t.Fatalf("snapshot list output = %v", snapSc)
		}
		for _, s := range snaps {
			item, ok := s.(map[string]any)
			if !ok {
				t.Fatalf("snapshot item = %v", s)
			}
			for _, k := range []string{"id", "name", "date", "state", "current", "parent"} {
				if _, ok := item[k]; !ok {
					t.Fatalf("snapshot item missing %s: %v", k, item)
				}
			}
		}
	}

	sessVM := connect("--vm-id", firstID)
	defer func() {
		if err := sessVM.Close(); err != nil {
			t.Logf("session close: %v", err)
		}
	}()

	vmNames := listed(t, sessVM)
	if !vmNames["parallels_list_vms"] || !vmNames["parallels_vm_info"] {
		t.Fatalf("vm mode: expected both parallels tools: %v", vmNames)
	}
	for _, p := range powerTools {
		if !vmNames[p] {
			t.Fatalf("vm mode: %s not listed: %v", p, vmNames)
		}
	}
	if !vmNames["parallels_vm_capture"] {
		t.Fatalf("vm mode: parallels_vm_capture not listed: %v", vmNames)
	}
	if !vmNames["parallels_vm_exec"] {
		t.Fatalf("vm mode: parallels_vm_exec not listed: %v", vmNames)
	}
	if !vmNames["parallels_vm_send_key"] || !vmNames["parallels_vm_type"] {
		t.Fatalf("vm mode: keyboard tools not listed: %v", vmNames)
	}
	if !vmNames["parallels_vm_delete"] {
		t.Fatalf("vm mode: parallels_vm_delete not listed: %v", vmNames)
	}
	if !vmNames["parallels_vm_clone"] {
		t.Fatalf("vm mode: parallels_vm_clone not listed: %v", vmNames)
	}
	if !vmNames["parallels_vm_sequence"] {
		t.Fatalf("vm mode: parallels_vm_sequence not listed: %v", vmNames)
	}
	for _, p := range []string{"parallels_vm_encrypt", "parallels_vm_decrypt", "parallels_vm_change_encryption_password"} {
		if !vmNames[p] {
			t.Fatalf("vm mode: %s not listed: %v", p, vmNames)
		}
	}
	if !vmNames["parallels_vm_reclaim_disk"] {
		t.Fatalf("vm mode: parallels_vm_reclaim_disk not listed: %v", vmNames)
	}
	for _, p := range []string{
		"parallels_vm_snapshot_create", "parallels_vm_snapshot_list",
		"parallels_vm_snapshot_delete", "parallels_vm_snapshot_switch",
	} {
		if !vmNames[p] {
			t.Fatalf("vm mode: %s not listed: %v", p, vmNames)
		}
	}

	// Opt-in real capture, run against the FLEET session: fleet mode accepts
	// an explicit vm_id (VM mode would correctly reject it), and capture is
	// read-only for the VM. Skipped unless PDFM_MCP_E2E_CAPTURE_VM names a
	// running VM.
	if captureVM := os.Getenv("PDFM_MCP_E2E_CAPTURE_VM"); captureVM != "" {
		res, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_capture",
			Arguments: map[string]any{"vm_id": captureVM},
		})
		if err != nil || res.IsError {
			t.Fatalf("opt-in capture failed: err=%v res=%+v", err, res)
		}
		var img *mcp.ImageContent
		for _, c := range res.Content {
			if ic, ok := c.(*mcp.ImageContent); ok {
				img = ic
			}
		}
		if img == nil || img.MIMEType != "image/png" {
			t.Fatalf("no image content block in capture result: %+v", res.Content)
		}
		if _, err := png.DecodeConfig(bytesReader(img.Data)); err != nil {
			t.Fatalf("capture data is not a decodable PNG: %v", err)
		}
	}

	// Opt-in real exec against the fleet session: only echo/exit are run —
	// provably non-mutating.
	if execVM := os.Getenv("PDFM_MCP_E2E_EXEC_VM"); execVM != "" {
		ok, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_exec",
			Arguments: map[string]any{"vm_id": execVM, "command": "echo e2e-exec-ok"},
		})
		if err != nil || ok.IsError {
			t.Fatalf("opt-in exec echo failed: err=%v res=%+v", err, ok)
		}
		okSc, _ := ok.StructuredContent.(map[string]any)
		if okSc["exit_code"] != float64(0) || !strings.Contains(okSc["stdout"].(string), "e2e-exec-ok") {
			t.Fatalf("echo result: %v", okSc)
		}

		// A non-zero guest exit is a RESULT, not a tool error.
		fail, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_exec",
			Arguments: map[string]any{"vm_id": execVM, "command": "exit 3"},
		})
		if err != nil || fail.IsError {
			t.Fatalf("exit 3 must not be a tool error: err=%v res=%+v", err, fail)
		}
		failSc, _ := fail.StructuredContent.(map[string]any)
		if failSc["exit_code"] != float64(3) {
			t.Fatalf("exit_code = %v, want 3: %v", failSc["exit_code"], failSc)
		}
	}

	// Opt-in real key send against the fleet session: a single Escape key —
	// a no-op in normal guest contexts. Skipped unless PDFM_MCP_E2E_KEY_VM
	// names a running VM.
	if keyVM := os.Getenv("PDFM_MCP_E2E_KEY_VM"); keyVM != "" {
		res, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name:      "parallels_vm_send_key",
			Arguments: map[string]any{"vm_id": keyVM, "key": "escape"},
		})
		if err != nil || res.IsError {
			t.Fatalf("opt-in send-key failed: err=%v res=%+v", err, res)
		}
		sc, _ := res.StructuredContent.(map[string]any)
		if sc["vm_id"] != keyVM || sc["events"] != float64(1) {
			t.Fatalf("send-key result: %v", sc)
		}
	}

	// Opt-in real sequence against the fleet session: the read-only
	// "capture, wait 1 s, capture" shape — no mutating steps, so it is safe
	// on any running VM. Skipped unless PDFM_MCP_E2E_SEQ_VM names one.
	if seqVM := os.Getenv("PDFM_MCP_E2E_SEQ_VM"); seqVM != "" {
		res, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name: "parallels_vm_sequence",
			Arguments: map[string]any{
				"vm_id": seqVM,
				"steps": []any{
					map[string]any{"id": "before", "tool": "parallels_vm_capture"},
					map[string]any{"wait": map[string]any{"seconds": 1}},
					map[string]any{"id": "after", "tool": "parallels_vm_capture"},
				},
			},
		})
		if err != nil || res.IsError {
			t.Fatalf("opt-in sequence failed: err=%v res=%+v", err, res)
		}
		sc, _ := res.StructuredContent.(map[string]any)
		if sc["status"] != "completed" || sc["steps_run"] != float64(3) {
			t.Fatalf("sequence result: %v", sc)
		}
		images := 0
		for _, c := range res.Content {
			if ic, ok := c.(*mcp.ImageContent); ok {
				images++
				if _, err := png.DecodeConfig(bytesReader(ic.Data)); err != nil {
					t.Fatalf("sequence image is not a decodable PNG: %v", err)
				}
			}
		}
		if images != 2 || sc["image_count"] != float64(2) {
			t.Fatalf("sequence images: blocks=%d image_count=%v", images, sc["image_count"])
		}
	}

	// Opt-in real wait-until against the fleet session: read-only capture
	// polling. On a still screen, `settled` must be met with zero image
	// blocks, and `changed` (no `from`) must time out as expectation_failed
	// against its own start baseline. A client cancellation mid-wait must
	// return promptly with no stray `prlctl capture` left behind. Skipped
	// unless PDFM_MCP_E2E_WAIT_VM names a running VM.
	if waitVM := os.Getenv("PDFM_MCP_E2E_WAIT_VM"); waitVM != "" {
		res, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name: "parallels_vm_sequence",
			Arguments: map[string]any{
				"vm_id": waitVM,
				"steps": []any{
					map[string]any{"id": "settle", "wait": map[string]any{"until": "settled", "timeout_seconds": 20}},
				},
			},
		})
		if err != nil || res.IsError {
			t.Fatalf("opt-in settled failed: err=%v res=%+v", err, res)
		}
		sc, _ := res.StructuredContent.(map[string]any)
		if sc["status"] != "completed" || sc["image_count"] != float64(0) {
			t.Fatalf("settled result: %v", sc)
		}
		if len(res.Content) != 1 {
			t.Fatalf("settled must return no image blocks: %d blocks", len(res.Content))
		}
		step := sc["results"].([]any)[0].(map[string]any)
		body := step["output"].(map[string]any)
		if body["until"] != "settled" || body["met"] != true || body["polls"].(float64) < 3 {
			t.Fatalf("settled step: %v", body)
		}
		t.Logf("settled on %s: met=%v polls=%v elapsed_ms=%v", waitVM, body["met"], body["polls"], body["elapsed_ms"])

		chg, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name: "parallels_vm_sequence",
			Arguments: map[string]any{
				"vm_id": waitVM,
				"steps": []any{
					map[string]any{"id": "chg", "wait": map[string]any{"until": "changed", "timeout_seconds": 3}},
				},
			},
		})
		if err != nil || chg.IsError {
			t.Fatalf("changed on a static screen must time out as a RESULT: err=%v res=%+v", err, chg)
		}
		chgSc, _ := chg.StructuredContent.(map[string]any)
		if chgSc["status"] != "stopped" {
			t.Fatalf("changed result: %v", chgSc)
		}
		if !strings.Contains(chgSc["stop_reason"].(string), "wait until changed timed out") {
			t.Fatalf("stop_reason: %v", chgSc["stop_reason"])
		}
		chgStep := chgSc["results"].([]any)[0].(map[string]any)
		chgBody := chgStep["output"].(map[string]any)
		if chgBody["met"] != false {
			t.Fatalf("changed step: %v", chgBody)
		}
		t.Logf("changed on %s: met=%v polls=%v elapsed_ms=%v", waitVM, chgBody["met"], chgBody["polls"], chgBody["elapsed_ms"])

		// A real change: move the boot-manager highlight with a key press,
		// then wait for `changed` relative to the pre-press capture.
		// Precondition: the VM is parked at its boot menu with the highlight
		// not on the last entry (disposable VM, opt-in only).
		chg2, err := sessFleet.CallTool(ctx, &mcp.CallToolParams{
			Name: "parallels_vm_sequence",
			Arguments: map[string]any{
				"vm_id": waitVM,
				"steps": []any{
					map[string]any{"id": "before", "tool": "parallels_vm_capture"},
					map[string]any{"tool": "parallels_vm_send_key", "args": map[string]any{"key": "down"}},
					map[string]any{"id": "moved", "wait": map[string]any{"until": "changed", "from": "before", "timeout_seconds": 10}},
				},
			},
		})
		if err != nil || chg2.IsError {
			t.Fatalf("changed-from-after-key failed: err=%v res=%+v", err, chg2)
		}
		sc2, _ := chg2.StructuredContent.(map[string]any)
		if sc2["status"] != "completed" {
			t.Fatalf("changed-from-after-key result: %v", sc2)
		}
		t.Logf("changed from pre-key capture: status=%v", sc2["status"])

		cctx, ccancel := context.WithCancel(ctx)
		start := time.Now()
		go func() { time.Sleep(4 * time.Second); ccancel() }()
		// `changed` on a static screen never settles, so the call is still
		// polling when the client cancels it.
		cres, cerr := sessFleet.CallTool(cctx, &mcp.CallToolParams{
			Name: "parallels_vm_sequence",
			Arguments: map[string]any{
				"vm_id": waitVM,
				"steps": []any{
					map[string]any{"wait": map[string]any{"until": "changed", "timeout_seconds": 60}},
				},
			},
		})
		elapsed := time.Since(start)
		if cerr == nil {
			t.Fatalf("canceled wait must not succeed: res=%+v", cres)
		}
		if elapsed > 10*time.Second {
			t.Fatalf("cancellation took too long: %v", elapsed)
		}
		t.Logf("canceled mid-wait after %v: %v", elapsed, cerr)
		if out, _ := exec.Command("pgrep", "-f", "prlctl capture").CombinedOutput(); strings.TrimSpace(string(out)) != "" {
			t.Fatalf("stray prlctl capture after cancel: %s", out)
		}
	}

	info, err := sessVM.CallTool(ctx, &mcp.CallToolParams{Name: "parallels_vm_info", Arguments: map[string]any{}})
	if err != nil || info.IsError {
		t.Fatalf("vm_info failed: err=%v res=%+v", err, info)
	}
	infoGot, _ := info.StructuredContent.(map[string]any)
	vmBlob, ok := infoGot["vm"].(map[string]any)
	if !ok {
		t.Fatalf("vm blob missing: %v", infoGot)
	}
	if vmBlob["ID"] != firstID {
		t.Fatalf("vm_info returned %v, want %s", vmBlob["ID"], firstID)
	}
}
