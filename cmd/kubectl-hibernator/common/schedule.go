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
)

// ScheduleEvent represents a single upcoming event.
type ScheduleEvent struct {
	Time      time.Time `json:"time"`
	Operation string    `json:"operation"`
}

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

// ComputeUpcomingEvents computes the next N hibernate/wakeup events.
func ComputeUpcomingEvents(schedule hibernatorv1alpha1.Schedule, count int) ([]ScheduleEvent, error) {
	hibernateCron, wakeUpCron, err := scheduler.ConvertOffHoursToCron(ConvertAPIWindows(schedule.OffHours))
	if err != nil {
		return nil, fmt.Errorf("failed to convert to cron: %w", err)
	}

	loc, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", schedule.Timezone, err)
	}

	now := time.Now().In(loc)

	parser := scheduler.NewCronParser()
	hSched, err := parser.Parse(hibernateCron)
	if err != nil {
		return nil, fmt.Errorf("failed to parse hibernate cron: %w", err)
	}

	wSched, err := parser.Parse(wakeUpCron)
	if err != nil {
		return nil, fmt.Errorf("failed to parse wakeup cron: %w", err)
	}

	var events []ScheduleEvent
	hNext := hSched.Next(now)
	wNext := wSched.Next(now)

	for len(events) < count {
		if hNext.Before(wNext) {
			events = append(events, ScheduleEvent{Time: hNext, Operation: "Hibernate"})
			hNext = hSched.Next(hNext)
		} else {
			events = append(events, ScheduleEvent{Time: wNext, Operation: "WakeUp"})
			wNext = wSched.Next(wNext)
		}
	}

	return events, nil
}

// ComputeNextEvent computes the next hibernate or wakeup event for a schedule.
// Returns nil if the schedule has no off-hour windows defined.
func ComputeNextEvent(schedule hibernatorv1alpha1.Schedule) (*ScheduleEvent, error) {
	if len(schedule.OffHours) == 0 {
		return nil, nil
	}

	events, err := ComputeUpcomingEvents(schedule, 1)
	if err != nil {
		return nil, err
	}

	if len(events) == 0 {
		return nil, nil
	}

	return &events[0], nil
}
