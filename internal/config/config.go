// Package config loads runtime configuration from flags and environment.
package config

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// DefaultVMCacheTTL is how long the Parallels VM list cache stays fresh.
const DefaultVMCacheTTL = 30 * time.Second

// maxVMIDLength bounds the --vm-id value; prlctl ids are UUIDs and names are
// short — anything longer is a mistake or an attack.
const maxVMIDLength = 256

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
}

// Load parses args (without the program name) and the environment.
func Load(args []string, stderr io.Writer) (Config, error) {
	var (
		cfg      Config
		levelStr = envOr("PDFM_MCP_LOG_LEVEL", "info")
		vmID     = envOr("PDFM_MCP_VM_ID", "")
		ttlStr   = envOr("PDFM_MCP_VM_CACHE_TTL", "")
	)

	fs := flag.NewFlagSet("pdfm-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&cfg.ShowVersion, "version", false, "print version and exit")
	fs.StringVar(&levelStr, "log-level", levelStr, "log level: debug|info|warn|error")
	fs.StringVar(&vmID, "vm-id", vmID, "Parallels VM id or name to target (empty = all VMs)")
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
	cfg.VMID = strings.TrimSpace(vmID)

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

// validateVMID rejects values that could not be a VM id or name. The value
// is later passed to prlctl as a single argv element (no shell), but we keep
// the surface small anyway.
func validateVMID(vmID string) error {
	vmID = strings.TrimSpace(vmID)
	if vmID == "" {
		return nil
	}
	if len(vmID) > maxVMIDLength {
		return fmt.Errorf("invalid --vm-id: longer than %d characters", maxVMIDLength)
	}
	for _, r := range vmID {
		if r == 0 || r < 0x20 || r > 0x7e {
			return fmt.Errorf("invalid --vm-id: contains non-printable characters")
		}
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
