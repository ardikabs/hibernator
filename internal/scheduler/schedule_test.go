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
		name            string
		window          ScheduleWindow
		now             time.Time
		wantHibernate   bool
		wantState       string
		wantErr         bool
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
