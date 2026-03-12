/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"fmt"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	statusprocessor "github.com/ardikabs/hibernator/internal/provider/processor/status"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// wakingUpState drives stage-based Job execution for the wakeup operation.
type wakingUpState struct {
	*state
}

func (state *wakingUpState) Handle(ctx context.Context) (StateResult, error) {
	plan := state.plan()
	log := state.Log.
		WithName("wakingup").
		WithValues(
			"plan", state.Key.String(),
			"phase", plan.Status.Phase,
			"cycleID", plan.Status.CurrentCycleID,
			"stage", plan.Status.CurrentStageIndex)

	if plan.Status.CurrentOperation != hibernatorv1alpha1.OperationWakeUp {
		log.V(1).Info("WakingUp but currentOperation != wakeup, skipping",
			"currentOperation", plan.Status.CurrentOperation)
		return StateResult{}, AsPlanError(fmt.Errorf("mismatch between phase and operation: phase=%s operation=%s", plan.Status.Phase, plan.Status.CurrentOperation))
	}

	return state.execute(ctx, log, hibernatorv1alpha1.OperationWakeUp, true,
		func(nextIdx int) { state.nextStage(nextIdx) },
		func(ctx context.Context, ep scheduler.ExecutionPlan) { state.finalize(ctx, log, ep) },
	)
}

func (state *wakingUpState) finalize(ctx context.Context, log logr.Logger, _ scheduler.ExecutionPlan) {
	plan := state.plan()

	if !IsOperationComplete(plan) {
		log.V(1).Info("targets still in progress, not completing wakeup yet")
		return
	}

	log.Info("all stages completed, finalizing wakeup operation")

	summary := BuildOperationSummary(state.Clock, plan, hibernatorv1alpha1.OperationWakeUp)
	currentCycleID := plan.Status.CurrentCycleID

	state.Statuses.PlanStatuses.Send(statusprocessor.Update[*hibernatorv1alpha1.HibernatePlan]{
		NamespacedName: state.Key,
		Resource:       plan,
		Mutator: statusprocessor.MutatorFunc[*hibernatorv1alpha1.HibernatePlan](func(p *hibernatorv1alpha1.HibernatePlan) {
			p.Status.Phase = hibernatorv1alpha1.PhaseActive
			p.Status.LastTransitionTime = ptr.To(metav1.NewTime(state.Clock.Now()))

			cycleIdx := findOrAppendCycle(&p.Status, currentCycleID)
			if p.Status.ExecutionHistory[cycleIdx].WakeupExecution == nil {
				p.Status.ExecutionHistory[cycleIdx].WakeupExecution = summary
			}
			pruneCycleHistory(&p.Status)

			p.Status.RetryCount = 0
			p.Status.LastRetryTime = nil
			p.Status.ErrorMessage = ""
		}),
	})

	state.postWakeupCleanup(ctx, log, plan)
}

func (state *wakingUpState) postWakeupCleanup(ctx context.Context, log logr.Logger, plan *hibernatorv1alpha1.HibernatePlan) {
	log.V(1).Info("starting post-wakeup cleanup")

	targetNames := make([]string, 0, len(plan.Spec.Targets))
	for _, t := range plan.Spec.Targets {
		targetNames = append(targetNames, t.Name)
	}

	restored, err := state.RestoreManager.MarkAllTargetsRestored(ctx, plan.Namespace, plan.Name, targetNames)
	if err != nil {
		log.Error(err, "failed to check restored targets (non-fatal)")
		return
	}
	if !restored {
		log.V(1).Info("not all targets restored yet, keeping restore data locked")
		return
	}

	log.Info("all targets restored, unlocking restore data")
	if err := state.RestoreManager.UnlockRestoreData(ctx, plan.Namespace, plan.Name); err != nil {
		log.Error(err, "failed to unlock restore data (non-fatal)")
		return
	}

	if _, ok := plan.Annotations[wellknown.AnnotationSuspendedAtPhase]; ok {
		orig := plan.DeepCopy()
		delete(plan.Annotations, wellknown.AnnotationSuspendedAtPhase)
		if err := state.patchAndPreserveStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
			log.Error(err, "failed to remove suspended-at-phase annotation (non-fatal)")
		}
	}
}
