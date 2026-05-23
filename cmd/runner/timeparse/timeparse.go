/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package timeparse provides user-friendly deadline parsing for CLI commands.
// It supports multiple input formats in a tiered fallback approach:
//
// Tier 1: Natural language relative times
//   - "in 30 minutes", "in 2 hours", "in 1 day"
//   - "tomorrow", "tomorrow at 6am", "tomorrow at 14:30"
//
// Tier 2: Simple date/time formats (local timezone)
//   - "2026-01-15"                    (date only, midnight)
//   - "2026-01-15 14:30"              (date + time, 24h format)
//   - "2026-01-15 14:30:00"           (with seconds)
//   - "Jan 15, 2026"                  (readable date)
//   - "Jan 15, 2026 14:30"            (readable date + time)
//
// Tier 3: Standard RFC3339
//   - "2026-01-15T14:30:00Z"          (UTC)
//   - "2026-01-15T14:30:00+07:00"     (with offset)
//
// All times are parsed in the user's local timezone and converted to UTC
// for internal storage. Output is always time.Time in UTC.
package timeparse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tj/go-naturaldate"
)

// ParseDeadline parses a user-provided deadline string into a UTC time.Time.
// It attempts parsing in three tiers: natural language, simple formats, then RFC3339.
// The 'now' parameter is used as the reference time for relative expressions like "in 30 minutes"
// or "tomorrow". This allows for deterministic parsing in tests using fake clocks.
// Returns an error if the input cannot be parsed or if the resulting time is in the past.
//
// Example:
//
//	// Parse relative to current time
//	deadline, err := ParseDeadline("in 30 minutes", time.Now())
//
//	// Parse relative to a specific time (useful for testing)
//	fakeNow := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
//	deadline, err := ParseDeadline("tomorrow at 6am", fakeNow)
func ParseDeadline(input string, now time.Time) (time.Time, error) {
	if input == "" {
		return time.Time{}, fmt.Errorf("empty input")
	}

	// Clean up input
	input = strings.TrimSpace(input)

	// Try each tier in order
	if t, ok := parseNaturalLanguage(input, now); ok {
		return validateDeadline(t, now)
	}

	if t, ok := parseSimpleFormats(input, now); ok {
		return validateDeadline(t, now)
	}

	if t, ok := parseRFC3339(input); ok {
		return validateDeadline(t, now)
	}

	return time.Time{}, fmt.Errorf(
		"unrecognized time format: %q\n\n"+
			"Supported formats:\n"+
			"  Relative:    'in 30 minutes', 'in 2 hours', 'tomorrow at 6am'\n"+
			"  Date:        '2026-01-15', 'Jan 15, 2026'\n"+
			"  Date+Time:   '2026-01-15 14:30', 'Jan 15, 2026 14:30'\n"+
			"  RFC3339:     '2026-01-15T14:30:00Z'",
		input,
	)
}

// parseNaturalLanguage handles relative time expressions.
// Returns (time, true) if parsed successfully, (zero, false) if not applicable.
func parseNaturalLanguage(input string, now time.Time) (time.Time, bool) {
	input = strings.ToLower(strings.TrimSpace(input))

	// Handle "in X minutes/hours/days"
	if strings.HasPrefix(input, "in ") {
		return parseInDuration(input, now)
	}

	// Handle "tomorrow" variants
	if strings.HasPrefix(input, "tomorrow") {
		return parseTomorrow(input, now)
	}

	// Handle "next " (next Monday, next week, etc.)
	if strings.HasPrefix(input, "next ") {
		return parseNext(input, now)
	}

	if result, err := naturaldate.Parse(input, now, naturaldate.WithDirection(naturaldate.Future)); err == nil {
		return result, true
	}

	return time.Time{}, false
}

