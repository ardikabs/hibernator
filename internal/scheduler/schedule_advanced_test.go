/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduler

import (
	"testing"
	"time"
)

func TestScheduleEvaluator_ComplexSchedules(t *testing.T) {
	evaluator := NewScheduleEvaluator()

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
			result, err := evaluator.Evaluate(tt.window, tt.now)
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
	evaluator := NewScheduleEvaluator()

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
			_, err := evaluator.Evaluate(tt.window, tt.now)
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error=%v, got error=%v", tt.wantErr, err)
			}
		})
	}
}

func TestConvertOffHoursToCron_EdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		windows       []OffHourWindow
		wantHibernate string
		wantWakeUp    string
		wantErr       bool
	}{
		{
			name:    "empty windows",
			windows: []OffHourWindow{},
			wantErr: true,
		},
		{
			name: "invalid time format - missing colon",
			windows: []OffHourWindow{
				{
					Start:      "2000",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid time format - three parts",
			windows: []OffHourWindow{
				{
					Start:      "20:00:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid hour",
			windows: []OffHourWindow{
				{
					Start:      "25:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid minute",
			windows: []OffHourWindow{
				{
					Start:      "20:60",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid day name",
			windows: []OffHourWindow{
				{
					Start:      "20:00",
					End:        "06:00",
					DaysOfWeek: []string{"MONDAY"},
				},
			},
			wantErr: true,
		},
		{
			name: "empty days of week",
			windows: []OffHourWindow{
				{
					Start:      "20:00",
					End:        "06:00",
					DaysOfWeek: []string{},
				},
			},
			wantErr: true,
		},
		{
			name: "midnight to midnight",
			windows: []OffHourWindow{
				{
					Start:      "00:00",
					End:        "00:00",
					DaysOfWeek: []string{"MON"},
				},
			},
			wantHibernate: "0 0 * * 1",
			wantWakeUp:    "0 0 * * 1",
		},
		{
			name: "all days of week",
			windows: []OffHourWindow{
				{
					Start:      "20:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
				},
			},
			wantHibernate: "0 20 * * 1,2,3,4,5,6,0",
			wantWakeUp:    "0 6 * * 2,3,4,5,6,0,1",
		},
		{
			name: "case insensitive day names",
			windows: []OffHourWindow{
				{
					Start:      "20:00",
					End:        "06:00",
					DaysOfWeek: []string{"mon", "TUE", "Wed"},
				},
			},
			wantHibernate: "0 20 * * 1,2,3",
			wantWakeUp:    "0 6 * * 2,3,4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hibernate, wakeup, err := ConvertOffHoursToCron(tt.windows)
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
	evaluator := NewScheduleEvaluator()

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
			result, err := evaluator.Evaluate(tt.window, tt.now)
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
