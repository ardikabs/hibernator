/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// Factory picks the appropriate Handler implementation based on the plan's current phase and state.
//
// Cross-cutting guards (deletion, suspension) are evaluated before phase dispatch, so callers never need to check them separately.
// Returns nil for unrecognised phases.
func Factory(state *State) Handler {
	plan := state.PlanCtx.Plan

	// Deletion in progress — run finalizer cleanup regardless of phase.
	if !plan.DeletionTimestamp.IsZero() {
		return &lifecycleState{State: state, delete: true}
	}

	// Suspension requested but not yet in PhaseSuspended — transition first.
	if plan.Spec.Suspend && plan.Status.Phase != hibernatorv1alpha1.PhaseSuspended {
		return HandlerFunc(func(ctx context.Context, onDeadline bool) {
			if err := state.TransitionToSuspended(ctx, onDeadline); err != nil {
				state.Log.Error(err, "failed to transition to Suspended")
			}
		})
	}

	switch plan.Status.Phase {
	case "":
		return &lifecycleState{State: state}
	case hibernatorv1alpha1.PhaseActive, hibernatorv1alpha1.PhaseHibernated:
		return &idleState{State: state}
	case hibernatorv1alpha1.PhaseHibernating:
		return &hibernatingState{State: state}
	case hibernatorv1alpha1.PhaseWakingUp:
		return &wakingUpState{State: state}
	case hibernatorv1alpha1.PhaseSuspended:
		return &suspendedState{State: state}
	case hibernatorv1alpha1.PhaseError:
		return &recoveryState{State: state}
	default:
		return nil
	}
}
