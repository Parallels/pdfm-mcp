package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// capturedWire mirrors the Amplitude HTTP-API payload we assert on.
type capturedWire struct {
	Events []map[string]interface{} `json:"events"`
}

type wireEvent struct {
	EventType string                 `json:"event_type"`
	UserID    string                 `json:"user_id"`
	DeviceID  string                 `json:"device_id"`
	Props     map[string]interface{} `json:"event_properties"`
}

// newCaptureServer records every flush body and always answers 200 with the
// Amplitude ack.
func newCaptureServer(t *testing.T) (*httptest.Server, *[]wireEvent) {
	t.Helper()
	var mu sync.Mutex
	events := &[]wireEvent{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read flush body: %v", err)
		}
		var wire capturedWire
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Errorf("flush body is not the expected envelope: %v\n%s", err, body)
		}
		mu.Lock()
		for _, e := range wire.Events {
			*events = append(*events, wireEventFromMap(e))
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200}`))
	}))
	t.Cleanup(srv.Close)
	return srv, events
}

func wireEventFromMap(m map[string]interface{}) wireEvent {
	e := wireEvent{
		EventType: stringOf(m["event_type"]),
		UserID:    stringOf(m["user_id"]),
		DeviceID:  stringOf(m["device_id"]),
	}
	if p, ok := m["event_properties"].(map[string]interface{}); ok {
		e.Props = p
	}
	return e
}

func stringOf(v interface{}) string {
	s, _ := v.(string)
	return s
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func fakeIdentityRunner() CommandRunner {
	return func(_ context.Context, name string, _ ...string) (string, error) {
		switch name {
		case "ioreg":
			return sampleIOReg, nil
		case "prlsrvctl":
			return `{"serial":"TEST-SERIAL","edition":"Pro"}`, nil
		}
		return "", context.Canceled
	}
}

const (
	fakeUUID   = "9AE1D424-FBA5-51B4-89DE-E8AF1998A164"
	fakeSerial = "TEST-SERIAL"
)

// expectedAllowlist is the spec 024 section 3 property allowlist; any other
// key in a transmitted event fails the tests.
var expectedAllowlist = []string{
	"service", "version", "os", "arch", "mode", "pd_edition",
	"tool", "ok", "duration_ms",
}

// isToolEvent reports whether the event is a per-tool event: tool events
// are named PDFM::<TOOL>, the fixed events are SERVER_STARTED/HEARTBEAT.
func isToolEvent(e wireEvent) bool {
	return e.EventType != EventServerStarted && e.EventType != EventHeartbeat
}

func assertAllowlist(t *testing.T, e wireEvent) {
	t.Helper()
	for k := range e.Props {
		if !contains(expectedAllowlist, k) {
			t.Fatalf("event %s carries non-allowlisted property %q", e.EventType, k)
		}
	}
	if !strings.HasPrefix(e.EventType, ServicePrefix+"::") {
		t.Fatalf("event %q does not follow the %s::{{TOOL}} name format", e.EventType, ServicePrefix)
	}
	switch {
	case isToolEvent(e):
		for _, k := range []string{"tool", "ok", "duration_ms"} {
			if _, ok := e.Props[k]; !ok {
				t.Fatalf("tool event missing property %q", k)
			}
		}
	default:
		for _, k := range []string{"tool", "ok", "duration_ms"} {
			if _, ok := e.Props[k]; ok {
				t.Fatalf("non-tool event must not carry %q", k)
			}
		}
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// setKey mimics a build with (or without) the ldflags-injected key.
func setKey(t *testing.T, key string) {
	t.Helper()
	old := APIKey
	APIKey = key
	t.Cleanup(func() { APIKey = old })
}

func TestInitDisabledWithoutKey(t *testing.T) {
	defer Reset()
	setKey(t, "")
	Init(context.Background(), InitConfig{Logger: silentLogger()})
	if IsEnabled() {
		t.Fatal("enabled without an API key")
	}
	if instance.client != nil {
		t.Fatal("a client was created although telemetry is disabled")
	}
}

func TestInitDisabledByOptOutWinsOverKey(t *testing.T) {
	defer Reset()
	setKey(t, "k")
	Init(context.Background(), InitConfig{Disabled: true, Logger: silentLogger()})
	if IsEnabled() {
		t.Fatal("opt-out must win over the key")
	}
}

func TestDecideEnabled(t *testing.T) {
	setKey(t, "")
	if r := decideEnabled(false); r != "no API key" {
		t.Fatalf("decideEnabled() = %q", r)
	}
	setKey(t, "k")
	if r := decideEnabled(true); r != "opt-out set" {
		t.Fatalf("decideEnabled(opt-out) = %q", r)
	}
	if r := decideEnabled(false); r != "" {
		t.Fatalf("decideEnabled(key) = %q", r)
	}
}

func TestInitEnabledSendsStartedAndToolEvents(t *testing.T) {
	defer Reset()
	srv, events := newCaptureServer(t)

	setKey(t, "fake-key")
	Init(context.Background(), InitConfig{
		Mode:      "fleet",
		Logger:    silentLogger(),
		Runner:    fakeIdentityRunner(),
		ServerURL: srv.URL,
		// Long heartbeat: this test asserts only started + tool events.
		Heartbeat: time.Hour,
	})
	if !IsEnabled() {
		t.Fatal("not enabled with key and no opt-out")
	}

	instance.TrackToolCall("parallels_vm_info", true, 42*time.Millisecond)
	instance.TrackToolCall("parallels_vm_stop", false, 3*time.Second)
	// Flush synchronously, then read the capture.
	instance.Close()

	wantUser := userIDFor(fakeUUID, fakeSerial)
	byType := map[string][]wireEvent{}
	for _, e := range *events {
		byType[e.EventType] = append(byType[e.EventType], e)
	}

	started := byType[EventServerStarted]
	if len(started) != 1 {
		t.Fatalf("server::started count = %d", len(started))
	}
	assertAllowlist(t, started[0])
	checkShared := func(e wireEvent) {
		t.Helper()
		if e.UserID != wantUser {
			t.Errorf("user_id = %q, want %q", e.UserID, wantUser)
		}
		if e.DeviceID != fakeUUID {
			t.Errorf("device_id = %q, want %q", e.DeviceID, fakeUUID)
		}
		if got := stringOf(e.Props["pd_edition"]); got != "Pro" {
			t.Errorf("pd_edition = %q", got)
		}
		if got := stringOf(e.Props["mode"]); got != "fleet" {
			t.Errorf("mode = %q", got)
		}
		if got := stringOf(e.Props["service"]); got != "pdfm-mcp" {
			t.Errorf("service = %q", got)
		}
		if got := stringOf(e.Props["version"]); got == "" {
			t.Error("version property empty")
		}
	}
	checkShared(started[0])

	// Each tool call is its own event, named after the tool (spec 024
	// section 3): PDFM::PARALLELS_VM_INFO / PDFM::PARALLELS_VM_STOP.
	if got := toolEventName("parallels_vm_info"); got != "PDFM::PARALLELS_VM_INFO" {
		t.Fatalf("toolEventName = %q", got)
	}
	calls := append(append([]wireEvent{}, byType[toolEventName("parallels_vm_info")]...), byType[toolEventName("parallels_vm_stop")]...)
	if len(calls) != 2 {
		t.Fatalf("tool event count = %d", len(calls))
	}
	sawOK, sawErr := false, false
	for _, e := range calls {
		assertAllowlist(t, e)
		checkShared(e)
		switch stringOf(e.Props["tool"]) {
		case "parallels_vm_info":
			sawOK = true
			if e.EventType != toolEventName("parallels_vm_info") {
				t.Errorf("event type = %q", e.EventType)
			}
			if v, _ := e.Props["ok"].(bool); !v {
				t.Error("parallels_vm_info should be ok=true")
			}
			if v, _ := e.Props["duration_ms"].(float64); v != 42 {
				t.Errorf("duration_ms = %v", v)
			}
		case "parallels_vm_stop":
			sawErr = true
			if v, _ := e.Props["ok"].(bool); v {
				t.Error("parallels_vm_stop should be ok=false")
			}
			if v, _ := e.Props["duration_ms"].(float64); v != 3000 {
				t.Errorf("duration_ms = %v", v)
			}
		}
	}
	if !sawOK || !sawErr {
		t.Fatal("missing expected tool events")
	}

	// Anonymisation: the raw serial and the API key must never appear
	// anywhere in the captured wire format.
	raw, _ := json.Marshal(*events)
	for _, secret := range []string{fakeSerial, "fake-key"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("captured payload contains %q", secret)
		}
	}
}

// TestInstrumentWrapsHandler verifies the wrapper calls the original
// handler exactly once, passes the input through, forwards a failing
// handler's error, and records the outcome.
func TestInstrumentWrapsHandler(t *testing.T) {
	defer Reset()
	srv, events := newCaptureServer(t)
	setKey(t, "fake-key")
	Init(context.Background(), InitConfig{
		Mode: "vm", Logger: silentLogger(),
		Runner: fakeIdentityRunner(), ServerURL: srv.URL, Heartbeat: time.Hour,
	})

	calls := 0
	okH := Instrument[string, string]("fake_ok", func(_ context.Context, _ *mcp.CallToolRequest, in string) (*mcp.CallToolResult, string, error) {
		calls++
		if in != "input" {
			t.Errorf("input not passed through: %q", in)
		}
		return nil, "out", nil
	})
	if _, out, err := okH(context.Background(), nil, "input"); err != nil || out != "out" {
		t.Fatalf("ok handler: out=%q err=%v", out, err)
	}

	errH := Instrument[string, string]("fake_err", func(_ context.Context, _ *mcp.CallToolRequest, _ string) (*mcp.CallToolResult, string, error) {
		return nil, "", context.DeadlineExceeded
	})
	if _, _, err := errH(context.Background(), nil, "input"); err != context.DeadlineExceeded {
		t.Fatalf("error not forwarded: %v", err)
	}

	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
	instance.Close()

	tools := map[string]bool{}
	for _, e := range *events {
		if isToolEvent(e) {
			tools[stringOf(e.Props["tool"])] = boolOf(e.Props["ok"])
		}
	}
	if tools["fake_ok"] != true || tools["fake_err"] != false {
		t.Fatalf("instrumented outcomes wrong: %v", tools)
	}
	if e := findEvent(*events, toolEventName("fake_ok")); e == nil {
		t.Fatalf("missing %s event", toolEventName("fake_ok"))
	}
}

func boolOf(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func findEvent(events []wireEvent, eventType string) *wireEvent {
	for i := range events {
		if events[i].EventType == eventType {
			return &events[i]
		}
	}
	return nil
}

func TestDisabledServiceDropsEvents(_ *testing.T) {
	defer Reset()
	Init(context.Background(), InitConfig{Logger: silentLogger()})
	// Must not panic and must send nothing (there is no client to flush).
	instance.TrackToolCall("parallels_vm_info", true, time.Millisecond)
	instance.Close()
}

func TestHeartbeatFires(t *testing.T) {
	defer Reset()
	srv, events := newCaptureServer(t)
	setKey(t, "fake-key")
	Init(context.Background(), InitConfig{
		Mode: "fleet", Logger: silentLogger(),
		Runner: fakeIdentityRunner(), ServerURL: srv.URL,
		Heartbeat: 50 * time.Millisecond,
	})
	time.Sleep(350 * time.Millisecond)
	instance.Close()

	n := 0
	for _, e := range *events {
		if e.EventType == EventHeartbeat {
			n++
			assertAllowlist(t, e)
		}
	}
	if n < 2 {
		t.Fatalf("heartbeat count = %d, want >= 2", n)
	}
}
