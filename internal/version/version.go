// Package version exposes build metadata injected via -ldflags.
package version

import "fmt"

// These are overwritten at build time by the Makefile.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human readable version line.
func String() string {
	return fmt.Sprintf("pdfm-mcp %s (commit %s, built %s)", Version, Commit, Date)
}
