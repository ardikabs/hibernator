/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// Gate defines a function that checks if a transition should be intercepted.
// It returns a Handler to redirect the flow, or nil to "pass" through the gate.
type Gate func(s *state) Handler

// deletionGate checks plans that are being deleted, routing them to finalizer
// cleanup regardless of their current phase.
//
// Returns a lifecycleState in delete mode when DeletionTimestamp is set and non-zero;
// returns nil (pass through) otherwise.
func deletionGate(s *state) Handler {
	plan := s.plan()

	if !plan.DeletionTimestamp.IsZero() {
		return &lifecycleState{state: s, delete: true}
	}

	return nil
}

// suspensionGate acts as the gatekeeper for entering the suspended state.
// It implements the priority logic for manual vs. automated suspension.
func suspensionGate(s *state) Handler {
	plan := s.plan()

	// 1. Bypass check: If we are already there, the gate is open.
	if plan.Status.Phase == hibernatorv1alpha1.PhaseSuspended {
		return nil
	}

	// 2. The "Manual-Intent" Gate
	if plan.Spec.Suspend {
		return &preSuspensionState{state: s}
	}

	// 3. The "Auto-Suspension" Gate
	return s.checkAutoSuspendAnnotation()
}

func (s *state) checkAutoSuspendAnnotation() Handler {
	plan := s.plan()
	val, ok := plan.Annotations[wellknown.AnnotationSuspendUntil]
	if !ok {
		return nil
	}

	deadline, err := time.Parse(time.RFC3339, val)
	if err != nil {
		s.Log.Error(err, "invalid suspend-until format, ignoring...",
			"plan", s.Key.String(),
			"suspend-until", val)
		return nil
	}

	now := s.Clock.Now()
	if now.Before(deadline) {
		return &preSuspensionState{state: s}
	}

	s.Log.Info("suspend-until deadline is already in the past, ignoring...",
		"plan", s.Key.String(),
		"suspend-until", val)

	return nil
}
