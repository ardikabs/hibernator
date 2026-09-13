/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func TestFormatAge(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{0, "<1m"},
		{30 * time.Second, "<1m"},
		{5 * time.Minute, "5m"},
		{59 * time.Minute, "59m"},
		{2 * time.Hour, "2h"},
		{23 * time.Hour, "23h"},
		{26 * time.Hour, "1d"},
		{29 * 24 * time.Hour, "29d"},
		{30 * 24 * time.Hour, "1mo"},
		{75 * 24 * time.Hour, "2mo"},
	} {
		require.Equal(t, tc.want, FormatAge(tc.in), "FormatAge(%s)", tc.in)
	}
}

func TestHumanDuration(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "past"},
		{0, "0s"},
		{45 * time.Second, "45s"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
		{26 * time.Hour, "1d2h"},
		{48 * time.Hour, "2d"},
	} {
		require.Equal(t, tc.want, HumanDuration(tc.in), "HumanDuration(%s)", tc.in)
	}
}

func TestStateIcon(t *testing.T) {
	for state, want := range map[hibernatorv1alpha1.ExecutionState]string{
		hibernatorv1alpha1.StateCompleted:          "[OK]",
		hibernatorv1alpha1.StateFailed:             "[FAIL]",
		hibernatorv1alpha1.StateAborted:            "[SKIP]",
		hibernatorv1alpha1.StateSkipped:            "[SKIP]",
		hibernatorv1alpha1.StateRunning:            "[..]",
		hibernatorv1alpha1.StatePending:            "[--]",
		hibernatorv1alpha1.ExecutionState("bogus"): "[??]",
	} {
		require.Equal(t, want, StateIcon(state), "StateIcon(%q)", state)
	}
}

func TestFormatNextEventNil(t *testing.T) {
	require.Equal(t, "-", FormatNextEvent(nil))
}
