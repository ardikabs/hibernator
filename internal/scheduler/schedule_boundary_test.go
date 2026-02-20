package scheduler

import (
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"
)

func TestFullDayHibernation(t *testing.T) {
	// Scenario:
	// Window: 00:00 - 23:59 (Full day shutdown)
	// Grace/Buffer: 1 minute
	// We expect the system to be hibernated CONTINUOUSLY.
	// Especially around the transition from 23:59 to 00:00.

	windows := []OffHourWindow{
		{
			Start:      "00:00",
			End:        "23:59",
			DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
		},
	}

	timezone := "UTC"
	buffer := "1m"

	// Mock clock
	// Let's pick a transition from Monday to Tuesday.
	// Monday 23:59:30 -> Should be Hibernated (End grace)
	// Tuesday 00:00:30 -> Should be Hibernated (Start of new window)

	// Base date: Monday, Jan 2, 2023
	monLate := time.Date(2023, 1, 2, 23, 59, 30, 0, time.UTC)
	tueEarly := time.Date(2023, 1, 3, 0, 0, 30, 0, time.UTC)

	tests := []struct {
		name string
		time time.Time
		want bool // true = hibernated, false = active
	}{
		{
			name: "Monday 23:59:30 (End Grace)",
			time: monLate,
			want: true,
		},
		{
			name: "Tuesday 00:00:30 (Start Grace)",
			time: tueEarly,
			want: true, // FAILS if logic flips to active
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clocktesting.NewFakeClock(tt.time)
			evaluator := NewScheduleEvaluator(fakeClock, WithScheduleBuffer(buffer))

			result, err := evaluator.Evaluate(windows, timezone, nil)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}

			if result.ShouldHibernate != tt.want {
				t.Errorf("At %v: ShouldHibernate = %v, want %v. State: %s, InGrace: %v",
					tt.time, result.ShouldHibernate, tt.want, result.CurrentState, result.InGracePeriod)
			}
		})
	}
}

func TestFullDayWakeupWithSuspend(t *testing.T) {
	// Scenario:
	// Base Window: 00:00 - 23:59 (Full day hibernation)
	// Suspend Exception: 14:00 - 16:00 (Suspend hibernation for 2 hours)
	// Expected Behavior:
	//   - Before 14:00: Hibernated (base schedule)
	//   - 14:00 - 16:00: Active (suspended)
	//   - After 16:00: Hibernated (base schedule)

	windows := []OffHourWindow{
		{
			Start:      "20:00",
			End:        "06:00",
			DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
		},
	}

	suspension := &Exception{
		Type:       ExceptionSuspend,
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		Windows: []OffHourWindow{
			{
				Start:      "00:00",
				End:        "23:59",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			},
		},
	}

	timezone := "UTC"
	buffer := "1m"

	tests := []struct {
		name string
		time time.Time
		want bool // true = hibernated, false = active
	}{
		{
			name: "Monday 00:00 - exact start time",
			time: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "Tuesday 23:59 - exact end time",
			time: time.Date(2026, 1, 6, 23, 59, 0, 0, time.UTC),
			want: false,
		},
		{
			name: "Tuesday 23:59:59 - at end time grace period",
			time: time.Date(2026, 1, 6, 23, 59, 59, 0, time.UTC),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clocktesting.NewFakeClock(tt.time)
			evaluator := NewScheduleEvaluator(fakeClock, WithScheduleBuffer(buffer))

			result, err := evaluator.Evaluate(windows, timezone, suspension)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}

			if result.ShouldHibernate != tt.want {
				t.Errorf("At %v: ShouldHibernate = %v, want %v. State: %s, InGrace: %v",
					tt.time, result.ShouldHibernate, tt.want, result.CurrentState, result.InGracePeriod)
			}
		})
	}
}

