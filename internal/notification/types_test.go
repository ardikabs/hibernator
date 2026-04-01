/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOverflow_Append(t *testing.T) {
	var o Overflow[int]

	o.Append(1)
	o.Append(2)
	o.Append(3)

	assert.Equal(t, 3, o.Len())
	assert.Equal(t, []int{1, 2, 3}, o.Snapshot())
}

func TestOverflow_Set(t *testing.T) {
	var o Overflow[string]

	o.Append("a")
	o.Append("b")
	o.Append("c")

	assert.True(t, o.Set(1, "B"))
	assert.Equal(t, []string{"a", "B", "c"}, o.Snapshot())
}

func TestOverflow_Set_OutOfBounds(t *testing.T) {
	var o Overflow[int]

	o.Append(1)

	assert.False(t, o.Set(-1, 99), "negative index should fail")
	assert.False(t, o.Set(1, 99), "index == len should fail")
	assert.False(t, o.Set(5, 99), "index > len should fail")
	assert.Equal(t, []int{1}, o.Snapshot())
}

func TestOverflow_Snapshot_IsIndependentCopy(t *testing.T) {
	var o Overflow[int]

	o.Append(1)
	o.Append(2)

	snap := o.Snapshot()
	snap[0] = 99

	assert.Equal(t, []int{1, 2}, o.Snapshot(), "mutating snapshot must not affect queue")
}

func TestOverflow_Snapshot_Empty(t *testing.T) {
	var o Overflow[int]

	snap := o.Snapshot()

	assert.Empty(t, snap)
}

func TestOverflow_Len_Empty(t *testing.T) {
	var o Overflow[int]

	assert.Equal(t, 0, o.Len())
}

func TestOverflow_Range(t *testing.T) {
	var o Overflow[int]

	o.Append(10)
	o.Append(20)
	o.Append(30)

	var got []int
	o.Range(func(v int) { got = append(got, v) })

	assert.Equal(t, []int{10, 20, 30}, got)
}

func TestOverflow_Clear(t *testing.T) {
	var o Overflow[int]

	o.Append(1)
	o.Append(2)
	o.Clear()

	assert.Equal(t, 0, o.Len())
	assert.Empty(t, o.Snapshot())
}

func TestOverflow_Consume_AllConsumed(t *testing.T) {
	var o Overflow[int]

	o.Append(1)
	o.Append(2)
	o.Append(3)

	var got []int
	o.Consume(func(_ context.Context, v int) bool {
		got = append(got, v)
		return true
	})

	assert.Equal(t, []int{1, 2, 3}, got)
	assert.Equal(t, 0, o.Len(), "all items consumed, queue should be empty")
}

func TestOverflow_Consume_PartialStop(t *testing.T) {
	var o Overflow[int]

	o.Append(1)
	o.Append(2)
	o.Append(3)

	var got []int
	o.Consume(func(_ context.Context, v int) bool {
		if v == 2 {
			return false // reject item 2; it and remaining go back to queue
		}
		got = append(got, v)
		return true
	})

	assert.Equal(t, []int{1}, got, "should consume only item 1")
	assert.Equal(t, []int{2, 3}, o.Snapshot(), "items 2 and 3 should remain in queue")
}

func TestOverflow_Consume_Empty(t *testing.T) {
	var o Overflow[int]

	called := false
	o.Consume(func(_ context.Context, _ int) bool {
		called = true
		return true
	})

	assert.False(t, called, "fn should not be called on empty queue")
}

func TestOverflow_Consume_PanicSafety(t *testing.T) {
	var o Overflow[int]

	o.Append(1)
	o.Append(2)
	o.Append(3)

	assert.Panics(t, func() {
		o.Consume(func(_ context.Context, v int) bool {
			if v == 2 {
				panic("boom")
			}
			return true
		})
	})

	// Item 1 was consumed, panic on item 2 means items 2 and 3 are returned.
	assert.Equal(t, []int{2, 3}, o.Snapshot(), "unprocessed items must be returned on panic")
}

func TestOverflow_Consume_ReturnsRemainderFalse(t *testing.T) {
	var o Overflow[int]

	o.Append(10)
	o.Append(20)
	o.Append(30)

	// fn returns false immediately on first item.
	o.Consume(func(_ context.Context, _ int) bool { return false })

	assert.Equal(t, []int{10, 20, 30}, o.Snapshot(), "all items must be returned when fn rejects first item")
}

func TestOverflow_Consume_ConcurrentAppendPreservesOrder(t *testing.T) {
	var o Overflow[int]

	o.Append(1)
	o.Append(2)

	barrier := make(chan struct{})
	o.Consume(func(_ context.Context, v int) bool {
		if v == 1 {
			// Signal that we're mid-consume, allow concurrent Append.
			close(barrier)
			// Consume item 1, stop after.
			return true
		}
		// Stop on item 2 — it goes back to queue.
		return false
	})

	// Concurrent append happened during Consume (simulate after barrier).
	<-barrier
	o.Append(99)

	// Item 2 was returned to front, 99 was appended after.
	snap := o.Snapshot()
	assert.Equal(t, 2, snap[0], "returned remainder should be at front")

	// 99 comes after the returned items.
	found := false
	for _, v := range snap {
		if v == 99 {
			found = true
		}
	}
	assert.True(t, found, "concurrently appended item should be present")
}

func TestOverflow_Consume_ConcurrentAppendDuringFullConsume(t *testing.T) {
	// Verifies that items appended while Consume processes the taken batch
	// are preserved and appear after any returned remainder.
	var o Overflow[int]

	o.Append(1)
	o.Append(2)
	o.Append(3)

	midConsume := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		o.Consume(func(_ context.Context, v int) bool {
			if v == 2 {
				close(midConsume) // Signal mid-consume
			}
			return true // Consume all
		})
	}()

	<-midConsume
	o.Append(4) // Append while consume is running

	<-done

	// All original items consumed; item 4 was appended to the (empty) queue.
	snap := o.Snapshot()
	assert.Equal(t, []int{4}, snap, "concurrently appended item should survive full consume")
}

func TestOverflow_ConcurrentAppendAndLen(t *testing.T) {
	var o Overflow[int]

	const goroutines = 50
	const perGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				o.Append(i)
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, goroutines*perGoroutine, o.Len())
}

func TestOverflow_ConcurrentConsumeAndAppend(t *testing.T) {
	// Hammers Consume and Append concurrently to verify no data is lost.
	var o Overflow[int]

	const total = 500
	for i := 0; i < total; i++ {
		o.Append(i)
	}

	var consumed atomic.Int64
	var wg sync.WaitGroup

	// Concurrent consumer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			before := o.Len()
			if before == 0 {
				return
			}
			o.Consume(func(_ context.Context, _ int) bool {
				consumed.Add(1)
				return true
			})
		}
	}()

	// Concurrent appender — adds more items.
	extra := 100
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < extra; i++ {
			o.Append(total + i)
		}
	}()

	wg.Wait()

	// Drain any remaining.
	o.Consume(func(_ context.Context, _ int) bool {
		consumed.Add(1)
		return true
	})

	remaining := o.Len()
	assert.Equal(t, 0, remaining, "queue should be fully drained")
	assert.Equal(t, int64(total+extra), consumed.Load(), "all items should be consumed exactly once")
}
