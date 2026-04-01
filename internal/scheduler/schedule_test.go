/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"
)

var (
	clk = clocktesting.NewFakeClock(time.Now())
)

func TestScheduleEvaluator_eval(t *testing.T) {
	tests := []struct {
		name          string
		window        ScheduleWindow
		now           time.Time
		wantHibernate bool
		wantState     string
		wantErr       bool
	}{
		{
			name: "active during work hours",
			window: ScheduleWindow{
				HibernateCron: "0 20 * * 1-5", // 8 PM weekdays
				WakeUpCron:    "0 6 * * 1-5",  // 6 AM weekdays
				Timezone:      "UTC",
			},
			// Wednesday 2 PM UTC
			now:           time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name: "hibernated during night hours",
			window: ScheduleWindow{
				HibernateCron: "0 20 * * 1-5", // 8 PM weekdays
				WakeUpCron:    "0 6 * * 1-5",  // 6 AM weekdays
				Timezone:      "UTC",
			},
			// Wednesday 11 PM UTC (after 8 PM hibernate)
			now:           time.Date(2026, 1, 28, 23, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name: "hibernated early morning before wake-up",
			window: ScheduleWindow{
				HibernateCron: "0 20 * * 1-5", // 8 PM weekdays
				WakeUpCron:    "0 6 * * 1-5",  // 6 AM weekdays
				Timezone:      "UTC",
			},
			// Thursday 4 AM UTC (before 6 AM wake-up)
			now:           time.Date(2026, 1, 29, 4, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name: "active after wake-up",
			window: ScheduleWindow{
				HibernateCron: "0 20 * * 1-5", // 8 PM weekdays
				WakeUpCron:    "0 6 * * 1-5",  // 6 AM weekdays
				Timezone:      "UTC",
			},
			// Thursday 7 AM UTC (after 6 AM wake-up)
			now:           time.Date(2026, 1, 29, 7, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name: "invalid timezone",
			window: ScheduleWindow{
				HibernateCron: "0 20 * * *",
				WakeUpCron:    "0 6 * * *",
				Timezone:      "Invalid/Zone",
			},
			now:     time.Now(),
			wantErr: true,
		},
		{
			name: "invalid hibernate cron",
			window: ScheduleWindow{
				HibernateCron: "invalid",
				WakeUpCron:    "0 6 * * *",
				Timezone:      "UTC",
			},
			now:     time.Now(),
			wantErr: true,
		},
		{
			name: "timezone handling - PST",
			window: ScheduleWindow{
				HibernateCron: "0 18 * * 1-5", // 6 PM PST
				WakeUpCron:    "0 8 * * 1-5",  // 8 AM PST
				Timezone:      "America/Los_Angeles",
			},
			// Wednesday 10 AM PST (2 PM UTC on 1/28)
			now:           time.Date(2026, 1, 28, 18, 0, 0, 0, time.UTC), // 10 AM PST
			wantHibernate: false,
			wantState:     "active",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClock := clocktesting.NewFakeClock(tt.now)
			evaluator := NewScheduleEvaluator(fakeClock)

			result, err := evaluator.eval(tt.window)
			if (err != nil) != tt.wantErr {
				t.Errorf("eval() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if result.ShouldHibernate != tt.wantHibernate {
				t.Errorf("eval() ShouldHibernate = %v, want %v", result.ShouldHibernate, tt.wantHibernate)
			}
			if result.CurrentState != tt.wantState {
				t.Errorf("eval() CurrentState = %v, want %v", result.CurrentState, tt.wantState)
			}
		})
	}
}

func TestScheduleEvaluator_NextRequeueTime(t *testing.T) {
	baseWindows := []OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
	}

	tests := []struct {
		name        string
		baseWindows []OffHourWindow
		timezone    string
		now         time.Time
		want        time.Duration
	}{
		{
			name:        "next reconcile to hibernate",
			baseWindows: baseWindows,
			timezone:    "UTC",
			now:         time.Date(2026, 1, 28, 19, 0, 0, 0, time.UTC), // Wednesday 7 PM UTC
			want:        time.Hour + time.Minute + 10*time.Second,
		},
		{
			name:        "next reconcile to hibernate - when at exact start time",
			baseWindows: baseWindows,
			timezone:    "UTC",
			now:         time.Date(2026, 1, 28, 20, 0, 0, 0, time.UTC), // Wednesday 8 PM UTC
			want:        time.Minute + 10*time.Second,
		},
		{
			name:        "within buffer window - should requeue soon not next day",
			baseWindows: baseWindows,
			timezone:    "UTC",
			// Wednesday 20:00:30 UTC - within 1m buffer of 20:00 start
			// ShouldHibernate will be FALSE due to buffer.
			// NextHibernateTime (from cron) will be TOMORROW 20:00.
			// We want requeue to be remaining buffer (30s) + safety (10s) = 40s
			now:  time.Date(2026, 1, 28, 20, 0, 30, 0, time.UTC),
			want: 30*time.Second + 10*time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := NewScheduleEvaluator(clocktesting.NewFakeClock(tt.now), WithScheduleBuffer("1m"))
			result, err := evaluator.Evaluate(tt.baseWindows, tt.timezone, nil)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}

			requeueDuration := evaluator.NextRequeueTime(result)
			if requeueDuration != tt.want {
				t.Errorf("NextRequeueTime() = %v, want %v", requeueDuration, tt.want)
			}
		})
	}
}

