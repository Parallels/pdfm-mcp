package tools_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/server"
	"github.com/Parallels/pdfm-mcp/internal/tools"
)

func intPtr(n int) *int { return &n }

func TestSendKeyHandlerModes(t *testing.T) {
	cases := []struct {
		name string
		vmID string // configured target
		arg  string // explicit vm_id argument
		want string // expected argv target; "" = call must fail
	}{
		{"vm-mode uses configured target", vmOneID, "", vmOneID},
		{"vm-mode rejects explicit vm_id", vmOneID, vmTwoID, ""},
		{"fleet requires vm_id", "", "", ""},
		{"fleet uses given vm_id", "", "guest-x", "guest-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, tc.vmID)
			_, out, err := tools.VMSendKey(context.Background(), nil, tools.SendKeyInput{VMID: tc.arg, Key: "enter"})
			if tc.want == "" {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.VMID != tc.want || out.Events != 1 {
				t.Fatalf("out = %+v", out)
			}
			_, args := runner.lastCall()
			if !slices.Equal(args, []string{"send-key-event", tc.want, "-j"}) {
				t.Fatalf("args = %v", args)
			}
			if got := runner.lastStdin(); got != `[{"key":36}]` {
				t.Fatalf("stdin = %q", got)
			}
		})
	}
}

func TestSendKeySingleFormShapes(t *testing.T) {
	cases := []struct {
		name  string
		in    tools.SendKeyInput
		want  string // expected stdin; empty = error case
		wPart string
	}{
		{
			"key name tap",
			tools.SendKeyInput{VMID: "guest-x", Key: "enter"},
			`[{"key":36}]`,
			"",
		},
		{
			"prefixed name press with delay",
			tools.SendKeyInput{VMID: "guest-x", Key: "PRL_KEY_LEFT_SHIFT", Event: "press", Delay: 100},
			`[{"key":50,"event":"press","delay":100}]`,
			"",
		},
		{
			"numeric code",
			tools.SendKeyInput{VMID: "guest-x", Key: "115"},
			`[{"key":115}]`,
			"",
		},
		{
			"scancode release",
			tools.SendKeyInput{VMID: "guest-x", Scancode: 7, Event: "release"},
			`[{"scancode":7,"event":"release"}]`,
			"",
		},
		{
			"key and scancode",
			tools.SendKeyInput{VMID: "guest-x", Key: "a", Scancode: 7},
			"",
			"not both",
		},
		{
			"nothing provided",
			tools.SendKeyInput{VMID: "guest-x"},
			"",
			"provide a key",
		},
		{
			"unknown key name",
			tools.SendKeyInput{VMID: "guest-x", Key: "nope"},
			"",
			"unknown key",
		},
		{
			"key and events conflict",
			tools.SendKeyInput{VMID: "guest-x", Key: "a", Events: []tools.KeyEventArg{{Key: "b"}}},
			"",
			"not both",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			_, _, err := tools.VMSendKey(context.Background(), nil, tc.in)
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), tc.wPart) {
					t.Fatalf("err = %v, want containing %q", err, tc.wPart)
				}
				if runner.count() != 0 {
					t.Fatalf("prlctl must not run: %d calls", runner.count())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := runner.lastStdin(); got != tc.want {
				t.Fatalf("stdin = %s, want %s", got, tc.want)
			}
		})
	}
}

