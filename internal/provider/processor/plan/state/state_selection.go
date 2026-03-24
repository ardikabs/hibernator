/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// selectHandler returns the phase-appropriate Handler for the given state.
// Dispatch follows a strict priority order:
//
//  1. Deletion in progress (DeletionTimestamp set) — returns a lifecycleState
//     configured for finalizer cleanup, regardless of the current phase.
//
//  2. Suspension requested (Spec.Suspend=true) but not yet reflected in status —
//     returns an inline handler that calls TransitionToSuspended, which drains
//     any in-flight executions before writing PhaseSuspended.
//
//  3. Phase-based dispatch — maps Status.Phase to its dedicated handler:
//     - ""               → lifecycleState (initialisation / first-time setup)
//     - PhaseActive      → selectIdleHandler (annotation-aware idle routing)
//     - PhaseHibernated  → selectIdleHandler (annotation-aware idle routing)
//     - PhaseHibernating → hibernatingState (job orchestration for shutdown)
//     - PhaseWakingUp    → wakingUpState (job orchestration for wakeup)
//     - PhaseSuspended   → suspendedState (suspended until resume)
//     - PhaseError       → recoveryState (retry / manual recovery)
//     - unknown phase    → nil (caller should treat as a no-op)
//
// For active/hibernated phases the concrete handler is chosen by selectIdleHandler.
//
// The selected handler is wrapped with an observation pipeline that executes
// phase-agnostic observers (exceptionReferences, metrics, conditions, etc.) before
// delegating to phase-specific handling. The pipeline is instantiated fresh on each
// selectHandler call, allowing observations to be correctly scoped to each state.
func selectHandler(s *state) Handler {
	plan := s.plan()

	// Deletion in progress — run finalizer cleanup regardless of phase.
	if !plan.DeletionTimestamp.IsZero() {
		return &lifecycleState{state: s, delete: true}
	}
	if plan.Spec.Suspend &&
		plan.Status.Phase != hibernatorv1alpha1.PhaseSuspended {
		// Suspension requested but not yet in PhaseSuspended — transition first.
		return &preSuspensionState{state: s}
	}
	// Phase-based dispatch.
	switch plan.Status.Phase {
	case "":
		return &lifecycleState{state: s}
	case hibernatorv1alpha1.PhaseActive, hibernatorv1alpha1.PhaseHibernated:
		return selectIdleHandler(s)
	case hibernatorv1alpha1.PhaseHibernating:
		return &hibernatingState{state: s}
	case hibernatorv1alpha1.PhaseWakingUp:
		return &wakingUpState{state: s}
	case hibernatorv1alpha1.PhaseSuspended:
		return &suspendedState{state: s}
	case hibernatorv1alpha1.PhaseError:
		return &recoveryState{state: s}
	default:
		return nil
	}
	// Wrap the phase-specific handler with the observation pipeline.
	// return wrapWithObservations(s, handler)
}

// selectIdleHandler resolves the concrete Handler for Active and Hibernated phases,
// applying annotation-driven overrides in priority order before falling back to the
// schedule-driven idleState.
//
// Priority chain (highest to lowest):
//
//  1. override-action=true  → overrideActionState: suppresses the schedule; direction
//     is taken from override-phase-target=hibernate|wakeup.
//
//  2. restart=true          → restartState: one-shot re-trigger of the last executor
//     operation, determined by .Status.CurrentOperation (not by any annotation value).
//
//  3. (default)             → idleState: pure schedule-driven evaluation.
func selectIdleHandler(s *state) Handler {
	plan := s.plan()
	idle := &idleState{state: s}

	if plan.Annotations[wellknown.AnnotationOverrideAction] == "true" {
		return &overrideActionState{idleState: idle}
	}

	if plan.Annotations[wellknown.AnnotationRestart] == "true" {
		return &restartState{idleState: idle}
	}

	return idle
}
