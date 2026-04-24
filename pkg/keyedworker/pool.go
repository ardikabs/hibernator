/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package keyedworker provides a per-key worker pool with lazy goroutine creation
// and idle-based goroutine reaping.
//
// # Design goals
//
//   - Deliver is always non-blocking: values are forwarded into the per-key Slot
//     regardless of whether the worker goroutine is currently alive.
//   - Per-key isolation: each key has exactly one goroutine running at any time,
//     eliminating reordering races inherent in a shared flat pool.
//   - Idle reaping: goroutines are created lazily on the first Deliver and are
//     terminated either by an idle signal from the goroutine itself or by an explicit
//     Remove call. If pending items remain when a goroutine exits, a new one is
//     re-spawned immediately to drain them.
//   - Concurrency across keys: different keys are processed fully in parallel.
//   - Clean removal: Remove cancels the per-key goroutine and drains the slot,
//     freeing memory when a resource is deleted.
//
// # Two Factories: Slot vs Worker
//
// The Pool uses two distinct factories:
//
//   - slotFactory (via WithSlotFactory option): Creates per-key Slots that buffer
//     values. Defaults to FIFOSlot. Must be provided at Pool construction time.
//
//   - workerFactory (via Register method): Creates per-key worker goroutine bodies.
//     Must be provided at runtime when the context becomes available.
//
// This separation allows Deliver() calls to buffer values before Register() is called,
// supporting decoupled initialization patterns common in controller-runtime Runnables.
//
// # Bring Your Own Slot
//
// Callers choose a Slot implementation via WithSlotFactory:
//
//   - FIFOSlot: backed by a buffered channel. Every update is preserved in FIFO
//     order. Use for consumers where no update can be dropped (e.g. status writers).
//
//   - LatestWinsSlot: backed by a conflate.Pipeline. Concurrent sends coalesce into
//     a single latest-value notification. Use for consumers where only the freshest snapshot matters
//     and intermediate updates are safe to discard (e.g. plan actor workers).
//
// # Bring Your Own Worker Body
//
// The consumer controls what the goroutine does by providing a workerFactory to Register:
//
//	pool.Register(ctx, func(key K, slot Slot[V]) func(context.Context) {
//	    // Return the goroutine body. The Pool owns the goroutine's lifecycle.
//	    return func(ctx context.Context) {
//	        for { select { case <-slot.C(): ... } }
//	    }
//	})
//
// The workerFactory is stored atomically to ensure safe concurrent access with Deliver.
// For the common stateless-callback pattern, RunnerFactory is a convenience
// helper that wraps a per-value handler and an idle TTL into the factory signature.
package keyedworker

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/go-logr/logr"
)

const defaultFIFOBufSize = 100

// workerFactoryFunc wraps the user-supplied worker factory for atomic storage.
// This enables safe concurrent access between Register (write) and Deliver (read).
type workerFactoryFunc[K comparable, V any] struct {
	fn func(K, Slot[V]) func(context.Context)
}

// Pool manages a set of per-key worker goroutines.
// The zero value is not usable; use New to create instances.
type Pool[K comparable, V any] struct {
	mu      sync.RWMutex
	entries map[K]*entry[V]

	// workerFactory represents the user-supplied factory for per-key goroutine bodies. It is nil until Register is called.
	// Use atomic operations to access.
	workerFactory atomic.Pointer[workerFactoryFunc[K, V]]

	// slotFactory creates a fresh Slot for each new entry.
	slotFactory func() Slot[V]

	ctxMu        sync.RWMutex
	parentCtx    context.Context //nolint:containedctx
	parentCancel context.CancelFunc

	log      logr.Logger
	onSpawn  func(K) // optional lifecycle hook — fires when a goroutine is started
	onRemove func(K) // optional lifecycle hook — fires when a goroutine exits or is cancelled

	// autoRemoveOnIdle, when true, deletes the per-key entry from p.entries after
	// an idle-reap where the slot is empty. Reclaims memory for keys that are no
	// longer sending deliveries. Only applies to idle exits; context-cancellation
	// exits (pool shutdown) are never auto-removed.
	autoRemoveOnIdle bool
}

