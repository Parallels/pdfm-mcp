package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLockSerializesSameVM(t *testing.T) {
	locks := newVMLocks()
	ctx := context.Background()

	if err := locks.acquire(ctx, "vmA"); err != nil {
		t.Fatal(err)
	}
	// Second acquisition of the same VM must block.
	done := make(chan struct{})
	go func() {
		if err := locks.acquire(ctx, "vmA"); err != nil {
			t.Error(err)
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("second acquire of the same VM must block")
	case <-time.After(50 * time.Millisecond):
	}
	locks.release("vmA")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second acquire must proceed after release")
	}
	locks.release("vmA")
}

func TestLockDifferentVMsIndependent(t *testing.T) {
	locks := newVMLocks()
	ctx := context.Background()
	if err := locks.acquire(ctx, "vmA"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := locks.acquire(ctx, "vmB"); err != nil {
			t.Error(err)
		}
		locks.release("vmB")
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("different VM must not be blocked")
	}
	locks.release("vmA")
}

func TestLockCancellationUnblocks(t *testing.T) {
	locks := newVMLocks()
	ctx := context.Background()
	if err := locks.acquire(ctx, "vmA"); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- locks.acquire(ctx2, "vmA")
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("cancelled acquire must return an error")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel must unblock the wait")
	}
	locks.release("vmA")
}

func TestLockConcurrentSameVMNoDoubleHold(t *testing.T) {
	locks := newVMLocks()
	ctx := context.Background()
	const n = 8
	var mu sync.Mutex
	inside := 0
	maxInside := 0
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := locks.acquire(ctx, "vm"); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			inside++
			if inside > maxInside {
				maxInside = inside
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			locks.release("vm")
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("maxInside = %d, want 1", maxInside)
	}
}
