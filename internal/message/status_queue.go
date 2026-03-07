/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package message

import "github.com/ardikabs/hibernator/internal/metrics"

// statusQueueCapacity is the buffer size for status update channels.
// A capacity of 1000 provides enough headroom for burst traffic from many concurrent
// supervisors while keeping memory overhead bounded.
const statusQueueCapacity = 1000

// StatusQueue is a bounded, non-blocking channel wrapper for fan-in status delivery.
// Multiple producers (e.g. PlanSupervisor goroutines) call Send(); a pool of consumer
// goroutines reads from C().
//
// Send never blocks — if the channel is full the update is dropped.  Because the status
// writer always reads a fresh object from the API server and uses an equality guard, a
// dropped update is safe: the next delivery will carry the same (or newer) intent.
type StatusQueue[T any] struct {
	ch   chan T
	name string
}

func newStatusQueue[T any](name string) *StatusQueue[T] {
	return &StatusQueue[T]{ch: make(chan T, statusQueueCapacity), name: name}
}

// Send enqueues an update for a consumer goroutine to process.
// If the channel is full the update is silently dropped and a metric counter is incremented.
func (q *StatusQueue[T]) Send(update T) {
	select {
	case q.ch <- update:
	default:
		// Drop: channel full. The owning supervisor will re-send on the next poll.
		metrics.StatusQueueDroppedTotal.WithLabelValues(q.name).Inc()
	}
}

// C returns a receive-only channel that consumer goroutines should read from.
func (q *StatusQueue[T]) C() <-chan T {
	return q.ch
}

// Len returns the number of items currently buffered in the queue.
func (q *StatusQueue[T]) Len() int {
	return len(q.ch)
}
