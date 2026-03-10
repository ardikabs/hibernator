/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// forceActionState handles manual phase override for Active and Hibernated plans.
// It is selected by selectHandler when the annotation
// hibernator.ardikabs.com/force-action is present on the plan.
//
// # Loop prevention
//
// The annotation is intentionally NOT removed by the controller.  Instead, looping
// is prevented by a target-already-reached no-op: once the forced operation completes
// and the plan is back in the target idle phase, forceActionState simply does nothing
// on every subsequent tick.  The user (or CI pipeline) removes the annotation when
// they want the cron schedule to resume control.
//
// This avoids the race condition that would arise from deleting the annotation and
// writing a new phase in two separate API calls: in the window between them the
// reconciler could re-fetch the plan (no annotation, old phase) and let the schedule
// handler fire unexpectedly.
//
// # Priority
//
// Spec.Suspend=true always wins over this handler (enforced by selectHandler before
// forceActionState is ever returned).
//
// # Transition helpers
//
// The handler embeds *idleState to reuse transitionToHibernating and
// transitionToWakingUp without duplicating any logic.
type forceActionState struct {
	*idleState
}

func (s *forceActionState) Handle(ctx context.Context) (StateResult, error) {
	plan := s.PlanCtx.Plan
	action := plan.Annotations[wellknown.AnnotationForceAction]

	log := s.Log.
		WithName("force").
		WithValues(
			"plan", s.Key.String(),
			"phase", plan.Status.Phase,
			"forceAction", action,
		)

	switch action {
	case wellknown.ForceActionHibernate:
		switch plan.Status.Phase {
		case hibernatorv1alpha1.PhaseActive:
			log.Info("manual override: forcing hibernation, transitioning to Hibernating")
			return s.transitionToHibernating(log)

		case hibernatorv1alpha1.PhaseHibernated:
			// Target already reached — stay quiet until the user removes the annotation.
			log.V(1).Info("manual override: plan is already Hibernated; " +
				"remove the annotation to restore schedule control")
		}

	case wellknown.ForceActionWakeup:
		switch plan.Status.Phase {
		case hibernatorv1alpha1.PhaseHibernated:
			if s.PlanCtx.HasRestoreData {
				log.Info("manual override: forcing wakeup, transitioning to WakingUp")
				return s.transitionToWakingUp(log)
			}
			// No restore data — leave annotation in place so user sees it is still pending.
			log.Info("manual override: wakeup requested but no restore data available — " +
				"the plan has not completed a hibernation cycle yet; " +
				"set force-action=hibernate (or remove the annotation) to let the plan " +
				"hibernate first so restore data is captured, then retry force-action=wakeup")

		case hibernatorv1alpha1.PhaseActive:
			// Target already reached — the plan is already awake. No-op regardless of
			// whether restore data exists: acting on stale restore data would trigger a
			// redundant WakingUp cycle that loops forever (UnlockRestoreData only removes
			// annotations, not ConfigMap data, so HasRestoreData stays true after wakeup).
			log.V(1).Info("manual override: plan is already Active; " +
				"remove the annotation to restore schedule control")
		}

	default:
		// Unrecognised value — leave the annotation so the user can see it is pending.
		log.Info("unrecognised force-action value, ignoring; "+
			"remove the annotation or use a valid value",
			"validValues", wellknown.ForceActionHibernate+"|"+wellknown.ForceActionWakeup)
	}

	return StateResult{}, nil
}