func TestParseWindowToCron(t *testing.T) {
	tests := []struct {
		name              string
		start             string
		end               string
		days              []string
		wantHibernateCron string
		wantWakeUpCron    string
		wantErr           bool
	}{
		{
			name:              "valid single window weekdays",
			start:             "20:00",
			end:               "06:00",
			days:              []string{"MON", "TUE", "WED", "THU", "FRI"},
			wantHibernateCron: "0 20 * * 1,2,3,4,5",
			wantWakeUpCron:    "0 6 * * 1,2,3,4,5",
			wantErr:           false,
		},
		{
			name:              "valid single window with all days",
			start:             "22:30",
			end:               "08:15",
			days:              []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"},
			wantHibernateCron: "30 22 * * 0,1,2,3,4,5,6",
			wantWakeUpCron:    "15 8 * * 0,1,2,3,4,5,6",
			wantErr:           false,
		},
		{
			name:              "valid single window weekend only",
			start:             "23:00",
			end:               "07:00",
			days:              []string{"SAT", "SUN"},
			wantHibernateCron: "0 23 * * 6,0",
			wantWakeUpCron:    "0 7 * * 6,0",
			wantErr:           false,
		},
		{
			name:              "overnight window end before start",
			start:             "20:00",
			end:               "06:00",
			days:              []string{"MON"},
			wantHibernateCron: "0 20 * * 1",
			wantWakeUpCron:    "0 6 * * 1",
			wantErr:           false,
		},
		{
			name:              "case insensitive days",
			start:             "18:00",
			end:               "09:00",
			days:              []string{"mon", "Wed", "FRI"},
			wantHibernateCron: "0 18 * * 1,3,5",
			wantWakeUpCron:    "0 9 * * 1,3,5",
			wantErr:           false,
		},
		{
			name:    "empty windows",
			wantErr: true,
		},
		{
			name:    "invalid start time format",
			start:   "25:00",
			end:     "06:00",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:    "invalid start time hour",
			start:   "24:00",
			end:     "06:00",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:    "invalid start time minute",
			start:   "20:60",
			end:     "06:00",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:    "invalid end time format missing leading zero",
			start:   "20:00",
			end:     "6",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:    "invalid day name",
			start:   "20:00",
			end:     "06:00",
			days:    []string{"MONDAY"},
			wantErr: true,
		},
		{
			name:    "invalid day name mixed",
			start:   "20:00",
			end:     "06:00",
			days:    []string{"MON", "INVALID", "WED"},
			wantErr: true,
		},
		{
			name:    "malformed time no colon",
			start:   "2000",
			end:     "06:00",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:    "negative hour",
			start:   "-1:00",
			end:     "06:00",
			days:    []string{"MON"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hibernateCron, wakeUpCron, err := ParseWindowToCron(tt.start, tt.end, tt.days...)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseWindowToCron() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if hibernateCron != tt.wantHibernateCron {
				t.Errorf("ParseWindowToCron() hibernateCron = %v, want %v", hibernateCron, tt.wantHibernateCron)
			}

			if wakeUpCron != tt.wantWakeUpCron {
				t.Errorf("ParseWindowToCron() wakeUpCron = %v, want %v", wakeUpCron, tt.wantWakeUpCron)
			}
		})
	}
}

