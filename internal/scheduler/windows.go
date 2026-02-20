/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package scheduler implements execution planning for HibernatePlan.
package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ScheduleWindow represents a hibernation window.
type ScheduleWindow struct {
	// Windows are the actual off-hour windows (for reference).
	Windows []OffHourWindow

	// HibernateCron is the cron expression for when to start hibernation.
	HibernateCron string

	// WakeUpCron is the cron expression for when to wake up.
	WakeUpCron string

	// Timezone is the timezone for schedule evaluation.
	Timezone string
}

// isInTimeWindows checks if the current time falls within any of the time windows.
func isInTimeWindows(windows []OffHourWindow, now time.Time) bool {
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

		// Check if current time is within the window
		if endMinutes > startMinutes {
			// Same-day window (e.g., 09:00 to 17:00)
			if currentTimeMinutes >= startMinutes && currentTimeMinutes < endMinutes {
				return true
			}
		} else {
			// Overnight window (e.g., 20:00 to 06:00)
			// Current time is in window if: after start OR before end
			if currentTimeMinutes >= startMinutes || currentTimeMinutes < endMinutes {
				return true
			}
		}
	}

	return false
}

// isInLeadTimeWindow checks if we're within lead time before any hibernation window.
// Primarily used for suspend exceptions, which measure the given start window within leading time from given time.
// E.g., for a window 20:00-06:00 (base schedule) with 60-minute lead time, for a suspend exception from 21:00 - 23:59,
// at 20:00 - 21:00 it prevents a hibernation to kick in
func isInLeadTimeWindow(windows []OffHourWindow, now time.Time, leadTime time.Duration) bool {
	currentDay := strings.ToUpper(now.Weekday().String()[:3])
	currentHour := now.Hour()
	currentMin := now.Minute()
	currentTimeMinutes := currentHour*60 + currentMin
	leadTimeMinutes := int(leadTime.Minutes())

	for _, w := range windows {
		// Check if today is in the window's days
		dayMatch := false
		checkDays := []string{currentDay}

		if currentHour > 12 {
			nextDay := now.Add(24 * time.Hour).Weekday()
			checkDays = append(checkDays, strings.ToUpper(nextDay.String()[:3]))
		}

		for _, checkDay := range checkDays {
			for _, day := range w.DaysOfWeek {
				if strings.EqualFold(day, checkDay) {
					dayMatch = true
					break
				}
			}
			if dayMatch {
				break
			}
		}

		if !dayMatch {
			continue
		}

		// Parse window start time
		startHour, startMin, err := parseTime(w.Start)
		if err != nil {
			continue
		}

		startMinutes := startHour*60 + startMin
		leadStartMinutes := startMinutes - leadTimeMinutes

		// Handle wrap-around for lead time crossing midnight
		if leadStartMinutes < 0 {
			leadStartMinutes += 24 * 60
		}

		// Check if current time is in lead time window (before start, within lead time)
		if leadStartMinutes < startMinutes {
			// Normal case: lead time window doesn't cross midnight
			if currentTimeMinutes >= leadStartMinutes && currentTimeMinutes < startMinutes {
				return true
			}
		} else {
			// Lead time crosses midnight
			if currentTimeMinutes >= leadStartMinutes || currentTimeMinutes < startMinutes {
				return true
			}
		}
	}

	return false
}

type WindowBoundary int

const (
	StartBoundary WindowBoundary = iota
	EndBoundary
)