func TestSuspendExceptionBackwardWindows(t *testing.T) {
	// Scenario: Suspend with backward/overnight window
	// Base Window: 20:00 - 06:00 (Nightly hibernation)
	// Suspend Exception: 22:00 - 03:00 (Keep awake during this overnight period)
	// Result: System stays active from 22:00 to 03:00, hibernates outside this window
	// This confirms evaluateSuspend accepts backward windows correctly.

	windowsBase := []OffHourWindow{
		{
			Start:      "20:00",
			End:        "06:00",
			DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
		},
	}

	timezone := "UTC"
	buffer := "1m"

	suspensionBackward := &Exception{
		Type:       ExceptionSuspend,
		ValidFrom:  time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		Windows: []OffHourWindow{
			{
				Start:      "22:00",
				End:        "03:00", // Backward: ends before it starts
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			},
		},
	}

	testCases := []struct {
		name string
		time time.Time
		want bool // true = hibernated, false = active
	}{
		{
			name: "Monday 20:30 (before suspension, follows base schedule)",
			time: time.Date(2026, 1, 2, 20, 30, 0, 0, time.UTC),
			want: true, // Hibernated (base schedule active, no suspension yet)
		},
		{
			name: "Monday 22:30 (during backward suspension window)",
			time: time.Date(2026, 1, 2, 22, 30, 0, 0, time.UTC),
			want: false, // Active (suspended from hibernation)
		},
		{
			name: "Tuesday 00:30 (midnight, still in backward suspension)",
			time: time.Date(2026, 1, 3, 0, 30, 0, 0, time.UTC),
			want: false, // Active (still in suspension window)
		},
		{
			name: "Tuesday 02:30 (near end of suspension window)",
			time: time.Date(2026, 1, 3, 2, 30, 0, 0, time.UTC),
			want: false, // Active (still in suspension window)
		},
		{
			name: "Tuesday 03:00:00 (suspension at exact end time window - grace period 1 minute)",
			time: time.Date(2026, 1, 3, 3, 0, 0, 0, time.UTC),
			want: false, // Active (outside hibernation window)
		},
		{
			name: "Tuesday 03:00:59 (suspension at end time window - grace period 1 minute)",
			time: time.Date(2026, 1, 3, 3, 0, 59, 0, time.UTC),
			want: false, // Active (outside hibernation window)
		},
		{
			name: "Tuesday 03:01:00 (suspension at end time window - grace period 1 minute)",
			time: time.Date(2026, 1, 3, 3, 0, 0, 0, time.UTC),
			want: false, // Active (still active at exact end of grace period, grace ends at 03:01)
		},
		{
			name: "Tuesday 03:01:10 (suspension at end time window - end of grace period 1 minute)",
			time: time.Date(2026, 1, 3, 3, 10, 0, 0, time.UTC),
			want: true, // Hibernated (follow base schedule, at hibernation window)
		},
		{
			name: "Tuesday 07:00 (after suspension and base hibernation ends)",
			time: time.Date(2026, 1, 3, 7, 0, 0, 0, time.UTC),
			want: false, // Active (outside hibernation window)
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clocktesting.NewFakeClock(tt.time)
			evaluator := NewScheduleEvaluator(fakeClock, WithScheduleBuffer(buffer))

			result, err := evaluator.Evaluate(windowsBase, timezone, suspensionBackward)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}

			if result.ShouldHibernate != tt.want {
				t.Errorf("At %v: ShouldHibernate = %v, want %v. State: %s, InGrace: %v",
					tt.time, result.ShouldHibernate, tt.want, result.CurrentState, result.InGracePeriod)
			}
		})
	}
}

func TestFullDayWakeup(t *testing.T) {
	// Scenario:
	// Window: 23:59 - 00:00 (Full day wakeup)
	// Grace/Buffer: 1 minute
	// We expect the system to be active CONTINUOUSLY.
	// Especially around the transition from 23:59 to 00:00.
	//
	// Edge Case: While 00:00:00 - 00:00:59 typically won't be evaluated during normal operation,
	// it can happen if the operator restarts or the user modifies the schedule at exactly that moment.
	// In such scenarios, the schedule might evaluate as hibernated instead of active.
	// Using a suspend exception is recommended to prevent this edge case.

	windowsBase := []OffHourWindow{
		{
			Start:      "23:59",
			End:        "00:00",
			DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
		},
	}

	timezone := "UTC"
	buffer := "1m"

	testCases := []struct {
		name string
		time time.Time
		want bool // true = hibernated, false = active
	}{
		{
			name: "Monday 23:58:59 (in end schedule window)",
			time: time.Date(2026, 1, 2, 23, 58, 59, 0, time.UTC),
			want: false, // Active
		},
		{
			name: "Tuesday 00:01:10, requeue time after above case",
			time: time.Date(2026, 1, 3, 0, 1, 10, 0, time.UTC),
			want: false, // Active (in start schedule grace period window of 1 minute)
		},
		{
			name: "Monday 23:59:30, grace period measured from start schedule, until 00:00:00",
			time: time.Date(2026, 1, 2, 23, 59, 30, 0, time.UTC),
			want: false, // Active (in start schedule grace period window of 1 minute)
		},
		{
			name: "[EDGE CASE] Tuesday 00:00:00, exactly at end schedule",
			time: time.Date(2026, 1, 3, 0, 1, 0, 0, time.UTC),
			want: true, // Hibernated
		},
		{
			name: "[EDGE CASE] Tuesday 00:00:30, grace period measured from end schedule, until 00:01:00",
			time: time.Date(2026, 1, 3, 0, 0, 30, 0, time.UTC),
			want: true, // Hibernated (where the transition evaluated after end schedule grace period window instead of start schedule grace period window)
		},
		{
			name: "[EDGE CASE] Tuesday 00:01:00, exactly at end of grace period.",
			time: time.Date(2026, 1, 3, 0, 1, 0, 0, time.UTC),
			want: true, // Hibernated (after grace period of end schedule)
		},
		{
			name: "Tuesday 00:01:01, normally based on next requeue should be 00:01:10, but just in case reconcile triggered at this time",
			time: time.Date(2026, 1, 3, 0, 1, 1, 0, time.UTC),
			want: false, // Active (after grace period of end schedule)
		},
		{
			name: "Tuesday 03:00:00",
			time: time.Date(2026, 1, 3, 3, 0, 0, 0, time.UTC),
			want: false, // Active (based on base schedule)
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clocktesting.NewFakeClock(tt.time)
			evaluator := NewScheduleEvaluator(fakeClock, WithScheduleBuffer(buffer))

			result, err := evaluator.Evaluate(windowsBase, timezone, nil)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}

			if result.ShouldHibernate != tt.want {
				t.Errorf("At %v: ShouldHibernate = %v, want %v. State: %s, InGrace: %v",
					tt.time, result.ShouldHibernate, tt.want, result.CurrentState, result.InGracePeriod)
			}
		})
	}
}