func TestScheduleEvaluator_Evaluate(t *testing.T) {
	// Base schedule: hibernate 20:00-06:00 on weekdays
	baseWindows := []OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
	}

	tests := []struct {
		name          string
		baseWindows   []OffHourWindow
		timezone      string
		exception     *Exception
		now           time.Time
		wantHibernate bool
		wantState     string
		wantErr       bool
	}{
		{
			name:        "no exception - active during work hours",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception:   nil,
			// Wednesday 2 PM UTC
			now:           time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "no exception - hibernated during night",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception:   nil,
			// Wednesday 11 PM UTC
			now:           time.Date(2026, 1, 28, 23, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:        "suspend exception - keep awake during normally hibernated time",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionSuspend,
				ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				Windows: []OffHourWindow{
					{Start: "21:00", End: "02:00", DaysOfWeek: []string{"WED"}},
				},
			},
			// Wednesday 11 PM UTC - normally hibernated, but in suspension window
			now:           time.Date(2026, 1, 28, 23, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "suspend exception - hibernate outside suspension window",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionSuspend,
				ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				Windows: []OffHourWindow{
					{Start: "21:00", End: "23:00", DaysOfWeek: []string{"WED"}},
				},
			},
			// Wednesday 11:30 PM UTC - outside suspension window (ends at 23:00)
			now:           time.Date(2026, 1, 28, 23, 30, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:        "suspend with lead time - prevent hibernation in lead time window",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionSuspend,
				ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				LeadTime:   1 * time.Hour,
				Windows: []OffHourWindow{
					{Start: "21:00", End: "02:00", DaysOfWeek: []string{"WED"}},
				},
			},
			// Wednesday 8:30 PM UTC - in lead time window (1h before 21:00 suspension)
			now:           time.Date(2026, 1, 28, 20, 30, 0, 0, time.UTC),
			wantHibernate: false, // Lead time prevents hibernation
			wantState:     "active",
		},
		{
			name:        "replace exception - use only exception windows",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionReplace,
				ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				Windows: []OffHourWindow{
					// Replace with 24/7 hibernation (holiday mode)
					{Start: "00:00", End: "23:59", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}},
				},
			},
			// Wednesday 2 PM UTC - normally active, but replaced with 24/7 hibernation
			now:           time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:        "exception outside valid period - use base schedule",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionSuspend,
				ValidFrom:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC), // Expired
				Windows: []OffHourWindow{
					{Start: "21:00", End: "02:00", DaysOfWeek: []string{"WED"}},
				},
			},
			// Wednesday 11 PM UTC - exception expired, use base schedule
			now:           time.Date(2026, 1, 28, 23, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:          "empty base windows with no exception",
			baseWindows:   []OffHourWindow{},
			timezone:      "UTC",
			exception:     nil,
			now:           time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "weekday overnight - stays hibernated over weekend",
			baseWindows: baseWindows,
			timezone:    "Asia/Jakarta",
			exception:   nil,
			// Friday 23:00 UTC (Saturday, 06:00 WIB) - should still be hibernated from Friday 13:00 UTC (Friday, 20:00 WIB)
			now:           time.Date(2026, 2, 6, 23, 5, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:          "weekend should still hibernated",
			baseWindows:   baseWindows,
			timezone:      "Asia/Jakarta",
			exception:     nil,
			now:           time.Date(2026, 2, 7, 10, 0, 0, 0, time.UTC), // Saturday, 17:00 WIB
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:          "monday should wakeup after weekend hibernation",
			baseWindows:   baseWindows,
			timezone:      "Asia/Jakarta",
			exception:     nil,
			now:           time.Date(2026, 2, 8, 23, 2, 0, 0, time.UTC), // Monday, 06:00 WIB
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name: "full day off on sunday - should hibernate all day",
			baseWindows: []OffHourWindow{
				{Start: "00:00", End: "23:59", DaysOfWeek: []string{"SAT", "SUN", "MON"}},
			},
			timezone:      "UTC",
			exception:     nil,
			now:           time.Date(2026, 2, 8, 23, 59, 10, 0, time.UTC), // Sunday, 23:59:10 WIB
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name: "full day off on sunday - should hibernate all day on next day",
			baseWindows: []OffHourWindow{
				{Start: "00:00", End: "23:59", DaysOfWeek: []string{"SAT", "SUN", "MON"}},
			},
			timezone:      "UTC",
			exception:     nil,
			now:           time.Date(2026, 2, 8, 1, 10, 0, 0, time.UTC), // Monday, 00:00:00 WIB
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name: "full day awake on sunday - should operate all day",
			baseWindows: []OffHourWindow{
				{Start: "23:59", End: "00:00", DaysOfWeek: []string{"SAT", "SUN", "MON"}},
			},
			timezone:      "UTC",
			exception:     nil,
			now:           time.Date(2026, 2, 8, 23, 59, 15, 0, time.UTC), // Sunday, 23:59:25 WIB
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name: "full day awake on sunday - should operate all day on next day",
			baseWindows: []OffHourWindow{
				{Start: "23:59", End: "00:00", DaysOfWeek: []string{"SAT", "SUN", "MON"}},
			},
			timezone:      "UTC",
			exception:     nil,
			now:           time.Date(2026, 2, 8, 0, 1, 10, 0, time.UTC), // Monday, 00:01:10 WIB
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name: "full day awake on all days - should be active past grace period",
			baseWindows: []OffHourWindow{
				{Start: "23:59", End: "00:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}},
			},
			timezone:      "UTC",
			exception:     nil,
			now:           time.Date(2026, 2, 9, 0, 1, 1, 0, time.UTC), // Monday 00:01:01 UTC, after 1m grace period ends at 00:01:00
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "suspend exception in boundary time - should prevent hibernation at boundary",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionSuspend,
				ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), // Expired
				Windows: []OffHourWindow{
					{Start: "20:00", End: "23:59", DaysOfWeek: []string{"THU", "FRI"}},
				},
			},
			now:           time.Date(2026, 3, 5, 20, 1, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := NewScheduleEvaluator(clocktesting.NewFakeClock(tt.now), WithScheduleBuffer("1m"))
			result, err := evaluator.Evaluate(tt.baseWindows, tt.timezone, []*Exception{tt.exception})

			if (err != nil) != tt.wantErr {
				t.Errorf("Evaluate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if result.ShouldHibernate != tt.wantHibernate {
				t.Errorf("Evaluate() ShouldHibernate = %v, want %v", result.ShouldHibernate, tt.wantHibernate)
			}

			if result.CurrentState != tt.wantState {
				t.Errorf("Evaluate() CurrentState = %v, want %v", result.CurrentState, tt.wantState)
			}
		})
	}
}

