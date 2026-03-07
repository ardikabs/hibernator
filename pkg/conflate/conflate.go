/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package conflate

import (
	"sync"
)

// Pipeline is a latest-wins handoff mechanism for a single value of type T.
// Each Send replaces the previously stored value. Notifications are
// edge-triggered and non-blocking, and intermediate updates may be coalesced.
type Pipeline[T any] struct {
	mu    sync.Mutex
	val   T
	ready chan struct{} // capacity 1 — non-blocking signal
}

func New[T any]() *Pipeline[T] {
	return &Pipeline[T]{ready: make(chan struct{}, 1)}
}

// Send replaces the currently stored value and attempts to notify the receiver.
// Notification is non-blocking: if a signal is already pending, it is not sent again.
// The receiver will observe the most recent value on its next wake-up.
func (s *Pipeline[T]) Send(v T) {
	s.mu.Lock()
	s.val = v
	s.mu.Unlock()

	select {
	case s.ready <- struct{}{}:
	default:
		// Drop: channel full.
		// Receiver will read the new value on its next wake-up regardless.
	}
}

// Recv returns the most recently stored value.
// It should be called only after receiving from the ready channel
// to ensure the value is observed in response to a notification.
func (s *Pipeline[T]) Recv() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.val
}

// C exposes the notification channel used to signal that a new value
// has been stored. The channel is edge-triggered and may coalesce
// multiple Send calls into a single notification.
//
// The receiver must read from this channel before calling Recv()
// to synchronize with a Send operation.
func (s *Pipeline[T]) C() <-chan struct{} {
	return s.ready
}
