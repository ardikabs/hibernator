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

// restartState handles the standalone restart annotation (hibernator.ardikabs.com/restart=true)
// when no override-action is active. It re-triggers the last executor operation as recorded
// in .Status.CurrentOperation and is consumed atomically (one-shot).
//
// The phase must match the operation: Hibernated+hibernate or Active+wakeup. Mismatches
// are no-ops with a warning; the annotation is still consumed.
type restartState struct {
	*idleState
}

func (s *restartState) Handle(ctx context.Context) (StateResult, error) {
	plan := s.PlanCtx.Plan
	op := plan.Status.CurrentOperation

	log := s.Log.
		WithName("restart").
		WithValues(
			"plan", s.Key.String(),
			"phase", plan.Status.Phase,
			"currentOperation", op,
		)

	// Consume the annotation atomically before acting — one-shot regardless of outcome.
	orig := plan.DeepCopy()
	delete(plan.Annotations, wellknown.AnnotationRestart)
	if err := s.patchAndPreserveStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
		return StateResult{}, fmt.Errorf("failed to consume %s annotation: %w", wellknown.AnnotationRestart, err)
	}

	switch op {
	case hibernatorv1alpha1.OperationHibernate:
		if plan.Status.Phase != hibernatorv1alpha1.PhaseHibernated {
			log.Info("restart: CurrentOperation=hibernate but plan is not Hibernated; no-op",
				"hint", "plan must be in PhaseHibernated to restart a hibernation executor")
			return StateResult{}, nil
		}
		log.Info("restart: re-triggering hibernation executor based on CurrentOperation")
		return s.transitionToHibernating(log)

	case hibernatorv1alpha1.OperationWakeUp:
		if plan.Status.Phase != hibernatorv1alpha1.PhaseActive {
			log.Info("restart: CurrentOperation=wakeup but plan is not Active; no-op",
				"hint", "plan must be in PhaseActive to restart a wakeup executor")
			return StateResult{}, nil
		}
		if !s.PlanCtx.HasRestoreData {
			log.Info("restart: CurrentOperation=wakeup but no restore data available; no-op")
			return StateResult{}, nil
		}
		log.Info("restart: re-triggering wakeup executor based on CurrentOperation")
		return s.transitionToWakingUp(log)

	default:
		log.Info("restart: CurrentOperation is empty or unrecognised; no-op",
			"hint", "plan must have completed at least one hibernation cycle before restart applies")
		return StateResult{}, nil
	}
}