// TestSuspend_NextHibernateForwardLook is a regression test for the case where
// nextHibernate falls inside a suspension window and must be advanced past the
// suspension end. Previously this caused ComputeUpcomingEvents (used by
// kubectl-hibernator preview) to emit two Hibernate events: one at the base window
// start and one at the suspension end.
func TestSuspend_NextHibernateForwardLook(t *testing.T) {
	baseWindows := []OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
	}

	alwaysValid := func() (time.Time, time.Time) {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	}

	tests := []struct {
		name              string
		exceptions        []*Exception
		now               time.Time
		wantHibernate     bool
		wantNextHibernate time.Time
	}{
		{
			// Suspend window starts exactly at the base hibernate time (20:00).
			// nextHibernate must be advanced to the suspension end (23:00) so the
			// preview only shows one Hibernate event at 23:00.
			name: "suspend coincides with base hibernate start - nextHibernate advanced to suspension end",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{{
					Type: ExceptionSuspend, ValidFrom: vf, ValidUntil: vu,
					Windows: []OffHourWindow{
						{Start: "20:00", End: "23:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
					},
				}}
			}(),
			// Thu 14:10 UTC — active, next base hibernate is 20:00 which falls inside the suspend window
			now:               time.Date(2026, 3, 26, 14, 10, 0, 0, time.UTC),
			wantHibernate:     false,
			wantNextHibernate: time.Date(2026, 3, 26, 23, 0, 0, 0, time.UTC),
		},
		{
			// Suspend window starts after the base hibernate time (21:00 vs 20:00).
			// nextHibernate must remain at 20:00 because that time is NOT inside the
			// suspension window, so the base hibernate event is valid.
			name: "suspend starts after base hibernate - nextHibernate unchanged",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{{
					Type: ExceptionSuspend, ValidFrom: vf, ValidUntil: vu,
					Windows: []OffHourWindow{
						{Start: "21:00", End: "23:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
					},
				}}
			}(),
			// Thu 14:10 UTC — next base hibernate is 20:00 which is NOT in [21:00-23:00]
			now:               time.Date(2026, 3, 26, 14, 10, 0, 0, time.UTC),
			wantHibernate:     false,
			wantNextHibernate: time.Date(2026, 3, 26, 20, 0, 0, 0, time.UTC),
		},
		{
			// extend+suspend path (applySuspendCarveOut): the same forward-look must apply.
			// extend adds 10:00-13:00 Thu; suspend covers 20:00-23:00 weekdays.
			// At 14:10 the extend window has passed; nextHibernate from the extended base is
			// 20:00 today which falls inside the suspend window → must advance to 23:00.
			name: "extend+suspend - coinciding suspend start advances nextHibernate past suspension end",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "10:00", End: "13:00", DaysOfWeek: []string{"THU"}},
						},
					},
					{
						Type: ExceptionSuspend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "20:00", End: "23:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
						},
					},
				}
			}(),
			// Thu 14:10 UTC — extend window already passed; next hibernate is base 20:00
			// which falls inside the suspend window → should advance to 23:00
			now:               time.Date(2026, 3, 26, 14, 10, 0, 0, time.UTC),
			wantHibernate:     false,
			wantNextHibernate: time.Date(2026, 3, 26, 23, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := NewScheduleEvaluator(clocktesting.NewFakeClock(tt.now))
			result, err := evaluator.Evaluate(baseWindows, "UTC", tt.exceptions)
			require.NoError(t, err)

			assert.Equal(t, tt.wantHibernate, result.ShouldHibernate, "ShouldHibernate mismatch")
			assert.Equal(t, tt.wantNextHibernate.UTC(), result.NextHibernateTime.UTC(), "NextHibernateTime mismatch")
		})
	}
}

// TestExtend_NextWakeUpSkipsIntoExtendWindow is a regression test for the case where
// the base schedule wakeup falls at the start of an extend window.
// Previously evaluateExtend picked min(base.nextWakeUp, extend.nextWakeUp) which produced
// a spurious WakeUp event immediately followed by a re-Hibernate in ComputeUpcomingEvents.
// The correct nextWakeUp is the extend window's own end when the base wakeup is consumed by it.
func TestExtend_NextWakeUpSkipsIntoExtendWindow(t *testing.T) {
	// Base: 20:00–06:00 weekdays.
	baseWindows := []OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
	}

	alwaysValid := func() (time.Time, time.Time) {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	}

	tests := []struct {
		name              string
		exceptions        []*Exception
		now               time.Time
		wantHibernate     bool
		wantNextWakeUp    time.Time
		wantNextHibernate time.Time
	}{
		{
			// Base wakes at 06:00; extend is 06:00–13:00 (immediately re-hibernates).
			// Without suspend: nextWakeUp must be 13:00 (extend end), not 06:00.
			name: "extend starts at base wakeup - nextWakeUp advanced to extend end",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{{
					Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
					Windows: []OffHourWindow{
						{Start: "06:00", End: "13:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
					},
				}}
			}(),
			// Thu 20:30 UTC — base hibernated, base wakeup is 06:00 tomorrow (inside extend)
			now:               time.Date(2026, 3, 26, 20, 30, 0, 0, time.UTC),
			wantHibernate:     true,
			wantNextWakeUp:    time.Date(2026, 3, 27, 13, 0, 0, 0, time.UTC),
			wantNextHibernate: time.Date(2026, 3, 26, 20, 0, 0, 0, time.UTC), // past already, cron gives tomorrow
		},
		{
			// Extend+suspend: base wakes at 06:00 → extend (06:00–13:00) re-hibernates →
			// suspend (08:00–11:00) carves out → true nextWakeUp is suspend start: 08:00.
			name: "extend+suspend - base wakeup consumed by extend, nextWakeUp is suspend start",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "06:00", End: "13:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
						},
					},
					{
						Type: ExceptionSuspend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "08:00", End: "11:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
						},
					},
				}
			}(),
			// Thu 20:30 UTC — hibernated; suspend starts at 08:00 tomorrow which is earlier
			// than extend's 13:00, so suspend wins as nextWakeUp.
			now:            time.Date(2026, 3, 26, 20, 30, 0, 0, time.UTC),
			wantHibernate:  true,
			wantNextWakeUp: time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC),
		},
		{
			// Extend starts well after base wakeup: base wakes at 06:00, extend is 09:00–13:00.
			// The 06:00 wakeup is a genuine gap (not inside any extend window), so it stays.
			name: "extend starts after base wakeup - nextWakeUp unchanged at base wakeup",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{{
					Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
					Windows: []OffHourWindow{
						{Start: "09:00", End: "13:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
					},
				}}
			}(),
			// Thu 20:30 UTC — hibernated; base wakeup 06:00 is genuinely before extend 09:00
			now:            time.Date(2026, 3, 26, 20, 30, 0, 0, time.UTC),
			wantHibernate:  true,
			wantNextWakeUp: time.Date(2026, 3, 27, 6, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := NewScheduleEvaluator(clocktesting.NewFakeClock(tt.now))
			result, err := evaluator.Evaluate(baseWindows, "UTC", tt.exceptions)
			require.NoError(t, err)

			assert.Equal(t, tt.wantHibernate, result.ShouldHibernate, "ShouldHibernate mismatch")
			assert.Equal(t, tt.wantNextWakeUp.UTC(), result.NextWakeUpTime.UTC(), "NextWakeUpTime mismatch")
		})
	}
}

