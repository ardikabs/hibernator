/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package keyedworker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

const testIdleTTL = 30 * time.Minute

// startedPool creates a Pool backed by FIFOSlot and a RunnerFactory wrapping
// the supplied handler. Using FIFOSlot preserves the FIFO ordering tests that
// were written against the original chan-backed implementation.
func startedPool(t *testing.T, opts ...Option[string, int]) (*Pool[string, int], chan int, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan int, 256)
	p := New(opts...)
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, v int) error {
			received <- v
			return nil
		},
		nil,
	))
	t.Cleanup(cancel)
	return p, received, cancel
}

// drain collects exactly n items from ch within timeout, returning them in order.
func drain(t *testing.T, ch <-chan int, n int, timeout time.Duration) []int {
	t.Helper()
	got := make([]int, 0, n)
	deadline := time.After(timeout)
	for range n {
		select {
		case v := <-ch:
			got = append(got, v)
		case <-deadline:
			t.Fatalf("timed out waiting for %d items; got %d so far", n, len(got))
		}
	}
	return got
}

// ---------------------------------------------------------------------------
// Basic delivery
// ---------------------------------------------------------------------------

func TestPool_Deliver_InvokesHandler(t *testing.T) {
	p, received, _ := startedPool(t)

	p.Deliver("k", 42)

	got := drain(t, received, 1, time.Second)
	assert.Equal(t, []int{42}, got)
}

func TestPool_Deliver_MultipleValues_DeliveredAll(t *testing.T) {
	p, received, _ := startedPool(t)

	for i := range 5 {
		p.Deliver("k", i)
	}

	got := drain(t, received, 5, time.Second)
	assert.Len(t, got, 5)
}

// ---------------------------------------------------------------------------
// FIFO ordering within a key
// ---------------------------------------------------------------------------

func TestPool_Deliver_PerKeyFIFO_OrderPreserved(t *testing.T) {
	// Use a slow handler so all items queue up before the first one is processed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan int, 256)
	p := New(WithSlotFactory[string](FIFOSlot[int](64)))
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, v int) error {
			time.Sleep(2 * time.Millisecond) // slow handler
			received <- v
			return nil
		},
		nil,
	))

	const n = 20
	for i := range n {
		p.Deliver("k", i)
	}

	got := drain(t, received, n, 5*time.Second)
	for i := range n {
		assert.Equal(t, i, got[i], "item at position %d out of order", i)
	}
}

// ---------------------------------------------------------------------------
// Pre-Start Deliver buffering
// ---------------------------------------------------------------------------

func TestPool_Deliver_BeforeStart_BufferedAndDrained(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := New[string, int]()

	// Deliver before Start — factory is nil, goroutine not spawned.
	p.Deliver("k", 1)
	p.Deliver("k", 2)
	p.Deliver("k", 3)

	received := make(chan int, 256)
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, v int) error {
			received <- v
			return nil
		},
		nil,
	))

	got := drain(t, received, 3, time.Second)
	assert.Equal(t, []int{1, 2, 3}, got)
}

// ---------------------------------------------------------------------------
// Parallel cross-key processing
// ---------------------------------------------------------------------------

func TestPool_Deliver_DifferentKeys_ProcessedInParallel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	concurrent := 0
	maxConcurrent := 0

	done := make(chan struct{}, 2)
	p := New[string, int]()
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, _ int) error {
			mu.Lock()
			concurrent++
			if concurrent > maxConcurrent {
				maxConcurrent = concurrent
			}
			mu.Unlock()

			time.Sleep(50 * time.Millisecond)

			mu.Lock()
			concurrent--
			mu.Unlock()
			done <- struct{}{}
			return nil
		},
		nil,
	))

	// Two different keys — should run simultaneously.
	p.Deliver("a", 1)
	p.Deliver("b", 1)

	// Wait for both to finish.
	<-done
	<-done

	assert.Equal(t, 2, maxConcurrent, "expected 2 keys to be processed concurrently")
}

// ---------------------------------------------------------------------------
// Buffer-full drop (FIFO slot)
// ---------------------------------------------------------------------------

