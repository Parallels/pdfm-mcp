// Package config loads runtime configuration from flags and environment.
package config

import (
	"flag"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config holds runtime settings.
type Config struct {
	LogLevel    slog.Level
	LogFile     string
	ShowVersion bool
}

// Load parses args (without the program name) and the environment.
func Load(args []string, stderr io.Writer) (Config, error) {
	var (
		cfg      Config
		levelStr = envOr("PDFM_MCP_LOG_LEVEL", "info")
	)

	fs := flag.NewFlagSet("pdfm-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&cfg.ShowVersion, "version", false, "print version and exit")
	fs.StringVar(&levelStr, "log-level", levelStr, "log level: debug|info|warn|error")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	cfg.LogFile = os.Getenv("PDFM_MCP_LOG_FILE")
	if err := cfg.LogLevel.UnmarshalText([]byte(strings.ToLower(levelStr))); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
