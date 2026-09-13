/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"testing"

	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func TestIsValidEvent(t *testing.T) {
	for _, e := range []string{"Start", "Success", "Failure", "Recovery", "PhaseChange"} {
		require.True(t, isValidEvent(e), "event %q", e)
	}
	for _, e := range []string{"", "success", "START", "bogus"} {
		require.False(t, isValidEvent(e), "event %q", e)
	}
}

func TestIsValidSinkType(t *testing.T) {
	for _, s := range []string{"slack", "telegram", "webhook"} {
		require.True(t, isValidSinkType(s), "sink %q", s)
	}
	require.False(t, isValidSinkType("Slack"))
	require.False(t, isValidSinkType(""))
	require.False(t, isValidSinkType("email"))
}

func TestCanonicalEvent(t *testing.T) {
	for _, e := range []string{"Start", "Success", "Failure", "Recovery", "PhaseChange"} {
		got, ok := canonicalEvent(e)
		require.True(t, ok)
		require.Equal(t, e, got)
	}
	// Case-insensitive input normalizes to canonical form.
	got, ok := canonicalEvent("success")
	require.True(t, ok)
	require.Equal(t, "Success", got)
	got, ok = canonicalEvent("PHASECHANGE")
	require.True(t, ok)
	require.Equal(t, "PhaseChange", got)

	_, ok = canonicalEvent("bogus")
	require.False(t, ok)
	_, ok = canonicalEvent("")
	require.False(t, ok)
}

func TestResolveSink(t *testing.T) {
	sinks := []hibernatorv1alpha1.NotificationSink{{Name: "a"}, {Name: "b"}}

	// Single sink auto-selects.
	only, err := resolveSink(sinks[:1], "")
	require.NoError(t, err)
	require.Equal(t, "a", only.Name)

	// Ambiguous without --sink.
	_, err = resolveSink(sinks, "")
	require.ErrorContains(t, err, "multiple sinks")

	// Explicit lookup hits and misses.
	got, err := resolveSink(sinks, "b")
	require.NoError(t, err)
	require.Equal(t, "b", got.Name)
	_, err = resolveSink(sinks, "nope")
	require.ErrorContains(t, err, `sink "nope" not found`)
}

func TestDefaultPhaseForEvent(t *testing.T) {
	require.Equal(t, string(hibernatorv1alpha1.PhaseHibernating), defaultPhaseForEvent("Start"))
	require.Equal(t, string(hibernatorv1alpha1.PhaseHibernated), defaultPhaseForEvent("Success"))
	require.Equal(t, string(hibernatorv1alpha1.PhaseError), defaultPhaseForEvent("Failure"))
	require.Equal(t, string(hibernatorv1alpha1.PhaseActive), defaultPhaseForEvent("bogus"))
}

func TestPreviousPhaseForEvent(t *testing.T) {
	require.Equal(t, string(hibernatorv1alpha1.PhaseHibernated),
		previousPhaseForEvent("Start", string(hibernatorv1alpha1.PhaseWakingUp)))
	require.Equal(t, string(hibernatorv1alpha1.PhaseActive),
		previousPhaseForEvent("Start", string(hibernatorv1alpha1.PhaseHibernating)))
	require.Equal(t, string(hibernatorv1alpha1.PhaseWakingUp),
		previousPhaseForEvent("Success", string(hibernatorv1alpha1.PhaseActive)))
	require.Equal(t, string(hibernatorv1alpha1.PhaseError),
		previousPhaseForEvent("Recovery", string(hibernatorv1alpha1.PhaseHibernating)))
	require.Equal(t, "", previousPhaseForEvent("bogus", "Active"))
}
