/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"k8s.io/utils/clock"
)

// ExceptionType defines the type of schedule exception.
type ExceptionType string

const (
	// ExceptionExtend adds hibernation windows to the base schedule.
	ExceptionExtend ExceptionType = "extend"
	// ExceptionSuspend prevents hibernation during specified windows (carve-out).
	ExceptionSuspend ExceptionType = "suspend"
	// ExceptionReplace completely replaces the base schedule during the exception period.
	ExceptionReplace ExceptionType = "replace"
)

// Exception represents a schedule exception for evaluation.
type Exception struct {
	// Type is the exception type: extend, suspend, or replace.
	Type ExceptionType

	// ValidFrom is when the exception period starts.
	ValidFrom time.Time

	// ValidUntil is when the exception period ends.
	ValidUntil time.Time

	// LeadTime is the buffer before suspension (only for suspend type).
	LeadTime time.Duration

	// Windows are the exception time windows.
	Windows []OffHourWindow
}

// ScheduleEvaluator evaluates cron-based schedules to determine hibernation state.
type ScheduleEvaluator struct {
	Clock clock.Clock

	parser         cron.Parser
	scheduleBuffer time.Duration
}

type ScheduleEvaluatorOption func(*ScheduleEvaluator)

// NewScheduleEvaluator creates a new schedule evaluator.
func NewScheduleEvaluator(clk clock.Clock, opts ...ScheduleEvaluatorOption) *ScheduleEvaluator {
	se := &ScheduleEvaluator{
		Clock: clk,
		// Use standard cron format with optional seconds
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}

	for _, o := range opts {
		o(se)
	}

	return se
}

func WithScheduleBuffer(duration string) ScheduleEvaluatorOption {
	return func(se *ScheduleEvaluator) {
		d, err := time.ParseDuration(duration)
		if err != nil {
			return
		}

		se.scheduleBuffer = d
	}
}

// EvaluationResult contains the result of schedule evaluation.
type EvaluationResult struct {
	// ShouldHibernate indicates if the system should be in hibernation state.
	ShouldHibernate bool

	// NextHibernateTime is the next scheduled hibernation time.
	NextHibernateTime time.Time

	// NextWakeUpTime is the next scheduled wake-up time.
	NextWakeUpTime time.Time

	// CurrentState describes the current state based on schedule.
	CurrentState string
}

// eval determines if we should be in hibernation based on the schedule.
// It compares the last hibernate and wake-up times to determine current state.
//
// The function also applies grace periods (lead time buffer) to prevent unnecessary
// phase transitions at window boundaries for minimal windows (e.g., 23:59-00:00).
func (e *ScheduleEvaluator) eval(window ScheduleWindow) (*EvaluationResult, error) {
	now := e.Clock.Now()
	loc, err := time.LoadLocation(window.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %s: %w", window.Timezone, err)
	}

	localNow := now.In(loc)

	// Parse cron expressions
	hibernateSched, err := e.parser.Parse(window.HibernateCron)
	if err != nil {
		return nil, fmt.Errorf("invalid hibernate cron %q: %w", window.HibernateCron, err)
	}

	wakeUpSched, err := e.parser.Parse(window.WakeUpCron)
	if err != nil {
		return nil, fmt.Errorf("invalid wakeUp cron %q: %w", window.WakeUpCron, err)
	}

	// Find the most recent hibernate and wake-up times before now
	lastHibernate := e.findLastOccurrence(hibernateSched, localNow)
	lastWakeUp := e.findLastOccurrence(wakeUpSched, localNow)

	// Determine current state by comparing which event happened more recently
	shouldHibernate := lastHibernate.After(lastWakeUp)

	if e.scheduleBuffer > 0 {
		if !shouldHibernate && isInGraceTimeWindow(window.Windows, localNow, e.scheduleBuffer) {
			shouldHibernate = true
		} else if shouldHibernate && isInLateWindow(window.Windows, localNow, e.scheduleBuffer) {
			shouldHibernate = false
		}
	}

	state := "active"
	if shouldHibernate {
		state = "hibernated"
	}

	// Calculate next occurrences
	nextHibernate := hibernateSched.Next(localNow)
	nextWakeUp := wakeUpSched.Next(localNow)

	return &EvaluationResult{
		ShouldHibernate:   shouldHibernate,
		NextHibernateTime: nextHibernate,
		NextWakeUpTime:    nextWakeUp,
		CurrentState:      state,
	}, nil
}

