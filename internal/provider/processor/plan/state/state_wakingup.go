/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/message"
	"github.com/ardikabs/hibernator/internal/scheduler"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// wakingUpState drives stage-based Job execution for the wakeup operation.
type wakingUpState struct {
	*State
}

func (state *wakingUpState) Handle(ctx context.Context) {
	plan := state.plan()
	log := state.Log.
		WithName("wakingup").
		WithValues(
			"plan", state.Key.String(),
			"phase", plan.Status.Phase,
			"cycleID", plan.Status.CurrentCycleID,
			"stage", plan.Status.CurrentStageIndex)

	defer func() {
		if plan.Status.Phase == hibernatorv1alpha1.PhaseWakingUp {
			state.RequeueAfter(wellknown.RequeueIntervalDuringStage)
		} else {
			state.CancelRequeue()
		}
	}()

	if plan.Status.CurrentOperation != "wakeup" {
		log.V(1).Info("WakingUp but currentOperation != wakeup, skipping",
			"currentOperation", plan.Status.CurrentOperation)
		return
	}

	state.execute(ctx, log, "wakeup", true,
		func(ctx context.Context, err error) { state.setError(ctx, err) },
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

	summary := BuildOperationSummary(state.Clock, plan, "wakeup")
	currentCycleID := plan.Status.CurrentCycleID

	mutate := func(st *hibernatorv1alpha1.HibernatePlanStatus) {
		st.Phase = hibernatorv1alpha1.PhaseActive
		st.LastTransitionTime = ptr.To(metav1.NewTime(state.Clock.Now()))

		cycleIdx := findOrAppendCycle(st, currentCycleID)
		if st.ExecutionHistory[cycleIdx].WakeupExecution == nil {
			st.ExecutionHistory[cycleIdx].WakeupExecution = summary
		}
		pruneCycleHistory(st)

		st.RetryCount = 0
		st.LastRetryTime = nil
		st.ErrorMessage = ""
	}

	mutate(&plan.Status)
	state.Statuses.PlanStatuses.Send(&message.PlanStatusUpdate{
		NamespacedName: state.Key,
		Mutate:         mutate,
	})

	state.postWakeupCleanup(ctx, log, plan)
	state.dispatch(ctx)
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
		if err := state.patchPreservingStatus(ctx, plan, client.MergeFrom(orig)); err != nil {
			log.Error(err, "failed to remove suspended-at-phase annotation (non-fatal)")
		}
	}
}
