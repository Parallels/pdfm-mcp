package orchestrator

import (
	"context"
	"sync"
)

// vmLocks is a map of per-VM mutexes. A sequence for a given VM waits for
// any other sequence on the same VM; different VMs run concurrently. The
// wait is context-aware: client cancellation unblocks it.
type vmLocks struct {
	mu    sync.Mutex
	locks map[string]*vmLock
}

type vmLock struct {
	ch chan struct{} // capacity 1
}

func newVMLocks() *vmLocks {
	return &vmLocks{locks: map[string]*vmLock{}}
}

func (l *vmLocks) get(vm string) *vmLock {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.locks[vm]
	if !ok {
		v = &vmLock{ch: make(chan struct{}, 1)}
		l.locks[vm] = v
	}
	return v
}

// acquire blocks until the VM lock is held or ctx is done.
func (l *vmLocks) acquire(ctx context.Context, vm string) error {
	lock := l.get(vm)
	select {
	case lock.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *vmLocks) release(vm string) {
	lock := l.get(vm)
	<-lock.ch
}