func TestIsInTimeWindows(t *testing.T) {
	tests := []struct {
		name    string
		windows []OffHourWindow
		now     time.Time
		want    bool
	}{
		{
			name: "in same-day window",
			windows: []OffHourWindow{
				{Start: "09:00", End: "17:00", DaysOfWeek: []string{"WED"}},
			},
			now:  time.Date(2026, 1, 28, 12, 0, 0, 0, time.UTC), // Wednesday noon
			want: true,
		},
		{
			name: "outside same-day window",
			windows: []OffHourWindow{
				{Start: "09:00", End: "17:00", DaysOfWeek: []string{"WED"}},
			},
			now:  time.Date(2026, 1, 28, 18, 0, 0, 0, time.UTC), // Wednesday 6 PM
			want: false,
		},
		{
			name: "in overnight window - evening",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:  time.Date(2026, 1, 28, 22, 0, 0, 0, time.UTC), // Wednesday 10 PM
			want: true,
		},
		{
			name: "wrong day",
			windows: []OffHourWindow{
				{Start: "09:00", End: "17:00", DaysOfWeek: []string{"MON"}},
			},
			now:  time.Date(2026, 1, 28, 12, 0, 0, 0, time.UTC), // Wednesday (not Monday)
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInTimeWindows(tt.windows, tt.now)
			if got != tt.want {
				t.Errorf("isInTimeWindows() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsInLeadTimeWindow(t *testing.T) {
	tests := []struct {
		name     string
		windows  []OffHourWindow
		now      time.Time
		leadTime time.Duration
		want     bool
	}{
		{
			name: "in lead time window",
			windows: []OffHourWindow{
				{Start: "21:00", End: "02:00", DaysOfWeek: []string{"WED"}},
			},
			now:      time.Date(2026, 1, 28, 20, 30, 0, 0, time.UTC), // 30 min before 21:00
			leadTime: 1 * time.Hour,
			want:     true,
		},
		{
			name: "before lead time window",
			windows: []OffHourWindow{
				{Start: "21:00", End: "02:00", DaysOfWeek: []string{"WED"}},
			},
			now:      time.Date(2026, 1, 28, 19, 0, 0, 0, time.UTC), // 2 hours before
			leadTime: 1 * time.Hour,
			want:     false,
		},
		{
			name: "after suspension started - not in lead time",
			windows: []OffHourWindow{
				{Start: "21:00", End: "02:00", DaysOfWeek: []string{"WED"}},
			},
			now:      time.Date(2026, 1, 28, 22, 0, 0, 0, time.UTC), // After 21:00
			leadTime: 1 * time.Hour,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInLeadTimeWindow(tt.windows, tt.now, tt.leadTime)
			if got != tt.want {
				t.Errorf("isInLeadTimeWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsInGraceTimeWindow(t *testing.T) {
	tests := []struct {
		boundary  WindowBoundary
		name      string
		windows   []OffHourWindow
		now       time.Time
		graceTime time.Duration
		want      bool
	}{
		{
			boundary: StartBoundary,
			name:     "at exact start time",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 28, 20, 0, 0, 0, time.UTC), // Exactly at 20:00
			graceTime: 1 * time.Hour,
			want:      true,
		},
		{
			boundary: StartBoundary,
			name:     "at exact end of grace time window of start time",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 28, 21, 0, 0, 0, time.UTC), // Exactly at 21:00
			graceTime: 1 * time.Hour,
			want:      true,
		},
		{
			boundary: StartBoundary,
			name:     "in grace time window after start",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 28, 20, 30, 0, 0, time.UTC), // 30 min after 20:00
			graceTime: 1 * time.Hour,
			want:      true,
		},
		{
			boundary: StartBoundary,
			name:     "outside grace time window - grace has expired",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 28, 21, 30, 0, 0, time.UTC), // 1.5 hours after 20:00
			graceTime: 1 * time.Hour,
			want:      false,
		},
		{
			boundary: StartBoundary,
			name:     "at exact start time - in grace period",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 28, 20, 0, 0, 0, time.UTC), // Exactly at 20:00
			graceTime: 1 * time.Hour,
			want:      true,
		},
		{
			boundary: StartBoundary,
			name:     "wrong day - not in grace time window",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 29, 20, 30, 0, 0, time.UTC), // Thursday (not Wednesday)
			graceTime: 1 * time.Hour,
			want:      false,
		},
		{
			boundary: EndBoundary,
			name:     "in grace time window after end",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 28, 6, 30, 0, 0, time.UTC), // 30 min after 06:00
			graceTime: 1 * time.Hour,
			want:      true,
		},
		{
			boundary: EndBoundary,
			name:     "outside grace time window - grace expired",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 28, 7, 30, 0, 0, time.UTC), // 1.5 hours after 06:00
			graceTime: 1 * time.Hour,
			want:      false,
		},
		{
			boundary: EndBoundary,
			name:     "at exact end time",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 28, 6, 0, 0, 0, time.UTC), // Exactly at 06:00
			graceTime: 1 * time.Hour,
			want:      true,
		},
		{
			boundary: EndBoundary,
			name:     "at exact end of grace time window of end time",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 28, 7, 0, 0, 0, time.UTC), // Exactly at 07:00
			graceTime: 1 * time.Hour,
			want:      true,
		},
		{
			boundary: EndBoundary,
			name:     "wrong day - not in grace window",
			windows: []OffHourWindow{
				{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
			},
			now:       time.Date(2026, 1, 29, 6, 30, 0, 0, time.UTC), // Thursday (not Wednesday)
			graceTime: 1 * time.Hour,
			want:      false,
		},
		{
			boundary: EndBoundary,
			name:     "grace period with small buffer - seconds precision",
			windows: []OffHourWindow{
				{Start: "23:59", End: "00:00", DaysOfWeek: []string{"SUN"}},
			},
			now:       time.Date(2026, 2, 1, 0, 0, 30, 0, time.UTC), // 30 seconds after midnight
			graceTime: 1 * time.Minute,
			want:      true,
		},
		{
			boundary: EndBoundary,
			name:     "grace period expires at boundary",
			windows: []OffHourWindow{
				{Start: "23:59", End: "00:00", DaysOfWeek: []string{"SUN"}},
			},
			now:       time.Date(2026, 2, 1, 0, 1, 1, 0, time.UTC), // 61 seconds after midnight
			graceTime: 1 * time.Minute,
			want:      false,
		},
		{
			boundary: EndBoundary,
			name:     "grace period expires at boundary",
			windows: []OffHourWindow{
				{Start: "23:59", End: "00:00", DaysOfWeek: []string{"SUN"}},
			},
			now:       time.Date(2026, 2, 2, 0, 0, 1, 0, time.UTC), // 1 seconds after midnight
			graceTime: 1 * time.Minute,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInGraceTimeWindow(tt.boundary, tt.windows, tt.now, tt.graceTime)
			if got != tt.want {
				t.Errorf("isInGraceTimeWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScheduleEvaluator_ComplexSchedules(t *testing.T) {
	tests := []struct {
		name          string
		window        ScheduleWindow
		now           time.Time
		wantHibernate bool
		wantState     string
	}{
		{
			name: "overnight window - currently hibernating",
			window: ScheduleWindow{
				HibernateCron: "0 22 * * *", // 10 PM daily
				WakeUpCron:    "0 7 * * *",  // 7 AM daily
				Timezone:      "UTC",
			},
			now:           time.Date(2026, 2, 4, 2, 0, 0, 0, time.UTC), // 2 AM
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name: "overnight window - just before wake up",
			window: ScheduleWindow{
				HibernateCron: "0 22 * * *",
				WakeUpCron:    "0 7 * * *",
				Timezone:      "UTC",
			},
			now:           time.Date(2026, 2, 4, 6, 59, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name: "overnight window - just after wake up",
			window: ScheduleWindow{
				HibernateCron: "0 22 * * *",
				WakeUpCron:    "0 7 * * *",
				Timezone:      "UTC",
			},
			now:           time.Date(2026, 2, 4, 7, 1, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name: "weekend only schedule - Saturday",
			window: ScheduleWindow{
				HibernateCron: "0 0 * * 6", // Saturday midnight
				WakeUpCron:    "0 0 * * 1", // Monday midnight
				Timezone:      "UTC",
			},
			now:           time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC), // Saturday noon
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name: "weekend only schedule - Monday",
			window: ScheduleWindow{
				HibernateCron: "0 0 * * 6",
				WakeUpCron:    "0 0 * * 1",
				Timezone:      "UTC",
			},
			now:           time.Date(2026, 2, 2, 12, 0, 0, 0, time.UTC), // Monday noon
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name: "complex timezone - Asia/Tokyo during JST business hours",
			window: ScheduleWindow{
				HibernateCron: "0 19 * * 1-5", // 7 PM JST weekdays
				WakeUpCron:    "0 9 * * 1-5",  // 9 AM JST weekdays
				Timezone:      "Asia/Tokyo",
			},
			now:           mustParseTimeInLocation("2026-02-04T14:00:00", "Asia/Tokyo"), // 2 PM JST Wed
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name: "complex timezone - Asia/Tokyo after hours",
			window: ScheduleWindow{
				HibernateCron: "0 19 * * 1-5",
				WakeUpCron:    "0 9 * * 1-5",
				Timezone:      "Asia/Tokyo",
			},
			now:           mustParseTimeInLocation("2026-02-04T21:00:00", "Asia/Tokyo"), // 9 PM JST Wed
			wantHibernate: true,
			wantState:     "hibernated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := NewScheduleEvaluator(clocktesting.NewFakeClock(tt.now))
			result, err := evaluator.eval(tt.window)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.ShouldHibernate != tt.wantHibernate {
				t.Errorf("expected shouldHibernate=%v, got %v", tt.wantHibernate, result.ShouldHibernate)
			}
			if result.CurrentState != tt.wantState {
				t.Errorf("expected state=%q, got %q", tt.wantState, result.CurrentState)
			}
		})
	}
}

func TestScheduleEvaluator_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		window  ScheduleWindow
		now     time.Time
		wantErr bool
	}{
		{
			name: "empty cron expressions",
			window: ScheduleWindow{
				HibernateCron: "",
				WakeUpCron:    "",
				Timezone:      "UTC",
			},
			now:     time.Now(),
			wantErr: true,
		},
		{
			name: "malformed hibernate cron",
			window: ScheduleWindow{
				HibernateCron: "not a cron",
				WakeUpCron:    "0 6 * * *",
				Timezone:      "UTC",
			},
			now:     time.Now(),
			wantErr: true,
		},
		{
			name: "malformed wakeup cron",
			window: ScheduleWindow{
				HibernateCron: "0 20 * * *",
				WakeUpCron:    "invalid",
				Timezone:      "UTC",
			},
			now:     time.Now(),
			wantErr: true,
		},
		{
			name: "empty timezone defaults to UTC",
			window: ScheduleWindow{
				HibernateCron: "0 20 * * *",
				WakeUpCron:    "0 6 * * *",
				Timezone:      "",
			},
			now:     time.Now(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := NewScheduleEvaluator(clocktesting.NewFakeClock(tt.now))
			_, err := evaluator.eval(tt.window)
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error=%v, got error=%v", tt.wantErr, err)
			}
		})
	}
}