// parseInDuration handles "in 30 minutes", "in 2 hours", etc.
func parseInDuration(input string, now time.Time) (time.Time, bool) {
	// Pattern: in <number> <unit>
	re := regexp.MustCompile(`^in\s+(\d+(?:\.\d+)?)\s*(minute|minutes|min|mins|hour|hours|hr|hrs|day|days|week|weeks)?s?$`)
	matches := re.FindStringSubmatch(input)
	if matches == nil {
		return time.Time{}, false
	}

	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return time.Time{}, false
	}

	unit := matches[2]
	if unit == "" {
		// Default to minutes if no unit specified
		unit = "minute"
	}

	var duration time.Duration
	switch {
	case strings.HasPrefix(unit, "minute"), unit == "min", unit == "mins":
		duration = time.Duration(value * float64(time.Minute))
	case strings.HasPrefix(unit, "hour"), unit == "hr", unit == "hrs":
		duration = time.Duration(value * float64(time.Hour))
	case strings.HasPrefix(unit, "day"):
		duration = time.Duration(value * float64(24*time.Hour))
	case strings.HasPrefix(unit, "week"):
		duration = time.Duration(value * float64(7*24*time.Hour))
	default:
		return time.Time{}, false
	}

	return now.Add(duration).UTC(), true
}

// parseTomorrow handles "tomorrow", "tomorrow at 6am", "tomorrow at 14:30", etc.
func parseTomorrow(input string, now time.Time) (time.Time, bool) {
	// Get tomorrow's date
	tomorrow := now.Add(24 * time.Hour)

	// Check if there's a time specified
	if input == "tomorrow" {
		// Tomorrow at current time
		return tomorrow.UTC(), true
	}

	// Try to parse "tomorrow at <time>" or "tomorrow <time>"
	timePart := strings.TrimPrefix(input, "tomorrow at ")
	if timePart == input {
		timePart = strings.TrimPrefix(input, "tomorrow ")
	}
	timePart = strings.TrimSpace(timePart)

	if timePart == "" {
		// Tomorrow at current time
		return tomorrow.UTC(), true
	}

	// Parse the time part
	timeOfDay, ok := parseTimeOfDay(timePart)
	if !ok {
		return time.Time{}, false
	}

	result := time.Date(
		tomorrow.Year(), tomorrow.Month(), tomorrow.Day(),
		timeOfDay.Hour(), timeOfDay.Minute(), timeOfDay.Second(), 0,
		now.Location(),
	)

	return result.UTC(), true
}

// parseNext handles "next Monday", "next week", etc.
func parseNext(input string, now time.Time) (time.Time, bool) {
	input = strings.TrimPrefix(input, "next ")
	input = strings.TrimSpace(input)

	// Handle "next week"
	if input == "week" {
		// Next week from now
		return now.Add(7 * 24 * time.Hour).UTC(), true
	}

	// Handle weekdays: "next monday", "next tuesday", etc.
	weekdays := map[string]time.Weekday{
		"sunday":    time.Sunday,
		"monday":    time.Monday,
		"tuesday":   time.Tuesday,
		"wednesday": time.Wednesday,
		"thursday":  time.Thursday,
		"friday":    time.Friday,
		"saturday":  time.Saturday,
	}

	targetDay, ok := weekdays[input]
	if !ok {
		return time.Time{}, false
	}

	// Calculate days until next occurrence
	daysUntil := int(targetDay - now.Weekday())
	if daysUntil <= 0 {
		daysUntil += 7
	}

	result := now.Add(time.Duration(daysUntil) * 24 * time.Hour)
	return result.UTC(), true
}

// parseTimeOfDay parses time strings like "6am", "6:30am", "14:30", "14:30:00".
func parseTimeOfDay(input string) (time.Time, bool) {
	input = strings.TrimSpace(input)

	// Go's reference time: Mon Jan 2 15:04:05 MST 2006
	// Use reference date for parsing
	refDate := "2006-01-02 "

	// Try 24-hour formats first
	layouts24 := []string{
		"15:04:05",
		"15:04",
	}

	for _, layout := range layouts24 {
		if t, err := time.Parse(refDate+layout, refDate+input); err == nil {
			return t, true
		}
	}

	// Try 12-hour formats with AM/PM
	// Handle both "6am" and "6:30pm" formats
	inputLower := strings.ToLower(strings.TrimSpace(input))

	// Check if input has AM/PM marker
	if !strings.Contains(inputLower, "am") && !strings.Contains(inputLower, "pm") {
		return time.Time{}, false
	}

	// Convert to uppercase for parsing
	inputUpper := strings.ToUpper(input)

	// Try various 12-hour layouts
	layouts12 := []string{
		"3:04:05PM",
		"3:04PM",
		"3PM",
	}

	for _, layout := range layouts12 {
		if t, err := time.Parse(refDate+layout, refDate+inputUpper); err == nil {
			return t, true
		}
	}

	return time.Time{}, false
}

