/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"sync"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification/sink"
	"k8s.io/apimachinery/pkg/types"
)

// DeliveryResult reports the outcome of a single notification dispatch.
type DeliveryResult struct {
	// NotificationRef identifies the HibernateNotification.
	NotificationRef types.NamespacedName

	// SinkName is the sink that was dispatched to.
	SinkName string

	// Timestamp is when the dispatch completed.
	Timestamp time.Time

	// Success is true when the notification was delivered without error.
	Success bool

	// Error is the dispatch error, if any. Nil when Success is true.
	Error error
}

// DeliveryCallback is invoked after each dispatch attempt to report delivery
// results back to the lifecycle processor for status tracking.
type DeliveryCallback func(result DeliveryResult)

// Request represents a single notification request submitted
// by a hook closure. It contains all data needed by the dispatcher to resolve
// credentials, render the message, and send it to the sink.
type Request struct {
	// Payload carries the notification event data.
	Payload Payload

	// SinkName is the human-readable sink identifier (for logging).
	SinkName string

	// SinkType is the sink provider type (e.g., "slack", "telegram", "webhook").
	SinkType string

	// SecretRef references the Secret containing sink config.
	// If Key is empty, the dispatcher uses a default key.
	SecretRef hibernatorv1alpha1.ObjectKeyReference

	// TemplateRef optionally references a ConfigMap key for custom templates.
	// Nil means use the default template.
	TemplateRef *hibernatorv1alpha1.ObjectKeyReference

	// NotificationRef identifies the HibernateNotification that owns this request.
	// Used by the delivery callback to update per-sink status.
	NotificationRef types.NamespacedName
}

// Payload carries the notification event data passed to sinks for dispatch.
type Payload = sink.Payload

// TargetInfo holds execution state for a single target.
type TargetInfo = sink.TargetInfo

// PlanInfo carries plan metadata for the template context.
type PlanInfo = sink.PlanInfo

// ConnectorInfo carries resolved connector metadata for template rendering.
type ConnectorInfo = sink.ConnectorInfo

// Overflow is a concurrency-safe, unbounded spillover queue.
//
// It is designed as a companion to a bounded channel: when the channel is full,
// callers Append items here instead of blocking. A background drainer moves
// items from the Overflow back into the channel as capacity becomes available.
//
// All methods are safe for concurrent use. The Consume method uses a
// take-and-return pattern to process items outside the lock without
// positional-mismatch races.
type Overflow[T any] struct {
	mu    sync.Mutex
	items []T
}

// Append adds an item to the back of the overflow queue.
func (o *Overflow[T]) Append(item T) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.items = append(o.items, item)
}

// Set replaces the item at index i. Returns false if i is out of bounds.
func (o *Overflow[T]) Set(i int, v T) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	if i < 0 || i >= len(o.items) {
		return false
	}
	o.items[i] = v
	return true
}

// Snapshot returns a shallow copy of the current items.
// The caller owns the returned slice; mutations do not affect the queue.
func (o *Overflow[T]) Snapshot() []T {
	o.mu.Lock()
	defer o.mu.Unlock()

	cp := make([]T, len(o.items))
	copy(cp, o.items)
	return cp
}

// Len returns the number of items currently in the queue.
func (o *Overflow[T]) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return len(o.items)
}

// Range calls fn for each item while holding the lock.
// Use for short, non-blocking callbacks only; for potentially blocking
// work prefer Consume.
func (o *Overflow[T]) Range(fn func(T)) {
	o.mu.Lock()
	defer o.mu.Unlock()

	for _, item := range o.items {
		fn(item)
	}
}

// Consume atomically takes ownership of items, processes them outside the lock,
// and prepends any unconsumed remainder back under the lock.
//
// fn is called for each item in order. If fn returns true the item is consumed
// and discarded. If fn returns false processing stops; that item and all remaining
// items are returned to the front of the queue (preserving order for the next call).
//
// Because items are extracted before fn runs, concurrent Append calls do not
// interfere with the consumed prefix — eliminating the positional-mismatch race
// that would occur with a snapshot-then-trim approach.
func (o *Overflow[T]) Consume(fn func(ctx context.Context, item T) bool) {
	// Step 1: atomically take all current items.
	o.mu.Lock()
	taken := o.items
	o.items = nil
	o.mu.Unlock()

	if len(taken) == 0 {
		return
	}

	// Hard limit: if processing all items takes longer than 5 seconds,
	// stop and return the remainder to the queue.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Step 2: process outside lock — fn may block (e.g., channel send).
	// Panic safety: if fn panics mid-iteration, the deferred closure returns
	// every unprocessed item (from consumed onward) back to the queue so no
	// data is silently lost.
	consumed := 0
	defer func() {
		remainder := taken[consumed:]
		if len(remainder) > 0 {
			o.mu.Lock()
			o.items = append(remainder, o.items...)
			o.mu.Unlock()
		}
	}()

	for _, item := range taken {
		if ctx.Err() != nil {
			break
		}

		if !fn(ctx, item) {
			break
		}
		consumed++
	}
}

// Clear removes all items from the queue.
func (o *Overflow[T]) Clear() {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.items = nil
}