func TestParseWindowToCron_EdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		start         string
		end           string
		days          []string
		wantHibernate string
		wantWakeUp    string
		wantErr       bool
	}{
		{
			name:    "empty days",
			start:   "20:00",
			end:     "06:00",
			days:    []string{},
			wantErr: true,
		},
		{
			name:    "invalid time format - missing colon",
			start:   "2000",
			end:     "06:00",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:    "invalid time format - three parts",
			start:   "20:00:00",
			end:     "06:00",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:    "invalid hour",
			start:   "25:00",
			end:     "06:00",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:    "invalid minute",
			start:   "20:60",
			end:     "06:00",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:    "invalid day name",
			start:   "20:00",
			end:     "06:00",
			days:    []string{"MONDAY"},
			wantErr: true,
		},
		{
			name:    "empty days of week",
			start:   "20:00",
			end:     "06:00",
			days:    []string{},
			wantErr: true,
		},
		{
			name:    "midnight to midnight - same start and end (invalid per webhook validation)",
			start:   "00:00",
			end:     "00:00",
			days:    []string{"MON"},
			wantErr: true,
		},
		{
			name:          "all days of week",
			start:         "20:00",
			end:           "06:00",
			days:          []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
			wantHibernate: "0 20 * * 1,2,3,4,5,6,0",
			wantWakeUp:    "0 6 * * 1,2,3,4,5,6,0",
		},
		{
			name:          "case insensitive day names",
			start:         "20:00",
			end:           "06:00",
			days:          []string{"mon", "TUE", "Wed"},
			wantHibernate: "0 20 * * 1,2,3",
			wantWakeUp:    "0 6 * * 1,2,3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hibernate, wakeup, err := ParseWindowToCron(tt.start, tt.end, tt.days...)
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error=%v, got error=%v", tt.wantErr, err)
			}
			if !tt.wantErr {
				if hibernate != tt.wantHibernate {
					t.Errorf("hibernate cron: expected %q, got %q", tt.wantHibernate, hibernate)
				}
				if wakeup != tt.wantWakeUp {
					t.Errorf("wakeup cron: expected %q, got %q", tt.wantWakeUp, wakeup)
				}
			}
		})
	}
}

