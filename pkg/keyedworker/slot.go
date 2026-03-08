/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package keyedworker

import (
	"github.com/ardikabs/hibernator/pkg/conflate"
)

// Slot is the per-key delivery mechanism used by Pool. The Pool never interprets
// the semantics of the slot — it only calls Send to enqueue a value from Deliver,
// inspects Len to decide whether to restart a goroutine after exit, and calls Drain
// on Remove to discard pending items.
//
// Two built-in implementations are provided:
//   - FIFOSlot: backed by a buffered channel; every update is preserved in order.
//   - LatestWinsSlot: backed by a conflate.Pipeline; concurrent sends coalesce into
//     a single latest-value notification.
//
// Custom implementations can be supplied via WithSlotFactory for specialised
// back-pressure or priority semantics.
type Slot[V any] interface {
	// Send enqueues value. Must never block.
	Send(v V)
	// C returns the edge-triggered notification channel. Fires when at least
	// one value is available for Recv. May be called from any goroutine.
	C() <-chan struct{}
	// Recv returns the next value to process. Must be called only after C fires.
	Recv() V
	// Len returns the number of pending values (used by Pool to detect items
	// that arrived while the goroutine was exiting and need a restart).
	Len() int
	// Drain discards all pending values. Called by Pool.Remove before the
	// entry is deleted.
	Drain()
}

// ---------------------------------------------------------------------------
// FIFOSlot — backed by a buffered channel
// ---------------------------------------------------------------------------

// FIFOSlot returns a SlotFactory that creates FIFO-ordered slots backed by a
// buffered channel of size bufSize. When the buffer is full, Send drops the value
// (non-blocking). Every value sent is preserved and delivered in order.
//
// Use FIFOSlot for consumers where every update is meaningful and must not be
// discarded (e.g. status writers that serialise K8s write calls).
func FIFOSlot[V any](bufSize int) func() Slot[V] {
	return func() Slot[V] {
		s := &fifoSlot[V]{
			queue:  make(chan V, bufSize),
			signal: make(chan struct{}, 1),
		}
		return s
	}
}

type fifoSlot[V any] struct {
	queue  chan V
	signal chan struct{}
}

func (s *fifoSlot[V]) Send(v V) {
	select {
	case s.queue <- v:
	default:
		// Buffer full — drop. Pool logs this at the Deliver call site.
		return
	}
	// Non-blocking signal: if one is already pending the receiver will wake
	// and drain multiple items from the queue.
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *fifoSlot[V]) C() <-chan struct{} { return s.signal }

func (s *fifoSlot[V]) Recv() V {
	v := <-s.queue
	// Re-arm the signal if the queue still has items so the goroutine
	// loops back immediately without waiting for the next Send.
	if len(s.queue) > 0 {
		select {
		case s.signal <- struct{}{}:
		default:
		}
	}
	return v
}

func (s *fifoSlot[V]) Len() int { return len(s.queue) }

func (s *fifoSlot[V]) Drain() {
	for {
		select {
		case <-s.queue:
		default:
			// Drain the signal channel too.
			select {
			case <-s.signal:
			default:
			}
			return
		}
	}
}

// ---------------------------------------------------------------------------
// LatestWinsSlot — backed by conflate.Pipeline
// ---------------------------------------------------------------------------

// LatestWinsSlot returns a SlotFactory that creates latest-wins slots backed by
// a conflate.Pipeline. Concurrent sends coalesce into a single notification; the
// goroutine always reads the most recent value regardless of how many sends occurred.
//
// Use LatestWinsSlot for consumers where only the freshest snapshot matters and
// intermediate updates can be discarded (e.g. plan workers processing PlanContext
// where only the current spec/status is relevant).
func LatestWinsSlot[V any]() func() Slot[V] {
	return func() Slot[V] {
		return &latestWinsSlot[V]{p: conflate.New[V]()}
	}
}

type latestWinsSlot[V any] struct {
	p *conflate.Pipeline[V]
}

func (s *latestWinsSlot[V]) Send(v V)          { s.p.Send(v) }
func (s *latestWinsSlot[V]) C() <-chan struct{} { return s.p.C() }
func (s *latestWinsSlot[V]) Recv() V           { return s.p.Recv() }

// Len returns 1 if a value is pending, 0 otherwise. conflate.Pipeline is a
// single-value store: the signal channel has capacity 1.
func (s *latestWinsSlot[V]) Len() int { return len(s.p.C()) }

func (s *latestWinsSlot[V]) Drain() {
	// Drain the signal channel. The stored value will be overwritten on the
	// next Send anyway, so there is nothing else to discard.
	select {
	case <-s.p.C():
	default:
	}
}