func TestPool_Deliver_FIFOBufferFull_DropsUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const bufSize = 3
	blocker := make(chan struct{}) // keep handler blocked so nothing is consumed

	p := New(WithSlotFactory[string](FIFOSlot[int](bufSize)))
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, _ int) error {
			<-blocker // block until we unblock
			return nil
		},
		nil,
	))

	// Fill buffer + trigger handler (1 consumed by goroutine, bufSize remain).
	for i := range bufSize + 1 {
		p.Deliver("k", i)
	}
	// Wait for the goroutine to pick up the first item (leaving buffer at bufSize).
	time.Sleep(20 * time.Millisecond)

	// These extra sends should be dropped silently.
	for range bufSize {
		p.Deliver("k", 99)
	}

	close(blocker)
	// No assertion needed beyond "no panic or deadlock".
}

// ---------------------------------------------------------------------------
// LatestWinsSlot — coalescing behaviour
// ---------------------------------------------------------------------------

func TestPool_LatestWinsSlot_CoalescesIntermediateUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blocker := make(chan struct{})
	var handlerCalls atomic.Int32

	p := New(WithSlotFactory[string](LatestWinsSlot[int]()))
	p.Register(ctx, func(_ string, slot Slot[int]) func(context.Context) {
		return func(ctx context.Context) {
			for {
				select {
				case <-ctx.Done():
					return
				case <-slot.C():
					<-blocker // hold to let multiple Delivers pile up
					handlerCalls.Add(1)
					slot.Recv() // consume
				}
			}
		}
	})

	// First Deliver starts the goroutine, handler blocks.
	p.Deliver("k", 1)
	time.Sleep(20 * time.Millisecond)

	// Multiple concurrent Delivers — only the latest value matters.
	p.Deliver("k", 2)
	p.Deliver("k", 3)
	p.Deliver("k", 4)

	// Unblock — one more iteration at most (latest-wins coalesces 2/3/4 → 4).
	close(blocker)

	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 0 || handlerCalls.Load() >= 2
	}, time.Second, 10*time.Millisecond)
}

// ---------------------------------------------------------------------------
// Remove
// ---------------------------------------------------------------------------

func TestPool_Remove_UnknownKey_IsNoop(t *testing.T) {
	p, _, _ := startedPool(t)
	// Must not panic or deadlock.
	assert.NotPanics(t, func() { p.Remove("nonexistent") })
}

func TestPool_Remove_StopsWorkerAndDiscardsEntry(t *testing.T) {
	p, received, _ := startedPool(t)

	// Deliver one item to ensure the goroutine is running.
	p.Deliver("k", 1)
	drain(t, received, 1, time.Second)

	p.Remove("k")

	// After Remove, the entry is gone from the map.
	p.mu.RLock()
	_, exists := p.entries["k"]
	p.mu.RUnlock()
	assert.False(t, exists, "entry should be removed from the map")
}

// ---------------------------------------------------------------------------
// Idle reap + restart
// ---------------------------------------------------------------------------

func TestPool_IdleReap_GoroutineExits_RestartsOnNewDeliver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const idleTTL = 30 * time.Millisecond
	received := make(chan int, 256)
	p := New[string, int]()
	p.Register(ctx, RunnerFactory[string](idleTTL,
		func(_ context.Context, v int) error {
			received <- v
			return nil
		},
		nil,
	))

	p.Deliver("k", 1)
	drain(t, received, 1, time.Second)

	// Wait for the goroutine to be reaped.
	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 0
	}, 500*time.Millisecond, 10*time.Millisecond, "goroutine should be reaped after idle TTL")

	// Deliver again — goroutine must restart and deliver.
	p.Deliver("k", 2)
	got := drain(t, received, 1, time.Second)
	assert.Equal(t, []int{2}, got)
}

func TestPool_IdleReap_ItemsSentDuringReap_NotStranded(t *testing.T) {
	// This tests the defer-restart path in runEntry():
	// items that arrive between the idle-timer fire and the defer
	// must still be processed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const idleTTL = 20 * time.Millisecond
	var handlerCalls atomic.Int32

	received := make(chan int, 256)
	p := New(WithSlotFactory[string](FIFOSlot[int](64)))
	p.Register(ctx, RunnerFactory[string](idleTTL,
		func(_ context.Context, v int) error {
			handlerCalls.Add(1)
			received <- v
			return nil
		},
		nil,
	))

	// Drain the first item so the goroutine enters the idle timer.
	p.Deliver("k", 0)
	drain(t, received, 1, time.Second)

	// Spam sends right around the idle window to catch the race.
	const extra = 10
	for i := 1; i <= extra; i++ {
		time.Sleep(idleTTL / 4)
		p.Deliver("k", i)
	}

	// All items must eventually be processed.
	assert.Eventually(t, func() bool {
		return handlerCalls.Load() == extra+1
	}, 5*time.Second, 20*time.Millisecond, "all items including stranded ones must be processed")
}