// isInGraceTimeWindow determines whether the current time falls within
// the grace period immediately after a window boundary.
//
// This is primarily used for full-day hibernation windows to prevent
// unintended wake-up actions at the exact boundary of the window.
//
// For example, with a window of 00:00–23:59 and a 1-minute grace period,
// the time range 23:59:00–23:59:59 is still considered within the grace
// window, preventing a wake-up operation from being triggered.
func isInGraceTimeWindow(boundary WindowBoundary, windows []OffHourWindow, now time.Time, grace time.Duration) bool {
	for _, w := range windows {
		for _, day := range w.DaysOfWeek {
			targetDay, ok := parseWeekday(day)
			if !ok {
				continue
			}

			// Find the correct base date for this window
			// We anchor using the most recent occurrence,
			// but adjust carefully for overnight windows.
			baseDate := mostRecentWeekday(now, targetDay)

			// If this is a shutdown window,
			// measured from the start time
			switch boundary {
			case StartBoundary:
				startHour, startMin, err := parseTime(w.Start)
				if err != nil {
					continue
				}

				startTime := time.Date(
					baseDate.Year(), baseDate.Month(), baseDate.Day(),
					startHour, startMin, 0, 0, now.Location(),
				)

				startGraceEnd := startTime.Add(grace)

				// Valid cases:
				// 1️⃣ Start grace (allow midnight crossing)
				if (now.After(startTime) || now.Equal(startTime)) &&
					(now.Before(startGraceEnd) || now.Equal(startGraceEnd)) {
					return true
				}

			case EndBoundary:
				endHour, endMin, err := parseTime(w.End)
				if err != nil {
					continue
				}

				endTime := time.Date(
					baseDate.Year(), baseDate.Month(), baseDate.Day(),
					endHour, endMin, 0, 0, now.Location(),
				)

				endGraceEnd := endTime.Add(grace)

				// 2️⃣ End grace (STRICT: must still be same weekday)
				if now.Weekday() == targetDay &&
					(now.After(endTime) || now.Equal(endTime)) &&
					(now.Before(endGraceEnd) || now.Equal(endGraceEnd)) {
					return true
				}
			}
		}
	}

	return false
}

func parseWeekday(day string) (time.Weekday, bool) {
	switch strings.ToUpper(day[:3]) {
	case "SUN":
		return time.Sunday, true
	case "MON":
		return time.Monday, true
	case "TUE":
		return time.Tuesday, true
	case "WED":
		return time.Wednesday, true
	case "THU":
		return time.Thursday, true
	case "FRI":
		return time.Friday, true
	case "SAT":
		return time.Saturday, true
	default:
		return 0, false
	}
}

func mostRecentWeekday(now time.Time, target time.Weekday) time.Time {
	diff := (int(now.Weekday()) - int(target) + 7) % 7
	return now.AddDate(0, 0, -diff)
}

// OffHourWindow represents a user-friendly time window for hibernation.
type OffHourWindow struct {
	Start      string   // HH:MM format (e.g., "20:00")
	End        string   // HH:MM format (e.g., "06:00")
	DaysOfWeek []string // MON, TUE, WED, THU, FRI, SAT, SUN
}

// ConvertOffHoursToCron converts OffHourWindow format to cron expressions.
// Returns hibernateCron and wakeUpCron.
// For overnight windows (where end time is before start time, e.g., 20:00 to 06:00),
// the wake-up cron uses the next day's schedule.
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

	// Validate that start and end times are different
	// A hibernation window must define a clear shutdown (start) and wakeup (end) period
	if startHour == endHour && startMin == endMin {
		return "", "", fmt.Errorf("start and end times must be different; start=%s, end=%s", window.Start, window.End)
	}

	// Convert day names to cron dow format (0-6, SUN=0)
	cronDays, err := convertDaysToCron(window.DaysOfWeek)
	if err != nil {
		return "", "", err
	}

	// Check if this is an overnight window (end time is before start time)
	// This is used for logic validation if needed, but cron generation now uses same days
	// isOvernight := endHour < startHour || (endHour == startHour && endMin < startMin)

	// Build cron expressions
	// Format: MIN HOUR DAY MONTH DOW
	hibernateCron := fmt.Sprintf("%d %d * * %s", startMin, startHour, cronDays)
	wakeUpCron := fmt.Sprintf("%d %d * * %s", endMin, endHour, cronDays)

	return hibernateCron, wakeUpCron, nil
}

// parseTime parses HH:MM format into hour and minute.
func parseTime(timeStr string) (hour, min int, err error) {
	parts := strings.Split(timeStr, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time format %q, expected HH:MM", timeStr)
	}

	hour, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hour in %q: %w", timeStr, err)
	}
	min, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minute in %q: %w", timeStr, err)
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
	if len(days) == 0 {
		return "", fmt.Errorf("at least one day of week is required")
	}

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
