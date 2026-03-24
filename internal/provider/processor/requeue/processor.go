/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package requeue provides the PlanRequeueProcessor, the sole owner of time-based
// re-enqueuing for HibernatePlan reconciliation. It subscribes to PlanResources and
// computes the next boundary time from both the schedule evaluation and exception
// lifecycle (ValidFrom/ValidUntil). When a boundary is reached, it fires a
// PlanEnqueuer.Enqueue() to trigger a fresh reconcile — no other component manages
// time-based requeues.
package requeue

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/telepresenceio/watchable"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"

	"github.com/ardikabs/hibernator/internal/message"
)

// timerEntry pairs a clock.Timer with the CancelFunc for the goroutine that
// waits on it. Cancelling the context terminates the goroutine even if the
// timer has not yet fired, preventing goroutine leaks when a plan update
// replaces an in-flight timer before it expires.
type timerEntry struct {
	timer  clock.Timer
	cancel context.CancelFunc
}

// PlanRequeueProcessor subscribes to PlanResources and manages time-based
// re-enqueuing for both schedule boundaries and exception lifecycle boundaries.
//
// It uses a timerEntry per plan — one timer per plan key, always set to the
// nearest upcoming boundary. The handler runs serially inside HandleSubscription,
// so the timer map requires no locking.
type PlanRequeueProcessor struct {
	Clock     clock.Clock
	Log       logr.Logger
	Resources *message.ControllerResources
	Enqueuer  message.PlanEnqueuer
}

// NeedLeaderElection returns true — only the leader should drive time-based re-enqueues.
func (p *PlanRequeueProcessor) NeedLeaderElection() bool { return true }

// Start implements manager.Runnable. It blocks until ctx is cancelled.
func (p *PlanRequeueProcessor) Start(ctx context.Context) error {
	log := p.Log.WithName("requeue")
	log.Info("starting plan requeue processor")

	timers := make(map[types.NamespacedName]*timerEntry)

	// On shutdown, cancel all goroutines and stop their timers.
	defer func() {
		for key, e := range timers {
			e.cancel()
			_ = e.timer.Stop()
			delete(timers, key)
		}
	}()

	message.HandleSubscription(ctx, log, message.Metadata{
		Runner:  "plan-requeue",
		Message: "plan-resources",
	}, p.Resources.PlanResources.Subscribe(ctx),
		func(update watchable.Update[types.NamespacedName, *message.PlanContext], _ chan error) {
			key := update.Key
			log := p.Log.WithValues("plan", key)

			// Cancel the previous goroutine and stop its timer before replacing.
			// Calling e.cancel() signals the goroutine via timerCtx.Done(), ensuring
			// it exits even if the timer has not fired yet.
			if e, ok := timers[key]; ok {
				e.cancel()
				_ = e.timer.Stop()
				delete(timers, key)
			}

			if update.Delete {
				return
			}

			now := p.Clock.Now()
			boundary, ok := computeBoundary(now, update.Value)
			if !ok {
				log.V(1).Info("no time boundary for plan")
				return
			}

			d := boundary.Sub(now)
			if d <= 0 {
				// Boundary already passed — enqueue immediately.
				log.V(1).Info("boundary already passed, enqueuing immediately")
				p.Enqueuer.Enqueue(key)
				return
			}

			log.V(1).Info("starting internal requeue timer",
				"boundary", boundary.Format(time.RFC3339),
				"duration", d.String(),
			)

			// Use Clock.NewTimer so fake clocks in tests control when the timer fires.
			t := p.Clock.NewTimer(d)
			timerCtx, cancel := context.WithCancel(ctx)
			timers[key] = &timerEntry{timer: t, cancel: cancel}
			go func() {
				// defer cancel ensures context resources are freed when the goroutine
				// exits, regardless of which select branch triggered.
				defer cancel()
				select {
				case <-timerCtx.Done():
					// Cancelled by a subsequent plan update or controller shutdown.
					_ = t.Stop()
				case <-t.C():
					log.V(1).Info("requeue timer fired, enqueuing plan")
					p.Enqueuer.Enqueue(key)
				}
			}()
		},
	)

	log.Info("plan requeue processor stopped")
	return nil
}

// computeBoundary returns the earliest time at which this plan should be re-enqueued.
// It considers:
//   - Schedule.NextEvent: the absolute timestamp of the next schedule transition
//     (already includes schedule buffer and safety buffer)
//   - Exception ValidFrom/ValidUntil: any future boundary timestamp across all exceptions
//
// Exception boundaries are evaluated purely from Spec timestamps, independent of
// Status.State. This ensures fresh exceptions (Status.State == "") and exceptions
// whose status write hasn't been observed yet are handled correctly.
//
// Returns (time.Time{}, false) if no boundary is found.
func computeBoundary(now time.Time, planCtx *message.PlanContext) (time.Time, bool) {
	if planCtx == nil {
		return time.Time{}, false
	}

	var earliest time.Time

	// Schedule boundary: NextEvent is an absolute timestamp; use it directly.
	if planCtx.Schedule != nil && !planCtx.Schedule.NextEvent.IsZero() {
		earliest = planCtx.Schedule.NextEvent
	}

	// Exception lifecycle boundaries: include every future ValidFrom or ValidUntil.
	// Using now.Before(t) rather than t.After(now) is equivalent but reads more naturally
	// as "is this timestamp still in the future?".
	for i := range planCtx.Exceptions {
		exc := &planCtx.Exceptions[i]
		if !exc.Spec.ValidFrom.IsZero() && now.Before(exc.Spec.ValidFrom.Time) {
			earliest = minTime(earliest, exc.Spec.ValidFrom.Time)
		}
		if !exc.Spec.ValidUntil.IsZero() && now.Before(exc.Spec.ValidUntil.Time) {
			earliest = minTime(earliest, exc.Spec.ValidUntil.Time)
		}
	}

	return earliest, !earliest.IsZero()
}

// minTime returns the earlier of two times. If a is zero, returns b and vice versa.
func minTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.Before(b) {
		return a
	}
	return b
}