// entry is the per-key state. The Slot outlives the worker goroutine so that values
// delivered while the goroutine is absent are buffered and drained on goroutine restart.
type entry[V any] struct {
	slot Slot[V]

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

// Option configures a Pool.
type Option[K comparable, V any] func(*Pool[K, V])

// WithSlotFactory sets the factory function used to create a new Slot for each key.
// Defaults to FIFOSlot with a buffer size of 100 if not set.
func WithSlotFactory[K comparable, V any](f func() Slot[V]) Option[K, V] {
	return func(p *Pool[K, V]) { p.slotFactory = f }
}

// WithLogger attaches a logger for internal diagnostics.
func WithLogger[K comparable, V any](log logr.Logger) Option[K, V] {
	return func(p *Pool[K, V]) { p.log = log }
}

// WithOnSpawnCallback registers a callback that fires each time a per-key goroutine is started.
// Suitable for incrementing a gauge metric.
func WithOnSpawnCallback[K comparable, V any](fn func(K)) Option[K, V] {
	return func(p *Pool[K, V]) { p.onSpawn = fn }
}

// WithOnRemoveCallback registers a callback that fires each time a per-key goroutine exits
// (idle reap, context cancel, or explicit Remove). Suitable for decrementing a gauge metric.
func WithOnRemoveCallback[K comparable, V any](fn func(K)) Option[K, V] {
	return func(p *Pool[K, V]) { p.onRemove = fn }
}

// WithAutoRemoveOnIdle causes the pool to delete the per-key entry from its
// internal map after an idle-reap where the slot is empty. This reclaims memory
// for keys that are no longer actively receiving deliveries. A subsequent Deliver
// for the same key recreates the entry transparently.
//
// Only entries that exit due to idle TTL are eligible for auto-removal; entries
// that exit due to context cancellation (pool shutdown) are never auto-removed.
func WithAutoRemoveOnIdle[K comparable, V any]() Option[K, V] {
	return func(p *Pool[K, V]) { p.autoRemoveOnIdle = true }
}

// New creates a new Pool with the configured slotFactory.
//
// The pool is not active until Register is called with the workerFactory.
// Deliver can be called before Register; values will buffer in the slot
// and workers will activate once Register provides the workerFactory.
func New[K comparable, V any](opts ...Option[K, V]) *Pool[K, V] {
	p := &Pool[K, V]{
		entries: make(map[K]*entry[V]),
		log:     logr.Discard(),
	}
	for _, o := range opts {
		o(p)
	}
	if p.slotFactory == nil {
		p.slotFactory = FIFOSlot[V](defaultFIFOBufSize)
	}
	return p
}

// Register arms the pool with a workerFactory and parent context, then
// activates workers for any keys whose slots already have pending items (from
// pre-Register Deliver calls). Must be called exactly once before workers can
// start processing.
//
// Register stores the workerFactory atomically to ensure safe concurrent access
// with Deliver. It is non-blocking — it returns immediately after arming the factory
// and flushing pre-buffered items. The caller is responsible for blocking until
// the desired lifetime is over (e.g. <-ctx.Done()), then calling Stop.
//
// workerFactory receives a key and the key's Slot and must return a func(context.Context)
// that is the goroutine body. The Pool owns the goroutine's lifecycle — the returned
// func must respect ctx cancellation and return when done.
func (p *Pool[K, V]) Register(ctx context.Context, workerFactoryFn func(K, Slot[V]) func(context.Context)) {
	p.ctxMu.Lock()
	p.parentCtx, p.parentCancel = context.WithCancel(ctx)
	p.ctxMu.Unlock()

	p.workerFactory.Store(&workerFactoryFunc[K, V]{fn: workerFactoryFn})

	// Activate workers for any items that arrived before Register was called.
	p.mu.RLock()
	for key, e := range p.entries {
		if e.slot.Len() > 0 {
			p.ensureRunning(key, e)
		}
	}
	p.mu.RUnlock()
}

// Deliver routes value to the per-key slot and ensures the worker goroutine is running.
// Never blocks: if the slot is full (for FIFO) the value is dropped at the slot level.
// Safe to call before Register; pending items are processed once Register provides the workerFactory.
func (p *Pool[K, V]) Deliver(key K, value V) {
	e := p.getOrCreate(key)
	e.slot.Send(value)

	if p.workerFactory.Load() != nil {
		p.ensureRunning(key, e)
	}
}

// Remove cancels the per-key worker goroutine, drains the slot, and removes the
// entry, reclaiming memory when the corresponding resource is deleted.
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
	e.slot.Drain()
}

// Len returns the total number of pending items across all slots.
func (p *Pool[K, V]) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	total := 0
	for _, e := range p.entries {
		total += e.slot.Len()
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
	e = &entry[V]{slot: p.slotFactory()}
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
	go p.runEntry(ctx, key, e)
}

// runEntry launches the user-supplied worker goroutine body. It fires the onSpawn hook
// before calling the body, and the onRemove hook plus idle-restart / auto-remove
// logic in its defer.
func (p *Pool[K, V]) runEntry(ctx context.Context, key K, e *entry[V]) {
	if p.onSpawn != nil {
		p.onSpawn(key)
	}

	// Load the workerFactory atomically. This is safe because Register stores it
	// atomically before any workers can start (ensureRunning checks parentCtx first).
	wf := p.workerFactory.Load()
	if wf == nil {
		// No workerFactory registered yet; this shouldn't happen because ensureRunning
		// checks parentCtx, but be defensive.
		return
	}
	fn := wf.fn(key, e.slot)

	defer func() {
		e.mu.Lock()

		// Capture idle vs shutdown BEFORE calling e.cancel(): once we cancel the
		// per-key context ourselves, ctx.Err() becomes non-nil regardless of whether
		// the goroutine exited due to an idle reap or a pool shutdown.
		idleExit := ctx.Err() == nil

		e.cancel()
		e.running = false

		if p.onRemove != nil {
			p.onRemove(key)
		}

		// If items arrived between the goroutine's idle-exit decision and here,
		// restart immediately so no value is stranded. We hold e.mu throughout,
		// so no concurrent ensureRunning can interfere.
		if e.slot.Len() > 0 {
			p.ctxMu.RLock()
			parentCtx := p.parentCtx
			p.ctxMu.RUnlock()

			if parentCtx != nil && parentCtx.Err() == nil {
				ctx2, cancel2 := context.WithCancel(parentCtx)
				e.cancel = cancel2
				e.running = true

				if p.onSpawn != nil {
					p.onSpawn(key)
				}

				go p.runEntry(ctx2, key, e)
			}
			e.mu.Unlock()
			return
		}

		e.mu.Unlock()

		if !p.autoRemoveOnIdle || !idleExit {
			return
		}

		// Auto-remove: reclaim the entry from the pool map now that the slot is
		// empty and the goroutine has fully exited.
		//
		// Lock order must be p.mu → e.mu to match Remove() and avoid deadlock.
		// Re-check liveness inside the lock pair: a concurrent Deliver may have
		// called ensureRunning between our e.mu.Unlock() and here.
		p.mu.Lock()
		e.mu.Lock()
		if !e.running && e.slot.Len() == 0 {
			delete(p.entries, key)
		}
		e.mu.Unlock()
		p.mu.Unlock()
	}()

	fn(ctx)
}
