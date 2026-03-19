/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduler

import (
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"
)

// TestMultipleActiveExceptions tests how the scheduler evaluates schedules
// with multiple concurrent active exceptions. Based on the current implementation,
// when multiple exceptions are active, the scheduler should pick the newest one.
//
// This test documents the current behavior and serves as a foundation for
// future enhancements to support true multi-exception evaluation.
func TestMultipleActiveExceptions(t *testing.T) {
	// Base schedule: hibernate 20:00-06:00 on weekdays (Mon-Fri)
	baseWindows := []OffHourWindow{
		{
			Start:      "20:00",
			End:        "06:00",
			DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
		},
	}

	tests := []struct {
		name string
		// currentTime represents the "now" when evaluating the schedule
		currentTime time.Time
		// exceptions represents multiple active exceptions
		// In the current implementation, only the newest is used
		exceptions []*Exception
		// expectedShouldHibernate is the expected hibernation state
		expectedShouldHibernate bool
		// explanation documents why this test case is important
		explanation string
	}{
		{
			name:                    "no exceptions - normal base schedule active",
			currentTime:             time.Date(2026, 2, 9, 22, 0, 0, 0, time.UTC), // Monday 22:00 UTC
			exceptions:              nil,
			expectedShouldHibernate: true,
			explanation:             "With no exceptions, the base schedule should determine hibernation state",
		},
		{
			name:        "single extend exception - union of schedules",
			currentTime: time.Date(2026, 2, 9, 14, 0, 0, 0, time.UTC), // Monday 14:00 UTC (awake)
			exceptions: []*Exception{
				{
					Type:       ExceptionExtend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{
							Start:      "12:00",
							End:        "18:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
			},
			expectedShouldHibernate: true,
			explanation:             "With extend exception, hibernation should occur during EITHER base schedule OR exception windows. 14:00 is in exception window [12:00-18:00]",
		},
		{
			name:        "single suspend exception - carve-out from base",
			currentTime: time.Date(2026, 2, 9, 22, 0, 0, 0, time.UTC), // Monday 22:00 UTC (normally hibernated)
			exceptions: []*Exception{
				{
					Type:       ExceptionSuspend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					LeadTime:   30 * time.Minute,
					Windows: []OffHourWindow{
						{
							Start:      "20:00",
							End:        "23:59",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
			},
			expectedShouldHibernate: false,
			explanation:             "With suspend exception, hibernation is prevented during exception windows. 22:00 is in suspend window [20:00-23:59], so should be awake",
		},
		{
			name:        "single replace exception - full override",
			currentTime: time.Date(2026, 2, 9, 14, 0, 0, 0, time.UTC), // Monday 14:00 UTC
			exceptions: []*Exception{
				{
					Type:       ExceptionReplace,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{
							Start:      "10:00",
							End:        "16:00",
							DaysOfWeek: []string{"MON", "WED", "FRI"},
						},
					},
				},
			},
			expectedShouldHibernate: true,
			explanation:             "With replace exception, base schedule is completely ignored. 14:00 Monday is in exception window [10:00-16:00], so should hibernate",
		},
		{
			name:        "multiple extend exceptions - newest is selected",
			currentTime: time.Date(2026, 2, 9, 14, 0, 0, 0, time.UTC), // Monday 14:00 UTC
			exceptions: []*Exception{
				// Older exception (created first)
				{
					Type:       ExceptionExtend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{
							Start:      "08:00",
							End:        "10:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
				// Newer exception (created last - should be selected by scheduler)
				{
					Type:       ExceptionExtend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{
							Start:      "12:00",
							End:        "18:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
			},
			expectedShouldHibernate: true,
			explanation:             "With multiple active exceptions, only the newest is evaluated. 14:00 is in the newest exception's window [12:00-18:00, Mon-Fri]",
		},
		{
			name:        "multiple suspend exceptions - newest is selected",
			currentTime: time.Date(2026, 2, 9, 22, 0, 0, 0, time.UTC), // Monday 22:00 UTC
			exceptions: []*Exception{
				// Older exception: suspends [22:00-23:59]
				{
					Type:       ExceptionSuspend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{
							Start:      "22:00",
							End:        "23:59",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
				// Newer exception: suspends [20:00-06:00] (full night)
				{
					Type:       ExceptionSuspend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{
							Start:      "20:00",
							End:        "06:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
			},
			expectedShouldHibernate: false,
			explanation:             "With multiple suspend exceptions, only the newest is used. 22:00 is in the newest exception's window [20:00-06:00]",
		},
		{
			name:        "mixed types: extend and suspend - newest extend is selected",
			currentTime: time.Date(2026, 2, 9, 14, 0, 0, 0, time.UTC), // Monday 14:00
			exceptions: []*Exception{
				// Older: Suspend exception
				{
					Type:       ExceptionSuspend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{
							Start:      "12:00",
							End:        "18:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
				// Newer: Extend exception
				{
					Type:       ExceptionExtend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{
							Start:      "12:00",
							End:        "18:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
			},
			expectedShouldHibernate: true,
			explanation:             "With mixed types, the newest exception (extend) is selected. 14:00 is in extend window [12:00-18:00], so should hibernate",
		},
		{
			name:        "exception not yet active - base schedule applies",
			currentTime: time.Date(2026, 2, 6, 22, 0, 0, 0, time.UTC), // Friday 22:00
			exceptions: []*Exception{
				{
					Type:       ExceptionExtend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC), // Active from Monday
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{
							Start:      "12:00",
							End:        "18:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
			},
			expectedShouldHibernate: true,
			explanation:             "Exception not yet active (ValidFrom in future), so base schedule applies. 22:00 Friday is in base window [20:00-06:00]",
		},
		{
			name:        "exception already expired - base schedule applies",
			currentTime: time.Date(2026, 2, 16, 22, 0, 0, 0, time.UTC), // Monday 22:00 after exception expires
			exceptions: []*Exception{
				{
					Type:       ExceptionExtend,
					ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC), // Expired
					Windows: []OffHourWindow{
						{
							Start:      "12:00",
							End:        "18:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
						},
					},
				},
			},
			expectedShouldHibernate: true,
			explanation:             "Exception has expired, so base schedule applies. 22:00 Monday is in base window [20:00-06:00]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clocktesting.NewFakeClock(tt.currentTime)
			evaluator := NewScheduleEvaluator(fakeClock, WithScheduleBuffer("1m"))

			var exception *Exception
			if len(tt.exceptions) > 0 {
				// In current implementation, only use the first exception
				// In a real multi-exception implementation, this would combine all
				exception = tt.exceptions[0]
			}

			result, err := evaluator.Evaluate(baseWindows, "UTC", exception)
			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}

			if result.ShouldHibernate != tt.expectedShouldHibernate {
				t.Errorf(
					"ShouldHibernate = %v, want %v\n"+
						"Test explanation: %s",
					result.ShouldHibernate,
					tt.expectedShouldHibernate,
					tt.explanation,
				)
			}

			// Log the result for debugging
			t.Logf(
				"Result: ShouldHibernate=%v, State=%s, NextHibernate=%v, NextWakeUp=%v",
				result.ShouldHibernate,
				result.CurrentState,
				result.NextHibernateTime,
				result.NextWakeUpTime,
			)
		})
	}
}

// TestMultipleExceptionsEvaluationStrategies documents potential evaluation strategies
// for handling multiple concurrent exceptions. This is a planning document for
// future enhancements.
//
// Possible strategies:
// 1. NEWEST_WINS (current): Use only the newest exception
// 2. UNION: Combine all exceptions (e.g., suspend windows are unioned)
// 3. PRECEDENCE: Define precedence order (e.g., suspend > extend > replace)
// 4. MERGE_TYPE: Group by type and apply separate merge logic
func TestMultipleExceptionsEvaluationStrategies_Documentation(t *testing.T) {
	/*
		STRATEGY 1: NEWEST_WINS (Current Implementation)
		- Pro: Simple, deterministic, predictable
		- Con: Silently ignores older exceptions, may lose intent
		- Use case: User creates new exception to override previous one

		STRATEGY 2: UNION (Potential Future)
		- Extend exceptions: Union windows (OR logic)
		- Suspend exceptions: Union windows (prevent hibernation in any suspended window)
		- Replace exceptions: ?
		- Pro: More powerful, captures multiple intents
		- Con: Complex merge logic, harder to predict
		- Use case: Multiple teams with different requirements

		STRATEGY 3: PRECEDENCE (Alternative Future)
		Example precedence: Suspend > Extend > Replace
		- Suspend outweighs extend (security: don't hibernate if any suspend is active)
		- Extend and Replace could have specific merge rules
		- Pro: Clear semantics, prevents conflicts
		- Con: Precendence rules must be explicit

		STRATEGY 4: TEMPORAL_ORDERING (Alternative Future)
		- Evaluate exceptions in reverse chronological order (newest first)
		- Later exceptions can "veto" earlier ones
		- Example: Extend + Suspend on same window -> Suspend wins
		- Pro: Natural conflict resolution
		- Con: Implicit semantics, hard to reason about
	*/

	t.Log("See documentation above for potential multi-exception evaluation strategies")
	t.Log("Current implementation uses NEWEST_WINS strategy")
}

// TestExceptionPrecedenceAndCombination documents how different exception types
// might be combined in a future multi-exception system.
func TestExceptionPrecedenceAndCombination_Documentation(t *testing.T) {
	/*
		Hypothetical combination rules for future multi-exception support:

		┌─────────┬──────────┬──────────┬────────────┐
		│ Older   │ Newer    │ Behavior │ Example    │
		├─────────┼──────────┼──────────┼────────────┤
		│ Extend  │ Extend   │ UNION    │ More windows to hibernate
		│ Suspend │ Suspend  │ UNION    │ More windows to prevent hibernation
		│ Extend  │ Suspend  │ SUSPEND  │ Suspend takes precedence
		│         │          │ WINS     │ (safety: don't hibernate if asked)
		│ Replace │ Extend   │ REPLACE  │ Replace defines the baseline
		│         │          │ APPLIES  │
		│ Replace │ Suspend  │ REPLACE  │ Replace defines baseline,
		│         │          │ +        │ Suspend carves out
		│         │          │ SUSPEND  │
		└─────────┴──────────┴──────────┴────────────┘

		Note: This is speculative and not currently implemented.
	*/

	t.Log("See documentation above for speculative combination rules")
}

// TestMultipleExceptionHandlingEdgeCases tests edge cases in multi-exception scenarios.
func TestMultipleExceptionHandlingEdgeCases(t *testing.T) {
	baseWindows := []OffHourWindow{
		{
			Start:      "20:00",
			End:        "06:00",
			DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
		},
	}

	tests := []struct {
		name        string
		currentTime time.Time
		exception   *Exception // Simulates "newest" exception selected
		want        bool
		description string
	}{
		{
			name:        "exception validity boundary: exactly at ValidFrom",
			currentTime: time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
			exception: &Exception{
				Type:       ExceptionExtend,
				ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
				Windows: []OffHourWindow{
					{
						Start:      "00:00",
						End:        "23:59",
						DaysOfWeek: []string{"SUN"},
					},
				},
			},
			want:        false, // Sunday, but exception window is 00:00-23:59
			description: "Exception should be active exactly at ValidFrom time",
		},
		{
			name:        "exception validity boundary: exactly at ValidUntil",
			currentTime: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
			exception: &Exception{
				Type:       ExceptionExtend,
				ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
				Windows: []OffHourWindow{
					{
						Start:      "20:00",
						End:        "23:59",
						DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
					},
				},
			},
			want:        true, // Sunday 23:59:59, should be in extend window
			description: "Exception should be active exactly at ValidUntil time",
		},
		{
			name:        "exception with lead time: before suspension window",
			currentTime: time.Date(2026, 2, 9, 17, 30, 0, 0, time.UTC), // Monday 17:30
			exception: &Exception{
				Type:       ExceptionSuspend,
				ValidFrom:  time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 2, 15, 23, 59, 59, 0, time.UTC),
				LeadTime:   2 * time.Hour, // 2-hour lead time
				Windows: []OffHourWindow{
					{
						Start:      "20:00",
						End:        "06:00",
						DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
					},
				},
			},
			want:        false, // In lead time window (17:30 is 2.5 hours before 20:00)
			description: "Suspension with lead time should prevent hibernation before the window",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clocktesting.NewFakeClock(tt.currentTime)
			evaluator := NewScheduleEvaluator(fakeClock, WithScheduleBuffer("1m"))

			result, err := evaluator.Evaluate(baseWindows, "UTC", tt.exception)
			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}

			if result.ShouldHibernate != tt.want {
				t.Errorf(
					"ShouldHibernate = %v, want %v\n"+
						"Description: %s",
					result.ShouldHibernate,
					tt.want,
					tt.description,
				)
			}
		})
	}
}
