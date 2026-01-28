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
)

// ScheduleEvaluator evaluates cron-based schedules to determine hibernation state.
type ScheduleEvaluator struct {
	parser cron.Parser
}

// NewScheduleEvaluator creates a new schedule evaluator.
func NewScheduleEvaluator() *ScheduleEvaluator {
	return &ScheduleEvaluator{
		// Use standard cron format with optional seconds
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

// ScheduleWindow represents a hibernation window.
type ScheduleWindow struct {
	// HibernateCron is the cron expression for when to start hibernation.
	HibernateCron string

	// WakeUpCron is the cron expression for when to wake up.
	WakeUpCron string

	// Timezone is the timezone for schedule evaluation.
	Timezone string
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

// Evaluate determines if we should be in hibernation based on the schedule.
// It compares the last hibernate and wake-up times to determine current state.
func (e *ScheduleEvaluator) Evaluate(window ScheduleWindow, now time.Time) (*EvaluationResult, error) {
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

	// Calculate next occurrences
	nextHibernate := hibernateSched.Next(localNow)
	nextWakeUp := wakeUpSched.Next(localNow)

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
func (e *ScheduleEvaluator) NextRequeueTime(result *EvaluationResult, now time.Time) time.Duration {
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
	return duration + 10*time.Second
}

// ValidateCron validates a cron expression.
func (e *ScheduleEvaluator) ValidateCron(expr string) error {
	_, err := e.parser.Parse(expr)
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return nil
}

// OffHourWindow represents a user-friendly time window for hibernation.
type OffHourWindow struct {
	Start      string   // HH:MM format (e.g., "20:00")
	End        string   // HH:MM format (e.g., "06:00")
	DaysOfWeek []string // MON, TUE, WED, THU, FRI, SAT, SUN
}

// ConvertOffHoursToCron converts OffHourWindow format to cron expressions.
// Returns hibernateCron and wakeUpCron.
func ConvertOffHoursToCron(windows []OffHourWindow) (string, string, error) {
	if len(windows) == 0 {
		return "", "", fmt.Errorf("at least one off-hour window is required")
	}

	// For simplicity, we'll use the first window
	// TODO: Support multiple windows by generating multiple cron expressions or finding common patterns
	window := windows[0]

	// Parse start time (HH:MM)
	startHour, startMin, err := parseTime(window.Start)
	if err != nil {
		return "", "", fmt.Errorf("invalid start time: %w", err)
	}

	// Parse end time (HH:MM)
	endHour, endMin, err := parseTime(window.End)
	if err != nil {
		return "", "", fmt.Errorf("invalid end time: %w", err)
	}

	// Convert day names to cron dow format (0-6, SUN=0)
	cronDays, err := convertDaysToCron(window.DaysOfWeek)
	if err != nil {
		return "", "", err
	}

	// Build cron expressions
	// Format: MIN HOUR DAY MONTH DOW
	hibernateCron := fmt.Sprintf("%d %d * * %s", startMin, startHour, cronDays)
	wakeUpCron := fmt.Sprintf("%d %d * * %s", endMin, endHour, cronDays)

	return hibernateCron, wakeUpCron, nil
}

// parseTime parses HH:MM format into hour and minute.
func parseTime(timeStr string) (hour, min int, err error) {
	_, err = fmt.Sscanf(timeStr, "%d:%d", &hour, &min)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid time format %q, expected HH:MM", timeStr)
	}
	if hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("hour %d out of range (0-23)", hour)
	}
	if min < 0 || min > 59 {
		return 0, 0, fmt.Errorf("minute %d out of range (0-59)", min)
	}
	return hour, min, nil
}

// convertDaysToCron converts day names (MON, TUE, etc.) to cron day-of-week format.
// Returns a comma-separated string like "1,2,3,4,5" for MON-FRI.
func convertDaysToCron(days []string) (string, error) {
	dayMap := map[string]int{
		"SUN": 0,
		"MON": 1,
		"TUE": 2,
		"WED": 3,
		"THU": 4,
		"FRI": 5,
		"SAT": 6,
	}

	var cronDays []string
	for _, day := range days {
		dayUpper := strings.ToUpper(day)
		cronDay, ok := dayMap[dayUpper]
		if !ok {
			return "", fmt.Errorf("invalid day %q, expected MON, TUE, WED, THU, FRI, SAT, or SUN", day)
		}
		cronDays = append(cronDays, fmt.Sprintf("%d", cronDay))
	}

	return strings.Join(cronDays, ","), nil
}