func TestScheduleEvaluator_RequeueCalculation(t *testing.T) {
	evaluator := NewScheduleEvaluator(clk)

	tests := []struct {
		name           string
		window         ScheduleWindow
		now            time.Time
		minRequeue     time.Duration
		maxRequeue     time.Duration
		wantRequeueMin bool
	}{
		{
			name: "requeue before next hibernate",
			window: ScheduleWindow{
				HibernateCron: "0 20 * * *", // 8 PM
				WakeUpCron:    "0 6 * * *",  // 6 AM
				Timezone:      "UTC",
			},
			now:            time.Date(2026, 2, 4, 10, 0, 0, 0, time.UTC), // 10 AM
			minRequeue:     1 * time.Minute,
			maxRequeue:     12 * time.Hour,
			wantRequeueMin: false,
		},
		{
			name: "requeue before next wakeup when hibernated",
			window: ScheduleWindow{
				HibernateCron: "0 20 * * *",
				WakeUpCron:    "0 6 * * *",
				Timezone:      "UTC",
			},
			now:            time.Date(2026, 2, 4, 23, 0, 0, 0, time.UTC), // 11 PM
			minRequeue:     1 * time.Minute,
			maxRequeue:     10 * time.Hour,
			wantRequeueMin: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator.Clock = clocktesting.NewFakeClock(tt.now)

			result, err := evaluator.eval(tt.window)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Calculate requeue time based on next transition
			var nextTransition time.Time
			if result.ShouldHibernate {
				nextTransition = result.NextWakeUpTime
			} else {
				nextTransition = result.NextHibernateTime
			}

			requeue := nextTransition.Sub(tt.now)
			if requeue < tt.minRequeue {
				t.Errorf("requeue %v is less than minimum %v", requeue, tt.minRequeue)
			}
			if requeue > tt.maxRequeue {
				t.Errorf("requeue %v is greater than maximum %v", requeue, tt.maxRequeue)
			}
		})
	}
}

// Helper function to parse time in specific timezone
func mustParseTimeInLocation(value, timezone string) time.Time {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		panic(err)
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", value, loc)
	if err != nil {
		panic(err)
	}
	return t
}

