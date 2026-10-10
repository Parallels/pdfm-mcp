package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/amplitude/analytics-go/amplitude"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Parallels/pdfm-mcp/internal/version"
)

// Event names (spec 024 section 3), format {{SERVICE}}::{{TOOL}}. Tool
// events are named after the tool itself (uppercase MCP tool name), so
// "what is most used" is directly readable from the event name.
const (
	// ServicePrefix is the {{SERVICE}} half of every event name.
	ServicePrefix      = "PDFM"
	EventServerStarted = "PDFM::SERVER_STARTED"
	EventHeartbeat     = "PDFM::HEARTBEAT"
)

// toolEventName renders the event name for a tool call, e.g.
// parallels_vm_start -> PDFM::PARALLELS_VM_START.
func toolEventName(tool string) string {
	return ServicePrefix + "::" + strings.ToUpper(tool)
}

// HeartbeatInterval is the liveness cadence (PLAI parity).
const HeartbeatInterval = 5 * time.Minute

// APIKey is the Amplitude project key, baked into the binary at build time
// with the same -ldflags -X mechanism as the version (spec 024 section 2):
// the Makefile / CI inject it from the build environment. An empty key
// disables telemetry entirely. The key is never logged.
var APIKey = ""

// InitConfig is everything Init needs. The API key is never logged.
type InitConfig struct {
	Disabled bool // the PDFM_MCP_TELEMETRY_DISABLED opt-out
	Mode     string
	Logger   *slog.Logger
	// Runner runs the identity lookups; nil ⇒ exec-based default
	// (production). Tests inject fakes.
	Runner CommandRunner
	// ServerURL overrides the Amplitude ingest endpoint; non-empty values
	// are for tests only (httptest). Production leaves it empty.
	ServerURL string
	// Heartbeat is the liveness cadence; zero ⇒ the default 5 m.
	Heartbeat time.Duration
}

// Service is the process-wide telemetry handle. The zero value is a
// disabled service: every method is a safe no-op, so callers never need a
// nil check.
type Service struct {
	mu        sync.RWMutex
	enabled   bool
	client    amplitude.Client
	identity  Identity
	props     map[string]interface{} // shared allowlisted properties
	heartbeat time.Duration
	stop      chan struct{}
	stopOnce  sync.Once
	closeOnce sync.Once
}

var (
	instance = &Service{stop: make(chan struct{})}
	initOnce sync.Once
)

// Init builds (or reuses) the process-wide service. It never fails: every
// error degrades to "disabled" with an info/warn log.
func Init(ctx context.Context, cfg InitConfig) *Service {
	initOnce.Do(func() {
		cfg.Logger = orLogger(cfg.Logger)
		if reason := decideEnabled(cfg.Disabled); reason != "" {
			cfg.Logger.Info("telemetry disabled", "reason", reason)
			return
		}
		if cfg.Heartbeat <= 0 {
			cfg.Heartbeat = HeartbeatInterval
		}

		runner := cfg.Runner
		if runner == nil {
			runner = defaultRunner
		}
		id := resolveIdentity(ctx, runner)

		s := &Service{
			identity:  id,
			heartbeat: cfg.Heartbeat,
			stop:      make(chan struct{}),
			props: map[string]interface{}{
				"service":    "pdfm-mcp",
				"version":    version.Version,
				"os":         runtime.GOOS,
				"arch":       runtime.GOARCH,
				"mode":       cfg.Mode,
				"pd_edition": id.Edition,
			},
		}

		amplCfg := amplitude.Config{
			APIKey:            APIKey,
			FlushQueueSize:    100,
			FlushInterval:     5 * time.Second,
			ConnectionTimeout: 10 * time.Second,
			Logger:            &slogAdapter{logger: cfg.Logger},
			ExecuteCallback: func(result amplitude.ExecuteResult) {
				if result.Code >= 400 {
					cfg.Logger.Warn("amplitude rejected a flush", "status", result.Code, "error", result.Message)
				}
			},
		}
		if cfg.ServerURL != "" {
			amplCfg.ServerURL = cfg.ServerURL
		}

		s.mu.Lock()
		s.client = amplitude.NewClient(amplCfg)
		s.enabled = true
		s.mu.Unlock()

		// Identifiers only (already anonymised per spec 024 section 2),
		// never the API key.
		cfg.Logger.Info("telemetry enabled", "device_id", id.DeviceID, "user_id", id.UserID, "pd_edition", id.Edition)
		s.track(EventServerStarted, nil)
		go s.runHeartbeat()

		instance = s
	})
	return instance
}

