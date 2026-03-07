/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package common

import (
	"fmt"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"k8s.io/utils/clock"
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
// The In field of every returned ScheduleEvent reflects the NextRequeueTime duration
// from the previous step's perspective, matching the controller's requeue cadence.
func ComputeUpcomingEvents(baseWindows []scheduler.OffHourWindow, timezone string, exception *scheduler.Exception, count int) ([]ScheduleEvent, error) {
	if len(baseWindows) == 0 {
		return nil, fmt.Errorf("no base windows defined")
	}

	var events []ScheduleEvent
	cursor := time.Now()

	for len(events) < count {
		eval := scheduler.NewScheduleEvaluator(fixedClock{t: cursor})
		result, err := eval.Evaluate(baseWindows, timezone, exception)
		if err != nil {
			return nil, fmt.Errorf("evaluate schedule: %w", err)
		}

		var nextEventTime time.Time
		var operation string

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
			In:        in,
		})

		// Advance cursor to the point the controller would next reconcile.
		cursor = cursor.Add(in)
	}

	return events, nil
}

// ComputeNextEvent computes the next hibernate or wakeup event for a schedule.
// Returns nil if the schedule has no off-hour windows defined.
// No active exception is considered here; this is used by the plan list view.
func ComputeNextEvent(schedule hibernatorv1alpha1.Schedule) (*ScheduleEvent, error) {
	if len(schedule.OffHours) == 0 {
		return nil, nil
	}

	events, err := ComputeUpcomingEvents(ConvertAPIWindows(schedule.OffHours), schedule.Timezone, nil, 1)
	if err != nil {
		return nil, err
	}

	if len(events) == 0 {
		return nil, nil
	}

	return &events[0], nil
}