// The doc example: Win+PrtScr is left_win press, print, left_win release.
func TestSendKeyModifierCombo(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, out, err := tools.VMSendKey(context.Background(), nil, tools.SendKeyInput{
		VMID: "guest-x",
		Events: []tools.KeyEventArg{
			{Key: "left_win", Event: "press", Delay: 100},
			{Key: "print", Delay: 100},
			{Key: "left_win", Event: "release"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Events != 3 {
		t.Fatalf("events = %d, want 3", out.Events)
	}
	want := `[{"key":115,"event":"press","delay":100},{"key":92,"delay":100},{"key":115,"event":"release"}]`
	if got := runner.lastStdin(); got != want {
		t.Fatalf("stdin = %s, want %s", got, want)
	}
}

func TestSendKeyAliasFields(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, _, err := tools.VMSendKey(context.Background(), nil, tools.SendKeyInput{
		VMID: "guest-x",
		Events: []tools.KeyEventArg{
			{K: "a"},
			{S: 7, D: 100, Event: "release"},
			{D: 50},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"key":38},{"scancode":7,"event":"release","delay":100},{"delay":50}]`
	if got := runner.lastStdin(); got != want {
		t.Fatalf("stdin = %s, want %s", got, want)
	}

	// Aliases that disagree are rejected before any exec.
	runner2 := &testRunner{}
	initParallelsForTest(t, runner2, []string{"prlctl", "prlsrvctl"}, "")
	_, _, err = tools.VMSendKey(context.Background(), nil, tools.SendKeyInput{
		VMID:   "guest-x",
		Events: []tools.KeyEventArg{{Key: "a", K: "b"}},
	})
	if err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("err = %v, want disagree", err)
	}
	if runner2.count() != 0 {
		t.Fatalf("prlctl must not run: %d calls", runner2.count())
	}
}

func TestSendKeyPrlctlFailureIsError(t *testing.T) {
	runner := &testRunner{captureFn: func(_ string, _ []string) (string, string, int) {
		return "", "PrlDevDisplay_ConnectToVm: PrlJob_Wait: PRL_ERR_IO_STOPPED", 255
	}}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
	_, _, err := tools.VMSendKey(context.Background(), nil, tools.SendKeyInput{VMID: "guest-x", Key: "enter"})
	if err == nil || !strings.Contains(err.Error(), "PRL_ERR_IO_STOPPED") {
		t.Fatalf("err = %v, want the prlctl error", err)
	}
}

func TestSendKeyProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(logger, testConfig())

	names := toolNames(t, srv)
	if !names["parallels_vm_send_key"] || !names["parallels_vm_type"] {
		t.Fatalf("tools not listed: %v", names)
	}
	if req := wireRequiredList(t, srv, "parallels_vm_send_key"); !slices.Equal(req, []string{"vm_id"}) {
		t.Fatalf("send_key required = %v, want [vm_id]", req)
	}
	if req := wireRequiredList(t, srv, "parallels_vm_type"); !slices.Contains(req, "vm_id") || !slices.Contains(req, "text") {
		t.Fatalf("type required = %v, want vm_id and text", req)
	}

	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "sendkey-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_send_key",
		Arguments: map[string]any{"vm_id": "guest-x", "key": "enter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	if sc == nil || sc["vm_id"] != "guest-x" || sc["events"] != float64(1) {
		t.Fatalf("structured = %v", sc)
	}

	// type: "hi\n" → h(43), i(31), enter(36) = 3 events, 3 chars.
	res2, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_type",
		Arguments: map[string]any{"vm_id": "guest-x", "text": "hi\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.IsError {
		t.Fatalf("type tool error: %+v", res2.Content)
	}
	sc2, _ := res2.StructuredContent.(map[string]any)
	if sc2 == nil || sc2["events"] != float64(3) || sc2["chars"] != float64(3) {
		t.Fatalf("structured = %v", sc2)
	}
	// default delay_ms is 200, so every event carries it.
	if got := runner.lastStdin(); got != `[{"key":43,"delay":200},{"key":31,"delay":200},{"key":36,"delay":200}]` {
		t.Fatalf("stdin = %s", got)
	}

	// Form-style escape: a literal backslash + n (as typed into a client
	// text field) becomes Enter, not a typed backslash and n. Explicit
	// delay_ms 0 keeps the events delay-free (the pointer default would
	// otherwise kick in).
	res3, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_type",
		Arguments: map[string]any{"vm_id": "guest-x", "text": "hi\\n", "delay_ms": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res3.IsError {
		t.Fatalf("type tool error: %+v", res3.Content)
	}
	if got := runner.lastStdin(); got != `[{"key":43},{"key":31},{"key":36}]` {
		t.Fatalf("stdin = %s, want enter without delay", got)
	}
}

func TestSendKeyVMModeProtocol(t *testing.T) {
	runner := &testRunner{}
	initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, vmOneID)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testConfig()
	cfg.VMID = vmOneID
	srv := server.New(logger, cfg)

	if req := wireRequiredList(t, srv, "parallels_vm_send_key"); slices.Contains(req, "vm_id") {
		t.Fatalf("VM mode: vm_id must not be required: %v", req)
	}

	clientT, serverT := mcp.NewInMemoryTransports()
	sess, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "sendkey-vmmode-test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_type",
		Arguments: map[string]any{"text": "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	_, args := runner.lastCall()
	if !slices.Equal(args, []string{"send-key-event", vmOneID, "-j"}) {
		t.Fatalf("args = %v", args)
	}
	if got := runner.lastStdin(); got != `[{"key":32,"delay":200},{"key":45,"delay":200}]` {
		t.Fatalf("stdin = %s (ok = 32, 45)", got)
	}

	bad, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "parallels_vm_send_key",
		Arguments: map[string]any{"vm_id": vmTwoID, "key": "a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Fatal("explicit vm_id must be rejected in VM mode")
	}
}

func TestTypeTextValidation(t *testing.T) {
	cases := []struct {
		name  string
		in    tools.TypeTextInput
		wPart string
	}{
		{"empty text", tools.TypeTextInput{VMID: "guest-x"}, "empty"},
		{"over-long text", tools.TypeTextInput{VMID: "guest-x", Text: strings.Repeat("a", 4097)}, "limit"},
		{"negative delay", tools.TypeTextInput{VMID: "guest-x", Text: "a", DelayMS: intPtr(-1)}, "delay_ms"},
		{"delay too big", tools.TypeTextInput{VMID: "guest-x", Text: "a", DelayMS: intPtr(5001)}, "delay_ms"},
		{"unmappable rune", tools.TypeTextInput{VMID: "guest-x", Text: "café"}, "U+00E9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &testRunner{}
			initParallelsForTest(t, runner, []string{"prlctl", "prlsrvctl"}, "")
			if _, _, err := tools.VMTypeText(context.Background(), nil, tc.in); err == nil || !strings.Contains(err.Error(), tc.wPart) {
				t.Fatalf("err = %v, want containing %q", err, tc.wPart)
			}
			if runner.count() != 0 {
				t.Fatalf("prlctl must not run: %d calls", runner.count())
			}
		})
	}
}
