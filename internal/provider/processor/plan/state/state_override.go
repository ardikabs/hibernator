/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// overrideActionState handles manual phase override for Active and Hibernated plans.
// Selected by selectHandler when override-action=true is present on the plan,
// paired with override-phase-target=hibernate|wakeup to specify the direction.
//
// # Suppression behaviour
//
// While override-action=true is set, schedule-driven transitions are suppressed.
// Once at the target phase, the handler becomes a silent no-op on every subsequent
// tick — preventing loops without the controller removing the annotation.
// The user (or CI pipeline) removes override-action (and override-phase-target)
// when normal schedule control should resume.
//
// This avoids the race window that would arise from deleting the annotation and
// writing a phase in two separate API calls.
//
// # Restarting the same operation
//
// When already at the target phase and the user wants to re-run the executor
// (e.g. to re-apply a partial operation), they set the one-shot companion annotation:
//
//	kubectl annotate hibernateplan <name> hibernator.ardikabs.com/restart=true
//
// The controller consumes restart in a single atomic patch before re-executing.
// restart uses .Status.CurrentOperation as the source of truth, not override-phase-target.
//
// # Priority
//
// Spec.Suspend=true always wins (enforced by selectHandler before this handler).
//
// # Transition helpers
//
// Embeds *idleState to reuse transitionToHibernating and transitionToWakingUp.
type overrideActionState struct {
	*idleState
}

func (s *overrideActionState) Handle(ctx context.Context) (StateResult, error) {
	plan := s.PlanCtx.Plan
	target := plan.Annotations[wellknown.AnnotationOverridePhaseTarget]

	log := s.Log.
		WithName("override").
		WithValues(
			"plan", s.Key.String(),
			"phase", plan.Status.Phase,
			"overridePhaseTarget", target,
		)

	switch target {
	case wellknown.OverridePhaseTargetHibernate:
		switch plan.Status.Phase {
		case hibernatorv1alpha1.PhaseActive:
			log.Info("manual override: forcing hibernation, transitioning to Hibernating")
			return s.transitionToHibernating(log)

		case hibernatorv1alpha1.PhaseHibernated:
			if restart, err := s.consumeRestart(ctx, plan); err != nil {
				return StateResult{}, err
			} else if restart {
				log.Info("restart: re-triggering hibernation executor")
				return s.transitionToHibernating(log)
			}
			// Target already reached — stay quiet until the user removes the annotations.
			log.V(1).Info("manual override: plan is already Hibernated; " +
				"remove the annotations to restore schedule control (or set restart=true to re-run)")
		}

	case wellknown.OverridePhaseTargetWakeup:
		switch plan.Status.Phase {
		case hibernatorv1alpha1.PhaseHibernated:
			if s.PlanCtx.HasRestoreData {
				log.Info("manual override: forcing wakeup, transitioning to WakingUp")
				return s.transitionToWakingUp(log)
			}
			// No restore data — leave annotations so the user sees it is still pending.
			log.Info("manual override: wakeup requested but no restore data available — " +
				"the plan has not completed a hibernation cycle yet; " +
				"set override-phase-target=hibernate (or remove the annotations) to let the plan " +
				"hibernate first so restore data is captured, then retry override-phase-target=wakeup")

		case hibernatorv1alpha1.PhaseActive:
			// Target already reached — the plan is already awake.  Without an explicit restart
			// signal this is always a no-op, because acting on stale restore data would loop
			// forever (UnlockRestoreData removes annotations only, not ConfigMap data, so
			// HasRestoreData stays true after wakeup).
			restart, err := s.consumeRestart(ctx, plan)
			if err != nil {
				return StateResult{}, err
			}
			if restart {
				if s.PlanCtx.HasRestoreData {
					log.Info("restart: re-triggering wakeup executor")
					return s.transitionToWakingUp(log)
				}
				log.Info("restart: wakeup re-trigger requested but no restore data available; " +
					"the plan has not completed a hibernation cycle yet — " +
					"hibernate first so restore data is captured, then retry")
				return StateResult{}, nil
			}
			log.V(1).Info("manual override: plan is already Active; " +
				"remove the annotations to restore schedule control (or set restart=true to re-run)")
		}

	default:
		// Missing or unrecognised override-phase-target — leave annotations so user can correct.
		log.Info("missing or unrecognised override-phase-target value, ignoring; "+
			"remove the annotation or use a valid value",
			"validValues", wellknown.OverridePhaseTargetHibernate+"|"+wellknown.OverridePhaseTargetWakeup)
	}

	return StateResult{}, nil
}

// consumeRestart checks for the restart annotation. If set to "true", deletes it via
// a single atomic patch (no phase change → no two-step race) and returns (true, nil).
// On patch failure returns (false, err) — the annotation survives, preserving intent.
func (s *overrideActionState) consumeRestart(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) (bool, error) {
	if plan.Annotations[wellknown.AnnotationRestart] != "true" {
		return false, nil
	}
	orig := plan.DeepCopy()
	delete(plan.Annotations, wellknown.AnnotationRestart)
	if err := s.patchPreservingStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
		return false, fmt.Errorf("failed to consume %s annotation: %w", wellknown.AnnotationRestart, err)
	}
	return true, nil
}
