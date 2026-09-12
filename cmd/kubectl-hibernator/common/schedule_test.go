/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/scheduler"
)

func TestConvertAPIWindows(t *testing.T) {
	out := ConvertAPIWindows([]hibernatorv1alpha1.OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "FRI"}},
	})
	require.Equal(t, []scheduler.OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "FRI"}},
	}, out)
	require.Empty(t, ConvertAPIWindows(nil))
}

func TestConvertAPIException(t *testing.T) {
	exc := hibernatorv1alpha1.ScheduleException{}
	exc.Spec.Type = hibernatorv1alpha1.ExceptionSuspend
	exc.Spec.LeadTime = "30m"
	exc.Spec.Windows = []hibernatorv1alpha1.OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"WED"}},
	}

	got := ConvertAPIException(exc)
	require.Equal(t, scheduler.ExceptionType(hibernatorv1alpha1.ExceptionSuspend), got.Type)
	require.Equal(t, 30*time.Minute, got.LeadTime)
	require.Len(t, got.Windows, 1)
	require.Equal(t, "20:00", got.Windows[0].Start)

	// Bad durations degrade to zero rather than failing conversion.
	exc.Spec.LeadTime = "not-a-duration"
	require.Equal(t, time.Duration(0), ConvertAPIException(exc).LeadTime)
}

func TestComputeUpcomingEvents(t *testing.T) {
	// Empty windows are a usage error, not an empty list.
	_, err := ComputeUpcomingEvents(nil, "UTC", nil, 5)
	require.ErrorContains(t, err, "no base windows defined")

	// Shape is day-independent: events alternate Hibernate/WakeUp, advance
	// in time, and start in the future (ComputeUpcomingEvents uses the
	// real clock, so no absolute anchors are asserted here).
	windows := []scheduler.OffHourWindow{
		{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
	}
	events, err := ComputeUpcomingEvents(windows, "UTC", nil, 4)
	require.NoError(t, err)
	require.Len(t, events, 4)
	require.True(t, events[0].Time.After(time.Now()))
	for i := 1; i < len(events); i++ {
		require.True(t, events[i].Time.After(events[i-1].Time), "events must advance")
		require.NotEqual(t, events[i].Operation, events[i-1].Operation, "events must alternate")
		require.Greater(t, events[i].In, events[i-1].In)
	}
}
