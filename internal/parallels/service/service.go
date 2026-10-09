// Package service implements the Parallels Desktop module: binary detection,
// command execution and the in-memory VM cache. It follows the
// PLAI-client-agent service pattern: a package-level singleton created by
// Initialize and consumed via GetInstance.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cjlapao/pdfm-mcp/internal/parallels/interfaces"
	"github.com/cjlapao/pdfm-mcp/internal/parallels/models"
)

// DefaultCacheTTL is how long the VM list cache stays fresh.
const DefaultCacheTTL = 30 * time.Second

// defaultFallbackDirs are checked when exec.LookPath misses. MCP clients
// (Claude Desktop and friends) may launch us with a minimal PATH.
var defaultFallbackDirs = []string{"/usr/local/bin", "/usr/bin"}

var (
	instance *service
	once     sync.Once
)

// ErrNotEnabled is returned when a method is used while the module is
// disabled (prlctl/prlsrvctl not found).
var ErrNotEnabled = errors.New("parallels module is disabled: prlctl or prlsrvctl not found")

// Option configures the service during initialization.
type Option func(*options)

type options struct {
	vmID         string
	logger       *slog.Logger
	runner       CommandRunner
	lookPath     func(string) (string, error)
	cacheTTL     time.Duration
	fallbackDirs []string
	hasFallback  bool // false = defaultFallbackDirs
}

// WithVMID sets the target VM id or name (empty = fleet mode).
func WithVMID(vmID string) Option {
	return func(o *options) { o.vmID = vmID }
}

// WithLogger sets the slog logger (defaults to slog.Default()).
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithRunner overrides the command runner (tests inject a fake).
func WithRunner(runner CommandRunner) Option {
	return func(o *options) { o.runner = runner }
}

// WithLookPath overrides executable resolution (tests inject a fake).
func WithLookPath(fn func(string) (string, error)) Option {
	return func(o *options) { o.lookPath = fn }
}

// WithCacheTTL sets the cache TTL (default 30s).
func WithCacheTTL(ttl time.Duration) Option {
	return func(o *options) { o.cacheTTL = ttl }
}

// WithFallbackDirs overrides the well-known dirs checked when LookPath
// misses (pass no dirs to disable fallbacks; tests only). Not calling this
// keeps the defaults.
func WithFallbackDirs(dirs ...string) Option {
	return func(o *options) {
		o.fallbackDirs = dirs
		o.hasFallback = true
	}
}

// Initialize creates the singleton (once) and reports whether the module is
// enabled, i.e. both prlctl and prlsrvctl were found.
func Initialize(opts ...Option) (interfaces.ParallelsService, bool) {
	once.Do(func() { instance = newService(opts...) })
	if !instance.enabled {
		return nil, false
	}
	return instance, true
}

// GetInstance returns the singleton. It panics when Initialize has not
// run (PLAI-client-agent convention) — tools only call it after the server
// has confirmed the module is enabled.
func GetInstance() interfaces.ParallelsService {
	if instance == nil {
		panic("parallels: service not initialized; call Initialize first")
	}
	return instance
}

// Reset discards the singleton (tests only).
func Reset() {
	once = sync.Once{}
	instance = nil
}

type service struct {
	enabled       bool
	prlctlPath    string
	prlsrvctlPath string
	vmID          string
	logger        *slog.Logger
	runner        CommandRunner
	cache         *vmCache
	fallbackDirs  []string

	targetMu   sync.RWMutex
	resolvedID string // id resolved from a name by the last fetch
}

func newService(opts ...Option) *service {
	o := &options{
		logger:   slog.Default(),
		runner:   NewCommandRunner(),
		lookPath: exec.LookPath,
		cacheTTL: DefaultCacheTTL,
	}
	for _, fn := range opts {
		fn(o)
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}

	fallbackDirs := defaultFallbackDirs
	if o.hasFallback {
		fallbackDirs = o.fallbackDirs
	}

	s := &service{
		logger:       o.logger,
		runner:       o.runner,
		cache:        newVMCache(o.cacheTTL),
		vmID:         o.vmID,
		fallbackDirs: fallbackDirs,
	}

	prlctl, errPrlctl := resolveExe(o.lookPath, s.fallbackDirs, "prlctl")
	prlsrvctl, errSrv := resolveExe(o.lookPath, s.fallbackDirs, "prlsrvctl")
	if errPrlctl != nil || errSrv != nil {
		o.logger.Info("parallels module disabled: Parallels Desktop CLI not found",
			"prlctl", errString(errPrlctl), "prlsrvctl", errString(errSrv))
		return s
	}

	s.prlctlPath = prlctl
	s.prlsrvctlPath = prlsrvctl
	s.enabled = true
	o.logger.Info("parallels module enabled",
		"prlctl", prlctl, "prlsrvctl", prlsrvctl, "vm_id", s.vmID, "cache_ttl", o.cacheTTL)
	return s
}