// decideEnabled returns the disable reason, or "" when telemetry should run.
// The opt-out always wins over the (build-time) key.
func decideEnabled(disabled bool) string {
	if disabled {
		return "opt-out set"
	}
	if APIKey == "" {
		return "no API key"
	}
	return ""
}

// IsEnabled reports whether the process-wide service sends events.
func IsEnabled() bool { return instance.IsEnabledOn() }

// IsEnabledOn reports whether this service instance sends events.
func (s *Service) IsEnabledOn() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// TrackToolCall records one tool invocation (spec 024 section 3). It never
// blocks: the SDK queues events in memory and flushes in the background.
// The handler's error message is deliberately not part of the event.
func (s *Service) TrackToolCall(name string, ok bool, d time.Duration) {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	s.track(toolEventName(name), map[string]interface{}{
		"tool":        name,
		"ok":          ok,
		"duration_ms": ms,
	})
}

// Instrument wraps a tool handler with the per-call telemetry event. The
// wrapper is always installed (one flag check per call when disabled) so
// every registration call site stays uniform.
func Instrument[In, Out any](name string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		result, out, err := h(ctx, req, in)
		instance.TrackToolCall(name, err == nil, time.Since(start))
		return result, out, err
	}
}

// Close flushes queued events and stops the heartbeat. Safe to call more
// than once; call it from the server's shutdown path.
func Close() { instance.Close() }

// Close flushes this service's queued events and stops its heartbeat. Safe
// to call more than once; a disabled service is a no-op.
func (s *Service) Close() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stop) })
	s.closeOnce.Do(func() {
		s.mu.RLock()
		client := s.client
		enabled := s.enabled
		s.mu.RUnlock()
		if enabled && client != nil {
			client.Flush()
		}
	})
}

// track sends one event with the shared allowlisted properties merged with
// the event-specific ones. A disabled service drops the event silently.
func (s *Service) track(eventType string, specific map[string]interface{}) {
	s.mu.RLock()
	enabled := s.enabled
	client := s.client
	identity := s.identity
	base := s.props
	s.mu.RUnlock()
	if !enabled || client == nil {
		return
	}

	props := make(map[string]interface{}, len(base)+len(specific))
	for k, v := range base {
		props[k] = v
	}
	for k, v := range specific {
		props[k] = v
	}
	client.Track(amplitude.Event{
		EventType:       eventType,
		UserID:          identity.UserID,
		DeviceID:        identity.DeviceID,
		EventProperties: props,
	})
}

// runHeartbeat sends the liveness event until Close is called.
func (s *Service) runHeartbeat() {
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.track(EventHeartbeat, nil)
		}
	}
}

// Reset replaces the process-wide instance with a fresh disabled one.
// Tests only.
func Reset() {
	instance.Close()
	instance = &Service{stop: make(chan struct{})}
	initOnce = sync.Once{}
}

// orLogger falls back to the standard logger so a nil logger can never
// panic the side channel.
func orLogger(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}

// slogAdapter forwards the SDK's logger interface to slog. The format
// string and arguments come from the SDK, not from tool input, so Sprintf
// is safe. SDK chatter is debug-level (it can include event payloads);
// real problems surface via the ExecuteCallback at warn.
type slogAdapter struct{ logger *slog.Logger }

func (a *slogAdapter) Debugf(format string, args ...interface{}) {
	if a.logger.Enabled(context.Background(), slog.LevelDebug) {
		a.logger.Debug("amplitude: " + fmt.Sprintf(format, args...))
	}
}

func (a *slogAdapter) Infof(format string, args ...interface{}) {
	if a.logger.Enabled(context.Background(), slog.LevelDebug) {
		a.logger.Debug("amplitude: " + fmt.Sprintf(format, args...))
	}
}

func (a *slogAdapter) Warnf(format string, args ...interface{}) {
	a.logger.Warn("amplitude: " + fmt.Sprintf(format, args...))
}

func (a *slogAdapter) Errorf(format string, args ...interface{}) {
	a.logger.Error("amplitude: " + fmt.Sprintf(format, args...))
}
