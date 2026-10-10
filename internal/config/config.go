// Package config loads runtime configuration from flags and environment.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Parallels/pdfm-mcp/internal/parallels"
	"github.com/Parallels/pdfm-mcp/internal/policy"
)

// DefaultVMCacheTTL is how long the Parallels VM list cache stays fresh.
const DefaultVMCacheTTL = 30 * time.Second

// Config holds runtime settings.
type Config struct {
	LogLevel    slog.Level
	LogFile     string
	ShowVersion bool

	// VMID is the target VM id or name (Parallels Desktop module). Empty =
	// fleet mode: commands address all VMs.
	VMID string
	// VMCacheTTL is the freshness window of the in-memory VM list cache.
	VMCacheTTL time.Duration

	// Safety policy (spec 019). ReadOnly exposes only read-class tools.
	// AllowTools (when non-empty) restricts the exposed tools to these
	// names/prefix globs; DenyTools removes tools and always wins. All
	// entries are validated by policy.ParseToolList.
	ReadOnly   bool
	AllowTools []string
	DenyTools  []string
	// AllowVMs restricts the VMs the tools may address (ids or names,
	// validated, max 64). Empty = no restriction.
	AllowVMs []string

	// Telemetry (spec 024). The API key is baked in at build time (see
	// internal/telemetry.APIKey); TelemetryDisabled is the runtime
	// opt-out (PDFM_MCP_TELEMETRY_DISABLED) and always wins.
	TelemetryDisabled bool
}

// Load parses args (without the program name) and the environment.
func Load(args []string, stderr io.Writer) (Config, error) {
	warn := slog.New(slog.NewTextHandler(stderr, nil))
	getenv := func(key string) string { return guardedEnv(key, warn) }

	var (
		cfg        Config
		levelStr   = orDefault(getenv("PDFM_MCP_LOG_LEVEL"), "info")
		vmID       = getenv("PDFM_MCP_VM_ID")
		ttlStr     = getenv("PDFM_MCP_VM_CACHE_TTL")
		allowTools = getenv("PDFM_MCP_ALLOW_TOOLS")
		denyTools  = getenv("PDFM_MCP_DENY_TOOLS")
		allowVMs   = getenv("PDFM_MCP_ALLOW_VMS")
	)
	readOnly, err := parseBoolEnv("PDFM_MCP_READ_ONLY", getenv("PDFM_MCP_READ_ONLY"))
	if err != nil {
		return cfg, err
	}
	telemetryDisabled, err := parseBoolEnv("PDFM_MCP_TELEMETRY_DISABLED", getenv("PDFM_MCP_TELEMETRY_DISABLED"))
	if err != nil {
		return cfg, err
	}

	fs := flag.NewFlagSet("pdfm-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&cfg.ShowVersion, "version", false, "print version and exit")
	fs.StringVar(&levelStr, "log-level", levelStr, "log level: debug|info|warn|error")
	fs.StringVar(&vmID, "vm-id", vmID, "Parallels VM id or name to target (empty = all VMs)")
	fs.BoolVar(&readOnly, "read-only", readOnly, "expose only read-class tools (no power, input, exec, destructive or secret tools)")
	fs.StringVar(&allowTools, "allow-tools", allowTools, "comma-separated tool names or prefix globs (trailing *); when set, only these tools are exposed")
	fs.StringVar(&denyTools, "deny-tools", denyTools, "comma-separated tool names or prefix globs (trailing *) to hide; wins over --allow-tools")
	fs.StringVar(&allowVMs, "allow-vms", allowVMs, "comma-separated VM ids or names the tools may address (empty = all VMs)")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	cfg.LogFile = os.Getenv("PDFM_MCP_LOG_FILE")
	if err := cfg.LogLevel.UnmarshalText([]byte(strings.ToLower(levelStr))); err != nil {
		return cfg, err
	}

	if err := validateVMID(vmID); err != nil {
		return cfg, err
	}
	// Store the normalized form (trimmed, one pair of surrounding quotes
	// stripped) so the configured target is always what prlctl receives
	// (spec 025).
	cfg.VMID = parallels.UnquoteVMRef(vmID)

	cfg.ReadOnly = readOnly
	if cfg.AllowTools, err = policy.ParseToolList(allowTools); err != nil {
		return cfg, fmt.Errorf("invalid --allow-tools: %w", err)
	}
	if cfg.DenyTools, err = policy.ParseToolList(denyTools); err != nil {
		return cfg, fmt.Errorf("invalid --deny-tools: %w", err)
	}
	if cfg.AllowVMs, err = policy.ParseVMList(allowVMs); err != nil {
		return cfg, fmt.Errorf("invalid --allow-vms: %w", err)
	}
	// VM mode: the configured target must itself be allowed (exact match;
	// the VM cache is not available at config time).
	if cfg.VMID != "" && len(cfg.AllowVMs) > 0 && !slices.Contains(cfg.AllowVMs, cfg.VMID) {
		return cfg, errors.New("--vm-id is not in --allow-vms")
	}

	cfg.TelemetryDisabled = telemetryDisabled

	if ttlStr == "" {
		cfg.VMCacheTTL = DefaultVMCacheTTL
	} else {
		ttl, err := time.ParseDuration(ttlStr)
		if err != nil {
			return cfg, fmt.Errorf("invalid PDFM_MCP_VM_CACHE_TTL %q: %w", ttlStr, err)
		}
		if ttl <= 0 {
			return cfg, fmt.Errorf("PDFM_MCP_VM_CACHE_TTL must be positive, got %s", ttlStr)
		}
		cfg.VMCacheTTL = ttl
	}
	return cfg, nil
}

// unresolvedPlaceholder reports values that look like an unsubstituted
// template variable (for example "${user_config.vm_id}") left by a host that
// did not fill an optional setting (spec 015 section 6).
func unresolvedPlaceholder(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}")
}

// guardedEnv returns the environment variable, or "" when it is unset or an
// unresolved placeholder (one warn line naming the variable, never the
// value).
func guardedEnv(key string, warn *slog.Logger) string {
	v := os.Getenv(key)
	if unresolvedPlaceholder(v) {
		warn.Warn("ignoring unresolved placeholder in environment variable; treating it as unset", "var", key)
		return ""
	}
	return v
}

// parseBoolEnv parses a strict boolean environment value; empty is false.
func parseBoolEnv(key, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	default:
		return false, fmt.Errorf("invalid %s: want true or false", key)
	}
}

// validateVMID rejects values that could not be a VM id or name. Empty is
// valid (fleet mode). The actual rules live in parallels.ValidateVMRef so
// that startup (--vm-id) and per-call tool arguments share one
// implementation.
func validateVMID(vmID string) error {
	if strings.TrimSpace(vmID) == "" {
		return nil
	}
	if _, err := parallels.ValidateVMRef(vmID); err != nil {
		return fmt.Errorf("invalid --vm-id: %w", err)
	}
	return nil
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
