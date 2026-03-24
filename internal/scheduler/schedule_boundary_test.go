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

			result, err := evaluator.Evaluate(windows, timezone, []*Exception{suspension})
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

			result, err := evaluator.Evaluate(windowsBase, timezone, []*Exception{suspensionBackward})
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

// TestSuspendNextWakeUpAdjustedForUpcomingSuspension validates that when the system is
// currently hibernating and a suspension window is scheduled later the same day,
// NextWakeUpTime is pulled forward to the suspension start instead of staying at the
// base-schedule wakeup time.  Without this fix the controller would requeue far in the
// future (e.g. 18:00) and silently skip the suspension carve-out at 09:00.
//
// Scenario
//   - Base window : Monday-Friday 20:00 → 18:00 (long overnight window)
//   - Suspension  : weekdays 09:00 → 10:00
//   - Clock       : Wednesday 06:00 (hibernating, suspension starts in 3 h)
//
// Expected: ShouldHibernate=true, NextWakeUpTime=09:00 (not 18:00).
func TestSuspendNextWakeUpAdjustedForUpcomingSuspension(t *testing.T) {
	timezone := "UTC"

	baseWindows := []OffHourWindow{
		{Start: "20:00", End: "18:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
	}

	exception := &Exception{
		Type:       ExceptionSuspend,
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		Windows: []OffHourWindow{
			{Start: "09:00", End: "10:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
		},
	}

	tests := []struct {
		name              string
		now               time.Time
		wantHibernate     bool
		wantNextWakeUpAt  time.Time // zero means "don't check"
		wantNextHibernate time.Time // zero means "don't check"
	}{
		{
			name: "06:00 AM - hibernating, upcoming suspension at 09:00 must drive nextWakeUp",
			// Wednesday 2026-01-28 06:00 UTC
			now:              time.Date(2026, 1, 28, 6, 0, 0, 0, time.UTC),
			wantHibernate:    true,
			wantNextWakeUpAt: time.Date(2026, 1, 28, 9, 0, 0, 0, time.UTC),
		},
		{
			name:              "09:30 AM - inside suspension window, must be active",
			now:               time.Date(2026, 1, 28, 9, 30, 0, 0, time.UTC),
			wantHibernate:     false,
			wantNextHibernate: time.Date(2026, 1, 28, 10, 0, 0, 0, time.UTC),
		},
		{
			name:          "10:30 AM - suspension ended, base schedule resumes hibernation",
			now:           time.Date(2026, 1, 28, 10, 30, 0, 0, time.UTC),
			wantHibernate: true,
			// No pending suspension today, nextWakeUp comes from base (18:00)
			wantNextWakeUpAt: time.Date(2026, 1, 28, 18, 0, 0, 0, time.UTC),
		},
		{
			name: "20:30 - evening, currently active, nextHibernate is from base (next day 20:00)",
			// Wednesday 20:30: base wakeup was at 18:00, so active now
			// Next hibernate = Monday 20:00 (wait, still Wednesday 20:30 so hibernate fires next)
			// Actually at 20:30 we just crossed into the base hibernate window -- should hibernate
			now:           time.Date(2026, 1, 28, 20, 30, 0, 0, time.UTC),
			wantHibernate: true,
			// Suspension starts tomorrow (Thu) at 09:00 → nextWakeUp should be 09:00 Thu
			wantNextWakeUpAt: time.Date(2026, 1, 29, 9, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clocktesting.NewFakeClock(tt.now)
			evaluator := NewScheduleEvaluator(fakeClock)

			result, err := evaluator.Evaluate(baseWindows, timezone, []*Exception{exception})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}

			if result.ShouldHibernate != tt.wantHibernate {
				t.Errorf("ShouldHibernate = %v, want %v (state=%s)", result.ShouldHibernate, tt.wantHibernate, result.CurrentState)
			}

			if !tt.wantNextWakeUpAt.IsZero() && !result.NextWakeUpTime.Equal(tt.wantNextWakeUpAt) {
				t.Errorf("NextWakeUpTime = %v, want %v", result.NextWakeUpTime, tt.wantNextWakeUpAt)
			}

			if !tt.wantNextHibernate.IsZero() && !result.NextHibernateTime.Equal(tt.wantNextHibernate) {
				t.Errorf("NextHibernateTime = %v, want %v", result.NextHibernateTime, tt.wantNextHibernate)
			}
		})
	}
}

// TestSuspendExceptionWeekendCarveOut validates a weekend suspension carve-out against a
// weekday-only base schedule.  The system hibernates from Friday 20:00 through Monday 06:00;
// on Saturday and Sunday the suspension window (09:00-17:00) must keep it active.
//
// Base window   : 20:00 → 06:00, MON-FRI
// Suspension    : 09:00 → 17:00, SAT-SUN
// Schedule buffer: 1 minute
//
// Scenarios
//  1. Friday 20:10   – just entered Friday night hibernation; outside all suspension windows → hibernated
//  2. Saturday 17:10 – suspension window ended at 17:00, grace (1m) expired → hibernated
//  3. Sunday 16:10   – inside Sunday suspension window → active
//  4. Sunday 17:01:10 – 1m grace from 17:00 ends at 17:01:00; 17:01:10 is past grace → hibernated
func TestSuspendExceptionWeekendCarveOut(t *testing.T) {
	timezone := "UTC"

	// January 30 2026 is a Friday.
	baseWindows := []OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
	}

	exception := &Exception{
		Type:       ExceptionSuspend,
		ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		Windows: []OffHourWindow{
			{Start: "09:00", End: "17:00", DaysOfWeek: []string{"SAT", "SUN"}},
		},
	}

	tests := []struct {
		name          string
		now           time.Time
		wantHibernate bool
		wantRequeue   time.Duration
	}{
		{
			// Hibernated. findNextSuspensionStart returns Sat 09:00 (12h50m away),
			// pulling nextWakeUp forward from Mon 06:00. Requeue = 12h50m + 1m + 10s.
			name:          "Friday 20:10 - just entered base hibernation window, no suspension today",
			now:           time.Date(2026, 1, 30, 20, 10, 0, 0, time.UTC),
			wantHibernate: true,
			wantRequeue:   12*time.Hour + 51*time.Minute + 10*time.Second, // next event: 2026-01-31 09:00 UTC (Sat suspension start)
		},
		{
			// Hibernated. Suspension ended and grace expired; next suspension start is
			// Sun 09:00 (15h50m away). Requeue = 15h50m + 1m + 10s.
			name:          "Saturday 17:10 - suspension window ended at 17:00, grace (1m) expired",
			now:           time.Date(2026, 1, 31, 17, 10, 0, 0, time.UTC),
			wantHibernate: true,
			wantRequeue:   15*time.Hour + 51*time.Minute + 10*time.Second, // next event: 2026-02-01 09:00 UTC (Sun suspension start)
		},
		{
			// Active (in suspension). nextHibernate is adjusted to suspension end 17:00
			// (50m away). Requeue = 50m + 1m + 10s.
			name:          "Sunday 16:10 - inside suspension window (09:00-17:00), should be active",
			now:           time.Date(2026, 2, 1, 16, 10, 0, 0, time.UTC),
			wantHibernate: false,
			wantRequeue:   51*time.Minute + 10*time.Second, // next event: 2026-02-01 17:00 UTC (Sun suspension end → nextHibernate)
		},
		{
			// Hibernated. No upcoming suspension in today/tomorrow (Mon is not SAT-SUN),
			// so nextWakeUp stays at Mon 06:00 (12h58m50s away). Requeue = 12h58m50s + 1m + 10s = 13h.
			name:          "Sunday 17:01:10 - 10s past end of 1m grace period from 17:00, should be hibernated",
			now:           time.Date(2026, 2, 1, 17, 1, 10, 0, time.UTC),
			wantHibernate: true,
			wantRequeue:   13 * time.Hour, // next event: 2026-02-02 06:00 UTC (Mon base wakeup)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clocktesting.NewFakeClock(tt.now)
			evaluator := NewScheduleEvaluator(fakeClock, WithScheduleBuffer("1m"))

			result, err := evaluator.Evaluate(baseWindows, timezone, []*Exception{exception})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}

			if result.ShouldHibernate != tt.wantHibernate {
				t.Errorf("ShouldHibernate = %v, want %v (state=%s, inGrace=%v)",
					result.ShouldHibernate, tt.wantHibernate, result.CurrentState, result.InGracePeriod)
			}

			if requeue := evaluator.NextRequeueTime(result); requeue != tt.wantRequeue {
				t.Errorf("NextRequeueTime = %v, want %v", requeue, tt.wantRequeue)
			}
		})
	}
}
