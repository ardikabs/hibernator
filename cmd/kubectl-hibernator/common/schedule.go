/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package common

import (
	"context"
	"fmt"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ScheduleEvent represents a single upcoming event.
type ScheduleEvent struct {
	Time      time.Time `json:"time"`
	Operation string    `json:"operation"`
	// In is the requeue duration to this event from the perspective of the
	// evaluator at the previous step (mirrors NextRequeueTime semantics).
	In time.Duration `json:"in,omitempty"`
}

// fixedClock implements clock.Clock but always returns a fixed time.
// Only Now() and Since() are meaningful; the remaining methods delegate to
// clock.RealClock so the struct satisfies the full interface.
type fixedClock struct {
	clock.RealClock

	t time.Time
}

func (c fixedClock) Now() time.Time                  { return c.t }
func (c fixedClock) Since(t time.Time) time.Duration { return c.t.Sub(t) }

// ConvertAPIWindows converts API OffHourWindows to scheduler OffHourWindows.
func ConvertAPIWindows(apiWindows []hibernatorv1alpha1.OffHourWindow) []scheduler.OffHourWindow {
	out := make([]scheduler.OffHourWindow, len(apiWindows))
	for i, w := range apiWindows {
		out[i] = scheduler.OffHourWindow{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}
	return out
}

// ConvertAPIException converts an active ScheduleException resource into the
// scheduler.Exception type used by the evaluator.
func ConvertAPIException(exc hibernatorv1alpha1.ScheduleException) *scheduler.Exception {
	windows := make([]scheduler.OffHourWindow, len(exc.Spec.Windows))
	for i, w := range exc.Spec.Windows {
		windows[i] = scheduler.OffHourWindow{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}

	var leadTime time.Duration
	if exc.Spec.LeadTime != "" {
		leadTime, _ = time.ParseDuration(exc.Spec.LeadTime)
	}

	return &scheduler.Exception{
		Type:       scheduler.ExceptionType(exc.Spec.Type),
		ValidFrom:  exc.Spec.ValidFrom.Time,
		ValidUntil: exc.Spec.ValidUntil.Time,
		LeadTime:   leadTime,
		Windows:    windows,
	}
}

// ComputeUpcomingEvents computes the next N hibernate/wakeup events by simulating
// successive evaluator steps. Each step advances the clock by NextRequeueTime so
// that state-transition boundaries respect schedule buffers and active exceptions.
// The In field of every returned ScheduleEvent reflects the duration from when the
// computation started (user's perspective) to when the event will occur.
func ComputeUpcomingEvents(baseWindows []scheduler.OffHourWindow, timezone string, exceptions []*scheduler.Exception, count int) ([]ScheduleEvent, error) {
	if len(baseWindows) == 0 {
		return nil, fmt.Errorf("no base windows defined")
	}

	var events []ScheduleEvent
	startTime := time.Now()
	cursor := startTime

	for len(events) < count {
		eval := scheduler.NewScheduleEvaluator(fixedClock{t: cursor})
		result, err := eval.Evaluate(baseWindows, timezone, exceptions)
		if err != nil {
			return nil, fmt.Errorf("evaluate schedule: %w", err)
		}

		var (
			nextEventTime time.Time
			operation     string
		)

		if result.ShouldHibernate {
			nextEventTime = result.NextWakeUpTime
			operation = "WakeUp"
		} else {
			nextEventTime = result.NextHibernateTime
			operation = "Hibernate"
		}

		if nextEventTime.IsZero() || !nextEventTime.After(cursor) {
			break
		}

		in := eval.NextRequeueTime(result)
		events = append(events, ScheduleEvent{
			Time:      nextEventTime,
			Operation: operation,
			In:        nextEventTime.Sub(startTime),
		})

		// Advance cursor to the point the controller would next reconcile.
		cursor = cursor.Add(in)
	}

	return events, nil
}

// ComputeNextEvent computes the next hibernate or wakeup event for a schedule,
// optionally considering an active exception.
// Returns nil if the schedule has no off-hour windows defined.
func ComputeNextEvent(schedule hibernatorv1alpha1.Schedule, exceptions []*scheduler.Exception) (*ScheduleEvent, error) {
	if len(schedule.OffHours) == 0 {
		return nil, nil
	}

	events, err := ComputeUpcomingEvents(ConvertAPIWindows(schedule.OffHours), schedule.Timezone, exceptions, 1)
	if err != nil {
		return nil, err
	}

	if len(events) == 0 {
		return nil, nil
	}

	return &events[0], nil
}

// FetchActiveExceptions lists ScheduleException resources for the given plan and
// returns all active ones as scheduler exceptions, ordered by creation timestamp
// descending (newest first).
func FetchActiveExceptions(ctx context.Context, c client.Client, plan hibernatorv1alpha1.HibernatePlan) ([]*scheduler.Exception, error) {
	var list hibernatorv1alpha1.ScheduleExceptionList
	if err := c.List(ctx, &list,
		client.InNamespace(plan.Namespace),
		client.MatchingLabels{"hibernator.ardikabs.com/plan": plan.Name},
	); err != nil {
		return nil, fmt.Errorf("list schedule exceptions: %w", err)
	}

	now := time.Now()
	var active []hibernatorv1alpha1.ScheduleException
	for _, exc := range list.Items {
		if exc.Status.State != hibernatorv1alpha1.ExceptionStateActive {
			continue
		}
		if now.Before(exc.Spec.ValidFrom.Time) || now.After(exc.Spec.ValidUntil.Time) {
			continue
		}
		active = append(active, exc)
	}

	if len(active) == 0 {
		return nil, nil
	}

	result := make([]*scheduler.Exception, len(active))
	for i, exc := range active {
		result[i] = ConvertAPIException(exc)
	}

	return result, nil
}
