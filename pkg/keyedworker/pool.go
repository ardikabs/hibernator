/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package keyedworker provides a per-key worker pool with lazy goroutine creation,
// FIFO delivery within each key, and idle-based goroutine reaping.
//
// Design goals:
//   - Send is always non-blocking: updates are buffered in a per-key channel
//     regardless of whether the worker goroutine is currently alive.
//   - Per-key FIFO: each key has exactly one goroutine processing its queue
//     at any time, eliminating reordering races inherent in a shared flat pool.
//   - Idle reaping: goroutines are created lazily on the first Send and exit
//     after idleTTL of inactivity. The per-key channel persists beyond the
//     goroutine lifetime, so updates sent while the goroutine is idle are
//     safely buffered and processed when it restarts.
//   - Concurrency across keys: different keys are processed fully in parallel.
//   - Clean removal: Remove cancels the per-key goroutine and discards the
//     entry, freeing memory when a resource is deleted.
package keyedworker

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

const defaultBufferSize = 100
const defaultIdleTTL = 30 * time.Minute

// Pool manages a set of per-key worker goroutines.
// The zero value is not usable; use New to create instances.
type Pool[K comparable, V any] struct {
	mu      sync.RWMutex
	entries map[K]*entry[V]

	handler func(ctx context.Context, value V) error

	ctxMu        sync.RWMutex
	parentCtx    context.Context //nolint:containedctx
	parentCancel context.CancelFunc

	bufSize int
	idleTTL time.Duration
	log     logr.Logger
}

// entry is the per-key state. Its queue channel outlives the worker goroutine.
type entry[V any] struct {
	queue chan V

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

// Option configures a Pool.
type Option[K comparable, V any] func(*Pool[K, V])

// WithBufSize sets the per-key channel buffer capacity (default: 100).
func WithBufSize[K comparable, V any](n int) Option[K, V] {
	return func(p *Pool[K, V]) { p.bufSize = n }
}

// WithIdleTTL sets how long a per-key goroutine stays alive with no work (default: 30m).
func WithIdleTTL[K comparable, V any](d time.Duration) Option[K, V] {
	return func(p *Pool[K, V]) { p.idleTTL = d }
}

// WithLogger attaches a logger for internal diagnostics.
func WithLogger[K comparable, V any](log logr.Logger) Option[K, V] {
	return func(p *Pool[K, V]) { p.log = log }
}

// New creates a new Pool. The pool is not active until Start is called.
func New[K comparable, V any](opts ...Option[K, V]) *Pool[K, V] {
	p := &Pool[K, V]{
		entries: make(map[K]*entry[V]),
		bufSize: defaultBufferSize,
		idleTTL: defaultIdleTTL,
		log:     logr.Discard(),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Start registers the handler and parent context, then activates workers for any
// keys that already have buffered items (from pre-Start Send calls).
// Must be called exactly once before Send can dispatch work.
func (p *Pool[K, V]) Start(ctx context.Context, handler func(ctx context.Context, value V) error) {
	p.ctxMu.Lock()
	p.parentCtx, p.parentCancel = context.WithCancel(ctx)
	p.ctxMu.Unlock()

	p.handler = handler

	// Activate workers for any items that arrived before Start was called.
	p.mu.RLock()
	for key, e := range p.entries {
		if len(e.queue) > 0 {
			p.ensureRunning(key, e)
		}
	}
	p.mu.RUnlock()
}

// Send routes value to the per-key buffer and ensures the worker goroutine is running.
// Never blocks: if the per-key buffer is full the update is logged and dropped.
// Safe to call before Start; buffered items are drained once Start registers the handler.
func (p *Pool[K, V]) Send(key K, value V) {
	e := p.getOrCreate(key)

	select {
	case e.queue <- value:
		p.log.V(1).Info("enqueued item for key", "key", key)

	default:
		p.log.Info("per-key buffer full, dropping update", "key", key)
		return
	}

	if p.handler != nil {
		p.ensureRunning(key, e)
	}
}

// Remove cancels the per-key worker goroutine and removes the entry, discarding
// any unprocessed buffered items. Call this when the corresponding K8s resource
// is deleted to reclaim goroutine and channel memory.
func (p *Pool[K, V]) Remove(key K) {
	p.mu.Lock()
	e, ok := p.entries[key]
	delete(p.entries, key)
	p.mu.Unlock()

	if !ok {
		return
	}

	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
}

// Len returns the total number of unprocessed items buffered across all keys.
func (p *Pool[K, V]) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	total := 0
	for _, e := range p.entries {
		total += len(e.queue)
	}
	return total
}

// ActiveWorkers returns the number of currently running per-key goroutines.
func (p *Pool[K, V]) ActiveWorkers() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	count := 0
	for _, e := range p.entries {
		e.mu.Lock()
		if e.running {
			count++
		}
		e.mu.Unlock()
	}
	return count
}

// Stop cancels the parent context, signalling all active per-key goroutines to exit.
func (p *Pool[K, V]) Stop() {
	p.ctxMu.RLock()
	cancel := p.parentCancel
	p.ctxMu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (p *Pool[K, V]) getOrCreate(key K) *entry[V] {
	p.mu.RLock()
	e, ok := p.entries[key]
	p.mu.RUnlock()
	if ok {
		return e
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok = p.entries[key]; ok {
		return e
	}
	e = &entry[V]{
		queue: make(chan V, p.bufSize),
	}
	p.entries[key] = e
	return e
}

func (p *Pool[K, V]) ensureRunning(key K, e *entry[V]) {
	p.ctxMu.RLock()
	parentCtx := p.parentCtx
	p.ctxMu.RUnlock()

	if parentCtx == nil || parentCtx.Err() != nil {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return
	}

	ctx, cancel := context.WithCancel(parentCtx)
	e.cancel = cancel
	e.running = true
	go p.run(ctx, key, e)
}

// run is the per-key worker goroutine. It drains the entry's queue serially,
// resetting the idle timer on each item. It exits when ctx is cancelled or
// idleTTL elapses without any work.
//
// On exit, if unprocessed items remain (arrived between the idle-timer fire and
// the defer), a new goroutine is restarted immediately under the entry lock to
// ensure no item is stranded.
func (p *Pool[K, V]) run(ctx context.Context, key K, e *entry[V]) {
	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		e.cancel()
		e.running = false

		// Restart immediately if items arrived between the idle-timer fire and now.
		// We hold e.mu throughout, so no concurrent ensureRunning can interfere.
		if len(e.queue) > 0 {
			p.ctxMu.RLock()
			parentCtx := p.parentCtx
			p.ctxMu.RUnlock()

			if parentCtx != nil && parentCtx.Err() == nil {
				ctx2, cancel2 := context.WithCancel(parentCtx)
				e.cancel = cancel2
				e.running = true
				go p.run(ctx2, key, e)
			}
		}
	}()

	idleTimer := time.NewTimer(p.idleTTL)
	defer idleTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case v, ok := <-e.queue:
			if !ok {
				return
			}
			// Reset idle timer correctly per Go timer docs.
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(p.idleTTL)

			if err := p.handler(ctx, v); err != nil {
				p.log.Error(err, "handler error")
			}

		case <-idleTimer.C:
			// Idle TTL reached — exit goroutine, defer will restart if needed.
			p.log.V(1).Info("idle TTL reached due to no activity, exiting worker goroutine", "key", key)

			return
		}
	}
}