func TestEvaluate_MultiException(t *testing.T) {
	// Base schedule: weekday overnight hibernation 20:00–06:00 UTC.
	baseWindows := []OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
	}

	alwaysValid := func() (time.Time, time.Time) {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	}

	tests := []struct {
		name          string
		baseWindows   []OffHourWindow
		timezone      string
		exceptions    []*Exception
		now           time.Time
		wantHibernate bool
		wantState     string
	}{
		{
			name:          "nil exceptions - base schedule only",
			baseWindows:   baseWindows,
			timezone:      "UTC",
			exceptions:    nil,
			now:           time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC), // Wed 14:00
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "extend+suspend non-colliding - extend on Wed, suspend on Thu night",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "00:00", End: "23:59", DaysOfWeek: []string{"WED"}},
						},
					},
					{
						Type: ExceptionSuspend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "23:00", End: "03:00", DaysOfWeek: []string{"THU"}},
						},
					},
				}
			}(),
			// Wed 14:00 — base says active, but extend says hibernate (full day Wed)
			now:           time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			// Regression: extend on Wed, suspend on Thu with a large LeadTime that
			// would reach back into Wednesday if evaluateSuspend were called blindly.
			// The fix (isSuspendContextActive) must route through evaluateExtend on
			// Wednesday so the lead-time window for Thursday does NOT prevents
			// extend-driven hibernation on Wednesday.
			name:        "extend+suspend non-colliding - large suspend leadtime must not bleed into extend day",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "08:00", End: "18:00", DaysOfWeek: []string{"WED"}},
						},
					},
					{
						Type:      ExceptionSuspend,
						ValidFrom: vf, ValidUntil: vu,
						LeadTime: 20 * time.Hour, // 20 h lead time reaches back to Wed 13:00 for Thu 09:00
						Windows: []OffHourWindow{
							{Start: "09:00", End: "17:00", DaysOfWeek: []string{"THU"}},
						},
					},
				}
			}(),
			// Wed 15:00 — extend window 08:00-18:00 is active; suspend's 20 h lead
			// time would start at Wed 13:00, but it must NOT suppress Wed hibernation.
			now:           time.Date(2026, 1, 28, 15, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:        "extend+suspend - suspend carves out from extended base",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "00:00", End: "23:59", DaysOfWeek: []string{"WED"}},
						},
					},
					{
						Type: ExceptionSuspend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "12:00", End: "14:00", DaysOfWeek: []string{"WED"}},
						},
					},
				}
			}(),
			// Wed 13:00 — extend says hibernate all day, but suspend carves out 12:00-14:00
			now:           time.Date(2026, 1, 28, 13, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "replace - replaces base entirely",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionReplace, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							// Replace with 10:00-14:00 only
							{Start: "10:00", End: "14:00", DaysOfWeek: []string{"WED"}},
						},
					},
				}
			}(),
			// Wed 12:00 — in the replaced window
			now:           time.Date(2026, 1, 28, 12, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:        "replace - base schedule ignored",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionReplace, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "10:00", End: "14:00", DaysOfWeek: []string{"WED"}},
						},
					},
				}
			}(),
			// Wed 22:00 — normally base would hibernate, but replaced with 10:00-14:00 only
			now:           time.Date(2026, 1, 28, 22, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "replace+extend - extend adds on top of replaced base",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionReplace, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "10:00", End: "14:00", DaysOfWeek: []string{"WED"}},
						},
					},
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "18:00", End: "20:00", DaysOfWeek: []string{"WED"}},
						},
					},
				}
			}(),
			// Wed 19:00 — replaced base is 10:00-14:00, extend adds 18:00-20:00
			now:           time.Date(2026, 1, 28, 19, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:        "replace+suspend - suspend carves out from replaced base",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionReplace, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}},
						},
					},
					{
						Type: ExceptionSuspend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "22:00", End: "02:00", DaysOfWeek: []string{"WED"}},
						},
					},
				}
			}(),
			// Wed 23:00 — replaced base says hibernate, but suspend carves out 22:00-02:00
			now:           time.Date(2026, 1, 28, 23, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "replace+extend+suspend triple composition",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionReplace, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "22:00", End: "04:00", DaysOfWeek: []string{"WED"}},
						},
					},
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "10:00", End: "12:00", DaysOfWeek: []string{"WED"}},
						},
					},
					{
						Type: ExceptionSuspend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "11:00", End: "11:30", DaysOfWeek: []string{"WED"}},
						},
					},
				}
			}(),
			// Wed 11:15 — extendedBase = replace(22:00-04:00) + extend(10:00-12:00)
			// suspend carves out 11:00-11:30 → should NOT hibernate
			now:           time.Date(2026, 1, 28, 11, 15, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "expired exceptions ignored, base schedule used",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: []*Exception{
				{
					Type:       ExceptionSuspend,
					ValidFrom:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
					ValidUntil: time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC),
					Windows: []OffHourWindow{
						{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
					},
				},
			},
			// Wed 22:00 — exception expired, base should hibernate
			now:           time.Date(2026, 1, 28, 22, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		// ── Multi-window evaluation (merged same-type exceptions) ───────────
		{
			name:        "two merged extend windows - inside first window",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "09:00", End: "12:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
						},
					},
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "14:00", End: "17:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
						},
					},
				}
			}(),
			// Wed 10:00 — inside extend-morning (09:00-12:00)
			now:           time.Date(2026, 1, 28, 10, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:        "two merged extend windows - inside second window",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "09:00", End: "12:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
						},
					},
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "14:00", End: "17:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
						},
					},
				}
			}(),
			// Wed 15:00 — inside extend-evening (14:00-17:00), was silently dropped before fix
			now:           time.Date(2026, 1, 28, 15, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:        "two merged extend windows - in gap between both",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exceptions: func() []*Exception {
				vf, vu := alwaysValid()
				return []*Exception{
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "09:00", End: "12:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
						},
					},
					{
						Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu,
						Windows: []OffHourWindow{
							{Start: "14:00", End: "17:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
						},
					},
				}
			}(),
			// Wed 13:00 — between extend-morning and extend-evening, base also says active
			now:           time.Date(2026, 1, 28, 13, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := NewScheduleEvaluator(clocktesting.NewFakeClock(tt.now), WithScheduleBuffer("1m"))
			result, err := evaluator.Evaluate(tt.baseWindows, tt.timezone, tt.exceptions)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}

			if result.ShouldHibernate != tt.wantHibernate {
				t.Errorf("ShouldHibernate = %v, want %v (state=%s)", result.ShouldHibernate, tt.wantHibernate, result.CurrentState)
			}

			if result.CurrentState != tt.wantState {
				t.Errorf("CurrentState = %v, want %v", result.CurrentState, tt.wantState)
			}
		})
	}
}