// findLastOccurrence finds the most recent occurrence of a schedule before the given time.
// It works by stepping back in time and finding when the schedule would have last fired.
func (e *ScheduleEvaluator) findLastOccurrence(sched cron.Schedule, now time.Time) time.Time {
	// Start from 24 hours ago and find the next occurrence
	// Keep stepping forward until we pass 'now', then return the previous occurrence
	searchStart := now.Add(-24 * time.Hour)

	var lastOccurrence time.Time
	current := sched.Next(searchStart)

	for current.Before(now) || current.Equal(now) {
		lastOccurrence = current
		current = sched.Next(current)
	}

	// If we didn't find any occurrence in the last 24 hours, search further back
	if lastOccurrence.IsZero() {
		searchStart = now.Add(-7 * 24 * time.Hour)
		current = sched.Next(searchStart)
		for current.Before(now) || current.Equal(now) {
			lastOccurrence = current
			current = sched.Next(current)
		}
	}

	return lastOccurrence
}

// NextRequeueTime calculates when the controller should next check the schedule.
// Returns the earlier of: next hibernate time or next wake-up time.
func (e *ScheduleEvaluator) NextRequeueTime(result *EvaluationResult) time.Duration {
	now := e.Clock.Now()
	var nextEvent time.Time

	if result.ShouldHibernate {
		// Currently hibernated, next event is wake-up
		nextEvent = result.NextWakeUpTime
	} else {
		// Currently active, next event is hibernate
		nextEvent = result.NextHibernateTime
	}

	duration := nextEvent.Sub(now)
	if duration < 0 {
		duration = time.Minute // Safety: requeue in 1 minute if calculation is off
	}

	// Add a small buffer to ensure we're past the scheduled time
	return duration + e.scheduleBuffer + 10*time.Second
}

// Evaluate evaluates the schedule with an optional exception applied.
// Exception semantics:
// - extend: Union of base schedule + exception windows (more hibernation time)
// - suspend: Carve-out from base schedule (keep awake during exception windows)
// - replace: Use only exception windows, ignore base schedule entirely
func (e *ScheduleEvaluator) Evaluate(baseWindows []OffHourWindow, timezone string, exception *Exception) (*EvaluationResult, error) {
	// If no exception or exception is not active, evaluate base schedule only
	if exception == nil || !e.isExceptionActive(exception) {
		return e.evaluateWindows(baseWindows, timezone)
	}

	switch exception.Type {
	case ExceptionExtend:
		return e.evaluateExtend(baseWindows, exception.Windows, timezone)
	case ExceptionSuspend:
		return e.evaluateSuspend(baseWindows, exception, timezone)
	case ExceptionReplace:
		return e.evaluateWindows(exception.Windows, timezone)
	default:
		// Unknown exception type, fall back to base schedule
		return e.evaluateWindows(baseWindows, timezone)
	}
}

// isExceptionActive checks if the exception is currently within its valid period.
func (e *ScheduleEvaluator) isExceptionActive(exception *Exception) bool {
	now := e.Clock.Now()
	return !now.Before(exception.ValidFrom) && !now.After(exception.ValidUntil)
}

// evaluateWindows evaluates a set of OffHourWindows and returns the result.
func (e *ScheduleEvaluator) evaluateWindows(windows []OffHourWindow, timezone string) (*EvaluationResult, error) {
	if len(windows) == 0 {
		// No windows means no hibernation
		return &EvaluationResult{
			ShouldHibernate: false,
			CurrentState:    "active",
		}, nil
	}

	hibernateCron, wakeUpCron, err := ConvertOffHoursToCron(windows)
	if err != nil {
		return nil, fmt.Errorf("convert windows to cron: %w", err)
	}

	window := ScheduleWindow{
		Windows:       windows,
		HibernateCron: hibernateCron,
		WakeUpCron:    wakeUpCron,
		Timezone:      timezone,
	}

	return e.eval(window)
}

// evaluateExtend combines base windows with exception windows (union).
// This means hibernation occurs during BOTH base and exception windows.
func (e *ScheduleEvaluator) evaluateExtend(baseWindows, exceptionWindows []OffHourWindow, timezone string) (*EvaluationResult, error) {
	// Evaluate base schedule
	baseResult, err := e.evaluateWindows(baseWindows, timezone)
	if err != nil {
		return nil, fmt.Errorf("evaluate base windows: %w", err)
	}

	// Evaluate exception windows
	exceptionResult, err := e.evaluateWindows(exceptionWindows, timezone)
	if err != nil {
		return nil, fmt.Errorf("evaluate exception windows: %w", err)
	}

	// Hibernate when both schedule says hibernate
	shouldHibernate := baseResult.ShouldHibernate && exceptionResult.ShouldHibernate

	// Calculate next events (take the earlier of the two for each)
	nextHibernate := earlierTime(baseResult.NextHibernateTime, exceptionResult.NextHibernateTime)
	nextWakeUp := earlierTime(baseResult.NextWakeUpTime, exceptionResult.NextWakeUpTime)

	state := "active"
	if shouldHibernate {
		state = "hibernated"
	}

	return &EvaluationResult{
		ShouldHibernate:   shouldHibernate,
		NextHibernateTime: nextHibernate,
		NextWakeUpTime:    nextWakeUp,
		CurrentState:      state,
	}, nil
}

