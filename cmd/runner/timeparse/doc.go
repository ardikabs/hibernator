/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package timeparse provides user-friendly deadline parsing for CLI commands.
//
// This package supports parsing human-readable time expressions into time.Time values.
// All parsing is done relative to a provided "now" time, making it suitable for testing
// with fake clocks.
//
// Supported formats:
//
// Tier 1 - Natural Language:
//   - Relative: "in 30 minutes", "in 2 hours", "in 1 day"
//   - Tomorrow: "tomorrow", "tomorrow at 6am", "tomorrow at 14:30"
//   - Weekdays: "next Monday", "next week"
//
// Tier 2 - Simple Date/Time (local timezone):
//   - Date only: "2026-01-15", "Jan 15, 2026"
//   - Date + Time: "2026-01-15 14:30", "Jan 15, 2026 2:30pm"
//
// Tier 3 - RFC3339:
//   - "2026-01-15T14:30:00Z" (UTC)
//   - "2026-01-15T14:30:00+07:00" (with offset)
//
// Usage:
//
//	// Parse relative to current time
//	deadline, err := timeparse.ParseDeadline("in 30 minutes", time.Now())
//	
//	// Parse relative to a specific time (useful for testing)
//	fakeNow := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
//	deadline, err := timeparse.ParseDeadline("tomorrow at 6am", fakeNow)
//
// All times are parsed in the local timezone and converted to UTC for internal storage.
package timeparse