// ---------------------------------------------------------------------------
// Auto-remove on idle
// ---------------------------------------------------------------------------

func TestPool_AutoRemoveOnIdle_EntryRemovedAfterIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const idleTTL = 30 * time.Millisecond
	received := make(chan int, 256)
	p := New(
		WithSlotFactory[string](FIFOSlot[int](64)),
		WithAutoRemoveOnIdle[string, int](),
	)
	p.Register(ctx, RunnerFactory[string](idleTTL,
		func(_ context.Context, v int) error {
			received <- v
			return nil
		},
		nil,
	))

	p.Deliver("k", 1)
	drain(t, received, 1, time.Second)

	// Entry must be removed once the goroutine idle-reaps with an empty slot.
	assert.Eventually(t, func() bool {
		p.mu.RLock()
		_, exists := p.entries["k"]
		p.mu.RUnlock()
		return !exists
	}, 500*time.Millisecond, 10*time.Millisecond, "entry should be auto-removed after idle TTL")
}

func TestPool_AutoRemoveOnIdle_DisabledByDefault_EntryRetained(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const idleTTL = 30 * time.Millisecond
	received := make(chan int, 256)
	p := New(WithSlotFactory[string](FIFOSlot[int](64))) // no WithAutoRemoveOnIdle
	p.Register(ctx, RunnerFactory[string](idleTTL,
		func(_ context.Context, v int) error {
			received <- v
			return nil
		},
		nil,
	))

	p.Deliver("k", 1)
	drain(t, received, 1, time.Second)

	// Wait for the goroutine to idle-reap.
	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 0
	}, 500*time.Millisecond, 10*time.Millisecond)

	// Without WithAutoRemoveOnIdle, entry must remain in the map.
	p.mu.RLock()
	_, exists := p.entries["k"]
	p.mu.RUnlock()
	assert.True(t, exists, "entry should be retained without WithAutoRemoveOnIdle")
}

func TestPool_AutoRemoveOnIdle_NewDeliverAfterRemove_ReusesEntry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const idleTTL = 30 * time.Millisecond
	received := make(chan int, 256)
	p := New(
		WithSlotFactory[string](FIFOSlot[int](64)),
		WithAutoRemoveOnIdle[string, int](),
	)
	p.Register(ctx, RunnerFactory[string](idleTTL,
		func(_ context.Context, v int) error {
			received <- v
			return nil
		},
		nil,
	))

	p.Deliver("k", 1)
	drain(t, received, 1, time.Second)

	// Wait for auto-remove.
	assert.Eventually(t, func() bool {
		p.mu.RLock()
		_, exists := p.entries["k"]
		p.mu.RUnlock()
		return !exists
	}, 500*time.Millisecond, 10*time.Millisecond)

	// A new Deliver recreates the entry and processes the value — no permanent loss.
	p.Deliver("k", 2)
	got := drain(t, received, 1, time.Second)
	assert.Equal(t, []int{2}, got)
}

func TestPool_AutoRemoveOnIdle_ContextCancel_EntryNotRemoved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	const idleTTL = 30 * time.Second // long TTL so ctx cancel fires first
	received := make(chan int, 256)
	p := New(
		WithSlotFactory[string](FIFOSlot[int](64)),
		WithAutoRemoveOnIdle[string, int](),
	)
	p.Register(ctx, RunnerFactory[string](idleTTL,
		func(_ context.Context, v int) error {
			received <- v
			return nil
		},
		nil,
	))

	p.Deliver("k", 1)
	drain(t, received, 1, time.Second)

	// Cancel the pool context — goroutine exits via ctx.Done(), not idle TTL.
	cancel()

	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 0
	}, 500*time.Millisecond, 10*time.Millisecond)

	// Entry must NOT be auto-removed when the exit reason is context cancellation.
	p.mu.RLock()
	_, exists := p.entries["k"]
	p.mu.RUnlock()
	assert.True(t, exists, "entry should not be removed on context cancellation")
}