func errString(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// resolveExe finds name via lookPath, then in the well-known fallback dirs.
func resolveExe(lookPath func(string) (string, error), fallbackDirs []string, name string) (string, error) {
	if p, err := lookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range fallbackDirs {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH or %s", name, strings.Join(fallbackDirs, " or "))
}

// ---- interfaces.ParallelsService ------------------------------------------

func (s *service) Enabled() bool { return s.enabled }

func (s *service) VMID() string { return s.vmID }

func (s *service) PRLCtlPath() string { return s.prlctlPath }

func (s *service) PRLSrvCtlPath() string { return s.prlsrvctlPath }

func (s *service) ListVMs(ctx context.Context, force bool) (interfaces.VMListResult, error) {
	vms, fetchedAt, refreshed, err := s.cache.getAll(ctx, s.fetch, force)
	if err != nil {
		return interfaces.VMListResult{}, err
	}
	if s.vmID != "" {
		ref := s.targetRef()
		filtered := vms[:0]
		for _, vm := range vms {
			if vm.ID == ref {
				filtered = append(filtered, vm)
			}
		}
		vms = filtered
	}
	return interfaces.VMListResult{VMs: vms, FetchedAt: fetchedAt, Refreshed: refreshed}, nil
}

func (s *service) VMRaw(ctx context.Context, force bool) (interfaces.VMRawResult, error) {
	if s.vmID == "" {
		return interfaces.VMRawResult{}, errors.New("no target VM configured: the server was started without --vm-id")
	}
	blob, fetchedAt, refreshed, err := s.cache.getRaw(ctx, s.targetRef(), s.fetch, force)
	if err != nil {
		return interfaces.VMRawResult{}, err
	}
	return interfaces.VMRawResult{Blob: blob, FetchedAt: fetchedAt, Refreshed: refreshed}, nil
}

func (s *service) RefreshCache(ctx context.Context) error {
	_, err := s.cache.refreshIfStale(ctx, s.fetch, true)
	return err
}

func (s *service) InvalidateCache() { s.cache.invalidate() }

// targetRef returns the cache key of the target VM: the id resolved by the
// last fetch when the configured value was a name, the configured value
// otherwise.
func (s *service) targetRef() string {
	s.targetMu.RLock()
	defer s.targetMu.RUnlock()
	if s.resolvedID != "" {
		return s.resolvedID
	}
	return s.vmID
}

func (s *service) setResolvedID(id string) {
	s.targetMu.Lock()
	s.resolvedID = id
	s.targetMu.Unlock()
}

// fetch runs `prlctl list [-a -i --json] [target]` and replaces the cache.
func (s *service) fetch(ctx context.Context) error {
	args := []string{"list"}
	if s.vmID != "" {
		args = append(args, s.vmID) // validated at config load; single argv element, never shell-interpolated
	}
	args = append(args, "-a", "-i", "--json")

	s.logger.Debug("running prlctl list", "vm_id", s.vmID)
	out, err := s.runner.Run(ctx, s.prlctlPath, args...)
	if err != nil {
		s.logger.Warn("prlctl list failed", "err", err)
		return err
	}
	out = strings.TrimSpace(out)

	var blobs []json.RawMessage
	if out != "" {
		if err := json.Unmarshal([]byte(out), &blobs); err != nil {
			s.logger.Debug("unparseable prlctl list output", "head", first500(out))
			return fmt.Errorf("unparseable output from prlctl list: %w", err)
		}
	}

	vms := make([]*models.VM, 0, len(blobs))
	raw := make(map[string]json.RawMessage, len(blobs))
	for i, b := range blobs {
		var vm models.VM
		if err := json.Unmarshal(b, &vm); err != nil {
			return fmt.Errorf("parse VM entry %d: %w", i, err)
		}
		if vm.ID == "" {
			return fmt.Errorf("VM entry %d is missing its ID", i)
		}
		vm.Normalize()
		vms = append(vms, &vm)
		raw[vm.ID] = b
	}

	s.cache.store(vms, raw)
	s.setResolvedID("")
	if s.vmID != "" {
		if len(vms) == 0 {
			return fmt.Errorf("VM %q not found (prlctl returned no results)", s.vmID)
		}
		if len(vms) > 1 {
			s.logger.Warn("multiple VMs matched the target", "vm_id", s.vmID, "matched", len(vms))
		}
		s.setResolvedID(vms[0].ID)
	}
	s.logger.Debug("VM cache refreshed", "vms", len(vms))
	return nil
}

func first500(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}