// evaluateSuspend evaluates schedule with suspension windows.
// Suspension PREVENTS hibernation during exception windows, even if base schedule says hibernate.
// Lead time prevents NEW hibernation starts within the buffer before suspension.
func (e *ScheduleEvaluator) evaluateSuspend(baseWindows []OffHourWindow, exception *Exception, timezone string) (*EvaluationResult, error) {
	now := e.Clock.Now()
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %s: %w", timezone, err)
	}

	localNow := now.In(loc)

	// First evaluate base schedule
	baseResult, err := e.evaluateWindows(baseWindows, timezone)
	if err != nil {
		return nil, fmt.Errorf("evaluate base windows: %w", err)
	}

	// Check if we're currently in a suspension window
	inSuspensionWindow := isInTimeWindows(exception.Windows, localNow)

	// Check if we're in lead time window (before suspension starts)
	inLeadTimeWindow := false
	if exception.LeadTime > 0 {
		inLeadTimeWindow = isInLeadTimeWindow(exception.Windows, localNow, exception.LeadTime)
	}

	// Determine final hibernation state
	// - If in suspension window: DON'T hibernate (override base)
	// - If in lead time window AND base says start hibernating: DON'T start (but ongoing hibernation continues)
	// - Otherwise: follow base schedule
	shouldHibernate := baseResult.ShouldHibernate

	if inSuspensionWindow {
		// Suspension active - keep awake regardless of base schedule
		shouldHibernate = false
	} else if inLeadTimeWindow && !baseResult.ShouldHibernate {
		// In lead time window but not currently hibernated
		// Don't start hibernation (will wait until after suspension)
		// Note: If already hibernated, continue hibernating until suspension window starts
	} else if inLeadTimeWindow && baseResult.ShouldHibernate {
		// In lead time window and base says hibernate
		// This is ambiguous - we need to check if hibernation just started or was already ongoing
		// For simplicity, we'll prevent hibernation during lead time
		// (In a more sophisticated implementation, we'd track hibernation start time)
		shouldHibernate = false
	}

	state := "active"
	if shouldHibernate {
		state = "hibernated"
	}

	// Calculate next events considering suspension
	nextHibernate := baseResult.NextHibernateTime
	nextWakeUp := baseResult.NextWakeUpTime

	// If in suspension or lead time, we may need to adjust next hibernate time
	if inSuspensionWindow || inLeadTimeWindow {
		// Find when suspension ends to recalculate
		suspensionEnd := e.findSuspensionEnd(exception.Windows, localNow)
		if !suspensionEnd.IsZero() && suspensionEnd.After(localNow) {
			// Schedule check after suspension ends
			if suspensionEnd.Before(nextHibernate) || nextHibernate.IsZero() {
				nextHibernate = suspensionEnd
			}
		}
	}

	return &EvaluationResult{
		ShouldHibernate:   shouldHibernate,
		NextHibernateTime: nextHibernate,
		NextWakeUpTime:    nextWakeUp,
		CurrentState:      state,
	}, nil
}

// findSuspensionEnd finds when the current or upcoming suspension window ends.
func (e *ScheduleEvaluator) findSuspensionEnd(windows []OffHourWindow, now time.Time) time.Time {
	currentDay := strings.ToUpper(now.Weekday().String()[:3])
	currentHour := now.Hour()
	currentMin := now.Minute()
	currentTimeMinutes := currentHour*60 + currentMin

	for _, w := range windows {
		// Check if today is in the window's days
		dayMatch := false
		for _, day := range w.DaysOfWeek {
			if strings.EqualFold(day, currentDay) {
				dayMatch = true
				break
			}
		}
		if !dayMatch {
			continue
		}

		// Parse window times
		startHour, startMin, err := parseTime(w.Start)
		if err != nil {
			continue
		}
		endHour, endMin, err := parseTime(w.End)
		if err != nil {
			continue
		}

		startMinutes := startHour*60 + startMin
		endMinutes := endHour*60 + endMin

		// Check if we're currently in this window
		inWindow := false
		if endMinutes > startMinutes {
			// Same-day window
			inWindow = currentTimeMinutes >= startMinutes && currentTimeMinutes < endMinutes
		} else {
			// Overnight window
			inWindow = currentTimeMinutes >= startMinutes || currentTimeMinutes < endMinutes
		}

		if inWindow {
			// Calculate when this window ends
			endTime := time.Date(now.Year(), now.Month(), now.Day(), endHour, endMin, 0, 0, now.Location())
			if endMinutes <= startMinutes && currentTimeMinutes >= startMinutes {
				// Overnight window, end is tomorrow
				endTime = endTime.Add(24 * time.Hour)
			}
			return endTime
		}
	}

	return time.Time{}
}

// ValidateCron validates a cron expression.
func (e *ScheduleEvaluator) ValidateCron(expr string) error {
	_, err := e.parser.Parse(expr)
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return nil
}

// earlierTime returns the earlier of two times, ignoring zero times.
func earlierTime(a, b time.Time) time.Time {
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