// parseSimpleFormats handles simple date/time formats without timezone info.
// These are interpreted in the user's local timezone.
// The now parameter provides the reference date for time-only inputs.
func parseSimpleFormats(input string, now time.Time) (time.Time, bool) {
	input = strings.TrimSpace(input)

	// Try various date/time layouts
	layouts := []string{
		// ISO-style with space separator
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",

		// Readable formats
		"Jan 2, 2006 15:04:05",
		"Jan 2, 2006 15:04",
		"Jan 2, 2006",
		"January 2, 2006 15:04:05",
		"January 2, 2006 15:04",
		"January 2, 2006",

		// Short formats
		"Jan 2 15:04:05",
		"Jan 2 15:04",
		"Jan 2",

		// Slash-separated (US style)
		"01/02/2006 15:04:05",
		"01/02/2006 15:04",
		"01/02/2006",
	}

	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, input, time.Local); err == nil {
			return t.UTC(), true
		}
	}

	// Try time-only formats (assumes reference date from now)
	if t, ok := parseTimeOfDay(input); ok {
		result := time.Date(
			now.Year(), now.Month(), now.Day(),
			t.Hour(), t.Minute(), t.Second(), 0,
			time.Local,
		)
		return result.UTC(), true
	}

	return time.Time{}, false
}

// parseRFC3339 handles standard RFC3339 format.
func parseRFC3339(input string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, input); err == nil {
		return t.UTC(), true
	}

	// Try RFC3339 without timezone (assume UTC)
	if t, err := time.Parse("2006-01-02T15:04:05", input); err == nil {
		return t.UTC(), true
	}

	return time.Time{}, false
}

// validateDeadline ensures the deadline is in the future relative to now.
func validateDeadline(t, now time.Time) (time.Time, error) {
	if t.IsZero() {
		return time.Time{}, fmt.Errorf("parsed time is zero")
	}

	// Ensure UTC
	t = t.UTC()

	// Check if deadline is in the past
	if t.Before(now.UTC()) {
		return time.Time{}, fmt.Errorf("deadline %s is in the past", t.Format(time.RFC3339))
	}

	return t, nil
}

// FormatDeadline formats a UTC time for display to the user.
// Shows both local time and UTC.
func FormatDeadline(t time.Time) string {
	if t.IsZero() {
		return "no deadline"
	}

	local := t.Local()
	utc := t.UTC()

	// If local and UTC are the same day, show compact format
	if local.Year() == utc.Year() && local.YearDay() == utc.YearDay() {
		return fmt.Sprintf("%s local (%s UTC)",
			local.Format("Jan 2, 2006 15:04 MST"),
			utc.Format("15:04"),
		)
	}

	// Different days, show full format
	return fmt.Sprintf("%s local (%s UTC)",
		local.Format("Jan 2, 2006 15:04 MST"),
		utc.Format("Jan 2, 2006 15:04"),
	)
}

// FormatDuration formats the duration from 'from' until 'until' in a human-readable way.
func FormatDuration(from, until time.Time) string {
	if until.IsZero() {
		return ""
	}

	d := until.Sub(from)
	if d < 0 {
		return "overdue"
	}

	if d < time.Minute {
		return "less than a minute"
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		if minutes == 0 {
			if hours == 1 {
				return "1 hour"
			}
			return fmt.Sprintf("%d hours", hours)
		}
		return fmt.Sprintf("%d hours %d minutes", hours, minutes)
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if hours == 0 {
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%d days %d hours", days, hours)
}
