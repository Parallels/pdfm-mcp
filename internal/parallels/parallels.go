// Package parallels is the facade of the Parallels Desktop module.
//
// It follows the PLAI-client-agent service pattern: a thin re-export layer
// over an interfaces/ package and a service/ package that owns the
// singleton. Consumers should only import this package.
package parallels

import (
	"log/slog"
	"time"

	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/service"
)

// Option configures the service during initialization.
type Option = service.Option

// CommandRunner executes a Parallels CLI binary (tests inject a fake).
type CommandRunner = service.CommandRunner

// NewCommandRunner returns the production command runner.
func NewCommandRunner() CommandRunner { return service.NewCommandRunner() }

// DefaultCacheTTL is the cache freshness window used when none is configured.
const DefaultCacheTTL = service.DefaultCacheTTL

// WithVMID sets the target VM id or name (empty = fleet mode).
func WithVMID(vmID string) Option { return service.WithVMID(vmID) }

// WithLogger sets the slog logger (defaults to slog.Default()).
func WithLogger(logger *slog.Logger) Option { return service.WithLogger(logger) }

// WithRunner overrides the command runner (tests inject a fake).
func WithRunner(runner CommandRunner) Option { return service.WithRunner(runner) }

// WithLookPath overrides executable resolution (tests inject a fake).
func WithLookPath(fn func(string) (string, error)) Option { return service.WithLookPath(fn) }

// WithCacheTTL sets the cache TTL (default 30s).
func WithCacheTTL(ttl time.Duration) Option { return service.WithCacheTTL(ttl) }

// WithFallbackDirs overrides the well-known dirs checked when LookPath
// misses (pass no dirs to disable fallbacks; tests only).
func WithFallbackDirs(dirs ...string) Option { return service.WithFallbackDirs(dirs...) }

// Initialize creates the module singleton (once) and reports whether it is
// enabled — i.e. both prlctl and prlsrvctl were found. When disabled the
// caller must not register any parallels_* tool.
func Initialize(opts ...Option) (interfaces.ParallelsService, bool) {
	return service.Initialize(opts...)
}

// GetInstance returns the singleton. It panics when Initialize has not run.
func GetInstance() interfaces.ParallelsService {
	return service.GetInstance()
}

// Reset discards the singleton (tests only).
func Reset() { service.Reset() }