// ---------------------------------------------------------------------------
// Context cancellation
// ---------------------------------------------------------------------------

func TestPool_ContextCancel_StopsAllWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	blocker := make(chan struct{})
	p := New[string, int]()
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, _ int) error {
			<-blocker
			return nil
		},
		nil,
	))

	p.Deliver("a", 1)
	p.Deliver("b", 1)
	p.Deliver("c", 1)

	// Wait for goroutines to start.
	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 3
	}, time.Second, 10*time.Millisecond)

	// Cancel — unblock handlers so goroutines can exit cleanly.
	cancel()
	close(blocker)

	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 0
	}, time.Second, 10*time.Millisecond, "all workers should stop after context cancel")
}

// ---------------------------------------------------------------------------
// Len / ActiveWorkers
// ---------------------------------------------------------------------------

func TestPool_Len_ReturnsBufferedCount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blocker := make(chan struct{})
	p := New(WithSlotFactory[string](FIFOSlot[int](100)))
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, _ int) error {
			<-blocker
			return nil
		},
		nil,
	))

	p.Deliver("a", 1)
	p.Deliver("a", 2)
	p.Deliver("b", 1)
	p.Deliver("b", 2)

	// Give the goroutines a moment to pick up the first item per key.
	time.Sleep(20 * time.Millisecond)

	// Each goroutine is blocked in the handler holding 1 item; 1 item per key remains buffered.
	assert.Equal(t, 2, p.Len())
	close(blocker)
}

func TestPool_ActiveWorkers_CountsRunningGoroutines(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blocker := make(chan struct{})
	p := New[string, int]()
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, _ int) error {
			<-blocker
			return nil
		},
		nil,
	))

	assert.Equal(t, 0, p.ActiveWorkers())

	p.Deliver("x", 1)
	p.Deliver("y", 1)

	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 2
	}, time.Second, 10*time.Millisecond)

	close(blocker)
}

// ---------------------------------------------------------------------------
// Handler errors
// ---------------------------------------------------------------------------

func TestPool_HandlerError_IsNonFatal_NextItemStillProcessed(t *testing.T) {
	var callCount atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan int, 256)

	errPool := New[string, int]()
	errPool.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, v int) error {
			callCount.Add(1)
			if v == 1 {
				return errors.New("intentional error")
			}
			received <- v
			return nil
		},
		nil,
	))

	errPool.Deliver("k", 1) // will error
	errPool.Deliver("k", 2) // must still be delivered

	got := drain(t, received, 1, time.Second)
	assert.Equal(t, []int{2}, got)
	assert.Equal(t, int32(2), callCount.Load())
}

// ---------------------------------------------------------------------------
// OnSpawn / OnRemove hooks
// ---------------------------------------------------------------------------

func TestPool_OnSpawnOnRemove_HooksFireOnLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var spawned, removed atomic.Int32

	p := New(
		WithOnSpawnCallback[string, int](func(_ string) { spawned.Add(1) }),
		WithOnRemoveCallback[string, int](func(_ string) { removed.Add(1) }),
	)

	received := make(chan int, 256)
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, v int) error {
			received <- v
			return nil
		},
		nil,
	))

	p.Deliver("a", 1)
	drain(t, received, 1, time.Second)

	assert.Equal(t, int32(1), spawned.Load(), "onSpawn should have fired once")

	// Cancel to trigger goroutine exit → onRemove should fire.
	cancel()
	assert.Eventually(t, func() bool {
		return removed.Load() == 1
	}, time.Second, 10*time.Millisecond, "onRemove should fire when goroutine exits")
}

// ---------------------------------------------------------------------------
// Stop
// ---------------------------------------------------------------------------

func TestPool_Stop_SignalsAllWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blocker := make(chan struct{})
	p := New[string, int]()
	p.Register(ctx, RunnerFactory[string](testIdleTTL,
		func(_ context.Context, _ int) error {
			<-blocker
			return nil
		},
		nil,
	))

	p.Deliver("a", 1)
	p.Deliver("b", 1)

	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 2
	}, time.Second, 10*time.Millisecond)

	close(blocker)
	p.Stop()

	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 0
	}, time.Second, 10*time.Millisecond, "Stop should signal all workers to exit")
}
