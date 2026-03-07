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

// helpers --------------------------------------------------------------------

func startedPool(t *testing.T, opts ...Option[string, int]) (*Pool[string, int], chan int, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan int, 256)
	p := New[string, int](opts...)
	p.Start(ctx, func(_ context.Context, v int) error {
		received <- v
		return nil
	})
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

func TestPool_Send_InvokesHandler(t *testing.T) {
	p, received, _ := startedPool(t)

	p.Send("k", 42)

	got := drain(t, received, 1, time.Second)
	assert.Equal(t, []int{42}, got)
}

func TestPool_Send_MultipleValues_DeliveredAll(t *testing.T) {
	p, received, _ := startedPool(t)

	for i := range 5 {
		p.Send("k", i)
	}

	got := drain(t, received, 5, time.Second)
	assert.Len(t, got, 5)
}

// ---------------------------------------------------------------------------
// FIFO ordering within a key
// ---------------------------------------------------------------------------

func TestPool_Send_PerKeyFIFO_OrderPreserved(t *testing.T) {
	// Use a slow handler so all items queue up before the first one is processed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan int, 256)
	p := New(WithBufSize[string, int](64))
	p.Start(ctx, func(_ context.Context, v int) error {
		time.Sleep(2 * time.Millisecond) // slow handler
		received <- v
		return nil
	})

	const n = 20
	for i := range n {
		p.Send("k", i)
	}

	got := drain(t, received, n, 5*time.Second)
	for i := range n {
		assert.Equal(t, i, got[i], "item at position %d out of order", i)
	}
}

// ---------------------------------------------------------------------------
// Pre-Start Send buffering
// ---------------------------------------------------------------------------

func TestPool_Send_BeforeStart_BufferedAndDrained(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := New[string, int]()

	// Send before Start — handler is nil, goroutine not spawned.
	p.Send("k", 1)
	p.Send("k", 2)
	p.Send("k", 3)

	received := make(chan int, 256)
	p.Start(ctx, func(_ context.Context, v int) error {
		received <- v
		return nil
	})

	got := drain(t, received, 3, time.Second)
	assert.Equal(t, []int{1, 2, 3}, got)
}

// ---------------------------------------------------------------------------
// Parallel cross-key processing
// ---------------------------------------------------------------------------

func TestPool_Send_DifferentKeys_ProcessedInParallel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	concurrent := 0
	maxConcurrent := 0

	done := make(chan struct{}, 2)
	p := New[string, int]()
	p.Start(ctx, func(_ context.Context, _ int) error {
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
	})

	// Two different keys — should run simultaneously.
	p.Send("a", 1)
	p.Send("b", 1)

	// Wait for both to finish.
	<-done
	<-done

	assert.Equal(t, 2, maxConcurrent, "expected 2 keys to be processed concurrently")
}

// ---------------------------------------------------------------------------
// Buffer-full drop
// ---------------------------------------------------------------------------

func TestPool_Send_BufferFull_DropsUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const bufSize = 3
	blocker := make(chan struct{}) // keep handler blocked so nothing is consumed

	p := New(WithBufSize[string, int](bufSize))
	p.Start(ctx, func(_ context.Context, _ int) error {
		<-blocker // block until we unblock
		return nil
	})

	// Fill buffer + trigger handler (1 consumed by goroutine, bufSize remain).
	for i := range bufSize + 1 {
		p.Send("k", i)
	}
	// Wait for the goroutine to pick up the first item (leaving buffer at bufSize).
	time.Sleep(20 * time.Millisecond)

	// These extra sends should be dropped silently.
	for range bufSize {
		p.Send("k", 99)
	}

	close(blocker)
	// No assertion needed beyond "no panic or deadlock".
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
	p.Send("k", 1)
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

func TestPool_IdleReap_GoroutineExits_RestartsOnNewSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan int, 256)
	p := New(
		WithIdleTTL[string, int](30 * time.Millisecond),
	)
	p.Start(ctx, func(_ context.Context, v int) error {
		received <- v
		return nil
	})

	p.Send("k", 1)
	drain(t, received, 1, time.Second)

	// Wait for the goroutine to be reaped.
	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 0
	}, 500*time.Millisecond, 10*time.Millisecond, "goroutine should be reaped after idle TTL")

	// Send again — goroutine must restart and deliver.
	p.Send("k", 2)
	got := drain(t, received, 1, time.Second)
	assert.Equal(t, []int{2}, got)
}

func TestPool_IdleReap_ItemsSentDuringReap_NotStranded(t *testing.T) {
	// This tests the defer-restart path in run():
	// items that arrive between the idle-timer fire and the defer
	// must still be processed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const idleTTL = 20 * time.Millisecond
	var handlerCalls atomic.Int32

	received := make(chan int, 256)
	p := New(
		WithIdleTTL[string, int](idleTTL),
		WithBufSize[string, int](64),
	)
	p.Start(ctx, func(_ context.Context, v int) error {
		handlerCalls.Add(1)
		received <- v
		return nil
	})

	// Drain the first item so the goroutine enters the idle timer.
	p.Send("k", 0)
	drain(t, received, 1, time.Second)

	// Spam sends right around the idle window to catch the race.
	const extra = 10
	for i := 1; i <= extra; i++ {
		time.Sleep(idleTTL / 4)
		p.Send("k", i)
	}

	// All items must eventually be processed.
	assert.Eventually(t, func() bool {
		return handlerCalls.Load() == extra+1
	}, 5*time.Second, 20*time.Millisecond, "all items including stranded ones must be processed")
}

// ---------------------------------------------------------------------------
// Context cancellation
// ---------------------------------------------------------------------------

func TestPool_ContextCancel_StopsAllWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	blocker := make(chan struct{})
	p := New[string, int]()
	p.Start(ctx, func(_ context.Context, _ int) error {
		<-blocker
		return nil
	})

	p.Send("a", 1)
	p.Send("b", 1)
	p.Send("c", 1)

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
	p := New[string, int]()
	p.Start(ctx, func(_ context.Context, _ int) error {
		<-blocker
		return nil
	})

	p.Send("a", 1)
	p.Send("a", 2)
	p.Send("b", 1)
	p.Send("b", 2)

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
	p.Start(ctx, func(_ context.Context, _ int) error {
		<-blocker
		return nil
	})

	assert.Equal(t, 0, p.ActiveWorkers())

	p.Send("x", 1)
	p.Send("y", 1)

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
	errPool.Start(ctx, func(_ context.Context, v int) error {
		callCount.Add(1)
		if v == 1 {
			return errors.New("intentional error")
		}
		received <- v
		return nil
	})

	errPool.Send("k", 1) // will error
	errPool.Send("k", 2) // must still be delivered

	got := drain(t, received, 1, time.Second)
	assert.Equal(t, []int{2}, got)
	assert.Equal(t, int32(2), callCount.Load())
}

// ---------------------------------------------------------------------------
// Stop
// ---------------------------------------------------------------------------

func TestPool_Stop_SignalsAllWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blocker := make(chan struct{})
	p := New[string, int]()
	p.Start(ctx, func(_ context.Context, _ int) error {
		<-blocker
		return nil
	})

	p.Send("a", 1)
	p.Send("b", 1)

	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 2
	}, time.Second, 10*time.Millisecond)

	close(blocker)
	p.Stop()

	assert.Eventually(t, func() bool {
		return p.ActiveWorkers() == 0
	}, time.Second, 10*time.Millisecond, "Stop should signal all workers to exit")
}
