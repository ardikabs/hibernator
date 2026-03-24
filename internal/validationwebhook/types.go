/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/samber/lo"
)

// windowsCollide returns true if any window in set A collides with any window in
// set B. Two windows collide when they share at least one day AND their time ranges
// overlap (handling overnight wraparound).
func windowsCollide(a, b []hibernatorv1alpha1.OffHourWindow) bool {
	// lo.SomeBy lets us easily check if any combination returns true
	return lo.SomeBy(a, func(wa hibernatorv1alpha1.OffHourWindow) bool {
		return lo.SomeBy(b, func(wb hibernatorv1alpha1.OffHourWindow) bool {
			return windowPairCollides(wa, wb)
		})
	})
}

// windowPairCollides checks whether two individual windows share a day and have
// overlapping time ranges.
func windowPairCollides(a, b hibernatorv1alpha1.OffHourWindow) bool {
	// lo.Some returns true if slice A and slice B have at least one intersecting element.
	// This replaces the entire nested day-checking loop!
	if !lo.Some(a.DaysOfWeek, b.DaysOfWeek) {
		return false
	}

	// Check time range overlap (handling overnight wraparound).
	return timeRangesOverlap(a.Start, a.End, b.Start, b.End)
}

type timeSpan struct {
	start int
	end   int
}

// timeRangesOverlap checks whether two HH:MM time ranges overlap, correctly
// handling overnight (backward) windows where start > end (e.g., 20:00–06:00).
//
// Overnight windows are expanded into two segments: [start, 24:00) and [00:00, end).
// Two ranges overlap if any of their segments overlap.
func timeRangesOverlap(startA, endA, startB, endB string) bool {
	aSegments := expandTimeRange(startA, endA)
	bSegments := expandTimeRange(startB, endB)

	// Check if any segment in A overlaps with any segment in B
	return lo.SomeBy(aSegments, func(sa timeSpan) bool {
		return lo.SomeBy(bSegments, func(sb timeSpan) bool {
			return sa.start < sb.end && sb.start < sa.end
		})
	})
}

// expandTimeRange converts a HH:MM–HH:MM range into one or two [startMin, endMin)
// segments in minutes-since-midnight. An overnight window (start >= end) is split
// into [start, 1440) and [0, end).
func expandTimeRange(start, end string) []timeSpan {
	s := hhmmToMinutes(start)
	e := hhmmToMinutes(end)

	if s < e {
		return []timeSpan{{start: s, end: e}}
	}
	// Overnight wraparound: split into two segments.
	// s == e is treated as a zero-width window (no collision).
	if s == e {
		return nil
	}
	return []timeSpan{{start: s, end: 1440}, {start: 0, end: e}}
}

// hhmmToMinutes parses "HH:MM" into minutes since midnight.
// Returns 0 on parse error (caller is expected to pre-validate via validateWindows).
func hhmmToMinutes(t string) int {
	parsed, err := time.Parse("15:04", t)
	if err != nil {
		return 0
	}

	return parsed.Hour()*60 + parsed.Minute()
}

// isAllowedTypePair returns true when two different exception types are allowed to
// coexist even if their windows collide. Allowed pairs:
//   - replace + extend  (replace acts as new base, extend adds on top)
//   - replace + suspend (replace acts as new base, suspend carves out)
//   - extend  + suspend (orthogonal intents)
func isAllowedTypePair(a, b hibernatorv1alpha1.ExceptionType) bool {
	pair := [2]hibernatorv1alpha1.ExceptionType{a, b}
	switch pair {
	case [2]hibernatorv1alpha1.ExceptionType{hibernatorv1alpha1.ExceptionExtend, hibernatorv1alpha1.ExceptionSuspend},
		[2]hibernatorv1alpha1.ExceptionType{hibernatorv1alpha1.ExceptionSuspend, hibernatorv1alpha1.ExceptionExtend},
		[2]hibernatorv1alpha1.ExceptionType{hibernatorv1alpha1.ExceptionReplace, hibernatorv1alpha1.ExceptionExtend},
		[2]hibernatorv1alpha1.ExceptionType{hibernatorv1alpha1.ExceptionExtend, hibernatorv1alpha1.ExceptionReplace},
		[2]hibernatorv1alpha1.ExceptionType{hibernatorv1alpha1.ExceptionReplace, hibernatorv1alpha1.ExceptionSuspend},
		[2]hibernatorv1alpha1.ExceptionType{hibernatorv1alpha1.ExceptionSuspend, hibernatorv1alpha1.ExceptionReplace}:
		return true
	default:
		return false
	}
}
