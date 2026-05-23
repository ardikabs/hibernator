/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package timeparse

import (
	"strings"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"
)

func TestParseDeadline_NaturalLanguage(t *testing.T) {
	clk := clocktesting.NewFakeClock(
		time.Date(2026, time.May, 15, 13, 10, 0, 0, time.UTC))
	now := clk.Now().UTC()

	tests := []struct {
		name       string
		input      string
		wantAfter  time.Duration // Expected minimum duration from now
		wantBefore time.Duration // Expected maximum duration from now
	}{
		{
			name:       "in 30 minutes",
			input:      "in 30 minutes",
			wantAfter:  29 * time.Minute,
			wantBefore: 31 * time.Minute,
		},
		{
			name:       "in 2 hours",
			input:      "in 2 hours",
			wantAfter:  119 * time.Minute,
			wantBefore: 121 * time.Minute,
		},
		{
			name:       "in 1 day",
			input:      "in 1 day",
			wantAfter:  23 * time.Hour,
			wantBefore: 25 * time.Hour,
		},
		{
			name:       "in 30 mins",
			input:      "in 30 mins",
			wantAfter:  29 * time.Minute,
			wantBefore: 31 * time.Minute,
		},
		{
			name:       "in 2 hrs",
			input:      "in 2 hrs",
			wantAfter:  119 * time.Minute,
			wantBefore: 121 * time.Minute,
		},
		{
			name:       "tomorrow",
			input:      "tomorrow",
			wantAfter:  20 * time.Hour,
			wantBefore: 28 * time.Hour,
		},
		{
			name:       "tomorrow at 6am",
			input:      "tomorrow at 6am",
			wantAfter:  16 * time.Hour,
			wantBefore: 28 * time.Hour,
		},
		{
			name:       "tomorrow at 14:30",
			input:      "tomorrow at 14:30",
			wantAfter:  20 * time.Hour,
			wantBefore: 28 * time.Hour,
		},
		{
			name:       "tomorrow 2:30pm",
			input:      "tomorrow 2:30pm",
			wantAfter:  20 * time.Hour,
			wantBefore: 28 * time.Hour,
		},
		{
			name:       "next week",
			input:      "next week",
			wantAfter:  6 * 24 * time.Hour,
			wantBefore: 8 * 24 * time.Hour,
		},
		{
			name:       "next monday",
			input:      "next monday",
			wantAfter:  0,
			wantBefore: 8 * 24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDeadline(tt.input, now)
			if err != nil {
				t.Fatalf("ParseDeadline(%q) error = %v", tt.input, err)
			}

			diff := got.Sub(now)

			if diff < tt.wantAfter {
				t.Errorf("ParseDeadline(%q) = %v, want at least %v from now (got %v)",
					tt.input, got, tt.wantAfter, diff)
			}
			if diff > tt.wantBefore {
				t.Errorf("ParseDeadline(%q) = %v, want at most %v from now (got %v)",
					tt.input, got, tt.wantBefore, diff)
			}
		})
	}
}

func TestParseDeadline_SimpleFormats(t *testing.T) {
	// Use fake clock for deterministic testing
	clk := clocktesting.NewFakeClock(
		time.Date(2026, time.May, 15, 13, 10, 0, 0, time.UTC))
	now := clk.Now()
	tomorrow := now.Add(24 * time.Hour)

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "ISO date only",
			input:   tomorrow.Format("2006-01-02"),
			wantErr: false,
		},
		{
			name:    "ISO date with time",
			input:   tomorrow.Format("2006-01-02 15:04"),
			wantErr: false,
		},
		{
			name:    "ISO date with seconds",
			input:   tomorrow.Format("2006-01-02 15:04:05"),
			wantErr: false,
		},
		{
			name:    "Readable date",
			input:   tomorrow.Format("Jan 2, 2006"),
			wantErr: false,
		},
		{
			name:    "Readable date with time",
			input:   tomorrow.Format("Jan 2, 2006 15:04"),
			wantErr: false,
		},
		{
			name:    "Full month name",
			input:   tomorrow.Format("January 2, 2006"),
			wantErr: false,
		},
		{
			name:    "Past date should fail",
			input:   "2020-01-01",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDeadline(tt.input, now)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseDeadline(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDeadline(%q) error = %v", tt.input, err)
			}
			if got.IsZero() {
				t.Errorf("ParseDeadline(%q) returned zero time", tt.input)
			}
		})
	}
}

