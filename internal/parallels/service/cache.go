package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cjlapao/pdfm-mcp/internal/parallels/models"
)

// ErrEmptyCache is returned when the cache has never been populated and no
// refresh could run.
var ErrEmptyCache = errors.New("VM cache is empty and no refresh could run")

// vmCache is the in-memory store for `prlctl list -a -i --json` results:
// a raw JSON blob per VM id ("the blobs") plus a typed index for fast field
// access. Readers never block on a refresh: freshness is decided under a
// read lock, the refresh runs single-flight under refreshMu, and both paths
// swap immutable snapshots.
type vmCache struct {
	mu        sync.RWMutex
	vms       []*models.VM
	raw       map[string]json.RawMessage
	fetchedAt time.Time

	ttl       time.Duration
	refreshMu sync.Mutex // single-flight: only one refresh at a time
}

func newVMCache(ttl time.Duration) *vmCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &vmCache{ttl: ttl}
}

// store replaces the cache contents with the result of one list command.
func (c *vmCache) store(vms []*models.VM, blobs map[string]json.RawMessage) {
	c.mu.Lock()
	c.vms = vms
	c.raw = blobs
	c.fetchedAt = time.Now()
	c.mu.Unlock()
}

// stale reports whether the cache needs a refresh.
func (c *vmCache) stale() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fetchedAt.IsZero() || time.Since(c.fetchedAt) > c.ttl
}

// refreshIfStale re-runs fetch exactly once per burst of concurrent callers.
// It returns the fetch error only when the cache was still empty afterwards.
// The bool says whether a fetch ran during this call.
func (c *vmCache) refreshIfStale(ctx context.Context, fetch func(ctx context.Context) error, force bool) (bool, error) {
	if !force && !c.stale() {
		return false, nil
	}

	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if !force && !c.stale() {
		return false, nil // another caller refreshed while we waited
	}

	err := fetch(ctx)
	c.mu.RLock()
	empty := c.fetchedAt.IsZero()
	c.mu.RUnlock()
	if err != nil {
		if empty {
			return true, fmt.Errorf("%w: %v", ErrEmptyCache, err)
		}
		return true, err // stale data retained; caller may decide how to surface it
	}
	return true, nil
}

// getAll returns a copy of the typed VM list, refreshing first when needed.
func (c *vmCache) getAll(ctx context.Context, fetch func(ctx context.Context) error, force bool) ([]models.VM, time.Time, bool, error) {
	refreshed, err := c.refreshIfStale(ctx, fetch, force)
	if err != nil {
		return nil, time.Time{}, refreshed, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]models.VM, 0, len(c.vms))
	for _, vm := range c.vms {
		out = append(out, *vm)
	}
	return out, c.fetchedAt, refreshed, nil
}

// getRaw returns the cached raw JSON blob for one VM id, refreshing first
// when needed.
func (c *vmCache) getRaw(ctx context.Context, id string, fetch func(ctx context.Context) error, force bool) (json.RawMessage, time.Time, bool, error) {
	refreshed, err := c.refreshIfStale(ctx, fetch, force)
	if err != nil {
		return nil, time.Time{}, refreshed, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	blob, ok := c.raw[id]
	if !ok {
		return nil, c.fetchedAt, refreshed, errVMNotFound(id)
	}
	return append(json.RawMessage(nil), blob...), c.fetchedAt, refreshed, nil
}

// invalidate marks the whole cache as stale (used after mutating commands).
func (c *vmCache) invalidate() {
	c.mu.Lock()
	c.fetchedAt = time.Time{}
	c.mu.Unlock()
}

// invalidateID drops one VM from the cache and marks it stale.
func (c *vmCache) invalidateID(id string) {
	c.mu.Lock()
	if c.raw != nil {
		delete(c.raw, id)
	}
	c.fetchedAt = time.Time{}
	c.mu.Unlock()
}

// errVMNotFound wraps the missing-id case; the id is validated by callers
// before it reaches here, but keep the format string safe regardless.
func errVMNotFound(id string) error {
	return fmt.Errorf("VM %q not found in cache", id)
}
