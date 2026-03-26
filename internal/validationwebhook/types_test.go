/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"testing"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func TestWindowsCollide(t *testing.T) {
	tests := []struct {
		name    string
		a       []hibernatorv1alpha1.OffHourWindow
		b       []hibernatorv1alpha1.OffHourWindow
		collide bool
	}{
		{
			name:    "no shared day",
			a:       []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"MON", "TUE"}}},
			b:       []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"WED", "THU"}}},
			collide: false,
		},
		{
			name:    "shared day, same time range",
			a:       []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"MON"}}},
			b:       []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"MON"}}},
			collide: true,
		},
		{
			name:    "shared day, overlapping time range",
			a:       []hibernatorv1alpha1.OffHourWindow{{Start: "08:00", End: "12:00", DaysOfWeek: []string{"MON"}}},
			b:       []hibernatorv1alpha1.OffHourWindow{{Start: "10:00", End: "14:00", DaysOfWeek: []string{"MON"}}},
			collide: true,
		},
		{
			name:    "shared day, non-overlapping time range",
			a:       []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "10:00", DaysOfWeek: []string{"MON"}}},
			b:       []hibernatorv1alpha1.OffHourWindow{{Start: "14:00", End: "18:00", DaysOfWeek: []string{"MON"}}},
			collide: false,
		},
		{
			name:    "overnight window A overlaps morning B on shared day",
			a:       []hibernatorv1alpha1.OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
			b:       []hibernatorv1alpha1.OffHourWindow{{Start: "04:00", End: "08:00", DaysOfWeek: []string{"MON"}}},
			collide: true, // 04:00-06:00 overlaps
		},
		{
			name:    "overnight window A, non-overlapping B on shared day",
			a:       []hibernatorv1alpha1.OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
			b:       []hibernatorv1alpha1.OffHourWindow{{Start: "08:00", End: "12:00", DaysOfWeek: []string{"MON"}}},
			collide: false,
		},
		{
			name:    "both overnight windows, shared day",
			a:       []hibernatorv1alpha1.OffHourWindow{{Start: "22:00", End: "04:00", DaysOfWeek: []string{"MON"}}},
			b:       []hibernatorv1alpha1.OffHourWindow{{Start: "23:00", End: "05:00", DaysOfWeek: []string{"MON"}}},
			collide: true,
		},
		{
			name: "multiple windows, one pair collides",
			a: []hibernatorv1alpha1.OffHourWindow{
				{Start: "06:00", End: "10:00", DaysOfWeek: []string{"MON"}},
				{Start: "14:00", End: "18:00", DaysOfWeek: []string{"WED"}},
			},
			b: []hibernatorv1alpha1.OffHourWindow{
				{Start: "15:00", End: "17:00", DaysOfWeek: []string{"WED"}},
			},
			collide: true,
		},
		{
			name:    "adjacent windows, no overlap",
			a:       []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "10:00", DaysOfWeek: []string{"MON"}}},
			b:       []hibernatorv1alpha1.OffHourWindow{{Start: "10:00", End: "14:00", DaysOfWeek: []string{"MON"}}},
			collide: false, // endpoint-exclusive: [06:00,10:00) and [10:00,14:00) don't overlap
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := windowsCollide(tt.a, tt.b)
			if got != tt.collide {
				t.Errorf("windowsCollide() = %v, want %v", got, tt.collide)
			}
		})
	}
}

func TestIsAllowedTypePair(t *testing.T) {
	tests := []struct {
		a    hibernatorv1alpha1.ExceptionType
		b    hibernatorv1alpha1.ExceptionType
		want bool
	}{
		{hibernatorv1alpha1.ExceptionExtend, hibernatorv1alpha1.ExceptionSuspend, true},
		{hibernatorv1alpha1.ExceptionSuspend, hibernatorv1alpha1.ExceptionExtend, true},
		{hibernatorv1alpha1.ExceptionReplace, hibernatorv1alpha1.ExceptionExtend, true},
		{hibernatorv1alpha1.ExceptionExtend, hibernatorv1alpha1.ExceptionReplace, true},
		{hibernatorv1alpha1.ExceptionReplace, hibernatorv1alpha1.ExceptionSuspend, true},
		{hibernatorv1alpha1.ExceptionSuspend, hibernatorv1alpha1.ExceptionReplace, true},
		{hibernatorv1alpha1.ExceptionExtend, hibernatorv1alpha1.ExceptionExtend, false},
		{hibernatorv1alpha1.ExceptionSuspend, hibernatorv1alpha1.ExceptionSuspend, false},
		{hibernatorv1alpha1.ExceptionReplace, hibernatorv1alpha1.ExceptionReplace, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.a)+"_"+string(tt.b), func(t *testing.T) {
			got := isAllowedTypePair(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("isAllowedTypePair(%s, %s) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
