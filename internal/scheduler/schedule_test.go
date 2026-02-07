/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduler

import (
	"testing"
	"time"
)

func TestScheduleEvaluator_Evaluate(t *testing.T) {
	evaluator := NewScheduleEvaluator()

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
			result, err := evaluator.Evaluate(tt.window, tt.now)
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

func TestScheduleEvaluator_ValidateCron(t *testing.T) {
	evaluator := NewScheduleEvaluator()

	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{"valid 5-field cron", "0 20 * * 1-5", false},
		{"valid every hour", "0 * * * *", false},
		{"valid complex", "30 8,12,18 * * 1-5", false},
		{"invalid syntax", "invalid", true},
		{"too few fields", "* *", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := evaluator.ValidateCron(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCron() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestScheduleEvaluator_NextRequeueTime(t *testing.T) {
	evaluator := NewScheduleEvaluator()

	now := time.Date(2026, 1, 28, 14, 0, 0, 0, time.UTC) // Wednesday 2 PM

	window := ScheduleWindow{
		HibernateCron: "0 20 * * 1-5", // 8 PM weekdays
		WakeUpCron:    "0 6 * * 1-5",  // 6 AM weekdays
		Timezone:      "UTC",
	}

	result, err := evaluator.Evaluate(window, now)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	requeueDuration := evaluator.NextRequeueTime(result, now)

	// Currently active (2 PM), next event is hibernate at 8 PM = 6 hours + 10s buffer
	expectedDuration := 6*time.Hour + 10*time.Second
	if requeueDuration != expectedDuration {
		t.Errorf("NextRequeueTime() = %v, want %v", requeueDuration, expectedDuration)
	}
}

func TestConvertOffHoursToCron(t *testing.T) {
	tests := []struct {
		name              string
		windows           []OffHourWindow
		wantHibernateCron string
		wantWakeUpCron    string
		wantErr           bool
	}{
		{
			name: "valid single window weekdays",
			windows: []OffHourWindow{
				{
					Start:      "20:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
				},
			},
			wantHibernateCron: "0 20 * * 1,2,3,4,5",
			wantWakeUpCron:    "0 6 * * 1,2,3,4,5",
			wantErr:           false,
		},
		{
			name: "valid single window with all days",
			windows: []OffHourWindow{
				{
					Start:      "22:30",
					End:        "08:15",
					DaysOfWeek: []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"},
				},
			},
			wantHibernateCron: "30 22 * * 0,1,2,3,4,5,6",
			wantWakeUpCron:    "15 8 * * 0,1,2,3,4,5,6",
			wantErr:           false,
		},
		{
			name: "valid single window weekend only",
			windows: []OffHourWindow{
				{
					Start:      "23:00",
					End:        "07:00",
					DaysOfWeek: []string{"SAT", "SUN"},
				},
			},
			wantHibernateCron: "0 23 * * 6,0",
			wantWakeUpCron:    "0 7 * * 6,0",
			wantErr:           false,
		},
		{
			name: "overnight window end before start",
			windows: []OffHourWindow{
				{
					Start:      "20:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				},
			},
			wantHibernateCron: "0 20 * * 1",
			wantWakeUpCron:    "0 6 * * 1",
			wantErr:           false,
		},
		{
			name: "case insensitive days",
			windows: []OffHourWindow{
				{
					Start:      "18:00",
					End:        "09:00",
					DaysOfWeek: []string{"mon", "Wed", "FRI"},
				},
			},
			wantHibernateCron: "0 18 * * 1,3,5",
			wantWakeUpCron:    "0 9 * * 1,3,5",
			wantErr:           false,
		},
		{
			name:    "empty windows",
			windows: []OffHourWindow{},
			wantErr: true,
		},
		{
			name: "invalid start time format",
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
			name: "invalid start time hour",
			windows: []OffHourWindow{
				{
					Start:      "24:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid start time minute",
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
			name: "invalid end time format missing leading zero",
			windows: []OffHourWindow{
				{
					Start:      "20:00",
					End:        "6",
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
			name: "invalid day name mixed",
			windows: []OffHourWindow{
				{
					Start:      "20:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON", "INVALID", "WED"},
				},
			},
			wantErr: true,
		},
		{
			name: "malformed time no colon",
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
			name: "negative hour",
			windows: []OffHourWindow{
				{
					Start:      "-1:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				},
			},
			wantErr: true,
		},
		{
			name: "negative hour",
			windows: []OffHourWindow{
				{
					Start:      "-1:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hibernateCron, wakeUpCron, err := ConvertOffHoursToCron(tt.windows)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertOffHoursToCron() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if hibernateCron != tt.wantHibernateCron {
				t.Errorf("ConvertOffHoursToCron() hibernateCron = %v, want %v", hibernateCron, tt.wantHibernateCron)
			}

			if wakeUpCron != tt.wantWakeUpCron {
				t.Errorf("ConvertOffHoursToCron() wakeUpCron = %v, want %v", wakeUpCron, tt.wantWakeUpCron)
			}
		})
	}
}

func TestScheduleEvaluator_EvaluateWithException(t *testing.T) {
	evaluator := NewScheduleEvaluator()

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
			name:        "extend exception - tentatively add weekend schedule (on Saturday 00:00)",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionExtend,
				ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				Windows: []OffHourWindow{
					{Start: "12:00", End: "00:00", DaysOfWeek: []string{"SAT", "SUN"}},
				},
			},
			// Friday 20:00 hibernated, Wakeup at 00:00 Saturday then hibernate again from 12:00
			now:           time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "extend exception - tentatively add weekend schedule (on Monday 00:00)",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionExtend,
				ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				Windows: []OffHourWindow{
					{Start: "12:00", End: "00:00", DaysOfWeek: []string{"SAT", "SUN"}},
				},
			},
			now:           time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC),
			wantHibernate: true,
			wantState:     "hibernated",
		},
		{
			name:        "extend exception - tentatively add weekend schedule (on Monday 06:00)",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionExtend,
				ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				Windows: []OffHourWindow{
					{Start: "12:00", End: "00:00", DaysOfWeek: []string{"SAT", "SUN"}},
				},
			},
			now:           time.Date(2026, 2, 2, 6, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
		},
		{
			name:        "extend exception - still active outside both windows",
			baseWindows: baseWindows,
			timezone:    "UTC",
			exception: &Exception{
				Type:       ExceptionExtend,
				ValidFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				ValidUntil: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
				Windows: []OffHourWindow{
					{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT", "SUN"}},
				},
			},
			// Saturday 2 PM UTC - outside both base (weekday) and exception (6-11 Saturday and Sunday)
			now:           time.Date(2026, 1, 31, 14, 0, 0, 0, time.UTC),
			wantHibernate: false,
			wantState:     "active",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluator.EvaluateWithException(tt.baseWindows, tt.timezone, tt.exception, tt.now)

			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateWithException() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if result.ShouldHibernate != tt.wantHibernate {
				t.Errorf("EvaluateWithException() ShouldHibernate = %v, want %v", result.ShouldHibernate, tt.wantHibernate)
			}

			if result.CurrentState != tt.wantState {
				t.Errorf("EvaluateWithException() CurrentState = %v, want %v", result.CurrentState, tt.wantState)
			}
		})
	}
}

func TestScheduleEvaluator_isInTimeWindows(t *testing.T) {
	evaluator := NewScheduleEvaluator()

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
			got := evaluator.isInTimeWindows(tt.windows, tt.now)
			if got != tt.want {
				t.Errorf("isInTimeWindows() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScheduleEvaluator_isInLeadTimeWindow(t *testing.T) {
	evaluator := NewScheduleEvaluator()

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
			got := evaluator.isInLeadTimeWindow(tt.windows, tt.now, tt.leadTime)
			if got != tt.want {
				t.Errorf("isInLeadTimeWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

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