func TestParseDeadline_RFC3339(t *testing.T) {
	// Use fake clock for deterministic testing
	clk := clocktesting.NewFakeClock(
		time.Date(2026, time.May, 15, 13, 10, 0, 0, time.UTC))
	now := clk.Now()

	// Create a future time based on fake clock
	future := now.Add(24 * time.Hour).UTC()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "RFC3339 with Z",
			input:   future.Format(time.RFC3339),
			wantErr: false,
		},
		{
			name:    "RFC3339 with offset",
			input:   future.Format("2006-01-02T15:04:05+07:00"),
			wantErr: false,
		},
		{
			name:    "Past RFC3339 should fail",
			input:   "2020-01-01T00:00:00Z",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDeadline(tt.input, now)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseDeadline(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDeadline(%q) error = %v", tt.input, err)
			}
			if got.IsZero() {
				t.Errorf("ParseDeadline(%q) returned zero time", tt.input)
			}
		})
	}
}

func TestParseDeadline_Errors(t *testing.T) {
	// Use fake clock for deterministic testing
	clk := clocktesting.NewFakeClock(
		time.Date(2026, time.May, 15, 13, 10, 0, 0, time.UTC))
	now := clk.Now()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "empty string",
			input: "",
		},
		{
			name:  "whitespace only",
			input: "   ",
		},
		{
			name:  "past time should fail",
			input: "2020-01-01",
		},
		{
			name:  "past RFC3339 should fail",
			input: "2020-01-01T00:00:00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDeadline(tt.input, now)
			if err == nil {
				t.Errorf("ParseDeadline(%q) = %v, want error", tt.input, got)
			}
		})
	}
}

func TestFormatDeadline(t *testing.T) {
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{
			name: "zero time",
			t:    time.Time{},
			want: "no deadline",
		},
		{
			name: "normal time",
			t:    time.Date(2026, 1, 15, 14, 30, 0, 0, time.UTC),
			want: "local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDeadline(tt.t)
			if tt.want == "no deadline" {
				if got != "no deadline" {
					t.Errorf("FormatDeadline() = %v, want %v", got, tt.want)
				}
			} else {
				// Just check it contains expected parts
				if !strings.Contains(got, "local") || !strings.Contains(got, "UTC") {
					t.Errorf("FormatDeadline() = %v, should contain 'local' and 'UTC'", got)
				}
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	// Use fake clock for deterministic testing
	clk := clocktesting.NewFakeClock(
		time.Date(2026, time.May, 15, 13, 10, 0, 0, time.UTC))
	now := clk.Now()

	tests := []struct {
		name         string
		d            time.Duration
		wantContains string
	}{
		{
			name:         "30 seconds",
			d:            30 * time.Second,
			wantContains: "minute",
		},
		{
			name:         "5 minutes",
			d:            5 * time.Minute,
			wantContains: "minutes",
		},
		{
			name:         "1 minute",
			d:            1 * time.Minute,
			wantContains: "minute",
		},
		{
			name:         "2 hours",
			d:            2 * time.Hour,
			wantContains: "hour",
		},
		{
			name:         "1 hour 30 minutes",
			d:            90 * time.Minute,
			wantContains: "hour",
		},
		{
			name:         "1 day",
			d:            24 * time.Hour,
			wantContains: "day",
		},
		{
			name:         "2 days",
			d:            48 * time.Hour,
			wantContains: "days",
		},
		{
			name:         "2 days 6 hours",
			d:            54 * time.Hour,
			wantContains: "days",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			until := now.Add(tt.d)
			got := FormatDuration(now, until)
			if !strings.Contains(got, tt.wantContains) {
				t.Errorf("FormatDuration() = %v, should contain %v", got, tt.wantContains)
			}
		})
	}
}
