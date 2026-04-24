/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package keyedworker

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"
)

// RunnerFactory wraps a stateless per-value handler into a workerFactory
// suitable for passing directly to Pool.Register when the consumer does not need
// to maintain any state across deliveries.
//
// # When to use RunnerFactory
//
// Use RunnerFactory when the caller has no proper way to own the goroutine
// lifecycle itself — that is, when the caller is a pure stateless callback and
// relies on the Pool to spawn, reap, and restart goroutines around an idle timer.
// The status processor is a canonical example: each reconciler invocation
// independently calls Send with no shared coordination context, so RunnerFactory
// provides the lifecycle management that the caller cannot supply on its own.
//
// If the caller can manage its own serialization — maintaining a dedicated
// long-lived goroutine, an event loop, or a structured receive/send discipline —
// it should supply a custom workerFactory directly to Pool.Register and skip
// RunnerFactory entirely. The Coordinator is a canonical example: it owns an
// explicit actor model with a select loop and state machine, so it drives the
// slot directly and does not need RunnerFactory's idle-reap abstraction.
//
// The goroutine body produced by the workerFactory drives the slot via an idle timer loop:
//   - On each slot signal the handler is called with the latest value.
//   - The idle timer is reset on each delivery.
//   - When idleTTL elapses with no delivery the goroutine returns, allowing
//     the Pool to reap it. The Pool restarts the goroutine on the next Deliver.
//
// Handler errors are non-fatal: the onErr callback is invoked when the handler
// returns an error, and processing continues with subsequent deliveries.
// Pass nil for onErr to silently discard errors.
//
// Typical usage:
//
//	pool.Register(ctx, keyedworker.RunnerFactory[K, V](30*time.Minute,
//	    func(ctx context.Context, v V) error {
//	        return process(ctx, v)
//	    },
//	    func(err error) { log.Error(err, "handler error") },
//	))
func RunnerFactory[K comparable, V any](
	idleTTL time.Duration,
	handler func(ctx context.Context, value V) error,
	onErr func(error),
) func(K, Slot[V]) func(context.Context) {
	return func(key K, slot Slot[V]) func(context.Context) {
		return func(ctx context.Context) {
			idleTimer := time.NewTimer(idleTTL)
			defer idleTimer.Stop()

			for {
				select {
				case <-ctx.Done():
					return

				case <-slot.C():
					// Reset idle timer correctly per Go timer docs.
					if !idleTimer.Stop() {
						select {
						case <-idleTimer.C:
						default:
						}
					}
					idleTimer.Reset(idleTTL)

					if err := handleWithCrashRecovery(ctx, key, slot.Recv(), handler); err != nil && onErr != nil {
						onErr(err)
					}

				case <-idleTimer.C:
					// Idle TTL reached. Flush any items that arrived in the race window
					// between the idle decision and now, so no value is silently lost when
					// the pool auto-removes the entry after this return.
					for {
						select {
						case <-slot.C():
							if err := handleWithCrashRecovery(ctx, key, slot.Recv(), handler); err != nil && onErr != nil {
								onErr(err)
							}
						default:
							return
						}
					}
				}
			}
		}
	}
}

func handleWithCrashRecovery[K comparable, V any](
	ctx context.Context,
	key K,
	value V,
	handler func(ctx context.Context, value V) error,
) (err error) {

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered in slot handler (%v): %v; stacktrace=%s", key, r, string(debug.Stack()))
		}
	}()

	return handler(ctx, value)
}
