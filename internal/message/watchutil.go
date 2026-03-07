/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package message

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/go-logr/logr"
	"github.com/telepresenceio/watchable"

	"github.com/ardikabs/hibernator/internal/metrics"
)

// Metadata holds identification information for a subscription handler,
// used for logging and metrics.
type Metadata struct {
	// Runner is the name of the subscription runner (e.g., "schedule-processor").
	Runner string
	// Message is the message type being processed (e.g., "plan-resources").
	Message string
}

// HandleSubscription processes updates from a watchable.Map subscription channel.
// Adapted from Envoy Gateway's HandleSubscription pattern.
//
// Behavior:
//  1. First snapshot → iterate snapshot.State, call handle for each entry (bootstrap).
//  2. Subsequent snapshots → iterate coalesced updates, call handle per update.
//  3. Each handle invocation is wrapped in crash recovery (panic catch → log → continue).
//  4. Metrics are recorded for each invocation.
//
// The function blocks until ctx is done or the subscription channel closes.
func HandleSubscription[K comparable, V any](
	ctx context.Context,
	log logr.Logger,
	meta Metadata,
	sub <-chan watchable.Snapshot[K, V],
	handle func(update watchable.Update[K, V], errChan chan error),
) {
	log = log.WithValues("runner", meta.Runner, "message", meta.Message)

	errChans := make(chan error, 10)
	go func() {
		for err := range errChans {
			log.Error(err, "observed an error")
			metrics.WatchableSubscribeTotal.WithLabelValues(meta.Runner, meta.Message, "error").Inc()
		}
	}()
	defer close(errChans)

	first := true
	for {
		select {
		case <-ctx.Done():
			return
		case snapshot, ok := <-sub:
			if !ok {
				return
			}

			if first {
				log.V(1).Info("processing initial snapshot", "numEntries", len(snapshot.State))

				// Bootstrap: process all current state entries
				first = false
				for k, v := range snapshot.State {
					handleWithCrashRecovery(log, meta, handle, watchable.Update[K, V]{
						Key:   k,
						Value: v,
					}, errChans)
				}
			} else {
				log.V(1).Info("processing snapshot updates", "numUpdates", len(snapshot.Updates))

				// Subsequent: process only coalesced updates
				for _, update := range coalesceUpdates(snapshot.Updates) {
					handleWithCrashRecovery(log, meta, handle, update, errChans)
				}
			}
		}
	}
}

// coalesceUpdates deduplicates a list of updates, keeping only the last update per key.
// This prevents processors from seeing stale intermediate states within a single snapshot.
func coalesceUpdates[K comparable, V any](updates []watchable.Update[K, V]) []watchable.Update[K, V] {
	if len(updates) <= 1 {
		return updates
	}

	// Iterate backwards to find the last update for each key
	seen := make(map[K]struct{}, len(updates))
	result := make([]watchable.Update[K, V], 0, len(updates))

	for i := len(updates) - 1; i >= 0; i-- {
		if _, ok := seen[updates[i].Key]; ok {
			continue
		}
		seen[updates[i].Key] = struct{}{}
		result = append(result, updates[i])
	}

	// Reverse to maintain original order
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return result
}

// handleWithCrashRecovery wraps a handler function with panic recovery.
// If the handler panics, the panic is caught, logged with a stack trace,
// and the panic counter metric is incremented. The subscription continues.
func handleWithCrashRecovery[K comparable, V any](
	log logr.Logger, meta Metadata,
	handle func(update watchable.Update[K, V], errChans chan error),
	update watchable.Update[K, V],
	errChans chan error,
) {
	start := time.Now()
	status := "success"

	defer func() {
		if r := recover(); r != nil {
			status = "panic"
			log.Error(nil, "panic recovered in subscription handler",
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}

		duration := time.Since(start).Seconds()
		metrics.WatchableSubscribeTotal.WithLabelValues(meta.Runner, meta.Message, status).Inc()
		metrics.WatchableSubscribeDuration.WithLabelValues(meta.Runner, meta.Message).Observe(duration)
	}()

	handle(update, errChans)
}
