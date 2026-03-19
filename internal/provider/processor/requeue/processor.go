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

// PlanRequeueProcessor subscribes to PlanResources and manages time-based
// re-enqueuing for both schedule boundaries and exception lifecycle boundaries.
//
// It uses clock.Timer per plan — one timer per plan key, always set to the
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

	timers := make(map[types.NamespacedName]clock.Timer)

	// On shutdown, stop all armed timers to prevent late fires.
	defer func() {
		for key, t := range timers {
			_ = t.Stop()
			delete(timers, key)
		}
	}()

	message.HandleSubscription(ctx, log, message.Metadata{
		Runner:  "plan-requeue",
		Message: "plan-resources",
	}, p.Resources.PlanResources.Subscribe(ctx),
		func(update watchable.Update[types.NamespacedName, *message.PlanContext], _ chan error) {
			key := update.Key

			// Always cancel the previous timer for this plan first.
			if t, ok := timers[key]; ok {
				_ = t.Stop()
				delete(timers, key)
			}

			if update.Delete {
				return
			}

			now := p.Clock.Now()
			boundary, ok := computeBoundary(now, update.Value)
			if !ok {
				log.V(1).Info("no time boundary for plan", "plan", key)
				return
			}

			d := boundary.Sub(now)
			if d <= 0 {
				// Boundary already passed — enqueue immediately.
				log.V(1).Info("boundary already passed, enqueuing immediately", "plan", key)
				p.Enqueuer.Enqueue(key)
				return
			}

			log.V(1).Info("arming requeue timer",
				"plan", key,
				"boundary", boundary.Format(time.RFC3339),
				"duration", d.String(),
			)

			// Use Clock.NewTimer so fake clocks in tests control when the timer fires.
			t := p.Clock.NewTimer(d)
			timers[key] = t
			go func() {
				select {
				case <-ctx.Done():
					_ = t.Stop()
				case <-t.C():
					log.V(1).Info("requeue timer fired", "plan", key)
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
//   - Schedule.RequeueAfter: the next schedule evaluation boundary
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

	// Schedule boundary
	if planCtx.Schedule != nil && planCtx.Schedule.RequeueAfter > 0 {
		earliest = now.Add(planCtx.Schedule.RequeueAfter)
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
