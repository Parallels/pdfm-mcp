// Package interfaces defines the contracts of the Parallels Desktop module.
package interfaces

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cjlapao/pdfm-mcp/internal/parallels/models"
)

// VMListResult is the outcome of a ListVMs call.
type VMListResult struct {
	VMs       []models.VM
	FetchedAt time.Time
	// Refreshed is true when this call executed a prlctl list command
	// (cold/stale cache or forced); false means it was served from memory.
	Refreshed bool
}

// VMRawResult is the outcome of a VMRaw call.
type VMRawResult struct {
	Blob      json.RawMessage // the exact prlctl JSON object for the target VM
	FetchedAt time.Time
	Refreshed bool
}

// ParallelsService is the contract the MCP tools consume. Implementations are
// expected to be singletons created via the parallels facade.
type ParallelsService interface {
	// Enabled reports whether prlctl and prlsrvctl were found at startup.
	Enabled() bool

	// VMID returns the configured target VM id or name (empty in fleet mode).
	VMID() string

	// PRLCtlPath / PRLSrvCtlPath return the resolved binary locations.
	PRLCtlPath() string
	PRLSrvCtlPath() string

	// ListVMs returns the cached VM list. In VM mode only the target VM is
	// returned. When the cache is empty or older than the TTL (or force is
	// true) the list command is re-run first.
	ListVMs(ctx context.Context, force bool) (VMListResult, error)

	// VMRaw returns the exact cached prlctl JSON blob of the target VM.
	// It is only meaningful in VM mode.
	VMRaw(ctx context.Context, force bool) (VMRawResult, error)

	// RefreshCache re-runs the list command unconditionally and replaces the
	// cache contents.
	RefreshCache(ctx context.Context) error

	// InvalidateCache marks the whole cache as stale (call after a
	// command that mutates VM state).
	InvalidateCache()
}
